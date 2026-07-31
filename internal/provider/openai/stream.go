package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/MichiTrader/ishakat/internal/convo"
	"github.com/MichiTrader/ishakat/internal/provider"
)

// eventBuffer es el tamaño del canal. No es una optimización caprichosa: el
// lector de la TUI drena cada 50 ms (§7.3) y en ese tiempo un modelo rápido
// manda del orden de decenas de chunks. Con el canal sin búfer, la goroutine
// del socket se bloquearía en cada token y el TCP se llenaría de esperas.
const eventBuffer = 64

// Stream ejecuta un turno.
//
// El handshake es síncrono a propósito: la petición se manda y se comprueba el
// estado HTTP antes de devolver el canal. Así un 429 o un 401 vuelven como
// error normal —que el engine puede reintentar o explicar sin haber pintado
// nada en pantalla— y el canal solo existe cuando ya hay un stream vivo del
// que van a salir tokens.
//
// Garantías del canal (las mismas que promete provider.Event):
// EventDone es siempre el último evento, un EventError lo precede si algo
// falló, y el canal se cierra al terminar, incluso si se canceló el contexto.
func (p *Provider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	if req.Model == "" {
		return nil, fmt.Errorf("openai: el turno no trae modelo (%s)", p.set.ID)
	}

	msgs, deg := FromConvo(prepend(req.System, req.Messages), req.Caps)
	if len(msgs) == 0 {
		return nil, fmt.Errorf("openai: el turno no tiene ningún mensaje con contenido")
	}

	body, err := p.buildBody(req, msgs)
	if err != nil {
		return nil, err
	}

	httpReq, err := p.newRequest(ctx, http.MethodPost, "/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if req.Stream {
		httpReq.Header.Set("Accept", "text/event-stream")
		// Sin esto, un proxy intermedio puede decidir bufferizar la respuesta
		// completa y el streaming se pierde sin dar ningún error.
		httpReq.Header.Set("Cache-Control", "no-cache")
	}

	resp, err := p.hc.Do(httpReq)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, p.netError(err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, p.httpError(resp)
	}

	ch := make(chan provider.Event, eventBuffer)
	go func() {
		defer close(ch)
		defer resp.Body.Close()

		if deg.Any() {
			if !emit(ctx, ch, provider.Event{Kind: provider.EventWarning, Text: deg.Reason()}) {
				return
			}
		}

		var streamErr error
		if req.Stream {
			streamErr = p.pumpSSE(ctx, resp.Body, ch)
		} else {
			streamErr = p.pumpWhole(ctx, resp.Body, ch)
		}

		// Una cancelación del usuario no es un error: §7.4 dice que el parcial
		// se guarda como turno abortado, y de eso se encarga el engine al ver
		// que él mismo canceló. Reportarlo como fallo pondría un mensaje rojo
		// cada vez que alguien pulsa esc.
		if streamErr != nil && ctx.Err() == nil {
			emit(ctx, ch, provider.Event{Kind: provider.EventError, Err: streamErr})
		}
		emit(ctx, ch, provider.Event{Kind: provider.EventDone})
	}()

	return ch, nil
}

// pumpSSE lee el flujo de eventos y lo traduce a provider.Event.
func (p *Provider) pumpSSE(ctx context.Context, body io.Reader, ch chan<- provider.Event) error {
	sc := newSSEScanner(body)
	tools := newToolAccumulator()
	sawDone := false

	for {
		ev, err := sc.Next()
		if err != nil {
			switch {
			case errors.Is(err, io.EOF):
				// Fin de flujo. Sin [DONE] previo es un corte: la mayoría de
				// los servicios lo mandan siempre, y su ausencia es la señal
				// más fiable de que la respuesta quedó a medias.
				tools.flush(ctx, ch)
				if !sawDone {
					return provider.ErrStreamTruncated
				}
				return nil
			case errors.Is(err, errIncompleteEvent):
				tools.flush(ctx, ch)
				return provider.ErrStreamTruncated
			default:
				tools.flush(ctx, ch)
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return fmt.Errorf("openai: error leyendo el stream: %w", err)
			}
		}

		// Eventos con nombre propio que no son datos de chat: los gateways
		// mandan "ping" para mantener la conexión.
		if ev.Name != "" && ev.Name != "message" && ev.Name != "chunk" {
			if ev.Name == "error" {
				if msg := errorMessage(ev.Data); msg != "" {
					return fmt.Errorf("openai: %s", msg)
				}
			}
			continue
		}

		data := bytes.TrimSpace(ev.Data)
		if len(data) == 0 {
			continue
		}
		if bytes.Equal(data, []byte("[DONE]")) {
			// Todo lo que venga después de [DONE] se ignora: el protocolo
			// termina aquí.
			tools.flush(ctx, ch)
			return nil
		}

		var chunk wireChunk
		if err := json.Unmarshal(data, &chunk); err != nil {
			// Un chunk ilegible no justifica tirar el turno entero: puede ser
			// un keep-alive con forma rara. Se salta y se sigue.
			continue
		}
		if chunk.Error != nil && chunk.Error.Message != "" {
			// Error con estado 200 en medio del stream: el peor formato
			// posible, y el que más veces deja al usuario mirando una
			// respuesta vacía sin explicación.
			tools.flush(ctx, ch)
			return &provider.Error{
				Provider:  p.set.ID,
				Code:      codeString(chunk.Error),
				Message:   chunk.Error.Message,
				Retryable: false,
			}
		}

		if chunk.Usage != nil {
			if u := toUsage(chunk.Usage); u != nil {
				if !emit(ctx, ch, provider.Event{Kind: provider.EventUsage, Usage: u}) {
					return ctx.Err()
				}
			}
		}
		if len(chunk.Choices) == 0 {
			continue
		}

		d := chunk.Choices[0].Delta
		if r := reasoningText(d); r != "" {
			if !emit(ctx, ch, provider.Event{Kind: provider.EventReasoning, Text: r}) {
				return ctx.Err()
			}
		}
		if d.Content != "" {
			if !emit(ctx, ch, provider.Event{Kind: provider.EventDelta, Text: d.Content}) {
				return ctx.Err()
			}
		}
		if d.Refusal != "" {
			if !emit(ctx, ch, provider.Event{Kind: provider.EventDelta, Text: d.Refusal}) {
				return ctx.Err()
			}
		}
		for _, tc := range d.ToolCalls {
			tools.add(tc)
		}

		if fr := chunk.Choices[0].FinishReason; fr != nil && *fr != "" {
			tools.flush(ctx, ch)
			sawDone = true
			// No se corta el bucle: el usage suele llegar en el chunk
			// siguiente al del finish_reason, y perderlo dejaría el footer
			// sin contador real.
		}
	}
}

// pumpWhole atiende el caso app.stream = false: una sola respuesta JSON. El
// canal de eventos es el mismo, así que ni el engine ni la TUI necesitan saber
// en qué modo se pidió el turno.
func (p *Provider) pumpWhole(ctx context.Context, body io.Reader, ch chan<- provider.Event) error {
	const maxWhole = 32 << 20
	raw, err := io.ReadAll(io.LimitReader(body, maxWhole))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("openai: error leyendo la respuesta: %w", err)
	}

	var chunk wireChunk
	if err := json.Unmarshal(raw, &chunk); err != nil {
		return fmt.Errorf("openai: respuesta ilegible: %w", err)
	}
	if chunk.Error != nil && chunk.Error.Message != "" {
		return &provider.Error{
			Provider: p.set.ID,
			Code:     codeString(chunk.Error),
			Message:  chunk.Error.Message,
		}
	}
	if len(chunk.Choices) == 0 {
		return errors.New("openai: la respuesta no trae ninguna alternativa")
	}

	msg := chunk.Choices[0].Message
	if msg == nil {
		// Algunos servicios devuelven el mensaje en "delta" incluso sin
		// streaming.
		msg = &chunk.Choices[0].Delta
	}

	if r := reasoningText(*msg); r != "" {
		if !emit(ctx, ch, provider.Event{Kind: provider.EventReasoning, Text: r}) {
			return ctx.Err()
		}
	}
	if msg.Content != "" {
		if !emit(ctx, ch, provider.Event{Kind: provider.EventDelta, Text: msg.Content}) {
			return ctx.Err()
		}
	}
	for _, tc := range msg.ToolCalls {
		args := tc.Function.Arguments
		if args == "" {
			args = "{}"
		}
		if !emit(ctx, ch, provider.Event{
			Kind: provider.EventToolCall,
			Name: tc.Function.Name,
			Args: json.RawMessage(args),
		}) {
			return ctx.Err()
		}
	}
	if u := toUsage(chunk.Usage); u != nil {
		if !emit(ctx, ch, provider.Event{Kind: provider.EventUsage, Usage: u}) {
			return ctx.Err()
		}
	}
	return nil
}

// emit manda un evento sin arriesgar una goroutine colgada: si el consumidor
// se fue (contexto cancelado), se abandona en vez de bloquear para siempre en
// un canal que nadie lee.
func emit(ctx context.Context, ch chan<- provider.Event, ev provider.Event) bool {
	select {
	case ch <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}

// prepend inserta el prompt de sistema al principio del historial si viene
// aparte. Si el historial ya trae uno propio, este lo precede: el de la
// configuración es el marco, el del historial puede ser un ajuste de sesión.
func prepend(system string, msgs []convo.Message) []convo.Message {
	if strings.TrimSpace(system) == "" {
		return msgs
	}
	out := make([]convo.Message, 0, len(msgs)+1)
	out = append(out, convo.System(system))
	return append(out, msgs...)
}

// reasoningText extrae el razonamiento de las tres formas en que llega:
// reasoning_content (DeepSeek), reasoning como cadena (OpenRouter), o
// reasoning como objeto con text/content dentro.
func reasoningText(d wireDelta) string {
	if d.ReasoningContent != "" {
		return d.ReasoningContent
	}
	if len(d.Reasoning) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(d.Reasoning, &s); err == nil {
		return s
	}
	var obj struct {
		Text    string `json:"text"`
		Content string `json:"content"`
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal(d.Reasoning, &obj); err == nil {
		switch {
		case obj.Text != "":
			return obj.Text
		case obj.Content != "":
			return obj.Content
		case obj.Summary != "":
			return obj.Summary
		}
	}
	return ""
}

// errorMessage saca el mensaje de un evento SSE con nombre "error".
func errorMessage(data []byte) string {
	var env struct {
		Error   *wireError `json:"error"`
		Message string     `json:"message"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(data), &env); err == nil {
		if env.Error != nil && env.Error.Message != "" {
			return env.Error.Message
		}
		if env.Message != "" {
			return env.Message
		}
	}
	return firstLine(string(data), 200)
}

// toolAccumulator rearma las llamadas a herramientas, que llegan partidas en
// muchos chunks: primero el nombre, después los argumentos carácter a carácter.
//
// Las herramientas son post-1.0 (§18), pero acumularlas cuesta treinta líneas y
// evita que un modelo que decide llamar a una herramienta produzca un turno
// vacío sin ninguna pista de lo que pasó.
type toolAccumulator struct {
	byIndex map[int]*toolAcc
}

type toolAcc struct {
	name string
	args strings.Builder
}

func newToolAccumulator() *toolAccumulator {
	return &toolAccumulator{byIndex: map[int]*toolAcc{}}
}

func (t *toolAccumulator) add(tc wireToolCall) {
	acc, ok := t.byIndex[tc.Index]
	if !ok {
		acc = &toolAcc{}
		t.byIndex[tc.Index] = acc
	}
	if tc.Function.Name != "" {
		acc.name = tc.Function.Name
	}
	acc.args.WriteString(tc.Function.Arguments)
}

func (t *toolAccumulator) flush(ctx context.Context, ch chan<- provider.Event) {
	if len(t.byIndex) == 0 {
		return
	}
	idx := make([]int, 0, len(t.byIndex))
	for i := range t.byIndex {
		idx = append(idx, i)
	}
	sort.Ints(idx)

	for _, i := range idx {
		acc := t.byIndex[i]
		args := strings.TrimSpace(acc.args.String())
		if args == "" {
			args = "{}"
		}
		if !emit(ctx, ch, provider.Event{
			Kind: provider.EventToolCall,
			Name: acc.name,
			Args: json.RawMessage(args),
		}) {
			return
		}
	}
	t.byIndex = map[int]*toolAcc{}
}

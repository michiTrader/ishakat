package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/MichiTrader/ishakat/internal/provider"
)

// eventBuffer es el tamaño del canal, copiado del mismo razonamiento que
// openai/stream.go documenta: el lector de la TUI drena cada 50 ms (§7.3) y
// en ese tiempo un modelo rápido manda decenas de chunks.
const eventBuffer = 64

// Stream ejecuta un turno contra POST /v1/messages.
//
// El handshake es síncrono, igual que en el dialecto OpenAI: la petición se
// manda y se comprueba el estado HTTP antes de devolver el canal, así un 429
// o un 401 vuelven como error normal en vez de como un canal que abre y
// cierra sin haber pintado nada.
func (p *Provider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	if req.Model == "" {
		return nil, fmt.Errorf("anthropic: el turno no trae modelo (%s)", p.set.ID)
	}

	// A diferencia de openai.Stream (que antepone el system como un mensaje
	// más y deja que FromConvo lo trate igual que cualquier otro), aquí el
	// prompt de sistema tiene que llegar como el campo `system` de nivel
	// superior del request, nunca como un mensaje: FromConvo ya extrae el
	// que trae el historial (poco común, pero convo lo permite) y aquí se
	// combina con el de la configuración, que actúa de marco y va primero.
	msgs, sysFromHistory, deg := FromConvo(req.Messages, req.Caps)
	system := combineSystem(req.System, sysFromHistory)
	if len(msgs) == 0 {
		return nil, fmt.Errorf("anthropic: el turno no tiene ningún mensaje con contenido")
	}

	body, err := p.buildBody(req, msgs, system)
	if err != nil {
		return nil, err
	}

	httpReq, err := p.newRequest(ctx, http.MethodPost, "/messages", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if req.Stream {
		httpReq.Header.Set("Accept", "text/event-stream")
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

		// Una cancelación del usuario no es un error, igual que en el
		// dialecto OpenAI (§7.4: el parcial se guarda como turno abortado).
		if streamErr != nil && ctx.Err() == nil {
			emit(ctx, ch, provider.Event{Kind: provider.EventError, Err: streamErr})
		}
		emit(ctx, ch, provider.Event{Kind: provider.EventDone})
	}()

	return ch, nil
}

// combineSystem junta el system de la configuración con el que (poco común,
// pero posible) trae el propio historial. El de la configuración es el
// marco y va primero, igual que prepend hace en el dialecto OpenAI.
func combineSystem(configured, fromHistory string) string {
	configured = strings.TrimSpace(configured)
	fromHistory = strings.TrimSpace(fromHistory)
	switch {
	case configured == "":
		return fromHistory
	case fromHistory == "":
		return configured
	default:
		return configured + "\n\n" + fromHistory
	}
}

// blockState rastrea, por índice de bloque, qué se está acumulando: solo
// hace falta para tool_use (el JSON de sus argumentos llega fragmentado en
// varios content_block_delta); un bloque de texto no necesita estado propio,
// cada text_delta se emite en el momento.
type blockState struct {
	kind string // "tool_use" | otro (se ignora)
	id   string
	name string
	args strings.Builder
}

// pumpSSE lee el flujo de eventos de la Messages API y lo traduce a
// provider.Event.
//
// A diferencia del dialecto OpenAI, aquí el "type" que decide qué hacer con
// cada evento viene dos veces —en la línea `event:` del SSE y de nuevo
// dentro del propio JSON de `data`— y se usa el del JSON como canónico: es
// el que la documentación describe campo a campo, y coincide siempre con el
// de la línea `event:` salvo que el gateway intermedio los desincronice, en
// cuyo caso el del cuerpo es la fuente de verdad real.
func (p *Provider) pumpSSE(ctx context.Context, body io.Reader, ch chan<- provider.Event) error {
	sc := newSSEScanner(body)
	blocks := map[int]*blockState{}
	var usage wireUsage
	haveUsage := false
	sawStop := false

	flushToolBlock := func(idx int) bool {
		st, ok := blocks[idx]
		delete(blocks, idx)
		if !ok || st.kind != "tool_use" {
			return true
		}
		args := strings.TrimSpace(st.args.String())
		if args == "" {
			args = "{}"
		}
		return emit(ctx, ch, provider.Event{
			Kind: provider.EventToolCall,
			ID:   st.id,
			Name: st.name,
			Args: json.RawMessage(args),
		})
	}

	for {
		ev, err := sc.Next()
		if err != nil {
			switch {
			case errors.Is(err, io.EOF):
				// Fin de flujo sin message_stop previo: la señal más fiable
				// de que la respuesta quedó a medias, igual que la ausencia
				// de [DONE] en el dialecto OpenAI.
				if !sawStop {
					return provider.ErrStreamTruncated
				}
				return nil
			case errors.Is(err, errIncompleteEvent):
				return provider.ErrStreamTruncated
			default:
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return fmt.Errorf("anthropic: error leyendo el stream: %w", err)
			}
		}

		data := bytes.TrimSpace(ev.Data)
		if len(data) == 0 {
			continue
		}

		var evt wireStreamEvent
		if err := json.Unmarshal(data, &evt); err != nil {
			// Un evento ilegible no justifica tirar el turno entero: puede
			// ser un keep-alive con forma rara.
			continue
		}

		switch evt.Type {
		case "ping":
			// Solo mantiene viva la conexión.

		case "message_start":
			if evt.Message != nil && evt.Message.Usage != nil {
				usage = *evt.Message.Usage
				haveUsage = true
			}

		case "content_block_start":
			if evt.ContentBlock == nil {
				continue
			}
			st := &blockState{kind: evt.ContentBlock.Type, id: evt.ContentBlock.ID, name: evt.ContentBlock.Name}
			blocks[evt.Index] = st

		case "content_block_delta":
			if evt.Delta == nil {
				continue
			}
			switch evt.Delta.Type {
			case "text_delta":
				if evt.Delta.Text != "" {
					if !emit(ctx, ch, provider.Event{Kind: provider.EventDelta, Text: evt.Delta.Text}) {
						return ctx.Err()
					}
				}
			case "input_json_delta":
				if st, ok := blocks[evt.Index]; ok {
					st.args.WriteString(evt.Delta.PartialJSON)
				}
			case "thinking_delta", "signature_delta":
				// Pensamiento extendido: este adaptador nunca lo pide (ver
				// serialize.go), así que estos deltas no deberían llegar
				// nunca en la práctica; se ignoran en vez de fallar si un
				// día llegan de todos modos.
			}

		case "content_block_stop":
			if !flushToolBlock(evt.Index) {
				return ctx.Err()
			}

		case "message_delta":
			// El usage de message_delta es *acumulado*, no un incremento
			// (ver el comentario de wireUsage): se reemplaza sin sumar.
			if evt.Usage != nil {
				merged := usage
				if evt.Usage.OutputTokens > 0 {
					merged.OutputTokens = evt.Usage.OutputTokens
				}
				if evt.Usage.InputTokens > 0 {
					merged.InputTokens = evt.Usage.InputTokens
				}
				if evt.Usage.CacheCreationInputTokens > 0 {
					merged.CacheCreationInputTokens = evt.Usage.CacheCreationInputTokens
				}
				if evt.Usage.CacheReadInputTokens > 0 {
					merged.CacheReadInputTokens = evt.Usage.CacheReadInputTokens
				}
				usage = merged
				haveUsage = true
			}

		case "message_stop":
			sawStop = true
			if haveUsage {
				if u := toUsage(&usage); u != nil {
					if !emit(ctx, ch, provider.Event{Kind: provider.EventUsage, Usage: u}) {
						return ctx.Err()
					}
				}
			}
			return nil

		case "error":
			// Error de estado 200 en medio del stream (p.ej. overloaded_error
			// tras haber empezado a mandar tokens): el sobre es siempre un
			// objeto, igual que el de una respuesta HTTP no-200 (ver
			// httpError en anthropic.go).
			if evt.Error != nil && evt.Error.Message != "" {
				return &provider.Error{
					Provider: p.set.ID,
					Code:     evt.Error.Type,
					Message:  evt.Error.Message,
				}
			}
			return fmt.Errorf("anthropic: el servicio mandó un evento de error sin mensaje")
		}
	}
}

// pumpWhole atiende el caso stream = false: una sola respuesta JSON con el
// Message completo, sin envoltura de streaming.
func (p *Provider) pumpWhole(ctx context.Context, body io.Reader, ch chan<- provider.Event) error {
	const maxWhole = 32 << 20
	raw, err := io.ReadAll(io.LimitReader(body, maxWhole))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("anthropic: error leyendo la respuesta: %w", err)
	}

	var msg wireMessageResponse
	if err := json.Unmarshal(raw, &msg); err != nil {
		return fmt.Errorf("anthropic: respuesta ilegible: %w", err)
	}

	for _, blk := range msg.Content {
		switch blk.Type {
		case "text":
			if blk.Text != "" {
				if !emit(ctx, ch, provider.Event{Kind: provider.EventDelta, Text: blk.Text}) {
					return ctx.Err()
				}
			}
		case "tool_use":
			args := blk.Input
			if len(args) == 0 {
				args = json.RawMessage("{}")
			}
			if !emit(ctx, ch, provider.Event{
				Kind: provider.EventToolCall,
				ID:   blk.ID,
				Name: blk.Name,
				Args: args,
			}) {
				return ctx.Err()
			}
		}
	}

	if u := toUsage(msg.Usage); u != nil {
		if !emit(ctx, ch, provider.Event{Kind: provider.EventUsage, Usage: u}) {
			return ctx.Err()
		}
	}
	return nil
}

// emit manda un evento sin arriesgar una goroutine colgada: si el
// consumidor se fue (contexto cancelado), se abandona en vez de bloquear
// para siempre en un canal que nadie lee.
func emit(ctx context.Context, ch chan<- provider.Event, ev provider.Event) bool {
	select {
	case ch <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}

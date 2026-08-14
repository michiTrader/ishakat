package gemini

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
// anthropic/stream.go y openai/stream.go ya documentan.
const eventBuffer = 64

// Stream ejecuta un turno. A diferencia de los otros dos dialectos, la
// elección entre streaming y respuesta única no es un campo del cuerpo sino
// el endpoint que se llama (ver el comentario de wireRequest en wire.go):
// generateContent para req.Stream == false, streamGenerateContent?alt=sse
// para req.Stream == true. alt=sse es obligatorio: sin él el endpoint de
// streaming responde un único array JSON, no SSE.
func (p *Provider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	if req.Model == "" {
		return nil, fmt.Errorf("gemini: el turno no trae modelo (%s)", p.set.ID)
	}

	contents, sysFromHistory, deg := FromConvo(req.Messages, req.Caps)
	system := combineSystem(req.System, sysFromHistory)
	if len(contents) == 0 {
		return nil, fmt.Errorf("gemini: el turno no tiene ningún mensaje con contenido")
	}

	body, err := p.buildBody(req, contents, system)
	if err != nil {
		return nil, err
	}

	path := "/models/" + req.Model + ":generateContent"
	if req.Stream {
		path = "/models/" + req.Model + ":streamGenerateContent?alt=sse"
	}

	httpReq, err := p.newRequest(ctx, http.MethodPost, path, bytes.NewReader(body))
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

		if streamErr != nil && ctx.Err() == nil {
			emit(ctx, ch, provider.Event{Kind: provider.EventError, Err: streamErr})
		}
		emit(ctx, ch, provider.Event{Kind: provider.EventDone})
	}()

	return ch, nil
}

// combineSystem junta el system de la configuración con el que (poco común,
// pero posible) trae el propio historial. El de la configuración es el
// marco y va primero, igual que en el dialecto de Anthropic.
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

// pumpSSE lee el flujo de streamGenerateContent?alt=sse y lo traduce a
// provider.Event.
//
// Cada evento `data:` es un wireGenerateContentResponse COMPLETO, no un
// delta con forma propia (ver el comentario de wireGenerateContentResponse
// en wire.go) — pero el texto de cada Part.text dentro de ese objeto SÍ es
// incremental, no acumulado: es la forma en que la práctica real y todos
// los SDKs oficiales (Python, JS, Go) consumen este endpoint, tratando cada
// evento como "lo nuevo desde el evento anterior", igual que cualquier otro
// dialecto de streaming de texto. Esta es la hipótesis de trabajo adoptada
// tras no encontrar una única cita autorizada que lo confirme sin ambigüedad
// (ver la Bitácora); si algún día se demuestra que es acumulado en cambio,
// el arreglo es de una sola línea aquí, y el test con fixture de
// testdata/ que fija este comportamiento hace que ese día el cambio sea
// obvio en vez de silencioso.
func (p *Provider) pumpSSE(ctx context.Context, body io.Reader, ch chan<- provider.Event) error {
	sc := newSSEScanner(body)
	var usage wireUsageMetadata
	haveUsage := false
	sawCandidate := false

	for {
		ev, err := sc.Next()
		if err != nil {
			switch {
			case errors.Is(err, io.EOF):
				if !sawCandidate {
					return provider.ErrStreamTruncated
				}
				if haveUsage {
					emit(ctx, ch, provider.Event{Kind: provider.EventUsage, Usage: toUsage(&usage)})
				}
				return nil
			case errors.Is(err, errIncompleteEvent):
				return provider.ErrStreamTruncated
			default:
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return fmt.Errorf("gemini: error leyendo el stream: %w", err)
			}
		}

		data := bytes.TrimSpace(ev.Data)
		if len(data) == 0 {
			continue
		}

		var chunk wireGenerateContentResponse
		if err := json.Unmarshal(data, &chunk); err != nil {
			// Un evento ilegible no justifica tirar el turno entero: puede
			// ser un keep-alive con forma rara.
			continue
		}

		if chunk.PromptFeedback != nil && chunk.PromptFeedback.BlockReason != "" && len(chunk.Candidates) == 0 {
			// El prompt en sí fue bloqueado, no la respuesta: sin
			// candidatos no hay nada que emitir como texto, así que esto
			// se traduce directamente a un error de turno.
			return &provider.Error{
				Provider: p.set.ID,
				Code:     "PROMPT_BLOCKED",
				Message:  "prompt bloqueado por el servicio: " + chunk.PromptFeedback.BlockReason,
			}
		}

		if chunk.UsageMetadata != nil {
			usage = *chunk.UsageMetadata
			haveUsage = true
		}

		for _, cand := range chunk.Candidates {
			sawCandidate = true
			for _, part := range cand.Content.Parts {
				if !emitPart(ctx, ch, part) {
					return ctx.Err()
				}
			}
		}
	}
}

// pumpWhole atiende el caso stream = false: una sola respuesta JSON con el
// GenerateContentResponse completo, sin envoltura de streaming.
func (p *Provider) pumpWhole(ctx context.Context, body io.Reader, ch chan<- provider.Event) error {
	const maxWhole = 32 << 20
	raw, err := io.ReadAll(io.LimitReader(body, maxWhole))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("gemini: error leyendo la respuesta: %w", err)
	}

	var resp wireGenerateContentResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return fmt.Errorf("gemini: respuesta ilegible: %w", err)
	}

	if resp.PromptFeedback != nil && resp.PromptFeedback.BlockReason != "" && len(resp.Candidates) == 0 {
		return &provider.Error{
			Provider: p.set.ID,
			Code:     "PROMPT_BLOCKED",
			Message:  "prompt bloqueado por el servicio: " + resp.PromptFeedback.BlockReason,
		}
	}

	for _, cand := range resp.Candidates {
		for _, part := range cand.Content.Parts {
			if !emitPart(ctx, ch, part) {
				return ctx.Err()
			}
		}
	}

	if u := toUsage(resp.UsageMetadata); u != nil {
		if !emit(ctx, ch, provider.Event{Kind: provider.EventUsage, Usage: u}) {
			return ctx.Err()
		}
	}
	return nil
}

// emitPart traduce un único Part a cero o un Event, según cuál de sus
// campos union esté presente. Devuelve false si el consumidor se fue.
func emitPart(ctx context.Context, ch chan<- provider.Event, part wirePart) bool {
	switch {
	case part.FunctionCall != nil:
		args := part.FunctionCall.Args
		if len(args) == 0 {
			args = json.RawMessage("{}")
		}
		return emit(ctx, ch, provider.Event{
			Kind:      provider.EventToolCall,
			ID:        part.FunctionCall.ID,
			Name:      part.FunctionCall.Name,
			Args:      args,
			Signature: part.ThoughtSignature,
		})
	case part.Thought:
		if part.Text == "" {
			return true
		}
		return emit(ctx, ch, provider.Event{Kind: provider.EventReasoning, Text: part.Text})
	case part.Text != "":
		return emit(ctx, ch, provider.Event{Kind: provider.EventDelta, Text: part.Text})
	default:
		return true
	}
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

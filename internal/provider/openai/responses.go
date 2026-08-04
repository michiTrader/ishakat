package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/MichiTrader/ishakat/internal/convo"
	"github.com/MichiTrader/ishakat/internal/provider"
)

// responsesRequest is the small common subset accepted by the OpenAI
// Responses API and Codex-compatible gateways such as Aerolink.
type responsesRequest struct {
	Model           string         `json:"model"`
	Input           []ChatMessage  `json:"input"`
	Stream          bool           `json:"stream"`
	Store           bool           `json:"store"`
	MaxOutputTokens *int           `json:"max_output_tokens,omitempty"`
}

// responseUsage uses the field names from the Responses API.
type responseUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type responseOutputContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type responseOutputItem struct {
	Type    string                  `json:"type"`
	Content []responseOutputContent `json:"content,omitempty"`
}

type responseDocument struct {
	Output []responseOutputItem `json:"output,omitempty"`
	Usage  *responseUsage        `json:"usage,omitempty"`
	Error  *wireError            `json:"error,omitempty"`
}

type responseEvent struct {
	Type     string            `json:"type"`
	Delta    string            `json:"delta,omitempty"`
	Response *responseDocument `json:"response,omitempty"`
	Error    *wireError        `json:"error,omitempty"`
}

func (p *Provider) streamResponses(ctx context.Context, req provider.Request, msgs []ChatMessage, deg provider.Degradation) (<-chan provider.Event, error) {
	body := responsesRequest{
		Model:  req.Model,
		Input:  msgs,
		Stream: req.Stream,
		Store:  false,
	}
	if req.MaxTokens != nil {
		body.MaxOutputTokens = req.MaxTokens
	}

	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("responses: could not encode request: %w", err)
	}
	var overrides map[string]any
	if len(p.set.Params) > 0 {
		if err := json.Unmarshal(raw, &overrides); err != nil {
			return nil, fmt.Errorf("responses: could not prepare request: %w", err)
		}
		for k, v := range p.set.Params {
			applyParam(overrides, k, v)
		}
		raw, err = json.Marshal(overrides)
		if err != nil {
			return nil, fmt.Errorf("responses: could not encode parameters: %w", err)
		}
	}
	if len(req.Params) > 0 {
		var merged map[string]any
		if err := json.Unmarshal(raw, &merged); err != nil {
			return nil, fmt.Errorf("responses: could not prepare turn parameters: %w", err)
		}
		for k, v := range req.Params {
			applyParam(merged, k, v)
		}
		raw, err = json.Marshal(merged)
		if err != nil {
			return nil, fmt.Errorf("responses: could not encode turn parameters: %w", err)
		}
	}

	httpReq, err := p.newRequest(ctx, http.MethodPost, "/responses", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	if req.Stream {
		httpReq.Header.Set("Accept", "text/event-stream")
		httpReq.Header.Set("Cache-Control", "no-cache")
	} else {
		httpReq.Header.Set("Accept", "application/json")
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

		if deg.Any() && !emit(ctx, ch, provider.Event{Kind: provider.EventWarning, Text: deg.Reason()}) {
			return
		}
		var streamErr error
		if req.Stream {
			streamErr = p.pumpResponsesSSE(ctx, resp.Body, ch)
		} else {
			streamErr = p.pumpResponsesWhole(ctx, resp.Body, ch)
		}
		if streamErr != nil && ctx.Err() == nil {
			emit(ctx, ch, provider.Event{Kind: provider.EventError, Err: streamErr})
		}
		emit(ctx, ch, provider.Event{Kind: provider.EventDone})
	}()
	return ch, nil
}

func (p *Provider) pumpResponsesSSE(ctx context.Context, body io.Reader, ch chan<- provider.Event) error {
	sc := newSSEScanner(body)
	for {
		ev, err := sc.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return provider.ErrStreamTruncated
			}
			if errors.Is(err, errIncompleteEvent) {
				return provider.ErrStreamTruncated
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("responses: error reading stream: %w", err)
		}
		data := bytes.TrimSpace(ev.Data)
		if len(data) == 0 {
			continue
		}
		var event responseEvent
		if err := json.Unmarshal(data, &event); err != nil {
			continue
		}
		if event.Type == "response.output_text.delta" && event.Delta != "" {
			if !emit(ctx, ch, provider.Event{Kind: provider.EventDelta, Text: event.Delta}) {
				return ctx.Err()
			}
			continue
		}
		if strings.HasPrefix(event.Type, "response.reasoning") && event.Delta != "" {
			if !emit(ctx, ch, provider.Event{Kind: provider.EventReasoning, Text: event.Delta}) {
				return ctx.Err()
			}
			continue
		}
		if event.Type == "error" || event.Type == "response.failed" {
			if event.Error != nil && event.Error.Message != "" {
				return &provider.Error{Provider: p.set.ID, Message: event.Error.Message, Code: codeString(event.Error)}
			}
			return errors.New("responses: the provider reported a failed response")
		}
		if event.Type == "response.completed" {
			if event.Response != nil && event.Response.Usage != nil {
				if !emit(ctx, ch, provider.Event{Kind: provider.EventUsage, Usage: &convo.Usage{
					In: event.Response.Usage.InputTokens, Out: event.Response.Usage.OutputTokens,
				}}) {
					return ctx.Err()
				}
			}
			return nil
		}
	}
}

func (p *Provider) pumpResponsesWhole(ctx context.Context, body io.Reader, ch chan<- provider.Event) error {
	const maxWhole = 32 << 20
	raw, err := io.ReadAll(io.LimitReader(body, maxWhole))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("responses: error reading response: %w", err)
	}
	var doc responseDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("responses: unreadable response: %w", err)
	}
	if doc.Error != nil && doc.Error.Message != "" {
		return &provider.Error{Provider: p.set.ID, Message: doc.Error.Message, Code: codeString(doc.Error)}
	}
	for _, item := range doc.Output {
		for _, content := range item.Content {
			if content.Text == "" {
				continue
			}
			kind := provider.EventDelta
			if strings.Contains(content.Type, "reasoning") {
				kind = provider.EventReasoning
			}
			if !emit(ctx, ch, provider.Event{Kind: kind, Text: content.Text}) {
				return ctx.Err()
			}
		}
	}
	if doc.Usage != nil {
		if !emit(ctx, ch, provider.Event{Kind: provider.EventUsage, Usage: &convo.Usage{
			In: doc.Usage.InputTokens, Out: doc.Usage.OutputTokens,
		}}) {
			return ctx.Err()
		}
	}
	return nil
}

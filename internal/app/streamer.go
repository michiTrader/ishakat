// streamer.go is the boundary Step 8 draws between internal/engine (which
// cannot import internal/provider: that package pulls in net/http, and
// internal/tui must never reach net/http transitively, per
// TestTUINoImportaHTTP) and a concrete provider.Provider. internal/app
// already imports both, so this is the one place allowed to translate
// between engine.Event and provider.Event.
package app

import (
	"context"

	"github.com/MichiTrader/ishakat/internal/engine"
	"github.com/MichiTrader/ishakat/internal/provider"
)

// NewStreamer adapts prov into an engine.Streamer. caps is bound once, at
// construction, because it is the model's own capabilities from the
// catalog (§5.4) — not something a running turn ever changes — which is
// exactly why engine.Request has no room for it (see types.go's comment).
//
// engine.ToolDef and provider.ToolDef are the same struct with different
// import graphs: engine's is the net/http-free copy, provider's is the one
// the dialect serializes. They share the same fields by design (§12bis #2),
// so the copy is field-by-field, the same way Model/Messages/System already
// cross this boundary.
//
// reasoning is bound at construction for the same reason caps is: it comes
// from [ui].reasoning (see ReasoningWanted), which a running turn never
// changes. It is a separate parameter rather than a field on caps because it
// is not a capability — see CapsFor's comment.
//
// It is an explicit parameter and not an inferred default on purpose. Every
// caller now has to say which it wants, and that is the point: the bug this
// closes was a whole feature that rendered correctly and displayed nothing
// because no layer ever actually asked the service for the data. A parameter
// that must be passed cannot silently go back to false the way an unset
// struct field did.
func NewStreamer(prov provider.Provider, caps provider.Caps, reasoning bool) engine.Streamer {
	return func(ctx context.Context, req engine.Request) (<-chan engine.Event, error) {
		tools := make([]provider.ToolDef, len(req.Tools))
		for i, t := range req.Tools {
			tools[i] = provider.ToolDef{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			}
		}
		pch, err := prov.Stream(ctx, provider.Request{
			Model:            req.Model,
			Messages:         req.Messages,
			System:           req.System,
			Caps:             caps,
			Stream:           true,
			Tools:            tools,
			IncludeReasoning: reasoning,
			// F9: copied straight through, unexamined — see
			// engine.Request.Params's own doc comment for who
			// populates it and why this package has no opinion on
			// what the keys mean.
			Params: req.Params,
		})
		if err != nil {
			// Returned as-is, not wrapped: retryAfter (internal/engine)
			// finds *provider.Error's Retry() method through errors.As,
			// and fmt.Errorf("%w", err) here would still satisfy that but
			// add nothing — the handshake error already carries every bit
			// of context a caller needs (provider.Error.Error()).
			return nil, err
		}

		ech := make(chan engine.Event)
		go translate(pch, ech)
		return ech, nil
	}
}

// translate copies a provider.Event channel into an engine.Event channel
// 1:1, closing the output exactly once the input closes — the same
// three-part contract (§5.4) both channels already promise: EventDone
// exactly once, EventError (if any) immediately before it, channel closed
// right after.
//
// EventToolCall is wired through as of Step 14 (§12bis): the agent loop in
// internal/engine drains it, runs the tool, appends a BlockToolResult, and
// iterates. ID carries the tool_call_id the service assigned so the
// dialect can correlate the result on the next turn.
func translate(in <-chan provider.Event, out chan<- engine.Event) {
	defer close(out)
	for ev := range in {
		switch ev.Kind {
		case provider.EventDelta:
			out <- engine.Event{Kind: engine.EventDelta, Text: ev.Text}
		case provider.EventReasoning:
			out <- engine.Event{Kind: engine.EventReasoning, Text: ev.Text}
		case provider.EventToolCall:
			out <- engine.Event{
				Kind: engine.EventToolCall,
				ID:   ev.ID,
				Name: ev.Name,
				Args: ev.Args,
				// Carried through untouched: dropping it here is what made
				// every Gemini 3 turn after a tool call fail with HTTP 400.
				Signature: ev.Signature,
			}
		case provider.EventUsage:
			out <- engine.Event{Kind: engine.EventUsage, Usage: ev.Usage}
		case provider.EventWarning:
			out <- engine.Event{Kind: engine.EventWarning, Text: ev.Text}
		case provider.EventError:
			out <- engine.Event{Kind: engine.EventError, Err: ev.Err}
		case provider.EventDone:
			out <- engine.Event{Kind: engine.EventDone, Usage: ev.Usage}
		}
	}
}

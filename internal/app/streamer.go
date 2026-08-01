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
func NewStreamer(prov provider.Provider, caps provider.Caps) engine.Streamer {
	return func(ctx context.Context, req engine.Request) (<-chan engine.Event, error) {
		pch, err := prov.Stream(ctx, provider.Request{
			Model:    req.Model,
			Messages: req.Messages,
			System:   req.System,
			Caps:     caps,
			Stream:   true,
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
// EventToolCall is deliberately dropped here: Step 8's scope is text
// streaming, and there is no tool-call rendering in the TUI yet (headless
// mode's own textSink doesn't render one specially either — see sink.go).
// Wiring tool calls through is future work once the TUI has somewhere to
// show them; dropping the event here costs nothing today because no
// provider this build ships (openai, fake) emits one outside a test that
// inspects provider.Event directly.
func translate(in <-chan provider.Event, out chan<- engine.Event) {
	defer close(out)
	for ev := range in {
		switch ev.Kind {
		case provider.EventDelta:
			out <- engine.Event{Kind: engine.EventDelta, Text: ev.Text}
		case provider.EventReasoning:
			out <- engine.Event{Kind: engine.EventReasoning, Text: ev.Text}
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

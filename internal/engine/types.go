// Package engine implements docs/PLAN.md's Step 8 (§7.3/§7.4): the turn
// runner that sits between internal/tui and a model provider — StreamBuf's
// coalescing drain (decoupled from repaint rate), the handshake-retry policy
// with backoff and jitter, and context-based cancellation.
//
// This package deliberately does not import internal/provider: that package
// imports net/http, and TestTUINoImportaHTTP (internal/arch_test.go) forbids
// internal/tui from reaching net/http transitively. internal/tui imports
// internal/engine directly per §7.1's Root wireframe, so the boundary has to
// sit one level below provider — engine defines its own minimal Event
// vocabulary, and internal/app (which already imports both) is what adapts a
// concrete provider.Provider into a Streamer closure below.
//
// internal/convo is fine to import here: it is §4's "moneda común", pure by
// TestConvoEsPuro's own contract (no net/http, no presentation), and both
// provider.Request and this package's Request describe the same history in
// the same currency on purpose — a Streamer built over a provider.Provider
// only has to copy the fields across, never translate them.
package engine

import (
	"context"
	"time"

	"github.com/MichiTrader/ishakat/internal/convo"
)

// EventKind mirrors provider.EventKind's cases that matter to a running
// turn. It exists separately, rather than as a type alias, so this package
// stays free of the provider import described above.
type EventKind int

const (
	EventDelta EventKind = iota
	EventReasoning
	EventUsage
	EventWarning
	EventDone
	EventError
)

// Event is engine's view of a single item off the wire, translated 1:1 from
// provider.Event by the Streamer adapter internal/app builds.
type Event struct {
	Kind  EventKind
	Text  string       // delta (EventDelta/EventReasoning) or message (EventWarning)
	Usage *convo.Usage // set on EventUsage, and optionally again on EventDone
	Err   error        // set on EventError
}

// Request is what a Streamer needs to open one turn. Model is the wire ID
// already resolved by the catalog/model reference machinery (§4.2) —
// engine never resolves a model reference itself. Messages is the active
// history (typically convo.Conversation.Active()) plus the new user turn
// already appended by the caller; System is the effective system prompt
// (§5.2's file-wins-over-inline rule already applied).
//
// Deliberately missing provider.Caps: which capabilities a model has is
// catalog/provider knowledge (§5.4), and the Streamer closure internal/app
// builds already has it bound at construction time — carrying it through
// Request would mean importing provider.Caps here, which pulls in
// net/http.
type Request struct {
	Model    string
	Messages []convo.Message
	System   string
}

// Streamer opens one turn against a provider and returns the event channel,
// exactly like provider.Provider.Stream but without naming that type. A
// non-nil error means the handshake itself failed (nothing has reached the
// screen yet): retry.go's retryAfter is what decides whether that's worth
// trying again.
type Streamer func(ctx context.Context, req Request) (<-chan Event, error)

// retryHint is the structural contract a Streamer's handshake error can
// satisfy to opt into engine's retry policy — matched via errors.As, so
// provider.Error (which already carries Retryable/RetryAfter, §5.4) can
// implement it without this package importing provider. Named Retry to
// avoid colliding with provider.Error's own RetryAfter/Retryable fields,
// which a method can't share a name with on the same type.
type retryHint interface {
	Retry() (wait time.Duration, retryable bool)
}

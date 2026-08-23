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
	"encoding/json"
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
	EventToolCall
	EventUsage
	EventWarning
	EventDone
	EventError
)

// Event is engine's view of a single item off the wire, translated 1:1 from
// provider.Event by the Streamer adapter internal/app builds.
type Event struct {
	Kind EventKind
	Text string
	// Name, Args and ID describe an EventToolCall. ID is the tool_call_id
	// the service assigned; agentloop.go copies it into the BlockToolCall so
	// the eventual BlockToolResult can round-trip the correlation the
	// OpenAI dialect requires (§12bis #5). Empty when the service does not
	// assign ids.
	Name string
	Args json.RawMessage
	ID   string

	// Signature is the opaque continuation token the provider attached to an
	// EventToolCall and requires back verbatim on the next request; the agent
	// loop copies it into the BlockToolCall it records so the dialect can
	// reattach it (convo.Block.Signature documents why). Empty for providers
	// that sign nothing.
	Signature string

	Usage *convo.Usage
	Err   error
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
	Tools    []ToolDef

	// Params is F9's per-turn wire escape hatch (roadmap: "/effort,
	// effort/thinking-level picker, a chord to cycle it, and a
	// headless-equivalent flag"), a plain map[string]any exactly mirroring
	// provider.Request.Params (internal/provider/provider.go) one field
	// down. It does NOT reopen the "no provider import" rule this
	// package's own doc comment states above: the type is
	// map[string]any, not a provider type, the same reasoning that
	// already lets provider.Caps stay out while Request still crosses
	// the internal/app boundary cleanly. The Streamer closure
	// (internal/app/streamer.go's NewStreamer) copies this straight into
	// provider.Request.Params, unexamined — engine has no opinion on
	// what a key means, only that some caller upstream (internal/tui or
	// internal/app) knows the per-provider/per-model wire key for the
	// effort or thinking-level it wants (e.g. "reasoning_effort" for
	// OpenAI, "generationConfig.thinkingConfig.thinkingLevel" for
	// Gemini 3+, one dotted path per applyParam's nested-key extension)
	// and has already resolved the value against that model's
	// catalog.Model.EffortLevels. nil is the zero value and the common
	// case: a turn with no effort override sends nothing new, byte-for-
	// byte identical to before this field existed.
	Params map[string]any
}

// ToolDef describes one callable tool without coupling engine to its implementation.
type ToolDef struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

// ToolResult is the model-visible outcome of a tool invocation.
type ToolResult struct {
	Text    string
	IsError bool
}

// ToolRunner executes one tool call. Implementations live outside engine.
type ToolRunner func(ctx context.Context, name string, args json.RawMessage) (ToolResult, error)

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

// deniedHint is the structural contract an error returned by a ToolRunner can
// satisfy to say "a human refused this", matched via errors.As exactly like
// retryHint above — so internal/permissions can express a denial without this
// package importing it, preserving the same boundary retryHint preserves
// against internal/provider.
//
// This distinction is load-bearing (§21.9, docs/BUG-rate-limit-amplifier.md).
// Every other failure a runner reports is data: the tool ran and failed, the
// model sees the error and reacts, and that is the mechanism by which the
// reactive loop handles the unforeseen (§3). A denial is not that. Nothing
// ran, and the reason nothing ran is that a person said no. Feeding it back
// as data invites the model to try a variant, each variant costs another
// provider request, and that is the amplifier that took a real user's account
// offline.
//
//	A denial is a decision, not a hint. When the human says no, the turn ends.
type deniedHint interface {
	// Denied reports that a human (or a policy standing in for one) refused
	// this call. The error's own message is the reason to show.
	Denied() bool
}

// agentstream.go is W2 item 2 (docs/ROADMAP-ux-2026-08-20.md's W2 section,
// "TUI consumes events: live reasoning during tool-enabled turns (F8a),
// phase/footer updates from real events rather than inferred ones"), built
// on top of W2 item 1's engine.AgentSink/RunAgentTurnStreaming
// (internal/engine/agentloop.go).
//
// agentStreamBuf is a tools-enabled turn's own counterpart to
// engine.StreamBuf: the same §7.3 buffering discipline — a producer
// goroutine writes deltas as fast as they arrive, a consumer drains them on
// its own repaint clock — DECISION-2 itself names StreamBuf as "the model
// for" when it describes RunAgentTurnStreaming's event surface. It cannot
// literally be an engine.StreamBuf, though: that type's push/pushReasoning/
// setUsage are unexported methods reachable only from inside package engine
// (Engine.run's own goroutine); a tools-enabled turn's AgentSink callbacks
// run from *this* package's own agentTurnStreamingCmd goroutine instead (see
// AgentSink's own doc comment: every callback fires synchronously on the
// caller's goroutine), so this package needs a buffer of its own to hand
// them.
//
// OnToolCallStart/OnToolCallEnd/Inject are deliberately left unused here:
// this file is W2 item 2, not item 4 (steering, docs/ROADMAP-ux-2026-08-20.md's
// W2 list) — the wave's own "eventing first, then the UI affordances that
// depend on it" ordering, respected the same way agentloop.go's own item-1
// change respected it by touching zero internal/tui files. Wiring those two
// fields is left for the sub-step that actually implements the steering
// queue and needs them.
package tui

import (
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/convo"
	"github.com/MichiTrader/ishakat/internal/engine"
)

// agentStreamBuf accumulates a tools-enabled turn's live events between one
// streamTickMsg and the next. mu guards every field: the sink's own
// callbacks (agentTurnStreamingCmd's goroutine) write, drainAgentStream
// (Update's goroutine) reads — the identical two-goroutine handoff
// engine.StreamBuf exists for, on the other side of the §6.1 import
// boundary.
type agentStreamBuf struct {
	mu        sync.Mutex
	text      strings.Builder
	reasoning strings.Builder
	usage     *convo.Usage

	// phase/phaseSet mirror engine.AgentSink.OnPhase's own per-iteration
	// event (W2 item 2's "real events rather than inferred ones").
	// phaseSet distinguishes "no OnPhase call since the last drain" from
	// "OnPhase fired with an empty string" (which never actually happens —
	// OnPhase's own doc comment says it only ever fires "exec" today —
	// but drain's contract stays honest about the difference either way,
	// the same reason engine.StreamBuf.Drain reports done as its own bool
	// rather than overloading a sentinel value on another field).
	phase    string
	phaseSet bool
}

// pushText is OnTextDelta's landing spot — append's sibling for the plain
// streamed path's own StreamBuf.push, just on this package's own buffer.
func (b *agentStreamBuf) pushText(delta string) {
	b.mu.Lock()
	b.text.WriteString(delta)
	b.mu.Unlock()
}

// pushReasoning is pushText's sibling for OnReasoningDelta, kept in its own
// builder for the same reason StreamBuf.pushReasoning is: §4 gives
// reasoning and text distinct block kinds, and coalescing them here would
// force drainAgentStream to re-split them later.
func (b *agentStreamBuf) pushReasoning(delta string) {
	b.mu.Lock()
	b.reasoning.WriteString(delta)
	b.mu.Unlock()
}

// setUsage mirrors StreamBuf.setUsage's own "either call overwrites, both
// carry the provider's running total" contract — never a delta — which is
// also exactly AgentSink.OnUsage's own documented contract.
func (b *agentStreamBuf) setUsage(u *convo.Usage) {
	b.mu.Lock()
	b.usage = u
	b.mu.Unlock()
}

// setPhase records AgentSink.OnPhase's per-iteration event.
func (b *agentStreamBuf) setPhase(phase string) {
	b.mu.Lock()
	b.phase = phase
	b.phaseSet = true
	b.mu.Unlock()
}

// drain empties the buffer and reports what arrived since the last call,
// mirroring engine.StreamBuf.Drain's own contract: safe to call from
// Update's goroutine while the sink's callbacks run concurrently from
// agentTurnStreamingCmd's own.
func (b *agentStreamBuf) drain() (text, reasoning string, usage *convo.Usage, phase string, phaseSet bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	text = b.text.String()
	b.text.Reset()
	reasoning = b.reasoning.String()
	b.reasoning.Reset()
	usage = b.usage
	phase = b.phase
	phaseSet = b.phaseSet
	b.phaseSet = false
	return
}

// sink builds the engine.AgentSink startAgentTurn hands to
// RunAgentTurnStreaming. Every field here only ever pushes into this
// buffer — never touches Root directly — because these callbacks run on
// agentTurnStreamingCmd's own goroutine (RunAgentTurnStreaming's own doc
// comment: "every callback fires synchronously on the same goroutine...
// nothing in this package starts a goroutine for a turn; see RunAgentTurn's
// own doc comment"), never Update's. Touching Root fields directly from
// here would be a data race Update's own goroutine could observe mid-write.
func (b *agentStreamBuf) sink() engine.AgentSink {
	return engine.AgentSink{
		OnTextDelta:      b.pushText,
		OnReasoningDelta: b.pushReasoning,
		OnUsage:          b.setUsage,
		OnPhase:          b.setPhase,
	}
}

// drainAgentStream is streamTickMsg's handler for a tools-enabled turn — the
// exact counterpart to root.go's own drainStream, feeding the very same
// m.live a plain streamed turn already fills. renderLiveTurn (chat.go)
// already reads m.live unconditionally whenever m.live.active is true
// (startAgentTurn already sets that, before this change existed); it just
// never had live text or reasoning written into it during a tools-enabled
// turn before this file, which is the exact F8a gap this closes — "reasoning
// streams live during a tool-enabled turn" becomes true with no change to
// chat.go/view.go at all.
//
// The phase update is applied only while m.mode == ModeBusy: a paused
// ModeToolApprove/ModeAskUser dialog already owns footer.Phase ("ask", set
// directly by openToolApprove/openAskUser) at the moment a tick like this
// one might run, and a queued "exec" event from before the pause began —
// buffered but not yet drained when the dialog opened — must not silently
// overwrite it. Nothing about the loop's own state is lost by skipping the
// write here: the next iteration's own OnPhase("exec") call fires once the
// loop actually resumes, by which point resolveToolApproveWith/
// resolveAskUserWith have already set the same word directly on their own
// return path.
func (m Root) drainAgentStream() (tea.Model, tea.Cmd) {
	if m.agentStream == nil {
		// A tick that outlived its turn — the same "one tick already in
		// flight when releaseTurn ran" case drainStream's own doc comment
		// documents for m.buf. Dropping it is correct and deliberately
		// silent.
		return m, nil
	}
	text, reasoning, usage, phase, phaseSet := m.agentStream.drain()
	if reasoning != "" {
		m.live.appendReasoning(reasoning)
	}
	if text != "" {
		m.live.append(text)
	}
	if usage != nil {
		m.live.usage = usage
	}
	if phaseSet && m.mode == ModeBusy {
		m.footer.Phase = phase
	}
	return m, tickStream()
}

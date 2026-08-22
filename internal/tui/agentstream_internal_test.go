package tui

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/MichiTrader/ishakat/internal/convo"
	"github.com/MichiTrader/ishakat/internal/engine"

	tea "charm.land/bubbletea/v2"
)

// runBatchAndCollectBudget bounds how long this file's own runBatchAndCollect
// waits for each leaf Cmd, mirroring idle_internal_test.go's own idleBudget
// reasoning: a Cmd that has not produced its message by then is either a
// ticker (fine to give up on here) or a real bug, never something this
// suite should hang on.
const runBatchAndCollectBudget = 200 * time.Millisecond

// runBatchAndCollect flattens a tea.Batch the same way idle_internal_test.go's
// own countTimers and banner_clear_internal_test.go's own walkPrintedLines
// already do for their own purposes, but collects every leaf tea.Msg instead
// of counting or pattern-matching one — this file's own
// TestStartAgentTurnArmsAgentStreamAndTickStream needs to see everything a
// startAgentTurn batch produced, not just whether one particular shape was
// present.
func runBatchAndCollect(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()

	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(runBatchAndCollectBudget):
		return nil
	}

	if batch, ok := msg.(tea.BatchMsg); ok {
		var all []tea.Msg
		for _, child := range batch {
			all = append(all, runBatchAndCollect(child)...)
		}
		return all
	}
	return []tea.Msg{msg}
}

// TestAgentStreamBufDrainCoalescesPushesAndResets mirrors
// engine.StreamBuf's own TestStreamBufDrainCoalescesPushesAndResets: the
// same coalesce-then-reset contract, just on this package's own buffer
// (agentstream.go's own doc comment explains why it cannot literally be an
// engine.StreamBuf).
func TestAgentStreamBufDrainCoalescesPushesAndResets(t *testing.T) {
	var b agentStreamBuf
	b.pushText("hel")
	b.pushText("lo")
	b.pushReasoning("thinking")

	text, reasoning, usage, phase, phaseSet := b.drain()
	if text != "hello" {
		t.Errorf("text = %q, want %q", text, "hello")
	}
	if reasoning != "thinking" {
		t.Errorf("reasoning = %q, want %q", reasoning, "thinking")
	}
	if usage != nil || phaseSet {
		t.Errorf("unexpected state: usage=%v phaseSet=%v", usage, phaseSet)
	}
	if phase != "" {
		t.Errorf("phase = %q, want empty when phaseSet is false", phase)
	}

	// A second drain with nothing pushed in between must come back empty —
	// the whole point of Reset() (mirrored from StreamBuf) is that text
	// doesn't repeat itself.
	text, reasoning, _, _, phaseSet = b.drain()
	if text != "" || reasoning != "" {
		t.Errorf("second drain() with no new pushes returned text=%q reasoning=%q, want both empty", text, reasoning)
	}
	if phaseSet {
		t.Errorf("second drain() phaseSet = true, want false with no new setPhase call")
	}
}

// TestAgentStreamBufUsagePersistsAcrossDrains mirrors StreamBuf's own
// TestStreamBufUsagePersistsAcrossDrains: OnUsage's contract is a running
// total, not a delta, so the pointer must survive a drain with nothing new
// pushed.
func TestAgentStreamBufUsagePersistsAcrossDrains(t *testing.T) {
	var b agentStreamBuf
	u := &convo.Usage{In: 10, Out: 5}
	b.setUsage(u)

	_, _, got, _, _ := b.drain()
	if got != u {
		t.Fatalf("drain() usage = %v, want the pointer set by setUsage", got)
	}

	_, _, got, _, _ = b.drain()
	if got != u {
		t.Errorf("drain() usage on second call = %v, want it to persist", got)
	}
}

// TestAgentStreamBufPhaseSetDistinguishesNoEventFromEmptyString pins the
// phaseSet flag's own reason for existing (agentStreamBuf's doc comment):
// a drain with no OnPhase call since the last one must report phaseSet ==
// false, not silently look identical to an OnPhase("") call that never
// actually happens in practice but whose absence still has to be provable.
func TestAgentStreamBufPhaseSetDistinguishesNoEventFromEmptyString(t *testing.T) {
	var b agentStreamBuf
	b.setPhase("exec")

	_, _, _, phase, phaseSet := b.drain()
	if !phaseSet || phase != "exec" {
		t.Fatalf("first drain() phase=%q phaseSet=%v, want %q/true", phase, phaseSet, "exec")
	}

	_, _, _, phase, phaseSet = b.drain()
	if phaseSet {
		t.Fatalf("second drain() phaseSet = true with no new setPhase call, want false (got phase=%q)", phase)
	}
}

// TestAgentStreamBufSinkWiresTextReasoningUsagePhase confirms sink() hands
// engine.AgentSink exactly the five callbacks agentstream.go's own doc
// comment says it wires (OnTextDelta/OnReasoningDelta/OnUsage/OnPhase/
// Inject) and that each one actually lands in this buffer — a
// compile-time signature check alone would not catch a callback silently
// wired to the wrong method. OnToolCallStart/OnToolCallEnd remain
// unwired: nothing in this package's own UI depends on them yet (see
// agentstream.go's own package doc comment).
func TestAgentStreamBufSinkWiresTextReasoningUsagePhase(t *testing.T) {
	var b agentStreamBuf
	sink := b.sink()

	if sink.OnToolCallStart != nil || sink.OnToolCallEnd != nil {
		t.Fatalf("sink() wired OnToolCallStart/OnToolCallEnd, want them left nil (nothing in this package reads them yet)")
	}
	if sink.Inject == nil {
		t.Fatalf("sink() left Inject nil, want it wired to b.inject (W2 item 4, F13)")
	}

	sink.OnTextDelta("answer")
	sink.OnReasoningDelta("thinking")
	u := &convo.Usage{In: 1, Out: 2}
	sink.OnUsage(u)
	sink.OnPhase("exec")

	text, reasoning, usage, phase, phaseSet := b.drain()
	if text != "answer" {
		t.Errorf("text = %q, want %q", text, "answer")
	}
	if reasoning != "thinking" {
		t.Errorf("reasoning = %q, want %q", reasoning, "thinking")
	}
	if usage != u {
		t.Errorf("usage = %v, want %v", usage, u)
	}
	if !phaseSet || phase != "exec" {
		t.Errorf("phase=%q phaseSet=%v, want %q/true", phase, phaseSet, "exec")
	}
}

// TestAgentStreamBufSinkInjectDrainsSteeringQueue is the new half of the
// test above, specific to W2 item 4: sink().Inject must actually reach
// this buffer's own steering queue and honour steeringMode, not just be
// non-nil.
func TestAgentStreamBufSinkInjectDrainsSteeringQueue(t *testing.T) {
	q := newSteeringQueue()
	q.enqueueSteering(convo.User("one"))
	q.enqueueSteering(convo.User("two"))
	b := &agentStreamBuf{steering: q, steeringMode: "one-at-a-time"}
	sink := b.sink()

	got := sink.Inject()
	if len(got) != 1 || got[0].Text() != "one" {
		t.Fatalf("first Inject() call = %+v, want exactly [\"one\"] (one-at-a-time)", got)
	}
	if q.steeringLen() != 1 {
		t.Fatalf("queue len after first Inject() = %d, want 1 (\"two\" still queued)", q.steeringLen())
	}

	got = sink.Inject()
	if len(got) != 1 || got[0].Text() != "two" {
		t.Fatalf("second Inject() call = %+v, want exactly [\"two\"]", got)
	}
	if got := sink.Inject(); got != nil {
		t.Fatalf("third Inject() call with an empty queue = %+v, want nil", got)
	}
}

// TestAgentStreamBufSinkInjectNilSteeringIsANoOp confirms a buffer built
// without a steering queue at all (every pre-W2-item-4 test in this
// package that still constructs a bare agentStreamBuf{}, and
// TestAgentStreamBufSinkWiresTextReasoningUsagePhase above) does not
// panic when Inject is actually called — steeringQueue's own nil
// receiver checks (steering.go) are what make this safe.
func TestAgentStreamBufSinkInjectNilSteeringIsANoOp(t *testing.T) {
	var b agentStreamBuf
	sink := b.sink()
	if got := sink.Inject(); got != nil {
		t.Fatalf("Inject() on a buffer with no steering queue = %+v, want nil", got)
	}
}

// TestDrainAgentStreamOutlivedTurnIsANoOp mirrors drainStream's own
// documented "a tick that outlived its turn" guard: a stray streamTickMsg
// after releaseTurn already cleared m.agentStream to nil must be dropped
// silently, not panic on a nil buffer.
func TestDrainAgentStreamOutlivedTurnIsANoOp(t *testing.T) {
	r := Root{agentStream: nil}
	model, cmd := r.drainAgentStream()
	got := model.(Root)
	if got.agentStream != nil {
		t.Fatalf("drainAgentStream on a nil buffer changed agentStream to %v, want it to stay nil", got.agentStream)
	}
	if cmd != nil {
		t.Fatalf("drainAgentStream on a nil buffer returned a non-nil cmd, want nil")
	}
}

// TestDrainAgentStreamFeedsLiveTurnTextAndReasoning is F8a's own closing
// test: text and reasoning pushed into agentStreamBuf before a
// streamTickMsg must reach the exact same m.live fields renderLiveTurn
// (chat.go) already reads unconditionally — no chat.go/view.go change is
// needed for this to work, per agentstream.go's own doc comment, and this
// test is what proves that claim rather than just asserting it in prose.
func TestDrainAgentStreamFeedsLiveTurnTextAndReasoning(t *testing.T) {
	buf := &agentStreamBuf{}
	buf.pushText("hello")
	buf.pushReasoning("thinking it through")

	r := Root{agentStream: buf, mode: ModeBusy}
	r.live.start("test/model")

	model, cmd := r.drainAgentStream()
	got := model.(Root)

	if got.live.body() != "hello" {
		t.Fatalf("live.body() = %q, want %q", got.live.body(), "hello")
	}
	if got.live.reasoning() != "thinking it through" {
		t.Fatalf("live.reasoning() = %q, want %q", got.live.reasoning(), "thinking it through")
	}
	if cmd == nil {
		t.Fatalf("drainAgentStream returned a nil cmd while the turn is still live, want tickStream() to re-arm")
	}
}

// TestDrainAgentStreamUpdatesFooterPhaseOnlyWhileModeBusy is W2 item 2's
// other half ("phase/footer updates from real events rather than inferred
// ones") together with the guard that keeps a paused ModeToolApprove/
// ModeAskUser dialog's own "ask" phase from being clobbered by a stale
// buffered "exec" event that arrived before the pause but was not yet
// drained when the dialog opened.
func TestDrainAgentStreamUpdatesFooterPhaseOnlyWhileModeBusy(t *testing.T) {
	t.Run("ModeBusy applies the drained phase", func(t *testing.T) {
		buf := &agentStreamBuf{}
		buf.setPhase("exec")
		r := Root{agentStream: buf, mode: ModeBusy, footer: FooterState{Phase: "stale"}}
		model, _ := r.drainAgentStream()
		got := model.(Root)
		if got.footer.Phase != "exec" {
			t.Fatalf("footer.Phase = %q, want %q", got.footer.Phase, "exec")
		}
	})

	t.Run("ModeToolApprove keeps its own ask phase", func(t *testing.T) {
		buf := &agentStreamBuf{}
		buf.setPhase("exec")
		r := Root{agentStream: buf, mode: ModeToolApprove, footer: FooterState{Phase: "ask"}}
		model, _ := r.drainAgentStream()
		got := model.(Root)
		if got.footer.Phase != "ask" {
			t.Fatalf("footer.Phase = %q, want %q (a stale buffered \"exec\" must not overwrite a paused dialog's own phase)", got.footer.Phase, "ask")
		}
	})
}

// TestAgentTurnCmdStreamsReasoningAndTextBeforeReturning is the
// integration proof that RunAgentTurnStreaming's sink is actually reached
// end to end from agentTurnCmd (agentturn.go), not just that
// agentStreamBuf's own methods work in isolation: a fake tool-enabled
// stream that emits EventReasoning and EventDelta before EventDone must
// have those deltas land in the sink synchronously, on the same goroutine
// agentTurnCmd's own tea.Cmd runs on — exactly the contract
// RunAgentTurnStreaming's own doc comment (agentloop.go) promises.
func TestAgentTurnCmdStreamsReasoningAndTextBeforeReturning(t *testing.T) {
	stream := func(ctx context.Context, req engine.Request) (<-chan engine.Event, error) {
		ch := make(chan engine.Event, 3)
		ch <- engine.Event{Kind: engine.EventReasoning, Text: "thinking"}
		ch <- engine.Event{Kind: engine.EventDelta, Text: "final answer"}
		ch <- engine.Event{Kind: engine.EventDone}
		close(ch)
		return ch, nil
	}
	eng := engine.New(stream, 0)
	history := &convo.Conversation{}

	buf := &agentStreamBuf{}
	cmd := agentTurnCmd(context.Background(), eng, engine.Request{Model: "test/model"}, engine.AgentOptions{}, history, buf.sink())

	value := cmd()
	msg, ok := value.(agentTurnDoneMsg)
	if !ok {
		t.Fatalf("agentTurnCmd returned %T, want agentTurnDoneMsg", value)
	}
	if msg.err != nil {
		t.Fatalf("agent turn error = %v, want nil", msg.err)
	}
	if msg.result.Text != "final answer" {
		t.Fatalf("agent result text = %q, want %q", msg.result.Text, "final answer")
	}

	// By the time cmd() has returned, the loop is over and every callback
	// it was going to fire already has — RunAgentTurnStreaming blocks until
	// the turn finishes (its own doc comment), so there is no race to wait
	// out here before draining.
	text, reasoning, _, phase, phaseSet := buf.drain()
	if text != "final answer" {
		t.Errorf("sink received text = %q, want %q", text, "final answer")
	}
	if reasoning != "thinking" {
		t.Errorf("sink received reasoning = %q, want %q", reasoning, "thinking")
	}
	if !phaseSet || phase != "exec" {
		t.Errorf("sink received phase=%q phaseSet=%v, want %q/true (OnPhase fires once per iteration)", phase, phaseSet, "exec")
	}
}

// TestStartAgentTurnArmsAgentStreamAndTickStream confirms startAgentTurn
// (agentturn.go) actually wires everything drainAgentStream needs before
// any streamTickMsg can arrive: m.agentStream set to a fresh, empty buffer,
// and tickStream() among the commands returned — the two things this
// turn's wiring added to startAgentTurn beyond what existed before W2 item
// 2 (see the diff between this test and Step 16's original startAgentTurn
// behaviour, which set neither).
func TestStartAgentTurnArmsAgentStreamAndTickStream(t *testing.T) {
	eng, _ := echoEngine(false)
	r := withEngine(Root{toolsEnabled: true}, eng)
	r.agentOpts = engine.AgentOptions{
		Tools: []engine.ToolDef{{Name: "read_file"}},
		Runner: func(ctx context.Context, name string, args json.RawMessage) (engine.ToolResult, error) {
			return engine.ToolResult{Text: "irrelevant"}, nil
		},
	}

	model, cmd := r.startAgentTurn("")
	got := model.(Root)

	if got.agentStream == nil {
		t.Fatal("startAgentTurn did not set agentStream")
	}
	if cmd == nil {
		t.Fatal("startAgentTurn returned a nil cmd, want at least agentTurnCmd and tickStream()")
	}

	// tickStream() delivers a streamTickMsg; the batched cmd is opaque, so
	// running it and checking one of the resulting messages is a
	// streamTickMsg is how this asserts tickStream() was actually included
	// rather than only agentTurnCmd (which alone would settle on
	// agentTurnDoneMsg instead, once the fake engine's real tool call
	// finishes — echoEngine has no tool support at all, so gating isn't
	// needed here, only that at least one streamTickMsg shows up among the
	// batch's messages).
	saw := runBatchAndCollect(cmd)
	found := false
	for _, m := range saw {
		if _, ok := m.(streamTickMsg); ok {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("startAgentTurn's returned cmd never produced a streamTickMsg, want tickStream() among its batched commands; got %#v", saw)
	}
}

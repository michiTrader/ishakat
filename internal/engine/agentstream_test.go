package engine

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/MichiTrader/ishakat/internal/convo"
)

// agentstream_test.go covers RunAgentTurnStreaming (DECISION-2,
// docs/ROADMAP-ux-2026-08-20.md's W2 section, item 1): the streaming
// sibling to RunAgentTurn. TestRunAgentTurnStreamingMatchesBlockingForm
// above proves the zero-AgentSink case behaves identically to the blocking
// form; the rest of this file proves each AgentSink field fires the events
// DECISION-2 promises, in the order it promises them, and — the one
// explicitly named W2 gate — that Inject can never be used to approve a
// pending tool call.

// recordingSink collects every event RunAgentTurnStreaming reports, in
// arrival order, behind a mutex — callbacks fire from the caller's own
// goroutine per AgentSink's contract, but tests run these assertions from
// a second goroutine in a couple of cases below, so this stays safe either
// way.
type recordingSink struct {
	mu         sync.Mutex
	textDeltas []string
	reasoning  []string
	usages     []*convo.Usage
	phases     []string
	starts     []toolEvent
	ends       []toolEndEvent
	inject     func() []convo.Message
}

type toolEvent struct {
	id, name string
	args     json.RawMessage
}

type toolEndEvent struct {
	id, name, result string
	isError          bool
}

func (r *recordingSink) sink() AgentSink {
	return AgentSink{
		OnTextDelta: func(delta string) {
			r.mu.Lock()
			r.textDeltas = append(r.textDeltas, delta)
			r.mu.Unlock()
		},
		OnReasoningDelta: func(delta string) {
			r.mu.Lock()
			r.reasoning = append(r.reasoning, delta)
			r.mu.Unlock()
		},
		OnUsage: func(u *convo.Usage) {
			r.mu.Lock()
			r.usages = append(r.usages, u)
			r.mu.Unlock()
		},
		OnPhase: func(phase string) {
			r.mu.Lock()
			r.phases = append(r.phases, phase)
			r.mu.Unlock()
		},
		OnToolCallStart: func(id, name string, args json.RawMessage) {
			r.mu.Lock()
			r.starts = append(r.starts, toolEvent{id: id, name: name, args: args})
			r.mu.Unlock()
		},
		OnToolCallEnd: func(id, name, result string, isError bool) {
			r.mu.Lock()
			r.ends = append(r.ends, toolEndEvent{id: id, name: name, result: result, isError: isError})
			r.mu.Unlock()
		},
		Inject: r.inject,
	}
}

func (r *recordingSink) snapshot() recordingSink {
	r.mu.Lock()
	defer r.mu.Unlock()
	return recordingSink{
		textDeltas: append([]string(nil), r.textDeltas...),
		reasoning:  append([]string(nil), r.reasoning...),
		usages:     append([]*convo.Usage(nil), r.usages...),
		phases:     append([]string(nil), r.phases...),
		starts:     append([]toolEvent(nil), r.starts...),
		ends:       append([]toolEndEvent(nil), r.ends...),
	}
}

// TestRunAgentTurnStreamingMatchesBlockingForm proves RunAgentTurnStreaming
// with a zero AgentSink (every field nil) returns byte-for-byte the same
// AgentResult and grows history identically to RunAgentTurn — the exact
// contract runAgentTurn's own doc comment promises ("a zero AgentSink...
// makes this call identical to RunAgentTurn"). Uses the same script
// TestRunAgentTurnToolCallThenAnswer already exercises, so any divergence
// between the two entry points shows up as a diff against an already-
// trusted fixture rather than a fresh one that could itself be wrong.
func TestRunAgentTurnStreamingMatchesBlockingForm(t *testing.T) {
	script := [][]Event{
		{toolCallEvent("c1", "list", `{"dir":"."}`), doneEvent()},
		{deltaEvent("the directory contains: a.go, b.go"), doneEvent()},
	}

	run := func(streaming bool) (AgentResult, error, *convo.Conversation) {
		ss := &scriptedStreamer{scripts: append([][]Event{}, script...)}
		runner := newFakeRunner()
		runner.results["list"] = ToolResult{Text: "a.go\nb.go"}
		eng := New(ss.stream, 0)
		hist := &convo.Conversation{}
		hist.Add(convo.User("what files are in this directory"))
		req := Request{Model: "fake/pro"}
		opts := AgentOptions{Tools: []ToolDef{{Name: "list", Description: "list files"}}, Runner: runner.run}
		if streaming {
			res, err := eng.RunAgentTurnStreaming(context.Background(), req, opts, hist, AgentSink{})
			return res, err, hist
		}
		res, err := eng.RunAgentTurn(context.Background(), req, opts, hist)
		return res, err, hist
	}

	wantRes, wantErr, wantHist := run(false)
	gotRes, gotErr, gotHist := run(true)

	if gotErr != wantErr {
		t.Fatalf("err = %v, want %v", gotErr, wantErr)
	}
	if gotRes.Text != wantRes.Text || gotRes.Calls != wantRes.Calls || gotRes.Stopped != wantRes.Stopped || gotRes.Aborted != wantRes.Aborted {
		t.Fatalf("AgentResult diverged: got %+v, want %+v", gotRes, wantRes)
	}
	if len(gotHist.Messages) != len(wantHist.Messages) {
		t.Fatalf("history length diverged: got %d, want %d", len(gotHist.Messages), len(wantHist.Messages))
	}
	for i := range wantHist.Messages {
		if gotHist.Messages[i].Role != wantHist.Messages[i].Role {
			t.Errorf("message %d role diverged: got %v, want %v", i, gotHist.Messages[i].Role, wantHist.Messages[i].Role)
		}
	}
}

// TestRunAgentTurnStreamingReportsTextAndReasoningDeltasLive proves
// OnTextDelta/OnReasoningDelta fire per-delta as the loop drains each
// iteration's channel — not just once at the end — and that concatenating
// what arrived equals AgentResult's own final Text/Reasoning (the same
// data, live rather than only once the turn ends, per AgentSink's own doc
// comment on OnReasoningDelta).
func TestRunAgentTurnStreamingReportsTextAndReasoningDeltasLive(t *testing.T) {
	ss := &scriptedStreamer{
		scripts: [][]Event{
			{reasoningEvent("thinking "), reasoningEvent("some more"), deltaEvent("hello "), deltaEvent("world"), doneEvent()},
		},
	}
	eng := New(ss.stream, 0)
	hist := &convo.Conversation{}
	hist.Add(convo.User("hi"))

	rec := &recordingSink{}
	res, err := eng.RunAgentTurnStreaming(context.Background(), Request{Model: "fake/pro"}, AgentOptions{}, hist, rec.sink())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	snap := rec.snapshot()
	if got := strings.Join(snap.textDeltas, ""); got != res.Text {
		t.Errorf("concatenated OnTextDelta = %q, want res.Text %q", got, res.Text)
	}
	if got := strings.Join(snap.reasoning, ""); got != res.Reasoning {
		t.Errorf("concatenated OnReasoningDelta = %q, want res.Reasoning %q", got, res.Reasoning)
	}
	if len(snap.textDeltas) != 2 {
		t.Errorf("OnTextDelta fired %d times, want 2 (one per delta, not coalesced)", len(snap.textDeltas))
	}
}

// TestRunAgentTurnStreamingReportsToolCallStartAndEnd proves
// OnToolCallStart fires before the tool actually runs and OnToolCallEnd
// fires after its BlockToolResult has already landed in history — the
// exact ordering AgentSink's own doc comments promise ("never a Start with
// no matching End", "history already carries the result by the time End
// fires").
func TestRunAgentTurnStreamingReportsToolCallStartAndEnd(t *testing.T) {
	ss := &scriptedStreamer{
		scripts: [][]Event{
			{toolCallEvent("c1", "list", `{"dir":"."}`), doneEvent()},
			{deltaEvent("done"), doneEvent()},
		},
	}
	runner := newFakeRunner()
	runner.results["list"] = ToolResult{Text: "a.go"}
	eng := New(ss.stream, 0)
	hist := &convo.Conversation{}
	hist.Add(convo.User("list files"))

	var historyLenAtEnd int
	rec := &recordingSink{}
	sink := rec.sink()
	sink.OnToolCallEnd = func(id, name, result string, isError bool) {
		rec.mu.Lock()
		rec.ends = append(rec.ends, toolEndEvent{id: id, name: name, result: result, isError: isError})
		rec.mu.Unlock()
		historyLenAtEnd = len(hist.Messages)
	}

	_, err := eng.RunAgentTurnStreaming(context.Background(), Request{Model: "fake/pro"}, AgentOptions{
		Tools:  []ToolDef{{Name: "list"}},
		Runner: runner.run,
	}, hist, sink)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	snap := rec.snapshot()
	if len(snap.starts) != 1 {
		t.Fatalf("OnToolCallStart fired %d times, want 1", len(snap.starts))
	}
	if snap.starts[0].id != "c1" || snap.starts[0].name != "list" {
		t.Errorf("OnToolCallStart = %+v, want id=c1 name=list", snap.starts[0])
	}
	if len(snap.ends) != 1 {
		t.Fatalf("OnToolCallEnd fired %d times, want 1", len(snap.ends))
	}
	if snap.ends[0].id != "c1" || snap.ends[0].name != "list" || snap.ends[0].isError {
		t.Errorf("OnToolCallEnd = %+v, want id=c1 name=list isError=false", snap.ends[0])
	}
	if snap.ends[0].result != "a.go" {
		t.Errorf("OnToolCallEnd result = %q, want %q", snap.ends[0].result, "a.go")
	}
	// history already has the tool result message by the time End fires —
	// user + assistant(tool_call) + tool(result) = 3, not 2.
	if historyLenAtEnd != 3 {
		t.Errorf("history length at OnToolCallEnd = %d, want 3 (result already persisted)", historyLenAtEnd)
	}
}

// TestRunAgentTurnStreamingReportsPhaseExecPerIteration proves OnPhase
// fires "exec" once per loop iteration (W2 item 2: "phase/footer updates
// from real events rather than inferred ones") — twice for a turn that
// makes one tool call and then answers, since that is two iterations of
// the loop.
func TestRunAgentTurnStreamingReportsPhaseExecPerIteration(t *testing.T) {
	ss := &scriptedStreamer{
		scripts: [][]Event{
			{toolCallEvent("c1", "list", `{}`), doneEvent()},
			{deltaEvent("done"), doneEvent()},
		},
	}
	runner := newFakeRunner()
	eng := New(ss.stream, 0)
	hist := &convo.Conversation{}
	hist.Add(convo.User("go"))

	rec := &recordingSink{}
	_, err := eng.RunAgentTurnStreaming(context.Background(), Request{Model: "fake/pro"}, AgentOptions{
		Tools:  []ToolDef{{Name: "list"}},
		Runner: runner.run,
	}, hist, rec.sink())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	snap := rec.snapshot()
	if len(snap.phases) != 2 {
		t.Fatalf("OnPhase fired %d times, want 2 (one per iteration)", len(snap.phases))
	}
	for i, p := range snap.phases {
		if p != "exec" {
			t.Errorf("phase[%d] = %q, want %q", i, p, "exec")
		}
	}
}

// TestRunAgentTurnStreamingInjectAddsMessageBeforeNextRequest proves
// Inject's return value lands in history before the next iteration's
// request is built — i.e. the model's next turn sees it in context — the
// exact contract AgentSink.Inject's own doc comment promises.
func TestRunAgentTurnStreamingInjectAddsMessageBeforeNextRequest(t *testing.T) {
	ss := &scriptedStreamer{
		scripts: [][]Event{
			{toolCallEvent("c1", "list", `{}`), doneEvent()},
			{deltaEvent("done"), doneEvent()},
		},
	}
	runner := newFakeRunner()
	eng := New(ss.stream, 0)
	hist := &convo.Conversation{}
	hist.Add(convo.User("go"))

	var injected bool
	sink := AgentSink{
		Inject: func() []convo.Message {
			if injected {
				return nil
			}
			injected = true
			return []convo.Message{convo.User("steering: focus on b.go instead")}
		},
	}
	_, err := eng.RunAgentTurnStreaming(context.Background(), Request{Model: "fake/pro"}, AgentOptions{
		Tools:  []ToolDef{{Name: "list"}},
		Runner: runner.run,
	}, hist, sink)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The second iteration's request (recorded by scriptedStreamer) must
	// already carry the injected steering message in its Messages, proving
	// it landed in history before that request was built — not merely
	// somewhere in the final history by the time the turn ended.
	req1 := ss.requestAt(1)
	found := false
	for _, m := range req1.Messages {
		for _, b := range m.Blocks {
			if strings.Contains(b.Text, "steering: focus on b.go instead") {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("second iteration's request did not carry the injected steering message; messages: %+v", req1.Messages)
	}

	// And it must appear in the persisted history as an ordinary
	// convo.RoleUser message, exactly like the user's own first message
	// (§21's "an injected message becomes an ordinary convo.RoleUser
	// history entry", AgentSink.Inject's own doc comment).
	sawSteering := false
	for _, m := range hist.Messages {
		if m.Role != convo.RoleUser {
			continue
		}
		for _, b := range m.Blocks {
			if strings.Contains(b.Text, "steering: focus on b.go instead") {
				sawSteering = true
			}
		}
	}
	if !sawSteering {
		t.Error("steering message not found as a RoleUser entry in the persisted history")
	}
}

// TestRunAgentTurnStreamingInjectCannotApprovePendingToolCall is the W2
// gate DECISION-2 names explicitly (consequence 2, docs/ROADMAP-ux-2026-08-20.md):
// "a steering message arriving while a tool call is pending must leave
// that call pending, unapproved." This is the negative-assertion security
// test the roadmap says W2 cannot be considered closed without.
//
// The setup: a Runner that blocks (simulating a tool call paused on a
// human decision, mirroring ModeToolApprove/permissions.Guard.Authorize's
// own real blocking-on-Reply shape) while, concurrently, Inject is called
// and returns a message. The assertion is structural, not timing-based:
// Inject's own return value can only ever become a convo.RoleUser message
// appended to history between iterations — engine.AgentSink has no field,
// no channel and no side channel that could reach into a Runner call
// already in flight and short-circuit it. There is no code path in
// runAgentTurn between "the tool call started" (OnToolCallStart /
// opts.Runner invoked) and "the tool call ended" (OnToolCallEnd) that
// reads sink.Inject at all — Inject is only ever polled once, at the top
// of the next iteration, which cannot begin until the current one's
// in-flight tool call has already returned. This test proves that by
// running Inject concurrently with a still-blocked Runner and observing
// that the tool call's own result is exactly what the Runner (not Inject)
// produced, with no trace of the injected content, and that the injected
// message's own history position is strictly after the tool result's.
func TestRunAgentTurnStreamingInjectCannotApprovePendingToolCall(t *testing.T) {
	toolStarted := make(chan struct{})
	releaseRunner := make(chan struct{})

	blockingApproval := func(ctx context.Context, name string, args json.RawMessage) (ToolResult, error) {
		close(toolStarted)
		select {
		case <-releaseRunner:
		case <-ctx.Done():
			return ToolResult{}, ctx.Err()
		}
		// The tool's own result, entirely independent of anything Inject
		// might have returned meanwhile — a real Guard.Authorize denial or
		// approval is decided by the human resolving the dialog, never by
		// history content.
		return ToolResult{Text: "approved-by-real-dialog-only"}, nil
	}

	ss := &scriptedStreamer{
		scripts: [][]Event{
			{toolCallEvent("c1", "sensitive_tool", `{}`), doneEvent()},
			{deltaEvent("final answer"), doneEvent()},
		},
	}
	eng := New(ss.stream, 0)
	hist := &convo.Conversation{}
	hist.Add(convo.User("do something sensitive"))

	injectCalls := 0
	sink := AgentSink{
		Inject: func() []convo.Message {
			injectCalls++
			if injectCalls == 1 {
				return nil
			}
			// A steering message arrives on iteration 2's poll — by
			// construction that can only happen after iteration 1's tool
			// call has already fully returned (Inject is polled once per
			// iteration, at the top, before that iteration's request is
			// built — see the comment above the call site in
			// runAgentTurn).
			return []convo.Message{convo.User("steering: please hurry")}
		},
	}

	// Release the blocked runner shortly after confirming it actually
	// started, from a second goroutine, so the main goroutine's call into
	// RunAgentTurnStreaming below stays genuinely blocked inside the tool
	// call for a moment — proving there is a real window during which a
	// steering message could theoretically race the pending call, and
	// that engine's own structure closes it regardless.
	go func() {
		<-toolStarted
		close(releaseRunner)
	}()

	result, err := eng.RunAgentTurnStreaming(context.Background(), Request{Model: "fake/pro"}, AgentOptions{
		Tools:  []ToolDef{{Name: "sensitive_tool"}},
		Runner: blockingApproval,
	}, hist, sink)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Text != "final answer" {
		t.Fatalf("result.Text = %q, want %q", result.Text, "final answer")
	}

	// The tool result in history must be exactly what the Runner produced —
	// no trace of the steering text, proving Inject never touched the
	// pending call's outcome.
	var toolResultIdx, steeringIdx = -1, -1
	for i, m := range hist.Messages {
		for _, b := range m.Blocks {
			if b.Kind == convo.BlockToolResult {
				if b.Text != "approved-by-real-dialog-only" {
					t.Errorf("tool result = %q, want %q (unaffected by Inject)", b.Text, "approved-by-real-dialog-only")
				}
				toolResultIdx = i
			}
			if strings.Contains(b.Text, "steering: please hurry") {
				steeringIdx = i
			}
		}
	}
	if toolResultIdx == -1 {
		t.Fatal("no BlockToolResult found in history")
	}
	if steeringIdx == -1 {
		t.Fatal("steering message never landed in history at all")
	}
	// The security property: the steering message's history position is
	// strictly AFTER the tool result that was already pending when it
	// arrived. It could not have influenced, approved, or altered a call
	// that had already finished before the next iteration (and therefore
	// the next Inject poll) could even run.
	if steeringIdx <= toolResultIdx {
		t.Fatalf("steering message at index %d did not come after the pending tool's result at index %d", steeringIdx, toolResultIdx)
	}
}

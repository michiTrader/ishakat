package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichiTrader/ishakat/internal/convo"
)

// scriptedStreamer returns a different channel per call, driven by a slice of
// "scripts" — one per iteration of the agent loop. Each script is the exact
// sequence of Events the fake provider emits that iteration. This is the §12bis
// "fake Streamer that emits a tool call then a text answer", generalized so the
// same harness covers the cap, loop-detection and recovery cases.
type scriptedStreamer struct {
	mu       sync.Mutex
	scripts  [][]Event
	calls    int
	inspects []Request // record of each request the loop made
}

func (s *scriptedStreamer) stream(ctx context.Context, req Request) (<-chan Event, error) {
	s.mu.Lock()
	idx := s.calls
	s.calls++
	s.inspects = append(s.inspects, req)
	s.mu.Unlock()

	if idx >= len(s.scripts) {
		// Past the last script: emit an empty text turn so the loop terminates
		// naturally rather than deadlocking. A test that does not expect this
		// can check s.calls to assert how many iterations ran.
		return chanOf(Event{Kind: EventDone}), nil
	}
	return chanOf(s.scripts[idx]...), nil
}

func (s *scriptedStreamer) requestAt(i int) Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i < 0 || i >= len(s.inspects) {
		return Request{}
	}
	return s.inspects[i]
}

func (s *scriptedStreamer) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// fakeRunner is the §12bis "fake ToolRunner": it returns a canned result per
// tool name, and records every call so a test can assert what ran and in what
// order.
type fakeRunner struct {
	mu      sync.Mutex
	calls   []fakeToolCall
	results map[string]ToolResult
}

type fakeToolCall struct {
	name string
	args json.RawMessage
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{results: map[string]ToolResult{}}
}

func (r *fakeRunner) run(ctx context.Context, name string, args json.RawMessage) (ToolResult, error) {
	r.mu.Lock()
	r.calls = append(r.calls, fakeToolCall{name: name, args: args})
	res, ok := r.results[name]
	r.mu.Unlock()
	if !ok {
		return ToolResult{Text: "ok", IsError: false}, nil
	}
	return res, nil
}

func (r *fakeRunner) callNames() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.calls))
	for i, c := range r.calls {
		out[i] = c.name
	}
	return out
}

func (r *fakeRunner) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// toolCallEvent is a shortcut for the most common Event in these tests.
func toolCallEvent(id, name string, args string) Event {
	return Event{Kind: EventToolCall, ID: id, Name: name, Args: json.RawMessage(args)}
}

func deltaEvent(s string) Event { return Event{Kind: EventDelta, Text: s} }
func doneEvent() Event          { return Event{Kind: EventDone} }

func TestRunAgentTurnStopsAtCostBudgetBeforeTool(t *testing.T) {
	ss := &scriptedStreamer{scripts: [][]Event{
		{{Kind: EventUsage, Usage: &convo.Usage{In: 1000, Out: 1000}}, toolCallEvent("c1", "list", `{}`), doneEvent()},
	}}
	runner := newFakeRunner()
	history := convo.Conversation{}
	result, err := (&Engine{stream: ss.stream}).RunAgentTurn(context.Background(), Request{Model: "model"}, AgentOptions{
		Tools:          []ToolDef{{Name: "list"}},
		Runner:         runner.run,
		BudgetUSD:      0.01,
		InputCostUSD:   5,
		OutputCostUSD:  5,
		MaxOutputBytes: -1,
	}, &history)
	if err != nil {
		t.Fatalf("RunAgentTurn: %v", err)
	}
	if result.CostUSD != 0.01 {
		t.Fatalf("CostUSD = %f, want 0.01", result.CostUSD)
	}
	if result.Stopped == "" || !strings.Contains(result.Stopped, "cost budget reached") {
		t.Fatalf("Stopped = %q, want cost-budget explanation", result.Stopped)
	}
	if runner.callCount() != 0 {
		t.Fatalf("tool calls = %d, want budget to stop before execution", runner.callCount())
	}
	if len(history.Messages) != 2 {
		t.Fatalf("history messages = %d, want assistant plus synthetic result", len(history.Messages))
	}
}

// TestRunAgentTurnToolCallThenAnswer is the closing-criterion case: a fake
// provider that emits a tool call, then (after the result is fed back) a text
// answer. The loop has to run the tool, append the result, iterate, and return
// the final text.
func TestRunAgentTurnToolCallThenAnswer(t *testing.T) {
	ss := &scriptedStreamer{
		scripts: [][]Event{
			{toolCallEvent("c1", "list", `{"dir":"."}`), doneEvent()},
			{deltaEvent("the directory contains: a.go, b.go"), doneEvent()},
		},
	}
	runner := newFakeRunner()
	runner.results["list"] = ToolResult{Text: "a.go\nb.go"}

	eng := New(ss.stream, 0)
	hist := &convo.Conversation{}
	hist.Add(convo.User("what files are in this directory"))

	res, err := eng.RunAgentTurn(context.Background(), Request{
		Model:    "fake/pro",
		Messages: hist.Active(),
	}, AgentOptions{
		Tools:  []ToolDef{{Name: "list", Description: "list files"}},
		Runner: runner.run,
	}, hist)
	if err != nil {
		t.Fatalf("RunAgentTurn: unexpected error: %v", err)
	}
	if res.Aborted {
		t.Fatal("turn should not be aborted")
	}
	if res.Stopped != "" {
		t.Fatalf("turn should terminate naturally, got Stopped=%q", res.Stopped)
	}
	if !strings.Contains(res.Text, "a.go") || !strings.Contains(res.Text, "b.go") {
		t.Errorf("final text should mention the files the tool returned, got %q", res.Text)
	}
	if res.Calls != 1 {
		t.Errorf("Calls = %d, want 1", res.Calls)
	}
	if got := runner.callNames(); len(got) != 1 || got[0] != "list" {
		t.Errorf("runner calls = %v, want [list]", got)
	}

	// The history must contain the assistant's tool_call, the tool result, and
	// the final assistant text — in that order, so the next request the loop
	// built would serialize them correctly.
	msgs := hist.Messages
	// msgs[0] = user, msgs[1] = assistant(tool_call), msgs[2] = tool result, msgs[3] = assistant(text)
	if len(msgs) != 4 {
		t.Fatalf("history has %d messages, want 4", len(msgs))
	}
	if !msgs[1].Has(convo.BlockToolCall) {
		t.Error("message 1 should carry a BlockToolCall")
	}
	if msgs[2].Role != convo.RoleTool || !msgs[2].Has(convo.BlockToolResult) {
		t.Error("message 2 should be a role:tool with a BlockToolResult")
	}
	if !msgs[2].Has(convo.BlockToolResult) {
		t.Error("message 2 should carry a BlockToolResult")
	}
	// The tool_call_id must round-trip: the BlockToolCall and the BlockToolResult
	// carry the same id the provider assigned.
	var callID, resultID string
	for _, b := range msgs[1].Blocks {
		if b.Kind == convo.BlockToolCall {
			callID = b.ToolCallID
		}
	}
	for _, b := range msgs[2].Blocks {
		if b.Kind == convo.BlockToolResult {
			resultID = b.ToolCallID
		}
	}
	if callID != "c1" {
		t.Errorf("BlockToolCall.ToolCallID = %q, want %q", callID, "c1")
	}
	if resultID != "c1" {
		t.Errorf("BlockToolResult.ToolCallID = %q, want %q", resultID, "c1")
	}
}

// TestRunAgentTurnNoToolsRunsOnce verifies the safe fallback: with no tools or
// no runner, the loop runs exactly one iteration and behaves like
// RunToCompletion.
func TestRunAgentTurnNoToolsRunsOnce(t *testing.T) {
	ss := &scriptedStreamer{
		scripts: [][]Event{
			{deltaEvent("hello"), doneEvent()},
		},
	}
	eng := New(ss.stream, 0)
	hist := &convo.Conversation{}
	hist.Add(convo.User("hi"))

	res, err := eng.RunAgentTurn(context.Background(), Request{
		Model:    "fake/pro",
		Messages: hist.Active(),
	}, AgentOptions{}, hist)
	if err != nil {
		t.Fatalf("RunAgentTurn: %v", err)
	}
	if res.Text != "hello" {
		t.Errorf("Text = %q, want %q", res.Text, "hello")
	}
	if ss.callCount() != 1 {
		t.Errorf("stream called %d times, want 1", ss.callCount())
	}
	if res.Calls != 0 {
		t.Errorf("Calls = %d, want 0", res.Calls)
	}
}

// TestRunAgentTurnCapFires uses a streamer that always emits a tool call. The
// hard cap must end the turn with a non-empty Stopped reason, and the runner
// must have been called at most MaxToolCalls times. Each iteration emits
// distinct arguments so loop detection (same name + byte-identical args) does
// not fire before the cap — the cap is the thing under test here.
func TestRunAgentTurnCapFires(t *testing.T) {
	ss := &scriptedStreamer{
		scripts: [][]Event{
			{toolCallEvent("c1", "repeat", `{"i":1}`), doneEvent()},
			{toolCallEvent("c2", "repeat", `{"i":2}`), doneEvent()},
			{toolCallEvent("c3", "repeat", `{"i":3}`), doneEvent()},
			{toolCallEvent("c4", "repeat", `{"i":4}`), doneEvent()},
			{toolCallEvent("c5", "repeat", `{"i":5}`), doneEvent()},
		},
	}
	runner := newFakeRunner()

	eng := New(ss.stream, 0)
	hist := &convo.Conversation{}
	hist.Add(convo.User("loop"))

	res, err := eng.RunAgentTurn(context.Background(), Request{
		Model:    "fake/pro",
		Messages: hist.Active(),
	}, AgentOptions{
		Tools:        []ToolDef{{Name: "repeat", Description: "d"}},
		Runner:       runner.run,
		MaxToolCalls: 3,
	}, hist)
	if err != nil {
		t.Fatalf("RunAgentTurn: %v", err)
	}
	if res.Stopped == "" {
		t.Fatal("cap should produce a non-empty Stopped reason")
	}
	if !strings.Contains(res.Stopped, "cap") {
		t.Errorf("Stopped should mention the cap, got %q", res.Stopped)
	}
	if runner.callCount() > 3 {
		t.Errorf("runner called %d times, should not exceed the cap of 3", runner.callCount())
	}
}

// TestRunAgentTurnLoopDetectionFires proves the same tool name with
// byte-identical arguments twice in a row stops the loop.
func TestRunAgentTurnLoopDetectionFires(t *testing.T) {
	// Use distinct ids so only the name+args trigger fires, not the cap.
	scripts := [][]Event{
		{toolCallEvent("c1", "grep", `{"q":"foo"}`), doneEvent()},
		{toolCallEvent("c2", "grep", `{"q":"foo"}`), doneEvent()},
	}
	ss := &scriptedStreamer{scripts: scripts}
	runner := newFakeRunner()

	eng := New(ss.stream, 0)
	hist := &convo.Conversation{}
	hist.Add(convo.User("search"))

	res, err := eng.RunAgentTurn(context.Background(), Request{
		Model:    "fake/pro",
		Messages: hist.Active(),
	}, AgentOptions{
		Tools:        []ToolDef{{Name: "grep", Description: "d"}},
		Runner:       runner.run,
		MaxToolCalls: 25,
	}, hist)
	if err != nil {
		t.Fatalf("RunAgentTurn: %v", err)
	}
	if res.Stopped == "" {
		t.Fatal("loop detection should produce a non-empty Stopped reason")
	}
	if !strings.Contains(res.Stopped, "loop") {
		t.Errorf("Stopped should mention the loop, got %q", res.Stopped)
	}
	if !strings.Contains(res.Stopped, "grep") {
		t.Errorf("Stopped should name the tool, got %q", res.Stopped)
	}
	// The second identical call must NOT have run — loop detection stops
	// before executing it.
	if runner.callCount() != 1 {
		t.Errorf("runner called %d times, want 1 (the second identical call is stopped before running)", runner.callCount())
	}
}

// TestRunAgentTurnLoopDetectionDifferentArgsDoesNotFire proves that the same
// tool with different arguments is allowed: loop detection is byte-identical,
// not name-identical.
func TestRunAgentTurnLoopDetectionDifferentArgsDoesNotFire(t *testing.T) {
	scripts := [][]Event{
		{toolCallEvent("c1", "grep", `{"q":"foo"}`), doneEvent()},
		{toolCallEvent("c2", "grep", `{"q":"bar"}`), doneEvent()},
		{deltaEvent("done"), doneEvent()},
	}
	ss := &scriptedStreamer{scripts: scripts}
	runner := newFakeRunner()

	eng := New(ss.stream, 0)
	hist := &convo.Conversation{}
	hist.Add(convo.User("search twice"))

	res, err := eng.RunAgentTurn(context.Background(), Request{
		Model:    "fake/pro",
		Messages: hist.Active(),
	}, AgentOptions{
		Tools:  []ToolDef{{Name: "grep", Description: "d"}},
		Runner: runner.run,
	}, hist)
	if err != nil {
		t.Fatalf("RunAgentTurn: %v", err)
	}
	if res.Stopped != "" {
		t.Errorf("different args should not trigger loop detection, got Stopped=%q", res.Stopped)
	}
	if runner.callCount() != 2 {
		t.Errorf("runner called %d times, want 2", runner.callCount())
	}
}

// TestRunAgentTurnLoopDetectionParallelSameCallDoesNotFire is the regression
// test for Bug 4: two calls with the *same* tool name and byte-identical
// arguments issued *in parallel, in the same batch* are a single decision by
// the model, not a retry loop, and must both run. Before the fix, the loop
// updated lastToolName/lastToolArgs after every call in the for-loop
// (including calls within the same batch) and compared unconditionally, so
// c2 here would be falsely flagged as repeating c1 even though they arrived
// in the very same tool_calls batch. The fix restricts the comparison to a
// batch's first call (i == 0) against the previous iteration's last call.
func TestRunAgentTurnLoopDetectionParallelSameCallDoesNotFire(t *testing.T) {
	ss := &scriptedStreamer{
		scripts: [][]Event{
			{
				toolCallEvent("c1", "grep", `{"q":"foo"}`),
				toolCallEvent("c2", "grep", `{"q":"foo"}`), // identical to c1, but same batch: legitimate parallel call
				doneEvent(),
			},
			{deltaEvent("done"), doneEvent()},
		},
	}
	runner := newFakeRunner()

	eng := New(ss.stream, 0)
	hist := &convo.Conversation{}
	hist.Add(convo.User("search in parallel"))

	res, err := eng.RunAgentTurn(context.Background(), Request{
		Model:    "fake/pro",
		Messages: hist.Active(),
	}, AgentOptions{
		Tools:  []ToolDef{{Name: "grep", Description: "d"}},
		Runner: runner.run,
	}, hist)
	if err != nil {
		t.Fatalf("RunAgentTurn: %v", err)
	}
	if res.Stopped != "" {
		t.Errorf("identical calls within one parallel batch should not trigger loop detection, got Stopped=%q", res.Stopped)
	}
	if runner.callCount() != 2 {
		t.Errorf("runner called %d times, want 2 (both c1 and c2 should run — they are parallel, not a retry)", runner.callCount())
	}
}

// TestRunAgentTurnLoopDetectionAcrossIterationsFires is the companion
// regression test for Bug 4's other half: the comparison must still fire
// across iteration boundaries — a batch's first call repeating the *previous
// iteration's last* call is exactly the stuck-loop shape loop detection
// exists to catch, and the Bug 4 fix (restricting the check to i == 0) must
// not have accidentally disabled it.
func TestRunAgentTurnLoopDetectionAcrossIterationsFires(t *testing.T) {
	ss := &scriptedStreamer{
		scripts: [][]Event{
			{toolCallEvent("c1", "grep", `{"q":"foo"}`), doneEvent()},
			{toolCallEvent("c2", "grep", `{"q":"foo"}`), doneEvent()}, // repeats c1 across the iteration boundary
		},
	}
	runner := newFakeRunner()

	eng := New(ss.stream, 0)
	hist := &convo.Conversation{}
	hist.Add(convo.User("search"))

	res, err := eng.RunAgentTurn(context.Background(), Request{
		Model:    "fake/pro",
		Messages: hist.Active(),
	}, AgentOptions{
		Tools:        []ToolDef{{Name: "grep", Description: "d"}},
		Runner:       runner.run,
		MaxToolCalls: 25,
	}, hist)
	if err != nil {
		t.Fatalf("RunAgentTurn: %v", err)
	}
	if res.Stopped == "" {
		t.Fatal("loop detection should still fire across iteration boundaries")
	}
	if runner.callCount() != 1 {
		t.Errorf("runner called %d times, want 1 (c2 repeats c1 across iterations and must not run)", runner.callCount())
	}
}

// TestRunAgentTurnToolErrorIsData proves that a tool returning an error becomes
// a BlockToolResult with IsError in the history, and the model recovers on the
// next iteration with a text answer.
func TestRunAgentTurnToolErrorIsData(t *testing.T) {
	scripts := [][]Event{
		{toolCallEvent("c1", "bash", `{"cmd":"ls /nope"}`), doneEvent()},
		{deltaEvent("the directory does not exist, try another path"), doneEvent()},
	}
	ss := &scriptedStreamer{scripts: scripts}
	runner := newFakeRunner()
	runner.results["bash"] = ToolResult{Text: "ls: /nope: No such file or directory", IsError: true}

	eng := New(ss.stream, 0)
	hist := &convo.Conversation{}
	hist.Add(convo.User("list /nope"))

	res, err := eng.RunAgentTurn(context.Background(), Request{
		Model:    "fake/pro",
		Messages: hist.Active(),
	}, AgentOptions{
		Tools:  []ToolDef{{Name: "bash", Description: "d"}},
		Runner: runner.run,
	}, hist)
	if err != nil {
		t.Fatalf("RunAgentTurn: %v", err)
	}
	if res.Stopped != "" {
		t.Errorf("a tool error should not stop the loop, got Stopped=%q", res.Stopped)
	}
	if !strings.Contains(res.Text, "does not exist") {
		t.Errorf("model should recover after seeing the error, got %q", res.Text)
	}
	// The history must carry the error as IsError so the dialect serializes it
	// faithfully.
	var sawErrorResult bool
	for _, m := range hist.Messages {
		if m.Role != convo.RoleTool {
			continue
		}
		for _, b := range m.Blocks {
			if b.Kind == convo.BlockToolResult && b.IsError {
				sawErrorResult = true
			}
		}
	}
	if !sawErrorResult {
		t.Error("history should contain a BlockToolResult with IsError=true")
	}
}

// TestRunAgentTurnRunnerErrorIsData proves that a runner returning a Go error
// (as opposed to a tool returning an IsError result) also becomes data: the
// model sees it and reacts.
func TestRunAgentTurnRunnerErrorIsData(t *testing.T) {
	scripts := [][]Event{
		{toolCallEvent("c1", "missing", `{}`), doneEvent()},
		{deltaEvent("that tool is not available, let me try another way"), doneEvent()},
	}
	ss := &scriptedStreamer{scripts: scripts}
	runner := &errorRunner{err: errors.New("tool not bound: missing")}

	eng := New(ss.stream, 0)
	hist := &convo.Conversation{}
	hist.Add(convo.User("use missing"))

	res, err := eng.RunAgentTurn(context.Background(), Request{
		Model:    "fake/pro",
		Messages: hist.Active(),
	}, AgentOptions{
		Tools:  []ToolDef{{Name: "missing", Description: "d"}},
		Runner: runner.run,
	}, hist)
	if err != nil {
		t.Fatalf("RunAgentTurn: %v", err)
	}
	if !strings.Contains(res.Text, "another way") {
		t.Errorf("model should recover after the runner error, got %q", res.Text)
	}
}

type errorRunner struct{ err error }

func (r *errorRunner) run(ctx context.Context, name string, args json.RawMessage) (ToolResult, error) {
	return ToolResult{}, r.err
}

// TestRunAgentTurnNilRunnerHallucinatedToolCallDoesNotPanic is the regression
// test for Bug 1: AgentOptions.Runner's own doc comment promises that a
// model producing a tool call with no runner bound "reports it as a tool
// error in the context rather than crashing". hasTools clears opts.Tools
// when Runner is nil, but a real model can still hallucinate a tool_call it
// was never offered — this is exactly the scripted case: the streamer emits
// one anyway. Before the fix, opts.Runner(ctx, ...) was called unconditionally
// and this test panicked with a nil pointer dereference.
func TestRunAgentTurnNilRunnerHallucinatedToolCallDoesNotPanic(t *testing.T) {
	ss := &scriptedStreamer{
		scripts: [][]Event{
			// The model calls a tool despite Tools/Runner both being unset —
			// req.Tools was empty, so this is a hallucination, not a legitimate
			// request the loop offered.
			{toolCallEvent("c1", "ghost", `{}`), doneEvent()},
			{deltaEvent("sorry, I made that up"), doneEvent()},
		},
	}

	eng := New(ss.stream, 0)
	hist := &convo.Conversation{}
	hist.Add(convo.User("hi"))

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("RunAgentTurn panicked: %v", r)
		}
	}()

	res, err := eng.RunAgentTurn(context.Background(), Request{
		Model:    "fake/pro",
		Messages: hist.Active(),
	}, AgentOptions{ /* zero value: no Tools, no Runner */ }, hist)
	if err != nil {
		t.Fatalf("RunAgentTurn: unexpected error: %v", err)
	}
	if !strings.Contains(res.Text, "made that up") {
		t.Errorf("model should recover after the synthetic tool error, got %q", res.Text)
	}

	// The hallucinated call must still get a BlockToolResult (IsError), so
	// the history stays valid to re-serialize.
	var sawErrorResult bool
	for _, m := range hist.Messages {
		if m.Role != convo.RoleTool {
			continue
		}
		for _, b := range m.Blocks {
			if b.Kind == convo.BlockToolResult && b.IsError && b.ToolCallID == "c1" {
				sawErrorResult = true
			}
		}
	}
	if !sawErrorResult {
		t.Error("history should contain a BlockToolResult(IsError) for the hallucinated call c1")
	}
}

// TestRunAgentTurnCapMidBatchLeavesNoOrphanedToolCall is the regression test
// for Bug 2 (the cap path): when the cap fires partway through a parallel
// batch of tool calls, every tool_calls entry on the assistant message
// already persisted must get a matching role:"tool" reply — including the
// ones that never ran — or the next request built from this history is
// invalid in the OpenAI dialect (a tool_calls entry with no corresponding
// tool message 400s the service).
func TestRunAgentTurnCapMidBatchLeavesNoOrphanedToolCall(t *testing.T) {
	ss := &scriptedStreamer{
		scripts: [][]Event{
			// One iteration, three parallel tool calls in the same batch.
			{
				toolCallEvent("c1", "repeat", `{"i":1}`),
				toolCallEvent("c2", "repeat", `{"i":2}`),
				toolCallEvent("c3", "repeat", `{"i":3}`),
				doneEvent(),
			},
		},
	}
	runner := newFakeRunner()

	eng := New(ss.stream, 0)
	hist := &convo.Conversation{}
	hist.Add(convo.User("batch"))

	res, err := eng.RunAgentTurn(context.Background(), Request{
		Model:    "fake/pro",
		Messages: hist.Active(),
	}, AgentOptions{
		Tools:        []ToolDef{{Name: "repeat", Description: "d"}},
		Runner:       runner.run,
		MaxToolCalls: 1,
	}, hist)
	if err != nil {
		t.Fatalf("RunAgentTurn: %v", err)
	}
	if res.Stopped == "" {
		t.Fatal("cap should produce a non-empty Stopped reason")
	}

	// The assistant message must carry all three tool_calls (c1, c2, c3):
	// find it and collect every ToolCallID it announced.
	var announced []string
	for _, m := range hist.Messages {
		if m.Role != convo.RoleAssistant {
			continue
		}
		for _, b := range m.Blocks {
			if b.Kind == convo.BlockToolCall {
				announced = append(announced, b.ToolCallID)
			}
		}
	}
	if len(announced) != 3 {
		t.Fatalf("assistant message should announce 3 tool_calls, got %v", announced)
	}

	// Every announced id must have a matching role:"tool" reply somewhere in
	// history — this is the invariant Bug 2 broke.
	replied := map[string]bool{}
	for _, m := range hist.Messages {
		if m.Role != convo.RoleTool {
			continue
		}
		for _, b := range m.Blocks {
			if b.Kind == convo.BlockToolResult {
				replied[b.ToolCallID] = true
			}
		}
	}
	for _, id := range announced {
		if !replied[id] {
			t.Errorf("tool_call %q has no matching role:tool reply — this orphans the next request", id)
		}
	}
}

// TestRunAgentTurnLoopDetectionMidBatchLeavesNoOrphanedToolCall is the
// regression test for Bug 2 (the loop-detection path), reshaped for the Bug 4
// fix: loop detection now only ever compares a batch's *first* call against
// the *previous iteration's last* call, never against an earlier call within
// the same batch — see agentloop.go's i == 0 guard. So this scripts two
// iterations: the first is a batch of two (c1, c2) that both run; the second
// iteration's first call, c3, repeats c2's name+args exactly (c2 was the
// previous iteration's last call) — that fires loop detection before c3
// runs, and c4 (queued right after it in the same batch) must still be
// closed out even though it was never even considered.
func TestRunAgentTurnLoopDetectionMidBatchLeavesNoOrphanedToolCall(t *testing.T) {
	ss := &scriptedStreamer{
		scripts: [][]Event{
			{
				toolCallEvent("c1", "grep", `{"q":"bar"}`),
				toolCallEvent("c2", "grep", `{"q":"foo"}`),
				doneEvent(),
			},
			{
				toolCallEvent("c3", "grep", `{"q":"foo"}`), // repeats c2 (previous iteration's last call): fires loop detection
				toolCallEvent("c4", "grep", `{"q":"baz"}`), // never reached
				doneEvent(),
			},
		},
	}
	runner := newFakeRunner()

	eng := New(ss.stream, 0)
	hist := &convo.Conversation{}
	hist.Add(convo.User("batch search"))

	res, err := eng.RunAgentTurn(context.Background(), Request{
		Model:    "fake/pro",
		Messages: hist.Active(),
	}, AgentOptions{
		Tools:        []ToolDef{{Name: "grep", Description: "d"}},
		Runner:       runner.run,
		MaxToolCalls: 25,
	}, hist)
	if err != nil {
		t.Fatalf("RunAgentTurn: %v", err)
	}
	if res.Stopped == "" {
		t.Fatal("loop detection should produce a non-empty Stopped reason")
	}
	if runner.callCount() != 2 {
		t.Errorf("runner called %d times, want 2 (c1 and c2 should have run; c3 is caught by loop detection, c4 never considered)", runner.callCount())
	}

	announced := map[string]bool{}
	for _, m := range hist.Messages {
		if m.Role != convo.RoleAssistant {
			continue
		}
		for _, b := range m.Blocks {
			if b.Kind == convo.BlockToolCall {
				announced[b.ToolCallID] = true
			}
		}
	}
	replied := map[string]bool{}
	for _, m := range hist.Messages {
		if m.Role != convo.RoleTool {
			continue
		}
		for _, b := range m.Blocks {
			if b.Kind == convo.BlockToolResult {
				replied[b.ToolCallID] = true
			}
		}
	}
	for id := range announced {
		if !replied[id] {
			t.Errorf("tool_call %q has no matching role:tool reply — this orphans the next request", id)
		}
	}
}

// TestRunAgentTurnCancellationMidBatchLeavesNoOrphanedToolCall is the
// regression test for Bug 2 (the cancellation path): the batch has two
// calls; the runner blocks on the first, and cancelling while it is running
// must close out both — the one that was cancelled in flight and the one
// that never got a chance to start.
func TestRunAgentTurnCancellationMidBatchLeavesNoOrphanedToolCall(t *testing.T) {
	scripts := [][]Event{
		{
			toolCallEvent("c1", "slow", `{}`),
			toolCallEvent("c2", "slow", `{"other":true}`),
			doneEvent(),
		},
	}
	ss := &scriptedStreamer{scripts: scripts}
	runner := &blockingRunner{}

	ctx, cancel := context.WithCancel(context.Background())
	eng := New(ss.stream, 0)
	hist := &convo.Conversation{}
	hist.Add(convo.User("run slow batch"))

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = eng.RunAgentTurn(ctx, Request{
			Model:    "fake/pro",
			Messages: hist.Active(),
		}, AgentOptions{
			Tools:  []ToolDef{{Name: "slow", Description: "d"}},
			Runner: runner.run,
		}, hist)
	}()

	runner.waitEntered(time.Second)
	cancel()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("RunAgentTurn did not return after cancellation")
	}

	announced := map[string]bool{}
	for _, m := range hist.Messages {
		if m.Role != convo.RoleAssistant {
			continue
		}
		for _, b := range m.Blocks {
			if b.Kind == convo.BlockToolCall {
				announced[b.ToolCallID] = true
			}
		}
	}
	replied := map[string]bool{}
	for _, m := range hist.Messages {
		if m.Role != convo.RoleTool {
			continue
		}
		for _, b := range m.Blocks {
			if b.Kind == convo.BlockToolResult {
				replied[b.ToolCallID] = true
			}
		}
	}
	if len(announced) != 2 {
		t.Fatalf("assistant message should announce 2 tool_calls, got %v", announced)
	}
	for id := range announced {
		if !replied[id] {
			t.Errorf("tool_call %q has no matching role:tool reply — this orphans the next request", id)
		}
	}
}

// TestRunAgentTurnCancellationMidTool proves that cancelling ctx while a tool
// is running aborts the turn, marks the result Aborted, and persists a tool
// error in the history — never leaving the loop hanging.
func TestRunAgentTurnCancellationMidTool(t *testing.T) {
	scripts := [][]Event{
		{toolCallEvent("c1", "slow", `{}`), doneEvent()},
	}
	ss := &scriptedStreamer{scripts: scripts}
	runner := &blockingRunner{}

	ctx, cancel := context.WithCancel(context.Background())
	eng := New(ss.stream, 0)
	hist := &convo.Conversation{}
	hist.Add(convo.User("run slow"))

	// Start the turn in a goroutine; cancel once the runner is inside the tool.
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = eng.RunAgentTurn(ctx, Request{
			Model:    "fake/pro",
			Messages: hist.Active(),
		}, AgentOptions{
			Tools:  []ToolDef{{Name: "slow", Description: "d"}},
			Runner: runner.run,
		}, hist)
	}()

	// Wait for the runner to be entered, then cancel.
	runner.waitEntered(time.Second)
	cancel()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("RunAgentTurn did not return after cancellation")
	}

	// The history should contain a tool result marked as an error (the
	// cancelled tool run), so --resume is faithful.
	var sawCancelledResult bool
	for _, m := range hist.Messages {
		if m.Role != convo.RoleTool {
			continue
		}
		for _, b := range m.Blocks {
			if b.Kind == convo.BlockToolResult && b.IsError && strings.Contains(b.Text, "cancelled") {
				sawCancelledResult = true
			}
		}
	}
	if !sawCancelledResult {
		t.Error("history should contain a BlockToolResult marking the cancelled tool run")
	}
}

// blockingRunner is a ToolRunner that blocks until its context is cancelled,
// then returns. It signals entered so the test can cancel after the tool has
// started, which is the case §12bis cares about (a tool already running).
type blockingRunner struct {
	entered chan struct{}
	once    sync.Once
}

func (r *blockingRunner) waitEntered(budget time.Duration) {
	r.once.Do(func() { r.entered = make(chan struct{}) })
	select {
	case <-r.entered:
	case <-time.After(budget):
	}
}

func (r *blockingRunner) run(ctx context.Context, name string, args json.RawMessage) (ToolResult, error) {
	r.once.Do(func() { r.entered = make(chan struct{}) })
	close(r.entered)
	<-ctx.Done()
	return ToolResult{}, ctx.Err()
}

// TestRunAgentTurnCancellationBeforeTool proves that cancelling before the
// tool starts still produces an Aborted result and a faithful history.
func TestRunAgentTurnCancellationBeforeTool(t *testing.T) {
	scripts := [][]Event{
		{toolCallEvent("c1", "any", `{}`), doneEvent()},
	}
	ss := &scriptedStreamer{scripts: scripts}
	runner := newFakeRunner()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before the call

	eng := New(ss.stream, 0)
	hist := &convo.Conversation{}
	hist.Add(convo.User("hi"))

	res, err := eng.RunAgentTurn(ctx, Request{
		Model:    "fake/pro",
		Messages: hist.Active(),
	}, AgentOptions{
		Tools:  []ToolDef{{Name: "any", Description: "d"}},
		Runner: runner.run,
	}, hist)
	if err == nil {
		t.Fatal("expected a cancelled error, got nil")
	}
	if !res.Aborted {
		t.Error("result should be marked Aborted")
	}
	if runner.callCount() != 0 {
		t.Errorf("runner called %d times, want 0 (cancelled before any tool ran)", runner.callCount())
	}
}

// TestRunAgentTurnOutputTruncation proves a tool result over MaxOutputBytes is
// clipped with an explicit marker naming how much was dropped.
func TestRunAgentTurnOutputTruncation(t *testing.T) {
	big := strings.Repeat("x", 5000)
	scripts := [][]Event{
		{toolCallEvent("c1", "bash", `{}`), doneEvent()},
		{deltaEvent("ok"), doneEvent()},
	}
	ss := &scriptedStreamer{scripts: scripts}
	runner := newFakeRunner()
	runner.results["bash"] = ToolResult{Text: big}

	eng := New(ss.stream, 0)
	hist := &convo.Conversation{}
	hist.Add(convo.User("run noisy"))

	_, err := eng.RunAgentTurn(context.Background(), Request{
		Model:    "fake/pro",
		Messages: hist.Active(),
	}, AgentOptions{
		Tools:          []ToolDef{{Name: "bash", Description: "d"}},
		Runner:         runner.run,
		MaxOutputBytes: 100,
	}, hist)
	if err != nil {
		t.Fatalf("RunAgentTurn: %v", err)
	}

	// Find the tool result in the history and check it was truncated.
	for _, m := range hist.Messages {
		if m.Role != convo.RoleTool {
			continue
		}
		for _, b := range m.Blocks {
			if b.Kind != convo.BlockToolResult {
				continue
			}
			if !strings.Contains(b.Text, "truncated") {
				t.Errorf("truncated result should carry the marker, got %q", b.Text)
			}
			if len(b.Text) > 200 {
				t.Errorf("truncated result should be ~100 bytes + marker, got %d bytes", len(b.Text))
			}
		}
	}
}

// TestRunAgentTurnHistoryGrowsEachIteration verifies the loop rebuilds the
// request with the grown history: the second iteration's request must contain
// the tool call and result the first iteration produced.
func TestRunAgentTurnHistoryGrowsEachIteration(t *testing.T) {
	scripts := [][]Event{
		{toolCallEvent("c1", "list", `{}`), doneEvent()},
		{deltaEvent("done"), doneEvent()},
	}
	ss := &scriptedStreamer{scripts: scripts}
	runner := newFakeRunner()

	eng := New(ss.stream, 0)
	hist := &convo.Conversation{}
	hist.Add(convo.User("list"))

	_, err := eng.RunAgentTurn(context.Background(), Request{
		Model:    "fake/pro",
		Messages: hist.Active(),
	}, AgentOptions{
		Tools:  []ToolDef{{Name: "list", Description: "d"}},
		Runner: runner.run,
	}, hist)
	if err != nil {
		t.Fatalf("RunAgentTurn: %v", err)
	}

	if ss.callCount() != 2 {
		t.Fatalf("stream called %d times, want 2", ss.callCount())
	}
	second := ss.requestAt(1)
	// The second request must include the assistant tool_call and the tool
	// result that the first iteration appended.
	var sawToolCall, sawToolResult bool
	for _, m := range second.Messages {
		if m.Has(convo.BlockToolCall) {
			sawToolCall = true
		}
		if m.Role == convo.RoleTool && m.Has(convo.BlockToolResult) {
			sawToolResult = true
		}
	}
	if !sawToolCall {
		t.Error("second iteration's request should contain the assistant's BlockToolCall")
	}
	if !sawToolResult {
		t.Error("second iteration's request should contain the tool's BlockToolResult")
	}
}

// TestRunAgentTurnMidStreamError proves a mid-stream EventError ends the loop
// and returns the partial text, matching run()'s contract for a text turn.
func TestRunAgentTurnMidStreamError(t *testing.T) {
	ss := &scriptedStreamer{
		scripts: [][]Event{
			{deltaEvent("partial"), {Kind: EventError, Err: errors.New("connection reset")}, doneEvent()},
		},
	}
	runner := newFakeRunner()

	eng := New(ss.stream, 0)
	hist := &convo.Conversation{}
	hist.Add(convo.User("hi"))

	res, err := eng.RunAgentTurn(context.Background(), Request{
		Model:    "fake/pro",
		Messages: hist.Active(),
	}, AgentOptions{
		Tools:  []ToolDef{{Name: "any", Description: "d"}},
		Runner: runner.run,
	}, hist)
	if err == nil {
		t.Fatal("expected the mid-stream error to be returned")
	}
	if !strings.Contains(err.Error(), "connection reset") {
		t.Errorf("error should be the stream error, got %v", err)
	}
	if res.Text != "partial" {
		t.Errorf("partial text should be returned, got %q", res.Text)
	}
	if runner.callCount() != 0 {
		t.Errorf("runner should not have been called, got %d", runner.callCount())
	}
}

// --- §21.9 fix 1: a refusal ends the turn ------------------------------------

// deniedErr is a runner error carrying the Denied() contract, standing in for
// what internal/permissions produces. It is defined here rather than imported
// so this test proves the *structural* match works — engine must recognize any
// error satisfying the interface, not one particular concrete type.
type deniedErr struct{ msg string }

func (e deniedErr) Error() string { return e.msg }
func (e deniedErr) Denied() bool  { return true }

// TestRunAgentTurnRefusalEndsTheTurn is closing criterion 1 at the engine
// level: a turn in which a call is refused makes exactly ONE provider request.
//
// The scripted streamer would happily serve a second iteration (the model
// "reacting" to the refusal). That second request is the defect, not the
// recovery: each variant the model tries carries the whole grown history, and
// a real user was rate-limited off their account this way
// (docs/BUG-rate-limit-amplifier.md).
func TestRunAgentTurnRefusalEndsTheTurn(t *testing.T) {
	ss := &scriptedStreamer{scripts: [][]Event{
		{toolCallEvent("c1", "bash", `{"command":"rm -rf build"}`), doneEvent()},
		{deltaEvent("Let me try a different way."), doneEvent()},
	}}

	eng := New(ss.stream, 0)
	hist := &convo.Conversation{}
	hist.Add(convo.User("clean the build"))

	res, err := eng.RunAgentTurn(context.Background(), Request{
		Model:    "fake/pro",
		Messages: hist.Active(),
	}, AgentOptions{
		Tools: []ToolDef{{Name: "bash", Description: "d"}},
		Runner: func(context.Context, string, json.RawMessage) (ToolResult, error) {
			return ToolResult{}, deniedErr{msg: "tool permission denied: user declined bash"}
		},
	}, hist)

	// A refusal is a normal outcome of a turn, not an engine failure: the
	// caller renders Stopped, it does not handle an error.
	if err != nil {
		t.Fatalf("a refusal must not surface as an engine error: %v", err)
	}
	if got := ss.callCount(); got != 1 {
		t.Errorf("provider requests = %d, want exactly 1: the loop bought the model another turn to route around the refusal", got)
	}
	if res.Stopped == "" {
		t.Fatal("a refused turn must report why it stopped")
	}
	if !strings.Contains(res.Stopped, "declined") {
		t.Errorf("Stopped = %q, want the refusal's own reason", res.Stopped)
	}
	if strings.Contains(res.Text, "different way") {
		t.Error("the model's second-turn text is present, so a second request was made")
	}

	// Bug 2: the refused call still needs a tool reply, or the assistant
	// message keeps an orphaned tool_call and every later request 400s.
	if !everyToolCallIsAnswered(hist) {
		t.Error("the refused tool call was left orphaned in history: the next request built from this session would 400 at the provider, permanently")
	}
}

// TestRunAgentTurnRefusalClosesOutTheWholeBatch covers the parallel case. A
// refusal mid-batch must still close out the calls after it, for the same
// reason the cap and cancellation paths do (Bug 2): one orphaned tool_call
// poisons the session, not merely the turn.
func TestRunAgentTurnRefusalClosesOutTheWholeBatch(t *testing.T) {
	ss := &scriptedStreamer{scripts: [][]Event{{
		toolCallEvent("c1", "bash", `{"command":"rm -rf build"}`),
		toolCallEvent("c2", "grep", `{"q":"foo"}`),
		toolCallEvent("c3", "grep", `{"q":"bar"}`),
		doneEvent(),
	}}}

	eng := New(ss.stream, 0)
	hist := &convo.Conversation{}
	hist.Add(convo.User("clean and search"))

	res, err := eng.RunAgentTurn(context.Background(), Request{
		Model:    "fake/pro",
		Messages: hist.Active(),
	}, AgentOptions{
		Tools: []ToolDef{{Name: "bash"}, {Name: "grep"}},
		Runner: func(_ context.Context, name string, _ json.RawMessage) (ToolResult, error) {
			if name == "bash" {
				return ToolResult{}, deniedErr{msg: "tool permission denied: user declined bash"}
			}
			return ToolResult{Text: "ok"}, nil
		},
	}, hist)
	if err != nil {
		t.Fatalf("RunAgentTurn: %v", err)
	}
	if res.Stopped == "" {
		t.Error("the turn must stop on the refusal even though later calls in the batch were harmless")
	}
	if !everyToolCallIsAnswered(hist) {
		t.Error("calls after the refused one were left orphaned: the next request would 400 permanently")
	}
}

// TestRunAgentTurnOrdinaryToolErrorStillIterates is the guard against fix 1
// overreaching. An ordinary runner failure must remain data the model reacts
// to — that is §12bis's error-is-data contract and the reason §3 needs no
// Planner. If this test ever fails, the loop stopped being reactive.
func TestRunAgentTurnOrdinaryToolErrorStillIterates(t *testing.T) {
	ss := &scriptedStreamer{scripts: [][]Event{
		{toolCallEvent("c1", "bash", `{"command":"make"}`), doneEvent()},
		{deltaEvent("make is missing; I used go build instead."), doneEvent()},
	}}

	eng := New(ss.stream, 0)
	hist := &convo.Conversation{}
	hist.Add(convo.User("build it"))

	res, err := eng.RunAgentTurn(context.Background(), Request{
		Model:    "fake/pro",
		Messages: hist.Active(),
	}, AgentOptions{
		Tools: []ToolDef{{Name: "bash", Description: "d"}},
		Runner: func(context.Context, string, json.RawMessage) (ToolResult, error) {
			return ToolResult{}, errors.New(`exec: "make": executable file not found in $PATH`)
		},
	}, hist)
	if err != nil {
		t.Fatalf("RunAgentTurn: %v", err)
	}
	if res.Stopped != "" {
		t.Errorf("Stopped = %q, want empty: an ordinary tool failure is data, not a reason to end the turn", res.Stopped)
	}
	if !strings.Contains(res.Text, "go build instead") {
		t.Errorf("text = %q, want the model's recovery: the loop must still hand ordinary failures back", res.Text)
	}
}

// everyToolCallIsAnswered checks the Bug 2 invariant: every BlockToolCall in
// the history has a tool message carrying its id.
func everyToolCallIsAnswered(h *convo.Conversation) bool {
	answered := map[string]bool{}
	for _, m := range h.Active() {
		if m.Role != convo.RoleTool {
			continue
		}
		for _, b := range m.Blocks {
			if b.ToolCallID != "" {
				answered[b.ToolCallID] = true
			}
		}
	}
	for _, m := range h.Active() {
		if m.Role != convo.RoleAssistant {
			continue
		}
		for _, b := range m.Blocks {
			if b.Kind == convo.BlockToolCall && !answered[b.ToolCallID] {
				return false
			}
		}
	}
	return true
}

// rateLimited is a fake 429: it satisfies retryHint structurally, exactly as
// provider.Error does, without this package importing internal/provider.
type rateLimited struct{ wait time.Duration }

func (e rateLimited) Error() string                { return "provider: HTTP 429" }
func (e rateLimited) Retry() (time.Duration, bool) { return e.wait, true }

// TestRunAgentTurnWaitsOutARateLimitAndResumes is closing criterion 2 of
// docs/BUG-rate-limit-amplifier.md, scaled down in time: a 429 carrying a
// Retry-After must make the loop wait out the window and then resume, rather
// than retrying immediately (which re-trips the limit) or failing the turn
// (which loses the user's work).
//
// The real criterion names 22 seconds. Asserting that literally would put a
// 22-second sleep in the test suite, so the wait is scaled and what gets
// pinned is the property, measured three ways: the turn survived, it did not
// come back early, and the wait was reported rather than silent.
func TestRunAgentTurnWaitsOutARateLimitAndResumes(t *testing.T) {
	const window = 250 * time.Millisecond

	var mu sync.Mutex
	var attempts int
	stream := func(ctx context.Context, req Request) (<-chan Event, error) {
		mu.Lock()
		attempts++
		n := attempts
		mu.Unlock()
		if n == 1 {
			return nil, rateLimited{wait: window}
		}
		return chanOf(deltaEvent("resumed after the rate limit"), doneEvent()), nil
	}

	var waits []time.Duration
	eng := New(stream, 3)
	hist := &convo.Conversation{}
	hist.Add(convo.User("do the thing"))

	start := time.Now()
	res, err := eng.RunAgentTurn(context.Background(), Request{Model: "fake/pro"}, AgentOptions{
		OnWait: func(w time.Duration, attempt int) {
			mu.Lock()
			waits = append(waits, w)
			mu.Unlock()
		},
	}, hist)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("a 429 with Retry-After must not fail the turn: %v", err)
	}
	if res.Text != "resumed after the rate limit" {
		t.Errorf("Text = %q, want the answer from after the wait", res.Text)
	}
	if attempts != 2 {
		t.Errorf("provider attempts = %d, want 2 (the 429 then the retry)", attempts)
	}
	// The point of fix 2: never earlier than the server permitted.
	if elapsed < window {
		t.Errorf("resumed after %v, inside the %v window the server closed", elapsed, window)
	}
	// And never silently: a wait long enough to matter must be reportable,
	// or it is indistinguishable from a hang.
	if len(waits) != 1 {
		t.Fatalf("OnWait called %d times, want 1", len(waits))
	}
	if waits[0] < window {
		t.Errorf("OnWait reported %v, less than the server's %v", waits[0], window)
	}
}

// TestRunAgentTurnRateLimitNeedsNoWaitHook guards the hook's optionality.
// OnWait is a courtesy to the user interface, never a condition for correct
// pacing -- a caller that does not set it (RunToCompletion, the plain text
// turn) must still wait exactly as long.
func TestRunAgentTurnRateLimitNeedsNoWaitHook(t *testing.T) {
	const window = 150 * time.Millisecond

	var mu sync.Mutex
	var attempts int
	stream := func(ctx context.Context, req Request) (<-chan Event, error) {
		mu.Lock()
		attempts++
		n := attempts
		mu.Unlock()
		if n == 1 {
			return nil, rateLimited{wait: window}
		}
		return chanOf(deltaEvent("fine"), doneEvent()), nil
	}

	eng := New(stream, 3)
	hist := &convo.Conversation{}
	start := time.Now()
	if _, err := eng.RunAgentTurn(context.Background(), Request{}, AgentOptions{}, hist); err != nil {
		t.Fatalf("RunAgentTurn: %v", err)
	}
	if elapsed := time.Since(start); elapsed < window {
		t.Errorf("resumed after %v without a hook set, inside the %v window", elapsed, window)
	}
}

// failingRunner fails every call with the same text, which is the shape of
// a configuration boundary (shell disabled, a path outside the workspace):
// the tool never runs and the reason never changes no matter how the model
// rewrites the arguments.
type failingRunner struct {
	mu    sync.Mutex
	calls int
	text  string
}

func (r *failingRunner) run(ctx context.Context, name string, args json.RawMessage) (ToolResult, error) {
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
	return ToolResult{Text: r.text, IsError: true}, nil
}

func (r *failingRunner) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// TestRunAgentTurnVariantHuntStops is closing criterion 3 of
// docs/BUG-rate-limit-amplifier.md, verbatim: ls -> ls -la -> find . must be
// recognized as one loop and stop the turn.
//
// Byte-exact loop detection cannot see this, because every attempt has
// different arguments -- that is precisely what makes it a variant hunt
// rather than a repeat. Measured before the fix, this scenario cost six
// provider requests, each carrying the whole grown history.
func TestRunAgentTurnVariantHuntStops(t *testing.T) {
	ss := &scriptedStreamer{scripts: [][]Event{
		{toolCallEvent("c1", "bash", `{"command":"ls"}`), doneEvent()},
		{toolCallEvent("c2", "bash", `{"command":"ls -la"}`), doneEvent()},
		{toolCallEvent("c3", "bash", `{"command":"find ."}`), doneEvent()},
		{toolCallEvent("c4", "bash", `{"command":"du -sh"}`), doneEvent()},
		{deltaEvent("I give up"), doneEvent()},
	}}
	runner := &failingRunner{text: "permission denied: shell is not allowed"}

	eng := New(ss.stream, 0)
	hist := &convo.Conversation{}
	hist.Add(convo.User("show me the files"))

	res, err := eng.RunAgentTurn(context.Background(), Request{Model: "fake/pro"}, AgentOptions{
		Tools: []ToolDef{{Name: "bash", Description: "run a shell command"}}, Runner: runner.run,
	}, hist)
	if err != nil {
		t.Fatalf("a stopped turn is not an engine failure: %v", err)
	}
	if res.Stopped == "" {
		t.Fatal("three identically-failing variants must stop the turn")
	}
	if runner.callCount() != 3 {
		t.Errorf("runner called %d times, want 3 (ls, ls -la, find .)", runner.callCount())
	}
	// The cost that matters: the fourth variant was never paid for.
	if n := ss.callCount(); n != 3 {
		t.Errorf("provider requests = %d, want 3", n)
	}
	// Bug 2: the history must stay valid for the next request.
	if !everyToolCallIsAnswered(hist) {
		t.Error("a tool_call was left without a reply; the next request would 400")
	}
}

// TestRunAgentTurnDifferentFailuresKeepGoing is the guard against
// overreach, and it is the reason futility is keyed on the error text rather
// than on a count of failures. A model working through genuinely different
// problems -- file not found, then a syntax error, then a missing dependency
// -- is making progress, and error-is-data (§12bis) is exactly what lets the
// reactive loop handle that without a Planner (§3). Stopping it would break
// the mechanism step 26 is supposed to protect.
func TestRunAgentTurnDifferentFailuresKeepGoing(t *testing.T) {
	ss := &scriptedStreamer{scripts: [][]Event{
		{toolCallEvent("c1", "bash", `{"command":"a"}`), doneEvent()},
		{toolCallEvent("c2", "bash", `{"command":"b"}`), doneEvent()},
		{toolCallEvent("c3", "bash", `{"command":"c"}`), doneEvent()},
		{toolCallEvent("c4", "bash", `{"command":"d"}`), doneEvent()},
		{deltaEvent("fixed it"), doneEvent()},
	}}
	var n int
	runner := func(ctx context.Context, name string, args json.RawMessage) (ToolResult, error) {
		n++
		return ToolResult{Text: fmt.Sprintf("distinct failure number %d", n), IsError: true}, nil
	}

	eng := New(ss.stream, 0)
	hist := &convo.Conversation{}
	res, err := eng.RunAgentTurn(context.Background(), Request{}, AgentOptions{
		Tools: []ToolDef{{Name: "bash"}}, Runner: runner,
	}, hist)
	if err != nil {
		t.Fatalf("RunAgentTurn: %v", err)
	}
	if res.Stopped != "" {
		t.Errorf("different failures are progress, not futility; got Stopped=%q", res.Stopped)
	}
	if res.Text != "fixed it" {
		t.Errorf("Text = %q, want the model's eventual answer", res.Text)
	}
}

// TestRunAgentTurnSuccessResetsFutility pins the other half of the same
// judgement: two identical failures followed by a success, then two more
// identical failures, is not a futile run of four. Without the reset, a long
// legitimate session that happened to hit the same recoverable error twice
// in separate places would be cut off partway through.
func TestRunAgentTurnSuccessResetsFutility(t *testing.T) {
	ss := &scriptedStreamer{scripts: [][]Event{
		{toolCallEvent("c1", "bash", `{"command":"a"}`), doneEvent()},
		{toolCallEvent("c2", "bash", `{"command":"b"}`), doneEvent()},
		{toolCallEvent("c3", "ok", `{}`), doneEvent()},
		{toolCallEvent("c4", "bash", `{"command":"c"}`), doneEvent()},
		{toolCallEvent("c5", "bash", `{"command":"d"}`), doneEvent()},
		{deltaEvent("done"), doneEvent()},
	}}
	runner := func(ctx context.Context, name string, args json.RawMessage) (ToolResult, error) {
		if name == "ok" {
			return ToolResult{Text: "worked"}, nil
		}
		return ToolResult{Text: "the same error every time", IsError: true}, nil
	}

	eng := New(ss.stream, 0)
	hist := &convo.Conversation{}
	res, err := eng.RunAgentTurn(context.Background(), Request{}, AgentOptions{
		Tools:  []ToolDef{{Name: "bash"}, {Name: "ok"}},
		Runner: runner,
	}, hist)
	if err != nil {
		t.Fatalf("RunAgentTurn: %v", err)
	}
	if res.Stopped != "" {
		t.Errorf("a success in between must reset the run; got Stopped=%q", res.Stopped)
	}
	if res.Text != "done" {
		t.Errorf("Text = %q, want the model's answer", res.Text)
	}
}

// TestRunAgentTurnFutilityStopsMidBatchWithoutOrphans checks fix 3 against
// Bug 2, the session-poisoning one: when the third identical failure lands
// mid-batch, every call the loop skipped afterwards still needs a matching
// tool reply, or the *next* request 400s permanently.
func TestRunAgentTurnFutilityStopsMidBatchWithoutOrphans(t *testing.T) {
	ss := &scriptedStreamer{scripts: [][]Event{
		{toolCallEvent("c1", "bash", `{"command":"a"}`), doneEvent()},
		{toolCallEvent("c2", "bash", `{"command":"b"}`), doneEvent()},
		{
			toolCallEvent("c3", "bash", `{"command":"c"}`), // third identical failure: stops here
			toolCallEvent("c4", "bash", `{"command":"d"}`), // never runs
			toolCallEvent("c5", "bash", `{"command":"e"}`), // never runs
			doneEvent(),
		},
	}}
	runner := &failingRunner{text: "always the same problem"}

	eng := New(ss.stream, 0)
	hist := &convo.Conversation{}
	res, err := eng.RunAgentTurn(context.Background(), Request{}, AgentOptions{
		Tools: []ToolDef{{Name: "bash"}}, Runner: runner.run,
	}, hist)
	if err != nil {
		t.Fatalf("RunAgentTurn: %v", err)
	}
	if res.Stopped == "" {
		t.Fatal("the third identical failure must stop the turn")
	}
	if runner.callCount() != 3 {
		t.Errorf("runner called %d times, want 3 (c4 and c5 must not run)", runner.callCount())
	}
	if !everyToolCallIsAnswered(hist) {
		t.Error("skipped calls were left unanswered; the next request would 400 permanently")
	}
}

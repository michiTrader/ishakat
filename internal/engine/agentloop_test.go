package engine

import (
	"context"
	"encoding/json"
	"errors"
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

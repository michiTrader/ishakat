package tui

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MichiTrader/ishakat/internal/convo"
	"github.com/MichiTrader/ishakat/internal/engine"
)

func TestAgentTurnCmdReturnsTheEngineResult(t *testing.T) {
	stream := func(ctx context.Context, req engine.Request) (<-chan engine.Event, error) {
		ch := make(chan engine.Event, 2)
		ch <- engine.Event{Kind: engine.EventDelta, Text: "final answer"}
		ch <- engine.Event{Kind: engine.EventDone}
		close(ch)
		return ch, nil
	}
	eng := engine.New(stream, 0)
	history := &convo.Conversation{}
	cmd := agentTurnCmd(context.Background(), eng, engine.Request{Model: "test/model"}, engine.AgentOptions{}, history)

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
}

func TestAgentTurnCmdRunsToolCallsBeforeReturningFinalAnswer(t *testing.T) {
	var requests int
	stream := func(ctx context.Context, req engine.Request) (<-chan engine.Event, error) {
		requests++
		ch := make(chan engine.Event, 3)
		if requests == 1 {
			ch <- engine.Event{Kind: engine.EventToolCall, Name: "read_file", ID: "call-1", Args: json.RawMessage(`{"path":"notes.txt"}`)}
		} else {
			ch <- engine.Event{Kind: engine.EventDelta, Text: "notes loaded"}
		}
		ch <- engine.Event{Kind: engine.EventDone}
		close(ch)
		return ch, nil
	}
	eng := engine.New(stream, 0)
	history := &convo.Conversation{}
	opts := engine.AgentOptions{
		Tools: []engine.ToolDef{{Name: "read_file"}},
		Runner: func(ctx context.Context, name string, args json.RawMessage) (engine.ToolResult, error) {
			if name != "read_file" || string(args) != `{"path":"notes.txt"}` {
				t.Fatalf("unexpected tool call: %s %s", name, args)
			}
			return engine.ToolResult{Text: "file contents"}, nil
		},
	}
	cmd := agentTurnCmd(context.Background(), eng, engine.Request{Model: "test/model"}, opts, history)

	value := cmd()
	msg, ok := value.(agentTurnDoneMsg)
	if !ok {
		t.Fatalf("agentTurnCmd returned %T, want agentTurnDoneMsg", value)
	}
	if msg.err != nil {
		t.Fatalf("agent turn error = %v, want nil", msg.err)
	}
	if msg.result.Text != "notes loaded" {
		t.Fatalf("agent result text = %q, want %q", msg.result.Text, "notes loaded")
	}
	if requests != 2 {
		t.Fatalf("stream opened %d times, want 2 iterations", requests)
	}
	// RunAgentTurn (internal/engine/agentloop.go) appends one message per
	// iteration step: the first iteration's assistant tool-call message,
	// its tool result, and the second (final) iteration's assistant text
	// message — the natural-termination text is recorded in history too,
	// not just returned in AgentResult.Text.
	if len(history.Messages) != 3 {
		t.Fatalf("history has %d messages, want assistant tool call, tool result, and final assistant text", len(history.Messages))
	}
	wantRoles := []convo.Role{convo.RoleAssistant, convo.RoleTool, convo.RoleAssistant}
	for i, want := range wantRoles {
		if got := history.Messages[i].Role; got != want {
			t.Errorf("history.Messages[%d].Role = %v, want %v", i, got, want)
		}
	}
}

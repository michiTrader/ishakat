package tui

import (
	"context"
	"encoding/json"
	"testing"
	"time"

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

// TestApplyPhaseWaitSetsFooterPhase is Step 32's own closing test for
// §21.1's "wait" phase (acceptance narrative item 6): PhaseWaitMsg's
// handler sets the same FooterState.Phase word startAgentTurn/
// openToolApprove already set for "exec"/"ask", this time to a rounded
// snapshot of the wait duration — matching the mockup's "auto·wait 22s"
// shape (footer.go's "autonomy" case already draws Autonomy+dot+Phase
// verbatim, so the space here becomes the space in "wait 22s").
func TestApplyPhaseWaitSetsFooterPhase(t *testing.T) {
	root := Root{mode: ModeBusy}
	model, cmd := root.applyPhaseWait(PhaseWaitMsg{Wait: 22 * time.Second})
	got := model.(Root)
	if got.footer.Phase != "wait 22s" {
		t.Fatalf("footer.Phase after applyPhaseWait = %q, want %q", got.footer.Phase, "wait 22s")
	}
	if cmd != nil {
		t.Fatalf("applyPhaseWait returned a non-nil cmd, want nil (fire-and-forget)")
	}
}

// TestApplyPhaseWaitRoundsSubSecondWaits mirrors internal/app/agentturn.go's
// own roundWait test coverage for the millisecond-resolution branch: a
// sub-second wait must not collapse to the misleading "0s".
func TestApplyPhaseWaitRoundsSubSecondWaits(t *testing.T) {
	root := Root{mode: ModeBusy}
	model, _ := root.applyPhaseWait(PhaseWaitMsg{Wait: 317 * time.Millisecond})
	got := model.(Root)
	if got.footer.Phase != "wait 317ms" {
		t.Fatalf("footer.Phase after applyPhaseWait = %q, want %q", got.footer.Phase, "wait 317ms")
	}
}

// TestUpdateDispatchDropsPhaseWaitOutsideModeBusy mirrors
// TestOpenAskUserSwitchesModeAndStoresDialog's own sibling coverage for
// ToolApproveRequestMsg/AskUserRequestMsg: a stale PhaseWaitMsg from a turn
// cancelAgentTurn already ended must not resurrect a footer phase for a
// mode that is no longer running a turn at all.
func TestUpdateDispatchDropsPhaseWaitOutsideModeBusy(t *testing.T) {
	root := Root{mode: ModeChat, footer: FooterState{Autonomy: "auto"}}
	model, _ := root.updateDispatch(PhaseWaitMsg{Wait: 5 * time.Second})
	got := model.(Root)
	if got.footer.Phase != "" {
		t.Fatalf("footer.Phase after a stale PhaseWaitMsg = %q, want empty (dropped)", got.footer.Phase)
	}
}

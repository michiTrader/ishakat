// steering_internal_test.go covers W2 item 4's own remaining W2 gate
// (docs/ROADMAP-ux-2026-08-20.md, F13, DECISION-2 consequence 2): "this
// needs a test that asserts the negative — a steering message arriving
// while a tool call is pending must leave that call pending." The
// engine-level version of this assertion already exists
// (internal/engine/agentstream_test.go's own
// TestRunAgentTurnStreamingInjectCannotApprovePendingToolCall, merged in
// PR #195); this file is that same property's TUI-side twin, exercising
// the actual path a human sees: a ModeToolApprove dialog already open,
// blocked on a reply channel, with a steering message submitted through
// this package's own queueSteering while it waits.
package tui

import (
	"encoding/json"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/permissions"
	"github.com/MichiTrader/ishakat/internal/theme"
)

// pausedToolApproveRoot builds a real, fully-initialised Root (through
// NewRoot, the same helper startBusyTurn/every grid-harness test in this
// package uses) and drops it straight into a paused ModeToolApprove —
// unlike toolapprove_internal_test.go's own bare Root{mode: ...} literals
// (which never call queueSteering, only updateToolApprove — a path that
// never touches m.input), this test's own queueSteering call reaches
// m.input.Reset(), and a zero-value textarea.Model panics on Reset (its
// embedded viewport is nil). A real Root, built the same way production
// code builds one, has no such gap.
func pausedToolApproveRoot(reply chan permissions.Decision) Root {
	root := NewRoot(Options{
		Version: "0.0.0-test",
		CWD:     "/home/user/projects/ishakat",
		Theme:   theme.Load(""),
		Cap:     theme.CapNone,
		NoTTY:   true,
	})
	req := permissions.Request{
		Name:      "write_file",
		Arguments: json.RawMessage(`{"path":"notes.txt","content":"hello"}`),
		Tier:      permissions.Sensitive,
	}
	root.mode = ModeToolApprove
	root.toolApprove = newToolApproveDialog(req, reply)
	return root
}

// TestQueueSteeringCannotResolvePendingToolApproval is the TUI-side W2
// gate DECISION-2 consequence 2 names explicitly. The setup mirrors
// TestToolApproveDialogSelectionWrapsAndCancelDenies's own Root{mode:
// ModeToolApprove, ...} construction: a dialog is open, blocked on
// reply, exactly as if a real ask-tier tool call had paused mid-turn.
//
// queueSteering is then called directly — the same call updateBusy's own
// Submit branch makes for ordinary text — while mode is still
// ModeToolApprove, not ModeBusy. The assertion is structural, the same
// way the engine-level test's is: queueSteering has no field, channel or
// side path that reaches toolApprove.reply, resolveToolApproveWith, or
// permissions.Guard.Authorize at all — it only ever touches
// m.transcript, m.inputHistory and m.steeringQueue().enqueueSteering.
// After the call, the dialog must still be exactly as it was: mode still
// ModeToolApprove, reply channel still open and empty (nothing sent, no
// approval and no denial), and the steering text must be sitting in the
// steering queue rather than having reached history at all — proving the
// message can never widen permissions, approve a pending tool call, or
// cancel it either.
func TestQueueSteeringCannotResolvePendingToolApproval(t *testing.T) {
	reply := make(chan permissions.Decision, 1)
	root := pausedToolApproveRoot(reply)

	next, cmd := root.queueSteering("focus on the other file instead")
	if cmd != nil {
		t.Fatalf("queueSteering returned a non-nil cmd: %v — it must never dispatch anything that could reach the reply channel", cmd)
	}
	got, ok := next.(Root)
	if !ok {
		t.Fatalf("queueSteering did not return a Root: %T", next)
	}

	// The security property itself: mode is untouched (still the exact
	// same paused dialog), and the reply channel has not been sent on —
	// no decision, allow or deny, reached the reviewer waiting on it.
	if got.mode != ModeToolApprove {
		t.Fatalf("mode after a steering message arrived mid-approval = %v, want ModeToolApprove (unchanged) — the pending call must still be pending", got.mode)
	}
	if got.toolApprove.reply == nil {
		t.Fatal("toolApprove.reply was cleared — a steering message must never resolve (or even touch) the pending approval's dialog state")
	}
	select {
	case decision := <-reply:
		t.Fatalf("reply channel received a decision (%+v) from a steering message — this must never happen: a steering message cannot approve, deny, or otherwise resolve a pending tool call", decision)
	default:
		// Correct: nothing was ever sent. The reviewer (a real
		// permissions.Guard.Authorize call in production) is still
		// blocked waiting for an actual human answer to this exact
		// dialog, exactly as before the steering message arrived.
	}

	// The steering message itself was not silently dropped either — it
	// is genuinely queued for the running turn's next Inject poll
	// (agentloop.go), the same "shown in the transcript... immediately"
	// contract queueSteering's own doc comment describes — it is simply
	// never allowed to reach the tool-approval machinery.
	if got.steering == nil || got.steering.steeringLen() != 1 {
		t.Fatalf("expected the steering message to be queued (not dropped) despite the pending approval, got %v", got.steering)
	}
	if len(got.transcript) == 0 || got.transcript[len(got.transcript)-1].text != "focus on the other file instead" {
		t.Fatalf("expected the steering message to still be shown in the transcript immediately, got transcript=%+v", got.transcript)
	}
}

// TestUpdateToolApproveIgnoresOrdinaryTextKeys is a second angle on the
// same gate, closer to how a human would actually trigger this: while
// ModeToolApprove owns the keyboard outright (updateToolApprove's own
// doc comment — "there is no textarea underneath it to fall through
// to"), an ordinary character keypress must not do anything at all to
// the pending dialog — in particular it must never reach
// resolveToolApprove/resolveToolApproveWith the way m.keys.Submit does.
// This pins the other half of the same property: it is not merely that
// queueSteering itself is safe to call, it is that ModeToolApprove's own
// dispatch (updateToolApprove) never calls it (or anything else that
// could resolve reply) for a plain letter key in the first place.
func TestUpdateToolApproveIgnoresOrdinaryTextKeys(t *testing.T) {
	reply := make(chan permissions.Decision, 1)
	root := pausedToolApproveRoot(reply)

	next, _ := root.updateToolApprove(tea.KeyPressMsg{Code: 'f', Text: "f"})
	got, ok := next.(Root)
	if !ok {
		t.Fatalf("updateToolApprove did not return a Root: %T", next)
	}
	if got.mode != ModeToolApprove {
		t.Fatalf("mode after an ordinary character key = %v, want ModeToolApprove (unchanged)", got.mode)
	}
	if got.toolApprove.reply == nil {
		t.Fatal("toolApprove.reply was cleared by an ordinary character key — only Submit/Cancel may ever resolve it")
	}
	select {
	case decision := <-reply:
		t.Fatalf("reply channel received a decision (%+v) from an ordinary character key", decision)
	default:
	}
}

// busyslash_internal_test.go covers W2 item 3's own new behaviour
// (docs/ROADMAP-ux-2026-08-20.md, F7/F3, DECISION-2 consequence 1):
// updateBusy's allow-listed slash commands, the esc-clears-vs-cancels
// re-scoping, and the honest "not wired yet" notice for an ordinary
// message submitted mid-turn. See updateBusy's and
// busyAllowedSlashKind's own doc comments (root.go) for the design this
// exercises.
package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/theme"
)

// startBusyTurn gets a headless Root into ModeBusy with a still-running
// (gated) turn, without draining it — the same shape
// TestRC2CursorStaysInsideTheInputWhileBusy's grid-harness test uses, just
// through the plain (non-grid) Root path these unit tests otherwise use.
func startBusyTurn(t *testing.T) tea.Model {
	t.Helper()
	root := NewRoot(Options{
		Version: "0.0.0-test",
		CWD:     "/home/user/projects/ishakat",
		Theme:   theme.Load(""),
		Cap:     theme.CapNone,
		NoTTY:   true,
	})
	// Ungated: these tests only assert on Root's synchronous state (mode,
	// transcript, input, live.aborted), all of which are set before any
	// tea.Cmd this package's helpers (typeAndEnter, plain Update calls)
	// ever run. The gated variant's returned "advance" func blocks on an
	// unbuffered channel waiting for the streaming goroutine that only
	// starts once the real tea.Cmd is executed - these helpers never do
	// that, so gated+t.Cleanup(advance) deadlocks the whole test binary.
	eng, _ := echoEngine(false)
	var m tea.Model = withEngine(root, eng)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "hola")
	if m.(Root).mode != ModeBusy {
		t.Fatal("precondition failed: expected ModeBusy after submit")
	}
	return m
}

// TestBusyAllowedSlashCommandRunsMidTurn proves a read-only, allow-listed
// command (/stats) actually runs while ModeBusy — the mode stays ModeBusy
// (there is no dedicated overlay Mode to return from, since KindStats only
// ever calls slashNotice) and its notice lands in the transcript exactly
// as it would in ModeChat.
func TestBusyAllowedSlashCommandRunsMidTurn(t *testing.T) {
	m := startBusyTurn(t)
	m = typeAndEnter(m, "/stats")

	root := m.(Root)
	if root.mode != ModeBusy {
		t.Fatalf("mode = %v, want ModeBusy (an allow-listed command must not leave it)", root.mode)
	}
	if len(root.transcript) == 0 {
		t.Fatal("expected /stats to append a transcript notice")
	}
	last := root.transcript[len(root.transcript)-1]
	if !strings.Contains(last.text, "stats") {
		t.Errorf("expected the /stats notice, got %q", last.text)
	}
}

// TestBusyDisallowedSlashCommandIsRefused proves a command outside
// busyAllowedSlashKind's list (e.g. /model, which opens ModePicker) is
// refused with an explicit notice while ModeBusy, rather than silently
// running and yanking the turn's own overlay out from under it.
func TestBusyDisallowedSlashCommandIsRefused(t *testing.T) {
	m := startBusyTurn(t)
	m = typeAndEnter(m, "/model")

	root := m.(Root)
	if root.mode != ModeBusy {
		t.Fatalf("mode = %v, want ModeBusy (disallowed command must not open ModePicker mid-turn)", root.mode)
	}
	if len(root.transcript) == 0 {
		t.Fatal("expected a refusal notice in the transcript")
	}
	last := root.transcript[len(root.transcript)-1]
	if !strings.Contains(last.text, "/model") {
		t.Errorf("expected a notice naming /model as unavailable, got %q", last.text)
	}
}

// TestBusyUnknownSlashCommandIsReported mirrors runSlashLine's own
// unknown-command case: "/nope" while ModeBusy reports the same "comando
// desconocido" notice ModeChat would, rather than being silently dropped
// or misfiled under the allow-list's own refusal message.
func TestBusyUnknownSlashCommandIsReported(t *testing.T) {
	m := startBusyTurn(t)
	m = typeAndEnter(m, "/nope")

	root := m.(Root)
	if root.mode != ModeBusy {
		t.Fatalf("mode = %v, want ModeBusy", root.mode)
	}
	last := root.transcript[len(root.transcript)-1]
	if !strings.Contains(last.text, "desconocido") {
		t.Errorf("expected an unknown-command notice, got %q", last.text)
	}
}

// TestBusySubmitWithOrdinaryTextReportsNotWiredYet proves an ordinary
// (non-slash) message submitted mid-turn gets an honest placeholder
// notice — W2 item 4's real steering queue, not this item's job — instead
// of silently vanishing or being sent straight into the running loop.
func TestBusySubmitWithOrdinaryTextReportsNotWiredYet(t *testing.T) {
	m := startBusyTurn(t)
	m = typeAndEnter(m, "focus on the other file instead")

	root := m.(Root)
	if root.mode != ModeBusy {
		t.Fatalf("mode = %v, want ModeBusy", root.mode)
	}
	last := root.transcript[len(root.transcript)-1]
	if !strings.Contains(last.text, "W2") {
		t.Errorf("expected a notice naming this as future W2 work, got %q", last.text)
	}
	if got := root.input.Value(); got != "" {
		t.Errorf("input should be cleared after the notice, got %q", got)
	}
}

// TestBusyEscWithTextClearsInputInsteadOfCancelling and
// TestBusyEscWithEmptyInputStillCancels together cover DECISION-2
// consequence 1's re-scoping of Esc: "cancel only while the input is
// empty — otherwise esc clears the editor". ctrl+c is deliberately not
// re-tested here: handleGlobalKey's own ModeBusy branch already covers
// it (TestCtrlCEnModeBusyCancelaEnVezDeSalir, root_test.go), unchanged by
// this item.
func TestBusyEscWithTextClearsInputInsteadOfCancelling(t *testing.T) {
	m := startBusyTurn(t)
	m, _ = m.Update(tea.KeyPressMsg{Text: "x", Code: 'x'})
	if got := m.(Root).input.Value(); got != "x" {
		t.Fatalf("precondition failed: input = %q, want %q", got, "x")
	}

	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})

	root := m.(Root)
	if root.mode != ModeBusy {
		t.Fatalf("mode = %v, want ModeBusy (esc must not cancel while text remains)", root.mode)
	}
	if got := root.input.Value(); got != "" {
		t.Errorf("esc with non-empty input must clear it, got %q", got)
	}
}

func TestBusyEscWithEmptyInputStillCancels(t *testing.T) {
	m := startBusyTurn(t)
	if got := m.(Root).input.Value(); got != "" {
		t.Fatalf("precondition failed: input = %q, want empty", got)
	}

	m, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if cmd == nil {
		t.Fatal("esc on an empty input must still cancel the turn (a cmd is expected)")
	}
	if got := m.(Root).live.aborted; !got {
		t.Error("esc on an empty input must mark the live turn aborted (cancelTurn/cancelAgentTurn)")
	}
}

// TestBusySlashMenuDropdownRefusesDisallowedSelection proves the §9.6
// dropdown's own accept-the-selection path (updateSlashMenu's Submit
// case) applies the identical allow-list a fully typed line does — the
// one branch shared between updateChat and updateBusy.
func TestBusySlashMenuDropdownRefusesDisallowedSelection(t *testing.T) {
	m := startBusyTurn(t)
	// Type "/mo" — matches "model" uniquely enough to keep the dropdown's
	// selection on a disallowed command without needing to steer the
	// arrow keys.
	m, _ = m.Update(tea.KeyPressMsg{Text: "/", Code: '/'})
	m, _ = m.Update(tea.KeyPressMsg{Text: "m", Code: 'm'})
	m, _ = m.Update(tea.KeyPressMsg{Text: "o", Code: 'o'})
	m, _ = m.Update(tea.KeyPressMsg{Text: "d", Code: 'd'})
	if !m.(Root).menu.Active() {
		t.Fatal("precondition failed: dropdown should be active for \"/mod\"")
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	root := m.(Root)
	if root.mode != ModeBusy {
		t.Fatalf("mode = %v, want ModeBusy (dropdown-selected disallowed command must not open ModePicker)", root.mode)
	}
	last := root.transcript[len(root.transcript)-1]
	if !strings.Contains(last.text, "/model") {
		t.Errorf("expected a notice naming /model as unavailable, got %q", last.text)
	}
}

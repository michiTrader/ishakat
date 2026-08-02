package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// drainTurn runs streamTickMsg until the live turn closes, the same pattern
// chat_internal_test.go's round-trip tests already use against the
// non-gated echoEngine.
func drainTurn(m tea.Model) tea.Model {
	for i := 0; i < 5000 && m.(Root).live.active; i++ {
		m, _ = m.Update(streamTickMsg{})
	}
	return m
}

func upKey() tea.KeyPressMsg   { return tea.KeyPressMsg{Code: tea.KeyUp} }
func downKey() tea.KeyPressMsg { return tea.KeyPressMsg{Code: tea.KeyDown} }

func TestInputHistoryRecallsPreviousLinesNewestFirst(t *testing.T) {
	var m tea.Model = newHeadlessRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	m = typeAndEnter(m, "primera linea")
	m = drainTurn(m)
	m = typeAndEnter(m, "segunda linea")
	m = drainTurn(m)

	m, _ = m.Update(upKey())
	if got := m.(Root).input.Value(); got != "segunda linea" {
		t.Fatalf("first up-arrow = %q, want the most recently submitted line", got)
	}
	m, _ = m.Update(upKey())
	if got := m.(Root).input.Value(); got != "primera linea" {
		t.Fatalf("second up-arrow = %q, want the line before that", got)
	}
	// A third up-arrow has nothing older to recall: it must leave the
	// textarea exactly as historyPrev's own "ok=false" contract promises,
	// not wrap around or clear it.
	m, _ = m.Update(upKey())
	if got := m.(Root).input.Value(); got != "primera linea" {
		t.Errorf("up-arrow past the oldest entry changed the value to %q", got)
	}
}

func TestInputHistoryDownArrowReturnsToTheUnsentDraft(t *testing.T) {
	var m tea.Model = newHeadlessRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "linea guardada")
	m = drainTurn(m)

	// Half-typed text that was never submitted has to survive a round trip
	// through history: up recalls the old line, down must hand back
	// exactly what was here before, not an empty box.
	for _, r := range "borrador sin enviar" {
		m, _ = m.Update(tea.KeyPressMsg{Text: string(r), Code: r})
	}
	m, _ = m.Update(upKey())
	if got := m.(Root).input.Value(); got != "linea guardada" {
		t.Fatalf("up-arrow = %q, want the recorded line", got)
	}
	m, _ = m.Update(downKey())
	if got := m.(Root).input.Value(); got != "borrador sin enviar" {
		t.Errorf("down-arrow past the newest entry = %q, want the draft restored", got)
	}
}

func TestInputHistoryRecordsSlashLinesToo(t *testing.T) {
	var m tea.Model = newHeadlessRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/clear")

	m, _ = m.Update(upKey())
	if got := m.(Root).input.Value(); got != "/clear" {
		t.Errorf("a slash command line should be recallable too, got %q", got)
	}
}

func TestInputHistoryUpArrowOnASecondLineMovesTheCursorInstead(t *testing.T) {
	var m tea.Model = newHeadlessRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "vieja")
	m = drainTurn(m)

	// A two-line draft: up-arrow while the cursor sits on the second line
	// must move the cursor up inside the textarea, not jump straight to
	// history — exactly what a shell's multi-line editor does, and the
	// reason historyPrev/historyNext gate on m.input.Line().
	m, _ = m.Update(tea.KeyPressMsg{Text: "a", Code: 'a'})
	m, _ = m.Update(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl}) // Newline
	m, _ = m.Update(tea.KeyPressMsg{Text: "b", Code: 'b'})

	m, _ = m.Update(upKey())
	got := m.(Root)
	if got.input.Value() != "a\nb" {
		t.Fatalf("up-arrow on the second line replaced the draft: %q", got.input.Value())
	}
	if got.input.Line() != 0 {
		t.Errorf("up-arrow should have moved the cursor to line 0, still on %d", got.input.Line())
	}
}

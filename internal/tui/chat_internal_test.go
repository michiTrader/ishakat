package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/theme"
)

// Bubble Tea's Update signature takes and returns the model *by value*, so the
// whole Root is copied on every single message. Any field that cannot survive
// being copied — strings.Builder is the textbook example, it panics with
// "strings: illegal use of non-zero Builder copied by value" — is a latent
// crash, not a style problem. These tests pin the invariant down so nobody
// reintroduces one.

func TestLiveTurnSurvivesBeingCopiedByValue(t *testing.T) {
	var turn liveTurn
	turn.start("model")
	turn.append("hello ")

	// This is exactly what Bubble Tea does between two Update calls.
	copied := turn
	copied.append("world")

	if got := copied.body(); got != "hello world" {
		t.Fatalf("body() after copy = %q, want %q", got, "hello world")
	}
	// The original must not be disturbed by writes to the copy: the two are
	// independent values, which is the only sane semantics for a model that
	// is copied on every message.
	if got := turn.body(); got != "hello " {
		t.Errorf("the original turn was mutated through the copy: %q", got)
	}
}

func TestRootSurvivesLongStreamingWithoutPanicking(t *testing.T) {
	// The reported crash needed a long-ish prompt because the echo is drained
	// in chunks: with three runes per tick, anything under four characters
	// only ever writes once and hides the bug.
	long := strings.TrimSpace(strings.Repeat("una respuesta larga para forzar muchos chunks. ", 8))

	var m tea.Model = newHeadlessRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	for _, r := range long {
		m, _ = m.Update(tea.KeyPressMsg{Text: string(r), Code: r})
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	// Drain the simulated stream synchronously: feeding streamTickMsg by hand
	// skips the 50 ms timer while exercising the exact same code path.
	for i := 0; i < 5000; i++ {
		m, _ = m.Update(streamTickMsg{})
		_ = m.View() // rendering also used to copy the builder
		if !m.(Root).live.active {
			break
		}
	}

	root := m.(Root)
	if root.live.active {
		t.Fatal("the turn never finished draining")
	}
	if root.mode != ModeChat {
		t.Errorf("after finishing the turn the mode should be ModeChat, got %v", root.mode)
	}
	last := root.transcript[len(root.transcript)-1]
	if last.text != long {
		t.Errorf("the echoed answer lost text:\n got %q\nwant %q", last.text, long)
	}
}

func TestRootSurvivesManyTurnsInARow(t *testing.T) {
	var m tea.Model = newHeadlessRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	for turn := 0; turn < 5; turn++ {
		for _, r := range "otra pregunta bastante larga para el maniquí" {
			m, _ = m.Update(tea.KeyPressMsg{Text: string(r), Code: r})
		}
		m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		for i := 0; i < 5000 && m.(Root).live.active; i++ {
			m, _ = m.Update(streamTickMsg{})
			_ = m.View()
		}
	}

	if got := len(m.(Root).transcript); got != 10 {
		t.Errorf("expected 5 user + 5 assistant entries, got %d", got)
	}
}

func TestCancelledTurnKeepsWhatWasAlreadyStreamed(t *testing.T) {
	var m tea.Model = newHeadlessRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	for _, r := range "abcdefghijklmnopqrstuvwxyz" {
		m, _ = m.Update(tea.KeyPressMsg{Text: string(r), Code: r})
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	m, _ = m.Update(streamTickMsg{})
	m, _ = m.Update(streamTickMsg{}) // six runes drained
	m, _ = m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	for i := 0; i < 100 && m.(Root).live.active; i++ {
		m, _ = m.Update(streamTickMsg{})
	}

	last := m.(Root).transcript[len(m.(Root).transcript)-1]
	if !strings.HasPrefix(last.text, "abcdef") {
		t.Errorf("the cancelled turn should keep the partial text, got %q", last.text)
	}
	if !strings.Contains(last.text, "cancel") {
		t.Errorf("the cancelled turn should be marked as such, got %q", last.text)
	}
}

func TestElapsedIsZeroBeforeTheTurnStarts(t *testing.T) {
	var turn liveTurn
	if turn.elapsed() != 0 {
		t.Error("a turn that never started has no elapsed time")
	}
	turn.start("m")
	time.Sleep(time.Millisecond)
	if turn.elapsed() <= 0 {
		t.Error("a started turn should report elapsed time")
	}
}

// newHeadlessRoot builds a Root that never needs a real terminal: no TTY means
// no banner and no cursor, which keeps these tests about state, not pixels.
func newHeadlessRoot() Root {
	return NewRoot(Options{
		Version: "0.0.0-test",
		CWD:     "/home/user/projects/ishakat",
		Theme:   theme.Load(""),
		Cap:     theme.CapNone,
		NoTTY:   true,
	})
}

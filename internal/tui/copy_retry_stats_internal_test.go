package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestCopyLastAnswerSetsTheClipboard(t *testing.T) {
	var m tea.Model = newHeadlessRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "hola mundo")
	m = drainTurn(m)

	m, cmd := m.Update(tea.KeyPressMsg{Code: 'y', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("ctrl+y should return a tea.SetClipboard command")
	}
	root := m.(Root)
	last := root.transcript[len(root.transcript)-1]
	if !strings.Contains(last.text, "copiado") {
		t.Errorf("expected a copy notice, got %q", last.text)
	}
}

func TestCopyWithNoAnswerYetWarns(t *testing.T) {
	var m tea.Model = newHeadlessRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/copy")

	root := m.(Root)
	last := root.transcript[len(root.transcript)-1]
	if !strings.Contains(last.text, "copiar") {
		t.Errorf("expected a warning about nothing to copy, got %q", last.text)
	}
}

func TestRetryDropsTheLastAnswerAndAsksAgain(t *testing.T) {
	var m tea.Model = newHeadlessRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "pregunta original")
	m = drainTurn(m)

	beforeMsgs := len(m.(Root).conv.Messages)
	m = typeAndEnter(m, "/retry")

	root := m.(Root)
	if root.mode != ModeBusy {
		t.Fatalf("mode = %v, want ModeBusy right after /retry starts a new turn", root.mode)
	}
	// The trailing assistant message was dropped before the new turn
	// started, so the conversation should be one message shorter than it
	// was right before /retry (still counting the user turn that remains).
	if got := len(root.conv.Messages); got != beforeMsgs-1 {
		t.Errorf("conv.Messages length = %d, want %d (assistant answer dropped)", got, beforeMsgs-1)
	}

	m = drainTurn(m)
	root = m.(Root)
	if root.mode != ModeChat {
		t.Errorf("mode after the retried turn finished = %v, want ModeChat", root.mode)
	}
}

func TestRetryWithNothingToAskWarns(t *testing.T) {
	var m tea.Model = newHeadlessRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/retry")

	root := m.(Root)
	if root.mode != ModeChat {
		t.Fatalf("mode = %v, want ModeChat: /retry on an empty conversation must not start a turn", root.mode)
	}
	last := root.transcript[len(root.transcript)-1]
	if !strings.Contains(last.text, "reintentar") {
		t.Errorf("expected a warning about nothing to retry, got %q", last.text)
	}
}

func TestStatsReportsTokensAfterATurn(t *testing.T) {
	var m tea.Model = newHeadlessRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "una pregunta cualquiera")
	m = drainTurn(m)
	m = typeAndEnter(m, "/stats")

	root := m.(Root)
	last := root.transcript[len(root.transcript)-1]
	if !strings.Contains(last.text, "stats") || !strings.Contains(last.text, "turno") {
		t.Errorf("expected a stats summary, got %q", last.text)
	}
}

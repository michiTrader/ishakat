package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/convo"
)

// typeAndEnter feeds each rune of text as a keypress and finishes with enter,
// discarding every intermediate tea.Cmd — the tests below only care about
// the state Root ends up in, not what got scheduled along the way.
func typeAndEnter(m tea.Model, text string) tea.Model {
	for _, r := range text {
		m, _ = m.Update(tea.KeyPressMsg{Text: string(r), Code: r})
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	return m
}

func TestSlashHelpEntersHelpModeAndListsTheRegistry(t *testing.T) {
	var m tea.Model = newHeadlessRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/help")

	root := m.(Root)
	if root.mode != ModeHelp {
		t.Fatalf("mode = %v, want ModeHelp", root.mode)
	}
	// The dropdown's own table has to be what /help draws (§9.6 vs §9.7
	// drift is exactly the bug a shared registry rules out).
	if !strings.Contains(m.View().Content, "/model") {
		t.Errorf("help screen should list /model from the registry, got:\n%s", m.View().Content)
	}
}

func TestSlashClearWipesTheScreenButKeepsTheConversation(t *testing.T) {
	root := newHeadlessRoot()
	root.transcript = []transcriptEntry{{role: "user", name: "tu", text: "hola"}}
	root.conv.Add(convo.User("hola"))

	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/clear")

	got := m.(Root)
	if len(got.transcript) != 0 {
		t.Errorf("transcript = %v, want empty after /clear", got.transcript)
	}
	if len(got.conv.Active()) != 1 {
		t.Errorf("/clear must not touch the conversation history (§9.7: 'limpiar pantalla'), got %d messages", len(got.conv.Active()))
	}
}

func TestSlashNewDropsTheConversationToo(t *testing.T) {
	root := newHeadlessRoot()
	root.transcript = []transcriptEntry{{role: "user", name: "tu", text: "hola"}}
	root.conv.Add(convo.User("hola"))

	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/new")

	got := m.(Root)
	if len(got.transcript) != 0 || len(got.conv.Active()) != 0 {
		t.Errorf("/new should drop both transcript and history, got transcript=%v conv=%v",
			got.transcript, got.conv.Active())
	}
}

func TestSlashExitQuits(t *testing.T) {
	var m tea.Model = newHeadlessRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	for _, r := range "/exit" {
		m, _ = m.Update(tea.KeyPressMsg{Text: string(r), Code: r})
	}
	m, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("/exit should return a command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("/exit should quit, got %T", cmd())
	}
	if !m.(Root).quitting {
		t.Error("/exit should set the quitting flag")
	}
}

func TestSlashUnknownCommandReportsANoticeWithoutTouchingHistory(t *testing.T) {
	var m tea.Model = newHeadlessRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/bogus")

	root := m.(Root)
	if root.mode != ModeChat {
		t.Fatalf("mode = %v, want ModeChat (an unknown command is not a turn)", root.mode)
	}
	if len(root.transcript) != 1 {
		t.Fatalf("expected one notice entry, got %d: %v", len(root.transcript), root.transcript)
	}
	if !strings.Contains(root.transcript[0].text, "bogus") {
		t.Errorf("notice should mention the unknown command, got %q", root.transcript[0].text)
	}
	if len(root.conv.Active()) != 0 {
		t.Error("an unknown command must not be added to the conversation the model sees")
	}
}

func TestSlashUnimplementedCommandSaysSoInsteadOfDoingNothing(t *testing.T) {
	var m tea.Model = newHeadlessRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/model son45")

	root := m.(Root)
	if len(root.transcript) != 1 {
		t.Fatalf("expected one notice entry, got %d: %v", len(root.transcript), root.transcript)
	}
	if !strings.Contains(root.transcript[0].text, "/model") {
		t.Errorf("notice should name the command, got %q", root.transcript[0].text)
	}
}

func TestSlashDropdownOpensWhileTypingAndClosesOnceAnArgumentStarts(t *testing.T) {
	var m tea.Model = newHeadlessRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	for _, r := range "/mo" {
		m, _ = m.Update(tea.KeyPressMsg{Text: string(r), Code: r})
	}
	if !m.(Root).menu.Active() {
		t.Fatal("dropdown should be active while a command name is still being typed")
	}
	if !strings.Contains(m.View().Content, "/model") {
		t.Errorf("dropdown should list /model, got:\n%s", m.View().Content)
	}

	m, _ = m.Update(tea.KeyPressMsg{Text: " ", Code: ' '})
	if m.(Root).menu.Active() {
		t.Error("dropdown should close once an argument starts")
	}
}

func TestSlashDropdownTabCompletesTheSelection(t *testing.T) {
	var m tea.Model = newHeadlessRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	for _, r := range "/hel" {
		m, _ = m.Update(tea.KeyPressMsg{Text: string(r), Code: r})
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})

	root := m.(Root)
	if got := root.input.Value(); got != "/help " {
		t.Errorf("input after tab = %q, want %q", got, "/help ")
	}
	if root.menu.Active() {
		t.Error("dropdown should close after completion, since a space now follows the name")
	}
}

func TestSlashDropdownEnterRunsTheSelectedCommand(t *testing.T) {
	var m tea.Model = newHeadlessRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	for _, r := range "/hel" {
		m, _ = m.Update(tea.KeyPressMsg{Text: string(r), Code: r})
	}
	// Enter while the dropdown is open accepts the highlighted command
	// (here, the only match: /help) rather than treating "/hel" itself as
	// unknown command syntax.
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	root := m.(Root)
	if root.mode != ModeHelp {
		t.Fatalf("mode = %v, want ModeHelp", root.mode)
	}
	if root.menu.Active() {
		t.Error("running a command from the dropdown should close it")
	}
}

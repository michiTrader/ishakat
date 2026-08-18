package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestToggleFoldKeyFlipsRootFoldCode pins the actual keybinding wiring for
// §17's 2026-08-18 "code blocks fill the terminal" fix (part 2): ctrl+r
// (tui.Map.ToggleFold, keys.go) must flip Root.foldCode, and pressing it a
// second time must flip it back — mirroring
// TestCopyLastAnswerSetsTheClipboard's own shape in
// copy_retry_stats_internal_test.go for a different ctrl+ chord.
func TestToggleFoldKeyFlipsRootFoldCode(t *testing.T) {
	var m tea.Model = newHeadlessRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	if m.(Root).foldCode {
		t.Fatal("foldCode should start false")
	}

	m, cmd := m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	if cmd != nil {
		t.Errorf("ToggleFold is a pure view toggle, it should not return a command, got %v", cmd)
	}
	if !m.(Root).foldCode {
		t.Fatal("first ctrl+r should set foldCode to true")
	}

	m, _ = m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	if m.(Root).foldCode {
		t.Fatal("second ctrl+r should flip foldCode back to false")
	}
}

// TestToggleFoldWorksWhileBusy confirms handleGlobalKey's own doc comment on
// the ToggleFold case: unlike ModelPicker/ThemePicker/CopyLast, folding is
// not gated to ModeChat, so it stays reachable while a turn is still
// streaming (ModeBusy) — arguably the moment it is most wanted, since that
// is exactly when a long code block is filling the terminal.
func TestToggleFoldWorksWhileBusy(t *testing.T) {
	var m tea.Model = newHeadlessRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "hola")

	if m.(Root).mode != ModeBusy {
		t.Fatalf("mode = %v, want ModeBusy right after submitting", m.(Root).mode)
	}

	m, _ = m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	if !m.(Root).foldCode {
		t.Error("ctrl+r must still toggle foldCode while ModeBusy")
	}
}

// TestPlayTurnWithFoldedCodeShowsSummaryNotBody is the integration check
// through a real Root and its actual view rendering: with foldCode toggled
// on before the code block ever prints, the finished frame must show
// foldSummary's one-line form instead of the code's own content — the
// concrete fix for the "terminal fills with pasted code" complaint —
// and toggling it back off must restore the full body, since committed
// scrollback aside (commitEntryCmd's own limitation, chat.go), everything
// still in the live-managed region must react to the toggle.
func TestPlayTurnWithFoldedCodeShowsSummaryNotBody(t *testing.T) {
	text := "antes\n```go\nfunc main() {\n\tfmt.Println(\"hola\")\n}\n```\ndespués"

	var m tea.Model = newVisibleRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 60, Height: 30})
	m, _ = m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	if !m.(Root).foldCode {
		t.Fatal("ctrl+r must have set foldCode before the turn even starts")
	}
	m = playTurn(m, text)

	plain := stripANSI(m.View().Content)
	if strings.Contains(plain, "fmt.Println") {
		t.Errorf("with foldCode on, the code body must not appear on screen: %s", plain)
	}
	if !strings.Contains(plain, unicodeGlyphs.foldMark) {
		t.Errorf("with foldCode on, the fold summary glyph must appear: %s", plain)
	}
	if !strings.Contains(plain, "antes") || !strings.Contains(plain, "después") {
		t.Errorf("surrounding prose must still be present: %s", plain)
	}

	m, _ = m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	if m.(Root).foldCode {
		t.Fatal("second ctrl+r should turn folding back off")
	}
	plain = stripANSI(m.View().Content)
	if !strings.Contains(plain, "fmt.Println") {
		t.Errorf("with foldCode off again, the code body must be visible: %s", plain)
	}
}

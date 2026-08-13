package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/theme"
)

func TestCtrlTOpensThemePickerFromModeChat(t *testing.T) {
	var m tea.Model = newHeadlessRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = m.Update(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})

	root := m.(Root)
	if root.mode != ModeThemePicker {
		t.Fatalf("mode = %v, want ModeThemePicker", root.mode)
	}
	if !strings.Contains(root.View().Content, "ascua") {
		t.Errorf("overlay should list the embedded default theme, got %q", root.View().Content)
	}
}

// TestCtrlTIsSwallowedOutsideModeChat mirrors ModelPicker's own gating
// (handleGlobalKey's ctrl+p comment): a chord that opens an overlay is
// swallowed rather than reopening a second one while ModeBusy (or any
// other overlay mode) already owns the keyboard.
func TestCtrlTIsSwallowedOutsideModeChat(t *testing.T) {
	var m tea.Model = newHeadlessRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	for _, r := range "hola" {
		m, _ = m.Update(tea.KeyPressMsg{Text: string(r), Code: r})
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	root := m.(Root)
	if root.mode != ModeBusy {
		t.Fatalf("precondition failed: mode = %v, want ModeBusy", root.mode)
	}

	m, _ = m.Update(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	root = m.(Root)
	if root.mode != ModeBusy {
		t.Errorf("mode = %v, want ModeBusy unchanged (ctrl+t swallowed)", root.mode)
	}
}

func TestThemePickerEscCancelsWithoutSwitching(t *testing.T) {
	var m tea.Model = newHeadlessRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	before := m.(Root).styles.Theme.Name

	m, _ = m.Update(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})

	root := m.(Root)
	if root.mode != ModeChat {
		t.Errorf("mode = %v, want ModeChat after esc", root.mode)
	}
	if root.styles.Theme.Name != before {
		t.Errorf("theme changed to %q after a plain esc, want unchanged %q", root.styles.Theme.Name, before)
	}
}

func TestThemePickerUpDownWraps(t *testing.T) {
	var m tea.Model = newHeadlessRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = m.Update(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})

	root := m.(Root)
	n := len(root.themePicker.names)
	if n == 0 {
		t.Fatal("expected at least the embedded default theme")
	}

	// One "up" from the initial selection wraps to the last row.
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	root = m.(Root)
	if root.themePicker.sel != n-1 {
		t.Errorf("sel after one up = %d, want %d (wrapped)", root.themePicker.sel, n-1)
	}

	// One "down" from there wraps back to the first row.
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	root = m.(Root)
	if root.themePicker.sel != 0 {
		t.Errorf("sel after one down = %d, want 0 (wrapped back)", root.themePicker.sel)
	}
}

// TestThemePickerEnterSwitchesThroughTheSamePathAsSlashTheme confirms the
// overlay is a second door into switchTheme, not a parallel
// implementation: enter on "ascua" produces the exact same confirmation
// notice and Theme.Name mutation /theme ascua already does
// (theme_internal_test.go's own TestSlashThemeWithNameSwitchesLive).
func TestThemePickerEnterSwitchesThroughTheSamePathAsSlashTheme(t *testing.T) {
	var m tea.Model = newHeadlessRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = m.Update(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})

	root := m.(Root)
	idx := -1
	for i, n := range root.themePicker.names {
		if n == "ascua" {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatal("expected \"ascua\" among the listed themes")
	}
	root.themePicker.sel = idx
	m = root

	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	root = m.(Root)

	if root.mode != ModeChat {
		t.Errorf("mode = %v, want ModeChat after choosing a theme", root.mode)
	}
	if root.styles.Theme.Name != "ascua" {
		t.Errorf("styles.Theme.Name = %q, want %q", root.styles.Theme.Name, "ascua")
	}
	if len(root.transcript) != 1 {
		t.Fatalf("expected one notice entry, got %d: %v", len(root.transcript), root.transcript)
	}
	if !strings.Contains(root.transcript[0].text, "ascua") {
		t.Errorf("confirmation notice should name the theme, got %q", root.transcript[0].text)
	}
}

// TestThemePickerEnterPersistsViaThemeStore confirms the overlay's apply
// path also goes through ThemeStore.Save when one is wired — the same
// persistence /theme ascua already exercises
// (TestSlashThemePersistsViaThemeStore), reached this time through the
// ctrl+t door instead of the slash command.
func TestThemePickerEnterPersistsViaThemeStore(t *testing.T) {
	store := &fakeThemeStore{}
	root := NewRoot(Options{
		Version:    "0.0.0-test",
		Theme:      theme.Load(""),
		Cap:        theme.CapNone,
		NoTTY:      true,
		ThemeStore: store,
	})
	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = m.Update(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})

	r := m.(Root)
	r.themePicker.sel = 0
	m = r

	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	r = m.(Root)

	if len(store.saved) != 1 {
		t.Fatalf("ThemeStore.Save calls = %v, want exactly one call", store.saved)
	}
	if store.saved[0] != r.styles.Theme.Name {
		t.Errorf("Save called with %q, want the applied theme %q", store.saved[0], r.styles.Theme.Name)
	}
}

func TestThemePickerEnterWithNoThemesIsANoOp(t *testing.T) {
	root := newHeadlessRoot()
	root.mode = ModeThemePicker
	root.themePicker = themePickerState{}
	var m tea.Model = root

	m, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		t.Error("enter with an empty theme list should produce no command")
	}
	r := m.(Root)
	if r.mode != ModeThemePicker {
		t.Errorf("mode = %v, want ModeThemePicker unchanged (nothing to choose)", r.mode)
	}
}

package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/theme"
)

// fakeThemeStore is theme.go's ThemeStore test double, mirroring
// fakeEvolveStore (suggest_internal_test.go): records every Save call so
// tests can assert on it, with an optional forced error for the
// best-effort-persistence path.
type fakeThemeStore struct {
	saved []string
	err   error
}

func (s *fakeThemeStore) Save(name string) error {
	s.saved = append(s.saved, name)
	return s.err
}

func TestSlashThemeNoArgListsAvailableThemes(t *testing.T) {
	var m tea.Model = newHeadlessRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/theme")

	root := m.(Root)
	if len(root.transcript) != 1 {
		t.Fatalf("expected one notice entry, got %d: %v", len(root.transcript), root.transcript)
	}
	text := root.transcript[0].text
	if !strings.Contains(text, "ascua") {
		t.Errorf("listing should mention the embedded default theme %q, got %q", theme.Default, text)
	}
}

func TestSlashThemeWithNameSwitchesLive(t *testing.T) {
	var m tea.Model = newHeadlessRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/theme ascua")

	root := m.(Root)
	if root.styles.Theme.Name != "ascua" {
		t.Errorf("styles.Theme.Name = %q, want %q after the switch", root.styles.Theme.Name, "ascua")
	}
	if len(root.transcript) != 1 {
		t.Fatalf("expected one notice entry, got %d: %v", len(root.transcript), root.transcript)
	}
	if !strings.Contains(root.transcript[0].text, "ascua") {
		t.Errorf("confirmation notice should name the theme, got %q", root.transcript[0].text)
	}
}

// TestSlashThemeUnknownNameDegradesInsteadOfErroring exercises theme.Load's
// own "never errors" contract end to end: an unresolvable name still
// switches (to the embedded default) and reports why, rather than the
// command failing outright.
func TestSlashThemeUnknownNameDegradesInsteadOfErroring(t *testing.T) {
	var m tea.Model = newHeadlessRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/theme no-existe-este-tema")

	root := m.(Root)
	if root.styles.Theme.Name != theme.Default {
		t.Errorf("styles.Theme.Name = %q, want the embedded default %q", root.styles.Theme.Name, theme.Default)
	}
	if len(root.transcript) != 1 {
		t.Fatalf("expected one notice entry, got %d: %v", len(root.transcript), root.transcript)
	}
	if !strings.Contains(root.transcript[0].text, "no-existe-este-tema") {
		t.Errorf("notice should explain the unknown name was substituted, got %q", root.transcript[0].text)
	}
}

// TestSlashThemePersistsViaThemeStore is the one test that needs a real
// (fake) ThemeStore wired in, since newHeadlessRoot's Options carries none
// — mirroring suggest_internal_test.go's own pattern of building a second
// Root by hand when a test needs a store the shared helper does not wire.
func TestSlashThemePersistsViaThemeStore(t *testing.T) {
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
	m = typeAndEnter(m, "/theme ascua")

	if len(store.saved) != 1 || store.saved[0] != "ascua" {
		t.Errorf("ThemeStore.Save calls = %v, want exactly one call with %q", store.saved, "ascua")
	}
}

// TestSlashThemeSaveFailureStillSwitches mirrors commitModelSwitch's own
// "the display already changed, hiding that would be a worse surprise"
// rule: a persistence failure must not undo the in-memory switch, only
// report itself alongside the confirmation.
func TestSlashThemeSaveFailureStillSwitches(t *testing.T) {
	store := &fakeThemeStore{err: errSaveFailedForTest}
	root := NewRoot(Options{
		Version:    "0.0.0-test",
		Theme:      theme.Load(""),
		Cap:        theme.CapNone,
		NoTTY:      true,
		ThemeStore: store,
	})
	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/theme ascua")

	got := m.(Root)
	if got.styles.Theme.Name != "ascua" {
		t.Errorf("styles.Theme.Name = %q, want the switch to apply despite the save error", got.styles.Theme.Name)
	}
	if !strings.Contains(got.transcript[0].text, "no se pudo guardar") {
		t.Errorf("notice should surface the save failure, got %q", got.transcript[0].text)
	}
}

var errSaveFailedForTest = themeSaveTestError{}

type themeSaveTestError struct{}

func (themeSaveTestError) Error() string { return "disco lleno" }

package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/theme"
)

// fakeSettingsStore is SettingsStore's own test double (settingscmd.go),
// mirroring fakeThemeStore/fakeTitleStore's shape one-for-one: records every
// Set call (key and value), with an optional forced error for the
// best-effort-persistence path.
type fakeSettingsStore struct {
	keys   []string
	values []string
	err    error
}

func (s *fakeSettingsStore) Set(key, value string) error {
	s.keys = append(s.keys, key)
	s.values = append(s.values, value)
	return s.err
}

// TestSlashSettingsNoArgListsKnownKeys is /settings' no-argument, read-only
// form: every internal/config.Settings key should show up somewhere in the
// listing, along with its current value.
func TestSlashSettingsNoArgListsKnownKeys(t *testing.T) {
	var m tea.Model = newHeadlessRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/settings")

	root := m.(Root)
	if len(root.transcript) != 1 {
		t.Fatalf("expected one notice entry, got %d: %v", len(root.transcript), root.transcript)
	}
	text := root.transcript[0].text
	for _, key := range []string{"ui.banner", "ui.markdown", "ui.syntax", "ui.reasoning"} {
		if !strings.Contains(text, key) {
			t.Errorf("listing should mention %q, got %q", key, text)
		}
	}
}

// TestSlashSettingsListShowsCurrentValues checks the no-arg listing reflects
// Root's own cfgBanner/cfgReasoning fields, not just the key names.
func TestSlashSettingsListShowsCurrentValues(t *testing.T) {
	var m tea.Model = newHeadlessRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/settings")

	root := m.(Root)
	text := root.transcript[0].text
	// newHeadlessRoot builds Options{} with no Cfg, so banner/markdown/
	// syntax default true and reasoning defaults "collapsed" (chat.go's
	// reasoningModeOr).
	if !strings.Contains(text, "true") {
		t.Errorf("listing should show the true default for the bool keys, got %q", text)
	}
	if !strings.Contains(text, "collapsed") {
		t.Errorf("listing should show the collapsed default for ui.reasoning, got %q", text)
	}
}

// TestSlashSettingsWithBoolKeyAppliesLive exercises the write half for a
// bool key: the value applies immediately to the field the roadmap
// investigation confirmed already drives rendering (Root.cfgBanner).
func TestSlashSettingsWithBoolKeyAppliesLive(t *testing.T) {
	var m tea.Model = newHeadlessRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/settings ui.banner false")

	root := m.(Root)
	if root.cfgBanner {
		t.Errorf("cfgBanner = true, want false after /settings ui.banner false")
	}
	if len(root.transcript) != 1 {
		t.Fatalf("expected one notice entry, got %d: %v", len(root.transcript), root.transcript)
	}
	if !strings.Contains(root.transcript[0].text, "ui.banner") || !strings.Contains(root.transcript[0].text, "false") {
		t.Errorf("confirmation notice should name the key and the new value, got %q", root.transcript[0].text)
	}
}

// TestSlashSettingsWithEnumKeyAppliesLive is the same shape for the one
// enum key this slice covers.
func TestSlashSettingsWithEnumKeyAppliesLive(t *testing.T) {
	var m tea.Model = newHeadlessRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/settings ui.reasoning full")

	root := m.(Root)
	if root.cfgReasoning != "full" {
		t.Errorf("cfgReasoning = %q, want %q after /settings ui.reasoning full", root.cfgReasoning, "full")
	}
}

// TestSlashSettingsUnknownKeyDegradesInsteadOfPanicking mirrors
// runPermissionsCommand's own "reject unrecognized value outright" rule,
// applied to an unrecognized key: nothing in Root changes, and the notice
// says why.
func TestSlashSettingsUnknownKeyDegradesInsteadOfPanicking(t *testing.T) {
	var m tea.Model = newHeadlessRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/settings ui.does-not-exist true")

	root := m.(Root)
	if !root.cfgBanner {
		t.Errorf("cfgBanner should stay at its default true, an unknown key must not touch unrelated state")
	}
	if len(root.transcript) != 1 {
		t.Fatalf("expected one notice entry, got %d: %v", len(root.transcript), root.transcript)
	}
	if !strings.Contains(root.transcript[0].text, "ui.does-not-exist") {
		t.Errorf("notice should name the unknown key, got %q", root.transcript[0].text)
	}
}

// TestSlashSettingsInvalidValueDegradesInsteadOfApplying is the same rule
// for a recognized key with an unrecognized value: the setting must not
// change to something Setting.Valid itself would reject.
func TestSlashSettingsInvalidValueDegradesInsteadOfApplying(t *testing.T) {
	var m tea.Model = newHeadlessRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/settings ui.reasoning muy-alto")

	root := m.(Root)
	if root.cfgReasoning != "collapsed" {
		t.Errorf("cfgReasoning = %q, want the default collapsed left untouched by an invalid value", root.cfgReasoning)
	}
	if !strings.Contains(root.transcript[0].text, "muy-alto") {
		t.Errorf("notice should name the rejected value, got %q", root.transcript[0].text)
	}
}

// TestSlashSettingsPersistsViaSettingsStore is the one test that needs a
// real (fake) SettingsStore wired in, mirroring
// TestSlashThemePersistsViaThemeStore/TestSlashNamePersistsViaTitleStore.
func TestSlashSettingsPersistsViaSettingsStore(t *testing.T) {
	store := &fakeSettingsStore{}
	root := NewRoot(Options{
		Version:       "0.0.0-test",
		Theme:         theme.Load(""),
		Cap:           theme.CapNone,
		NoTTY:         true,
		SettingsStore: store,
	})
	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/settings ui.markdown false")

	if len(store.keys) != 1 || store.keys[0] != "ui.markdown" || store.values[0] != "false" {
		t.Errorf("SettingsStore.Set calls = keys %v values %v, want exactly one call with (\"ui.markdown\", \"false\")",
			store.keys, store.values)
	}
}

// TestSlashSettingsSaveFailureStillApplies mirrors
// TestSlashThemeSaveFailureStillSwitches/TestSlashNameSaveFailureStillRenames:
// a persistence failure must not undo the in-memory change, only report
// itself alongside the confirmation.
func TestSlashSettingsSaveFailureStillApplies(t *testing.T) {
	store := &fakeSettingsStore{err: errSaveFailedForTest}
	root := NewRoot(Options{
		Version:       "0.0.0-test",
		Theme:         theme.Load(""),
		Cap:           theme.CapNone,
		NoTTY:         true,
		SettingsStore: store,
	})
	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/settings ui.syntax false")

	got := m.(Root)
	if got.cfgSyntax {
		t.Errorf("cfgSyntax = true, want the change to apply despite the save error")
	}
	if !strings.Contains(got.transcript[0].text, "no se pudo guardar") {
		t.Errorf("notice should surface the save failure, got %q", got.transcript[0].text)
	}
}

// TestSlashSettingsNilStoreIsANoOp is SettingsStore's own documented
// default: nil must not panic, and the change still applies for the
// running session — the same rule ThemeStore/TitleStore already establish.
func TestSlashSettingsNilStoreIsANoOp(t *testing.T) {
	var m tea.Model = newHeadlessRoot() // settingsStore left nil
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/settings ui.banner false")

	root := m.(Root)
	if root.cfgBanner {
		t.Errorf("cfgBanner = true, want the change to apply even with no SettingsStore")
	}
	if strings.Contains(root.transcript[0].text, "no se pudo guardar") {
		t.Errorf("a nil settingsStore is not a failure, should not report one: %q", root.transcript[0].text)
	}
}

// TestOptionsSettingsStoreIsWiredIntoRoot is the same regression shape
// TestOptionsTitleStoreIsWiredIntoRoot/TestOptionsThemeStoreIsWiredIntoRoot
// already establish: it goes through NewRoot(Options{...}) rather than
// assigning the private field directly, so a future refactor that drops
// Options.SettingsStore on the floor fails a test instead of silently
// making every real /settings persistence attempt a no-op.
func TestOptionsSettingsStoreIsWiredIntoRoot(t *testing.T) {
	store := &fakeSettingsStore{}
	root := NewRoot(Options{SettingsStore: store})
	if root.settingsStore == nil {
		t.Fatal("NewRoot did not wire Options.SettingsStore into Root.settingsStore — /settings would never persist")
	}
}

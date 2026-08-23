package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/theme"
)

// fakeTitleStore is SessionTitleStore's own test double (session.go),
// mirroring fakeThemeStore's shape one-for-one: records every SetTitle
// call, with an optional forced error for the best-effort-persistence
// path.
type fakeTitleStore struct {
	saved []string
	err   error
}

func (s *fakeTitleStore) SetTitle(title string) error {
	s.saved = append(s.saved, title)
	return s.err
}

// TestSlashNameNoArgReportsNoTitleYet is /name's no-argument form on a
// brand-new session: nothing has been sent yet, so m.conv.Title is still
// "", and the notice must say so rather than reporting an empty title.
func TestSlashNameNoArgReportsNoTitleYet(t *testing.T) {
	var m tea.Model = newHeadlessRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/name")

	root := m.(Root)
	if len(root.transcript) != 1 {
		t.Fatalf("expected one notice entry, got %d: %v", len(root.transcript), root.transcript)
	}
	if !strings.Contains(root.transcript[0].text, "todavía no tiene título") {
		t.Errorf("notice should say there is no title yet, got %q", root.transcript[0].text)
	}
}

// TestSlashNameWithArgRenamesLiveAndReports mirrors
// TestSlashThemeWithNameSwitchesLive: renaming applies immediately
// (m.conv.Title) and the confirmation names the new title.
func TestSlashNameWithArgRenamesLiveAndReports(t *testing.T) {
	var m tea.Model = newHeadlessRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/name mi sesion favorita")

	root := m.(Root)
	if root.conv.Title != "mi sesion favorita" {
		t.Errorf("conv.Title = %q, want %q after /name", root.conv.Title, "mi sesion favorita")
	}
	if len(root.transcript) != 1 {
		t.Fatalf("expected one notice entry, got %d: %v", len(root.transcript), root.transcript)
	}
	if !strings.Contains(root.transcript[0].text, "mi sesion favorita") {
		t.Errorf("confirmation notice should name the new title, got %q", root.transcript[0].text)
	}
}

// TestSlashNameNoArgReportsCurrentTitleOnceSet is the read half once a
// title actually exists: no-argument /name should echo it back, not the
// "no title yet" notice.
func TestSlashNameNoArgReportsCurrentTitleOnceSet(t *testing.T) {
	var m tea.Model = newHeadlessRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/name mi sesion")
	m = typeAndEnter(m, "/name")

	root := m.(Root)
	if len(root.transcript) != 2 {
		t.Fatalf("expected two notice entries, got %d: %v", len(root.transcript), root.transcript)
	}
	if !strings.Contains(root.transcript[1].text, "mi sesion") {
		t.Errorf("second /name should report the current title, got %q", root.transcript[1].text)
	}
}

// TestSlashNamePersistsViaTitleStore is the one test that needs a real
// (fake) SessionTitleStore wired in, mirroring
// TestSlashThemePersistsViaThemeStore's own pattern.
func TestSlashNamePersistsViaTitleStore(t *testing.T) {
	store := &fakeTitleStore{}
	root := NewRoot(Options{
		Version:    "0.0.0-test",
		Theme:      theme.Load(""),
		Cap:        theme.CapNone,
		NoTTY:      true,
		TitleStore: store,
	})
	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/name un nombre nuevo")

	if len(store.saved) != 1 || store.saved[0] != "un nombre nuevo" {
		t.Errorf("SessionTitleStore.SetTitle calls = %v, want exactly one call with %q", store.saved, "un nombre nuevo")
	}
}

// TestSlashNameSaveFailureStillRenames mirrors
// TestSlashThemeSaveFailureStillSwitches: a persistence failure must not
// undo the in-memory rename, only report itself alongside the
// confirmation.
func TestSlashNameSaveFailureStillRenames(t *testing.T) {
	store := &fakeTitleStore{err: errSaveFailedForTest}
	root := NewRoot(Options{
		Version:    "0.0.0-test",
		Theme:      theme.Load(""),
		Cap:        theme.CapNone,
		NoTTY:      true,
		TitleStore: store,
	})
	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/name un nombre nuevo")

	got := m.(Root)
	if got.conv.Title != "un nombre nuevo" {
		t.Errorf("conv.Title = %q, want the rename to apply despite the save error", got.conv.Title)
	}
	if !strings.Contains(got.transcript[0].text, "no se pudo guardar") {
		t.Errorf("notice should surface the save failure, got %q", got.transcript[0].text)
	}
}

// TestSlashNameNilTitleStoreIsANoOp is TitleStore's own documented default:
// nil must not panic, and the rename still applies for the running
// session — the same "nothing wired, nothing happens beyond the in-memory
// effect" rule ThemeStore/EvolveStore already establish.
func TestSlashNameNilTitleStoreIsANoOp(t *testing.T) {
	var m tea.Model = newHeadlessRoot() // titleStore left nil
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/name sin persistencia")

	root := m.(Root)
	if root.conv.Title != "sin persistencia" {
		t.Errorf("conv.Title = %q, want the rename to apply even with no TitleStore", root.conv.Title)
	}
	if strings.Contains(root.transcript[0].text, "no se pudo guardar") {
		t.Errorf("a nil titleStore is not a failure, should not report one: %q", root.transcript[0].text)
	}
}

// TestOptionsTitleStoreIsWiredIntoRoot is the same regression shape
// TestOptionsRecorderIsWiredIntoRoot/TestOptionsSessionListerIsWiredIntoRoot
// already establish: it goes through NewRoot(Options{...}) rather than
// assigning the private field directly, so a future refactor that drops
// Options.TitleStore on the floor fails a test instead of silently making
// every real /name persistence attempt a no-op.
func TestOptionsTitleStoreIsWiredIntoRoot(t *testing.T) {
	store := &fakeTitleStore{}
	root := NewRoot(Options{TitleStore: store})
	if root.titleStore == nil {
		t.Fatal("NewRoot did not wire Options.TitleStore into Root.titleStore — /name would never persist")
	}
}

package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/catalog"
)

// fakeCurationStore is an in-memory CurationStore for tests — the same
// role fakeTrustStore-shaped helpers already play elsewhere in this
// package's test suite (mirroring the real fileCurationStore's own
// contract without touching a filesystem).
type fakeCurationStore struct {
	hidden map[string]bool
}

func newFakeCurationStore(hiddenRefs ...string) *fakeCurationStore {
	s := &fakeCurationStore{hidden: map[string]bool{}}
	for _, ref := range hiddenRefs {
		s.hidden[strings.ToLower(ref)] = true
	}
	return s
}

func (s *fakeCurationStore) IsHidden(ref string) bool { return s.hidden[strings.ToLower(ref)] }
func (s *fakeCurationStore) Hide(ref string) error {
	s.hidden[strings.ToLower(ref)] = true
	return nil
}
func (s *fakeCurationStore) Unhide(ref string) error {
	delete(s.hidden, strings.ToLower(ref))
	return nil
}
func (s *fakeCurationStore) Reason(ref string) string {
	if s.IsHidden(ref) {
		return curationHideReason
	}
	return ""
}

// rootWithCatalogAndCuration is rootWithCatalog plus a CurationStore attached
// directly to the unexported field — the same pattern rootWithCatalog itself
// uses for root.cat (slashrun_internal_test.go).
func rootWithCatalogAndCuration(cat *catalog.Catalog, store CurationStore) Root {
	root := rootWithCatalog(cat)
	root.curationStore = store
	return root
}

// TestPickerCtrlXHidesTheSelectedModel pins the ordinary case: ctrl+x on a
// visible row hides that model (row count drops, the store records it) —
// design doc §2's own first bullet.
func TestPickerCtrlXHidesTheSelectedModel(t *testing.T) {
	store := newFakeCurationStore()
	root := rootWithCatalogAndCuration(catalogWithModels("omni/son45"), store)
	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})

	before := countModelRows(m.(Root).picker.rows)
	if before != 1 {
		t.Fatalf("expected 1 model row before hiding, got %d", before)
	}
	// Move onto the model row (row 0 is the provider header).
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})

	m, _ = m.Update(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})

	got := m.(Root)
	if n := countModelRows(got.picker.rows); n != 0 {
		t.Fatalf("expected 0 model rows after ctrl+x, got %d", n)
	}
	if !store.IsHidden("omni/son45") {
		t.Error("ctrl+x should have recorded the hide in the CurationStore")
	}
}

// TestPickerCtrlHRevealsHiddenRowsDimmedWithReason pins ctrl+h: hidden rows
// come back, tagged with their reason — design doc §2's wireframe.
func TestPickerCtrlHRevealsHiddenRowsDimmedWithReason(t *testing.T) {
	store := newFakeCurationStore("omni/son45")
	root := rootWithCatalogAndCuration(catalogWithModels("omni/son45"), store)
	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})

	if n := countModelRows(m.(Root).picker.rows); n != 0 {
		t.Fatalf("expected the pre-hidden model to be absent before ctrl+h, got %d rows", n)
	}

	m, _ = m.Update(tea.KeyPressMsg{Code: 'h', Mod: tea.ModCtrl})

	got := m.(Root)
	if n := countModelRows(got.picker.rows); n != 1 {
		t.Fatalf("expected the hidden model to reappear after ctrl+h, got %d rows", n)
	}
	var found bool
	for _, row := range got.picker.rows {
		if row.header {
			continue
		}
		found = true
		if !row.hidden {
			t.Error("row.hidden should be true once ctrl+h reveals it")
		}
		if row.hiddenReason != curationHideReason {
			t.Errorf("hiddenReason = %q, want %q", row.hiddenReason, curationHideReason)
		}
	}
	if !found {
		t.Fatal("expected to find the model row after ctrl+h")
	}
}

// TestPickerCtrlXOnAShownHiddenRowUnhidesIt pins the toggle half of ctrl+x:
// "same key, reads as a toggle" (design doc §2) — pressing it again on a row
// already shown dimmed via ctrl+h un-hides it.
func TestPickerCtrlXOnAShownHiddenRowUnhidesIt(t *testing.T) {
	store := newFakeCurationStore("omni/son45")
	root := rootWithCatalogAndCuration(catalogWithModels("omni/son45"), store)
	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	m, _ = m.Update(tea.KeyPressMsg{Code: 'h', Mod: tea.ModCtrl})
	// Move onto the (now visible, dimmed) model row.
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})

	m, _ = m.Update(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})

	got := m.(Root)
	if store.IsHidden("omni/son45") {
		t.Error("ctrl+x on a shown-hidden row should have un-hidden it")
	}
	// showHidden is still on, so the now-not-hidden row should be visible
	// and no longer flagged hidden.
	var stillHidden bool
	for _, row := range got.picker.rows {
		if !row.header && row.hidden {
			stillHidden = true
		}
	}
	if stillHidden {
		t.Error("no row should still report hidden = true after un-hiding")
	}
}

// TestPickerTypingLiteralXStillFiltersNeverHides is design doc §2.3's own
// closing criterion: a bare "x" keystroke must reach typeText like any other
// character, never be mistaken for the ctrl+x chord.
func TestPickerTypingLiteralXStillFiltersNeverHides(t *testing.T) {
	store := newFakeCurationStore()
	root := rootWithCatalogAndCuration(catalogWithModels("omni/xon45", "other/unrelated"), store)
	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})

	m, _ = m.Update(tea.KeyPressMsg{Text: "x", Code: 'x'})

	got := m.(Root)
	if got.picker.query != "x" {
		t.Fatalf("typing x should reach typeText, query = %q, want %q", got.picker.query, "x")
	}
	if store.IsHidden("omni/xon45") {
		t.Error("typing the literal letter x must never hide a model")
	}
}

// TestPickerCtrlXAndCtrlHAreNoOpsWithoutACurationStore confirms Layer 2's
// nil-store degradation: every pre-F5 caller (curationStore == nil) keeps
// today's behaviour exactly, per CurationStore's own doc comment.
func TestPickerCtrlXAndCtrlHAreNoOpsWithoutACurationStore(t *testing.T) {
	root := rootWithCatalog(catalogWithModels("omni/son45"))
	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})

	m, _ = m.Update(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
	m, _ = m.Update(tea.KeyPressMsg{Code: 'h', Mod: tea.ModCtrl})

	got := m.(Root)
	if n := countModelRows(got.picker.rows); n != 1 {
		t.Fatalf("expected the model to remain visible with no curationStore, got %d rows", n)
	}
}

// TestPickerHiddenCountMatchesStoreForCurrentCandidates pins the footer's
// "N shown · M hidden" count (design doc §2's wireframe) against a
// representative case with a mix of hidden and visible models.
func TestPickerHiddenCountMatchesStoreForCurrentCandidates(t *testing.T) {
	store := newFakeCurationStore("omni/son45")
	root := rootWithCatalogAndCuration(catalogWithModels("omni/son45", "omni/gpt-5"), store)
	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})

	if n := m.(Root).picker.countCurationHidden(); n != 1 {
		t.Fatalf("countCurationHidden() = %d, want 1", n)
	}
}

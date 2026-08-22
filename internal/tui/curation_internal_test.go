package tui

import (
	"sort"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/catalog"
)

// fakeCurationStore is an in-memory CurationStore for tests — the same
// role fakeTrustStore-shaped helpers already play elsewhere in this
// package's test suite (mirroring the real fileCurationStore's own
// contract without touching a filesystem). hidden/kept are keyed by the
// ORIGINAL ref (not lower-cased) so Hidden() can return something worth
// asserting against; lookups still fold case via the lower-cased
// membership maps below, matching fileCurationStore/curation.Store's own
// case-insensitive contract.
type fakeCurationStore struct {
	hidden map[string]string // lower(ref) -> original ref
	kept   map[string]bool   // lower(ref) -> true
}

func newFakeCurationStore(hiddenRefs ...string) *fakeCurationStore {
	s := &fakeCurationStore{hidden: map[string]string{}, kept: map[string]bool{}}
	for _, ref := range hiddenRefs {
		s.hidden[strings.ToLower(ref)] = ref
	}
	return s
}

func (s *fakeCurationStore) IsHidden(ref string) bool {
	_, ok := s.hidden[strings.ToLower(ref)]
	return ok
}
func (s *fakeCurationStore) Hide(ref string) error {
	delete(s.kept, strings.ToLower(ref))
	s.hidden[strings.ToLower(ref)] = ref
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
func (s *fakeCurationStore) Keep(ref string) error {
	delete(s.hidden, strings.ToLower(ref))
	s.kept[strings.ToLower(ref)] = true
	return nil
}
func (s *fakeCurationStore) Hidden() []string {
	out := make([]string, 0, len(s.hidden))
	for _, ref := range s.hidden {
		out = append(out, ref)
	}
	sort.Strings(out)
	return out
}
func (s *fakeCurationStore) Reset() error {
	s.hidden = map[string]string{}
	return nil
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

// TestSlashModelHideHidesAnUnambiguousMatch pins "/model hide <query>"
// (design doc §2.1): an unambiguous query resolves exactly like /model's
// own bare form and calls Hide, never opening the picker or switching the
// active model.
func TestSlashModelHideHidesAnUnambiguousMatch(t *testing.T) {
	store := newFakeCurationStore()
	root := rootWithCatalogAndCuration(catalogWithModels("omni/son45", "other/unrelated"), store)
	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/model hide omni/son45")

	got := m.(Root)
	if got.mode != ModeChat {
		t.Fatalf("mode = %v, want ModeChat: /model hide must not open the picker on an unambiguous match", got.mode)
	}
	if !store.IsHidden("omni/son45") {
		t.Error("/model hide should have recorded the hide in the CurationStore")
	}
	if got.model == "omni/son45" {
		t.Error("/model hide must never switch the active model")
	}
	if len(got.transcript) != 1 || !strings.Contains(got.transcript[0].text, "omni/son45") {
		t.Fatalf("expected one notice naming the model, got %v", got.transcript)
	}
}

// TestSlashModelKeepPinsTheModelAndUnhidesIt pins "/model keep <query>":
// the inverse verb, routed through the same resolver and dispatch.
func TestSlashModelKeepPinsTheModelAndUnhidesIt(t *testing.T) {
	store := newFakeCurationStore("omni/son45")
	root := rootWithCatalogAndCuration(catalogWithModels("omni/son45"), store)
	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/model keep omni/son45")

	got := m.(Root)
	if store.IsHidden("omni/son45") {
		t.Error("/model keep should have removed the ref from the hide list")
	}
	if !store.kept["omni/son45"] {
		t.Error("/model keep should have recorded the keep in the CurationStore")
	}
	if len(got.transcript) != 1 || !strings.Contains(got.transcript[0].text, "omni/son45") {
		t.Fatalf("expected one notice naming the model, got %v", got.transcript)
	}
}

// TestSlashModelHideWithAnAmbiguousQueryOpensThePickerPrefiltered pins
// design doc §2.1's own instruction: "resolve with §4.5's resolver, hide
// the winner (ambiguous -> picker prefiltered, never a bare 'not found')".
func TestSlashModelHideWithAnAmbiguousQueryOpensThePickerPrefiltered(t *testing.T) {
	store := newFakeCurationStore()
	root := rootWithCatalogAndCuration(catalogWithModels("a/gpt-5", "b/gpt-5"), store)
	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/model hide gpt5")

	got := m.(Root)
	if got.mode != ModePicker {
		t.Fatalf("mode = %v, want ModePicker for an ambiguous /model hide query", got.mode)
	}
	if store.IsHidden("a/gpt-5") || store.IsHidden("b/gpt-5") {
		t.Error("an ambiguous /model hide must not guess and hide either candidate")
	}
}

// TestSlashModelHideWithNoCurationStoreReportsInsteadOfPanicking pins the
// nil-store degradation this command shares with the picker's own ctrl+x
// (CurationStore's own doc comment): no store configured means nothing can
// persist, reported plainly rather than silently doing nothing or crashing.
func TestSlashModelHideWithNoCurationStoreReportsInsteadOfPanicking(t *testing.T) {
	var m tea.Model = rootWithCatalog(catalogWithModels("omni/son45"))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/model hide omni/son45")

	got := m.(Root)
	if len(got.transcript) != 1 {
		t.Fatalf("expected one notice, got %d: %v", len(got.transcript), got.transcript)
	}
	if !strings.Contains(got.transcript[0].text, "almacen de curacion") {
		t.Errorf("notice should explain there is no curation store, got %q", got.transcript[0].text)
	}
}

// TestSlashModelsHiddenListsRefsAndReasons pins "/models hidden" (design
// doc §2.1): every ref the store currently hides, plus its reason, read
// from the store directly rather than m.cat (which a real curated catalog
// would already have filtered these refs out of).
func TestSlashModelsHiddenListsRefsAndReasons(t *testing.T) {
	store := newFakeCurationStore("omni/son45", "a/gpt-5")
	root := rootWithCatalogAndCuration(catalogWithModels("other/unrelated"), store)
	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/models hidden")

	got := m.(Root)
	if len(got.transcript) != 1 {
		t.Fatalf("expected one notice, got %d: %v", len(got.transcript), got.transcript)
	}
	text := got.transcript[0].text
	for _, want := range []string{"omni/son45", "a/gpt-5", curationHideReason} {
		if !strings.Contains(text, want) {
			t.Errorf("notice missing %q, got:\n%s", want, text)
		}
	}
}

// TestSlashModelsHiddenWithNothingHiddenSaysSo pins the empty case: a
// store with nothing hidden reports that plainly instead of an empty list.
func TestSlashModelsHiddenWithNothingHiddenSaysSo(t *testing.T) {
	store := newFakeCurationStore()
	root := rootWithCatalogAndCuration(catalogWithModels("omni/son45"), store)
	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/models hidden")

	got := m.(Root)
	if len(got.transcript) != 1 || !strings.Contains(got.transcript[0].text, "no hay modelos escondidos") {
		t.Fatalf("expected an empty-state notice, got %v", got.transcript)
	}
}

// TestSlashModelsResetDropsEveryHide pins "/models reset": every user hide
// is dropped, reported with the count.
func TestSlashModelsResetDropsEveryHide(t *testing.T) {
	store := newFakeCurationStore("omni/son45", "a/gpt-5")
	root := rootWithCatalogAndCuration(catalogWithModels("other/unrelated"), store)
	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/models reset")

	got := m.(Root)
	if len(store.Hidden()) != 0 {
		t.Errorf("expected every hide dropped, store still reports %v", store.Hidden())
	}
	if len(got.transcript) != 1 || !strings.Contains(got.transcript[0].text, "2") {
		t.Fatalf("expected a notice reporting the count, got %v", got.transcript)
	}
}

// TestSlashModelsCommandWithUnrecognisedArgumentFallsThroughToTheListing
// confirms "/models <anything else>" (including a stray word that is not
// "hidden"/"reset") keeps behaving exactly like a bare "/models" always
// has — the sub-verb dispatch must never swallow the ordinary listing.
func TestSlashModelsCommandWithUnrecognisedArgumentFallsThroughToTheListing(t *testing.T) {
	root := rootWithCatalog(catalogWithModels("a/gpt-5"))
	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/models bogus")

	got := m.(Root)
	if len(got.transcript) != 1 || !strings.Contains(got.transcript[0].text, "gpt-5") {
		t.Fatalf("expected the ordinary listing, got %v", got.transcript)
	}
}

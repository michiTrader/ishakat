package app

import (
	"path/filepath"
	"testing"

	"github.com/MichiTrader/ishakat/internal/curation"
)

func TestFileCurationStoreHidePersistsAndReloads(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "curation.json")

	store := newFileCurationStore(path)
	if store.IsHidden("omni/son45") {
		t.Fatal("a fresh store should not report anything hidden")
	}
	if err := store.Hide("omni/son45"); err != nil {
		t.Fatalf("Hide: %v", err)
	}
	if !store.IsHidden("omni/son45") {
		t.Error("IsHidden should be true immediately after Hide")
	}
	if got := store.Reason("omni/son45"); got != "hidden by you" {
		t.Errorf("Reason = %q, want %q", got, "hidden by you")
	}

	// Reload from disk into a fresh store and confirm the hide survived.
	reloaded := newFileCurationStore(path)
	if !reloaded.IsHidden("omni/son45") {
		t.Error("a reloaded store should still report the ref hidden")
	}

	onDisk, err := curation.Load(path)
	if err != nil {
		t.Fatalf("curation.Load: %v", err)
	}
	if !onDisk.IsHidden("omni/son45") {
		t.Error("curation.json on disk should record the hide")
	}
}

func TestFileCurationStoreUnhidePersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "curation.json")

	store := newFileCurationStore(path)
	if err := store.Hide("omni/son45"); err != nil {
		t.Fatalf("Hide: %v", err)
	}
	if err := store.Unhide("omni/son45"); err != nil {
		t.Fatalf("Unhide: %v", err)
	}
	if store.IsHidden("omni/son45") {
		t.Error("IsHidden should be false immediately after Unhide")
	}
	if got := store.Reason("omni/son45"); got != "" {
		t.Errorf("Reason after Unhide = %q, want empty", got)
	}

	reloaded := newFileCurationStore(path)
	if reloaded.IsHidden("omni/son45") {
		t.Error("a reloaded store should not report the un-hidden ref as hidden")
	}
}

// TestFileCurationStoreKeepUnhidesAndPersists pins /model keep's own verb:
// Keep pulls a ref OUT of the hide list (unlike Unhide, it also has to work
// for a ref that was never hidden through this store at all — the common
// case, a model [catalog.curate]'s own automatic rules hid), and the pin
// survives a reload.
func TestFileCurationStoreKeepUnhidesAndPersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "curation.json")

	store := newFileCurationStore(path)
	if err := store.Hide("omni/son45"); err != nil {
		t.Fatalf("Hide: %v", err)
	}
	if err := store.Keep("omni/son45"); err != nil {
		t.Fatalf("Keep: %v", err)
	}
	if store.IsHidden("omni/son45") {
		t.Error("Keep should remove the ref from the hide list")
	}

	onDisk, err := curation.Load(path)
	if err != nil {
		t.Fatalf("curation.Load: %v", err)
	}
	if !onDisk.IsKept("omni/son45") {
		t.Error("curation.json on disk should record the keep")
	}
}

// TestFileCurationStoreHiddenListsEveryHiddenRefSorted pins /models
// hidden's own data source: Hidden() has to enumerate every ref this
// store's hide list holds, sorted, regardless of insertion order.
func TestFileCurationStoreHiddenListsEveryHiddenRefSorted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "curation.json")

	store := newFileCurationStore(path)
	if got := store.Hidden(); len(got) != 0 {
		t.Fatalf("a fresh store's Hidden() = %v, want empty", got)
	}
	if err := store.Hide("b/model-b"); err != nil {
		t.Fatalf("Hide: %v", err)
	}
	if err := store.Hide("a/model-a"); err != nil {
		t.Fatalf("Hide: %v", err)
	}

	got := store.Hidden()
	want := []string{"a/model-a", "b/model-b"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("Hidden() = %v, want %v", got, want)
	}
}

// TestFileCurationStoreResetDropsHidesKeepsKeptAndPersists pins /models
// reset's own definition (design doc §2.1): every hide is dropped, but a
// Keep is left alone — a reset undoes accidental noise, not a deliberate
// pin.
func TestFileCurationStoreResetDropsHidesKeepsKeptAndPersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "curation.json")

	store := newFileCurationStore(path)
	if err := store.Hide("omni/hidden-one"); err != nil {
		t.Fatalf("Hide: %v", err)
	}
	if err := store.Keep("omni/kept-one"); err != nil {
		t.Fatalf("Keep: %v", err)
	}

	if err := store.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if store.IsHidden("omni/hidden-one") {
		t.Error("Reset should have dropped the hide")
	}
	if got := store.Hidden(); len(got) != 0 {
		t.Errorf("Hidden() after Reset = %v, want empty", got)
	}

	reloaded := newFileCurationStore(path)
	if reloaded.IsHidden("omni/hidden-one") {
		t.Error("a reloaded store should not see the reset hide come back")
	}
	onDisk, err := curation.Load(path)
	if err != nil {
		t.Fatalf("curation.Load: %v", err)
	}
	if !onDisk.IsKept("omni/kept-one") {
		t.Error("Reset must not touch Kept")
	}
}

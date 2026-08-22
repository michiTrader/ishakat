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

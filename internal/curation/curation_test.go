package curation

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadMissingFileReturnsEmptyStore(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err != nil {
		t.Fatalf("Load() error = %v, want nil for a missing file", err)
	}
	if len(s.Hidden) != 0 || len(s.Kept) != 0 {
		t.Fatalf("Store = %+v, want empty", s)
	}
}

func TestLoadEmptyPathReturnsNoteNotError(t *testing.T) {
	s, err := Load("")
	if err != nil {
		t.Fatalf("Load(\"\") error = %v, want nil", err)
	}
	if s.Note == "" {
		t.Fatal("Note is empty, want an explanation for an unconfigured path")
	}
}

func TestLoadCorruptFileDegradesGracefully(t *testing.T) {
	path := filepath.Join(t.TempDir(), "curation.json")
	if err := os.WriteFile(path, []byte("not valid json"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil (corrupt content degrades, never fails)", err)
	}
	if len(s.Hidden) != 0 || len(s.Kept) != 0 {
		t.Fatalf("Store = %+v, want empty on corrupt content", s)
	}
	if s.Note == "" {
		t.Fatal("Note is empty, want an explanation of the corruption")
	}
}

func TestLoadFutureVersionDegradesGracefully(t *testing.T) {
	path := filepath.Join(t.TempDir(), "curation.json")
	if err := os.WriteFile(path, []byte(`{"v":999,"hidden":[{"ref":"x","at":"2026-08-16T00:00:00Z"}]}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if len(s.Hidden) != 0 {
		t.Fatalf("Hidden = %v, want empty for an unrecognized version", s.Hidden)
	}
	if s.Note == "" {
		t.Fatal("Note is empty, want an explanation of the version mismatch")
	}
}

func TestHideThenIsHiddenRoundTrips(t *testing.T) {
	s := &Store{}
	at := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	s.Hide("gemini-direct/gemini-embedding-2", at)

	if !s.IsHidden("gemini-direct/gemini-embedding-2") {
		t.Fatal("IsHidden() = false, want true for a ref that was just Hide()n")
	}
	if !s.IsHidden("GEMINI-DIRECT/Gemini-Embedding-2") {
		t.Fatal("IsHidden() = false, want true (lookup should be case-insensitive)")
	}
	if s.IsKept("gemini-direct/gemini-embedding-2") {
		t.Fatal("IsKept() = true, want false for a hidden-only ref")
	}
}

func TestHideIsIdempotent(t *testing.T) {
	s := &Store{}
	at := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	s.Hide("a/b", at)
	s.Hide("a/b", at.Add(time.Hour))

	if len(s.Hidden) != 1 {
		t.Fatalf("Hidden has %d entries, want 1 (a second Hide of the same ref must not duplicate)", len(s.Hidden))
	}
	if s.Hidden[0].At != at.UTC().Format(time.RFC3339) {
		t.Fatalf("At = %q, want the original timestamp preserved", s.Hidden[0].At)
	}
}

func TestKeepMovesRefOutOfHidden(t *testing.T) {
	s := &Store{}
	now := time.Now()
	s.Hide("a/b", now)
	s.Keep("a/b", now)

	if s.IsHidden("a/b") {
		t.Fatal("IsHidden() = true, want false after Keep() moved the ref")
	}
	if !s.IsKept("a/b") {
		t.Fatal("IsKept() = false, want true after Keep()")
	}
}

func TestHideMovesRefOutOfKept(t *testing.T) {
	s := &Store{}
	now := time.Now()
	s.Keep("a/b", now)
	s.Hide("a/b", now)

	if s.IsKept("a/b") {
		t.Fatal("IsKept() = true, want false after Hide() moved the ref")
	}
	if !s.IsHidden("a/b") {
		t.Fatal("IsHidden() = false, want true after Hide()")
	}
}

func TestUnhideRemovesWithoutKeeping(t *testing.T) {
	s := &Store{}
	s.Hide("a/b", time.Now())
	s.Unhide("a/b")

	if s.IsHidden("a/b") {
		t.Fatal("IsHidden() = true, want false after Unhide()")
	}
	if s.IsKept("a/b") {
		t.Fatal("IsKept() = true, want false -- Unhide is not Keep")
	}
}

func TestSaveThenLoadPreservesBothLists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "curation.json")
	s := &Store{}
	now := time.Now()
	s.Hide("a/hidden-one", now)
	s.Keep("b/kept-one", now)

	if err := Save(path, s); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !loaded.IsHidden("a/hidden-one") {
		t.Fatal("hidden ref not found after round trip")
	}
	if !loaded.IsKept("b/kept-one") {
		t.Fatal("kept ref not found after round trip")
	}
}

func TestSaveWritesReadableFilePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "curation.json")
	s := &Store{}
	s.Hide("a/b", time.Now())
	if err := Save(path, s); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestNilStoreIsHiddenIsKeptAreSafe(t *testing.T) {
	var s *Store
	if s.IsHidden("a/b") {
		t.Fatal("IsHidden() on nil Store = true, want false")
	}
	if s.IsKept("a/b") {
		t.Fatal("IsKept() on nil Store = true, want false")
	}
}

package trust

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
	if len(s.Records) != 0 {
		t.Fatalf("Records = %v, want empty", s.Records)
	}
}

func TestSetThenLookupRoundTrips(t *testing.T) {
	s := &Store{}
	at := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	s.Set("/home/user/dev/orbital-dash", "auto", at)

	rec, ok := s.Lookup("/home/user/dev/orbital-dash")
	if !ok {
		t.Fatal("Lookup() ok = false, want true for a path that was just Set")
	}
	if rec.Autonomy != "auto" {
		t.Fatalf("Autonomy = %q, want %q", rec.Autonomy, "auto")
	}
	if rec.DecidedAt == "" {
		t.Fatal("DecidedAt is empty, want an RFC3339 timestamp")
	}
}

// TestClosingCriterionSecondRunInAKnownProjectAsksNothing is Step 30's own
// closing criterion (docs/PLAN.md §21.14 row 30), read at this package's
// level: a Store built from a Set-then-Save-then-Load round trip -- the
// exact sequence a first, then a second, ishakat run performs against the
// same project path -- must report the project as already answered on the
// second run, without the caller (internal/app.Run, in practice) ever
// needing to show the dialog again.
func TestClosingCriterionSecondRunInAKnownProjectAsksNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trust.json")
	project := "/home/user/dev/orbital-dash"

	// First run: nothing saved yet, the dialog must fire.
	first, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if _, ok := first.Lookup(project); ok {
		t.Fatal("first run: Lookup() ok = true, want false (no decision saved yet)")
	}
	first.Set(project, "auto", time.Now())
	if err := Save(path, first); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Second run: a fresh Load of the same path must already know.
	second, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	rec, ok := second.Lookup(project)
	if !ok {
		t.Fatal("second run: Lookup() ok = false, want true -- the trust dialog would fire again")
	}
	if rec.Autonomy != "auto" {
		t.Fatalf("second run: Autonomy = %q, want %q", rec.Autonomy, "auto")
	}
}

func TestLookupFallsBackToNearestAncestor(t *testing.T) {
	s := &Store{}
	s.Set("/home/user/dev", "agile", time.Now())

	rec, ok := s.Lookup("/home/user/dev/orbital-dash/sub/pkg")
	if !ok {
		t.Fatal("Lookup() ok = false, want true -- a parent decision must cover children (§21.4 layer 2)")
	}
	if rec.Autonomy != "agile" {
		t.Fatalf("Autonomy = %q, want %q (inherited from the parent)", rec.Autonomy, "agile")
	}
}

func TestLookupMostSpecificPathWins(t *testing.T) {
	s := &Store{}
	s.Set("/home/user/dev", "readonly", time.Now())
	s.Set("/home/user/dev/orbital-dash", "auto", time.Now())

	rec, ok := s.Lookup("/home/user/dev/orbital-dash")
	if !ok {
		t.Fatal("Lookup() ok = false, want true")
	}
	if rec.Autonomy != "auto" {
		t.Fatalf("Autonomy = %q, want %q (the more specific decision, not the parent's)", rec.Autonomy, "auto")
	}
}

func TestLookupUnrelatedPathIsNotCovered(t *testing.T) {
	s := &Store{}
	s.Set("/home/user/dev/orbital-dash", "auto", time.Now())

	if _, ok := s.Lookup("/home/user/other-project"); ok {
		t.Fatal("Lookup() ok = true, want false -- a sibling directory must not inherit an unrelated project's decision")
	}
}

func TestSetOverwritesExistingRecordForSamePath(t *testing.T) {
	s := &Store{}
	s.Set("/home/user/dev/orbital-dash", "agile", time.Now())
	s.Set("/home/user/dev/orbital-dash", "readonly", time.Now())

	if len(s.Records) != 1 {
		t.Fatalf("Records has %d entries, want 1 (re-deciding the same path must replace, not append)", len(s.Records))
	}
	rec, ok := s.Lookup("/home/user/dev/orbital-dash")
	if !ok || rec.Autonomy != "readonly" {
		t.Fatalf("Lookup() = %+v, %v, want autonomy=readonly", rec, ok)
	}
}

func TestSaveThenLoadPreservesMultipleRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trust.json")
	s := &Store{}
	s.Set("/home/user/dev/a", "auto", time.Now())
	s.Set("/home/user/dev/b", "readonly", time.Now())

	if err := Save(path, s); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(loaded.Records) != 2 {
		t.Fatalf("Records has %d entries, want 2", len(loaded.Records))
	}
	if _, ok := loaded.Lookup("/home/user/dev/a"); !ok {
		t.Fatal("project a not found after round trip")
	}
	if _, ok := loaded.Lookup("/home/user/dev/b"); !ok {
		t.Fatal("project b not found after round trip")
	}
}

func TestSaveWritesReadableFilePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trust.json")
	s := &Store{}
	s.Set("/home/user/dev/orbital-dash", "auto", time.Now())
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

func TestLoadSkipsCorruptedLineWithoutFailingTheWholeFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trust.json")
	content := "{\"path\":\"/a\",\"autonomy\":\"auto\",\"decided_at\":\"2026-08-16T00:00:00Z\"}\n" +
		"not valid json\n" +
		"{\"path\":\"/b\",\"autonomy\":\"agile\",\"decided_at\":\"2026-08-16T00:00:00Z\"}\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil (a corrupted line should be skipped, not fail the load)", err)
	}
	if len(s.Records) != 2 {
		t.Fatalf("Records has %d entries, want 2 (one line was corrupted and should have been skipped)", len(s.Records))
	}
}

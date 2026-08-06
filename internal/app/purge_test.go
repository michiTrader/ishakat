package app_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichiTrader/ishakat/internal/app"
	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/xdg"
)

// setXDGEnv points every XDG base directory the app writes to at
// subdirectories of a single t.TempDir(), the same isolation pattern
// internal/config's own tests already rely on (t.Setenv("XDG_CONFIG_HOME",
// ...)), but covering all four base dirs since PurgeTargets/Purge touch all
// of them, not just config.
func setXDGEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(dir, "data"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	return dir
}

// --- PurgeTargets ----------------------------------------------------------

// TestPurgeTargetsFullReturnsAllFourXDGDirsPlusSessions covers the union
// PurgeTargets' own doc comment describes: all four XDG base dirs plus the
// resolved sessions dir, deduplicated. With no customized [session] dir,
// SessionsDir() sits under DataDir(), so the naive union would report 5
// entries where PurgeTargets' doc comment says duplicates are kept (only
// exact-string duplicates are removed, not nested ones) — this test locks
// in that exact behavior rather than guessing at it.
func TestPurgeTargetsFullReturnsAllFourXDGDirsPlusSessions(t *testing.T) {
	setXDGEnv(t)

	got := app.PurgeTargets(nil, false)

	want := map[string]bool{
		xdg.ConfigDir():   true,
		xdg.CacheDir():    true,
		xdg.DataDir():     true,
		xdg.StateDir():    true,
		xdg.SessionsDir(): true,
	}
	if len(got) != len(want) {
		t.Fatalf("PurgeTargets(nil, false) = %v, want exactly the %d entries in %v", got, len(want), want)
	}
	for _, d := range got {
		if !want[d] {
			t.Errorf("PurgeTargets(nil, false) contains unexpected dir %q", d)
		}
		delete(want, d)
	}
	if len(want) != 0 {
		t.Errorf("PurgeTargets(nil, false) is missing %v", want)
	}
}

// TestPurgeTargetsSessionsOnlyReturnsJustSessionsDir covers the
// --sessions-only short-circuit: a single-element slice, not the full
// four-plus-one union.
func TestPurgeTargetsSessionsOnlyReturnsJustSessionsDir(t *testing.T) {
	setXDGEnv(t)

	got := app.PurgeTargets(nil, true)

	if len(got) != 1 || got[0] != xdg.SessionsDir() {
		t.Errorf("PurgeTargets(nil, true) = %v, want exactly [%s]", got, xdg.SessionsDir())
	}
}

// TestPurgeTargetsHonoursCustomSessionDir is the reason PurgeTargets takes
// a *config.Config at all: a customized [session] dir must win over
// xdg.SessionsDir(), the same precedence NewSessionRecorder/NewSessionLister
// already use.
func TestPurgeTargetsHonoursCustomSessionDir(t *testing.T) {
	setXDGEnv(t)
	custom := filepath.Join(t.TempDir(), "my-sessions")
	cfg := &config.Config{}
	cfg.Session.Dir = custom

	gotSessionsOnly := app.PurgeTargets(cfg, true)
	if len(gotSessionsOnly) != 1 || gotSessionsOnly[0] != custom {
		t.Errorf("PurgeTargets(cfg, true) = %v, want exactly [%s]", gotSessionsOnly, custom)
	}

	gotFull := app.PurgeTargets(cfg, false)
	found := false
	for _, d := range gotFull {
		if d == custom {
			found = true
		}
	}
	if !found {
		t.Errorf("PurgeTargets(cfg, false) = %v, does not include the customized session dir %q", gotFull, custom)
	}
}

// TestPurgeTargetsFullDedupesWhenSessionDirEqualsDefault guards the
// "deduplicated" half of the doc comment for the exact-match case: if a
// customized [session] dir happens to equal xdg.DataDir() itself (an
// unusual but not invalid configuration), it must not appear twice.
func TestPurgeTargetsFullDedupesWhenSessionDirEqualsDefault(t *testing.T) {
	setXDGEnv(t)
	cfg := &config.Config{}
	cfg.Session.Dir = xdg.DataDir()

	got := app.PurgeTargets(cfg, false)
	count := 0
	for _, d := range got {
		if d == xdg.DataDir() {
			count++
		}
	}
	if count != 1 {
		t.Errorf("PurgeTargets(cfg, false) lists xdg.DataDir() %d times, want exactly 1 (deduplicated)", count)
	}
	if len(got) != 4 {
		t.Errorf("PurgeTargets(cfg, false) = %v, want exactly 4 entries once session dir == DataDir()", got)
	}
}

// --- Purge -------------------------------------------------------------

// TestPurgeRemovesExistingDirs is the straightforward happy path: every
// target that exists on disk is removed and reported in Removed.
func TestPurgeRemovesExistingDirs(t *testing.T) {
	base := t.TempDir()
	dirA := filepath.Join(base, "a")
	dirB := filepath.Join(base, "b")
	for _, d := range []string{dirA, dirB} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "file.txt"), []byte("data"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	res, err := app.Purge([]string{dirA, dirB})
	if err != nil {
		t.Fatalf("Purge() error = %v", err)
	}
	if len(res.Removed) != 2 || len(res.Missing) != 0 {
		t.Errorf("Purge() result = %+v, want both dirs Removed and none Missing", res)
	}
	for _, d := range []string{dirA, dirB} {
		if _, err := os.Stat(d); !os.IsNotExist(err) {
			t.Errorf("%s still exists after Purge()", d)
		}
	}
}

// TestPurgeReportsMissingDirsAsNotAnError: a target that never existed is
// reported separately from Removed and does not produce an error — the
// same "no-op on absence" rule as RemoveAlias/RemoveFavorite.
func TestPurgeReportsMissingDirsAsNotAnError(t *testing.T) {
	base := t.TempDir()
	missing := filepath.Join(base, "does-not-exist")

	res, err := app.Purge([]string{missing})
	if err != nil {
		t.Fatalf("Purge() error = %v, want nil for a missing dir", err)
	}
	if len(res.Missing) != 1 || res.Missing[0] != missing {
		t.Errorf("Purge() result = %+v, want Missing = [%s]", res, missing)
	}
	if len(res.Removed) != 0 {
		t.Errorf("Purge() result = %+v, want empty Removed", res)
	}
}

// TestPurgeMixOfExistingAndMissing checks both outcomes are reported
// independently within a single call, in the same order they were given.
func TestPurgeMixOfExistingAndMissing(t *testing.T) {
	base := t.TempDir()
	exists := filepath.Join(base, "exists")
	missing := filepath.Join(base, "missing")
	if err := os.MkdirAll(exists, 0o700); err != nil {
		t.Fatal(err)
	}

	res, err := app.Purge([]string{exists, missing})
	if err != nil {
		t.Fatalf("Purge() error = %v", err)
	}
	if len(res.Removed) != 1 || res.Removed[0] != exists {
		t.Errorf("Purge() Removed = %v, want [%s]", res.Removed, exists)
	}
	if len(res.Missing) != 1 || res.Missing[0] != missing {
		t.Errorf("Purge() Missing = %v, want [%s]", res.Missing, missing)
	}
}

// TestPurgeEmptyTargetsIsANoOp: an empty slice (e.g. sessionsOnly with a
// dir that PurgeTargets already filtered to nothing) must not error and
// must return an empty, non-nil-ish result.
func TestPurgeEmptyTargetsIsANoOp(t *testing.T) {
	res, err := app.Purge(nil)
	if err != nil {
		t.Fatalf("Purge(nil) error = %v, want nil", err)
	}
	if len(res.Removed) != 0 || len(res.Missing) != 0 {
		t.Errorf("Purge(nil) result = %+v, want both empty", res)
	}
}

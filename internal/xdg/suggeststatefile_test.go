package xdg_test

import (
	"path/filepath"
	"testing"

	"github.com/MichiTrader/ishakat/internal/xdg"
)

// TestSuggestStateFileSitsBesideUsageFile locks §19.7's budget/decay
// counters to the same directory as the observation ledger (StateDir),
// while keeping them as two distinct files: usage.jsonl is an append-only
// ledger a user might hand-edit, suggest-state.json is small mutable
// counter state that must round-trip exactly. Desyncing one must never
// corrupt the other.
func TestSuggestStateFileSitsBesideUsageFile(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	got := xdg.SuggestStateFile()
	want := filepath.Join(xdg.StateDir(), "suggest-state.json")
	if got != want {
		t.Errorf("SuggestStateFile() = %q, want %q", got, want)
	}
	if filepath.Dir(got) != filepath.Dir(xdg.UsageFile()) {
		t.Errorf("SuggestStateFile() and UsageFile() live in different directories: %q vs %q",
			filepath.Dir(got), filepath.Dir(xdg.UsageFile()))
	}
	if got == xdg.UsageFile() {
		t.Errorf("SuggestStateFile() must not collide with UsageFile(): both = %q", got)
	}
}

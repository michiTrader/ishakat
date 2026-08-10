package agentsmd_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichiTrader/ishakat/internal/agentsmd"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

// TestResolveWithNoFilesIsEmptyAndSilent is the ordinary case: most projects
// have none of the three layers, and that must not look like an error.
func TestResolveWithNoFilesIsEmptyAndSilent(t *testing.T) {
	dir := t.TempDir()
	res := agentsmd.Resolve(filepath.Join(dir, "global", "AGENTS.md"), dir)

	if res.Text != "" {
		t.Errorf("Text = %q, want empty", res.Text)
	}
	if len(res.Files) != 0 {
		t.Errorf("Files = %v, want none", res.Files)
	}
	if res.Warn != "" {
		t.Errorf("Warn = %q, want empty: a missing file is not a warning", res.Warn)
	}
}

// TestResolveConcatenatesAllThreeLowToHighPrecedence is the core contract:
// unlike config.toml's replace-on-override merge (§5.1), prose rules stack —
// a project's AGENTS.md does not erase the user's global one, it adds to it.
func TestResolveConcatenatesAllThreeLowToHighPrecedence(t *testing.T) {
	dir := t.TempDir()
	globalPath := filepath.Join(dir, "global", "AGENTS.md")
	write(t, globalPath, "Always answer in English.")
	write(t, filepath.Join(dir, "AGENTS.md"), "This repo uses Go 1.26.")
	write(t, filepath.Join(dir, "AGENTS.local.md"), "My machine has no Docker.")

	res := agentsmd.Resolve(globalPath, dir)

	wantOrder := []string{"Always answer in English.", "This repo uses Go 1.26.", "My machine has no Docker."}
	gi, pi, li := strings.Index(res.Text, wantOrder[0]), strings.Index(res.Text, wantOrder[1]), strings.Index(res.Text, wantOrder[2])
	if gi == -1 || pi == -1 || li == -1 {
		t.Fatalf("Text is missing a layer, got: %q", res.Text)
	}
	if !(gi < pi && pi < li) {
		t.Errorf("layers are not in global < project < local order, got: %q", res.Text)
	}
	if len(res.Files) != 3 {
		t.Errorf("Files = %v, want all three paths", res.Files)
	}
	if res.Warn != "" {
		t.Errorf("Warn = %q, want empty", res.Warn)
	}
}

// TestResolveSkipsMissingLayersWithoutBreakingTheOthers covers the common
// real shape: a project AGENTS.md with no global or local file at all.
func TestResolveSkipsMissingLayersWithoutBreakingTheOthers(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "AGENTS.md"), "Project-only rule.")

	res := agentsmd.Resolve(filepath.Join(dir, "global", "AGENTS.md"), dir)

	if res.Text != "Project-only rule." {
		t.Errorf("Text = %q, want just the project rule", res.Text)
	}
	if len(res.Files) != 1 || res.Files[0] != filepath.Join(dir, "AGENTS.md") {
		t.Errorf("Files = %v, want only the project path", res.Files)
	}
}

// TestResolveTreatsAnEmptyFileAsAbsent guards against a warning firing on a
// project that ran `touch AGENTS.md` and has not written anything yet.
func TestResolveTreatsAnEmptyFileAsAbsent(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "AGENTS.md"), "   \n\n  ")

	res := agentsmd.Resolve(filepath.Join(dir, "global", "AGENTS.md"), dir)

	if res.Text != "" {
		t.Errorf("Text = %q, want empty for a whitespace-only file", res.Text)
	}
	if len(res.Files) != 0 {
		t.Errorf("Files = %v, want none: a blank file contributed nothing", res.Files)
	}
	if res.Warn != "" {
		t.Errorf("Warn = %q, want empty", res.Warn)
	}
}

// TestResolveWarnsOnAnUnreadableLayerButKeepsGoing is the failure mode that
// is not "missing": a file exists but cannot be read (here, a directory
// where AGENTS.md was expected), which must be reported without discarding
// whatever the other layers said.
func TestResolveWarnsOnAnUnreadableLayerButKeepsGoing(t *testing.T) {
	dir := t.TempDir()
	// A directory named AGENTS.md instead of a file: os.ReadFile fails on it
	// with a real, portable error, unlike chmod 0000 which root ignores.
	if err := os.MkdirAll(filepath.Join(dir, "AGENTS.md"), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	write(t, filepath.Join(dir, "AGENTS.local.md"), "Local rule still applies.")

	res := agentsmd.Resolve(filepath.Join(dir, "global", "AGENTS.md"), dir)

	if res.Warn == "" {
		t.Error("Warn is empty, want a message naming the unreadable project layer")
	}
	if !strings.Contains(res.Warn, "project") {
		t.Errorf("Warn = %q, want it to name the project layer", res.Warn)
	}
	if res.Text != "Local rule still applies." {
		t.Errorf("Text = %q, want the local layer to still come through", res.Text)
	}
}

// TestSourcesListsAllThreePathsRegardlessOfExistence backs doctor's own
// closing criterion (docs/PLAN.md §17): all three paths must be visible even
// when none of them exist yet.
func TestSourcesListsAllThreePathsRegardlessOfExistence(t *testing.T) {
	dir := t.TempDir()
	globalPath := filepath.Join(dir, "global", "AGENTS.md")

	srcs := agentsmd.Sources(globalPath, dir)
	if len(srcs) != 3 {
		t.Fatalf("Sources returned %d entries, want 3", len(srcs))
	}
	wantLayers := []agentsmd.Layer{agentsmd.Global, agentsmd.Project, agentsmd.Local}
	wantPaths := []string{globalPath, filepath.Join(dir, "AGENTS.md"), filepath.Join(dir, "AGENTS.local.md")}
	for i, src := range srcs {
		if src.Layer != wantLayers[i] {
			t.Errorf("Sources[%d].Layer = %v, want %v", i, src.Layer, wantLayers[i])
		}
		if src.Path != wantPaths[i] {
			t.Errorf("Sources[%d].Path = %q, want %q", i, src.Path, wantPaths[i])
		}
	}
}

func TestLayerString(t *testing.T) {
	cases := []struct {
		l    agentsmd.Layer
		want string
	}{
		{agentsmd.Global, "global"},
		{agentsmd.Project, "project"},
		{agentsmd.Local, "local"},
		{agentsmd.Layer(99), "unknown"},
	}
	for _, tc := range cases {
		if got := tc.l.String(); got != tc.want {
			t.Errorf("Layer(%d).String() = %q, want %q", tc.l, got, tc.want)
		}
	}
}

package xdg_test

import (
	"path/filepath"
	"testing"

	"github.com/MichiTrader/ishakat/internal/xdg"
)

// TestAgentsFileSitsBesideConfigFile locks Step 18's global layer to the same
// directory as config.toml: both are hand-edited files the user is meant to
// find together, not one under XDG_CONFIG_HOME and the other under
// XDG_DATA_HOME/XDG_CACHE_HOME where the rest of ConfigDir's siblings live.
func TestAgentsFileSitsBesideConfigFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	got := xdg.AgentsFile()
	want := filepath.Join(xdg.ConfigDir(), "AGENTS.md")
	if got != want {
		t.Errorf("AgentsFile() = %q, want %q", got, want)
	}
	if filepath.Dir(got) != filepath.Dir(xdg.ConfigFile()) {
		t.Errorf("AgentsFile() and ConfigFile() live in different directories: %q vs %q",
			filepath.Dir(got), filepath.Dir(xdg.ConfigFile()))
	}
}

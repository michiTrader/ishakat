package main

import (
	"path/filepath"
	"testing"
)

// setModelsXDGEnv isolates cmdModels' own config.Load call under one
// t.TempDir() with no config.toml at all — the same "fresh install, no
// providers configured" state config_test.go's own helper exercises
// elsewhere in this package — so these tests only check that --hidden/
// --why parse and reach app.Models without a flag.Parse error, not that
// they resolve a real model (internal/app/models_cmd_test.go already
// covers that end-to-end against a real catalog).
func setModelsXDGEnv(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(dir, "data"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
}

// TestCmdModelsHiddenFlagParses is design doc §2.1's `ishakat models
// --hidden`: the flag must parse (no ExitUsage) and, with nothing hidden
// on a fresh install, must not fail the way an unhandled flag or a nil
// dereference would (ExitOK, since "nothing hidden" is not a failure).
func TestCmdModelsHiddenFlagParses(t *testing.T) {
	setModelsXDGEnv(t)
	if code := cmdModels([]string{"--hidden"}); code != 0 {
		t.Fatalf("cmdModels --hidden = %d, want 0 (ExitOK)", code)
	}
}

// TestCmdModelsWhyFlagParses is `ishakat models --why <ref>`: the flag
// parses and reaches app.Models, which on a fresh install with no catalog
// entry for the ref reports "no model matches" (ExitError) rather than a
// usage error — the distinction this test actually cares about.
func TestCmdModelsWhyFlagParses(t *testing.T) {
	setModelsXDGEnv(t)
	if code := cmdModels([]string{"--why", "nonexistent-model-xyz"}); code == 2 {
		t.Fatalf("cmdModels --why = %d (ExitUsage), want the flag to parse", code)
	}
}

// TestCmdModelsAllFlagStillParses guards against a regression in the
// --all rewiring: it must still be accepted as a plain boolean flag.
func TestCmdModelsAllFlagStillParses(t *testing.T) {
	setModelsXDGEnv(t)
	if code := cmdModels([]string{"--all"}); code == 2 {
		t.Fatalf("cmdModels --all = %d (ExitUsage), want the flag to parse", code)
	}
}

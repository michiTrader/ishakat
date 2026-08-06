package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setPurgeXDGEnv mirrors internal/app's own purge_test.go helper: purge
// touches all four XDG base dirs plus the sessions dir, not just config, so
// every test here isolates all of them under one t.TempDir() rather than
// relying on config_test.go's XDG_CONFIG_HOME-only pattern.
func setPurgeXDGEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(dir, "data"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	return dir
}

// TestCmdPurgeNoTTYWithoutForceRefuses covers the safety rail cmdPurge's
// own doc comment describes: with no terminal attached to answer the
// confirmation prompt (exactly the state `go test` itself runs under) and
// no --force, purge must refuse rather than either hang forever waiting
// for an answer or silently proceed as if the answer were yes.
func TestCmdPurgeNoTTYWithoutForceRefuses(t *testing.T) {
	dir := setPurgeXDGEnv(t)
	configDir := filepath.Join(dir, "config", "ishakat")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(configDir, "config.toml")
	if err := os.WriteFile(marker, []byte("[app]\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if code := cmdPurge(nil); code != 2 {
		t.Errorf("cmdPurge(nil) with no TTY and no --force = %d, want 2", code)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("marker file was removed despite refusing the no-TTY purge: %v", err)
	}
}

// TestCmdPurgeForceRemovesEverything covers the --force path end to end:
// it must actually remove the XDG dirs PurgeTargets(nil, false) reports,
// without needing a TTY at all.
func TestCmdPurgeForceRemovesEverything(t *testing.T) {
	dir := setPurgeXDGEnv(t)
	configDir := filepath.Join(dir, "config", "ishakat")
	dataDir := filepath.Join(dir, "data", "ishakat")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte("[app]\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if code := cmdPurge([]string{"--force"}); code != 0 {
		t.Fatalf("cmdPurge([--force]) = %d, want 0", code)
	}
	if _, err := os.Stat(configDir); !os.IsNotExist(err) {
		t.Errorf("%s still exists after cmdPurge([--force])", configDir)
	}
	if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
		t.Errorf("%s still exists after cmdPurge([--force])", dataDir)
	}
}

// TestCmdPurgeForceShortFlag covers -f as the same thing as --force.
func TestCmdPurgeForceShortFlag(t *testing.T) {
	dir := setPurgeXDGEnv(t)
	configDir := filepath.Join(dir, "config", "ishakat")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}

	if code := cmdPurge([]string{"-f"}); code != 0 {
		t.Fatalf("cmdPurge([-f]) = %d, want 0", code)
	}
	if _, err := os.Stat(configDir); !os.IsNotExist(err) {
		t.Errorf("%s still exists after cmdPurge([-f])", configDir)
	}
}

// TestCmdPurgeSessionsOnlyLeavesConfigAlone is the P3 --sessions half:
// only the sessions dir should be removed, config/cache/state must be left
// untouched.
func TestCmdPurgeSessionsOnlyLeavesConfigAlone(t *testing.T) {
	dir := setPurgeXDGEnv(t)
	configDir := filepath.Join(dir, "config", "ishakat")
	sessionsDir := filepath.Join(dir, "data", "ishakat", "sessions")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte("[app]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionsDir, "session-1.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	if code := cmdPurge([]string{"--sessions", "--force"}); code != 0 {
		t.Fatalf("cmdPurge([--sessions --force]) = %d, want 0", code)
	}
	if _, err := os.Stat(sessionsDir); !os.IsNotExist(err) {
		t.Errorf("%s still exists after cmdPurge([--sessions --force])", sessionsDir)
	}
	if _, err := os.Stat(configDir); err != nil {
		t.Errorf("%s was removed by --sessions purge, want it left alone: %v", configDir, err)
	}
}

// TestCmdPurgeMissingDirsAreNotAnError: purging when nothing has ever been
// written (a brand-new environment) must not error out — every target is
// simply reported as Missing (see internal/app's PurgeResult).
func TestCmdPurgeMissingDirsAreNotAnError(t *testing.T) {
	setPurgeXDGEnv(t)

	if code := cmdPurge([]string{"--force"}); code != 0 {
		t.Errorf("cmdPurge([--force]) on an empty environment = %d, want 0", code)
	}
}

// TestCmdPurgeUnknownFlagIsUsageError guards the flag.ContinueOnError
// wiring: an unrecognized flag must produce a usage error (2), not a panic
// or a silent ignore.
func TestCmdPurgeUnknownFlagIsUsageError(t *testing.T) {
	setPurgeXDGEnv(t)

	if code := cmdPurge([]string{"--frobnicate"}); code != 2 {
		t.Errorf("cmdPurge([--frobnicate]) = %d, want 2", code)
	}
}

// TestPurgeDescriptionDiffersByScope guards purgeDescription's own two
// branches so `ishakat purge` and `ishakat purge --sessions` cannot
// silently start printing the same (misleading) description.
func TestPurgeDescriptionDiffersByScope(t *testing.T) {
	full := purgeDescription(false)
	sessionsOnly := purgeDescription(true)
	if full == sessionsOnly {
		t.Error("purgeDescription(false) and purgeDescription(true) must differ")
	}
	if !strings.Contains(sessionsOnly, "session") {
		t.Errorf("purgeDescription(true) = %q, want it to mention sessions", sessionsOnly)
	}
	if !strings.Contains(full, "configuration") && !strings.Contains(full, "credentials") {
		t.Errorf("purgeDescription(false) = %q, want it to mention config/credentials", full)
	}
}

// --- readPurgeConfirm --------------------------------------------------

// TestReadPurgeConfirmDefaultsToNo covers the "[y/N]" convention purge's
// own doc comment calls out: a bare Enter (empty line) must be treated as
// "no", not "yes" — the opposite default from offerDefaultModel's own
// readYesNo (provider.go), because purge is destructive/irreversible.
func TestReadPurgeConfirmDefaultsToNo(t *testing.T) {
	yes, err := readPurgeConfirm(strings.NewReader("\n"))
	if err != nil {
		t.Fatalf("readPurgeConfirm(\"\\n\") error = %v", err)
	}
	if yes {
		t.Error("readPurgeConfirm(\"\\n\") = true, want false (default must be NO)")
	}
}

func TestReadPurgeConfirmAcceptsYAndYes(t *testing.T) {
	for _, in := range []string{"y\n", "Y\n", "yes\n", "YES\n", "  yes  \n"} {
		yes, err := readPurgeConfirm(strings.NewReader(in))
		if err != nil {
			t.Fatalf("readPurgeConfirm(%q) error = %v", in, err)
		}
		if !yes {
			t.Errorf("readPurgeConfirm(%q) = false, want true", in)
		}
	}
}

func TestReadPurgeConfirmRejectsAnythingElse(t *testing.T) {
	for _, in := range []string{"n\n", "no\n", "maybe\n", "yesplease\n"} {
		yes, err := readPurgeConfirm(strings.NewReader(in))
		if err != nil {
			t.Fatalf("readPurgeConfirm(%q) error = %v", in, err)
		}
		if yes {
			t.Errorf("readPurgeConfirm(%q) = true, want false", in)
		}
	}
}

// TestReadPurgeConfirmEOFWithoutNewlineIsNotAnError: piping input with no
// trailing newline (echo -n "y" | ishakat purge) must still be read as a
// valid answer rather than erroring out on the resulting io.EOF.
func TestReadPurgeConfirmEOFWithoutNewlineIsNotAnError(t *testing.T) {
	yes, err := readPurgeConfirm(strings.NewReader("y"))
	if err != nil {
		t.Fatalf("readPurgeConfirm(\"y\" no newline) error = %v, want nil", err)
	}
	if !yes {
		t.Error("readPurgeConfirm(\"y\" no newline) = false, want true")
	}
}

// TestUsageMentionsPurgeSubcommand is a light guard mirroring
// TestUsageMentionsModelSubcommand (model_test.go).
func TestUsageMentionsPurgeSubcommand(t *testing.T) {
	if !strings.Contains(usage, "purge") {
		t.Error("usage text does not mention `purge`; the subcommand should be discoverable from `ishakat --help`")
	}
}

// TestKnownSubcommandsIncludesPurge guards main()'s own dispatch table.
func TestKnownSubcommandsIncludesPurge(t *testing.T) {
	found := false
	for _, s := range knownSubcommands {
		if s == "purge" {
			found = true
		}
	}
	if !found {
		t.Errorf("knownSubcommands = %v, missing \"purge\"", knownSubcommands)
	}
}

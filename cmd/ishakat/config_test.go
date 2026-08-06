package main

import (
	"os"
	"strings"
	"testing"

	"github.com/MichiTrader/ishakat/internal/config"
)

// TestCmdConfigInitDefaultsToMinimal covers P2 (3/3) of the 2026-08-06
// audit: `ishakat config init` with no flags used to always write the
// ~200-line fully annotated example. It now writes the short skeleton
// (config.MinimalTOML) by default, and only writes the annotated example
// (config.ExampleTOML) when --full is passed.
func TestCmdConfigInitDefaultsToMinimal(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	if code := cmdConfig([]string{"init"}); code != 0 {
		t.Fatalf("cmdConfig([init]) = %d, want 0", code)
	}

	got, err := readConfigTOML(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != config.MinimalTOML {
		t.Errorf("config init (no flags) did not write MinimalTOML:\ngot:\n%s\nwant:\n%s", got, config.MinimalTOML)
	}
}

// TestCmdConfigInitFullWritesExample is the --full counterpart: it must
// still produce byte-for-byte the same annotated example config init
// always used to write, so anyone relying on today's documented behaviour
// keeps it under an explicit flag.
func TestCmdConfigInitFullWritesExample(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	if code := cmdConfig([]string{"init", "--full"}); code != 0 {
		t.Fatalf("cmdConfig([init --full]) = %d, want 0", code)
	}

	got, err := readConfigTOML(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != config.ExampleTOML {
		t.Errorf("config init --full did not write ExampleTOML:\ngot:\n%s\nwant:\n%s", got, config.ExampleTOML)
	}
}

// TestCmdConfigInitRefusesToOverwriteWithoutForce guards the pre-existing
// --force gate: it must keep applying regardless of which content
// (minimal or full) would be written.
func TestCmdConfigInitRefusesToOverwriteWithoutForce(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	if code := cmdConfig([]string{"init"}); code != 0 {
		t.Fatalf("first cmdConfig([init]) = %d, want 0", code)
	}
	if code := cmdConfig([]string{"init"}); code != 1 {
		t.Fatalf("second cmdConfig([init]) without --force = %d, want 1 (must refuse to overwrite)", code)
	}

	// --force must still allow switching from minimal to full.
	if code := cmdConfig([]string{"init", "--full", "--force"}); code != 0 {
		t.Fatalf("cmdConfig([init --full --force]) = %d, want 0", code)
	}
	got, err := readConfigTOML(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != config.ExampleTOML {
		t.Error("--force did not allow overwriting the minimal file with --full's content")
	}
}

// TestCmdConfigInitWritesMode0600 guards the permission requirement
// carried over from the pre-existing full-example path: whichever content
// is written, config.toml must land at 0600, not the world/group-readable
// default a plain os.WriteFile without an explicit mode would leave.
func TestCmdConfigInitWritesMode0600(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	if code := cmdConfig([]string{"init"}); code != 0 {
		t.Fatalf("cmdConfig([init]) = %d, want 0", code)
	}

	info, err := os.Stat(dir + "/ishakat/config.toml")
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("config.toml mode = %#o, want 0600", mode)
	}
}

// TestCmdConfigInitMinimalFileIsUsable closes the loop end to end: the
// minimal file config init writes by default must actually config.Load
// successfully afterward, through the same XDG resolution the real binary
// uses (config.Load with UserPath left empty falls back to
// xdg.ConfigFile()) — not just when read directly as a string, which is
// what internal/config's own TestMinimalTOMLLoads already checks.
func TestCmdConfigInitMinimalFileIsUsable(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	if code := cmdConfig([]string{"init"}); code != 0 {
		t.Fatalf("cmdConfig([init]) = %d, want 0", code)
	}

	cfg, err := config.Load(config.Options{SkipProject: true})
	if err != nil {
		t.Fatalf("the minimal file config init wrote failed to load: %v", err)
	}
	if len(cfg.Providers) == 0 {
		t.Error("expected at least the built-in omniroute provider from defaults.toml")
	}
}

// TestCmdConfigInitNoArgsUsage is a narrow regression guard for the
// existing `ishakat config` (no subcommand) usage error, touched only
// incidentally by this change (the message was translated to English in
// the same session as the --full flag was added).
func TestCmdConfigInitNoArgsUsage(t *testing.T) {
	if code := cmdConfig(nil); code != 2 {
		t.Errorf("cmdConfig(nil) = %d, want 2", code)
	}
}

// TestCmdConfigInitUsageMentionsFull is a light guard that the top-level
// help text was updated alongside the flag, rather than shipping a flag
// nobody discovers without reading the source.
func TestCmdConfigInitUsageMentionsFull(t *testing.T) {
	if !strings.Contains(usage, "--full") {
		t.Error("usage text does not mention --full; config init's new flag should be discoverable from `ishakat --help`")
	}
}

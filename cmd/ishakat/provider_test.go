package main

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/MichiTrader/ishakat/internal/config"
)

// TestReadYesNo covers the "[Y/n]" convention offerDefaultModel relies on:
// empty input (bare Enter) is yes because Y is the capitalised default,
// anything starting with n/N is no, and anything else is treated as an
// affirmative answer rather than silently doing nothing.
func TestReadYesNo(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"\n", true},
		{"", true},
		{"y\n", true},
		{"Y\n", true},
		{"yes\n", true},
		{"n\n", false},
		{"N\n", false},
		{"no\n", false},
		{"whatever\n", true},
	}
	for _, c := range cases {
		got, err := readYesNo(strings.NewReader(c.input))
		if err != nil {
			t.Errorf("readYesNo(%q) error = %v", c.input, err)
			continue
		}
		if got != c.want {
			t.Errorf("readYesNo(%q) = %v, want %v", c.input, got, c.want)
		}
	}
}

// TestOfferDefaultModelSkipsWhenDefaultAlreadyWorks is the regression test
// for wiring config.SetDefaultModel (previously dead code, see its own doc
// comment) into `provider add`: when app.default_model already resolves to
// a usable provider, offerDefaultModel must not touch config.toml or print
// anything asking the user to change it.
func TestOfferDefaultModelSkipsWhenDefaultAlreadyWorks(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	// Seed a config.toml where app.default_model already points at a
	// working, credentialed provider — offerDefaultModel reloads
	// config.Load(config.Options{}) itself, so the seed must go through
	// the real file, not a struct literal.
	preset, err := config.ResolveProviderPreset("nvidia")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := config.SaveProviderConnection(preset, false); err != nil {
		t.Fatalf("SaveProviderConnection: %v", err)
	}
	if err := config.SaveCredential(preset.ID, "sk-already-set"); err != nil {
		t.Fatalf("SaveCredential: %v", err)
	}
	if err := config.SetDefaultModel(preset.ID + "/" + preset.VerifyModel); err != nil {
		t.Fatalf("SetDefaultModel: %v", err)
	}

	before, err := readConfigTOML(t, dir)
	if err != nil {
		t.Fatal(err)
	}

	offerDefaultModel(preset)

	after, err := readConfigTOML(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Errorf("offerDefaultModel touched config.toml when the default already worked\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// TestOfferDefaultModelNoTTYPrintsPointerInsteadOfPrompting is the
// non-interactive half of the same fix: with no TTY on stdin (the state
// this test process runs under, and the state any script/CI invocation of
// `provider add` runs under), offerDefaultModel must not block waiting for
// an answer that will never arrive — it degrades to the same "edit it
// yourself" pointer text `provider add` always printed.
func TestOfferDefaultModelNoTTYPrintsPointerInsteadOfPrompting(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	preset, err := config.ResolveProviderPreset("openai")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := config.SaveProviderConnection(preset, false); err != nil {
		t.Fatalf("SaveProviderConnection: %v", err)
	}
	if err := config.SaveCredential(preset.ID, "sk-test"); err != nil {
		t.Fatalf("SaveCredential: %v", err)
	}
	// Deliberately leave app.default_model unset: NeedsDefaultModel must
	// see this provider as not yet the default.

	before, err := readConfigTOML(t, dir)
	if err != nil {
		t.Fatal(err)
	}

	// The test binary's own stdin is not a terminal, so offerDefaultModel
	// takes the no-TTY branch deterministically — this asserts the
	// consequence of that branch (config.toml is not touched) rather than
	// its stdout text, which the "go test" harness's stdin/stdout wiring
	// is not a reliable place to capture from.
	offerDefaultModel(preset)

	after, err := readConfigTOML(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Errorf("offerDefaultModel modified config.toml on the no-TTY path (it must only print, never prompt or write)\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func readConfigTOML(t *testing.T, xdgConfigHome string) (string, error) {
	t.Helper()
	b, err := os.ReadFile(xdgConfigHome + "/ishakat/config.toml")
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(b), nil
}

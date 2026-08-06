package config_test

import (
	"os"
	"strings"
	"testing"

	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/xdg"

	// config itself never imports provider (no cycle: config_test is a
	// separate compiled package). This import exists solely for
	// TestEveryPresetKindHasAnAdapter, the "three-second grep" the audit
	// asked for: it would have caught the Anthropic preset shipping
	// kind = "anthropic" with zero registered adapter before it ever
	// reached a user.
	_ "github.com/MichiTrader/ishakat/internal/provider/openai"

	"github.com/MichiTrader/ishakat/internal/provider"
)

// TestEveryPresetKindHasAnAdapter is the test an audit of this feature asked
// for by name: every ProviderPreset's Kind must resolve to something
// registered in the provider package, or `provider add <that preset>` can
// collect a real secret from a user and hand back a provider that fails on
// its first turn. This is what would have caught the Anthropic preset
// shipping kind = "anthropic" with no adapter ever registering that string.
func TestEveryPresetKindHasAnAdapter(t *testing.T) {
	for _, preset := range config.ProviderPresets() {
		if !provider.Registered(preset.Kind) {
			t.Errorf("preset %q (id %q) declares kind %q, which has no registered adapter (registered: %v)",
				preset.Name, preset.ID, preset.Kind, provider.Kinds())
		}
		if preset.VerifyModel == "" {
			t.Errorf("preset %q has no VerifyModel; `provider add` cannot authenticate it without one", preset.Name)
		}
	}
}

func TestSaveCredentialWritesOnlyAPIKey(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := config.SaveCredential("nvidia", "nv-secret"); err != nil {
		t.Fatalf("SaveCredential() error = %v", err)
	}

	info, err := os.Stat(xdg.CredentialsFile())
	if err != nil {
		t.Fatalf("credentials file missing: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("credentials mode = %o, want 600", info.Mode().Perm())
	}

	contents, err := os.ReadFile(xdg.CredentialsFile())
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	text := string(contents)
	if !strings.Contains(text, "nv-secret") {
		t.Fatal("credentials file did not contain the saved key")
	}
	// The whole point of the split: credentials.toml must never carry
	// connection metadata, because it is the last-loaded layer and would
	// silently clobber a base_url the user set deliberately in config.toml
	// (see connection.go's package comment) every time a key is rotated.
	for _, forbidden := range []string{"base_url", "kind", "discover", "\nname"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("credentials.toml contains %q, want only schema/provider.id/provider.api_key:\n%s", forbidden, text)
		}
	}
}

// TestSaveCredentialAloneDoesNotActivateProvider documents the two-step flow
// deliberately: a bare SaveCredential no longer writes base_url anywhere,
// because that now belongs solely to config.SaveProviderConnection (called
// by `provider add` right after a successful verification — see
// cmd/ishakat/provider.go). For a preset with no prior config.toml entry —
// unlike "omniroute", which ships in defaults.toml — that means Load() fails
// outright with the pre-existing "falta base_url" error, rather than
// silently activating a provider nobody told where to send requests. A
// credential can never again be sufficient, by itself, to reach a service.
func TestSaveCredentialAloneDoesNotActivateProvider(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := config.SaveCredential("nvidia", "nv-secret"); err != nil {
		t.Fatalf("SaveCredential() error = %v", err)
	}

	_, err := config.Load(config.Options{SkipProject: true})
	if err == nil {
		t.Fatal("Load() with a credential but no connection metadata: want error, got nil (provider silently activated with no base_url)")
	}
	if !strings.Contains(err.Error(), "base_url") {
		t.Fatalf("Load() error = %v, want it to name the missing base_url", err)
	}
}

// TestProviderAddFlowActivatesProvider exercises the full sequence
// `provider add` performs: SaveProviderConnection (config.toml) followed by
// SaveCredential (credentials.toml). Only together do they produce an
// enabled, authenticated provider — this is the replacement for the old
// single-call contract.
func TestProviderAddFlowActivatesProvider(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	preset, err := config.ResolveProviderPreset("nvidia")
	if err != nil {
		t.Fatalf("ResolveProviderPreset() error = %v", err)
	}
	if _, err := config.SaveProviderConnection(preset, false); err != nil {
		t.Fatalf("SaveProviderConnection() error = %v", err)
	}
	if err := config.SaveCredential(preset.ID, "nv-secret"); err != nil {
		t.Fatalf("SaveCredential() error = %v", err)
	}

	cfg, err := config.Load(config.Options{SkipProject: true})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	var found bool
	for _, p := range cfg.Providers {
		if p.ID == "nvidia" {
			found = true
			if !p.Enabled || !p.AuthOK || p.APIKey != "nv-secret" {
				t.Fatalf("configured provider = %+v, want enabled and authenticated", p)
			}
			if p.BaseURL != preset.BaseURL {
				t.Fatalf("base_url = %q, want %q", p.BaseURL, preset.BaseURL)
			}
		}
	}
	if !found {
		t.Fatal("configured provider was not loaded")
	}
}

// TestSaveProviderConnectionPreservesCustomBaseURL is §2.1/§3's regression
// test: once a user has pointed a provider id at something other than the
// preset default (a proxy, a pinned API version, a self-hosted gateway),
// re-running `provider add` for that same id must not silently revert it.
func TestSaveProviderConnectionPreservesCustomBaseURL(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	preset, err := config.ResolveProviderPreset("openai")
	if err != nil {
		t.Fatalf("ResolveProviderPreset() error = %v", err)
	}
	customized := preset
	customized.BaseURL = "https://my-proxy.internal/v1"
	if _, err := config.SaveProviderConnection(customized, false); err != nil {
		t.Fatalf("SaveProviderConnection() error = %v", err)
	}

	// Rotating the key later must not touch base_url at all: SaveCredential
	// no longer knows what a base_url is.
	if err := config.SaveCredential(preset.ID, "sk-rotated"); err != nil {
		t.Fatalf("SaveCredential() error = %v", err)
	}

	overwrote, err := config.SaveProviderConnection(preset, false)
	if err != nil {
		t.Fatalf("SaveProviderConnection() error = %v", err)
	}
	if overwrote {
		t.Fatal("SaveProviderConnection() overwrote a custom base_url without --force")
	}

	cfg, err := config.Load(config.Options{SkipProject: true})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	for _, p := range cfg.Providers {
		if p.ID == preset.ID && p.BaseURL != customized.BaseURL {
			t.Fatalf("base_url = %q, want preserved custom value %q", p.BaseURL, customized.BaseURL)
		}
	}
}

func TestRemoveCredentialDeletesPrivateFileAndDisables(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	preset, err := config.ResolveProviderPreset("gemini")
	if err != nil {
		t.Fatalf("ResolveProviderPreset() error = %v", err)
	}
	if _, err := config.SaveProviderConnection(preset, false); err != nil {
		t.Fatalf("SaveProviderConnection() error = %v", err)
	}
	if err := config.SaveCredential("gemini-direct", "gem-secret"); err != nil {
		t.Fatalf("SaveCredential() error = %v", err)
	}
	if err := config.RemoveCredential("gemini-direct"); err != nil {
		t.Fatalf("RemoveCredential() error = %v", err)
	}
	if _, err := os.Stat(xdg.CredentialsFile()); !os.IsNotExist(err) {
		t.Fatalf("credentials file still exists, stat error = %v", err)
	}

	cfg, err := config.Load(config.Options{SkipProject: true})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	for _, p := range cfg.Providers {
		if p.ID == "gemini-direct" && p.Enabled {
			t.Fatalf("provider gemini-direct still enabled in config.toml after RemoveCredential")
		}
	}
}

// TestOmniRouteDisabledByDefaultOnFreshInstall documents the P0 change: a
// fresh install (embedded defaults.toml only, no config.toml at all) must
// ship with zero active providers, omniroute included — see
// internal/config/defaults.toml's own comment on why `enabled = false` is
// the built-in default now. Before this change omniroute shipped
// `enabled = true` with a $OMNIROUTE_API_KEY it almost never had, which is
// what produced the two identical "missing API key" warnings a user with
// no interest in OmniRoute saw on every single launch.
func TestOmniRouteDisabledByDefaultOnFreshInstall(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if _, err := os.Stat(xdg.ConfigFile()); !os.IsNotExist(err) {
		t.Fatalf("config.toml unexpectedly exists before the test runs")
	}

	cfg, err := config.Load(config.Options{SkipProject: true})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	found := false
	for _, p := range cfg.Providers {
		if p.ID == "omniroute" {
			found = true
			if p.Enabled {
				t.Fatalf("omniroute enabled = true on a fresh install; " +
					"the embedded default must ship disabled")
			}
		}
	}
	if !found {
		t.Fatal("omniroute not present; embedded defaults.toml changed?")
	}
	if len(cfg.Warnings) > 0 {
		t.Errorf("a fresh install with no providers enabled must produce zero warnings, got %+v", cfg.Warnings)
	}
}

// TestRemoveCredentialAppendsDisableOverrideWithNoExistingEntry is the
// regression test for the original bug report: a provider that has no
// config.toml entry of its own — reachable only through some other layer,
// which is exactly what the embedded defaults.toml is for omniroute — must
// still end up with an explicit disable override written to config.toml
// after RemoveCredential. disableProviderConnection must append an
// {id, enabled = false} entry when there is no existing one to flip, not
// silently do nothing — mergeProviders (merge.go) merges layers by id, so
// this override then wins over whatever an earlier layer said about the
// same id, on every subsequent config.Load.
//
// omniroute now ships `enabled = false` by default (P0: no active providers
// out of the box), so this test activates it first through a real
// config.toml entry with `enabled = true` — mirroring what `provider add`
// would do — specifically so RemoveCredential's append path has something
// non-trivial to flip, rather than asserting on a state that was already
// disabled before the call.
func TestRemoveCredentialAppendsDisableOverrideWithNoExistingEntry(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	preset, err := config.ResolveProviderPreset("omniroute")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := config.SaveProviderConnection(preset, false); err != nil {
		t.Fatalf("SaveProviderConnection() error = %v", err)
	}

	cfg, err := config.Load(config.Options{SkipProject: true})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	found := false
	for _, p := range cfg.Providers {
		if p.ID == "omniroute" {
			found = true
			if !p.Enabled {
				t.Fatalf("omniroute enabled = false right after SaveProviderConnection; want true")
			}
		}
	}
	if !found {
		t.Fatal("omniroute not present after SaveProviderConnection")
	}

	if err := config.RemoveCredential("omniroute"); err != nil {
		t.Fatalf("RemoveCredential() error = %v", err)
	}

	cfg, err = config.Load(config.Options{SkipProject: true})
	if err != nil {
		t.Fatalf("Load() error after RemoveCredential = %v", err)
	}
	for _, p := range cfg.Providers {
		if p.ID == "omniroute" && p.Enabled {
			t.Fatalf("omniroute still enabled after RemoveCredential")
		}
	}
}

func TestResolveProviderPresetAliases(t *testing.T) {
	for _, name := range []string{"gemini", "google"} {
		p, err := config.ResolveProviderPreset(name)
		if err != nil {
			t.Fatalf("ResolveProviderPreset(%q) error = %v", name, err)
		}
		if p.ID != "gemini-direct" {
			t.Errorf("ResolveProviderPreset(%q).ID = %q, want gemini-direct", name, p.ID)
		}
	}
}

// TestVerifyModelFor is P2's lookup: internal/app.ResolveModelForBoot uses
// this to find a wire id known to work for a fallback provider without
// touching the network (§4.4). "gemini-direct" is the preset's own ID
// field, not the "gemini"/"google" friendly names ResolveProviderPreset
// also accepts — VerifyModelFor is keyed by presetByID, i.e. the same ID a
// config.Provider.ID would actually carry.
func TestVerifyModelFor(t *testing.T) {
	wire, ok := config.VerifyModelFor("gemini-direct")
	if !ok {
		t.Fatal("VerifyModelFor(\"gemini-direct\") ok = false, want true")
	}
	if wire != "gemini-2.0-flash" {
		t.Errorf("wire = %q, want %q", wire, "gemini-2.0-flash")
	}
}

func TestVerifyModelForUnknownID(t *testing.T) {
	if _, ok := config.VerifyModelFor("some-hand-rolled-provider"); ok {
		t.Error("VerifyModelFor of an id with no matching preset: want ok = false")
	}
}

func TestSaveCredentialRejectsUnknownProvider(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := config.SaveCredential("does-not-exist", "whatever"); err == nil {
		t.Fatal("SaveCredential() with unknown provider id: want error, got nil")
	}
}

// TestSaveCredentialRefusesWhenHomeUnresolved is §2.2's regression test: a
// broken or stripped-down environment where $HOME can't be determined must
// never fall back to writing a secret into the current working directory,
// which could be an unrelated git checkout with no .gitignore protection.
func TestSaveCredentialRefusesWhenHomeUnresolved(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "") // Windows equivalent
	t.Setenv("XDG_CONFIG_HOME", "")

	err := config.SaveCredential("openai", "sk-should-not-be-written")
	if err == nil {
		t.Fatal("SaveCredential() with no resolvable home: want error, got nil")
	}
}

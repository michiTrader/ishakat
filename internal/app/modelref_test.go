package app

import (
	"strings"
	"testing"

	"github.com/MichiTrader/ishakat/internal/config"
)

func cfgWithProviders() *config.Config {
	return &config.Config{
		Schema: config.Schema,
		App: config.App{
			DefaultModel: "omniroute/auto/coding",
			Stream:       true,
		},
		Alias: map[string]string{
			"smart": "omniroute/auto/coding",
			"son45": "omniroute/anthropic/claude-sonnet-4-5",
			"loop":  "loop",
		},
		Providers: []config.Provider{
			{ID: "omniroute", Kind: "openai", BaseURL: "http://x/v1", Enabled: true, AuthOK: true},
			{ID: "openai", Kind: "openai", BaseURL: "https://api.openai.com/v1", Enabled: false, AuthOK: true},
		},
	}
}

func TestResolveModel(t *testing.T) {
	cfg := cfgWithProviders()

	cases := []struct {
		name     string
		input    string
		ref      string
		provider string
		wire     string
		via      string
	}{
		{"full reference", "omniroute/auto/coding",
			"omniroute/auto/coding", "omniroute", "auto/coding", ""},
		// The slash inside wire_id is the case that breaks strings.Split (§4.2).
		{"wire_id containing slashes", "omniroute/anthropic/claude-sonnet-4-5",
			"omniroute/anthropic/claude-sonnet-4-5", "omniroute", "anthropic/claude-sonnet-4-5", ""},
		{"empty falls back to default_model", "",
			"omniroute/auto/coding", "omniroute", "auto/coding", "default"},
		{"alias", "smart",
			"omniroute/auto/coding", "omniroute", "auto/coding", "alias"},
		{"alias with slashes", "son45",
			"omniroute/anthropic/claude-sonnet-4-5", "omniroute", "anthropic/claude-sonnet-4-5", "alias"},
		{"no prefix falls back to first enabled provider", "gpt-4o-mini",
			"omniroute/gpt-4o-mini", "omniroute", "gpt-4o-mini", "implicit-provider"},
		{"uppercase provider", "OMNIROUTE/auto/fast",
			"omniroute/auto/fast", "omniroute", "auto/fast", ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ResolveModel(cfg, c.input)
			if err != nil {
				t.Fatalf("ResolveModel(%q) returned an error: %v", c.input, err)
			}
			if got.Ref != c.ref {
				t.Errorf("Ref = %q, expected %q", got.Ref, c.ref)
			}
			if got.Provider != c.provider {
				t.Errorf("Provider = %q, expected %q", got.Provider, c.provider)
			}
			if got.WireID != c.wire {
				t.Errorf("WireID = %q, expected %q", got.WireID, c.wire)
			}
			if got.Via != c.via {
				t.Errorf("Via = %q, expected %q", got.Via, c.via)
			}
		})
	}
}

// An alias that points to itself is a configuration error that must not turn
// into an infinite loop inside the resolver.
func TestResolveModelCyclicAlias(t *testing.T) {
	cfg := cfgWithProviders()
	ref, err := ResolveModel(cfg, "loop")
	if err != nil {
		t.Fatalf("a cyclic alias must not fail, it must degrade: %v", err)
	}
	if ref.WireID != "loop" {
		t.Fatalf("WireID = %q, expected the cyclic alias to be treated as a literal wire_id", ref.WireID)
	}
}

func TestResolveModelDisabledProvider(t *testing.T) {
	cfg := cfgWithProviders()
	_, err := ResolveModel(cfg, "openai/gpt-5")
	if err == nil {
		t.Fatal("a provider with enabled = false must give an explicit error")
	}
	if !strings.Contains(err.Error(), "enabled = true") {
		t.Errorf("the error must say how to fix it, it says: %v", err)
	}
}

func TestResolveModelNoModel(t *testing.T) {
	cfg := cfgWithProviders()
	cfg.App.DefaultModel = ""
	_, err := ResolveModel(cfg, "")
	if err == nil {
		t.Fatal("without default_model and without -m it must fail")
	}
	if !strings.Contains(err.Error(), "default_model") {
		t.Errorf("the error must name app.default_model, it says: %v", err)
	}
}

func TestSettingsPerProviderTimeout(t *testing.T) {
	cfg := cfgWithProviders()
	cfg.App.TimeoutS = 120
	cfg.App.ConnectTimeoutS = 10

	// The provider's own timeout wins over app's: OmniRoute combos take
	// longer than a normal call (§5.2).
	p := cfg.Providers[0]
	p.TimeoutS = 180
	s := Settings(cfg, p, "1.2.3")

	if s.Timeout.Seconds() != 180 {
		t.Errorf("Timeout = %v, expected 180s", s.Timeout)
	}
	if s.ConnectTimeout.Seconds() != 10 {
		t.Errorf("ConnectTimeout = %v, expected 10s", s.ConnectTimeout)
	}
	if s.UserAgent != "ishakat/1.2.3" {
		t.Errorf("UserAgent = %q", s.UserAgent)
	}

	// Without its own timeout, it inherits app's.
	p.TimeoutS = 0
	if s := Settings(cfg, p, "dev"); s.Timeout.Seconds() != 120 {
		t.Errorf("inherited Timeout = %v, expected 120s", s.Timeout)
	}
}

func TestNewProviderUnknownKind(t *testing.T) {
	cfg := cfgWithProviders()
	p := cfg.Providers[0]
	p.Kind = "anthropic" // valid in the schema, no adapter yet

	_, err := NewProvider(cfg, p, "dev")
	if err == nil {
		t.Fatal("a kind with no registered adapter must fail to construct")
	}
	if !strings.Contains(err.Error(), "openai") {
		t.Errorf("the error must list the available dialects, it says: %v", err)
	}
}

func TestNewProviderMissingKey(t *testing.T) {
	cfg := cfgWithProviders()
	p := cfg.Providers[0]
	p.AuthOK = false
	p.MissingEnv = "OMNIROUTE_API_KEY"

	_, err := NewProvider(cfg, p, "dev")
	if err == nil {
		t.Fatal("a provider without a resolved key must fail before sending the turn")
	}
	if !strings.Contains(err.Error(), "OMNIROUTE_API_KEY") {
		t.Errorf("the error must name the missing variable, it says: %v", err)
	}
}

// --- P2: ResolveModelForBoot ------------------------------------------

// TestResolveModelForBootFallsBackWhenDefaultHasNoCredential is P2's core
// case: app.default_model names a declared, enabled provider that has no
// working credential (omniroute, AuthOK: false below) while a second
// provider (gemini-direct) IS usable. Boot must not fail the way
// ResolveModel alone would — it must land on the usable provider instead,
// using the exact VerifyModel wire id config.VerifyModelFor knows for that
// preset, and report what it did via the returned *BootFallback.
func TestResolveModelForBootFallsBackWhenDefaultHasNoCredential(t *testing.T) {
	cfg := &config.Config{
		Schema: config.Schema,
		App:    config.App{DefaultModel: "omniroute/auto/coding"},
		Providers: []config.Provider{
			{ID: "omniroute", Kind: "openai", Enabled: true, AuthOK: false, MissingEnv: "OMNIROUTE_API_KEY"},
			{ID: "gemini-direct", Kind: "openai", Enabled: true, AuthOK: true},
		},
	}

	ref, fb, err := ResolveModelForBoot(cfg, "")
	if err != nil {
		t.Fatalf("ResolveModelForBoot() error = %v, want a successful fallback", err)
	}
	if ref.Provider != "gemini-direct" {
		t.Errorf("ref.Provider = %q, want %q", ref.Provider, "gemini-direct")
	}
	if ref.WireID != "gemini-2.0-flash" {
		t.Errorf("ref.WireID = %q, want the gemini preset's VerifyModel", ref.WireID)
	}
	if ref.Via != "fallback" {
		t.Errorf("ref.Via = %q, want %q", ref.Via, "fallback")
	}
	if fb == nil {
		t.Fatal("want a non-nil *BootFallback describing what happened")
	}
	if fb.From != "omniroute/auto/coding" {
		t.Errorf("fb.From = %q, want %q", fb.From, "omniroute/auto/coding")
	}
	if fb.To != "gemini-direct/gemini-2.0-flash" {
		t.Errorf("fb.To = %q, want %q", fb.To, "gemini-direct/gemini-2.0-flash")
	}
	if !strings.Contains(fb.Reason, "credential") {
		t.Errorf("fb.Reason = %q, want it to mention the missing credential", fb.Reason)
	}
}

// TestResolveModelForBootFallsBackWhenDefaultProviderDisabled covers the
// other reason a default can be unusable: the provider it names is
// declared but enabled = false, distinct from "has no working credential".
func TestResolveModelForBootFallsBackWhenDefaultProviderDisabled(t *testing.T) {
	cfg := &config.Config{
		Schema: config.Schema,
		App:    config.App{DefaultModel: "omniroute/auto/coding"},
		Providers: []config.Provider{
			{ID: "omniroute", Kind: "openai", Enabled: false, AuthOK: true},
			{ID: "openai", Kind: "openai", Enabled: true, AuthOK: true},
		},
	}

	ref, fb, err := ResolveModelForBoot(cfg, "")
	if err != nil {
		t.Fatalf("ResolveModelForBoot() error = %v", err)
	}
	if ref.Provider != "openai" {
		t.Errorf("ref.Provider = %q, want %q", ref.Provider, "openai")
	}
	if fb == nil || fb.Reason != "is disabled" {
		t.Errorf("fb = %+v, want Reason = %q", fb, "is disabled")
	}
}

// TestResolveModelForBootNoFallbackWhenDefaultAlreadyWorks is the negative
// case: when app.default_model already resolves to a usable provider,
// ResolveModelForBoot must behave exactly like ResolveModel and return a
// nil *BootFallback — nothing happened worth reporting.
func TestResolveModelForBootNoFallbackWhenDefaultAlreadyWorks(t *testing.T) {
	cfg := cfgWithProviders()
	ref, fb, err := ResolveModelForBoot(cfg, "")
	if err != nil {
		t.Fatalf("ResolveModelForBoot() error = %v", err)
	}
	if fb != nil {
		t.Errorf("fb = %+v, want nil: nothing to fall back from", fb)
	}
	if ref.Ref != "omniroute/auto/coding" {
		t.Errorf("ref.Ref = %q, want %q", ref.Ref, "omniroute/auto/coding")
	}
}

// TestResolveModelForBootNeverOverridesAnExplicitModelFlag is the guard
// this whole feature exists around: an explicit -m/--model (modelText !=
// "") must go through ResolveModel's ordinary, non-fallback path even when
// it names an unusable provider — silently landing a typo somewhere else
// would be worse than failing loudly. ResolveModel itself only rejects a
// disabled provider (an unauthenticated-but-enabled one fails later, in
// NewProvider — see TestNewProviderMissingKey above), so "disabled" is
// what this test uses to exercise the "explicit -m still fails" path.
func TestResolveModelForBootNeverOverridesAnExplicitModelFlag(t *testing.T) {
	cfg := &config.Config{
		Schema: config.Schema,
		App:    config.App{DefaultModel: "omniroute/auto/coding"},
		Providers: []config.Provider{
			{ID: "omniroute", Kind: "openai", Enabled: false, AuthOK: true},
			{ID: "gemini-direct", Kind: "openai", Enabled: true, AuthOK: true},
		},
	}

	_, fb, err := ResolveModelForBoot(cfg, "omniroute/auto/coding")
	if err == nil {
		t.Fatal("an explicit -m naming a disabled provider must fail, not silently fall back")
	}
	if fb != nil {
		t.Errorf("fb = %+v, want nil: an explicit -m is never second-guessed", fb)
	}
}

// TestResolveModelForBootFailsWithNoUsableProviderAtAll: when neither the
// default nor any other configured provider is usable, ResolveModelForBoot
// must fail exactly like ResolveModel would, with an error naming the
// original default and the reason it was unusable.
func TestResolveModelForBootFailsWithNoUsableProviderAtAll(t *testing.T) {
	cfg := &config.Config{
		Schema: config.Schema,
		App:    config.App{DefaultModel: "omniroute/auto/coding"},
		Providers: []config.Provider{
			{ID: "omniroute", Kind: "openai", Enabled: true, AuthOK: false, MissingEnv: "OMNIROUTE_API_KEY"},
		},
	}

	_, fb, err := ResolveModelForBoot(cfg, "")
	if err == nil {
		t.Fatal("want an error: no provider anywhere is usable")
	}
	if fb != nil {
		t.Errorf("fb = %+v, want nil on total failure", fb)
	}
	if !strings.Contains(err.Error(), "omniroute/auto/coding") {
		t.Errorf("error must name the original default, it says: %v", err)
	}
}

// TestResolveModelForBootSkipsProvidersWithNoVerifyModelPreset: a usable
// provider whose id doesn't match any known preset (added entirely by
// hand, config.VerifyModelFor has nothing for it) must be skipped in favor
// of one that does, rather than guessing an unverified wire id.
func TestResolveModelForBootSkipsProvidersWithNoVerifyModelPreset(t *testing.T) {
	cfg := &config.Config{
		Schema: config.Schema,
		App:    config.App{DefaultModel: "omniroute/auto/coding"},
		Providers: []config.Provider{
			{ID: "omniroute", Kind: "openai", Enabled: true, AuthOK: false, MissingEnv: "OMNIROUTE_API_KEY"},
			{ID: "myservice", Kind: "openai", Enabled: true, AuthOK: true}, // not a preset id
			{ID: "openai", Kind: "openai", Enabled: true, AuthOK: true},
		},
	}

	ref, fb, err := ResolveModelForBoot(cfg, "")
	if err != nil {
		t.Fatalf("ResolveModelForBoot() error = %v", err)
	}
	if ref.Provider != "openai" {
		t.Errorf("ref.Provider = %q, want %q (myservice has no VerifyModel preset)", ref.Provider, "openai")
	}
	if fb == nil {
		t.Fatal("want a non-nil *BootFallback")
	}
}

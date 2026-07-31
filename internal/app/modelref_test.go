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

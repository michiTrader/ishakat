package app

import (
	"strings"
	"testing"

	"github.com/MichiTrader/ishakat/internal/catalog"
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

// TestResolveFallbackModelEmptyIsANoOp covers defaults.toml's documented
// meaning of fallback_model = "": ResolveFallbackModel must return "" with
// no error, and — the actual regression this pins — never fall through to
// ResolveModel's own empty-string rule (which would silently resolve "" to
// app.default_model, exactly the model checkFallback would then be asked
// to fall back to from itself).
func TestResolveFallbackModelEmptyIsANoOp(t *testing.T) {
	cfg := cfgWithProviders()
	cfg.App.FallbackModel = ""

	got, err := ResolveFallbackModel(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Fatalf("ResolveFallbackModel(\"\") = %q, want empty (never app.default_model)", got)
	}
}

// TestResolveFallbackModelResolvesAConfiguredRef is the ordinary case: a
// real fallback_model resolves through the same ResolveModel path as any
// other reference (§4.2), to the canonical Ref form checkFallback's own
// string comparison against m.model needs.
func TestResolveFallbackModelResolvesAConfiguredRef(t *testing.T) {
	cfg := cfgWithProviders()
	cfg.App.FallbackModel = "smart" // alias for omniroute/auto/coding

	got, err := ResolveFallbackModel(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "omniroute/auto/coding" {
		t.Fatalf("ResolveFallbackModel(%q) = %q, want the resolved alias", cfg.App.FallbackModel, got)
	}
}

// TestResolveFallbackModelReportsAnUnresolvableRef covers the warning path
// app.go's own Run takes when fallback_model names a disabled or unknown
// provider: the interactive session must still start (a warning, not a
// fatal error, same rule compact_model's own resolution failure follows),
// but the caller needs a non-nil error to know to print one.
func TestResolveFallbackModelReportsAnUnresolvableRef(t *testing.T) {
	cfg := cfgWithProviders()
	cfg.App.FallbackModel = "openai/gpt-5" // openai is Enabled: false above

	got, err := ResolveFallbackModel(cfg)
	if err == nil {
		t.Fatal("a fallback_model naming a disabled provider must report an error")
	}
	if got != "" {
		t.Fatalf("got = %q, want empty on error", got)
	}
	if !strings.Contains(err.Error(), "fallback_model") {
		t.Errorf("the error must name fallback_model, it says: %v", err)
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
	// "anthropic" used to be this test's example of "valid in the schema,
	// no adapter yet" — that was exactly the bug Fase 4 fixed by giving it
	// a real adapter (internal/provider/anthropic). "gemini" is next in
	// line for the same treatment (see validate.go's validKind doc
	// comment): valid per validKind, but nothing calls
	// provider.Register("gemini", ...) yet.
	p.Kind = "gemini" // valid in the schema, no adapter yet

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
	// P3: the message must also offer the escape hatch for a provider the
	// user never wanted in the first place, not just "export the variable"
	// as if activating it were the only option.
	if !strings.Contains(err.Error(), "provider remove") {
		t.Errorf("the error must suggest `ishakat provider remove`, it says: %v", err)
	}
}

// TestResolveModelNoProviderEnabledSuggestsProviderAdd is P3: on a fresh
// install (P0/P1) with zero active providers, "no provider is enabled" is
// an expected, honest state, not a broken config — the error must point at
// the actual fix (`provider add`) rather than only telling the user to go
// hand-edit a config.toml that may not even exist yet.
func TestResolveModelNoProviderEnabledSuggestsProviderAdd(t *testing.T) {
	cfg := &config.Config{Schema: config.Schema}

	_, err := ResolveModel(cfg, "gpt-4o-mini")
	if err == nil {
		t.Fatal("no provider is enabled: want an error")
	}
	if !strings.Contains(err.Error(), "provider add") {
		t.Errorf("the error must suggest `ishakat provider add`, it says: %v", err)
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

	ref, fb, err := ResolveModelForBoot(cfg, nil, "")
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

	ref, fb, err := ResolveModelForBoot(cfg, nil, "")
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
	ref, fb, err := ResolveModelForBoot(cfg, nil, "")
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

	_, fb, err := ResolveModelForBoot(cfg, nil, "omniroute/auto/coding")
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

	_, fb, err := ResolveModelForBoot(cfg, nil, "")
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

	ref, fb, err := ResolveModelForBoot(cfg, nil, "")
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

// --- boot with no app.default_model at all -------------------------------
//
// The tests below cover the bug report's third symptom: a configuration
// with exactly one enabled, credentialed provider and no [app] section
// warned "no model to use: pass -m/--model or set app.default_model" on
// every single launch and started with eng = nil, even though there was
// obviously something usable to run. See ResolveModelForBoot's own comment
// on the errNoModelConfigured branch.

// userReportedCfg is the configuration from the bug report, verbatim in
// structure: schema + one [[provider]] entry, and no [app] table, so
// App.DefaultModel is the zero value.
func userReportedCfg() *config.Config {
	return &config.Config{
		Schema: config.Schema,
		Providers: []config.Provider{{
			ID: "gemini-direct", Name: "Google Gemini", Kind: "openai",
			BaseURL:  "https://generativelanguage.googleapis.com/v1beta/openai",
			Discover: true, Enabled: true, AuthOK: true,
		}},
	}
}

func TestResolveModelForBootNoDefaultModelPicksAUsableProvider(t *testing.T) {
	cfg := userReportedCfg()

	ref, fb, err := ResolveModelForBoot(cfg, nil, "")
	if err != nil {
		t.Fatalf("ResolveModelForBoot() error = %v; a config with one enabled, "+
			"credentialed provider must not fail to boot just because "+
			"app.default_model is unset", err)
	}
	if ref.Provider != "gemini-direct" {
		t.Errorf("ref.Provider = %q, want %q", ref.Provider, "gemini-direct")
	}
	if ref.WireID == "" {
		t.Error("ref.WireID is empty: the fallback must name a concrete model")
	}
	if fb == nil {
		t.Fatal("want a non-nil *BootFallback: silently choosing a model the user " +
			"never configured is exactly the kind of invisible decision this " +
			"type exists to report")
	}
	if !fb.Unset() {
		t.Errorf("fb.Unset() = false (fb.From = %q), want true: nothing was configured "+
			"to fall back away from", fb.From)
	}
}

// TestBootFallbackDescribeUnsetNamesNoEmptyRef is the reason Describe()
// exists at all. The previous phrasing was a bare
// "app.default_model (%s) %s; using %s instead", which for the unset case
// would have rendered "app.default_model () is not set; using X instead" —
// an empty pair of parentheses in the very first line of output.
func TestBootFallbackDescribeUnsetNamesNoEmptyRef(t *testing.T) {
	fb := &BootFallback{To: "gemini-direct/gemini-3-flash", Reason: "is not set"}

	got := fb.Describe()
	if strings.Contains(got, "()") {
		t.Errorf("Describe() = %q, must not contain an empty '()' pair", got)
	}
	if !strings.Contains(got, "gemini-direct/gemini-3-flash") {
		t.Errorf("Describe() = %q, want it to name the model actually used", got)
	}
	if !strings.Contains(got, "model set") {
		t.Errorf("Describe() = %q, want it to say how to make the choice stick", got)
	}
}

func TestBootFallbackDescribeNilIsEmpty(t *testing.T) {
	var fb *BootFallback
	if got := fb.Describe(); got != "" {
		t.Errorf("(*BootFallback)(nil).Describe() = %q, want \"\": the no-fallback "+
			"case must print nothing at all", got)
	}
}

// TestResolveModelForBootStillFailsWithNoProviderAtAll pins the boundary of
// the fix: routing around an unset default is only correct when there is
// something to route to. An empty configuration has to keep failing, and
// with the message that names the actual fix.
func TestResolveModelForBootStillFailsWithNoProviderAtAll(t *testing.T) {
	cfg := &config.Config{Schema: config.Schema}

	_, _, err := ResolveModelForBoot(cfg, nil, "")
	if err == nil {
		t.Fatal("with no providers declared at all there is nothing to fall back to; " +
			"ResolveModelForBoot must fail")
	}
	if !strings.Contains(err.Error(), "provider add") {
		t.Errorf("error = %v, want it to point at `ishakat provider add`", err)
	}
}

// TestResolveModelForBootNoDefaultSkipsUncredentialedProvider: an enabled
// provider whose credential never resolved cannot answer a turn, so picking
// it would trade a startup warning for a failing first turn.
func TestResolveModelForBootNoDefaultSkipsUncredentialedProvider(t *testing.T) {
	cfg := &config.Config{
		Schema: config.Schema,
		Providers: []config.Provider{
			{ID: "omniroute", Kind: "openai", Enabled: true, AuthOK: false, MissingEnv: "OMNIROUTE_API_KEY"},
			{ID: "openai", Kind: "openai", Enabled: true, AuthOK: true},
		},
	}

	ref, fb, err := ResolveModelForBoot(cfg, nil, "")
	if err != nil {
		t.Fatalf("ResolveModelForBoot() error = %v", err)
	}
	if ref.Provider != "openai" {
		t.Errorf("ref.Provider = %q, want %q (omniroute has no credential)", ref.Provider, "openai")
	}
	if fb == nil || !fb.Unset() {
		t.Fatalf("want an unset-default fallback, got %+v", fb)
	}
}

// TestResolveModelForBootStillFailsWhenNothingIsCredentialed: providers are
// declared and enabled, but none of them can authenticate. There is nothing
// honest to boot into, so the error must survive.
func TestResolveModelForBootStillFailsWhenNothingIsCredentialed(t *testing.T) {
	cfg := &config.Config{
		Schema: config.Schema,
		Providers: []config.Provider{
			{ID: "omniroute", Kind: "openai", Enabled: true, AuthOK: false, MissingEnv: "OMNIROUTE_API_KEY"},
			{ID: "openai", Kind: "openai", Enabled: true, AuthOK: false, MissingEnv: "OPENAI_API_KEY"},
		},
	}

	_, _, err := ResolveModelForBoot(cfg, nil, "")
	if err == nil {
		t.Fatal("no provider has a working credential; ResolveModelForBoot must fail " +
			"rather than boot into a model that cannot answer")
	}
}

// --- the catalog is preferred over the compiled-in preset id --------------

// catalogOf builds a snapshot with the given models, using the same
// provider/wire split catalog.Build produces.
func catalogOf(models ...catalog.Model) *catalog.Catalog {
	return &catalog.Catalog{Models: models}
}

func catModel(provider, wireID string, opts ...func(*catalog.Model)) catalog.Model {
	m := catalog.Model{
		Ref:      provider + "/" + wireID,
		Provider: provider,
		WireID:   wireID,
		Name:     wireID,
		Health:   catalog.HealthOK,
	}
	for _, o := range opts {
		o(&m)
	}
	return m
}

// TestResolveModelForBootPrefersTheCatalogOverThePreset is the reason
// pickBootModel takes a catalog at all. config.VerifyModelFor returns a
// model id compiled into *this build* ("gemini-2.0-flash"); the catalog
// holds what the provider was last seen actually serving on this machine.
// When the two disagree the live answer has to win, or a user whose account
// has moved on gets booted onto a model that may no longer exist.
func TestResolveModelForBootPrefersTheCatalogOverThePreset(t *testing.T) {
	cfg := userReportedCfg()
	cat := catalogOf(catModel("gemini-direct", "gemini-3.1-flash-lite"))

	ref, fb, err := ResolveModelForBoot(cfg, cat, "")
	if err != nil {
		t.Fatalf("ResolveModelForBoot() error = %v", err)
	}
	if ref.WireID != "gemini-3.1-flash-lite" {
		t.Errorf("ref.WireID = %q, want the catalog's model, not the preset's "+
			"compiled-in %q", ref.WireID, "gemini-2.0-flash")
	}
	if fb == nil || fb.To != "gemini-direct/gemini-3.1-flash-lite" {
		t.Errorf("fb = %+v, want it to report the catalog model it chose", fb)
	}
}

// TestResolveModelForBootFallsBackToPresetWithoutACatalog: a first run has
// no cache, and that is an ordinary state rather than an error. The preset
// id is the honest second choice — `provider add` proved it answers.
func TestResolveModelForBootFallsBackToPresetWithoutACatalog(t *testing.T) {
	cfg := userReportedCfg()

	ref, _, err := ResolveModelForBoot(cfg, catalogOf(), "")
	if err != nil {
		t.Fatalf("ResolveModelForBoot() error = %v", err)
	}
	if ref.WireID != "gemini-2.0-flash" {
		t.Errorf("ref.WireID = %q, want the gemini preset's VerifyModel with an "+
			"empty catalog", ref.WireID)
	}
}

// TestResolveModelForBootSkipsDeprecatedAndUnusableCatalogEntries: the
// provider itself said these are going away or cannot be authenticated, so
// starting a session on one is a slow-motion failure.
func TestResolveModelForBootSkipsDeprecatedAndUnusableCatalogEntries(t *testing.T) {
	cfg := userReportedCfg()
	cat := catalogOf(
		catModel("gemini-direct", "gemini-1.0-retired", func(m *catalog.Model) {
			m.Tags = []string{catalog.TagDeprecated}
		}),
		catModel("gemini-direct", "gemini-locked", func(m *catalog.Model) {
			m.Health = catalog.HealthUnauthenticated
		}),
		catModel("gemini-direct", "gemini-3.1-flash-lite"),
	)

	ref, _, err := ResolveModelForBoot(cfg, cat, "")
	if err != nil {
		t.Fatalf("ResolveModelForBoot() error = %v", err)
	}
	if ref.WireID != "gemini-3.1-flash-lite" {
		t.Errorf("ref.WireID = %q, want the first usable, non-deprecated entry", ref.WireID)
	}
}

// TestResolveModelForBootIgnoresOtherProvidersCatalogEntries: the catalog
// holds every provider's models at once, so the lookup must be scoped or a
// gemini-only configuration could boot with an OpenAI wire id.
func TestResolveModelForBootIgnoresOtherProvidersCatalogEntries(t *testing.T) {
	cfg := userReportedCfg()
	cat := catalogOf(
		catModel("openai", "gpt-4o"),
		catModel("gemini-direct", "gemini-3.1-flash-lite"),
	)

	ref, _, err := ResolveModelForBoot(cfg, cat, "")
	if err != nil {
		t.Fatalf("ResolveModelForBoot() error = %v", err)
	}
	if ref.Provider != "gemini-direct" || ref.WireID != "gemini-3.1-flash-lite" {
		t.Errorf("ref = %q, want a gemini-direct model: openai is not even declared "+
			"in this configuration", ref.Ref)
	}
}

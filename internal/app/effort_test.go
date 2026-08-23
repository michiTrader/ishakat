package app

import (
	"reflect"
	"testing"

	"github.com/MichiTrader/ishakat/internal/config"
)

// TestEffortParamsPerDialect pins the exact wire shape each dialect gets,
// per effort.go's own doc comment: a flat "reasoning_effort" for
// openai/responses (including the empty-kind default), a dotted
// "generationConfig.thinkingConfig.thinkingLevel" for gemini, and a dotted
// "output_config.effort" for anthropic — all carrying the level string
// verbatim, with no per-model numeric translation.
func TestEffortParamsPerDialect(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind string
		want map[string]any
	}{
		{"openai", "openai", map[string]any{"reasoning_effort": "high"}},
		{"responses alias", "responses", map[string]any{"reasoning_effort": "high"}},
		{"empty kind defaults to openai", "", map[string]any{"reasoning_effort": "high"}},
		{"case-insensitive kind", "OpenAI", map[string]any{"reasoning_effort": "high"}},
		{"gemini", "gemini", map[string]any{
			"generationConfig.thinkingConfig.thinkingLevel": "high",
		}},
		{"anthropic", "anthropic", map[string]any{"output_config.effort": "high"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := EffortParams(tc.kind, "high")
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("EffortParams(%q, \"high\") = %#v, want %#v", tc.kind, got, tc.want)
			}
		})
	}
}

// TestEffortParamsEmptyLevelReturnsNil pins "nothing to ask for" as nil,
// not an empty map: a caller that unconditionally sets
// engine.Request.Params from this return value must not accidentally send
// an empty params object on every turn just because no effort was chosen.
func TestEffortParamsEmptyLevelReturnsNil(t *testing.T) {
	for _, kind := range []string{"openai", "gemini", "anthropic", "", "unknown"} {
		if got := EffortParams(kind, ""); got != nil {
			t.Errorf("EffortParams(%q, \"\") = %#v, want nil", kind, got)
		}
		if got := EffortParams(kind, "   "); got != nil {
			t.Errorf("EffortParams(%q, \"   \") = %#v, want nil (whitespace-only trims to empty)", kind, got)
		}
	}
}

// TestEffortParamsUnknownDialectReturnsNil pins the "silence, not refusal"
// rule for a provider dialect this function has not been taught about: F9
// is additive, so an unrecognized kind must not fail the turn, only leave
// it without an effort override — exactly as if the user never asked.
func TestEffortParamsUnknownDialectReturnsNil(t *testing.T) {
	if got := EffortParams("some-future-dialect", "high"); got != nil {
		t.Errorf("EffortParams(unknown, \"high\") = %#v, want nil", got)
	}
}

// TestEffortParamsForReadsProviderKind checks the config.Provider
// convenience wrapper reads pc.Kind the same way EffortParams itself would,
// including the zero-value case (found == false at a call site) matching
// the empty-kind openai default.
func TestEffortParamsForReadsProviderKind(t *testing.T) {
	pc := config.Provider{Kind: "gemini"}
	got := EffortParamsFor(pc, "low")
	want := map[string]any{"generationConfig.thinkingConfig.thinkingLevel": "low"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("EffortParamsFor(gemini, \"low\") = %#v, want %#v", got, want)
	}

	// Zero-value config.Provider{} (Kind == "") must resolve the same as
	// an explicit kind = "openai", matching provider.New's own default.
	got = EffortParamsFor(config.Provider{}, "low")
	want = map[string]any{"reasoning_effort": "low"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("EffortParamsFor(zero-value, \"low\") = %#v, want %#v", got, want)
	}
}

// TestNewEffortResolverWalksTheSameResolveModelFindProviderPath pins
// NewEffortResolver's own doc-comment promise: it must resolve ref through
// the exact same ResolveModel/FindProvider chain NewEngineFactory uses for
// the same ref, so a turn's effort override and the engine it runs on can
// never disagree about which provider dialect they are both addressing.
func TestNewEffortResolverWalksTheSameResolveModelFindProviderPath(t *testing.T) {
	cfg := &config.Config{
		Schema: config.Schema,
		Providers: []config.Provider{
			{ID: "omniroute", Kind: "openai", Enabled: true, AuthOK: true},
			{ID: "gem", Kind: "gemini", Enabled: true, AuthOK: true},
		},
	}

	resolve := NewEffortResolver(cfg)

	got := resolve("omniroute/gpt-5", "high")
	want := map[string]any{"reasoning_effort": "high"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("resolve(omniroute/gpt-5, high) = %#v, want %#v", got, want)
	}

	got = resolve("gem/gemini-3-pro", "low")
	want = map[string]any{"generationConfig.thinkingConfig.thinkingLevel": "low"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("resolve(gem/gemini-3-pro, low) = %#v, want %#v", got, want)
	}
}

// TestNewEffortResolverReturnsNilOnAResolutionFailure pins the "silence, not
// refusal" rule for a ref this resolver cannot place a provider for (a
// disabled or undeclared provider, or a malformed ref): F9 is additive, so
// a turn must never fail just because its effort override could not be
// resolved.
func TestNewEffortResolverReturnsNilOnAResolutionFailure(t *testing.T) {
	cfg := &config.Config{
		Schema: config.Schema,
		Providers: []config.Provider{
			{ID: "omniroute", Kind: "openai", Enabled: false, AuthOK: true},
		},
	}
	resolve := NewEffortResolver(cfg)

	if got := resolve("nope/does-not-exist", "high"); got != nil {
		t.Errorf("resolve(undeclared provider) = %#v, want nil", got)
	}
	if got := resolve("omniroute/gpt-5", "high"); got != nil {
		t.Errorf("resolve(disabled provider) = %#v, want nil", got)
	}
}

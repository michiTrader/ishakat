package catalog

import "testing"

// curate_test.go pins Layer 1's own closing criterion
// (docs/DESIGN-model-curation.md §3, "Closing criterion for layer 1"):
// a table test over Google-shaped ids that asserts exactly which are
// hidden and under which reason, plus the general rules (Keep overrides
// everything, UseCount/SourceConfig are never hidden, Curate is pure).

func chatCaps() Caps { return Caps{Tools: false, JSONSchema: false} }

func boolPtr(b bool) *bool { return &b }

// gm is a small helper for building a Model with just the fields curate.go
// cares about, since Curate does not touch anything else.
func gm(wireID string, opts ...func(*Model)) Model {
	m := Model{
		Ref:      "google/" + wireID,
		Provider: "google",
		WireID:   wireID,
		Caps:     chatCaps(),
	}
	for _, o := range opts {
		o(&m)
	}
	return m
}

func withModalities(mods ...string) func(*Model) {
	return func(m *Model) { m.Modalities = mods }
}
func withMaxOutput(n int) func(*Model) { return func(m *Model) { m.MaxOutput = n } }
func withTemperature(b bool) func(*Model) {
	v := b
	return func(m *Model) { m.Temperature = &v }
}
func withTools() func(*Model)         { return func(m *Model) { m.Caps.Tools = true } }
func withDeprecated() func(*Model)    { return func(m *Model) { m.addTag(TagDeprecated) } }
func withUseCount(n int) func(*Model) { return func(m *Model) { m.UseCount = n } }
func withConfigSource() func(*Model)  { return func(m *Model) { m.Source |= SourceConfig } }

// TestCurateNonChatModality covers §1.2 signal 1: output modalities that
// exist but do not include text (Veo/TTS shape). A model with NO declared
// modalities is unknown, not evidence, and must be kept (principle 10).
func TestCurateNonChatModality(t *testing.T) {
	cat := Catalog{Models: []Model{
		gm("veo-3.1-generate-preview", withModalities("video")),
		gm("gemini-3.5-flash", withModalities("text")),
		gm("gemini-unknown-modalities"), // no Modalities at all: keep
	}}
	kept, hidden := Curate(cat, Rules{ChatOnly: true})

	mustHiddenReason(t, hidden, "google/veo-3.1-generate-preview", ReasonNonChatModality)
	mustKept(t, kept, "google/gemini-3.5-flash")
	mustKept(t, kept, "google/gemini-unknown-modalities")
}

// TestCurateNonChatLimit covers §1.2 signal 2: MaxOutput == 1 cannot carry
// a turn. Zero must NOT be treated as evidence — it means "unknown" on
// some gateways, not "degenerate" (§1.2's own worked example).
func TestCurateNonChatLimit(t *testing.T) {
	cat := Catalog{Models: []Model{
		gm("gemini-embedding-2", withModalities("text"), withMaxOutput(1)),
		gm("deep-research-preview", withMaxOutput(0)), // 0 = unknown, keep
	}}
	kept, hidden := Curate(cat, Rules{ChatOnly: true})

	mustHiddenReason(t, hidden, "google/gemini-embedding-2", ReasonNonChatLimit)
	mustKept(t, kept, "google/deep-research-preview")
}

// TestCurateNonChatSampling covers §1.2 signal 3: Temperature explicitly
// false, no tools, no structured output. Temperature == nil (never
// mentioned) must be kept, never treated as false (principle 10).
func TestCurateNonChatSampling(t *testing.T) {
	cat := Catalog{Models: []Model{
		gm("some-reranker", withTemperature(false)),
		gm("some-reranker-with-tools", withTemperature(false), withTools()),
		gm("some-chat-model-no-temp-field"), // Temperature == nil: keep
	}}
	kept, hidden := Curate(cat, Rules{ChatOnly: true})

	mustHiddenReason(t, hidden, "google/some-reranker", ReasonNonChatSampling)
	mustKept(t, kept, "google/some-reranker-with-tools") // has Tools: not a pure scorer
	mustKept(t, kept, "google/some-chat-model-no-temp-field")
}

// TestCurateDeprecated covers the metadata rule, and that it inherits the
// same carve-out merge.go's own HideDeprecated block already had before
// this rule generalized it: UseCount>0 or a config-declared source is
// never hidden.
func TestCurateDeprecated(t *testing.T) {
	cat := Catalog{Models: []Model{
		gm("gemini-2.0-pro-exp", withDeprecated()),
		gm("gemini-1.0-pro", withDeprecated(), withUseCount(3)),
		gm("gemini-1.5-pro", withDeprecated(), withConfigSource()),
	}}
	kept, hidden := Curate(cat, Rules{HideDeprecated: true})

	mustHiddenReason(t, hidden, "google/gemini-2.0-pro-exp", ReasonDeprecated)
	mustKept(t, kept, "google/gemini-1.0-pro")
	mustKept(t, kept, "google/gemini-1.5-pro")
}

// TestCurateSupersededIsRelational is §1.3's core assertion: a "-preview"
// id is hidden only when the GA twin exists in the SAME provider. The
// design doc's own worked example: 22 of Google's ids contain "preview",
// only some are actually redundant.
func TestCurateSupersededIsRelational(t *testing.T) {
	cat := Catalog{Models: []Model{
		gm("gemini-3.1-flash-image"),         // the GA id
		gm("gemini-3.1-flash-image-preview"), // has a GA twin: hidden
		gm("gemini-3.1-pro-preview"),         // NO GA twin: kept
	}}
	kept, hidden := Curate(cat, Rules{HideSuperseded: true})

	mustHiddenReason(t, hidden, "google/gemini-3.1-flash-image-preview", ReasonSuperseded)
	mustKept(t, kept, "google/gemini-3.1-flash-image")
	mustKept(t, kept, "google/gemini-3.1-pro-preview")
}

// TestCurateDatedTwinIsRelational mirrors the superseded test for date
// stamps (Anthropic/OpenAI's own duplicate shape per §1.3).
func TestCurateDatedTwinIsRelational(t *testing.T) {
	cat := Catalog{Models: []Model{
		gm("claude-sonnet-4-5"),
		gm("claude-sonnet-4-5-20250929"),    // undated twin exists: hidden
		gm("deep-research-preview-04-2026"), // no undated twin: kept
	}}
	kept, hidden := Curate(cat, Rules{HideDatedTwins: true})

	mustHiddenReason(t, hidden, "google/claude-sonnet-4-5-20250929", ReasonDatedTwin)
	mustKept(t, kept, "google/claude-sonnet-4-5")
	mustKept(t, kept, "google/deep-research-preview-04-2026")
}

// TestCurateLatestAliasOffByDefault: HideLatest is a zero-value-false
// field, so Rules{} must never hide a "-latest" alias, and explicitly
// enabling it must.
func TestCurateLatestAliasOffByDefault(t *testing.T) {
	cat := Catalog{Models: []Model{gm("gemini-flash-latest")}}

	_, hiddenOff := Curate(cat, Rules{})
	if len(hiddenOff) != 0 {
		t.Fatalf("HideLatest defaults to false: want nothing hidden, got %v", hiddenOff)
	}

	_, hiddenOn := Curate(cat, Rules{HideLatest: true})
	mustHiddenReason(t, hiddenOn, "google/gemini-flash-latest", ReasonLatestAlias)
}

// TestCurateKeepOverridesEverything: an explicit Keep glob beats every
// other rule, including ChatOnly and HideDeprecated at once.
func TestCurateKeepOverridesEverything(t *testing.T) {
	cat := Catalog{Models: []Model{
		gm("gemini-embedding-2", withModalities("text"), withMaxOutput(1), withDeprecated()),
	}}
	kept, hidden := Curate(cat, Rules{
		ChatOnly: true, HideDeprecated: true,
		Keep: []string{"gemini-embedding-2"},
	})
	if len(hidden) != 0 {
		t.Fatalf("Keep should override every rule, got hidden = %v", hidden)
	}
	mustKept(t, kept, "google/gemini-embedding-2")
}

// TestCurateUserGlobHide covers Rules.Hide and per-provider merge (§1.3):
// both the global list and the provider's own list apply, additively.
func TestCurateUserGlobHide(t *testing.T) {
	cat := Catalog{Models: []Model{
		gm("gemini-2.5-flash-preview-tts"),
		gm("veo-3.1-generate-preview"),
		gm("gemini-3.5-flash"),
	}}
	kept, hidden := Curate(cat, Rules{
		Hide: []string{"*-tts*"},
		Providers: map[string]ProviderRules{
			"google": {Hide: []string{"veo-*"}},
		},
	})
	mustHiddenReason(t, hidden, "google/gemini-2.5-flash-preview-tts", ReasonUserGlob)
	mustHiddenReason(t, hidden, "google/veo-3.1-generate-preview", ReasonUserGlob)
	mustKept(t, kept, "google/gemini-3.5-flash")
}

// TestCurateNeverHidesUsedOrConfigDeclared is principle 3, applied to
// EVERY rule (not just deprecation): a model the user has run, or wrote
// into config.toml by hand, survives every automatic rule.
func TestCurateNeverHidesUsedOrConfigDeclared(t *testing.T) {
	cat := Catalog{Models: []Model{
		gm("gemini-embedding-2", withModalities("text"), withMaxOutput(1), withUseCount(1)),
		gm("veo-3.1-generate-preview", withModalities("video"), withConfigSource()),
	}}
	kept, _ := Curate(cat, Rules{ChatOnly: true})
	mustKept(t, kept, "google/gemini-embedding-2")
	mustKept(t, kept, "google/veo-3.1-generate-preview")
}

// TestCurateKeepRefsOverridesEverythingIncludingGlobHide is Layer 2's own
// precedence check (design doc §2.2): curation.json's KeepRefs beats even
// the glob-based Hide from config.toml, the strongest layer winning over a
// weaker one.
func TestCurateKeepRefsOverridesEverythingIncludingGlobHide(t *testing.T) {
	cat := Catalog{Models: []Model{
		gm("gemini-embedding-2", withModalities("text"), withMaxOutput(1), withDeprecated()),
	}}
	kept, hidden := Curate(cat, Rules{
		ChatOnly: true, HideDeprecated: true,
		Hide:     []string{"gemini-embedding-2"},
		KeepRefs: []string{"google/gemini-embedding-2"},
	})
	if len(hidden) != 0 {
		t.Fatalf("KeepRefs should override every rule, got hidden = %v", hidden)
	}
	mustKept(t, kept, "google/gemini-embedding-2")
}

// TestCurateHideRefsCatchesWhatNoAutomaticRuleWould asserts HideRefs can
// hide a model that ChatOnly/HideDeprecated/etc. would never flag on their
// own -- a plain, healthy chat model the user simply doesn't want to see.
func TestCurateHideRefsCatchesWhatNoAutomaticRuleWould(t *testing.T) {
	cat := Catalog{Models: []Model{
		gm("gemini-3.5-flash"),
	}}
	kept, hidden := Curate(cat, Rules{
		HideRefs: []string{"google/gemini-3.5-flash"},
	})
	mustHiddenReason(t, hidden, "google/gemini-3.5-flash", ReasonUserGlob)
	if len(kept.Models) != 0 {
		t.Fatalf("kept = %v, want empty", kept.Models)
	}
}

// TestCurateHideRefsIsNotBlockedByUsedOrDeclared: an explicit, one-model
// ctrl+x is a direct human instruction, not a heuristic guessing at
// intent, so it is NOT subject to principle 3's "never hide what the user
// actually used" carve-out the way HideDeprecated/ChatOnly/etc. are.
func TestCurateHideRefsIsNotBlockedByUsedOrDeclared(t *testing.T) {
	cat := Catalog{Models: []Model{
		gm("gemini-embedding-2", withUseCount(3)),
	}}
	_, hidden := Curate(cat, Rules{
		HideRefs: []string{"google/gemini-embedding-2"},
	})
	mustHiddenReason(t, hidden, "google/gemini-embedding-2", ReasonUserGlob)
}

// TestCuratePreservesFootCount asserts the invariant Layer 2's own
// closing criterion depends on: len(kept.Models) + len(hidden) ==
// len(input.Models), for every rule combination — the picker's footer
// ("N shown, M hidden") must always add back up.
func TestCuratePreservesFootCount(t *testing.T) {
	cat := Catalog{Models: []Model{
		gm("gemini-embedding-2", withModalities("text"), withMaxOutput(1)),
		gm("gemini-3.1-flash-image"),
		gm("gemini-3.1-flash-image-preview"),
		gm("gemini-flash-latest"),
		gm("gemini-3.5-flash"),
	}}
	kept, hidden := Curate(cat, Rules{
		ChatOnly: true, HideSuperseded: true, HideLatest: true,
	})
	if got, want := len(kept.Models)+len(hidden), len(cat.Models); got != want {
		t.Fatalf("kept(%d) + hidden(%d) = %d, want %d", len(kept.Models), len(hidden), got, want)
	}
}

// TestCurateIsPure: same input, same output, no clock, no map-order
// dependence — running it twice on the same catalog must produce
// identical kept/hidden sets in the same order.
func TestCurateIsPure(t *testing.T) {
	cat := Catalog{Models: []Model{
		gm("gemini-embedding-2", withModalities("text"), withMaxOutput(1)),
		gm("gemini-3.1-flash-image"),
		gm("gemini-3.1-flash-image-preview"),
		gm("gemini-3.5-flash"),
	}}
	rules := Rules{ChatOnly: true, HideSuperseded: true}

	kept1, hidden1 := Curate(cat, rules)
	kept2, hidden2 := Curate(cat, rules)

	if len(kept1.Models) != len(kept2.Models) || len(hidden1) != len(hidden2) {
		t.Fatalf("Curate is not pure: run1 kept=%d hidden=%d, run2 kept=%d hidden=%d",
			len(kept1.Models), len(hidden1), len(kept2.Models), len(hidden2))
	}
	for i := range kept1.Models {
		if kept1.Models[i].Ref != kept2.Models[i].Ref {
			t.Fatalf("kept order differs at %d: %q vs %q", i, kept1.Models[i].Ref, kept2.Models[i].Ref)
		}
	}
}

// TestCurateNeverMutatesInput guards principle 1/5: Curate must not
// mutate the Catalog it was handed — a caller holding onto cat (the
// picker rebuild path, applyCatalogRefreshed) must see it unchanged.
func TestCurateNeverMutatesInput(t *testing.T) {
	cat := Catalog{Models: []Model{
		gm("gemini-embedding-2", withModalities("text"), withMaxOutput(1)),
		gm("gemini-3.5-flash"),
	}}
	originalLen := len(cat.Models)
	_, _ = Curate(cat, Rules{ChatOnly: true})
	if len(cat.Models) != originalLen {
		t.Fatalf("Curate mutated its input: len = %d, want %d", len(cat.Models), originalLen)
	}
}

func mustHiddenReason(t *testing.T, hidden []Hidden, ref string, want Reason) {
	t.Helper()
	for _, h := range hidden {
		if h.Model.Ref == ref {
			if h.Reason != want {
				t.Fatalf("%s hidden with reason %q, want %q", ref, h.Reason, want)
			}
			return
		}
	}
	t.Fatalf("%s not found in hidden list %v", ref, hidden)
}

func mustKept(t *testing.T, kept Catalog, ref string) {
	t.Helper()
	if !kept.Has(ref) {
		t.Fatalf("%s should be kept, but refs = %v", ref, kept.Refs())
	}
}

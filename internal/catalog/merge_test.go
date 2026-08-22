package catalog

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// These tests pin down §4.3, the three-source fusion, one rule per test:
// existence is decided by discovery, metadata is filled in by models.dev
// only where discovery left a hole, and the user's configuration wins over
// both. Build is a pure function of its input —no clock, no filesystem, no
// network— so every case below is a plain table.

func find(t *testing.T, c Catalog, ref string) Model {
	t.Helper()
	m, ok := c.Get(ref)
	if !ok {
		t.Fatalf("model %q missing from the catalog; refs = %v", ref, c.Refs())
	}
	return m
}

func mustNotHave(t *testing.T, c Catalog, ref string) {
	t.Helper()
	if _, ok := c.Get(ref); ok {
		t.Fatalf("model %q should not be in the catalog; refs = %v", ref, c.Refs())
	}
}

func okProvider(id string, discovered []DiscoveredModel) ProviderInput {
	return ProviderInput{
		ID:         id,
		Enabled:    true,
		AuthOK:     true,
		Discover:   true,
		DiscoverOK: true,
		Discovered: discovered,
	}
}

// TestExistenceIsDefinedByDiscovery is the first half of the rule: what the
// provider lists is callable, and a models.dev record on its own is not a
// model — it is metadata about a model that may not exist here.
func TestExistenceIsDefinedByDiscovery(t *testing.T) {
	ix := fixtureIndex() // knows claude-sonnet-4-5, llama-3.3-70b, claude-haiku-4-5
	cat := Build(BuildInput{
		Providers: []ProviderInput{okProvider("omniroute", []DiscoveredModel{
			{WireID: "anthropic/claude-sonnet-4-5"},
		})},
		ModelsDev: ix,
	})

	if cat.Len() != 1 {
		t.Fatalf("Len = %d, want 1; refs = %v", cat.Len(), cat.Refs())
	}
	find(t, cat, "omniroute/anthropic/claude-sonnet-4-5")
	// models.dev also knows about llama, but nobody serves it here.
	mustNotHave(t, cat, "omniroute/meta/llama-3.3-70b")
}

// TestModelsDevFillsGapsButNeverOverwrites is the second half: the gateway
// is the authority on what it actually serves, so a context window it
// reports is kept even when models.dev disagrees. Only the empty fields are
// filled.
func TestModelsDevFillsGapsButNeverOverwrites(t *testing.T) {
	cat := Build(BuildInput{
		Providers: []ProviderInput{okProvider("omniroute", []DiscoveredModel{
			// The gateway trims the window and reports no price at all.
			{WireID: "anthropic/claude-sonnet-4-5", Context: 100_000},
		})},
		ModelsDev: fixtureIndex(),
	})

	m := find(t, cat, "omniroute/anthropic/claude-sonnet-4-5")
	if m.Context != 100_000 {
		t.Errorf("Context = %d, want the gateway's 100000 (models.dev must not overwrite)", m.Context)
	}
	if m.MaxOutput != 64_000 {
		t.Errorf("MaxOutput = %d, want 64000 filled in from models.dev", m.MaxOutput)
	}
	if m.Name != "Claude Sonnet 4.5" {
		t.Errorf("Name = %q, want the models.dev name", m.Name)
	}
	if m.Cost == nil || m.Cost.In != 3 || m.Cost.Out != 15 {
		t.Errorf("Cost = %+v, want in=3 out=15 from models.dev", m.Cost)
	}
	if !m.Caps.Tools || !m.Caps.Vision || !m.Caps.Streaming {
		t.Errorf("Caps = %+v, want tools/vision/streaming from models.dev", m.Caps)
	}
	if !m.Source.Has(SourceDiscover) || !m.Source.Has(SourceModelsDev) {
		t.Errorf("Source = %s, want discover+modelsdev", m.Source)
	}
}

// TestNoMatchLeavesMetadataUnknown: when the cascade finds nothing, the
// record stays honest. §4.3 forbids guessing 128k, and Cost stays nil
// because "unknown" is not "free".
func TestNoMatchLeavesMetadataUnknown(t *testing.T) {
	cat := Build(BuildInput{
		Providers: []ProviderInput{okProvider("omniroute", []DiscoveredModel{
			{WireID: "someone/an-unheard-of-model"},
		})},
		ModelsDev: fixtureIndex(),
	})

	m := find(t, cat, "omniroute/someone/an-unheard-of-model")
	if m.Source.Has(SourceModelsDev) {
		t.Errorf("Source = %s, want no modelsdev bit on an unmatched model", m.Source)
	}
	if m.Context != 0 {
		t.Errorf("Context = %d, want 0 (unknown), never a guessed default", m.Context)
	}
	if m.ContextKnown() {
		t.Error("ContextKnown() = true on a model with no reported window")
	}
	if m.EffectiveContext() != ContextFloor {
		t.Errorf("EffectiveContext() = %d, want the conservative floor %d", m.EffectiveContext(), ContextFloor)
	}
	if m.Cost != nil {
		t.Errorf("Cost = %+v, want nil (unknown is not free)", m.Cost)
	}
}

// TestUserConfigAlwaysWins covers the third source, field by field: only
// what the user actually set is overridden.
func TestUserConfigAlwaysWins(t *testing.T) {
	p := okProvider("omniroute", []DiscoveredModel{
		{WireID: "anthropic/claude-sonnet-4-5", Name: "Claude Sonnet 4.5", Context: 200_000, Output: 64_000},
	})
	p.Declared = []DeclaredModel{{
		WireID:  "anthropic/claude-sonnet-4-5",
		Name:    "Sonnet (mine)",
		Context: 120_000,
		Tags:    []string{"favorite"},
	}}

	cat := Build(BuildInput{Providers: []ProviderInput{p}, ModelsDev: fixtureIndex()})
	if cat.Len() != 1 {
		t.Fatalf("Len = %d, want 1: a declared model must amend the discovered one, not duplicate it", cat.Len())
	}

	m := find(t, cat, "omniroute/anthropic/claude-sonnet-4-5")
	if m.Name != "Sonnet (mine)" {
		t.Errorf("Name = %q, want the user's", m.Name)
	}
	if m.Context != 120_000 {
		t.Errorf("Context = %d, want the user's 120000", m.Context)
	}
	if m.MaxOutput != 64_000 {
		t.Errorf("MaxOutput = %d, want the discovered 64000 kept (the user did not set it)", m.MaxOutput)
	}
	if !m.HasTag("favorite") {
		t.Errorf("Tags = %v, want the user's tag preserved", m.Tags)
	}
	if !m.Source.Has(SourceConfig) || !m.Source.Has(SourceDiscover) {
		t.Errorf("Source = %s, want discover+config", m.Source)
	}
	if m.HasTag(TagUnlisted) {
		t.Error("a discovered model must not be tagged unlisted")
	}
}

// TestDeclaredButNotDiscoveredStaysVisible is the OmniRoute virtual-model
// case named in §4.3: "auto/coding" is real and callable but never shows up
// in GET /models, so it has to survive the merge, marked.
func TestDeclaredButNotDiscoveredStaysVisible(t *testing.T) {
	p := okProvider("omniroute", []DiscoveredModel{{WireID: "openai/gpt-5"}})
	p.Declared = []DeclaredModel{{
		WireID: "auto/coding", Name: "Auto · Coding", Context: 200_000, Tags: []string{TagVirtual},
	}}

	cat := Build(BuildInput{Providers: []ProviderInput{p}})
	m := find(t, cat, "omniroute/auto/coding")
	if !m.HasTag(TagUnlisted) {
		t.Errorf("Tags = %v, want %q: the provider answered and did not list it", m.Tags, TagUnlisted)
	}
	if !m.HasTag(TagVirtual) {
		t.Errorf("Tags = %v, want the declared %q kept", m.Tags, TagVirtual)
	}
	if m.Source != SourceConfig {
		t.Errorf("Source = %s, want config only", m.Source)
	}
	if m.WireID != "auto/coding" || m.Provider != "omniroute" {
		t.Errorf("bad split: provider=%q wire=%q", m.Provider, m.WireID)
	}
}

// TestUnlistedNotClaimedWhenDiscoveryFailed: "the provider does not list it"
// is only true if the provider answered. With discovery down, or off, the
// tag would be a lie.
func TestUnlistedNotClaimedWhenDiscoveryFailed(t *testing.T) {
	for _, tc := range []struct {
		name     string
		discover bool
		ok       bool
	}{
		{"discovery failed", true, false},
		{"discovery disabled", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := ProviderInput{
				ID: "omniroute", Enabled: true, AuthOK: true,
				Discover: tc.discover, DiscoverOK: tc.ok,
				Declared: []DeclaredModel{{WireID: "auto/coding"}},
			}
			cat := Build(BuildInput{Providers: []ProviderInput{p}})
			m := find(t, cat, "omniroute/auto/coding")
			if m.HasTag(TagUnlisted) {
				t.Errorf("Tags = %v, must not claim unlisted when discovery did not answer", m.Tags)
			}
		})
	}
}

// TestHealthReflectsTheLastContact walks the four states of §4.2. The
// unauthenticated case matters most: the model is still listed, because
// hiding it makes the fix impossible to find.
func TestHealthReflectsTheLastContact(t *testing.T) {
	cases := []struct {
		name string
		in   ProviderInput
		want Health
	}{
		{"answering", okProvider("p", []DiscoveredModel{{WireID: "m"}}), HealthOK},
		{
			"no credential",
			ProviderInput{ID: "p", Enabled: true, AuthOK: false, Discover: true, DiscoverOK: true,
				Discovered: []DiscoveredModel{{WireID: "m"}}},
			HealthUnauthenticated,
		},
		{
			"discovery down, models from cache",
			ProviderInput{ID: "p", Enabled: true, AuthOK: true, Discover: true, DiscoverOK: false,
				DiscoverErr: "dial tcp: timeout", Discovered: []DiscoveredModel{{WireID: "m"}}},
			HealthUnreachable,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cat := Build(BuildInput{Providers: []ProviderInput{tc.in}})
			m := find(t, cat, "p/m")
			if m.Health != tc.want {
				t.Errorf("Health = %v, want %v", m.Health, tc.want)
			}
		})
	}

	t.Run("unauthenticated is the only unusable one", func(t *testing.T) {
		if HealthUnauthenticated.Usable() {
			t.Error("HealthUnauthenticated.Usable() = true")
		}
		for _, h := range []Health{HealthOK, HealthCooling, HealthUnreachable} {
			if !h.Usable() {
				t.Errorf("%v.Usable() = false, want true", h)
			}
		}
	})
}

// TestStatsFeedTheRecordAndCooling: the local statistics of §4.5 are merged
// in, and a model that failed three times in a row goes into cooling
// without any provider having said so.
func TestStatsFeedTheRecordAndCooling(t *testing.T) {
	last := time.Date(2026, 7, 30, 14, 0, 0, 0, time.UTC)
	cat := Build(BuildInput{
		Providers: []ProviderInput{okProvider("p", []DiscoveredModel{{WireID: "hot"}, {WireID: "broken"}})},
		Stats: map[string]Stat{
			"p/hot":    {UseCount: 41, LastUsed: last, P50ms: 820},
			"p/broken": {FailStreak: 3},
		},
	})

	hot := find(t, cat, "p/hot")
	if hot.UseCount != 41 || !hot.LastUsed.Equal(last) || hot.P50Latency != 820*time.Millisecond {
		t.Errorf("stats not applied: %+v", hot)
	}
	if hot.Health != HealthOK {
		t.Errorf("Health = %v, want ok", hot.Health)
	}
	if broken := find(t, cat, "p/broken"); broken.Health != HealthCooling {
		t.Errorf("Health = %v after three failures, want cooling", broken.Health)
	}
}

// TestPricingStringsBecomePerMillion covers the OpenRouter-style payload:
// USD per token, as strings. A present-but-zero pricing block is a claim of
// free and must be kept as a zero Cost, not dropped to nil.
func TestPricingStringsBecomePerMillion(t *testing.T) {
	raw := func(s string) json.RawMessage { return json.RawMessage(s) }
	cat := Build(BuildInput{
		Providers: []ProviderInput{okProvider("gw", []DiscoveredModel{
			{WireID: "paid", Raw: raw(`{"pricing":{"prompt":"0.000003","completion":"0.000015"}}`)},
			{WireID: "gratis", Raw: raw(`{"pricing":{"prompt":"0","completion":"0"}}`)},
			{WireID: "silent", Raw: raw(`{"owned_by":"someone"}`)},
			{WireID: "junk", Raw: raw(`{"pricing":{"prompt":"free","completion":"0"}}`)},
		})},
	})

	paid := find(t, cat, "gw/paid")
	if paid.Cost == nil || paid.Cost.In != 3 || paid.Cost.Out != 15 {
		t.Errorf("Cost = %+v, want per-million in=3 out=15", paid.Cost)
	}
	if paid.Free() {
		t.Error("a paid model reports Free() = true")
	}

	free := find(t, cat, "gw/gratis")
	if free.Cost == nil || !free.Cost.Zero() {
		t.Errorf("Cost = %+v, want a zero cost, not nil", free.Cost)
	}
	if !free.HasTag(TagFree) || !free.Free() {
		t.Errorf("Tags = %v, want the free tag", free.Tags)
	}

	if silent := find(t, cat, "gw/silent"); silent.Cost != nil || silent.Free() {
		t.Errorf("Cost = %+v / Free = %v, want unknown and not free", silent.Cost, silent.Free())
	}
	if junk := find(t, cat, "gw/junk"); junk.Cost != nil {
		t.Errorf("Cost = %+v, want nil when a price is unparseable", junk.Cost)
	}
}

// TestRawCapabilitiesAndDeprecation reads the rest of the gateway extras.
func TestRawCapabilitiesAndDeprecation(t *testing.T) {
	cat := Build(BuildInput{
		Providers: []ProviderInput{okProvider("gw", []DiscoveredModel{{
			WireID: "vendor/fancy-1",
			Raw: json.RawMessage(`{
				"owned_by": "vendor",
				"deprecated": true,
				"architecture": {"input_modalities": ["text", "image"]},
				"supported_parameters": ["tools", "reasoning", "response_format"]
			}`),
		}})},
	})

	m := find(t, cat, "gw/vendor/fancy-1")
	if !m.Deprecated() {
		t.Errorf("Tags = %v, want deprecated", m.Tags)
	}
	if !m.Caps.Vision || !m.Caps.Tools || !m.Caps.Reasoning || !m.Caps.JSONSchema {
		t.Errorf("Caps = %+v, want vision+tools+reasoning+json_schema", m.Caps)
	}
	if m.Family != "vendor" {
		t.Errorf("Family = %q, want the owned_by fallback %q", m.Family, "vendor")
	}
}

// TestHideDeprecatedNeverHidesWhatYouUse: the flag drops the models the
// provider is retiring, except the ones in the user's history or in their
// configuration. Something they have actually run has to stay findable.
func TestHideDeprecatedNeverHidesWhatYouUse(t *testing.T) {
	dep := json.RawMessage(`{"deprecated":true}`)
	p := okProvider("gw", []DiscoveredModel{
		{WireID: "old-unused", Raw: dep},
		{WireID: "old-used", Raw: dep},
		{WireID: "old-declared", Raw: dep},
		{WireID: "current"},
	})
	p.Declared = []DeclaredModel{{WireID: "old-declared"}}

	cat := Build(BuildInput{
		Providers:      []ProviderInput{p},
		Stats:          map[string]Stat{"gw/old-used": {UseCount: 7}},
		HideDeprecated: true,
	})

	mustNotHave(t, cat, "gw/old-unused")
	find(t, cat, "gw/old-used")
	find(t, cat, "gw/old-declared")
	find(t, cat, "gw/current")

	if len(cat.Notes) == 0 {
		t.Fatal("hiding models must leave a note saying so")
	}
}

// TestHideDeprecatedViaModelsDevStatus is Layer 0's other half of the
// closing criterion: before this change, hide_deprecated=true had nothing
// to hide for any provider that does not send its own "deprecated": true
// on /models — which is OpenAI, Google and NVIDIA, i.e. most of them (see
// docs/DESIGN-model-curation.md §1.1). This drives the deprecation signal
// entirely from a models.dev fixture, with no gateway "deprecated" field
// anywhere in sight, and a "beta" record to confirm TagBeta gets its first
// real producer.
func TestHideDeprecatedViaModelsDevStatus(t *testing.T) {
	ix := NewIndex()
	ix.ByProvider["gw"] = map[string]MDModel{
		"old-unused": {ID: "old-unused", Status: "deprecated"},
		"old-used":   {ID: "old-used", Status: "deprecated"},
		"preview-1":  {ID: "preview-1", Status: "beta"},
		"current":    {ID: "current"},
	}
	p := okProvider("gw", []DiscoveredModel{
		{WireID: "old-unused"},
		{WireID: "old-used"},
		{WireID: "preview-1"},
		{WireID: "current"},
	})

	cat := Build(BuildInput{
		Providers:      []ProviderInput{p},
		ModelsDev:      ix,
		Stats:          map[string]Stat{"gw/old-used": {UseCount: 3}},
		HideDeprecated: true,
	})

	mustNotHave(t, cat, "gw/old-unused")
	find(t, cat, "gw/old-used") // used, so it survives despite the tag
	find(t, cat, "gw/current")

	preview := find(t, cat, "gw/preview-1")
	if !preview.HasTag(TagBeta) {
		t.Errorf("Tags = %v, want beta (from models.dev status)", preview.Tags)
	}
	if preview.Deprecated() {
		t.Error("a beta model must not also read as deprecated")
	}

	used := find(t, cat, "gw/old-used")
	if !used.Deprecated() {
		t.Errorf("Tags = %v, want deprecated even though it is kept for having been used", used.Tags)
	}

	if len(cat.Notes) == 0 {
		t.Fatal("hiding a model must leave a note saying so")
	}
}

// TestApplyModelsDevCarriesModalitiesAndTemperature is the fix behind
// Layer 1's dependency on both fields actually reaching Model, not just
// MDModel: before this change nonChat() had nothing to read because
// applyModelsDev never copied either field over. Both must stay
// copy-only-if-unset (discovery/config still outrank models.dev), and
// both must preserve "unknown" (empty slice / nil pointer) rather than
// ever synthesizing false evidence.
func TestApplyModelsDevCarriesModalitiesAndTemperature(t *testing.T) {
	falsePtr := false
	ix := NewIndex()
	ix.ByProvider["gw"] = map[string]MDModel{
		"tts-model":     {ID: "tts-model", Modalities: []string{"audio"}, Temperature: &falsePtr},
		"chat-model":    {ID: "chat-model", Modalities: []string{"text"}},
		"unknown-model": {ID: "unknown-model"}, // no modalities, no temperature key
	}
	p := okProvider("gw", []DiscoveredModel{
		{WireID: "tts-model"}, {WireID: "chat-model"}, {WireID: "unknown-model"},
	})
	cat := Build(BuildInput{Providers: []ProviderInput{p}, ModelsDev: ix})

	tts := find(t, cat, "gw/tts-model")
	if len(tts.Modalities) != 1 || tts.Modalities[0] != "audio" {
		t.Errorf("tts-model.Modalities = %v, want [\"audio\"]", tts.Modalities)
	}
	if tts.Temperature == nil || *tts.Temperature != false {
		t.Errorf("tts-model.Temperature = %v, want a pointer to false", tts.Temperature)
	}

	chat := find(t, cat, "gw/chat-model")
	if len(chat.Modalities) != 1 || chat.Modalities[0] != "text" {
		t.Errorf("chat-model.Modalities = %v, want [\"text\"]", chat.Modalities)
	}
	if chat.Temperature != nil {
		t.Errorf("chat-model.Temperature = %v, want nil (key absent)", *chat.Temperature)
	}

	unknown := find(t, cat, "gw/unknown-model")
	if len(unknown.Modalities) != 0 {
		t.Errorf("unknown-model.Modalities = %v, want empty (unknown, not evidence)", unknown.Modalities)
	}
	if unknown.Temperature != nil {
		t.Errorf("unknown-model.Temperature = %v, want nil", *unknown.Temperature)
	}
}

// TestNotesAreHonestAboutFailures: a provider that could not be reached, or
// has no credential, produces a one-liner the interface can show. Never an
// error that aborts the startup.
func TestNotesAreHonestAboutFailures(t *testing.T) {
	cat := Build(BuildInput{Providers: []ProviderInput{{
		ID: "omniroute", Enabled: true, AuthOK: false, Discover: true, DiscoverOK: false,
		DiscoverErr: "dial tcp: timeout",
		Declared:    []DeclaredModel{{WireID: "auto/coding"}},
	}}})

	if cat.Len() != 1 {
		t.Fatalf("Len = %d, want the declared model to survive a total failure", cat.Len())
	}
	joined := ""
	for _, n := range cat.Notes {
		joined += n + "\n"
	}
	for _, want := range []string{"could not list the models of omniroute", "dial tcp: timeout", "no resolved credential"} {
		if !strings.Contains(joined, want) {
			t.Errorf("notes %q missing %q", joined, want)
		}
	}
}

// TestPendingProvidersCountsFailedDiscovery is F11's "catalogs refreshed /
// N pending" notice own data source: a provider that came back
// !DiscoverOK is exactly the case TestNotesAreHonestAboutFailures above
// already narrates in a Note; this asserts the same condition is also
// counted, not only narrated.
func TestPendingProvidersCountsFailedDiscovery(t *testing.T) {
	cat := Build(BuildInput{Providers: []ProviderInput{
		okProvider("a", []DiscoveredModel{{WireID: "m1"}}),
		{ID: "b", Enabled: true, AuthOK: true, Discover: true, DiscoverOK: false,
			DiscoverErr: "timeout", Declared: []DeclaredModel{{WireID: "m2"}}},
		{ID: "c", Enabled: true, AuthOK: true, Discover: true, DiscoverOK: false,
			DiscoverErr: "timeout", Declared: []DeclaredModel{{WireID: "m3"}}},
	}})
	if cat.PendingProviders != 2 {
		t.Errorf("PendingProviders = %d, want 2 (b and c both failed discovery)", cat.PendingProviders)
	}
}

// TestPendingProvidersIsZeroWhenEveryoneAnswered is the negative case: a
// catalog built from providers that all succeeded (or never had discovery
// enabled to begin with) must not claim anything is pending.
func TestPendingProvidersIsZeroWhenEveryoneAnswered(t *testing.T) {
	cat := Build(BuildInput{Providers: []ProviderInput{
		okProvider("a", []DiscoveredModel{{WireID: "m1"}}),
		{ID: "b", Enabled: true, AuthOK: true, Discover: false,
			Declared: []DeclaredModel{{WireID: "m2"}}},
	}})
	if cat.PendingProviders != 0 {
		t.Errorf("PendingProviders = %d, want 0", cat.PendingProviders)
	}
}

// TestFetchedAtIsTheOldestSource: the staleness strip of §4.4 must show the
// worst case, not the best. Two providers refreshed hours apart are "as old
// as" the older one.
func TestFetchedAtIsTheOldestSource(t *testing.T) {
	old := time.Date(2026, 7, 28, 9, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 7, 30, 14, 0, 0, 0, time.UTC)

	a := okProvider("a", []DiscoveredModel{{WireID: "m"}})
	a.FetchedAt = recent
	b := okProvider("b", []DiscoveredModel{{WireID: "m"}})
	b.FetchedAt = old

	cat := Build(BuildInput{Providers: []ProviderInput{a, b}})
	if !cat.FetchedAt.Equal(old) {
		t.Errorf("FetchedAt = %v, want the oldest %v", cat.FetchedAt, old)
	}
}

// TestOrderIsDeterministic: providers in configuration order, most-used
// first inside each provider, deprecated last, ties by reference. A list
// that reshuffles between runs cannot be used from muscle memory.
func TestOrderIsDeterministic(t *testing.T) {
	dep := json.RawMessage(`{"deprecated":true}`)
	first := okProvider("first", []DiscoveredModel{
		{WireID: "zeta"},
		{WireID: "alpha"},
		{WireID: "used"},
		{WireID: "gone", Raw: dep},
	})
	second := okProvider("second", []DiscoveredModel{{WireID: "only"}})

	cat := Build(BuildInput{
		Providers: []ProviderInput{first, second},
		Stats:     map[string]Stat{"first/used": {UseCount: 3}},
	})

	want := []string{"first/used", "first/alpha", "first/zeta", "first/gone", "second/only"}
	got := cat.Refs()
	if len(got) != len(want) {
		t.Fatalf("refs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("refs = %v, want %v", got, want)
		}
	}
	if p := cat.Providers(); len(p) != 2 || p[0] != "first" || p[1] != "second" {
		t.Errorf("Providers() = %v, want configuration order", p)
	}
	if g := cat.ByProvider(); len(g["first"]) != 4 || len(g["second"]) != 1 {
		t.Errorf("ByProvider() sizes = %d/%d, want 4/1", len(g["first"]), len(g["second"]))
	}
}

// TestEntriesWithoutAnIDAreDropped: an entry with no identifier cannot be
// called, so it is not a model. Real gateways do ship these.
func TestEntriesWithoutAnIDAreDropped(t *testing.T) {
	p := okProvider("gw", []DiscoveredModel{{WireID: "  "}, {WireID: "real"}})
	p.Declared = []DeclaredModel{{WireID: ""}}

	cat := Build(BuildInput{Providers: []ProviderInput{p}})
	if cat.Len() != 1 {
		t.Fatalf("Len = %d, want 1; refs = %v", cat.Len(), cat.Refs())
	}
	find(t, cat, "gw/real")
}

// TestFamilyOf is the last-resort grouping guess. Being wrong is cheap;
// being absent is not, because then nothing groups.
func TestFamilyOf(t *testing.T) {
	cases := map[string]string{
		"anthropic/claude-sonnet-4-5": "claude",
		"gpt-4o-mini":                 "gpt",
		"llama-3.3-70b":               "llama",
		"4-mini":                      "mini",
		"qwen2.5:7b":                  "qwen2.5",
		"":                            "",
	}
	for in, want := range cases {
		if got := familyOf(in); got != want {
			t.Errorf("familyOf(%q) = %q, want %q", in, got, want)
		}
	}
}

package catalog

import (
	"strings"
	"testing"
	"time"
)

// resolve_test.go is the contract with the central requirement of the
// product —never having to type the exact id— and §12 says in as many words
// that it is written before the picker UI. Every case in the mandatory
// table of Step 7 has a test here, plus the mechanics each one depends on.

// resolveFixture is the catalog these tests reason about: one gateway
// serving several vendors, with the three pairs that make the scoring
// interesting —sonnet-4-5 next to sonnet-4-0, gpt-5 next to gpt-5-nano, and
// a haiku nobody else serves.
func resolveFixture() Catalog {
	return Build(BuildInput{
		Providers: []ProviderInput{okProvider("omniroute", []DiscoveredModel{
			{WireID: "anthropic/claude-sonnet-4-5", Name: "Claude Sonnet 4.5"},
			{WireID: "anthropic/claude-sonnet-4-0", Name: "Claude Sonnet 4"},
			{WireID: "anthropic/claude-haiku-4-5", Name: "Claude Haiku 4.5"},
			{WireID: "openai/gpt-5", Name: "GPT-5"},
			{WireID: "openai/gpt-5-nano", Name: "GPT-5 nano"},
			{WireID: "google/gemini-2.5-pro", Name: "Gemini 2.5 Pro"},
			{WireID: "meta/llama-3.3-70b", Name: "Llama 3.3 70B"},
			{WireID: "auto/coding"},
			{WireID: "auto/cheap"},
		})},
	})
}

func mustResolve(t *testing.T, c Catalog, q string, opts ResolveOptions) Resolution {
	t.Helper()
	res := c.Resolve(q, opts)
	if !res.Outcome.Decided() {
		t.Fatalf("Resolve(%q) = %s, want a decided outcome; candidates = %s",
			q, res.Outcome, candidateRefs(res.Candidates))
	}
	return res
}

func candidateRefs(cs []Candidate) string {
	if len(cs) == 0 {
		return "(none)"
	}
	var b strings.Builder
	for i, c := range cs {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(c.Model.Ref)
	}
	return b.String()
}

// TestResolveTable is the mandatory table of §12, Step 7, case by case.
func TestResolveTable(t *testing.T) {
	cat := resolveFixture()
	opts := ResolveOptions{
		Alias: map[string]string{
			"smart": "omniroute/auto/coding",
			"fast":  "omniroute/auto/cheap",
		},
		Now: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC),
	}

	cases := []struct {
		name    string
		query   string
		wantRef string
		wantOut Outcome
	}{
		{
			// The marquee case of §4.5: the digits have to break the tie
			// against sonnet-4-0.
			name:    "son45 lands on sonnet 4.5 and not on 4.0",
			query:   "son45",
			wantRef: "omniroute/anthropic/claude-sonnet-4-5",
			wantOut: OutcomeFuzzy,
		},
		{
			// gpt5 is the entire leaf of gpt-5 and only part of the leaf
			// of gpt-5-nano, so it must not be treated as ambiguous.
			name:    "gpt5 picks gpt-5 over gpt-5-nano",
			query:   "gpt5",
			wantRef: "omniroute/openai/gpt-5",
			wantOut: OutcomeSuffix,
		},
		{
			name:    "haiku is a unique whole-word match",
			query:   "haiku",
			wantRef: "omniroute/anthropic/claude-haiku-4-5",
			wantOut: OutcomeWord,
		},
		{
			name:    "smart resolves through the config alias",
			query:   "smart",
			wantRef: "omniroute/auto/coding",
			wantOut: OutcomeAlias,
		},
		{
			name:    "a full wire id resolves by suffix",
			query:   "anthropic/claude-sonnet-4-5",
			wantRef: "omniroute/anthropic/claude-sonnet-4-5",
			wantOut: OutcomeSuffix,
		},
		{
			name:    "the exact reference is stage one",
			query:   "omniroute/openai/gpt-5-nano",
			wantRef: "omniroute/openai/gpt-5-nano",
			wantOut: OutcomeExact,
		},
		{
			name:    "the exact reference is case insensitive",
			query:   "OmniRoute/OpenAI/GPT-5-Nano",
			wantRef: "omniroute/openai/gpt-5-nano",
			wantOut: OutcomeExact,
		},
		{
			name:    "an explicit provider prefix narrows the fuzzy search",
			query:   "omniroute/son45",
			wantRef: "omniroute/anthropic/claude-sonnet-4-5",
			wantOut: OutcomeFuzzy,
		},
		{
			name:    "the human name is matchable too",
			query:   "gemini 2.5 pro",
			wantRef: "omniroute/google/gemini-2.5-pro",
			wantOut: OutcomeSuffix,
		},
		{
			// "nano" is also a word-aligned suffix, so it never reaches
			// the whole-word rung: stage 3a wins first.
			name:    "nano is a unique suffix",
			query:   "nano",
			wantRef: "omniroute/openai/gpt-5-nano",
			wantOut: OutcomeSuffix,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := mustResolve(t, cat, tc.query, opts)
			if res.Model.Ref != tc.wantRef {
				t.Errorf("Resolve(%q).Model.Ref = %q, want %q (outcome %s, candidates: %s)",
					tc.query, res.Model.Ref, tc.wantRef, res.Outcome, candidateRefs(res.Candidates))
			}
			if res.Outcome != tc.wantOut {
				t.Errorf("Resolve(%q).Outcome = %s, want %s", tc.query, res.Outcome, tc.wantOut)
			}
		})
	}
}

// TestAmbiguousSuffixOpensThePicker is the case §12 spells out: two
// providers serving the same model must not be guessed between.
func TestAmbiguousSuffixOpensThePicker(t *testing.T) {
	cat := Build(BuildInput{
		Providers: []ProviderInput{
			okProvider("omniroute", []DiscoveredModel{
				{WireID: "anthropic/claude-sonnet-4-5"},
			}),
			okProvider("anthropic", []DiscoveredModel{
				{WireID: "claude-sonnet-4-5"},
			}),
		},
	})

	res := cat.Resolve("claude-sonnet-4-5", ResolveOptions{})
	if res.Outcome != OutcomePicker {
		t.Fatalf("Outcome = %s, want picker (it resolved to %q)", res.Outcome, res.Model.Ref)
	}
	if res.Model.Ref != "" {
		t.Errorf("Model = %q, want the zero value: nothing was decided", res.Model.Ref)
	}
	if len(res.Candidates) != 2 {
		t.Fatalf("Candidates = %s, want both providers", candidateRefs(res.Candidates))
	}
	if res.Query != "claude-sonnet-4-5" {
		t.Errorf("Query = %q, want the picker prefiltered with what was typed", res.Query)
	}
}

// TestNoMatchOpensThePickerAndNeverErrors is the other half of the rule:
// "model not found" on its own is forbidden by §4.5.
func TestNoMatchOpensThePickerAndNeverErrors(t *testing.T) {
	cat := resolveFixture()

	for _, q := range []string{"zzzzz", "quetzalcoatl", "!!!", "9999999"} {
		res := cat.Resolve(q, ResolveOptions{})
		if res.Outcome != OutcomePicker {
			t.Errorf("Resolve(%q).Outcome = %s, want picker", q, res.Outcome)
		}
		if res.Query != q {
			t.Errorf("Resolve(%q).Query = %q, want the picker prefiltered with the query", q, res.Query)
		}
	}
}

// TestEmptyQueryAndEmptyCatalog covers the two degenerate inputs: neither
// may panic and neither may claim to have decided anything.
func TestEmptyQueryAndEmptyCatalog(t *testing.T) {
	cat := resolveFixture()
	if res := cat.Resolve("   ", ResolveOptions{}); res.Outcome != OutcomePicker {
		t.Errorf("empty query: Outcome = %s, want picker", res.Outcome)
	}

	var empty Catalog
	if res := empty.Resolve("son45", ResolveOptions{}); res.Outcome != OutcomeNone {
		t.Errorf("empty catalog: Outcome = %s, want none", res.Outcome)
	}
	var nilCat *Catalog
	if res := nilCat.Resolve("son45", ResolveOptions{}); res.Outcome != OutcomeNone {
		t.Errorf("nil catalog: Outcome = %s, want none", res.Outcome)
	}
}

// TestAliasChainAndCycle: an alias may point at another alias, and a cycle
// in somebody's toml must degrade to the picker instead of hanging.
func TestAliasChainAndCycle(t *testing.T) {
	cat := resolveFixture()

	chain := cat.Resolve("work", ResolveOptions{Alias: map[string]string{
		"work":  "smart",
		"smart": "omniroute/auto/coding",
	}})
	if chain.Outcome != OutcomeAlias || chain.Model.Ref != "omniroute/auto/coding" {
		t.Errorf("alias chain: got %s / %q, want alias / omniroute/auto/coding",
			chain.Outcome, chain.Model.Ref)
	}
	if chain.Via == "" {
		t.Error("Via is empty; the alias chain should be reportable")
	}

	// A cycle: bounded hops, then whatever the last hop looks like. What
	// matters is that it returns at all.
	done := make(chan Resolution, 1)
	go func() {
		done <- cat.Resolve("a", ResolveOptions{Alias: map[string]string{
			"a": "b", "b": "c", "c": "a",
		}})
	}()
	select {
	case res := <-done:
		if res.Outcome.Decided() {
			t.Errorf("alias cycle resolved to %q; it should not decide anything", res.Model.Ref)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Resolve did not return: the alias cycle is a loop")
	}

	// An alias pointing at itself is a configuration error, not a loop,
	// and must not shadow a real model with the same name.
	self := cat.Resolve("gpt5", ResolveOptions{Alias: map[string]string{"gpt5": "gpt5"}})
	if self.Model.Ref != "omniroute/openai/gpt-5" {
		t.Errorf("self-referencing alias: got %q, want the real gpt-5", self.Model.Ref)
	}
}

// TestAliasOutranksSimilarity: an alias is an explicit instruction and must
// beat any amount of string similarity.
func TestAliasOutranksSimilarity(t *testing.T) {
	cat := resolveFixture()
	res := cat.Resolve("gpt5", ResolveOptions{Alias: map[string]string{
		"gpt5": "omniroute/anthropic/claude-haiku-4-5",
	}})
	if res.Outcome != OutcomeAlias || res.Model.Ref != "omniroute/anthropic/claude-haiku-4-5" {
		t.Errorf("got %s / %q, want the alias to win", res.Outcome, res.Model.Ref)
	}
}

// TestDigitsDecideTheVersion is the mechanism §4.5 names explicitly, tested
// on its own so a regression here is unmistakable.
func TestDigitsDecideTheVersion(t *testing.T) {
	cat := resolveFixture()
	opts := ResolveOptions{}

	for _, tc := range []struct{ query, want string }{
		{"son45", "omniroute/anthropic/claude-sonnet-4-5"},
		{"son40", "omniroute/anthropic/claude-sonnet-4-0"},
		{"sonnet4-0", "omniroute/anthropic/claude-sonnet-4-0"},
		{"llama33", "omniroute/meta/llama-3.3-70b"},
	} {
		res := mustResolve(t, cat, tc.query, opts)
		if res.Model.Ref != tc.want {
			t.Errorf("Resolve(%q) = %q, want %q", tc.query, res.Model.Ref, tc.want)
		}
	}
}

// TestDeprecatedIsPenalized: a retired model is still findable by its exact
// reference, but it never wins a fuzzy tie against a live sibling.
func TestDeprecatedIsPenalized(t *testing.T) {
	cat := Build(BuildInput{
		Providers: []ProviderInput{okProvider("omniroute", []DiscoveredModel{
			{WireID: "openai/gpt-5-turbo"},
			{WireID: "openai/gpt-5-turbo-old", Tags: []string{TagDeprecated}},
		})},
	})

	res := cat.Resolve("gpt5turbo", ResolveOptions{})
	if res.Model.Ref != "omniroute/openai/gpt-5-turbo" {
		t.Errorf("got %q, want the live model to win (outcome %s, candidates %s)",
			res.Model.Ref, res.Outcome, candidateRefs(res.Candidates))
	}
	if exact, ok := cat.Get("omniroute/openai/gpt-5-turbo-old"); !ok || exact.Ref == "" {
		t.Error("the deprecated model disappeared; it must stay reachable by exact reference")
	}
}

// TestStatsBreakTies: recency and frequency are a tiebreaker between things
// that already matched, and they are capped low enough not to promote
// something the user did not type.
func TestStatsBreakTies(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	cat := Build(BuildInput{
		Providers: []ProviderInput{okProvider("omniroute", []DiscoveredModel{
			{WireID: "vendor-a/mixtral-8x7b"},
			{WireID: "vendor-b/mixtral-8x7b"},
		})},
		Stats: map[string]Stat{
			"omniroute/vendor-b/mixtral-8x7b": {UseCount: 40, LastUsed: now.Add(-time.Hour)},
		},
	})

	cands := cat.Filter("mixtral", ResolveOptions{Now: now})
	if len(cands) != 2 {
		t.Fatalf("Filter returned %s, want both", candidateRefs(cands))
	}
	if cands[0].Model.Ref != "omniroute/vendor-b/mixtral-8x7b" {
		t.Errorf("ranking = %s, want the frequently used one first", candidateRefs(cands))
	}
	// ...but not by enough to clear the 20% bar: two identical models are
	// still a question for the user.
	if res := cat.Resolve("mixtral", ResolveOptions{Now: now}); res.Outcome != OutcomePicker {
		t.Errorf("Outcome = %s, want picker: stats break ties, they do not decide", res.Outcome)
	}
}

// TestPreferFree only bonuses when asked, and never turns unknown cost into
// free — that lie is the one thing §4.2 forbids outright.
func TestPreferFree(t *testing.T) {
	cat := Build(BuildInput{
		Providers: []ProviderInput{okProvider("omniroute", []DiscoveredModel{
			{WireID: "vendor/qwen-30b", Tags: []string{TagFree}},
			{WireID: "vendor/qwen-30b-pro"},
		})},
	})

	plain := cat.Filter("qwen30b", ResolveOptions{})
	free := cat.Filter("qwen30b", ResolveOptions{PreferFree: true})
	if len(plain) != 2 || len(free) != 2 {
		t.Fatalf("plain = %s, free = %s; want both in each", candidateRefs(plain), candidateRefs(free))
	}
	if free[0].Score <= plain[0].Score && free[0].Model.Free() {
		t.Errorf("prefer_free did not raise the free model's score (%v vs %v)",
			free[0].Score, plain[0].Score)
	}
}

// TestFilterEmptyQueryReturnsEverything: the picker opens on the full list,
// in catalog order, and only starts scoring once something is typed.
func TestFilterEmptyQueryReturnsEverything(t *testing.T) {
	cat := resolveFixture()
	got := cat.Filter("", ResolveOptions{})
	if len(got) != cat.Len() {
		t.Fatalf("Filter(\"\") returned %d, want all %d", len(got), cat.Len())
	}
	for i, c := range got {
		if c.Model.Ref != cat.Models[i].Ref {
			t.Fatalf("Filter(\"\")[%d] = %q, want catalog order %q", i, c.Model.Ref, cat.Models[i].Ref)
		}
	}
}

// TestNormalizeRef pins the normalization of §4.5: lowercase, the five
// separators dropped, and a word start at every letter↔digit transition so
// `gpt5` and `gpt-5` reduce to the same shape.
func TestNormalizeRef(t *testing.T) {
	got := normalizeRef("OmniRoute/OpenAI/GPT-5-nano")
	if string(got.runes) != "omnirouteopenaigpt5nano" {
		t.Fatalf("runes = %q", string(got.runes))
	}
	if string(got.digits) != "5" {
		t.Errorf("digits = %q, want \"5\"", string(got.digits))
	}
	if got.leafFrom != len("omnirouteopenai") {
		t.Errorf("leafFrom = %d, want %d", got.leafFrom, len("omnirouteopenai"))
	}
	// The "5" must be a word start even though no separator preceded it in
	// the normalized form.
	five := strings.IndexRune(string(got.runes), '5')
	if !got.start[five] {
		t.Error("the digit after a letter is not marked as a word start")
	}
	if !got.start[0] {
		t.Error("the first rune is not marked as a word start")
	}

	if plain := normalizeRef("gpt5"); string(plain.runes) != "gpt5" || !plain.start[3] {
		t.Errorf("gpt5 normalized to %q with start %v", string(plain.runes), plain.start)
	}
}

// TestMatchQuality checks the base scorer directly: a contiguous
// word-aligned match must outscore the same letters scattered around.
func TestMatchQuality(t *testing.T) {
	q := normalizeRef("son")
	tight, _, ok := matchQuality(q, normalizeRef("claude-sonnet"))
	if !ok {
		t.Fatal("son is a subsequence of claude-sonnet")
	}
	loose, _, ok := matchQuality(q, normalizeRef("stratos-orion-nexus"))
	if !ok {
		t.Fatal("son is a subsequence of stratos-orion-nexus")
	}
	if tight <= loose {
		t.Errorf("contiguous match scored %v, scattered scored %v; want contiguous higher", tight, loose)
	}

	if _, _, ok := matchQuality(normalizeRef("zzz"), normalizeRef("claude-sonnet")); ok {
		t.Error("zzz matched claude-sonnet")
	}
	if _, _, ok := matchQuality(normalizeRef("claude-sonnet"), normalizeRef("son")); ok {
		t.Error("a query longer than the target matched")
	}
}

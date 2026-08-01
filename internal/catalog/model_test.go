package catalog

import "testing"

func TestSplitRefFirstSlashOnly(t *testing.T) {
	// §4.2: OmniRoute serves models whose own id already has a slash
	// ("anthropic/claude-sonnet-4-5"), so the cut must happen on the FIRST
	// slash only.
	cases := []struct {
		ref, wantProvider, wantWire string
		wantOK                      bool
	}{
		{"omniroute/anthropic/claude-sonnet-4-5", "omniroute", "anthropic/claude-sonnet-4-5", true},
		{"openai/gpt-5", "openai", "gpt-5", true},
		{"no-slash-at-all", "", "no-slash-at-all", false},
		{"/leading-slash", "", "/leading-slash", false},
		{"trailing-slash/", "", "trailing-slash/", false},
		{"  omniroute/gpt-5  ", "omniroute", "gpt-5", true},
	}
	for _, c := range cases {
		gotProvider, gotWire, gotOK := SplitRef(c.ref)
		if gotProvider != c.wantProvider || gotWire != c.wantWire || gotOK != c.wantOK {
			t.Errorf("SplitRef(%q) = (%q, %q, %v), want (%q, %q, %v)",
				c.ref, gotProvider, gotWire, gotOK, c.wantProvider, c.wantWire, c.wantOK)
		}
	}
}

func TestJoinRefRoundTrip(t *testing.T) {
	ref := JoinRef("omniroute", "anthropic/claude-sonnet-4-5")
	provID, wireID, ok := SplitRef(ref)
	if !ok || provID != "omniroute" || wireID != "anthropic/claude-sonnet-4-5" {
		t.Fatalf("round trip broke: ref=%q provider=%q wire=%q ok=%v", ref, provID, wireID, ok)
	}
}

func TestCostZeroVsNil(t *testing.T) {
	// §4.2: nil means UNKNOWN, which is not the same as free (Cost{0,0}).
	var nilCost *Cost
	if nilCost.Zero() {
		t.Error("a nil Cost must not report Zero() == true; nil means unknown, not free")
	}
	free := &Cost{}
	if !free.Zero() {
		t.Error("a Cost with every field at zero must report Zero() == true")
	}
	paid := &Cost{In: 3}
	if paid.Zero() {
		t.Error("a Cost with a non-zero field must not report Zero()")
	}
}

func TestModelFreeIsNotUnknown(t *testing.T) {
	unknown := Model{}
	if unknown.Free() {
		t.Error("a model with Cost == nil and no tag must not be reported as free")
	}
	free := Model{Cost: &Cost{}}
	if !free.Free() {
		t.Error("a model whose Cost is all-zero must be reported as free")
	}
	tagged := Model{Cost: &Cost{In: 5}, Tags: []string{TagFree}}
	if !tagged.Free() {
		t.Error("a model tagged \"free\" must be reported as free even with a non-zero Cost value")
	}
}

func TestEffectiveContextFloor(t *testing.T) {
	known := Model{Context: 128_000}
	if !known.ContextKnown() || known.EffectiveContext() != 128_000 {
		t.Errorf("known context: got %d, want 128000", known.EffectiveContext())
	}
	unknown := Model{}
	if unknown.ContextKnown() {
		t.Error("Context == 0 must report ContextKnown() == false")
	}
	if unknown.EffectiveContext() != ContextFloor {
		t.Errorf("unknown context: got %d, want the conservative floor %d", unknown.EffectiveContext(), ContextFloor)
	}
}

func TestCapsMergeNeverClears(t *testing.T) {
	a := Caps{Tools: true}
	b := Caps{Vision: true}
	got := a.Merge(b)
	if !got.Tools || !got.Vision {
		t.Errorf("Merge must OR the two sets, got %+v", got)
	}
	// Merging with an empty Caps must not clear what a already had.
	if got2 := a.Merge(Caps{}); !got2.Tools {
		t.Error("merging with an empty Caps cleared an existing capability")
	}
}

func TestCatalogGetCaseInsensitive(t *testing.T) {
	cat := Catalog{Models: []Model{
		{Ref: "OmniRoute/GPT-5", Provider: "OmniRoute", WireID: "GPT-5"},
	}}
	m, ok := cat.Get("omniroute/gpt-5")
	if !ok {
		t.Fatal("Get must be case-insensitive")
	}
	if m.Ref != "OmniRoute/GPT-5" {
		t.Errorf("Get returned the wrong model: %+v", m)
	}
	if !cat.Has("omniroute/gpt-5") {
		t.Error("Has must agree with Get")
	}
	if cat.Has("nonexistent/model") {
		t.Error("Has must be false for a reference that was never added")
	}
}

func TestCatalogProvidersFirstAppearanceOrder(t *testing.T) {
	cat := Catalog{Models: []Model{
		{Ref: "b/x", Provider: "b"},
		{Ref: "a/y", Provider: "a"},
		{Ref: "b/z", Provider: "b"},
	}}
	got := cat.Providers()
	want := []string{"b", "a"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("Providers() = %v, want %v (first-appearance order)", got, want)
	}
}

func TestNoteDeduplicates(t *testing.T) {
	var cat Catalog
	cat.Note("hello")
	cat.Note("hello")
	cat.Note("world")
	if len(cat.Notes) != 2 {
		t.Errorf("Note must ignore an exact duplicate, got %v", cat.Notes)
	}
}

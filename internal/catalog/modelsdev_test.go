package catalog

import "testing"

func TestNormalizeID(t *testing.T) {
	cases := map[string]string{
		"claude-3-5-sonnet-latest":     "claude-3-5-sonnet",
		"claude-3-5-sonnet-20241022":   "claude-3-5-sonnet",
		"claude-3-5-sonnet-2024-10-22": "claude-3-5-sonnet",
		"anthropic/claude-sonnet-4-5":  "claude-sonnet-4-5",
		"gpt-4.1":                      "gpt-4.1", // a dot before digits is a version, not a vendor
		"GPT-5":                        "gpt-5",
		"  spaced  ":                   "spaced",
	}
	for in, want := range cases {
		if got := NormalizeID(in); got != want {
			t.Errorf("NormalizeID(%q) = %q, want %q", in, got, want)
		}
	}
}

func fixtureIndex() *Index {
	ix := NewIndex()
	ix.ByProvider["anthropic"] = map[string]MDModel{
		"claude-sonnet-4-5": {
			ID: "claude-sonnet-4-5", Name: "Claude Sonnet 4.5", Family: "claude",
			Context: 200000, Output: 64000, Tools: true, Vision: true,
			Cost: &MDCost{Input: 3, Output: 15},
		},
	}
	ix.Agnostic["meta/llama-3.3-70b"] = MDModel{
		ID: "llama-3.3-70b", Name: "Llama 3.3 70B", Family: "llama", Context: 131072,
	}
	ix.Agnostic["claude-haiku-4-5"] = MDModel{
		ID: "claude-haiku-4-5", Name: "Claude Haiku 4.5", Family: "claude", Context: 200000,
	}
	return ix
}

func TestLookupCascadeAllFourStages(t *testing.T) {
	ix := fixtureIndex()

	t.Run("exact", func(t *testing.T) {
		m, stage := ix.Lookup("anthropic", "claude-sonnet-4-5")
		if stage != MatchExact {
			t.Fatalf("stage = %v, want MatchExact", stage)
		}
		if m.Context != 200000 || m.Cost.Input != 3 {
			t.Errorf("wrong record matched: %+v", m)
		}
	})

	t.Run("vendor prefix carried in the wire id (the gateway case)", func(t *testing.T) {
		// OmniRoute serves this model as "anthropic/claude-sonnet-4-5" under
		// its OWN provider id "omniroute" — models.dev has no "omniroute"
		// provider, so the vendor segment inside the wire id must be tried.
		m, stage := ix.Lookup("omniroute", "anthropic/claude-sonnet-4-5")
		if stage != MatchVendor {
			t.Fatalf("stage = %v, want MatchVendor", stage)
		}
		if m.Name != "Claude Sonnet 4.5" {
			t.Errorf("wrong record matched: %+v", m)
		}
	})

	t.Run("normalized (date suffix + vendor prefix stripped)", func(t *testing.T) {
		m, stage := ix.Lookup("omniroute", "meta/llama-3.3-70b-20250101")
		if stage != MatchNormalized {
			t.Fatalf("stage = %v, want MatchNormalized", stage)
		}
		if m.Name != "Llama 3.3 70B" {
			t.Errorf("wrong record matched: %+v", m)
		}
	})

	t.Run("family, longest common prefix wins", func(t *testing.T) {
		// Not in the index under any of its exact/vendor/normalized forms,
		// but "claude" is a known family and this identifier is closer to
		// claude-haiku-4-5 than to claude-sonnet-4-5.
		m, stage := ix.Lookup("customgw", "claude-haiku-4-5-preview")
		if stage != MatchFamily {
			t.Fatalf("stage = %v, want MatchFamily", stage)
		}
		if m.Name != "Claude Haiku 4.5" {
			t.Errorf("family match picked the wrong candidate: %+v", m)
		}
	})

	t.Run("no match at all", func(t *testing.T) {
		_, stage := ix.Lookup("customgw", "totally-unknown-model-xyz")
		if stage != MatchNone {
			t.Fatalf("stage = %v, want MatchNone", stage)
		}
	})
}

func TestParseAPISkipsBrokenEntriesButKeepsGoodOnes(t *testing.T) {
	raw := []byte(`{
		"anthropic": {"id":"anthropic","name":"Anthropic","models":{
			"claude-sonnet-4-5": {"id":"claude-sonnet-4-5","name":"Claude Sonnet 4.5","limit":{"context":200000}}
		}},
		"broken-provider": "not-an-object"
	}`)
	ix := NewIndex()
	if err := ix.ParseAPI(raw); err != nil {
		t.Fatalf("ParseAPI must tolerate one broken provider entry: %v", err)
	}
	m, stage := ix.Lookup("anthropic", "claude-sonnet-4-5")
	if stage != MatchExact || m.Context != 200000 {
		t.Errorf("the good provider entry must still parse: %+v (stage=%v)", m, stage)
	}
}

func TestParseAPIUnreadableReturnsError(t *testing.T) {
	ix := NewIndex()
	if err := ix.ParseAPI([]byte("not json at all")); err == nil {
		t.Fatal("completely unreadable api.json must return an error")
	}
}

func TestMDModelCatalogCostNilVsSet(t *testing.T) {
	noCost := MDModel{}
	if noCost.CatalogCost() != nil {
		t.Error("a models.dev record with no cost block must map to a nil *Cost")
	}
	withCost := MDModel{Cost: &MDCost{Input: 1, Output: 2}}
	got := withCost.CatalogCost()
	if got == nil || got.In != 1 || got.Out != 2 {
		t.Errorf("cost conversion broke: %+v", got)
	}
}

func TestDigestPath(t *testing.T) {
	got := DigestPath("/home/user/.cache/ishakat/catalog.json")
	want := "/home/user/.cache/ishakat/catalog-modelsdev.json"
	if got != want {
		t.Errorf("DigestPath = %q, want %q", got, want)
	}
	if DigestPath("") != "" {
		t.Error("DigestPath(\"\") must return \"\"")
	}
}

func TestSaveLoadDigestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/catalog-modelsdev.json"

	ix := fixtureIndex()
	ix.ETag = `"abc123"`
	if err := ix.SaveDigest(path); err != nil {
		t.Fatalf("SaveDigest: %v", err)
	}

	got := LoadDigest(path)
	if got.ETag != `"abc123"` {
		t.Errorf("ETag round trip broke: %q", got.ETag)
	}
	m, stage := got.Lookup("anthropic", "claude-sonnet-4-5")
	if stage != MatchExact || m.Name != "Claude Sonnet 4.5" {
		t.Errorf("digest round trip lost data: %+v (stage=%v)", m, stage)
	}
}

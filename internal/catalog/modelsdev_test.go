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

// TestParseAPIStatusAndTemperature is Layer 0's own closing criterion
// (docs/DESIGN-model-curation.md): a models.dev record carrying
// "status": "deprecated" must produce MDModel.Status == "deprecated", one
// with "beta" or "alpha" must round-trip as-is (the tag mapping itself is
// merge.go's job, tested end to end in merge_test.go), a record with no
// status key must leave Status empty, and Temperature must stay nil when
// the key is absent rather than becoming a false pointer.
func TestParseAPIStatusAndTemperature(t *testing.T) {
	raw := []byte(`{
		"anthropic": {"id":"anthropic","name":"Anthropic","models":{
			"claude-old":     {"id":"claude-old",     "status":"deprecated"},
			"claude-preview": {"id":"claude-preview", "status":"beta"},
			"claude-alpha":   {"id":"claude-alpha",   "status":"alpha"},
			"claude-current": {"id":"claude-current"},
			"claude-embed":   {"id":"claude-embed",   "temperature": false},
			"claude-chat":    {"id":"claude-chat",    "temperature": true}
		}}
	}`)
	ix := NewIndex()
	if err := ix.ParseAPI(raw); err != nil {
		t.Fatalf("ParseAPI: %v", err)
	}

	cases := []struct {
		id         string
		wantStatus string
	}{
		{"claude-old", "deprecated"},
		{"claude-preview", "beta"},
		{"claude-alpha", "alpha"},
		{"claude-current", ""},
	}
	for _, c := range cases {
		m, stage := ix.Lookup("anthropic", c.id)
		if stage != MatchExact {
			t.Fatalf("Lookup(%q) stage = %v, want MatchExact", c.id, stage)
		}
		if m.Status != c.wantStatus {
			t.Errorf("Lookup(%q).Status = %q, want %q", c.id, m.Status, c.wantStatus)
		}
	}

	current, _ := ix.Lookup("anthropic", "claude-current")
	if current.Temperature != nil {
		t.Errorf("claude-current.Temperature = %v, want nil (key absent)", *current.Temperature)
	}
	embed, _ := ix.Lookup("anthropic", "claude-embed")
	if embed.Temperature == nil || *embed.Temperature != false {
		t.Errorf("claude-embed.Temperature = %v, want a pointer to false", embed.Temperature)
	}
	chat, _ := ix.Lookup("anthropic", "claude-chat")
	if chat.Temperature == nil || *chat.Temperature != true {
		t.Errorf("claude-chat.Temperature = %v, want a pointer to true", chat.Temperature)
	}
}

// TestParseAPIModalitiesIsOutputNotInput pins the fix behind
// docs/DESIGN-model-curation.md §1.2's first curation signal: MDModel.Modalities
// must carry the WIRE's OUTPUT modalities, not input. A model that accepts
// image+text but only emits audio (a TTS shape) must report Modalities =
// ["audio"], never ["image","text"] — the input side is a completely
// different question (vision capability, handled separately by Caps.Vision)
// and conflating the two would make nonChat's own modality check useless.
func TestParseAPIModalitiesIsOutputNotInput(t *testing.T) {
	raw := []byte(`{
		"google": {"id":"google","name":"Google","models":{
			"gemini-tts": {
				"id": "gemini-tts",
				"modalities": {"input": ["text"], "output": ["audio"]}
			},
			"gemini-3.5-flash": {
				"id": "gemini-3.5-flash",
				"modalities": {"input": ["text","image","video","audio"], "output": ["text"]}
			}
		}}
	}`)
	ix := NewIndex()
	if err := ix.ParseAPI(raw); err != nil {
		t.Fatalf("ParseAPI: %v", err)
	}

	tts, _ := ix.Lookup("google", "gemini-tts")
	if len(tts.Modalities) != 1 || tts.Modalities[0] != "audio" {
		t.Errorf("gemini-tts.Modalities = %v, want [\"audio\"] (the output side, not input)", tts.Modalities)
	}

	flash, _ := ix.Lookup("google", "gemini-3.5-flash")
	if len(flash.Modalities) != 1 || flash.Modalities[0] != "text" {
		t.Errorf("gemini-3.5-flash.Modalities = %v, want [\"text\"]", flash.Modalities)
	}
	// Vision is still derived from the INPUT side, independently.
	if !flash.Vision {
		t.Errorf("gemini-3.5-flash.Vision = false, want true (input modalities include image/video)")
	}
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

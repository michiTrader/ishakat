package app

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/MichiTrader/ishakat/internal/catalog"
	"github.com/MichiTrader/ishakat/internal/config"
)

// TestUncuratedCatalogBypassesCuration is design doc §2.1's own closing
// promise for --all: a model curation would otherwise hide (here, via the
// provider's own glob-based Hide, the same knob TestRefreshCatalogAppliesCurationEndToEnd
// exercises) is still present in UncuratedCatalog's own output, and Layer 0
// (HideDeprecated) is bypassed too — no `catalog.hide_deprecated` note.
func TestUncuratedCatalogBypassesCuration(t *testing.T) {
	gw := newOmniRouteServer(t)
	cfg := catalogCfg(t, gw.URL, "")
	cfg.Providers[0].Hide = []string{"openai/gpt-5-nano"}

	snap, err := RefreshCatalog(t.Context(), cfg, "test", LoadCatalog(cfg))
	if err != nil {
		t.Fatalf("RefreshCatalog: %v", err)
	}
	if snap.Catalog.Has("omniroute/openai/gpt-5-nano") {
		t.Fatalf("setup: expected the glob to hide gpt-5-nano from the curated snapshot")
	}
	if len(snap.Hidden) == 0 {
		t.Fatalf("setup: expected snap.Hidden to record the hide")
	}

	full := UncuratedCatalog(cfg, snap)
	if !full.Has("omniroute/openai/gpt-5-nano") {
		t.Errorf("UncuratedCatalog dropped omniroute/openai/gpt-5-nano; refs = %v", full.Refs())
	}
	if !full.Has("omniroute/openai/gpt-5") {
		t.Errorf("UncuratedCatalog is missing an ordinarily-visible model; refs = %v", full.Refs())
	}
}

// TestUncuratedCatalogSeedFallback mirrors LoadCatalog's own "nothing at
// all -> embedded seed" rule (§4.4 case 3): a snapshot with an empty
// Cache/Index and no declared config models must still fall back to the
// seed catalog rather than returning an empty one.
func TestUncuratedCatalogSeedFallback(t *testing.T) {
	cfg := &config.Config{
		Schema: config.Schema,
		Providers: []config.Provider{{
			ID: "omniroute", Kind: "openai", Enabled: true, AuthOK: false, Discover: true,
		}},
	}
	snap := CatalogSnapshot{Cache: catalog.NewCache(""), Index: catalog.NewIndex()}
	got := UncuratedCatalog(cfg, snap)
	if got.Len() == 0 {
		t.Error("UncuratedCatalog returned an empty catalog instead of falling back to the seed")
	}
}

// TestWriteModelsHiddenListsTheAuditTrail checks --hidden's own output
// shape: every snap.Hidden entry, its reason, and the summary line —
// nothing pulled from snap.Catalog.
func TestWriteModelsHiddenListsTheAuditTrail(t *testing.T) {
	snap := CatalogSnapshot{
		Hidden: []catalog.Hidden{
			{Model: catalog.Model{Ref: "p/embed"}, Reason: catalog.ReasonNonChatLimit},
			{Model: catalog.Model{Ref: "p/old"}, Reason: catalog.ReasonDeprecated},
		},
	}
	var out, errw bytes.Buffer
	code := writeModelsHidden(&out, &errw, snap)
	if code != ExitOK {
		t.Fatalf("exit code = %d, want ExitOK", code)
	}
	text := out.String()
	for _, want := range []string{"p/embed", string(catalog.ReasonNonChatLimit), "p/old", string(catalog.ReasonDeprecated), "2 model(s) hidden"} {
		if !strings.Contains(text, want) {
			t.Errorf("output %q missing %q", text, want)
		}
	}
}

// TestWriteModelsHiddenEmpty is the "nothing hidden" case: a friendly
// message, never a bare empty stdout that reads like a bug.
func TestWriteModelsHiddenEmpty(t *testing.T) {
	var out, errw bytes.Buffer
	code := writeModelsHidden(&out, &errw, CatalogSnapshot{})
	if code != ExitOK {
		t.Fatalf("exit code = %d, want ExitOK", code)
	}
	if !strings.Contains(out.String(), "nothing hidden") {
		t.Errorf("output = %q, want an explicit \"nothing hidden\" message", out.String())
	}
}

// TestWriteModelWhyHiddenModel is design doc §2.1's own worked example,
// shape-checked: a model hidden by ReasonNonChatLimit whose Modalities DOES
// include "text" must get the "modality alone would not have caught it"
// note, plus the hidden-by/because/still-usable/to-show-it block.
func TestWriteModelWhyHiddenModel(t *testing.T) {
	cache := catalog.NewCache("")
	cache.SetProvider("gemini-direct", []catalog.DiscoveredModel{
		{WireID: "gemini-embedding-2", Output: 1},
	}, timeNowForTest())
	index := catalog.NewIndex()
	index.ByProvider["gemini-direct"] = map[string]catalog.MDModel{
		"gemini-embedding-2": {ID: "gemini-embedding-2", Modalities: []string{"text"}},
	}

	m := catalog.Model{
		Ref: "gemini-direct/gemini-embedding-2", Provider: "gemini-direct",
		WireID: "gemini-embedding-2", MaxOutput: 1, Modalities: []string{"text"},
		Source: catalog.SourceDiscover | catalog.SourceModelsDev,
	}
	snap := CatalogSnapshot{
		Catalog: catalog.Catalog{}, // hidden model is NOT in Catalog
		Cache:   cache,
		Index:   index,
		Hidden:  []catalog.Hidden{{Model: m, Reason: catalog.ReasonNonChatLimit}},
	}
	cfg := &config.Config{
		Schema: config.Schema,
		Providers: []config.Provider{{
			ID: "gemini-direct", Enabled: true, Discover: true,
		}},
	}

	var out, errw bytes.Buffer
	code := writeModelWhy(&out, &errw, cfg, snap, "gemini-embedding-2")
	if code != ExitOK {
		t.Fatalf("exit code = %d, want ExitOK; stderr = %q", code, errw.String())
	}
	text := out.String()
	for _, want := range []string{
		"gemini-direct/gemini-embedding-2",
		"discovered   yes",
		"models.dev   matched",
		"hidden by    catalog.curate.chat_only",
		"limit.output = 1",
		"modality IS text",
		"still usable yes — `/model gemini-direct/gemini-embedding-2` by exact ref",
		`to show it   add "gemini-embedding-2" to [catalog.curate].keep`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output missing %q; full output:\n%s", want, text)
		}
	}
}

// TestWriteModelWhyVisibleModel is the "not hidden" branch: still a clean,
// answerable diagnostic rather than an error.
func TestWriteModelWhyVisibleModel(t *testing.T) {
	cache := catalog.NewCache("")
	cache.SetProvider("p", []catalog.DiscoveredModel{{WireID: "chat"}}, timeNowForTest())
	m := catalog.Model{Ref: "p/chat", Provider: "p", WireID: "chat", Source: catalog.SourceDiscover}
	snap := CatalogSnapshot{
		Catalog: catalog.Catalog{Models: []catalog.Model{m}},
		Cache:   cache,
		Index:   catalog.NewIndex(),
	}
	cfg := &config.Config{
		Schema:    config.Schema,
		Providers: []config.Provider{{ID: "p", Enabled: true, Discover: true}},
	}
	var out, errw bytes.Buffer
	code := writeModelWhy(&out, &errw, cfg, snap, "p/chat")
	if code != ExitOK {
		t.Fatalf("exit code = %d, want ExitOK; stderr = %q", code, errw.String())
	}
	if !strings.Contains(out.String(), "not hidden") {
		t.Errorf("output = %q, want a \"not hidden\" line", out.String())
	}
}

// timeNowForTest is a small indirection so the fixture setup above reads as
// "when this was discovered" without pulling in a whole clock seam just for
// two test cache entries.
func timeNowForTest() time.Time { return time.Now() }

// TestWriteModelWhyNoMatch is the "nothing resolves" case: an error exit
// and a clear stderr message, never a panic or an empty success.
func TestWriteModelWhyNoMatch(t *testing.T) {
	snap := CatalogSnapshot{Cache: catalog.NewCache(""), Index: catalog.NewIndex()}
	cfg := &config.Config{Schema: config.Schema}
	var out, errw bytes.Buffer
	code := writeModelWhy(&out, &errw, cfg, snap, "definitely-not-a-model")
	if code != ExitError {
		t.Fatalf("exit code = %d, want ExitError", code)
	}
	if !strings.Contains(errw.String(), "no model matches") {
		t.Errorf("stderr = %q, want a \"no model matches\" message", errw.String())
	}
}

// TestModelsDispatchPrecedence exercises Models()'s own Why > Hidden > All
// precedence through the public entry point, using ModelsOptions.Config to
// avoid touching the real XDG config path.
func TestModelsDispatchPrecedence(t *testing.T) {
	gw := newOmniRouteServer(t)
	cfg := catalogCfg(t, gw.URL, "")
	cfg.Providers[0].Hide = []string{"openai/gpt-5-nano"}

	// Prime the cache so Models()'s own LoadCatalog (no --refresh) sees the
	// hide reflected in snap.Hidden, mirroring how a real run would have a
	// warm cache from a previous `--refresh`.
	if _, err := RefreshCatalog(t.Context(), cfg, "test", LoadCatalog(cfg)); err != nil {
		t.Fatalf("priming RefreshCatalog: %v", err)
	}

	t.Run("--why wins over --hidden and --all", func(t *testing.T) {
		var out, errw bytes.Buffer
		code := Models(ModelsOptions{
			Config: cfg, Stdout: &out, Stderr: &errw,
			Why: "gpt-5-nano", Hidden: true, All: true,
		})
		if code != ExitOK {
			t.Fatalf("exit = %d, stderr = %q", code, errw.String())
		}
		if !strings.Contains(out.String(), "gpt-5-nano") {
			t.Errorf("expected the --why diagnostic, got %q", out.String())
		}
		if strings.Contains(out.String(), "model(s) hidden ·") {
			t.Errorf("--why output looks like --hidden's own listing: %q", out.String())
		}
	})

	t.Run("--hidden wins over --all", func(t *testing.T) {
		var out, errw bytes.Buffer
		code := Models(ModelsOptions{
			Config: cfg, Stdout: &out, Stderr: &errw,
			Hidden: true, All: true,
		})
		if code != ExitOK {
			t.Fatalf("exit = %d, stderr = %q", code, errw.String())
		}
		if !strings.Contains(out.String(), "gpt-5-nano") {
			t.Errorf("expected --hidden's audit trail to mention gpt-5-nano, got %q", out.String())
		}
	})

	t.Run("--all shows the model --hidden would otherwise report as hidden", func(t *testing.T) {
		var out, errw bytes.Buffer
		code := Models(ModelsOptions{Config: cfg, Stdout: &out, Stderr: &errw, All: true})
		if code != ExitOK {
			t.Fatalf("exit = %d, stderr = %q", code, errw.String())
		}
		if !strings.Contains(out.String(), "gpt-5-nano") {
			t.Errorf("--all output missing gpt-5-nano: %q", out.String())
		}
	})

	t.Run("plain (no flags) hides gpt-5-nano", func(t *testing.T) {
		var out, errw bytes.Buffer
		code := Models(ModelsOptions{Config: cfg, Stdout: &out, Stderr: &errw})
		if code != ExitOK {
			t.Fatalf("exit = %d, stderr = %q", code, errw.String())
		}
		if strings.Contains(out.String(), "gpt-5-nano") {
			t.Errorf("plain listing should not show the curated-away model: %q", out.String())
		}
	})
}

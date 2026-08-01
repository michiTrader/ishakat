package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichiTrader/ishakat/internal/catalog"
	"github.com/MichiTrader/ishakat/internal/config"
)

// The closing tests of Step 6. Three promises, in this order:
//
//  1. The real OmniRoute /models fixture, merged with models.dev, produces
//     the expected catalog.
//  2. A cold start with the network off returns the cache without blocking
//     on anything — the non-negotiable budget of §4.4.
//  3. With no cache and no network, the embedded seed is what shows up.

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	return raw
}

// omniRouteServer serves the real GET /models fixture and counts the hits,
// so a test can assert that startup did *not* call it.
type omniRouteServer struct {
	*httptest.Server
	Hits *atomic.Int64
}

func newOmniRouteServer(t *testing.T) omniRouteServer {
	t.Helper()
	body := readFixture(t, "models_omniroute.json")
	hits := &atomic.Int64{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/models") {
			http.NotFound(w, r)
			return
		}
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return omniRouteServer{Server: srv, Hits: hits}
}

// modelsDevServer serves the two trimmed documents with strong ETags and
// honors If-None-Match, which is the whole point of §4.4's cheap refresh.
type modelsDevServer struct {
	*httptest.Server
	APIHits  *atomic.Int64
	MetaHits *atomic.Int64
	NotMod   *atomic.Int64
}

func newModelsDevServer(t *testing.T) modelsDevServer {
	t.Helper()
	api := readFixture(t, "modelsdev_api.json")
	meta := readFixture(t, "modelsdev_models.json")

	s := modelsDevServer{APIHits: &atomic.Int64{}, MetaHits: &atomic.Int64{}, NotMod: &atomic.Int64{}}
	serve := func(w http.ResponseWriter, r *http.Request, body []byte, etag string, hits *atomic.Int64) {
		hits.Add(1)
		w.Header().Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			s.NotMod.Add(1)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api.json":
			serve(w, r, api, `"api-v1"`, s.APIHits)
		case "/models.json":
			serve(w, r, meta, `"meta-v1"`, s.MetaHits)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Server.Close)
	return s
}

// catalogCfg is a configuration with one OmniRoute-shaped provider and a
// cache inside the test's own temporary directory: nothing here ever touches
// the user's real XDG paths.
func catalogCfg(t *testing.T, providerURL, modelsDevURL string) *config.Config {
	t.Helper()
	cache := filepath.Join(t.TempDir(), "catalog.json")
	cfg := &config.Config{
		Schema:  config.Schema,
		App:     config.App{TimeoutS: 5, ConnectTimeoutS: 2},
		Session: config.Session{Save: false, Dir: t.TempDir()},
		Catalog: config.Catalog{CacheFile: cache, TTLHours: 24},
		Providers: []config.Provider{{
			ID: "omniroute", Kind: "openai", BaseURL: providerURL,
			APIKey: "test-key", Enabled: true, AuthOK: true, Discover: true,
		}},
	}
	if modelsDevURL == "" {
		// No metadata source: the tests that only care about discovery ask
		// for the provider alone instead of hitting the real models.dev.
		cfg.Catalog.Sources = []string{"provider", "config"}
	} else {
		cfg.Catalog.ModelsDevURL = modelsDevURL + "/api.json"
		cfg.Catalog.ModelsDevMetaURL = modelsDevURL + "/models.json"
	}
	return cfg
}

func mustGet(t *testing.T, c catalog.Catalog, ref string) catalog.Model {
	t.Helper()
	m, ok := c.Get(ref)
	if !ok {
		t.Fatalf("model %q missing; refs = %v", ref, c.Refs())
	}
	return m
}

// TestRefreshWithTheRealOmniRouteFixture is the closing criterion of Step 6:
// the fixture that came off the real gateway, plus models.dev, produces the
// expected catalog — including the three different names OmniRoute uses for
// the context window and the entry with no id, which is not a model.
func TestRefreshWithTheRealOmniRouteFixture(t *testing.T) {
	gw := newOmniRouteServer(t)
	md := newModelsDevServer(t)
	cfg := catalogCfg(t, gw.URL, md.URL)

	snap, err := RefreshCatalog(context.Background(), cfg, "test", LoadCatalog(cfg))
	if err != nil {
		t.Fatalf("RefreshCatalog: %v", err)
	}
	cat := snap.Catalog

	// Four callable models: the fifth fixture entry has no id.
	want := []string{
		"omniroute/anthropic/claude-sonnet-4-5",
		"omniroute/openai/gpt-5",
		"omniroute/openai/gpt-5-nano",
		"omniroute/meta/llama-3.3-70b",
	}
	if cat.Len() != len(want) {
		t.Fatalf("Len = %d, want %d; refs = %v", cat.Len(), len(want), cat.Refs())
	}
	for _, ref := range want {
		mustGet(t, cat, ref)
	}

	t.Run("gateway metadata is read as sent", func(t *testing.T) {
		son := mustGet(t, cat, "omniroute/anthropic/claude-sonnet-4-5")
		if son.WireID != "anthropic/claude-sonnet-4-5" || son.Provider != "omniroute" {
			t.Errorf("bad split: provider=%q wire=%q", son.Provider, son.WireID)
		}
		if son.Context != 200_000 || son.MaxOutput != 64_000 {
			t.Errorf("context/output = %d/%d, want 200000/64000 (context_length)", son.Context, son.MaxOutput)
		}
		// USD per token on the wire, USD per million in the catalog.
		if son.Cost == nil || son.Cost.In != 3 || son.Cost.Out != 15 {
			t.Errorf("Cost = %+v, want in=3 out=15 per million", son.Cost)
		}
		if son.Family != "anthropic" && son.Family != "claude" {
			t.Errorf("Family = %q, want it grouped under the vendor or the family", son.Family)
		}

		gpt5 := mustGet(t, cat, "omniroute/openai/gpt-5")
		if gpt5.Context != 400_000 || gpt5.MaxOutput != 128_000 {
			t.Errorf("context/output = %d/%d, want 400000/128000 (context_window)", gpt5.Context, gpt5.MaxOutput)
		}
		llama := mustGet(t, cat, "omniroute/meta/llama-3.3-70b")
		if llama.Context != 131_072 {
			t.Errorf("Context = %d, want 131072 (max_context_tokens)", llama.Context)
		}
	})

	t.Run("models.dev fills the holes through the cascade", func(t *testing.T) {
		// gpt-5-nano arrives with no name and no price; the api.json entry
		// is reached by stripping the vendor prefix off the wire id.
		nano := mustGet(t, cat, "omniroute/openai/gpt-5-nano")
		if nano.Name != "GPT-5 Nano" {
			t.Errorf("Name = %q, want the models.dev name", nano.Name)
		}
		if nano.Cost == nil || nano.Cost.In != 0.05 || nano.Cost.Out != 0.4 {
			t.Errorf("Cost = %+v, want in=0.05 out=0.4 from models.dev", nano.Cost)
		}
		if !nano.Source.Has(catalog.SourceModelsDev) {
			t.Errorf("Source = %s, want the modelsdev bit", nano.Source)
		}

		// llama is not in api.json at all: it is only in the agnostic base,
		// which is exactly the rung of the cascade that exists for gateways.
		llama := mustGet(t, cat, "omniroute/meta/llama-3.3-70b")
		if llama.Name != "Llama 3.3 70B" {
			t.Errorf("Name = %q, want the models.json name", llama.Name)
		}
		if !llama.Caps.Tools {
			t.Errorf("Caps = %+v, want tool support from models.json", llama.Caps)
		}

		// The gateway reported a price for sonnet, so models.dev must not
		// have replaced it (the per-million numbers happen to agree; the
		// cache_read field only models.dev knows is the tell).
		son := mustGet(t, cat, "omniroute/anthropic/claude-sonnet-4-5")
		if son.Cost.CacheRead != 0 {
			t.Errorf("Cost = %+v, want the gateway's block kept whole", son.Cost)
		}
	})

	t.Run("nothing claims to be free without a price", func(t *testing.T) {
		for _, m := range cat.Models {
			if m.Cost == nil && m.Free() {
				t.Errorf("%q: unknown cost reported as free", m.Ref)
			}
		}
	})

	t.Run("the cache is written and reloads identically", func(t *testing.T) {
		if _, err := os.Stat(snap.CachePath); err != nil {
			t.Fatalf("cache not written: %v", err)
		}
		if _, err := os.Stat(snap.DigestPath); err != nil {
			t.Fatalf("models.dev digest not written: %v", err)
		}
		again := LoadCatalog(cfg)
		if again.Catalog.Len() != cat.Len() {
			t.Errorf("reloaded Len = %d, want %d", again.Catalog.Len(), cat.Len())
		}
		if again.Expired {
			t.Error("a cache written seconds ago reports as expired")
		}
		nano := mustGet(t, again.Catalog, "omniroute/openai/gpt-5-nano")
		if nano.Name != "GPT-5 Nano" {
			t.Errorf("Name = %q after reload, the digest did not survive", nano.Name)
		}
	})

	t.Run("the second refresh is conditional", func(t *testing.T) {
		before := md.NotMod.Load()
		if _, err := RefreshCatalog(context.Background(), cfg, "test", LoadCatalog(cfg)); err != nil {
			t.Fatalf("second RefreshCatalog: %v", err)
		}
		if md.NotMod.Load() <= before {
			t.Error("no 304 on the second pass: If-None-Match is not being sent")
		}
	})
}

// TestStartupNeverTouchesTheNetwork is the budget of §4.4 written as a test:
// the provider points at a server that never answers, and LoadCatalog still
// returns immediately, with the cached models.
func TestStartupNeverTouchesTheNetwork(t *testing.T) {
	blocked := make(chan struct{})
	t.Cleanup(func() { close(blocked) })
	hits := &atomic.Int64{}
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		<-blocked // a provider that hangs forever
	}))
	t.Cleanup(slow.Close)

	cfg := catalogCfg(t, slow.URL, "")

	// Seed the cache with something a previous run would have written.
	pre := catalog.NewCache(CatalogCachePath(cfg))
	pre.SetProvider("omniroute", []catalog.DiscoveredModel{
		{WireID: "anthropic/claude-sonnet-4-5", Name: "Claude Sonnet 4.5", Context: 200_000},
		{WireID: "openai/gpt-5", Name: "GPT-5", Context: 400_000},
	}, time.Now())
	pre.FetchedAt = time.Now()
	if err := pre.Save(""); err != nil {
		t.Fatalf("preparing the cache: %v", err)
	}

	start := time.Now()
	snap := LoadCatalog(cfg)
	elapsed := time.Since(start)

	if elapsed > 500*time.Millisecond {
		t.Errorf("LoadCatalog took %v: something on the startup path is doing I/O over the network", elapsed)
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("the provider was called %d times during startup; the budget of §4.4 says zero", n)
	}
	if snap.Catalog.Len() != 2 {
		t.Fatalf("Len = %d, want the 2 cached models; refs = %v", snap.Catalog.Len(), snap.Catalog.Refs())
	}
	if snap.Catalog.Seeded {
		t.Error("a usable cache must not fall back to the seed")
	}
	mustGet(t, snap.Catalog, "omniroute/openai/gpt-5")
}

// TestExpiredCacheIsStillPaintedFirst: §4.4 is explicit that an expired
// cache is shown immediately, with a staleness note, and the refresh happens
// afterwards and off the critical path.
func TestExpiredCacheIsStillPaintedFirst(t *testing.T) {
	cfg := catalogCfg(t, "http://127.0.0.1:1", "")
	cfg.Catalog.TTLHours = 24

	old := time.Now().Add(-72 * time.Hour)
	pre := catalog.NewCache(CatalogCachePath(cfg))
	pre.SetProvider("omniroute", []catalog.DiscoveredModel{{WireID: "openai/gpt-5"}}, old)
	pre.FetchedAt = old
	if err := pre.Save(""); err != nil {
		t.Fatalf("preparing the cache: %v", err)
	}

	snap := LoadCatalog(cfg)
	if !snap.Expired {
		t.Error("Expired = false on a three-day-old cache with a 24 h TTL")
	}
	if snap.Catalog.Len() != 1 {
		t.Fatalf("Len = %d, want the expired cache used anyway", snap.Catalog.Len())
	}
	if !snap.Catalog.Stale {
		t.Error("Stale = false: the interface would show old data as fresh")
	}
	notes := strings.Join(snap.Catalog.Notes, "\n")
	if !strings.Contains(notes, "3 days") {
		t.Errorf("Notes = %q, want the age strip of §4.4", notes)
	}
}

// TestRefreshModeStartupAndManual: the two special values of
// [catalog].refresh are expressed through the TTL, with no second flag.
func TestRefreshModeStartupAndManual(t *testing.T) {
	for _, tc := range []struct {
		mode        string
		wantExpired bool
	}{
		{"startup", true},
		{"manual", false},
		{"", false},
	} {
		t.Run("refresh="+tc.mode, func(t *testing.T) {
			cfg := catalogCfg(t, "http://127.0.0.1:1", "")
			cfg.Catalog.Refresh = tc.mode
			pre := catalog.NewCache(CatalogCachePath(cfg))
			pre.SetProvider("omniroute", []catalog.DiscoveredModel{{WireID: "m"}}, time.Now())
			pre.FetchedAt = time.Now()
			if err := pre.Save(""); err != nil {
				t.Fatalf("preparing the cache: %v", err)
			}
			if got := LoadCatalog(cfg).Expired; got != tc.wantExpired {
				t.Errorf("Expired = %v, want %v", got, tc.wantExpired)
			}
		})
	}
}

// TestNoCacheNoNetworkFallsBackToTheSeed is the third promise: a first run
// on a phone with no signal shows a usable list, marked as unverified.
func TestNoCacheNoNetworkFallsBackToTheSeed(t *testing.T) {
	cfg := catalogCfg(t, "http://127.0.0.1:1", "")

	snap := LoadCatalog(cfg)
	if snap.Catalog.Len() == 0 {
		t.Fatal("first run with no cache and no network shows an empty list")
	}
	if !snap.Catalog.Seeded {
		t.Error("Seeded = false: the interface would present the seed as real data")
	}
	if !snap.Expired {
		t.Error("Expired = false with no cache at all")
	}
	for _, m := range snap.Catalog.Models {
		if !m.Source.Has(catalog.SourceSeed) {
			t.Errorf("%q: Source = %s, want the seed bit", m.Ref, m.Source)
		}
	}
	// The virtual models are the reason the seed exists.
	mustGet(t, snap.Catalog, "omniroute/auto/coding")
}

// TestDeclaredModelsBeatTheSeed: a user with [[provider.model]] entries has
// already told us what they can call, so the seed is not needed.
func TestDeclaredModelsBeatTheSeed(t *testing.T) {
	cfg := catalogCfg(t, "http://127.0.0.1:1", "")
	cfg.Providers[0].Models = []config.ProviderModel{{ID: "private/mine", Name: "Mine", Context: 8000}}

	snap := LoadCatalog(cfg)
	if snap.Catalog.Seeded {
		t.Error("Seeded = true even though the configuration declared models")
	}
	m := mustGet(t, snap.Catalog, "omniroute/private/mine")
	if m.Name != "Mine" || m.Context != 8000 {
		t.Errorf("declared fields lost: %+v", m)
	}
}

// TestCorruptCacheDoesNotBreakStartup: the cache is a convenience, never a
// dependency. A truncated file degrades to the seed with a note, and the
// program still starts.
func TestCorruptCacheDoesNotBreakStartup(t *testing.T) {
	cfg := catalogCfg(t, "http://127.0.0.1:1", "")
	path := CatalogCachePath(cfg)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"v":1,"providers":{"omni`), 0o600); err != nil {
		t.Fatal(err)
	}

	snap := LoadCatalog(cfg)
	if snap.Catalog.Len() == 0 {
		t.Fatal("a corrupt cache left the user with nothing")
	}
	if !strings.Contains(strings.Join(snap.Catalog.Notes, "\n"), "corrupt") {
		t.Errorf("Notes = %v, want the corruption reported, not swallowed", snap.Catalog.Notes)
	}
}

// TestRefreshKeepsCachedModelsWhenTheProviderIsDown: a refresh that fails
// must leave the user exactly where they were, not with an empty list.
func TestRefreshKeepsCachedModelsWhenTheProviderIsDown(t *testing.T) {
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"upstream is on fire"}}`, http.StatusInternalServerError)
	}))
	t.Cleanup(down.Close)

	cfg := catalogCfg(t, down.URL, "")
	pre := catalog.NewCache(CatalogCachePath(cfg))
	pre.SetProvider("omniroute", []catalog.DiscoveredModel{{WireID: "openai/gpt-5", Name: "GPT-5"}}, time.Now())
	pre.FetchedAt = time.Now().Add(-48 * time.Hour)
	if err := pre.Save(""); err != nil {
		t.Fatalf("preparing the cache: %v", err)
	}

	snap, err := RefreshCatalog(context.Background(), cfg, "test", LoadCatalog(cfg))
	if err == nil {
		t.Error("RefreshCatalog returned no error even though the provider answered 500")
	}
	if snap.Catalog.Len() != 1 {
		t.Fatalf("Len = %d, want the cached model kept; refs = %v", snap.Catalog.Len(), snap.Catalog.Refs())
	}
	m := mustGet(t, snap.Catalog, "omniroute/openai/gpt-5")
	if m.Health != catalog.HealthUnreachable {
		t.Errorf("Health = %v, want unreachable", m.Health)
	}
	if !strings.Contains(strings.Join(snap.Catalog.Notes, "\n"), "could not list the models") {
		t.Errorf("Notes = %v, want the failure reported", snap.Catalog.Notes)
	}
}

// TestSourcesFilterIsHonored: [catalog].sources turns a whole source off,
// and the merge has to notice.
func TestSourcesFilterIsHonored(t *testing.T) {
	gw := newOmniRouteServer(t)
	md := newModelsDevServer(t)

	t.Run("modelsdev off", func(t *testing.T) {
		cfg := catalogCfg(t, gw.URL, md.URL)
		cfg.Catalog.Sources = []string{"provider"}
		snap, err := RefreshCatalog(context.Background(), cfg, "test", LoadCatalog(cfg))
		if err != nil {
			t.Fatalf("RefreshCatalog: %v", err)
		}
		if md.APIHits.Load() != 0 {
			t.Error("models.dev was fetched even though the source is off")
		}
		nano := mustGet(t, snap.Catalog, "omniroute/openai/gpt-5-nano")
		if nano.Source.Has(catalog.SourceModelsDev) {
			t.Errorf("Source = %s, want no modelsdev bit", nano.Source)
		}
	})

	t.Run("provider discovery off", func(t *testing.T) {
		cfg := catalogCfg(t, gw.URL, "")
		cfg.Catalog.Sources = []string{"config"}
		cfg.Providers[0].Models = []config.ProviderModel{{ID: "auto/coding", Name: "Auto · Coding"}}
		before := gw.Hits.Load()
		snap, err := RefreshCatalog(context.Background(), cfg, "test", LoadCatalog(cfg))
		if err != nil {
			t.Fatalf("RefreshCatalog: %v", err)
		}
		if gw.Hits.Load() != before {
			t.Error("the provider was interrogated even though discovery is off")
		}
		if snap.Catalog.Len() != 1 {
			t.Fatalf("Len = %d, want only the declared model; refs = %v", snap.Catalog.Len(), snap.Catalog.Refs())
		}
	})
}

// TestRecordModelUseFeedsTheRanking: the local statistics of §4.5 survive a
// round trip through the cache file, and a failure to write is never a
// visible error.
func TestRecordModelUseFeedsTheRanking(t *testing.T) {
	cfg := catalogCfg(t, "http://127.0.0.1:1", "")
	RecordModelUse(cfg, "omniroute/auto/coding", 820*time.Millisecond)
	RecordModelUse(cfg, "omniroute/auto/coding", 900*time.Millisecond)

	c := catalog.LoadCache(CatalogCachePath(cfg))
	st := c.Stat("omniroute/auto/coding")
	if st.UseCount != 2 {
		t.Errorf("UseCount = %d, want 2", st.UseCount)
	}
	if st.P50ms == 0 {
		t.Error("P50ms = 0, the latency was not recorded")
	}
	if st.LastUsed.IsZero() {
		t.Error("LastUsed is zero")
	}

	// An empty reference is a no-op, not a panic and not a write.
	RecordModelUse(cfg, "   ", time.Second)

	// An unwritable path must stay silent: this feeds a tiebreaker, not the
	// conversation.
	broken := *cfg
	broken.Catalog.CacheFile = filepath.Join(t.TempDir(), "not-a-dir", "\x00", "catalog.json")
	RecordModelUse(&broken, "omniroute/auto/coding", time.Second)
}

// TestCacheFileIsPrivate: the cache holds no secrets today, but it sits next
// to files that do, and 0600 is the habit worth keeping.
func TestCacheFileIsPrivate(t *testing.T) {
	gw := newOmniRouteServer(t)
	cfg := catalogCfg(t, gw.URL, "")
	if _, err := RefreshCatalog(context.Background(), cfg, "test", LoadCatalog(cfg)); err != nil {
		t.Fatalf("RefreshCatalog: %v", err)
	}
	fi, err := os.Stat(CatalogCachePath(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("cache permissions = %o, want 600", perm)
	}
}

// TestCacheIsValidJSONAfterRefresh guards the shape documented in §4.4: one
// file, version 1, providers keyed by id.
func TestCacheIsValidJSONAfterRefresh(t *testing.T) {
	gw := newOmniRouteServer(t)
	cfg := catalogCfg(t, gw.URL, "")
	if _, err := RefreshCatalog(context.Background(), cfg, "test", LoadCatalog(cfg)); err != nil {
		t.Fatalf("RefreshCatalog: %v", err)
	}

	raw, err := os.ReadFile(CatalogCachePath(cfg))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		V         int    `json:"v"`
		FetchedAt string `json:"fetched_at"`
		Providers map[string]struct {
			OK     bool `json:"ok"`
			Models []struct {
				WireID string `json:"wire_id"`
			} `json:"models"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("the cache is not valid JSON: %v", err)
	}
	if doc.V != catalog.CacheVersion {
		t.Errorf("v = %d, want %d", doc.V, catalog.CacheVersion)
	}
	if doc.FetchedAt == "" {
		t.Error("fetched_at missing: the staleness strip has nothing to read")
	}
	omni, ok := doc.Providers["omniroute"]
	if !ok {
		t.Fatalf("providers = %v, want an omniroute entry", doc.Providers)
	}
	if !omni.OK || len(omni.Models) != 4 {
		t.Errorf("ok = %v with %d models, want true with 4", omni.OK, len(omni.Models))
	}
}

// TestHumanAge is what the staleness strip actually prints.
func TestHumanAge(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{72 * time.Hour, "3 days"},
		{5 * time.Hour, "5 hours"},
		{12 * time.Minute, "12 minutes"},
		{30 * time.Second, "moments"},
	}
	for _, tc := range cases {
		if got := humanAge(tc.in); got != tc.want {
			t.Errorf("humanAge(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

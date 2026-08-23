package app

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/MichiTrader/ishakat/internal/catalog"
	"github.com/MichiTrader/ishakat/internal/catalog/fetch"
	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/curation"
	"github.com/MichiTrader/ishakat/internal/xdg"
)

// catalog.go is the wiring of Step 6: it turns the configuration into the
// catalog's inputs and enforces the startup sequence of §4.4.
//
// THE NON-NEGOTIABLE BUDGET: startup does not touch the network on the
// critical path. LoadCatalog only reads files —cache, digest, embedded
// seed— and always returns something usable, even with an expired cache,
// even with no cache at all. RefreshCatalog is the one that goes out to the
// network, and nobody is allowed to call it before the interface is drawn.

// CatalogSnapshot is the catalog plus the state needed to refresh it. It
// travels as one value so the caller does not have to keep four related
// things in sync.
type CatalogSnapshot struct {
	Catalog catalog.Catalog
	Cache   *catalog.Cache
	Index   *catalog.Index

	// CachePath and DigestPath are where the two files live.
	CachePath  string
	DigestPath string

	// Expired says whether the TTL elapsed, which is what decides if a
	// background refresh is worth launching.
	Expired bool

	// Hidden is applyCuration's own audit trail (design doc §2.1's CLI
	// table: `ishakat models --hidden`/`--why`): every model catalog.Curate
	// removed from Catalog on this snapshot, and why. It is the reporting
	// twin of Catalog itself — Catalog is "what to show", Hidden is "what
	// was NOT shown and the reason", and neither can be derived from the
	// other once applyCuration has already run (Catalog.Models no longer
	// contains these refs at all — principle 1's "hiding is a view, never
	// a deletion" is about the CATALOG the rest of the program reads, not
	// about this audit trail, which exists precisely so nothing is lost).
	// Nil whenever applyCuration hid nothing, or was skipped entirely (the
	// "would hide every model" guard) — never a reason on its own to
	// treat the snapshot as broken.
	Hidden []catalog.Hidden
}

// CatalogCachePath resolves [catalog].cache_file, falling back to the XDG
// location. The configuration value has already been through expand.go, so
// $XDG_CACHE_HOME is a real path by the time it gets here.
func CatalogCachePath(cfg *config.Config) string {
	if cfg != nil {
		if p := strings.TrimSpace(cfg.Catalog.CacheFile); p != "" {
			return p
		}
	}
	return xdg.CatalogFile()
}

// catalogTTL is [catalog].ttl_h, with the refresh mode folded in: "startup"
// means the cache is always considered expired, "manual" means it never is.
func catalogTTL(cfg *config.Config) time.Duration {
	if cfg == nil {
		return 24 * time.Hour
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Catalog.Refresh)) {
	case "startup":
		return 0 // always expired
	case "manual":
		return 100 * 365 * 24 * time.Hour // never, until `ishakat models --refresh`
	}
	h := cfg.Catalog.TTLHours
	if h <= 0 {
		h = 24
	}
	return time.Duration(h) * time.Hour
}

// wantsSource reports whether a source is enabled in [catalog].sources. An
// empty list means all three, which is what a user who never touched the
// section expects.
func wantsSource(cfg *config.Config, name string) bool {
	if cfg == nil || len(cfg.Catalog.Sources) == 0 {
		return true
	}
	for _, s := range cfg.Catalog.Sources {
		if strings.EqualFold(strings.TrimSpace(s), name) {
			return true
		}
	}
	return false
}

// providerInputs turns the configuration plus whatever the cache holds into
// the merge inputs. Disabled providers are left out entirely: a provider the
// user turned off should not appear in the picker.
func providerInputs(cfg *config.Config, cache *catalog.Cache) []catalog.ProviderInput {
	useConfigModels := wantsSource(cfg, "config")
	useDiscovery := wantsSource(cfg, "provider")

	var out []catalog.ProviderInput
	for _, p := range EnabledProviders(cfg) {
		in := catalog.ProviderInput{
			ID:       p.ID,
			Name:     p.Name,
			Enabled:  p.Enabled,
			AuthOK:   p.AuthOK,
			Discover: p.Discover && useDiscovery,
		}
		if useConfigModels {
			for _, m := range p.Models {
				in.Declared = append(in.Declared, catalog.DeclaredModel{
					WireID:  m.ID,
					Name:    m.Name,
					Context: m.Context,
					Output:  m.Output,
					Tags:    m.Tags,
				})
			}
		}
		if pc, ok := cache.Provider(p.ID); ok {
			in.Discovered = pc.Models
			in.DiscoverOK = pc.OK
			in.DiscoverErr = pc.Error
			in.FetchedAt = pc.FetchedAt
		}
		out = append(out, in)
	}
	return out
}

// curationRules turns [catalog.curate] (and each provider's own hide/keep)
// into a catalog.Rules, the input Curate needs (docs/DESIGN-model-curation.md
// Layer 1). A nil cfg returns the zero value, which Curate treats as "show
// everything" — never a reason to hide anything just because the caller
// forgot to pass a configuration.
//
// HideDeprecated is read from cfg.Catalog.Curate.HideDeprecated OR the
// top-level cfg.Catalog.HideDeprecated (design doc §1.3: the flag "moved
// here... kept as an alias one release" — a config.toml written before
// [catalog.curate] existed must keep behaving the same way).
//
// KeepRefs/HideRefs come from curation.json (Layer 2's own persisted,
// per-model decisions — a ctrl+x/ctrl+h in the picker, or /model hide|keep),
// loaded here rather than threaded through as a parameter: LoadCatalog and
// RefreshCatalog already resolve every other on-disk input (cache path,
// digest) internally without the caller injecting them, and curation.Load
// degrades to an empty, unhidden Store on any missing/corrupt/wrong-version
// file — never an error worth failing curationRules(nil)'s own "no cfg, no
// opinions" contract over, and never worth changing this function's
// signature (and its two existing curationRules(nil)/curationRules(cfg)
// tests) for.
func curationRules(cfg *config.Config) catalog.Rules {
	if cfg == nil {
		return catalog.Rules{}
	}
	c := cfg.Catalog.Curate
	r := catalog.Rules{
		ChatOnly:       c.ChatOnly,
		HideDeprecated: c.HideDeprecated || cfg.Catalog.HideDeprecated,
		HideSuperseded: c.HideSuperseded,
		HideDatedTwins: c.HideDatedTwins,
		HideLatest:     c.HideLatest,
		Hide:           c.Hide,
		Keep:           c.Keep,
	}
	for _, p := range cfg.Providers {
		if len(p.Hide) == 0 && len(p.Keep) == 0 {
			continue
		}
		if r.Providers == nil {
			r.Providers = make(map[string]catalog.ProviderRules, len(cfg.Providers))
		}
		r.Providers[p.ID] = catalog.ProviderRules{Hide: p.Hide, Keep: p.Keep}
	}
	if cur, err := curation.Load(xdg.CurationFile()); err == nil && cur != nil {
		for _, e := range cur.Kept {
			r.KeepRefs = append(r.KeepRefs, e.Ref)
		}
		for _, e := range cur.Hidden {
			r.HideRefs = append(r.HideRefs, e.Ref)
		}
	}
	return r
}

// applyCuration runs catalog.Curate over a freshly built snapshot and notes
// the count, mirroring merge.go's own "N deprecated model(s) hidden" note
// (principle 2, design doc §2: "say the number", never a silent filter).
//
// Guard against the degenerate case a misconfigured [catalog.curate] could
// produce: if curation would hide EVERY model, that is treated as "curation
// found nothing sane to show" and skipped entirely, with a note explaining
// why — an empty picker is a worse failure than a noisy one, and nothing in
// [catalog.curate] should be able to brick the model list.
//
// The second return value is CatalogSnapshot.Hidden's own source: every
// model actually removed, and why (nil whenever nothing was hidden, or the
// "would hide every model" guard fired — in both cases nothing was
// removed, so there is nothing to report). This is what makes `ishakat
// models --hidden`/`--why` possible without CatalogSnapshot.Catalog itself
// having to carry the models it no longer contains.
func applyCuration(cat catalog.Catalog, r catalog.Rules) (catalog.Catalog, []catalog.Hidden) {
	if cat.Len() == 0 {
		return cat, nil
	}
	kept, hidden := catalog.Curate(cat, r)
	if len(hidden) == 0 {
		return cat, nil
	}
	if kept.Len() == 0 {
		cat.Note("catalog.curate would hide every model; showing the uncurated list instead")
		return cat, nil
	}
	kept.Note(strconv.Itoa(len(hidden)) + " model(s) hidden by catalog.curate")
	return kept, hidden
}

// LoadCatalog builds the snapshot from disk. It NEVER touches the network
// and it never fails: the worst case is the embedded seed.
//
// The three cases of §4.4, in order:
//
//  1. Usable cache (even expired) → the interface is painted with it and a
//     staleness note is attached.
//  2. No cache but declared models in the config → those are shown; the
//     virtual OmniRoute models are exactly this case.
//  3. Nothing at all → the embedded seed, marked as such.
func LoadCatalog(cfg *config.Config) CatalogSnapshot {
	now := time.Now()
	cachePath := CatalogCachePath(cfg)
	digestPath := catalog.DigestPath(cachePath)

	cache := catalog.LoadCache(cachePath)
	index := catalog.NewIndex()
	if wantsSource(cfg, "modelsdev") {
		index = catalog.LoadDigest(digestPath)
	}

	snap := CatalogSnapshot{
		Cache:      cache,
		Index:      index,
		CachePath:  cachePath,
		DigestPath: digestPath,
		Expired:    cache.Expired(catalogTTL(cfg), now),
	}

	inputs := providerInputs(cfg, cache)
	snap.Catalog = catalog.Build(catalog.BuildInput{
		Providers:      inputs,
		ModelsDev:      index,
		Stats:          cache.Stats,
		HideDeprecated: cfg != nil && cfg.Catalog.HideDeprecated,
		Stale:          snap.Expired && cache.Loaded,
	})

	if cache.Note != "" {
		snap.Catalog.Note(cache.Note)
	}
	if snap.Catalog.Len() == 0 {
		seeded := catalog.SeedCatalog(inputs)
		if seeded.Len() > 0 {
			// Carry over the notes from the empty build, but through
			// Note() so the duplicates are dropped: both builds walk the
			// same providers and produce the same "no resolved
			// credential" line, and printing it twice makes the tool look
			// like it is stuttering.
			carried := snap.Catalog.Notes
			snap.Catalog = seeded
			for i := len(carried) - 1; i >= 0; i-- {
				snap.Catalog.Prepend(carried[i])
			}
		}
	}
	// Layer 1 curation (docs/DESIGN-model-curation.md) runs last, on the
	// snapshot that is actually going to be shown — after the seed
	// fallback, never instead of it, so a first run with nothing cached
	// still gets a usable list before anything is filtered from it.
	snap.Catalog, snap.Hidden = applyCuration(snap.Catalog, curationRules(cfg))
	if snap.Catalog.Stale && !snap.Catalog.Seeded {
		if age := cache.Age(now); age < time.Duration(1<<62-1) {
			snap.Catalog.Note("catalog from " + humanAge(age) + " ago")
		}
	}
	return snap
}

// UncuratedCatalog rebuilds the catalog from snap's own Cache/Index —
// exactly what LoadCatalog/RefreshCatalog already fetched, no new network
// call — but skips both Layer 0 (BuildInput.HideDeprecated) and Layer 1/2
// curation (applyCuration) entirely: design doc §2.1's `ishakat models
// --all`, "existing flag, now bypasses curation". Before this, --all's own
// doc comment promised "disables the display filters" but ModelsOptions.All
// was never actually read past Models' own call site (writeModelsText took
// an `all bool` parameter and never consulted it) — this is the fix, and it
// now bypasses every layer, not only [catalog].hide_deprecated.
//
// It still runs the seed fallback: an empty cache/index with declared
// config models should show those, --all or not, for the same reason
// LoadCatalog's own seed fallback exists (a first run must never look
// broken). Deliberately does NOT re-derive the staleness note or re-run
// PruneStats — those are snapshot bookkeeping, not curation, and snap
// already carries the correct Stale/Seeded/FetchedAt values from whichever
// of LoadCatalog/RefreshCatalog built it.
func UncuratedCatalog(cfg *config.Config, snap CatalogSnapshot) catalog.Catalog {
	cache := snap.Cache
	if cache == nil {
		cache = catalog.LoadCache(CatalogCachePath(cfg))
	}
	index := snap.Index
	if index == nil {
		index = catalog.NewIndex()
	}
	inputs := providerInputs(cfg, cache)
	built := catalog.Build(catalog.BuildInput{
		Providers: inputs,
		ModelsDev: index,
		Stats:     cache.Stats,
		Stale:     snap.Catalog.Stale,
	})
	if built.Len() == 0 {
		if seeded := catalog.SeedCatalog(inputs); seeded.Len() > 0 {
			built = seeded
		}
	}
	return built
}

// RefreshCatalog goes to the network: discovery against every enabled
// provider with the per-provider timeout of §4.4, plus a conditional
// models.dev refresh. It writes the cache and returns a new snapshot.
//
// It returns the snapshot even on failure. A refresh that could not reach
// anything must leave the user exactly where they were —with the cached
// catalog— and not with an empty list.
func RefreshCatalog(ctx context.Context, cfg *config.Config, version string, prev CatalogSnapshot) (CatalogSnapshot, error) {
	now := time.Now()
	cache := prev.Cache
	if cache == nil {
		cache = catalog.LoadCache(CatalogCachePath(cfg))
	}
	index := prev.Index
	if index == nil {
		index = catalog.NewIndex()
	}
	cachePath := prev.CachePath
	if cachePath == "" {
		cachePath = CatalogCachePath(cfg)
	}
	digestPath := prev.DigestPath
	if digestPath == "" {
		digestPath = catalog.DigestPath(cachePath)
	}

	var firstErr error

	// 1. Provider discovery, in parallel.
	if wantsSource(cfg, "provider") {
		var targets []fetch.Target
		for _, p := range EnabledProviders(cfg) {
			if !p.Discover {
				continue
			}
			prov, err := NewProvider(cfg, p, version)
			if err != nil {
				cache.SetProviderError(p.ID, err, now)
				if firstErr == nil {
					firstErr = err
				}
				continue
			}

			discoverTimeout := time.Duration(p.TimeoutS) * time.Second
			if discoverTimeout <= 0 {
				discoverTimeout = time.Duration(cfg.App.TimeoutS) * time.Second
			}
			if discoverTimeout <= 0 {
				discoverTimeout = fetch.DefaultDiscoverTimeout
			}
			targets = append(targets, fetch.Target{
				ID:       p.ID,
				Provider: prov,
				Timeout:  discoverTimeout,
			})
		}
		if len(targets) > 0 {
			results := fetch.Discover(ctx, targets, fetch.DefaultDiscoverTimeout)
			fetch.Apply(cache, results, now)
			for _, r := range results {
				if r.Err != nil && firstErr == nil {
					firstErr = fmt.Errorf("%s: %w", r.ID, r.Err)
				}
			}
		}
	}

	// 2. models.dev, conditional. A failure here is not fatal: the metadata
	// is a nice-to-have, the provider list is not.
	if wantsSource(cfg, "modelsdev") {
		client := &fetch.ModelsDev{
			APIURL:    cfg.Catalog.ModelsDevURL,
			MetaURL:   cfg.Catalog.ModelsDevMetaURL,
			UserAgent: userAgent(version),
			Client:    &http.Client{Timeout: fetch.DefaultTimeout},
		}
		res := client.Refresh(ctx, index)
		switch {
		case res.Err != nil:
			cache.ModelsDev.Error = res.Err.Error()
			if firstErr == nil {
				firstErr = res.Err
			}
		default:
			index = res.Index
			cache.ModelsDev = catalog.ModelsDevCache{
				ETag:      index.ETag,
				FetchedAt: index.FetchedAt,
				Models:    index.Count(),
			}
			if res.Changed {
				if err := index.SaveDigest(digestPath); err != nil && firstErr == nil {
					firstErr = err
				}
			}
		}
	}

	cache.FetchedAt = now

	// 3. Rebuild and persist. A cache that cannot be written is a warning,
	// never a failure: the catalog in memory is already correct.
	inputs := providerInputs(cfg, cache)
	built := catalog.Build(catalog.BuildInput{
		Providers:      inputs,
		ModelsDev:      index,
		Stats:          cache.Stats,
		HideDeprecated: cfg != nil && cfg.Catalog.HideDeprecated,
	})

	// alive is computed from the UNcurated build (principle 1: "hiding is
	// a view, never a deletion" — a model curation hides is still resolvable
	// by exact ref, so its usage statistics must survive the same prune a
	// visible model's would).
	alive := make(map[string]bool, built.Len())
	for _, m := range built.Models {
		alive[m.Ref] = true
	}
	cache.PruneStats(alive, 500)

	cat, hiddenList := applyCuration(built, curationRules(cfg))

	if err := cache.Save(cachePath); err != nil {
		cat.Note("could not write the cache: " + err.Error())
		if firstErr == nil {
			firstErr = err
		}
	}

	return CatalogSnapshot{
		Catalog:    cat,
		Cache:      cache,
		Index:      index,
		CachePath:  cachePath,
		DigestPath: digestPath,
		Expired:    false,
		Hidden:     hiddenList,
	}, firstErr
}

// BackgroundRefresh is §4.4/§11's background-refresh side, wired from
// app.Run's own goroutine (never called on the startup path itself — see
// LoadCatalog's own comment on the non-negotiable budget). It wraps
// RefreshCatalog with the one policy call sites should not have to repeat:
// a refresh that failed AND produced nothing usable must not overwrite a
// catalog the user is already looking at with an empty one, so this
// returns nil in exactly that case, and the caller (tui.CatalogRefreshedMsg's
// handler) already treats nil as "nothing changed, leave the picker alone".
//
// A refresh that failed but still produced something — the common case,
// since RefreshCatalog rebuilds from cache plus whatever discovery did
// reach even on a partial failure — is returned normally: cat.Len() > 0 is
// the bar, not err == nil.
func BackgroundRefresh(ctx context.Context, cfg *config.Config, version string, prev CatalogSnapshot) *catalog.Catalog {
	snap, err := RefreshCatalog(ctx, cfg, version, prev)
	if err != nil && snap.Catalog.Len() == 0 {
		return nil
	}
	return &snap.Catalog
}

// CleanResult reports what CleanCatalogCache actually found and removed, so
// the CLI can print an honest summary instead of a bare "done" that leaves
// the user guessing whether there was anything to delete in the first
// place.
type CleanResult struct {
	CachePath     string
	DigestPath    string
	CacheRemoved  bool
	DigestRemoved bool
}

// CleanCatalogCache deletes the on-disk catalog cache (catalog.json) and its
// sibling models.dev digest (catalog-modelsdev.json), the two files
// LoadCatalog/RefreshCatalog read and write (see store.go and
// modelsdev.go's DigestPath). Neither file lives under $XDG_CONFIG_HOME:
// they are cache data ($XDG_CACHE_HOME/ishakat by default), so deleting
// ~/.config/ishakat — the fix for a bad config.toml — does not touch them
// and stale discovery results (a provider that used to answer, a model list
// from months ago) survive a config wipe unless this is called explicitly.
//
// Missing files are not an error: "already clean" and "just cleaned" should
// both report success, since the caller only cares that the cache is empty
// afterwards.
func CleanCatalogCache(cfg *config.Config) (CleanResult, error) {
	cachePath := CatalogCachePath(cfg)
	digestPath := catalog.DigestPath(cachePath)

	res := CleanResult{CachePath: cachePath, DigestPath: digestPath}

	removed, err := removeIfExists(cachePath)
	if err != nil {
		return res, fmt.Errorf("remove %s: %w", cachePath, err)
	}
	res.CacheRemoved = removed

	if digestPath != "" {
		removed, err = removeIfExists(digestPath)
		if err != nil {
			return res, fmt.Errorf("remove %s: %w", digestPath, err)
		}
		res.DigestRemoved = removed
	}
	return res, nil
}

func removeIfExists(path string) (bool, error) {
	if strings.TrimSpace(path) == "" {
		return false, nil
	}
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// RecordModelUse bumps the local statistics after a turn. Best effort by
// design: this feeds a ranking tiebreaker, and a cache that cannot be
// written must never turn into a visible error for the user.
func RecordModelUse(cfg *config.Config, ref string, latency time.Duration) {
	if strings.TrimSpace(ref) == "" {
		return
	}
	path := CatalogCachePath(cfg)
	cache := catalog.LoadCache(path)
	cache.RecordUse(ref, latency, time.Now())
	_ = cache.Save(path)
}

func userAgent(version string) string {
	if version != "" && version != "dev" {
		return "ishakat/" + version
	}
	return "ishakat"
}

// humanAge renders a duration the way the staleness strip of §4.4 reads:
// "3 days", "5 hours", "12 minutes".
func humanAge(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%d days", int(d.Hours()/24))
	case d >= 2*time.Hour:
		return fmt.Sprintf("%d hours", int(d.Hours()))
	case d >= 2*time.Minute:
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	default:
		return "moments"
	}
}

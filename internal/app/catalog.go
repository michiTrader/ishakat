package app

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/MichiTrader/ishakat/internal/catalog"
	"github.com/MichiTrader/ishakat/internal/catalog/fetch"
	"github.com/MichiTrader/ishakat/internal/config"
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
	if snap.Catalog.Stale && !snap.Catalog.Seeded {
		if age := cache.Age(now); age < time.Duration(1<<62-1) {
			snap.Catalog.Note("catalog from " + humanAge(age) + " ago")
		}
	}
	return snap
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
			targets = append(targets, fetch.Target{ID: p.ID, Provider: prov})
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
	cat := catalog.Build(catalog.BuildInput{
		Providers:      inputs,
		ModelsDev:      index,
		Stats:          cache.Stats,
		HideDeprecated: cfg != nil && cfg.Catalog.HideDeprecated,
	})

	alive := make(map[string]bool, cat.Len())
	for _, m := range cat.Models {
		alive[m.Ref] = true
	}
	cache.PruneStats(alive, 500)

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
	}, firstErr
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

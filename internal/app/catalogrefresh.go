// catalogrefresh.go is internal/app's real implementation of F2's own
// hot-apply seam (docs/ROADMAP-ux-2026-08-20.md's W4,
// internal/tui/catalogrefresh.go): the actual disk re-read and network
// discovery that a successful /login needs before the provider it just
// authenticated can appear anywhere a running session looks — /model,
// ctrl+p, or a plain model switch — without a restart or a separate
// --refresh.
//
// It lives here, not in internal/tui, for exactly the reason
// tui.CatalogRefreshFactory's own doc comment gives: internal/tui cannot
// import net/http or config.Load's own disk-touching path
// (internal/arch_test.go's TestTUINoImportaHTTP), so both steps have to
// run on this side of the §6.1 boundary, reached only through the
// function-typed seam tui.Root calls through.
package app

import (
	"context"

	"github.com/MichiTrader/ishakat/internal/catalog"
	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/tui"
	"github.com/MichiTrader/ishakat/internal/xdg"
)

// NewCatalogRefreshFactory returns a tui.CatalogRefreshFactory closed over
// version only — deliberately not over a *config.Config the way
// NewEngineFactory/NewLoginFactory are, since the entire point of this
// factory is that the cfg it must discover against cannot be the one
// captured at boot: it has to be reloaded from disk on every call, so that
// whatever a successful /login just wrote (config.SaveProviderConnection/
// SaveCredential, loginfactory.go) is what discovery actually sees.
//
// Each call: (1) reloads configuration from the same path Run's own
// config.Load call reads (xdg.ConfigFile()), (2) loads the on-disk catalog
// snapshot fresh against that reloaded config (LoadCatalog never touches
// the network, so this is cheap and cannot itself go stale), then (3) runs
// RefreshCatalog against it — the same discovery-against-every-enabled-
// provider-plus-models.dev call app.BackgroundRefresh already wraps for
// the §4.4/§11 startup refresh.
//
// A failure at any step (a corrupt config.toml, a network error during
// discovery) reports as (nil, nil): a hot-apply attempt that could not
// improve on what the session already has must not surface as a crash, or
// worse, silently erase the in-memory config the closing wizard already
// showed a success line for. tui.applyCatalogRefreshed's own nil handling
// for both fields already exists for precisely this "produced nothing
// usable" case (see CatalogRefreshedMsg's own doc comment) — reusing it
// here means no new no-op contract has to be invented.
func NewCatalogRefreshFactory(version string) tui.CatalogRefreshFactory {
	return func(ctx context.Context) (*catalog.Catalog, *config.Config) {
		cfg, err := config.Load(config.Options{UserPath: xdg.ConfigFile()})
		if err != nil || cfg == nil {
			return nil, nil
		}

		snap := LoadCatalog(cfg)
		next := BackgroundRefresh(ctx, cfg, version, snap)
		if next == nil {
			// Discovery could not improve on the disk snapshot (network
			// unreachable, every provider timed out) — but the freshly
			// reloaded cfg is still worth adopting: a successful /login
			// wrote a real credential to disk, and dropping the whole
			// hot-apply attempt just because discovery itself failed
			// would leave the session unable to even resolve the new
			// provider by /model, let alone list it, on the very next
			// try. LoadCatalog's own snapshot is what a plain restart
			// would have produced from the same disk state, so handing
			// that back (rather than nil) here still moves the needle.
			built := snap.Catalog
			return &built, cfg
		}
		return next, cfg
	}
}

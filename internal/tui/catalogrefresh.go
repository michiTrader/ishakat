// catalogrefresh.go names the seam F2's `/login` hot-apply fix crosses
// (§6.1, the same boundary loginfactory.go's own LoginFactory already draws
// for the wizard's HTTP calls, and engine.go's EngineFactory draws for
// switching models): internal/tui cannot import net/http or anything that
// reaches it (internal/arch_test.go's TestTUINoImportaHTTP), so re-loading
// configuration from disk and re-running catalog discovery against the
// provider a user just authenticated has to live on the far side of a
// function-typed seam this package only calls through, never implements.
//
// internal/app's real implementation (wired from app.go's
// tui.NewRoot(tui.Options{...}) call, mirroring NewEngineFactory/
// NewLoginFactory) re-reads config.toml/credentials.toml from disk — not
// the *config.Config this package's own Root.cfg still holds, which is a
// snapshot taken once at boot and does not see whatever a successful
// /login just wrote — before calling app.RefreshCatalog, so the provider's
// freshly-saved credential is what discovery actually sees. Without that
// disk re-read, a hot refresh would rebuild the exact same catalog the
// stale in-memory config already produced, defeating the whole point of
// F2 (docs/ROADMAP-ux-2026-08-20.md's own "the hot-swap machinery already
// exists" note undersells this one piece: the machinery exists for a
// catalog that is already correct, not for one that needs a config reload
// first).
package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/catalog"
	"github.com/MichiTrader/ishakat/internal/config"
)

// CatalogRefreshFactory re-loads configuration from disk and re-runs
// catalog discovery against it, returning both: the refreshed catalog
// (the same nil-means-"produced nothing usable" contract
// app.BackgroundRefresh already documents, which CatalogRefreshedMsg's
// handler treats as a no-op) and the freshly-loaded *config.Config the
// discovery actually ran against.
//
// The cfg return is deliberately a *second*, independent value —
// internal/app's real implementation never mutates the *config.Config
// this package's own Root.cfg points at directly, because that call runs
// on the tea.Cmd's own goroutine (refreshCatalogCmd, below), concurrently
// with Update/View's single goroutine, which may be reading m.cfg (e.g.
// /config, /debug) at the exact same time — mutating shared state from a
// background goroutine here would be a real, not merely theoretical, data
// race. Instead, the fresh value travels back on CatalogRefreshedMsg and
// is applied by applyCatalogRefreshed, which runs on Update's own
// goroutine and can safely copy it into the existing *config.Config
// Root.cfg (and every closure — engineFor included — that already holds
// that same pointer) points at.
//
// nil is a supported *value* for the field that holds this (most of this
// package's own tests, and any caller with nothing wired): finishLogin
// only chains a refresh when catalogRefreshFor is non-nil, the same "no
// silent panic on an unwired dependency" rule switchEngine's own nil
// check for engineFor and startLogin's own nil check for loginFor already
// follow.
type CatalogRefreshFactory func(ctx context.Context) (*catalog.Catalog, *config.Config)

// refreshCatalogCmd wraps a CatalogRefreshFactory call as a tea.Cmd, the
// same wrapping requestLoginCodeCmd applies for LoginFactory: Bubble Tea
// already runs every Cmd in its own goroutine, so the (network-bound)
// call is safe to make directly here. It reuses CatalogRefreshedMsg
// (msgs.go) rather than a bespoke message type, so this new trigger and
// the pre-existing §4.4/§11 background refresh both land on the exact
// same applyCatalogRefreshed handler — one code path, two triggers, never
// two competing "apply a fresh catalog" implementations. The background
// refresh's own call site never has a fresh cfg to report (its cfg never
// changed), so CatalogRefreshedMsg.Cfg is simply nil there — see
// applyCatalogRefreshed's own nil check.
func refreshCatalogCmd(ctx context.Context, factory CatalogRefreshFactory) tea.Cmd {
	return func() tea.Msg {
		cat, cfg := factory(ctx)
		return CatalogRefreshedMsg{Catalog: cat, Cfg: cfg}
	}
}

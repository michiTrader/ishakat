// reload.go is internal/app's real implementation of F17's own hot-apply
// seam (docs/ROADMAP-ux-2026-08-20.md's W5, internal/tui/reload.go): the
// actual disk re-read and re-derivation of the pieces /reload covers that
// have no live-reload path of their own yet — the keymap, the rung-0
// skills listing, and the effective system prompt.
//
// It lives here, not in internal/tui, for the same reason
// NewCatalogRefreshFactory does: internal/tui cannot import config.Load's
// own disk-touching path (internal/arch_test.go's TestTUINoImportaHTTP),
// so config.Load/DiscoverSkills/SystemPrompt all have to run on this side
// of the §6.1 boundary, reached only through the function-typed seam
// tui.Root calls through.
package app

import (
	"context"

	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/tui"
	"github.com/MichiTrader/ishakat/internal/xdg"
)

// NewReloadFactory returns a tui.ReloadFactory closed over nothing at all
// (unlike NewEngineFactory's cfg, or NewCatalogRefreshFactory's version):
// every call re-reads config.toml/.ishakat.toml/credentials.toml fresh
// from the same paths app.Run's own config.Load call reads, exactly the
// same "the cfg it must discover against cannot be the one captured at
// boot" reasoning NewCatalogRefreshFactory's own comment gives, applied
// here to /reload's own three targets instead of catalog discovery.
//
// A failed config.Load (a corrupt config.toml) reports a zero
// tui.ReloadResult (Cfg == nil): tui.applyReloaded's own nil check treats
// that as "could not improve on what the session already has", the same
// contract CatalogRefreshFactory's nil-Catalog case already establishes.
//
// ctx is accepted for the same reason CatalogRefreshFactory's signature
// takes one — config.Load/skills.Discover/os.ReadFile do no network I/O
// and never consult it today, but the seam's own shape should not have to
// change the day one of them starts a context-aware read.
func NewReloadFactory() tui.ReloadFactory {
	return func(ctx context.Context) tui.ReloadResult {
		cfg, err := config.Load(config.Options{UserPath: xdg.ConfigFile()})
		if err != nil || cfg == nil {
			var warn string
			if err != nil {
				warn = err.Error()
			}
			return tui.ReloadResult{Warn: warn}
		}

		system, warn := SystemPrompt(cfg)
		return tui.ReloadResult{
			Cfg:    cfg,
			Keys:   cfg.Keys,
			Skills: DiscoverSkills(cfg),
			System: system,
			Warn:   warn,
		}
	}
}

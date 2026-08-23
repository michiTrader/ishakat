// reload.go implements /reload (F17, docs/ROADMAP-ux-2026-08-20.md's W5):
// re-reading the on-disk pieces that were only ever resolved once, at
// NewRoot time, and applying them to the running session without a
// restart.
//
// The roadmap names five things: keybindings, skills, prompts, themes,
// context files. Two of those already have a live-reload path before this
// file exists at all — theme via /theme (theme.go's own switchTheme,
// theme.Load never errors) and config/catalog via F2's own
// CatalogRefreshFactory seam (catalogrefresh.go) — so /reload's own new
// work is only the remaining three, all of them cheap, synchronous,
// disk-only reads with no network involved: the keymap (config.Keys →
// NewMap), the rung-0 skills listing (skills.Discover), and the effective
// system prompt (app.SystemPrompt, which itself folds in AGENTS.md — the
// roadmap's "context files" — and the skills summary). /reload calls
// /theme's and F2's own hot-apply paths too, so one command answers for
// everything the roadmap named, not just the three pieces that needed new
// plumbing.
//
// This crosses the same §6.1 boundary CatalogRefreshFactory already
// crosses, for the same reason: internal/tui cannot import net/http or
// config.Load's own disk-touching path (TestTUINoImportaHTTP), so the
// actual config.Load + skills.Discover + SystemPrompt calls have to run on
// the far side of a function-typed seam this package only calls through.
// ReloadFactory deliberately returns everything already computed —
// config.Keys, skills.Result, the system prompt string, and (mirroring
// CatalogRefreshFactory) the freshly-loaded *config.Config itself, since a
// caller that already re-read config.toml has no reason to make internal/tui
// re-derive NewMap/DiscoverSkills/SystemPrompt's inputs a second time from a
// raw path it is not allowed to open.
package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/skills"
)

// ReloadFactory re-reads configuration from disk and recomputes the
// keymap, skills listing, and system prompt against it. internal/app's
// real implementation is expected to call the exact same
// config.Load/NewMap/DiscoverSkills/SystemPrompt path app.Run itself walks
// at startup, so a /reload mid-session and a fresh restart can never
// disagree about what the same files on disk resolve to.
//
// A failure at any step (a corrupt config.toml) reports as a zero Config
// (cfg == nil): applyReloaded's own nil check treats that as "could not
// improve on what the session already has", the same no-op contract
// CatalogRefreshFactory's nil-Catalog case already establishes, rather
// than blanking a working keymap/skills/prompt over a typo in a file the
// user is mid-edit of.
type ReloadFactory func(ctx context.Context) ReloadResult

// ReloadResult is what a ReloadFactory call produced. Cfg is nil on
// failure (see ReloadFactory's own doc comment); every other field is only
// meaningful when Cfg is non-nil.
type ReloadResult struct {
	Cfg    *config.Config
	Keys   config.Keys
	Skills skills.Result
	System string
	Warn   string
}

// ReloadedMsg wraps a ReloadFactory's result as a tea.Msg, the same
// pattern CatalogRefreshedMsg already establishes for F2's own hot-apply
// seam.
type ReloadedMsg struct {
	Result ReloadResult
}

// reloadCmd wraps a ReloadFactory call as a tea.Cmd. The call itself is
// disk-only (no network, unlike CatalogRefreshFactory's discovery step),
// but it still runs through Bubble Tea's own goroutine rather than
// directly inside Update, for the identical reason refreshCatalogCmd
// does: a disk read of unknown size (a hand-edited AGENTS.md, a slow
// filesystem) must not block the render loop while it happens.
func reloadCmd(ctx context.Context, factory ReloadFactory) tea.Cmd {
	return func() tea.Msg {
		return ReloadedMsg{Result: factory(ctx)}
	}
}

// runReloadCommand implements /reload (§13, F17): no argument is the only
// form — there is nothing to parse an argument into, unlike /theme or
// /settings, since this command re-reads everything it knows how to
// reload in one pass rather than letting a user reload one piece at a
// time. A nil reloadFor (every test in this package, and any caller with
// nothing wired) degrades to a notice instead of a silent no-op, the same
// "explain the gap instead of pretending it worked" rule startLogin's own
// nil check already follows for /login.
func (m Root) runReloadCommand() (tea.Model, tea.Cmd) {
	if m.reloadFor == nil {
		return m.slashNotice(m.lay.glyphs().warnMark +
			" /reload todavia no esta disponible en esta build")
	}
	return m, reloadCmd(context.Background(), m.reloadFor)
}

// applyReloaded is ReloadedMsg's only handler. res.Cfg == nil means the
// factory could not produce a usable configuration (a corrupt config.toml)
// — see ReloadFactory's own doc comment for why that is a no-op rather
// than an applied-but-empty reload.
//
// *m.cfg = *res.Cfg (not m.cfg = res.Cfg) for the identical pointer-identity
// reason applyCatalogRefreshed already documents: engineFor/catalogRefreshFor
// were built once, at boot, closed over the exact pointer value Root.cfg
// held at construction time. Copying the fresh value's fields into the same
// struct that pointer already refers to is what makes a config edit visible
// to those closures too, with no rewiring. The nil-m.cfg fallback (every
// test in this package) simply adopts the pointer directly, mirroring
// applyCatalogRefreshed's own fallback.
//
// m.keys, m.skills and m.system are replaced outright: all three are plain
// values read fresh on every use (m.keys by every key-comparison call site,
// m.skills only by /skills, m.system only when a turn builds its
// engine.Request — root.go:2411), so there is no cached derivative of any
// of them anywhere else in Root that would otherwise go stale.
func (m Root) applyReloaded(msg ReloadedMsg) (tea.Model, tea.Cmd) {
	res := msg.Result
	if res.Cfg == nil {
		return m.slashNotice(m.lay.glyphs().warnMark + " no se pudo recargar: " + or(res.Warn, "config.toml invalido"))
	}
	if m.cfg != nil {
		*m.cfg = *res.Cfg
	} else {
		m.cfg = res.Cfg
	}
	m.keys = NewMap(res.Keys)
	m.skills = res.Skills
	m.system = res.System

	g := m.lay.glyphs()
	msgText := g.assistantMark + " recargado: atajos, skills, contexto"
	if res.Warn != "" {
		msgText += "\n  " + g.warnMark + " " + res.Warn
	}
	return m.slashNotice(msgText)
}

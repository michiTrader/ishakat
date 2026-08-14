// debugcmd.go implements /debug (§13, Step 18's other left-over half,
// closed alongside /config's own §17 2026-08-13 entry): a local-only
// diagnostic snapshot rendered inline, the in-session counterpart to
// `ishakat doctor`'s non-network half.
//
// This deliberately mirrors runConfigCommand's shape (configcmd.go): a
// single slashNotice built from values Root already holds or from
// dependencies confirmed net/http-free by internal/arch_test.go's
// TestTUINoImportaHTTP (internal/netfix.CGOEnabled is a build-tag-gated
// const, internal/xdg.IsTermux/AgentsFile only stat/join paths,
// internal/agentsmd.Sources only joins paths without touching the
// filesystem). It does NOT re-run netfix.Install() (real DNS I/O — main.go
// already ran it once at process start, calling it again from inside
// Update would block the event loop the same way a second network probe
// would) and it does NOT attempt doctor's DNS/HTTPS probes at all: both
// need either a live resolver re-check or net/http itself, neither of
// which this package may touch (§6.1). `ishakat doctor` remains the
// answer for "is the network actually reachable" and is named explicitly
// below — the same "point at the remedy instead of a silent gap" honesty
// unimplementedNotice's own former /debug case already established.
//
// Deliberately out of scope for this increment: §9.8's
// "$XDG_STATE_HOME/ishakat/last-error.json" (xdg.ErrorFile()) has no
// writer anywhere in the codebase yet, so there is nothing on disk for
// /debug to show even if it read that path — implementing the write side
// is a separate increment, not silently folded into this one.
package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/agentsmd"
	"github.com/MichiTrader/ishakat/internal/netfix"
	"github.com/MichiTrader/ishakat/internal/xdg"
)

// runDebugCommand renders the local half of `ishakat doctor` (version,
// platform is intentionally omitted — Root never imports "runtime", and
// adding it for one line is not worth the new dependency), cgo/termux,
// config paths, the AGENTS.md three-layer listing, and the terminal's own
// already-resolved color/glyph decision (m.cap/m.lay.Glyphs, both decided
// once at startup or resize by internal/theme, never re-detected here).
// m.cfg nil (every test in this package that never sets Options.Cfg) still
// renders every section that does not need it — unlike runConfigCommand,
// there is no single "nothing loaded" bail-out, because most of this
// screen's value (cgo/termux/glyphs) has nothing to do with config.toml.
func (m Root) runDebugCommand() (tea.Model, tea.Cmd) {
	g := m.lay.glyphs()

	var b strings.Builder
	fmt.Fprintf(&b, "%s debug %s ishakat %s", g.assistantMark, g.dot, m.version)

	fmt.Fprintf(&b, "\n\n[network]")
	fmt.Fprintf(&b, "\n  cgo             %v", netfix.CGOEnabled)
	fmt.Fprintf(&b, "\n  termux          %v", xdg.IsTermux())
	fmt.Fprintf(&b, "\n  (para DNS/HTTPS, resolver y servidores: %s)", "`ishakat doctor`")

	fmt.Fprintf(&b, "\n\n[paths]")
	fmt.Fprintf(&b, "\n  config          %s", xdg.Pretty(xdg.ConfigFile()))
	fmt.Fprintf(&b, "\n  cache           %s", xdg.Pretty(xdg.CacheDir()))
	fmt.Fprintf(&b, "\n  data            %s", xdg.Pretty(xdg.DataDir()))
	fmt.Fprintf(&b, "\n  state           %s", xdg.Pretty(xdg.StateDir()))

	agentsOn := m.cfg == nil || m.cfg.App.AgentsMD
	fmt.Fprintf(&b, "\n\n[agents.md]  habilitado: %v", agentsOn)
	if agentsOn {
		for _, src := range agentsmd.Sources(xdg.AgentsFile(), ".") {
			fmt.Fprintf(&b, "\n  %s %-8s %s", g.dot, src.Layer, xdg.Pretty(src.Path))
		}
	}

	fmt.Fprintf(&b, "\n\n[terminal]")
	fmt.Fprintf(&b, "\n  cwd             %s", m.cwd)
	fmt.Fprintf(&b, "\n  color           %s", m.cap)
	fmt.Fprintf(&b, "\n  glyphs          %s", m.lay.Glyphs)

	fmt.Fprintf(&b, "\n\n  (dump completo de errores: aun no implementado — ver docs/PLAN.md §9.8)")

	return m.slashNotice(b.String())
}

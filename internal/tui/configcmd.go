// configcmd.go implements /config (§13, Step 18's own left-over scope):
// the effective configuration, secrets redacted, drawn inline in the
// running session — the in-session counterpart to `ishakat config check`
// that unimplementedNotice's "todavia no" message used to point at
// instead.
//
// This is the first real caller internal/config.Redacted()/Mask()
// (validate.go) ever had: docs/PLAN.md's Phase 4 paragraph flagged both
// as tested (TestRedacted, config_test.go) but dead code, "likely meant
// for a future /debug secrets-redaction screen that was never finished".
// runConfigCommand below is that screen, mirroring runModelsCommand's/
// runSkillsCommand's own shape — a single slashNotice built from a
// snapshot Root already holds (m.cfg), never a fresh disk read from
// inside Update, the same "read once, hand over" rule this package
// applies to the catalog and to skills.
package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/config"
)

// runConfigCommand renders the effective configuration exactly as
// ishakat config check's own summary line does (provider count, layers
// read, warning count) plus the handful of [app]/[session]/[ui] settings
// a user actually asks about mid-session, and one row per provider —
// id, kind, enabled/authOK, and its api_key already masked by
// config.Redacted(). m.cfg is nil for every test in this package that
// never sets Options.Cfg (and for any real caller that somehow started
// with no configuration at all): reported instead of a nil dereference,
// the same "nothing wired, nothing happens" default startLogin already
// follows for a nil loginFor.
func (m Root) runConfigCommand() (tea.Model, tea.Cmd) {
	g := m.lay.glyphs()
	if m.cfg == nil {
		return m.slashNotice(g.warnMark + " no hay configuracion cargada todavia")
	}

	cfg := m.cfg.Redacted()

	var b strings.Builder
	fmt.Fprintf(&b, "%s config %s %d provider(s)", g.assistantMark, g.dot, len(cfg.Providers))
	if len(cfg.Files) > 0 {
		fmt.Fprintf(&b, "\n  capas: %s", strings.Join(cfg.Files, ", "))
	}
	if len(cfg.Warnings) > 0 {
		fmt.Fprintf(&b, "\n  %s %d advertencia(s)", g.warnMark, len(cfg.Warnings))
		for _, w := range cfg.Warnings {
			fmt.Fprintf(&b, "\n    - [%s] %s", w.Where, w.Msg)
		}
	}

	fmt.Fprintf(&b, "\n\n[app]")
	fmt.Fprintf(&b, "\n  default_model   %s", orDash(cfg.App.DefaultModel))
	fmt.Fprintf(&b, "\n  compact_model   %s", orDash(cfg.App.CompactModel))
	fmt.Fprintf(&b, "\n  fallback_model  %s", orDash(cfg.App.FallbackModel))

	fmt.Fprintf(&b, "\n\n[session]")
	fmt.Fprintf(&b, "\n  save            %s", yesNo(cfg.Session.Save))
	fmt.Fprintf(&b, "\n  dir             %s", orDash(cfg.Session.Dir))

	fmt.Fprintf(&b, "\n\n[ui]")
	fmt.Fprintf(&b, "\n  theme           %s", orDash(cfg.UI.Theme))
	fmt.Fprintf(&b, "\n  markdown        %s", yesNo(cfg.UI.Markdown))
	fmt.Fprintf(&b, "\n  syntax          %s", yesNo(cfg.UI.Syntax))

	for _, p := range sortedByID(cfg.Providers) {
		mark := " "
		if p.Enabled {
			mark = g.modelMark
		}
		status := "auth ok"
		if !p.AuthOK {
			status = "sin autenticar"
			if p.MissingEnv != "" {
				status = "falta $" + p.MissingEnv
			}
		}
		fmt.Fprintf(&b, "\n\n%s %-14s %s %s", mark, p.ID, g.dot, p.Kind)
		fmt.Fprintf(&b, "\n  base_url  %s", p.BaseURL)
		fmt.Fprintf(&b, "\n  enabled   %s  %s %s", yesNo(p.Enabled), g.dot, status)
		if p.APIKey != "" {
			fmt.Fprintf(&b, "\n  api_key   %s", p.APIKey)
		}
	}

	return m.slashNotice(b.String())
}

// orDash renders an empty configuration value as "-" rather than a blank
// column, so a scan of the listing can tell "unset" apart from a render
// bug that swallowed the value.
func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// yesNo renders a bool as the config file's own vocabulary (true/false),
// not a localized word: this listing is meant to be cross-checked against
// config.toml line by line, the same reason ishakat config check's own
// summary never translates a TOML value either.
func yesNo(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// sortedByID orders providers by id so the listing reads the same run to
// run regardless of the order config.toml declared them in — the same
// "list is cheap, load is deferred" honesty sortedByRef (models.go) and
// sortedThemeNames (theme.go) already follow for their own listings.
func sortedByID(providers []config.Provider) []config.Provider {
	out := append([]config.Provider(nil), providers...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

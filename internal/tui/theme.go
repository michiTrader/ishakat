// theme.go implements /theme (§8/§11 Fase 3, first increment): a live,
// in-session theme switch — the "/theme en vivo" Fase 3's own opening
// sentence names, and the one unimplementedNotice's own doc comment
// flagged as the last remaining Phase-3-reserved KindUnimplemented row.
//
// No argument lists the themes available right now — the embedded
// default plus anything theme.Available finds under Options.ThemesDir —
// mirroring runModelsCommand's own read-only inline-notice shape
// (models.go). A name argument resolves via theme.Load, which never
// errors: an unknown or broken theme degrades to the embedded fallback
// plus a warning, exactly as it already does at startup in
// internal/app.Run. Applying it is a straight rebuild of m.styles, the
// same "value type, method returns the next Root" pattern
// commitModelSwitch (root.go) already follows for /model.
//
// Persisting the choice to [ui].theme — so it survives a restart — goes
// through ThemeStore.Save, not a direct internal/config call: the same
// §6.1 seam EvolveStore.Decay (suggest.go) already draws for its own
// config write, so this package still never imports internal/config's
// write path directly. nil is a supported value (every test in this
// package, and any caller with nothing wired): the switch still applies
// for the running session, it just does not survive a restart — the same
// "nothing wired, nothing happens beyond the in-memory effect" default
// EvolveStore/EngineFactory already establish elsewhere in this package.
package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/theme"
)

// ThemeStore is /theme's own persistence seam. Save writes name into
// [ui].theme so the choice survives a restart. internal/app is expected
// to implement this over config.SetTheme (internal/config/connection.go),
// the same "package never touches internal/config's write path itself"
// rule EvolveStore.Decay already follows for its own write.
type ThemeStore interface {
	Save(name string) error
}

// runThemeCommand implements /theme's two behaviours: no argument lists,
// a name switches. args arrives already trimmed by slash.Parse, but
// TrimSpace again costs nothing and keeps this function correct if that
// ever changes.
func (m Root) runThemeCommand(args string) (tea.Model, tea.Cmd) {
	name := strings.TrimSpace(args)
	if name == "" {
		return m.listThemes()
	}
	return m.switchTheme(name)
}

// listThemes renders every theme name theme.Available finds — the
// embedded default plus anything under m.themesDir — sorted so the
// listing reads the same run to run regardless of directory order,
// the same "list is cheap, load is deferred" honesty runModelsCommand's
// own listing already follows for the catalog. The active theme's row
// carries the assistant mark, mirroring runModelsCommand's own "current
// model" mark.
func (m Root) listThemes() (tea.Model, tea.Cmd) {
	g := m.lay.glyphs()
	names := sortedThemeNames(theme.Available(m.themesDir))

	var b strings.Builder
	fmt.Fprintf(&b, "%s temas %s %d", g.assistantMark, g.dot, len(names))
	for _, n := range names {
		mark := " "
		if n == m.styles.Theme.Name {
			mark = g.assistantMark
		}
		fmt.Fprintf(&b, "\n  %s %s", mark, n)
	}
	return m.slashNotice(b.String())
}

// sortedThemeNames orders theme.Available's result alphabetically. A
// copy, never Available's own backing slice, the same defensive rule
// sortedByRef (models.go) already follows for the catalog's own slice.
func sortedThemeNames(names []string) []string {
	out := append([]string(nil), names...)
	sort.Strings(out)
	return out
}

// switchTheme resolves name via theme.Load and applies it immediately by
// rebuilding m.styles — theme.Load never errors, so there is no failure
// path here to report, only theme.Theme.Warnings (an unknown name, a
// broken TOML file) to surface exactly as `ishakat doctor` already would.
// Persistence (ThemeStore.Save) is best-effort: a write failure switches
// the running session anyway and says so, the same "the display already
// changed, hiding that would be a worse surprise" reasoning
// commitModelSwitch's own comment gives for a failed engine rebuild.
func (m Root) switchTheme(name string) (tea.Model, tea.Cmd) {
	g := m.lay.glyphs()
	th := theme.Load(name, m.themesDir)
	m.styles = theme.NewStyles(th, m.cap, m.lay.Glyphs)

	msg := g.assistantMark + " tema: " + th.Name
	if m.themeStore != nil {
		if err := m.themeStore.Save(th.Name); err != nil {
			msg += " (no se pudo guardar: " + err.Error() + ")"
		}
	}
	for _, w := range th.Warnings {
		msg += "\n  " + g.warnMark + " " + w
	}
	return m.slashNotice(msg)
}

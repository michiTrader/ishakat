// settingscmd.go implements /settings (F4, docs/ROADMAP-ux-2026-08-20.md
// W5): the first slice of the wave's own largest item, whose text names
// per-key schema metadata as "the actual work... the overlay is the easy
// half". This file is that first half made real, for a deliberately
// narrow slice of the schema -- see internal/config/settings.go's own
// package comment for exactly which four keys and why only those four.
//
// Shape mirrors runPermissionsCommand (permissionscmd.go) more than
// runThemeCommand: bare "/settings" is a read-only listing (one row per
// config.Settings entry, current value, a short help string), while
// "/settings <key> <value>" is the write half -- validated against
// config.Setting.Valid before anything changes, exactly the "reject an
// unrecognized value outright" rule permissionscmd.go's own package
// comment gives for the identical reason: a settings surface silently
// accepting a typo is worse than refusing it. No interactive overlay
// (a searchable list, a picker) is built in this slice; the roadmap's own
// text calls that "the easy half", and a working read/write command over
// real schema metadata is the wave's load-bearing piece.
package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/config"
)

// SettingsStore is /settings' own persistence seam (§6.1, the same
// boundary ThemeStore/SessionTitleStore already draw): internal/tui may
// validate and apply a setting in memory, but every write to config.toml
// itself goes through internal/app's own implementation over
// config.SetSetting.
//
// nil is a supported value, the same "nothing wired, nothing happens
// beyond the in-memory change" default every other Store in this package
// already establishes: a live apply still takes effect for the running
// session, it just does not survive a restart.
type SettingsStore interface {
	// Set persists key=value into config.toml. An error means the write
	// was not persisted; the caller still applies the change in memory,
	// the same best-effort order switchTheme/renameSession/
	// applyPermissionsAutonomy all already follow for their own writes.
	Set(key, value string) error
}

// runSettingsCommand dispatches /settings' two behaviours: no argument
// (or anything that does not parse as "<key> <value>") renders the
// read-only listing; a well-formed pair validates and applies.
func (m Root) runSettingsCommand(args string) (tea.Model, tea.Cmd) {
	key, value, ok := parseSettingsArgs(args)
	if !ok {
		return m.listSettings()
	}
	return m.applySetting(key, value)
}

// parseSettingsArgs recognizes exactly "<key> <value...>": the key is the
// first whitespace-separated field, the value is everything after it
// (TrimSpace'd), so a future string-kind value with its own internal
// spaces is not truncated at the first space the way strings.Fields alone
// would. Zero or one field (bare "/settings", or "/settings key" with
// nothing after it) is not a write attempt at all -- it falls through to
// the listing, mirroring isPermissionsAutonomyArg's own two-outcome shape
// but collapsed into one boolean since Settings' vocabulary (unlike
// permissions' "autonomy" sub-verb) has no second read-only sub-command
// to distinguish from.
func parseSettingsArgs(args string) (key, value string, ok bool) {
	args = strings.TrimSpace(args)
	if args == "" {
		return "", "", false
	}
	key, rest, found := strings.Cut(args, " ")
	if !found {
		return "", "", false
	}
	value = strings.TrimSpace(rest)
	if value == "" {
		return "", "", false
	}
	return key, value, true
}

// listSettings renders one row per config.Settings entry: key, current
// value (read from m.cfg where each key's own Root-side cache already
// lives -- cfgBanner/cfgMarkdown/cfgSyntax/cfgReasoning, never a second
// read of m.cfg itself, so the listing can never disagree with what a
// live apply actually flipped), and its help text. m.cfg == nil is not
// checked here the way runConfigCommand checks it: unlike /config's own
// full snapshot, every value this listing shows already has a safe
// Root-side default from NewRoot's own o.Cfg == nil fallback (see
// cfgBanner's own doc comment), so there is something honest to show
// either way.
func (m Root) listSettings() (tea.Model, tea.Cmd) {
	g := m.lay.glyphs()

	var b strings.Builder
	fmt.Fprintf(&b, "%s settings %s %d", g.assistantMark, g.dot, len(config.Settings))
	for _, s := range config.Settings {
		fmt.Fprintf(&b, "\n  %-14s %-10s %s", s.Key, m.settingValue(s.Key), s.Help)
	}
	fmt.Fprintf(&b, "\n\n%s /settings <clave> <valor>", g.scrollHint)
	return m.slashNotice(b.String())
}

// settingValue reads the live in-memory value for one of the four keys
// this slice covers, straight off the Root fields NewRoot already
// populated from Options.Cfg -- never a second parse of m.cfg, so
// /settings' own listing and whatever actually drives rendering can
// never drift apart mid-session the way a fresh m.cfg.UI.* read after a
// live apply could (m.cfg itself is not mutated by applySetting below;
// only the Root-side cache is, exactly like /theme never touches m.cfg
// either).
func (m Root) settingValue(key string) string {
	switch key {
	case "ui.banner":
		return yesNo(m.cfgBanner)
	case "ui.markdown":
		return yesNo(m.cfgMarkdown)
	case "ui.syntax":
		return yesNo(m.cfgSyntax)
	case "ui.reasoning":
		return m.cfgReasoning
	default:
		return "-"
	}
}

// applySetting validates key/value against config.Settings, applies it
// to the matching Root field immediately, and persists best-effort
// through m.settingsStore -- the identical apply-then-persist order
// switchTheme/renameSession/applyPermissionsAutonomy all already follow:
// the display already changed, so a save failure is reported alongside
// it, not instead of it.
func (m Root) applySetting(key, value string) (tea.Model, tea.Cmd) {
	g := m.lay.glyphs()

	setting, ok := config.FindSetting(key)
	if !ok {
		return m.slashNotice(g.warnMark + " configuracion desconocida: " + key)
	}
	if !setting.Valid(value) {
		return m.slashNotice(fmt.Sprintf("%s valor invalido %q para %s -- usa %s",
			g.warnMark, value, key, strings.Join(setting.AllowedDisplay(), ", ")))
	}

	switch key {
	case "ui.banner":
		m.cfgBanner = value == "true"
	case "ui.markdown":
		m.cfgMarkdown = value == "true"
	case "ui.syntax":
		m.cfgSyntax = value == "true"
	case "ui.reasoning":
		m.cfgReasoning = value
	}

	msg := g.assistantMark + " " + key + " " + g.dot + " " + value
	if m.settingsStore != nil {
		if err := m.settingsStore.Set(key, value); err != nil {
			msg += " (no se pudo guardar: " + err.Error() + ")"
		}
	} else {
		msg += " (solo esta sesion -- no hay donde guardarlo)"
	}
	return m.slashNotice(msg)
}

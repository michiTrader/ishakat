// settings.go is F4's own opening slice (docs/ROADMAP-ux-2026-08-20.md W5):
// "the actual work" the roadmap names for /settings is per-key schema
// metadata (label, help text, kind, allowed values) in place of a
// hand-maintained UI — this file is that metadata, plus the one write
// primitive (SetSetting) a slash command or a future overlay needs to
// persist a change.
//
// Scope, deliberately narrow for this first slice: the four [ui] keys
// that already have a direct, already-tested live-apply path in
// internal/tui.Root with no engine rebuild, no picker, and no restart
// required — ui.banner/ui.markdown/ui.syntax (Root.cfgBanner/cfgMarkdown/
// cfgSyntax, wired at NewRoot time from these exact keys) and ui.reasoning
// (Root.cfgReasoning, via chat.go's reasoningModeOr). Schema.go's other
// ~50-odd keys (provider connections, tool permissions, keybindings,
// autonomy...) are not covered here: most need a restart, an engine
// rebuild, or carry enough danger (permissions.Read/Write/Shell) that a
// typo silently taking effect is a materially different risk than a typo
// in whether markdown rendering is on. Widening Settings to the rest of
// the schema is the next slice's job, not this one's — the same
// "smallest independently-shippable slice first" discipline every prior
// wave in this engagement has followed.
//
// Persistence decision, the one this slice's own investigation flagged as
// open and now resolves: SetSetting reuses readRawConfigTOML/
// writeRawConfigTOML (connection.go), the same machinery SetTheme/
// SetAppModel/SetAlias/AddFavorite already use for every other config.toml
// write. That machinery does not preserve hand-written comments — the
// exact reason internal/curation exists as a *separate* file — but
// curation's problem was categorically different: a hide/keep decision
// fires on every single ctrl+x keystroke inside an open picker, so a
// comment-destroying round trip risked corrupting config.toml on nearly
// every session. A /settings write is the same shape and the same
// frequency as /theme's own write: a deliberate, occasional, user-typed
// line, already accepted as config.toml's own trade-off since SetTheme
// shipped. There is no new risk here that /theme, /model set, or /model
// alias set did not already carry — so this reuses their machinery rather
// than inventing a second, parallel write path (or a second file) for a
// change that is not more frequent or more automatic than theirs.
package config

import (
	"errors"
	"fmt"
	"strings"
)

// SettingKind is the shape of one /settings-editable value: a plain
// on/off switch, or a closed vocabulary of strings. Both kinds are
// self-validating (Setting.Valid), so a caller never has to hand-roll
// its own parsing rules per key the way runPermissionsCommand's own
// parsePermissionsAutonomyArg had to before this table existed.
type SettingKind int

const (
	SettingBool SettingKind = iota
	SettingEnum
)

// Setting is one row of the Settings table below: everything a generic
// /settings command needs to list, validate, and describe a single
// config.toml key, without hand-coding a switch statement per key at the
// call site the way runConfigCommand's read-only listing does.
type Setting struct {
	// Key is the dotted TOML path ("ui.banner"), matching how a user
	// would find it in config.toml itself -- deliberately not a Go field
	// name (UI.Banner), since this table's whole point is being read by
	// someone who has config.toml open, not internal/config's own source.
	Key string

	// Label is a short human name for a listing row.
	Label string

	// Help is a one-line description shown by /settings's own listing.
	Help string

	Kind SettingKind

	// Values is the enum's own closed vocabulary. Empty for SettingBool,
	// whose two legal strings ("true"/"false") never need repeating per
	// row.
	Values []string
}

// AllowedDisplay is Values for an enum, or the literal "true"/"false" for
// a bool -- what an "invalid value" notice lists as the accepted answers,
// so the message tells the user exactly what to type instead of just
// what was wrong.
func (s Setting) AllowedDisplay() []string {
	if s.Kind == SettingBool {
		return []string{"true", "false"}
	}
	return s.Values
}

// Valid reports whether value is a legal value for this setting's kind --
// exactly "true"/"false" for SettingBool (matching yesNo's own TOML-value
// vocabulary, internal/tui/configcmd.go, not a localized word), or one of
// Values for SettingEnum.
func (s Setting) Valid(value string) bool {
	switch s.Kind {
	case SettingBool:
		return value == "true" || value == "false"
	case SettingEnum:
		for _, v := range s.Values {
			if v == value {
				return true
			}
		}
	}
	return false
}

// Settings is the single source of truth /settings' listing and write
// path both read from -- the same "one table, never hand-duplicated"
// rule internal/slash.Commands already follows for the command registry
// itself. See this file's own package comment for why only these four
// keys are covered by this slice.
var Settings = []Setting{
	{Key: "ui.banner", Label: "banner", Help: "mostrar el logo de arranque", Kind: SettingBool},
	{Key: "ui.markdown", Label: "markdown", Help: "renderizar negritas/listas/enlaces en las respuestas", Kind: SettingBool},
	{Key: "ui.syntax", Label: "syntax", Help: "resaltar sintaxis en bloques de codigo", Kind: SettingBool},
	{
		Key:   "ui.reasoning",
		Label: "reasoning",
		Help:  "cuanto del razonamiento del modelo se muestra: off, collapsed o full",
		Kind:  SettingEnum,
		// Matches Root.cfgReasoning's own three-value vocabulary exactly
		// (internal/tui/root.go's doc comment on that field, and
		// internal/tui/chat.go's reasoningModeOr default).
		Values: []string{"off", "collapsed", "full"},
	},
}

// FindSetting looks up key in Settings, exact match (config.toml keys are
// not case-folded anywhere else in this package either).
func FindSetting(key string) (Setting, bool) {
	for _, s := range Settings {
		if s.Key == key {
			return s, true
		}
	}
	return Setting{}, false
}

// SetSetting validates key/value against the Settings table and writes
// the change into config.toml's [ui] table via readRawConfigTOML/
// writeRawConfigTOML -- see this file's own package comment for why that
// machinery, not a new one, is the right choice here. Every key this
// slice covers happens to live under [ui], the same flat table SetTheme
// already reads/writes; a future slice widening Settings to a nested
// table (e.g. [tools.evolve]) will need the same two-level read
// SetEvolveMode already demonstrates, not a change to this function's
// own shape.
func SetSetting(key, value string) error {
	setting, ok := FindSetting(key)
	if !ok {
		return fmt.Errorf("unknown setting %q", key)
	}
	if !setting.Valid(value) {
		return fmt.Errorf("invalid value %q for %s: must be one of %s",
			value, key, strings.Join(setting.AllowedDisplay(), ", "))
	}

	table, field, ok := strings.Cut(key, ".")
	if !ok {
		// Unreachable for anything in Settings today (every Key above
		// has a dot), but a defensive error beats a panic on
		// raw[key]-shaped nonsense if this table ever grows a
		// top-level-only entry by mistake.
		return errors.New("setting key must be \"table.field\"")
	}

	raw, err := readRawConfigTOML()
	if err != nil {
		return err
	}
	section, _ := raw[table].(map[string]any)
	if section == nil {
		section = map[string]any{}
	}
	if setting.Kind == SettingBool {
		section[field] = value == "true"
	} else {
		section[field] = value
	}
	raw[table] = section
	return writeRawConfigTOML(raw)
}

// effortcmd.go implements /effort (F9, docs/ROADMAP-ux-2026-08-20.md W5):
// the user's own choice of effort/thinking-level for the active model,
// alongside the EffortCycle chord (keys.go) that steps through the same
// vocabulary without typing a command at all.
//
// No argument reports the session's current effort level (or explains why
// there is none to report), mirroring runNameCommand's own
// "no argument -> read-only report" shape (namecmd.go) — the same
// precedent /theme's own no-argument listing and /name's own no-argument
// report already established for this package's single-value commands.
// An argument sets a new level, but — unlike /name, which accepts any
// string — the argument here is validated against the *active model's
// own* catalog.Model.EffortLevels vocabulary (effort.go's own field
// comment: "always per-model, never a fixed global list"), since a level
// this model's dialect has never heard of would either be silently
// dropped on the wire or, worse, rejected by the provider mid-turn. A
// model whose EffortLevels is empty (no reasoning at all, or only a
// toggle/budget_tokens control — the same field's own doc comment) has
// nothing here to set, and both forms say so honestly instead of
// pretending an override was applied.
//
// Root.effort itself is a plain string, not persisted anywhere: unlike
// /name's title or /theme's choice, an effort level is a per-turn request
// parameter (engine.Request.Params, via EffortFor below), not part of the
// session's own saved state — the same reasoning that keeps
// Root.compactModel/Root.system session-scoped rather than routed through
// any *Store seam.
package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// EffortResolver is F9's own §6.1 seam: internal/tui knows the active
// model's Ref (m.model) and the level the user asked for, but turning
// those into the engine.Request.Params override that actually reaches the
// wire needs the model's provider *dialect* (config.Provider.Kind) — a
// config concept this package does not resolve on its own (ModelPicker's
// own catalog.Model already carries Provider as a bare id string, not a
// resolved Kind). internal/app.NewEffortResolver is the real
// implementation, built over the exact same FindProvider path
// NewEngineFactory already walks for the same ref, so a turn's effort
// override and the engine it runs on can never disagree about which
// dialect they are both addressing.
//
// ref is a §4.2 Ref ("provider/model"), the same form engineFor's own
// parameter takes — never the wire ID. level is the exact string the user
// picked (already validated against the model's own EffortLevels by
// runEffortCommand/cycleEffort below, so this function does not
// re-validate it). The returned map is engine.Request.Params verbatim, or
// nil when nothing should be sent (an empty level, or a ref this resolver
// cannot place a provider for) — see app.EffortParams's own "silence, not
// refusal" doc comment for why a resolution failure here is not an error
// a turn should ever fail on.
//
// nil is a supported value, the same nil-factory discipline engineFor/
// reloadFor/pathLister already establish: every test in this package, and
// any caller with nothing wired, simply sends no effort override at all —
// the exact behaviour a session had before F9 existed.
type EffortResolver func(ref, level string) map[string]any

// effortLevelsFor returns the active model's own discrete effort-level
// vocabulary, or nil when the catalog has nothing for m.model (no catalog
// at all, or a model the catalog does not know — the same "leaves nothing
// trustworthy to compare against" case checkAutoCompact's own m.cat.Get
// guard already treats as "the feature simply does not fire").
func (m Root) effortLevelsFor(ref string) []string {
	model, ok := m.cat.Get(ref)
	if !ok {
		return nil
	}
	return model.EffortLevels
}

// runEffortCommand implements /effort's two behaviours: no argument
// reports the active model's current effort level and its full available
// vocabulary; an argument sets a new level, once matched
// case-insensitively against that same vocabulary.
func (m Root) runEffortCommand(args string) (tea.Model, tea.Cmd) {
	level := strings.TrimSpace(args)
	if level == "" {
		return m.reportEffort()
	}
	return m.setEffort(level)
}

// reportEffort is /effort's no-argument form. A model with no discrete
// effort levels at all (effortLevelsFor returns nil/empty) says so plainly
// rather than showing an empty list — the same "nothing to report" framing
// reportTitle uses for a session with no title yet.
func (m Root) reportEffort() (tea.Model, tea.Cmd) {
	g := m.lay.glyphs()
	levels := m.effortLevelsFor(m.model)
	if len(levels) == 0 {
		return m.slashNotice(g.warnMark + " " + m.model + " no expone niveles de esfuerzo discretos")
	}
	current := m.effort
	if current == "" {
		current = "(predeterminado del proveedor)"
	}
	return m.slashNotice(g.assistantMark + " esfuerzo: " + current +
		" — disponibles: " + strings.Join(levels, ", "))
}

// setEffort validates level against the active model's own EffortLevels
// (matchEffortLevel below) and, on a match, applies it immediately to
// Root.effort — read by startEngineTurn/startAgentTurn (root.go/
// agentturn.go) the next time either builds an engine.Request. There is
// no persistence store here (see this file's own package comment for
// why): the change is visible on the very next turn and lasts exactly as
// long as the running session does.
func (m Root) setEffort(level string) (tea.Model, tea.Cmd) {
	g := m.lay.glyphs()
	levels := m.effortLevelsFor(m.model)
	if len(levels) == 0 {
		return m.slashNotice(g.warnMark + " " + m.model + " no expone niveles de esfuerzo discretos")
	}
	matched, ok := matchEffortLevel(levels, level)
	if !ok {
		return m.slashNotice(g.warnMark + " nivel desconocido " + "\"" + level + "\"" +
			" — disponibles: " + strings.Join(levels, ", "))
	}
	m.effort = matched
	return m.slashNotice(g.assistantMark + " esfuerzo: " + matched)
}

// matchEffortLevel resolves want against levels case-insensitively,
// returning the catalog's own canonical spelling (never the user's raw
// casing) so Root.effort always carries exactly the string
// EffortResolver/EffortParams expects on the wire.
func matchEffortLevel(levels []string, want string) (string, bool) {
	for _, l := range levels {
		if strings.EqualFold(l, want) {
			return l, true
		}
	}
	return "", false
}

// effortParams is startEngineTurn's and startAgentTurn's shared last step
// before building an engine.Request: nil when nothing has been chosen
// (m.effort == "") or nothing is wired (m.effortFor == nil) — both
// legitimate "no override" states, not errors — otherwise exactly what
// m.effortFor resolves for the active model and the chosen level. Split
// out so both turn-start call sites ask the same question the same way,
// the same "one shared middle" discipline switchEngine/wireModel already
// established for the model-switch seam.
func (m Root) effortParams() map[string]any {
	if m.effort == "" || m.effortFor == nil {
		return nil
	}
	return m.effortFor(m.model, m.effort)
}

// cycleEffort implements the EffortCycle chord (keys.go): steps
// Root.effort forward through the active model's own EffortLevels,
// wrapping back to the first level after the last, and starting at the
// first level when nothing has been chosen yet this session. A model with
// no discrete levels leaves m unchanged — the same "nothing to cycle
// through" case reportEffort/setEffort already report explicitly when
// reached via /effort instead, but here (a bare keypress, not a typed
// command) there is nowhere to print an explanation, so silence is the
// correct degrade: a chord that does nothing is a better failure than one
// that opens an error dialog over whatever the user was doing.
func (m Root) cycleEffort() Root {
	levels := m.effortLevelsFor(m.model)
	if len(levels) == 0 {
		return m
	}
	if m.effort == "" {
		m.effort = levels[0]
		return m
	}
	for i, l := range levels {
		if strings.EqualFold(l, m.effort) {
			m.effort = levels[(i+1)%len(levels)]
			return m
		}
	}
	// m.effort held a value that is no longer in this model's own
	// vocabulary (a model switch moved to one with a different set) —
	// restart from the first level rather than leaving a stale value
	// that would still masquerade as valid the next time EffortFor is
	// called.
	m.effort = levels[0]
	return m
}

// confirm.go implements the §9.5 conflict dialog: the overlay ModeConfirm
// draws when engine.CheckSwap (Step 11, §4.6) reports a Plan that is not OK.
// Like Picker, it is a value type — every method takes a confirmDialog and
// returns the next one — and it never talks to engine.CheckSwap itself: Root
// calls that in applyModelChosen and hands this component the resulting
// engine.Plan to render and to walk with the keyboard.
package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/catalog"
	"github.com/MichiTrader/ishakat/internal/convo"
	"github.com/MichiTrader/ishakat/internal/engine"
)

// confirmOption is one selectable row of the dialog. action is only
// meaningful for the two mechanical remedies (compact, drop the oldest
// turns) — a row offering ActionCancel just closes the dialog. proceed is
// the one row that has no engine.Action of its own: "switch anyway" past a
// capability warning, a TUI-only extension over §4.6's literal three-action
// sketch (there is nothing for engine.CheckSwap to compute here — the
// switch just happens, unmodified).
type confirmOption struct {
	action  engine.Action
	proceed bool
	label   string
}

// confirmDialog is ModeConfirm's own state.
type confirmDialog struct {
	from, to catalog.Model
	plan     engine.Plan
	options  []confirmOption
	sel      int
}

// newConfirmDialog builds the dialog from the plan CheckSwap already
// produced. It never calls CheckSwap itself — Root did that once, in
// applyModelChosen, and handing the same Plan through keeps the dialog from
// ever disagreeing with the check that opened it.
func newConfirmDialog(from, to catalog.Model, plan engine.Plan) confirmDialog {
	return confirmDialog{from: from, to: to, plan: plan, options: confirmOptionsFor(plan)}
}

// confirmOptionsFor decides which rows the dialog offers, by conflict
// priority — a Plan can carry more than one Conflict at once, and only one
// row set can be shown. A context conflict is the one this package can
// remedy mechanically (§4.6): compact now (Step 12's startCompact, a real
// compact_model summary) or drop the oldest turns outright, exactly the two
// choices §9.5's wireframe draws; it takes
// priority because switching without fixing it would just fail on the very
// next request. Failing that, a missing credential (NoAuth) also offers only
// cancel: §4.6 says the credential has to exist "before you're allowed to
// switch", so there is nothing to proceed with, and it takes priority over a
// mere capability warning for the same reason — proceeding could not
// possibly work without it. Only once neither of those applies does a
// capabilities-only conflict get its own row: §4.6 says those blocks degrade
// to descriptive text rather than breaking the request, which makes
// proceeding a legitimate choice once the warning has been read.
func confirmOptionsFor(plan engine.Plan) []confirmOption {
	switch {
	case plan.Has(engine.ContextTooSmall):
		return []confirmOption{
			{action: engine.ActionCompact, label: fmt.Sprintf("compactar y cambiar  (~%s)", formatContextTokens(plan.EstAfter))},
			{action: engine.ActionDropOldest, label: "cambiar y recortar los turnos más viejos"},
			{action: engine.ActionCancel, label: "cancelar"},
		}
	case plan.Has(engine.NoAuth):
		return []confirmOption{{action: engine.ActionCancel, label: "cancelar"}}
	case plan.Has(engine.MissingCaps):
		return []confirmOption{
			{proceed: true, label: "cambiar de todos modos"},
			{action: engine.ActionCancel, label: "cancelar"},
		}
	default:
		// Unreachable in practice — newConfirmDialog is only ever built from
		// a non-OK Plan, and CheckSwap never returns one with zero
		// Conflicts — but a bare cancel is the only safe default for a
		// Plan.OK dialog that should not have opened in the first place.
		return []confirmOption{{action: engine.ActionCancel, label: "cancelar"}}
	}
}

// moveSel moves the selection by delta rows, wrapping like Picker.moveSel.
func (d confirmDialog) moveSel(delta int) confirmDialog {
	if len(d.options) == 0 {
		return d
	}
	n := len(d.options)
	d.sel = ((d.sel+delta)%n + n) % n
	return d
}

// selected is the option under the cursor. Every dialog has at least one row
// (confirmOptionsFor never returns an empty slice), so there is no empty
// case to guard here the way Picker.selected asks its callers to.
func (d confirmDialog) selected() confirmOption { return d.options[d.sel] }

// updateConfirm handles every key while mode == ModeConfirm (§9.5). Like
// updatePicker it owns the keyboard outright: there is nothing underneath it
// to fall through to.
func (m Root) updateConfirm(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	switch keyPressString(key) {
	case m.keys.Cancel:
		m.mode = ModeChat
		m.confirm = confirmDialog{}
		return m, nil
	case "up":
		m.confirm = m.confirm.moveSel(-1)
		return m, nil
	case "down":
		m.confirm = m.confirm.moveSel(1)
		return m, nil
	case m.keys.Submit:
		return m.resolveConfirm()
	}
	return m, nil
}

// resolveConfirm applies whichever option was selected. The proceed and
// drop-oldest remedies are instant and finish the switch right here, the
// same way an unconflicted applyModelChosen always has (§4.6's confirmation
// line included); the compact remedy hands off to startCompact (Step 12),
// which is asynchronous — it draws its own ModeCompact screen and finishes
// the switch itself once engine.Summarize answers (see compact.go's
// finishCompact).
func (m Root) resolveConfirm() (tea.Model, tea.Cmd) {
	opt := m.confirm.selected()
	to := m.confirm.to
	m.confirm = confirmDialog{}

	switch {
	case opt.proceed:
		// "Switch anyway" past a caps warning: nothing to mutate, the
		// conversation's history is unchanged and only future turns degrade.
		m.mode = ModeChat
	case opt.action == engine.ActionCompact:
		return m.startCompact(to.Ref)
	case opt.action == engine.ActionDropOldest:
		m.applyDropOldest(to.EffectiveContext())
		m.mode = ModeChat
	default: // engine.ActionCancel
		m.mode = ModeChat
		return m, nil
	}

	// commitModelSwitch (root.go) is the same rebind applyModelChosen's own
	// unconflicted path uses — see switchEngine's comment for why relabeling
	// alone used to leave every subsequent turn on the wrong provider.
	return m.commitModelSwitch(to.Ref)
}

// applyDropOldest discards the oldest messages until the conversation fits
// under window, using the exact same "append a marker, never delete" shape
// compaction uses (convo.ApplySummary) so both remedies are equally
// auditable from the JSONL later — the only difference is the marker names
// itself as a discard rather than a summary.
func (m *Root) applyDropOldest(window int) {
	idx := convo.DropOldest(m.conv.Messages, window)
	if len(idx) == 0 {
		return
	}
	m.conv.ApplySummary(convo.Plan{Replace: idx}, "(turnos más viejos descartados al cambiar de modelo)", "")
}

// renderConfirm draws the §9.5 overlay. Like renderPicker it replaces the
// whole live region — there is nothing left to type into chat while this
// dialog owns the keyboard — and, also like renderPicker, it draws no box
// border: this package's glyph table (glyphs.go) has no corner/side
// characters, and inventing one for a single screen is exactly the
// temptation that file's own comment warns against.
func (m Root) renderConfirm() string {
	g := m.lay.glyphs()
	width := m.lay.ContentWidth()
	d := m.confirm

	var b strings.Builder
	b.WriteString(" cambiar modelo\n")
	fmt.Fprintf(&b, " de  %s   %s\n", d.from.Display(), contextLabel(d.from))
	fmt.Fprintf(&b, " a   %s   %s\n", d.to.Display(), contextLabel(d.to))
	b.WriteString(" " + strings.Repeat(g.rule, width-1) + "\n")

	for _, line := range confirmConflictLines(g, d.plan) {
		b.WriteString(" " + line + "\n")
	}

	b.WriteString(" " + strings.Repeat(g.rule, width-1) + "\n")
	for i, opt := range d.options {
		pointer := " "
		if i == d.sel {
			pointer = g.inputPrefix
		}
		line := pointer + " " + opt.label
		if i == d.sel {
			line = m.styles.Accent.Render(line)
		}
		b.WriteString(" " + line + "\n")
	}

	b.WriteString(" " + strings.Repeat(g.rule, width-1) + "\n")
	fmt.Fprintf(&b, " %s move  enter choose  esc cancel\n", g.scrollHint)
	return b.String()
}

// confirmConflictLines renders every conflict in the plan as one honest,
// human-readable line — never a bare "conflict", always what specifically
// will not work and why, the same rule §4.5 already holds for "model not
// found".
func confirmConflictLines(g glyphs, plan engine.Plan) []string {
	var lines []string
	for _, c := range plan.Conflicts {
		switch c.Kind {
		case engine.ContextTooSmall:
			lines = append(lines, fmt.Sprintf("%s la conversación usa %s tok y no cabe en %s.",
				g.warnMark, formatContextTokens(c.Tokens), formatContextTokens(c.Window)))
		case engine.MissingCaps:
			lines = append(lines, fmt.Sprintf("%s %s se van a degradar a texto descriptivo.",
				g.warnMark, missingCapsLabel(c.Missing)))
		case engine.NoAuth:
			lines = append(lines, fmt.Sprintf("%s falta credencial para este proveedor.", g.warnMark))
		}
	}
	return lines
}

// missingCapsLabel names, in Spanish prose matching the rest of this
// dialog's copy, which kinds of content the destination model cannot serve.
func missingCapsLabel(c catalog.Caps) string {
	var parts []string
	if c.Vision {
		parts = append(parts, "las imágenes")
	}
	if c.Tools {
		parts = append(parts, "los resultados de herramientas")
	}
	if len(parts) == 0 {
		return "algunos bloques"
	}
	return strings.Join(parts, " y ")
}

// toolscope.go implements §21.6's own second dialog (Step 31, part 6):
// "Tools for this mission", the tool-scope proposal shown alongside the
// constraint-confirmation dialog mission.go already implements (part 2).
// Where mission.go asks "should this constraint be enforced", this file
// asks "which tools may this mission use at all" — §21.6's own "auto
// proposes, the human confirms or corrects, and confirming costs one
// keystroke" line.
//
// Like mission.go, this is the dialog half only: internal/mission.ProposeTools
// (Step 31 part 5) already compiled the pure data this file renders and
// reacts to. This file adds no new compilation logic of its own — it is
// purely a presentation and keyboard-handling layer over ToolScope, the
// exact "compile now, dialog later" split part 1 → part 2 already
// established for the section's other mockup.
//
// This dialog is chained after ModeMission, not offered independently:
// per checkMission's own doc comment and §21.6's own three stated
// triggers, "the goal contains a constraint" is the one trigger this
// codebase can act on today (the other two — "a capability outside
// current policy" and explicit "/plan" — have no Go concept to compile
// against yet, see internal/mission.ProposeTools' own doc comment). A
// goal that opens ModeMission at all has, by definition, already met that
// one trigger, so resolveMission's own tail is where this dialog opens
// next rather than a second, independent check duplicating checkMission's
// own gate.
package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/mission"
)

// toolScopeOption is one selectable row of the dialog, in §21.6's own
// mockup order.
type toolScopeOption int

const (
	// toolScopeAsProposed is row 1, "As proposed  ✓" — auto's own
	// decision, preselected, matching §21.6's own "Option 1 is auto's
	// decision, preselected" line.
	toolScopeAsProposed toolScopeOption = iota
	// toolScopeProposedPlusBrowser is row 2, "Proposed + browser" — what
	// auto considered and rejected, offered here with its own reason
	// shown (BrowserWeightMB), per §21.6's "declining to hide the
	// reasoning is what makes the human's mental model correct" line.
	// Applying it widens the proposed set with the same three named
	// browser-automation tools internal/mission's own keywordRules table
	// already names (playwright, puppeteer, selenium) — see
	// resolveToolScope's own comment for why this pass has nothing yet
	// to apply that widening *to*.
	toolScopeProposedPlusBrowser
	// toolScopeEverything is row 3, "Everything installed" — §21.6's own
	// "allow them all" reading: never a silent new download, invariants
	// still apply.
	toolScopeEverything
	// toolScopePickOneByOne is row 4, "Pick one by one..." — Kiro's own
	// manual checkbox list. This pass has no free-standing checkbox
	// widget yet, the same "not invented here half-finished" reasoning
	// mission.go's own missionAdjust gives for its own row 2 — so, like
	// that row, it resolves to the same safe default as Esc for now
	// rather than opening a second, unbuilt dialog.
	toolScopePickOneByOne
)

// toolScopeOptionLabels is §21.6's own four rows, in its own order.
var toolScopeOptionLabels = []string{
	"1. As proposed",
	"2. Proposed + browser",
	"3. Everything installed",
	"4. Pick one by one...",
}

// toolScopeDialogDefault is the row Esc resolves to — the same "Esc
// defaults to the safer option" rule §21.4 states for trust.go, applied
// here the same way missionDialogDefault applies it for mission.go: the
// safer choice never silently widens past what auto itself proposed, so
// Esc resolves to toolScopeAsProposed (row 0) rather than any row that
// grants more than auto's own considered recommendation.
const toolScopeDialogDefault = toolScopeAsProposed

// toolScopeDialog is ModeToolScope's own state, live only while
// mode == ModeToolScope. Like missionDialog, this is a value type
// (compared to trustDialog's own copy-safe shape) — cheap to copy in and
// out of Root the same way every other overlay's own state already is.
type toolScopeDialog struct {
	s   mission.ToolScope
	sel int
}

// newToolScopeDialog builds the dialog directly from a
// mission.ProposeTools result. Unlike newMissionDialog, there is no
// HasDeny()-equivalent precondition to have already checked here: §21.6's
// own worked example proposes a scope for *every* goal that reaches this
// dialog, constrained or not — a scope with an empty BashAllow can never
// happen (ProposeTools always falls back to defaultBashAllow), so there
// is no "nothing to propose" case this constructor needs to refuse.
func newToolScopeDialog(s mission.ToolScope) toolScopeDialog {
	return toolScopeDialog{s: s}
}

// moveSel moves the selection by delta rows, wrapping like every other
// dialog's own moveSel (trust.go, mission.go, suggest.go).
func (d toolScopeDialog) moveSel(delta int) toolScopeDialog {
	n := len(toolScopeOptionLabels)
	d.sel = ((d.sel+delta)%n + n) % n
	return d
}

// openToolScope opens ModeToolScope for the goal resolveMission just
// finished handling, preselecting toolScopeAsProposed the same way
// newMissionDialog's own caller leaves row 0 selected by default. Called
// only from resolveMission's own tail — see this file's own package
// comment for why this dialog is chained rather than independently
// triggered.
//
// It re-sets m.missionText itself rather than trusting the caller to have
// left it populated: resolveMission clears m.mission but hands goal in as
// this call's own argument, so this dialog has to be the one to put the
// deferred text back where resolveToolScope (below) — and, transitively,
// m.submit itself — already know to look for it. This mirrors
// checkMission's own role for ModeMission: the function that opens a
// dialog is the one responsible for leaving m.missionText correct for
// whatever eventually closes it, not an invariant a caller has to uphold
// from outside.
func (m Root) openToolScope(goal string) Root {
	m.missionText = goal
	m.toolScope = newToolScopeDialog(mission.ProposeTools(goal))
	m.mode = ModeToolScope
	return m
}

// updateToolScope handles every key while mode == ModeToolScope. Like
// updateMission it owns the keyboard outright, and Cancel resolves to
// toolScopeDialogDefault rather than simply discarding — see that
// constant's own doc comment for why "do nothing" is not a safe default
// here either.
func (m Root) updateToolScope(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	switch keyPressString(key) {
	case m.keys.Cancel:
		return m.resolveToolScope(toolScopeDialogDefault)
	case "up":
		m.toolScope = m.toolScope.moveSel(-1)
		return m, nil
	case "down":
		m.toolScope = m.toolScope.moveSel(1)
		return m, nil
	case m.keys.Submit:
		return m.resolveToolScope(toolScopeOption(m.toolScope.sel))
	}
	return m, nil
}

// resolveToolScope applies the chosen option, then always starts the turn
// resolveMission itself already paused for and handed forward via
// openToolScope's own m.missionText re-set — mirroring resolveMission's
// own "opened in the middle of submit" shape one level further in: two
// dialogs have now closed in sequence, but only one turn has ever been
// paused, and this is what finally starts it.
//
// No option here actually restricts or widens what tools.Registry exposes
// or what Guard allows yet — see this file's own package comment for why:
// there is no wiring from a chosen ToolScope into a real session's
// Registry/Guard in this pass, the same "compiler and dialog before
// enforcement" order part 1's own Mission/Guard split already followed
// (Compile existed for a full pass before hardDeny's own missionHardDeny
// enforced anything it produced). Every option below therefore only
// closes the dialog and starts the turn — the option chosen is not yet
// observable in what the turn can call, which is the next Step 31 slice
// this pass leaves explicitly open, not silently.
func (m Root) resolveToolScope(opt toolScopeOption) (tea.Model, tea.Cmd) {
	text := m.missionText
	m.toolScope = toolScopeDialog{}
	m.missionText = ""
	m.mode = ModeChat
	_ = opt // no session-scoping effect yet; see this method's own comment
	return m.submit(text)
}

// renderToolScope draws §21.6's own mockup: the proposed base capabilities
// and bash subcommand scope, the browser-widen option with its own shown
// reason (only when BrowserOffered), the everything-installed option, and
// the manual pick-one-by-one option. Like renderMission it replaces the
// whole live region and draws no box border.
func (m Root) renderToolScope() string {
	g := m.lay.glyphs()
	width := m.lay.ContentWidth()
	d := m.toolScope

	var b strings.Builder
	b.WriteString(" " + strings.Repeat(g.rule, width-1) + "\n")
	b.WriteString(" Tools for this mission\n\n")
	b.WriteString(" Proposed from your goal:\n")

	for i, label := range toolScopeOptionLabels {
		pointer := " "
		if i == d.sel {
			pointer = g.inputPrefix
		}
		line := pointer + " " + label
		if toolScopeOption(i) == toolScopeAsProposed {
			line += "  \u2713"
		}
		if i == d.sel {
			line = m.styles.Accent.Render(line)
		}
		b.WriteString(" " + line + "\n")

		switch toolScopeOption(i) {
		case toolScopeAsProposed:
			fmt.Fprintf(&b, "      %s\n", strings.Join(d.s.Base, " \u00b7 "))
			fmt.Fprintf(&b, "      bash(%s)\n", strings.Join(d.s.BashAllow, ", "))
		case toolScopeProposedPlusBrowser:
			if !d.s.BrowserOffered {
				continue
			}
			fmt.Fprintf(&b, "      %s ~%d MB download; your phone\n", g.warnMark, d.s.BrowserWeightMB)
			b.WriteString("        will struggle\n")
		case toolScopeEverything:
			b.WriteString("      (invariants still apply)\n")
		}
	}

	b.WriteString("\n " + strings.Repeat(g.rule, width-1) + "\n")
	fmt.Fprintf(&b, " %s move  enter choose  esc = 1\n", g.scrollHint)
	return b.String()
}

// toolscope.go implements §21.6's own second dialog: "Tools for this
// mission", the tool-scope proposal shown alongside the
// constraint-confirmation dialog mission.go already implements (part 2).
// Where mission.go asks "should this constraint be enforced", this file
// asks "which tools may this mission use at all" — §21.6's own "auto
// proposes, the human confirms or corrects, and confirming costs one
// keystroke" line. The dialog itself (rendering, keyboard handling)
// landed in Step 31 part 6; resolveToolScope's own wiring into a real
// session's Guard (the bash-subcommand half of ToolScope — see that
// function's own doc comment for why Base is not restricted by this pass)
// landed in part 7.
//
// Like mission.go, this file adds no new compilation logic of its own:
// internal/mission.ProposeTools (Step 31 part 5) already compiled the pure
// data this file renders and reacts to — this is purely a presentation,
// keyboard-handling, and (as of part 7) Guard-wiring layer over ToolScope,
// the exact "compile now, dialog later, wire enforcement last" order part
// 1 → part 2 already established for the section's other mockup (Compile,
// then ModeMission, then hardDeny's own missionHardDeny enforcement).
//
// This dialog has two ways to open, both of §21.6's own three stated
// triggers that this codebase can act on today (the third, explicit
// "/plan", still has no Go concept to compile against — not yet a real
// slash.Kind, per Step 32's own still-⬜ row): "the goal contains a
// constraint" chains it after ModeMission (resolveMission's own tail,
// below — a goal that opens ModeMission at all has, by definition,
// already met that trigger, so there is no second, independent check
// duplicating checkMission's own gate for it); "the mission requests a
// capability outside current policy" opens it directly, via
// checkToolPolicy (below), for a goal that never opened ModeMission at
// all (no negated constraint to confirm, only a scope proposal to show).
// §21.6's own mockup prose places its "this dialog is not shown for
// every task... appears when..." sentence immediately after describing
// this dialog, not the first one, which is why both triggers land here
// rather than on ModeMission: there is no constraint to confirm in the
// policy-collision case, only a scope worth surfacing.
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

// checkToolPolicy is submit's own second pre-flight hook, a sibling of
// checkMission for §21.6's own second stated trigger ("the mission
// requests a capability outside current policy") — now that
// internal/mission.OutsidePolicy (Step 31 part 8) exists to answer it.
// Only called when checkMission itself returned ok == false: a goal
// that already opened ModeMission has, by definition, already reached
// this dialog too, via resolveMission's own unconditional tail, so
// checking again here would open ModeToolScope a second time for the
// same goal.
//
// m.missionPolicy == nil (every test in this package that never sets
// Options.MissionPolicy, and any real caller not yet updated) means this
// trigger never fires — see Root.missionPolicy's own doc comment for why
// that is the correct degradation, not mission.Policy{}'s own zero
// value. Only once a policy is actually wired is text compiled through
// mission.OutsidePolicy against *m.missionPolicy and, only when it
// reports true, ModeToolScope opens directly via openToolScope, exactly
// the same dialog resolveMission's own tail already opens for the other
// trigger. A goal that does not collide with policy (the ordinary case:
// OutsidePolicy itself already excludes a goal with no recognized
// keyword, one that only affirms an unrelated technology, or one that
// negates the keyword entirely -- see that function's own doc comment)
// returns ok == false and the caller proceeds exactly as before this
// function existed, the same "gate the existing submit call on this
// function's ok return" contract checkMission's own doc comment already
// states.
func (m Root) checkToolPolicy(text string) (Root, bool) {
	if m.missionPolicy == nil {
		return m, false
	}
	if !mission.OutsidePolicy(text, *m.missionPolicy) {
		return m, false
	}
	return m.openToolScope(text), true
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

// browserBashAllow is the fixed, documented set of extra bash prefixes
// "2. Proposed + browser" adds on top of the proposed BashAllow — the
// same "small, named table a human confirming can trust completely, not a
// general classifier" discipline internal/mission's own keywordRules and
// bashSubcommandKeywords already state for themselves. "npx" is included
// because that is how a browser-automation tool is actually invoked in
// practice ("npx playwright test"), not merely its own bare name; the
// three named tools are included too for the less common case of a
// globally-installed binary invoked directly. This intentionally
// duplicates a subset of internal/mission's own browserKeywords rather
// than importing it as a shape to widen with, because that var names goal
// *keywords* to scan text for (including the generic alias "browser",
// which is not itself an invokable command), while this one names actual
// bash-invokable prefixes — two different vocabularies that happen to
// overlap on three entries, not one the other can be derived from.
var browserBashAllow = []string{"npx", "playwright", "puppeteer", "selenium"}

// resolveToolScope applies the chosen option to the real session's Guard
// (Step 31 part 7: wiring a chosen tool scope into a real session,
// picking up where part 6 left off — see this function's git history for
// that pass's own version of this comment, describing why nothing was
// wired yet), then always starts the turn resolveMission itself already
// paused for and handed forward via openToolScope's own m.missionText
// re-set — mirroring resolveMission's own "opened in the middle of
// submit" shape one level further in: two dialogs have now closed in
// sequence, but only one turn has ever been paused, and this is what
// finally starts it.
//
// This wires the bash-subcommand half of §21.6's second mockup only —
// mission.ToolScope.Base ("read · edit · dispatch") is not restricted by
// any option here, because every option in the mockup itself proposes the
// exact same Base regardless of which row is chosen (compare the mockup's
// own four rows: only the bash scope and the browser/everything additions
// ever change). Restricting Base would mean refusing read/edit/dispatch
// outright for some option, which is not what any of the four rows
// actually describe. A future pass that adds a mockup row meaning
// "restrict Base too" would need a second Guard-side mechanism the same
// shape as this one; this pass wires the one axis §21.6's own four rows
// actually vary today.
//
// m.missionGuard == nil (every test in this package that calls
// newMissionRoot(nil), and any real caller that never wires
// Options.MissionGuard) degrades the same way resolveMission's own
// missionAccept branch already does: the dialog still closes and the turn
// still starts, it simply enforces nothing.
func (m Root) resolveToolScope(opt toolScopeOption) (tea.Model, tea.Cmd) {
	text := m.missionText
	scope := m.toolScope.s
	m.toolScope = toolScopeDialog{}
	m.missionText = ""
	m.mode = ModeChat

	if m.missionGuard != nil {
		switch opt {
		case toolScopeAsProposed:
			m.missionGuard.SetBashScope(scope.BashAllow)
		case toolScopeProposedPlusBrowser:
			m.missionGuard.SetBashScope(append(append([]string(nil), scope.BashAllow...), browserBashAllow...))
		case toolScopeEverything:
			// "invariants still apply" (§21.6's own mockup line) — nil
			// clears any bash-subcommand restriction, but AddMissionRules'
			// own deny rules (checked first inside hardDeny, see that
			// method's own doc comment) are never touched by this call,
			// so a stated "no Playwright" constraint still refuses it even
			// under "Everything installed".
			m.missionGuard.SetBashScope(nil)
		case toolScopePickOneByOne:
			// No free-standing checkbox widget yet (see this option's own
			// doc comment on toolScopeOption) — resolves to the same
			// scope as toolScopeAsProposed for now, the identical "not
			// invented here half-finished" deferral missionAdjust already
			// applies for §21.6's first dialog.
			m.missionGuard.SetBashScope(scope.BashAllow)
		}
	}

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

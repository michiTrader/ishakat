// mission.go implements §21.6's own confirmation dialog (Step 31, part 2):
// once internal/mission.Compile has turned a stated goal into a Mission
// carrying at least one negated Constraint, this file is what shows the
// human the exact rules that are about to be enforced and applies them
// with one keystroke — never silently, per §21.15's own risk table ("show
// the compiled rule and require confirmation; never compile silently").
//
// Unlike trust.go's ModeTrust, this package is allowed to hold both
// internal/mission's pure Mission/Rule values *and* internal/permissions'
// MissionRule directly: internal/tui already imports internal/permissions
// for ModeToolApprove's own Request/Decision/Tier vocabulary
// (toolapprove.go), so importing internal/mission's own equally pure,
// equally presentation-free Mission/Rule types alongside it does not cross
// any new boundary — the one rule §6.1 actually enforces here
// (TestMissionStaysPureAndDoesNotImportPermissions, internal/arch_test.go)
// is that internal/mission itself never imports internal/permissions, not
// that no importer may use both.
//
// MissionGuard below is still a seam, not a bare *permissions.Guard field,
// for the same reason TrustStore/EvolveStore/ToolsLister already are ones:
// a fake in this package's own tests should never have to build a real
// Guard (with its own permissions.Config, tiers, reviewer, mutex) just to
// prove resolveMission calls AddMissionRules with the right values.
package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/mission"
	"github.com/MichiTrader/ishakat/internal/permissions"
)

// MissionGuard is §21.4 layer 4's own persistence seam, now backing both of
// §21.6's dialogs (not just its first): AddMissionRules (guard.go, added
// alongside MissionRule in Step 31 part 1) to make a confirmed mission's
// deny rules actually enforced for the rest of the session, MissionRules
// (added in the same part 1 pass, but with no caller in this package until
// Step 31 part 3) to read them back for display, the exact use its own doc
// comment names: "used by a caller wanting to display 'no browser · no
// network' the way §21.11's own sub-agent mockup shows it on the
// children" — and SetBashScope (added in Step 31 part 7, toolscope.go's own
// resolveToolScope) to make a chosen tool-scope option actually restrict
// which bash subcommands the rest of the session may run.
// *permissions.Guard already satisfies this exactly as it stands — no
// adapter type is needed, unlike TrustStore's fileTrustStore, because every
// method already takes and returns exactly the shape this package needs,
// the same "the real type already fits" shortcut EvolveStore's own comment
// notes is not always available but is here.
type MissionGuard interface {
	AddMissionRules([]permissions.MissionRule)
	MissionRules() []permissions.MissionRule
	SetBashScope(allow []string)
}

// missionOption is one selectable row of the dialog, in §21.6's own mockup
// order ("1. That's right", "2. Adjust the rule", "3. Just a preference").
type missionOption int

const (
	// missionAccept applies every compiled deny rule verbatim via
	// MissionGuard.AddMissionRules — the one-keystroke path §21.6's own
	// "One keystroke. From then on the constraint is enforced by Go, not
	// by the model's good intentions" line describes.
	missionAccept missionOption = iota
	// missionAdjust is §21.6's "2. Adjust the rule" row. This pass has no
	// free-text/per-rule editing widget yet — the same "not invented here
	// half-finished" reasoning trust.go's own package comment gives for
	// its own option 4 — so, for now, it resolves to the same safe
	// default as Esc: nothing is silently widened, and adjusting a
	// specific pattern is left to a future pass, exactly like trust.go's
	// own "Type something..." row defers a real text widget.
	missionAdjust
	// missionSoft is §21.6's "3. Just a preference (ask me if it comes
	// up)" row: the constraint is real but not absolute, so no Guard rule
	// is added at all — the human is trusting the model's own judgment
	// rather than Go's enforcement for this one. §21.6's own prose: "some
	// constraints genuinely are soft preferences, and saying so honestly
	// is better than pretending everything is absolute."
	missionSoft
)

// missionOptions is §21.6's own three rows, in its own order.
var missionOptionLabels = []string{
	"1. That's right",
	"2. Adjust the rule",
	"3. Just a preference",
}

// missionDialogDefault is the row Esc resolves to — the same "Esc defaults
// to the safer option" rule §21.4 states for trust.go's own dialog. Here
// the safer choice is missionAdjust (index 1): closing the dialog without
// reading it must never silently apply a rule the human never actually
// confirmed (missionAccept), and must also never silently discard a real
// constraint as if it were merely a soft preference (missionSoft) — both
// of those are decisions, and Esc is "I did not decide", which is
// closest to "let me look again", not to either extreme.
const missionDialogDefault = missionAdjust

// missionDialog is ModeMission's own state, live only while
// mode == ModeMission.
type missionDialog struct {
	m   mission.Mission
	sel int
}

// newMissionDialog builds the dialog directly from a mission.Compile
// result. It is the caller's job (checkMission, below) to have already
// verified m.HasDeny() — this constructor does not re-check, the same way
// newConfirmDialog never re-calls CheckSwap itself.
func newMissionDialog(m mission.Mission) missionDialog {
	return missionDialog{m: m}
}

// moveSel moves the selection by delta rows, wrapping like every other
// dialog's own moveSel (trust.go, confirm.go, suggest.go).
func (d missionDialog) moveSel(delta int) missionDialog {
	n := len(missionOptionLabels)
	d.sel = ((d.sel+delta)%n + n) % n
	return d
}

// checkMission is submit's own pre-flight hook (§21.6: "the dialog is not
// shown for every task... it appears when the goal contains a
// constraint"): it compiles text through mission.Compile and, only when
// the result carries at least one negated constraint, opens ModeMission
// in place of starting the turn immediately. A goal with no recognized
// constraint (mission.Mission.HasDeny() == false — the ordinary case for
// most goals, per mission.Compile's own doc comment) returns ok == false
// and the caller proceeds exactly as before this step existed.
//
// This does not itself decide what happens to text once the mission is
// resolved — resolveMission (below) is what finally calls m.submit(text)
// — so a caller only has to gate its existing submit call on this
// function's ok return, not restructure how a turn starts.
func (m Root) checkMission(text string) (Root, bool) {
	compiled := mission.Compile(text)
	if !compiled.HasDeny() {
		return m, false
	}
	m.mission = newMissionDialog(compiled)
	m.missionText = text
	m.mode = ModeMission
	return m, true
}

// updateMission handles every key while mode == ModeMission. Like
// updateTrust it owns the keyboard outright, and Cancel resolves to
// missionDialogDefault rather than simply discarding — see that
// constant's own doc comment for why "do nothing" is not a safe default
// here.
func (m Root) updateMission(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	switch keyPressString(key) {
	case m.keys.Cancel:
		return m.resolveMission(missionDialogDefault)
	case "up":
		m.mission = m.mission.moveSel(-1)
		return m, nil
	case "down":
		m.mission = m.mission.moveSel(1)
		return m, nil
	case m.keys.Submit:
		return m.resolveMission(missionOption(m.mission.sel))
	}
	return m, nil
}

// resolveMission applies the chosen option, then always chains into
// ModeToolScope (Step 31 part 6, toolscope.go) rather than calling
// m.submit(text) directly — §21.6's own second dialog ("Tools for this
// mission") has no independent trigger of its own to check (see
// ModeToolScope's own doc comment on root.go): the one trigger this
// codebase can act on today, "the goal contains a constraint", is exactly
// what already opened ModeMission, regardless of which of its three rows
// was chosen. So every outcome here — missionAccept, missionAdjust, and
// missionSoft alike — opens the tool-scope dialog next; none of them
// starts the turn directly anymore. openToolScope itself is what finally
// re-captures the deferred text this dialog paused, the same
// m.missionText seam resolveMission always used, just handed forward one
// dialog further instead of consumed here.
func (m Root) resolveMission(opt missionOption) (tea.Model, tea.Cmd) {
	compiled := m.mission.m
	text := m.missionText
	m.mission = missionDialog{}
	m.mode = ModeChat

	if opt == missionAccept && m.missionGuard != nil {
		m.missionGuard.AddMissionRules(denyRulesOf(compiled))
	}
	// missionAdjust and missionSoft both apply no rule at all for this
	// pass — see missionAdjust's and missionSoft's own doc comments for
	// why that is each one's correct behaviour today, not a shortcut
	// shared by accident.

	return m.openToolScope(text), nil
}

// missionRulesOr degrades a nil m.missionGuard to "no rules in effect",
// mirroring resolveMission's own "if opt == missionAccept && m.missionGuard
// != nil" nil check: every test in this package, and any caller that never
// wires Options.MissionGuard, must see toolActivityLines render exactly as
// it did before this method existed, not panic on a nil interface value.
func (m Root) missionRulesOr() []permissions.MissionRule {
	if m.missionGuard == nil {
		return nil
	}
	return m.missionGuard.MissionRules()
}

// denyRulesOf converts every negated Constraint's Rules in mn into the
// permissions.MissionRule shape Guard.AddMissionRules expects — the one
// field-by-field bridge internal/mission's own doc comment on Rule
// promises exists so neither package has to import the other (§6.1,
// TestMissionStaysPureAndDoesNotImportPermissions). internal/tui is
// allowed to be this bridge for the exact same reason internal/app
// usually is: this package already imports both types directly (see this
// file's own package comment), and the alternative — inventing a third,
// shared type neither internal/mission nor internal/permissions would
// ever import either — buys nothing over one small conversion function.
//
// Only "deny"-effect Rules convert. An "allow"-effect Rule (the §21.6
// inverse example, "use Playwright if you think it helps") is
// intentionally left out: MissionRule only ever narrows (guard.go's own
// missionHardDeny is a pure deny list), so there is nothing for an allow
// rule to apply — see mission.Constraint's own doc comment for why that is
// by design, not a gap.
func denyRulesOf(mn mission.Mission) []permissions.MissionRule {
	var out []permissions.MissionRule
	for _, c := range mn.Constraints {
		if !c.Negated {
			continue
		}
		for _, r := range c.Rules {
			if r.Effect != "deny" {
				continue
			}
			out = append(out, permissions.MissionRule{Capability: r.Capability, Pattern: r.Pattern})
		}
	}
	return out
}

// renderMission draws §21.6's own mockup: the goal, one line per detected
// constraint showing every compiled rule, "sub-agents inherit this", and
// the three options. Like renderTrust it replaces the whole live region
// and draws no box border.
func (m Root) renderMission() string {
	g := m.lay.glyphs()
	width := m.lay.ContentWidth()
	d := m.mission

	var b strings.Builder
	b.WriteString(" " + strings.Repeat(g.rule, width-1) + "\n")
	// "Goal        " is 12 columns plus the line's own leading space (1),
	// so the goal text itself may use at most width-13 — a long stated
	// goal at 40 columns (this dialog's own narrowest supported width,
	// per width_internal_test.go's TestNoOverflowAtCriticalWidths) would
	// otherwise push the whole line past the terminal, the same overflow
	// ShortenPath exists to prevent for renderTrust's own project-path
	// line.
	fmt.Fprintf(&b, " Goal        %s\n", truncateRunes(d.m.Goal, width-13))

	for _, c := range d.m.Constraints {
		if !c.Negated {
			continue
		}
		fmt.Fprintf(&b, " Constraint detected: %s\n\n", c.Keyword)
		b.WriteString(" Compiling to a rule:\n")
		for _, r := range c.Rules {
			fmt.Fprintf(&b, "   %-7s %-16s %s\n", r.Capability, r.Pattern, r.Effect)
		}
		b.WriteString("   sub-agents inherit this\n")
	}

	b.WriteString("\n")
	for i, label := range missionOptionLabels {
		pointer := " "
		if i == d.sel {
			pointer = g.inputPrefix
		}
		line := pointer + " " + label
		if i == d.sel {
			line = m.styles.Accent.Render(line)
		}
		b.WriteString(" " + line + "\n")
		if missionOption(i) == missionSoft {
			b.WriteString("      (ask me if it comes up)\n")
		}
	}

	b.WriteString("\n " + strings.Repeat(g.rule, width-1) + "\n")
	fmt.Fprintf(&b, " %s move  enter choose  esc = 2\n", g.scrollHint)
	return b.String()
}

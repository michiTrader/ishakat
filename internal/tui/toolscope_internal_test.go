package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/mission"
	"github.com/MichiTrader/ishakat/internal/theme"
)

// mustOpenToolScope drives the real chain this dialog is only ever reached
// through — checkMission opening ModeMission, then accepting it — rather
// than calling openToolScope directly, so these tests exercise the actual
// wiring resolveMission's own tail relies on (in particular, that
// m.missionText survives the hop from ModeMission to ModeToolScope).
func mustOpenToolScope(t *testing.T, r Root, goal string) Root {
	t.Helper()
	r, ok := mustCheckMission(t, r, goal)
	if !ok {
		t.Fatalf("checkMission did not open ModeMission for %q", goal)
	}
	m, _ := r.updateMission(tea.KeyPressMsg{Code: tea.KeyEnter})
	got, ok := m.(Root)
	if !ok || got.mode != ModeToolScope {
		t.Fatalf("resolveMission did not chain into ModeToolScope for %q", goal)
	}
	return got
}

func TestResolveMissionChainsIntoModeToolScopeForEveryOption(t *testing.T) {
	// §21.6's own tool-scope trigger is "the goal contains a constraint",
	// which is exactly what already opened ModeMission — so every row of
	// that dialog, not just "1. That's right", must chain into
	// ModeToolScope next (see resolveMission's own doc comment).
	for _, opt := range []missionOption{missionAccept, missionAdjust, missionSoft} {
		r := newMissionRoot(nil)
		r, ok := mustCheckMission(t, r, "fix orbital-dash, no playwright")
		if !ok {
			t.Fatalf("[opt=%d] checkMission did not open ModeMission", opt)
		}
		r.mission = r.mission.moveSel(int(opt))
		m, _ := r.updateMission(tea.KeyPressMsg{Code: tea.KeyEnter})
		got, ok := m.(Root)
		if !ok || got.mode != ModeToolScope {
			t.Fatalf("[opt=%d] mode = %v, want ModeToolScope", opt, got.mode)
		}
		if got.missionText != "fix orbital-dash, no playwright" {
			t.Fatalf("[opt=%d] missionText = %q, not preserved across the chain", opt, got.missionText)
		}
	}
}

func TestOpenToolScopePreselectsAsProposed(t *testing.T) {
	r := mustOpenToolScope(t, newMissionRoot(nil), "fix orbital-dash, no playwright")
	if r.toolScope.sel != int(toolScopeAsProposed) {
		t.Fatalf("sel = %d, want toolScopeAsProposed (%d)", r.toolScope.sel, toolScopeAsProposed)
	}
}

func TestUpdateToolScopeArrowsWrapSelection(t *testing.T) {
	r := mustOpenToolScope(t, newMissionRoot(nil), "fix orbital-dash, no playwright")

	m, _ := r.updateToolScope(tea.KeyPressMsg{Code: tea.KeyUp})
	r = m.(Root)
	if r.toolScope.sel != int(toolScopePickOneByOne) {
		t.Fatalf("sel after Up from row 0 = %d, want toolScopePickOneByOne (%d) via wraparound", r.toolScope.sel, toolScopePickOneByOne)
	}

	m, _ = r.updateToolScope(tea.KeyPressMsg{Code: tea.KeyDown})
	r = m.(Root)
	if r.toolScope.sel != int(toolScopeAsProposed) {
		t.Fatalf("sel after Down from the last row = %d, want toolScopeAsProposed (%d) via wraparound", r.toolScope.sel, toolScopeAsProposed)
	}
}

func TestUpdateToolScopeSubmitStartsTheTurn(t *testing.T) {
	r := mustOpenToolScope(t, newMissionRoot(nil), "fix orbital-dash, no playwright")

	m, _ := r.updateToolScope(tea.KeyPressMsg{Code: tea.KeyEnter})
	got := m.(Root)
	if got.mode == ModeToolScope {
		t.Fatal("mode still ModeToolScope after submit: dialog did not close")
	}
	// resolveToolScope always calls m.submit(text), which moves to
	// ModeBusy the same way any ordinary submit would (submit_internal_test.go
	// and mission_internal_test.go's own submit-path tests already pin that
	// shape for the dialogs this one is chained after).
	if got.mode != ModeBusy {
		t.Fatalf("mode = %v, want ModeBusy: resolveToolScope must start the paused turn", got.mode)
	}
}

func TestUpdateToolScopeEscDefaultsToAsProposedAndStillStartsTheTurn(t *testing.T) {
	r := mustOpenToolScope(t, newMissionRoot(nil), "fix orbital-dash, no playwright")
	// Move off row 0 first, so Esc resolving to toolScopeDialogDefault
	// (rather than whatever happens to be selected) is actually exercised.
	m, _ := r.updateToolScope(tea.KeyPressMsg{Code: tea.KeyDown})
	r = m.(Root)

	m, _ = r.updateToolScope(tea.KeyPressMsg{Code: tea.KeyEsc})
	got := m.(Root)
	if got.mode != ModeBusy {
		t.Fatalf("mode = %v, want ModeBusy: Esc must still start the paused turn, not discard it", got.mode)
	}
}

func TestRenderToolScopeShowsProposedBaseAndBashScope(t *testing.T) {
	r := mustOpenToolScope(t, newMissionRoot(nil), "fix orbital-dash, no playwright")
	out := r.renderToolScope()
	if !contains(out, "Tools for this mission") {
		t.Fatalf("renderToolScope missing its own title:\n%s", out)
	}
	if !contains(out, "read") || !contains(out, "edit") || !contains(out, "dispatch") {
		t.Fatalf("renderToolScope missing one of the base capabilities:\n%s", out)
	}
	if !contains(out, "bash(node, npm, git)") {
		t.Fatalf("renderToolScope missing the worked example's own bash scope:\n%s", out)
	}
	if !contains(out, "As proposed") || !contains(out, "Everything installed") || !contains(out, "Pick one by one") {
		t.Fatalf("renderToolScope missing one of the four options:\n%s", out)
	}
}

func TestRenderToolScopeShowsBrowserWarningWhenOffered(t *testing.T) {
	// "fix orbital-dash, no playwright" denies playwright, which
	// internal/mission.ProposeTools' own doc comment (wantsBrowserOffer)
	// still treats as BrowserOffered=true: a denial is shown, not hidden.
	r := mustOpenToolScope(t, newMissionRoot(nil), "fix orbital-dash, no playwright")
	out := r.renderToolScope()
	if !contains(out, "Proposed + browser") {
		t.Fatalf("renderToolScope missing the browser-widen row:\n%s", out)
	}
	if !contains(out, "180 MB download") {
		t.Fatalf("renderToolScope missing the browser row's own shown reason:\n%s", out)
	}
}

// TestResolveToolScopeAsProposedSetsExactlyTheProposedBashAllow is Step 31
// part 7's own closing property for row 1: choosing "As proposed" must
// wire mission.ToolScope.BashAllow into the session's Guard verbatim, via
// SetBashScope — not merely render it (part 6's own scope, already
// covered by TestRenderToolScopeShowsProposedBaseAndBashScope).
func TestResolveToolScopeAsProposedSetsExactlyTheProposedBashAllow(t *testing.T) {
	guard := &fakeMissionGuard{}
	r := mustOpenToolScope(t, newMissionRoot(guard), "fix orbital-dash, no playwright")

	r.updateToolScope(tea.KeyPressMsg{Code: tea.KeyEnter})

	if !guard.bashScopeSet {
		t.Fatal("SetBashScope was never called for toolScopeAsProposed")
	}
	want := []string{"node", "npm", "git"} // defaultBashAllow, this goal names no ecosystem keyword
	if !equalStrings(guard.bashScope, want) {
		t.Fatalf("bashScope = %v, want %v", guard.bashScope, want)
	}
}

// TestResolveToolScopeProposedPlusBrowserWidensTheBashAllow covers row 2:
// choosing it must set a scope that is the proposed BashAllow plus
// browserBashAllow's own fixed set, not merely the proposed set alone —
// otherwise the mockup's own "considered and rejected" widen option would
// resolve identically to row 1, which would make the dialog's second row
// meaningless.
func TestResolveToolScopeProposedPlusBrowserWidensTheBashAllow(t *testing.T) {
	guard := &fakeMissionGuard{}
	r := mustOpenToolScope(t, newMissionRoot(guard), "fix orbital-dash, no playwright")
	r.toolScope = r.toolScope.moveSel(int(toolScopeProposedPlusBrowser))

	r.updateToolScope(tea.KeyPressMsg{Code: tea.KeyEnter})

	if !guard.bashScopeSet {
		t.Fatal("SetBashScope was never called for toolScopeProposedPlusBrowser")
	}
	for _, want := range []string{"node", "npm", "git", "npx", "playwright"} {
		if !containsString(guard.bashScope, want) {
			t.Fatalf("bashScope = %v, missing %q", guard.bashScope, want)
		}
	}
}

// TestResolveToolScopeEverythingClearsTheBashAllow covers row 3: "3.
// Everything installed" must clear any restriction (SetBashScope(nil)),
// per the mockup's own "allow them all" reading — proven by an explicit
// nil check, not merely an empty slice, since bashScopeAllow's own doc
// comment on guard.go draws that exact distinction.
func TestResolveToolScopeEverythingClearsTheBashAllow(t *testing.T) {
	guard := &fakeMissionGuard{}
	r := mustOpenToolScope(t, newMissionRoot(guard), "fix orbital-dash, no playwright")
	r.toolScope = r.toolScope.moveSel(int(toolScopeEverything))

	r.updateToolScope(tea.KeyPressMsg{Code: tea.KeyEnter})

	if !guard.bashScopeSet {
		t.Fatal("SetBashScope was never called for toolScopeEverything")
	}
	if guard.bashScope != nil {
		t.Fatalf("bashScope = %v, want nil (Everything installed clears any restriction)", guard.bashScope)
	}
}

// TestResolveToolScopePickOneByOneFallsBackToProposed covers row 4: with
// no free-standing checkbox widget yet, it must resolve to the same scope
// as "As proposed" (row 1) rather than leaving the session unrestricted or
// panicking — the same "not invented here half-finished" deferral
// missionAdjust already applies for §21.6's first dialog.
func TestResolveToolScopePickOneByOneFallsBackToProposed(t *testing.T) {
	guard := &fakeMissionGuard{}
	r := mustOpenToolScope(t, newMissionRoot(guard), "fix orbital-dash, no playwright")
	r.toolScope = r.toolScope.moveSel(int(toolScopePickOneByOne))

	r.updateToolScope(tea.KeyPressMsg{Code: tea.KeyEnter})

	want := []string{"node", "npm", "git"}
	if !equalStrings(guard.bashScope, want) {
		t.Fatalf("bashScope = %v, want %v (same as As proposed)", guard.bashScope, want)
	}
}

// TestResolveToolScopeEscDefaultsToAsProposedScope proves Esc's own
// documented default (toolScopeDialogDefault = toolScopeAsProposed) is
// honoured for the Guard wiring too, not just the rendered selection:
// closing the dialog without deciding must never leave the session wider
// than what auto itself proposed.
func TestResolveToolScopeEscDefaultsToAsProposedScope(t *testing.T) {
	guard := &fakeMissionGuard{}
	r := mustOpenToolScope(t, newMissionRoot(guard), "fix orbital-dash, no playwright")
	// Move off row 0 first, so this actually exercises Esc's own default
	// rather than happening to match whatever was already selected.
	r.toolScope = r.toolScope.moveSel(int(toolScopeEverything))

	r.updateToolScope(tea.KeyPressMsg{Code: tea.KeyEsc})

	want := []string{"node", "npm", "git"}
	if !equalStrings(guard.bashScope, want) {
		t.Fatalf("bashScope = %v, want %v (Esc must default to As proposed, not Everything installed)", guard.bashScope, want)
	}
}

// TestResolveToolScopeNilGuardDoesNotPanic covers newMissionRoot(nil), the
// shape every other test in this file already uses: a nil MissionGuard
// must degrade to "enforces nothing" (no SetBashScope call attempted at
// all), never panic, mirroring resolveMission's own "if opt == missionAccept
// && m.missionGuard != nil" nil check.
func TestResolveToolScopeNilGuardDoesNotPanic(t *testing.T) {
	r := mustOpenToolScope(t, newMissionRoot(nil), "fix orbital-dash, no playwright")
	m, _ := r.updateToolScope(tea.KeyPressMsg{Code: tea.KeyEnter})
	got := m.(Root)
	if got.mode != ModeBusy {
		t.Fatalf("mode = %v, want ModeBusy even with a nil MissionGuard", got.mode)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func TestRenderToolScopeHidesBrowserWarningWhenGoalAlreadyAffirmsIt(t *testing.T) {
	// This goal has no recognized constraint, so it never opens ModeMission
	// at all — this test drives openToolScope directly (the one case where
	// that is correct: there is no chain to exercise, only ProposeTools'
	// own BrowserOffered=false branch reaching renderToolScope).
	r := newMissionRoot(nil).openToolScope("use Playwright if you think it helps")
	out := r.renderToolScope()
	if contains(out, "180 MB download") {
		t.Fatalf("renderToolScope shows the browser-widen reason even though the goal already affirms Playwright:\n%s", out)
	}
}

// newToolPolicyRoot builds a Root with the given policy wired the same way
// internal/app's missionPolicyOf does, for checkToolPolicy's own tests.
func newToolPolicyRoot(policy *mission.Policy) Root {
	return NewRoot(Options{
		Version:       "0.0.0-test",
		CWD:           "/home/user/projects/orbital-dash",
		Theme:         theme.Load(""),
		Cap:           theme.CapNone,
		NoTTY:         true,
		MissionPolicy: policy,
	})
}

func TestCheckToolPolicyOpensModeToolScopeWhenGoalCollidesWithPolicy(t *testing.T) {
	r := newToolPolicyRoot(&mission.Policy{ShellAllowed: false})
	next, ok := r.checkToolPolicy("use Playwright if you think it helps")
	if !ok {
		t.Fatal("checkToolPolicy returned ok=false for a goal colliding with policy")
	}
	if next.mode != ModeToolScope {
		t.Fatalf("mode = %v, want ModeToolScope", next.mode)
	}
	if next.missionText != "use Playwright if you think it helps" {
		t.Fatalf("missionText = %q, not preserved", next.missionText)
	}
}

func TestCheckToolPolicyFallsThroughForAnOrdinaryGoal(t *testing.T) {
	r := newToolPolicyRoot(&mission.Policy{ShellAllowed: true})
	_, ok := r.checkToolPolicy("why does the game stutter?")
	if ok {
		t.Fatal("checkToolPolicy returned ok=true for a goal with no recognized keyword")
	}
}

func TestCheckToolPolicyFallsThroughWhenPolicyNeverWired(t *testing.T) {
	// mission.Policy{}'s own zero value (ShellAllowed: false) would make
	// every affirmed keyword look like a collision — Root.missionPolicy's
	// own doc comment states why nil, not that zero value, is what an
	// unwired caller must get instead.
	r := newToolPolicyRoot(nil)
	_, ok := r.checkToolPolicy("use Playwright if you think it helps")
	if ok {
		t.Fatal("checkToolPolicy returned ok=true even though Options.MissionPolicy was never wired (nil)")
	}
}

func TestCheckToolPolicyOpensModeToolScopeWhenFetchCollidesWithPolicy(t *testing.T) {
	// Step 31 part 10: the fetch-capability half of the collision check
	// (mission.Policy.FetchAllowed), exercised through the real
	// checkToolPolicy seam rather than only mission.OutsidePolicy's own
	// unit tests -- shell is allowed here, only fetch is off, so this
	// pins down that the bash half alone cannot mask a fetch collision.
	r := newToolPolicyRoot(&mission.Policy{ShellAllowed: true, FetchAllowed: false})
	next, ok := r.checkToolPolicy("use Playwright if you think it helps")
	if !ok {
		t.Fatal("checkToolPolicy returned ok=false for a goal colliding only on the fetch half of policy")
	}
	if next.mode != ModeToolScope {
		t.Fatalf("mode = %v, want ModeToolScope", next.mode)
	}
}

func TestCheckToolPolicyDoesNotDoubleUpWithANegatedConstraint(t *testing.T) {
	// A goal that already negates the keyword is checkMission's own
	// trigger (HasDeny()) — OutsidePolicy itself already excludes a
	// negated constraint (see that function's own doc comment), so this
	// is the "no second dialog for the same word" guarantee, exercised
	// through the real Submit-key seam this pass wired in root.go.
	r := newToolPolicyRoot(&mission.Policy{ShellAllowed: false})
	_, ok := r.checkToolPolicy("fix orbital-dash, no playwright")
	if ok {
		t.Fatal("checkToolPolicy returned ok=true for a negated constraint, which checkMission already handles")
	}
}

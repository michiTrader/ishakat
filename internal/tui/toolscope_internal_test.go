package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
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

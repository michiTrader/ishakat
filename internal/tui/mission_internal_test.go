package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/permissions"
	"github.com/MichiTrader/ishakat/internal/theme"
)

// fakeMissionGuard is the test double for MissionGuard — the same "a fake
// in this package's own tests should never have to build a real Guard"
// reasoning mission.go's own package comment gives for why MissionGuard
// exists at all.
type fakeMissionGuard struct {
	added [][]permissions.MissionRule
}

func (g *fakeMissionGuard) AddMissionRules(rules []permissions.MissionRule) {
	g.added = append(g.added, rules)
}

func newMissionRoot(guard MissionGuard) Root {
	return NewRoot(Options{
		Version:      "0.0.0-test",
		CWD:          "/home/user/projects/orbital-dash",
		Theme:        theme.Load(""),
		Cap:          theme.CapNone,
		NoTTY:        true,
		MissionGuard: guard,
	})
}

func TestCheckMissionOpensModeMissionWhenGoalHasConstraint(t *testing.T) {
	r := newMissionRoot(nil)
	next, ok := r.checkMission("fix orbital-dash, no playwright")
	if !ok {
		t.Fatal("checkMission returned ok=false for a goal containing a recognized constraint")
	}
	if next.mode != ModeMission {
		t.Fatalf("mode = %v, want ModeMission", next.mode)
	}
	if next.missionText != "fix orbital-dash, no playwright" {
		t.Fatalf("missionText = %q, not preserved", next.missionText)
	}
}

func TestCheckMissionFallsThroughForAnOrdinaryGoal(t *testing.T) {
	r := newMissionRoot(nil)
	_, ok := r.checkMission("why does the game stutter?")
	if ok {
		t.Fatal("checkMission returned ok=true for a goal with no recognized constraint")
	}
}

func TestUpdateMissionSubmitAppliesRulesToGuard(t *testing.T) {
	guard := &fakeMissionGuard{}
	r := newMissionRoot(guard)
	r, ok := mustCheckMission(t, r, "fix orbital-dash, no playwright")
	if !ok {
		t.Fatal("checkMission did not open ModeMission")
	}

	// Cursor starts on row 0 ("1. That's right" / missionAccept) — the
	// same "recommending it visually is not the same as choosing it"
	// starting position trust.go's own newTrustDialog establishes for
	// its own first row, but here row 0 genuinely *is* the safe one-
	// keystroke path §21.6 describes, so Enter with no movement is the
	// dialog's own common case, not a footgun.
	m, _ := r.updateMission(tea.KeyPressMsg{Code: tea.KeyEnter})
	got := m.(Root)

	if got.mode == ModeMission {
		t.Fatal("mode still ModeMission after submit: dialog did not close")
	}
	if len(guard.added) != 1 {
		t.Fatalf("guard.added = %v, want exactly one AddMissionRules call", guard.added)
	}
	rules := guard.added[0]
	wantCapabilities := map[string]bool{"bash": false, "fetch": false}
	for _, r := range rules {
		if _, ok := wantCapabilities[r.Capability]; !ok {
			t.Fatalf("unexpected capability %q in compiled rule %+v", r.Capability, r)
		}
		wantCapabilities[r.Capability] = true
		if r.Pattern != "**playwright**" {
			t.Fatalf("pattern = %q, want \"**playwright**\"", r.Pattern)
		}
	}
	for cap, seen := range wantCapabilities {
		if !seen {
			t.Fatalf("no compiled rule for capability %q", cap)
		}
	}
}

func TestUpdateMissionEscDoesNotApplyAnyRule(t *testing.T) {
	guard := &fakeMissionGuard{}
	r := newMissionRoot(guard)
	r, ok := mustCheckMission(t, r, "fix orbital-dash, no playwright")
	if !ok {
		t.Fatal("checkMission did not open ModeMission")
	}

	m, _ := r.updateMission(tea.KeyPressMsg{Code: tea.KeyEsc})
	got := m.(Root)

	if got.mode == ModeMission {
		t.Fatal("mode still ModeMission after Esc: dialog did not close")
	}
	if len(guard.added) != 0 {
		t.Fatalf("guard.added = %v, want none — Esc must never silently apply a rule", guard.added)
	}
}

func TestUpdateMissionSoftPreferenceDoesNotApplyAnyRule(t *testing.T) {
	guard := &fakeMissionGuard{}
	r := newMissionRoot(guard)
	r, ok := mustCheckMission(t, r, "fix orbital-dash, no playwright")
	if !ok {
		t.Fatal("checkMission did not open ModeMission")
	}

	// Move down twice: row 0 (accept) -> row 1 (adjust) -> row 2 (soft).
	m, _ := r.updateMission(tea.KeyPressMsg{Code: tea.KeyDown})
	r = m.(Root)
	m, _ = r.updateMission(tea.KeyPressMsg{Code: tea.KeyDown})
	r = m.(Root)
	if r.mission.sel != int(missionSoft) {
		t.Fatalf("sel = %d, want missionSoft (%d)", r.mission.sel, missionSoft)
	}
	m, _ = r.updateMission(tea.KeyPressMsg{Code: tea.KeyEnter})
	got := m.(Root)

	if got.mode == ModeMission {
		t.Fatal("mode still ModeMission after choosing 'just a preference'")
	}
	if len(guard.added) != 0 {
		t.Fatalf("guard.added = %v, want none for the soft-preference row", guard.added)
	}
}

func TestUpdateMissionNilGuardDoesNotPanic(t *testing.T) {
	r := newMissionRoot(nil)
	r, ok := mustCheckMission(t, r, "fix orbital-dash, no playwright")
	if !ok {
		t.Fatal("checkMission did not open ModeMission")
	}
	// A nil missionGuard must degrade to "enforces nowhere", never panic
	// — the same nil-is-supported contract TrustStore's own doc comment
	// establishes for a different write.
	m, _ := r.updateMission(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.(Root).mode == ModeMission {
		t.Fatal("mode still ModeMission: dialog did not close even with a nil guard")
	}
}

func TestRenderMissionShowsGoalAndCompiledRule(t *testing.T) {
	r := newMissionRoot(nil)
	r, ok := mustCheckMission(t, r, "fix orbital-dash, no playwright")
	if !ok {
		t.Fatal("checkMission did not open ModeMission")
	}
	out := r.renderMission()
	if !contains(out, "fix orbital-dash, no playwright") {
		t.Fatalf("renderMission missing goal:\n%s", out)
	}
	if !contains(out, "playwright") {
		t.Fatalf("renderMission missing detected keyword:\n%s", out)
	}
	if !contains(out, "**playwright**") {
		t.Fatalf("renderMission missing compiled pattern:\n%s", out)
	}
	if !contains(out, "sub-agents inherit this") {
		t.Fatalf("renderMission missing inheritance line:\n%s", out)
	}
	if !contains(out, "That's right") || !contains(out, "Adjust the rule") || !contains(out, "Just a preference") {
		t.Fatalf("renderMission missing one of the three options:\n%s", out)
	}
}

// mustCheckMission is this file's own small helper: every test above needs
// the same "call checkMission and keep going with the Root it returned"
// two-step, and repeating `r, ok := r.checkMission(...)` with the ok check
// inlined at each call site would just be this function's body copied six
// times.
func mustCheckMission(t *testing.T, r Root, text string) (Root, bool) {
	t.Helper()
	next, ok := r.checkMission(text)
	return next, ok
}

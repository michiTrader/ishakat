package permissions

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// TestGuardBashScopeRefusesCommandOutsideAllowList is §21.6's second
// mockup's own worked example ("bash(node, npm, git)") on the Guard side: a
// bash command whose leading word is not in the chosen scope's allow list
// must be refused end to end (errors.Is(err, ErrDenied)), the same
// "compiled to a rule the Guard enforces" property Step 31's first mockup
// already established for mission-deny, applied here to the second
// mockup's allow-shaped scope instead.
func TestGuardBashScopeRefusesCommandOutsideAllowList(t *testing.T) {
	guard := New(testPermissions(), false, &recordingReviewer{decision: Decision{Allow: true}})
	guard.SetBashScope([]string{"node", "npm", "git"})

	err := guard.Authorize(context.Background(), "bash", json.RawMessage(`{"command":"cargo build"}`))
	if err == nil {
		t.Fatal("Authorize() = nil, want a tool-scope-deny error")
	}
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("Authorize() error = %v, want errors.Is(err, ErrDenied)", err)
	}
}

// TestGuardBashScopeAllowsCommandInsideAllowList proves the scope is a
// narrow allow, not an accidental lockout of everything: a bash command
// whose leading word is in the chosen scope must run exactly as it would
// with no scope active at all.
func TestGuardBashScopeAllowsCommandInsideAllowList(t *testing.T) {
	reviewer := &recordingReviewer{decision: Decision{Allow: true}}
	guard := New(testPermissions(), false, reviewer)
	guard.SetBashScope([]string{"node", "npm", "git"})

	// "npm install left-pad" is Sensitive (not in any safe/controlled
	// prefix list), so it should reach the reviewer, not be hard-denied.
	if err := guard.Authorize(context.Background(), "bash", json.RawMessage(`{"command":"npm install left-pad"}`)); err != nil {
		t.Fatalf("Authorize() error = %v, want nil (npm is inside the scope)", err)
	}
	if reviewer.calls != 1 {
		t.Fatalf("reviewer calls = %d, want 1 (npm install is Sensitive, not hard-denied)", reviewer.calls)
	}
}

// TestGuardBashScopeStillAllowsSafeReadOnlyCommands is bashScopeHardDeny's
// own first escape hatch: a scope restricts what a mission may *do*, not
// whether it can look around at all. "ls"/"git status" must never be
// refused merely because "ls"/"git" (as a bare read command, not the "git"
// subcommand family the scope actually names) is not itself one of the
// scoped ecosystems.
func TestGuardBashScopeStillAllowsSafeReadOnlyCommands(t *testing.T) {
	reviewer := &recordingReviewer{decision: Decision{Allow: true}}
	guard := New(testPermissions(), false, reviewer)
	guard.SetBashScope([]string{"node", "npm"}) // deliberately no "git", no bare "ls"

	if err := guard.Authorize(context.Background(), "bash", json.RawMessage(`{"command":"ls -la"}`)); err != nil {
		t.Fatalf("Authorize(ls) error = %v, want nil (safeBashPrefixes escape hatch)", err)
	}
	if err := guard.Authorize(context.Background(), "bash", json.RawMessage(`{"command":"git status"}`)); err != nil {
		t.Fatalf("Authorize(git status) error = %v, want nil (safeBashPrefixes escape hatch)", err)
	}
	if reviewer.calls != 0 {
		t.Fatalf("reviewer calls = %d, want 0 (both commands are Safe)", reviewer.calls)
	}
}

// TestGuardBashScopeNilMeansUnrestricted covers SetBashScope(nil) — the
// mockup's own "3. Everything installed" option (see resolveToolScope's
// own comment on why this pass maps that option to a nil scope) — and the
// zero-value Guard that never calls SetBashScope at all: neither must
// refuse a command that would otherwise run.
func TestGuardBashScopeNilMeansUnrestricted(t *testing.T) {
	reviewer := &recordingReviewer{decision: Decision{Allow: true}}
	guard := New(testPermissions(), false, reviewer)
	// No SetBashScope call at all — matches every pre-part-7 Guard.

	if err := guard.Authorize(context.Background(), "bash", json.RawMessage(`{"command":"cargo build"}`)); err != nil {
		t.Fatalf("Authorize() error = %v, want nil (no scope set)", err)
	}

	// Explicitly setting nil after a real restriction must also clear it —
	// the "Everything installed" case, chosen after the dialog already
	// proposed a narrower scope for this same goal.
	guard.SetBashScope([]string{"node"})
	guard.SetBashScope(nil)
	if err := guard.Authorize(context.Background(), "bash", json.RawMessage(`{"command":"cargo build"}`)); err != nil {
		t.Fatalf("Authorize() error = %v, want nil (scope cleared back to nil)", err)
	}
}

// TestGuardBashScopeSurvivesYolo is the sharpest form of "a lower layer can
// never widen a higher one" (§21.4), the same property
// TestGuardMissionRuleSurvivesYolo already pins for mission-deny: --yolo
// turns Sensitive ask into allow for bash, but a chosen tool scope is
// checked inside hardDeny, which runs before --yolo's own bypass is ever
// reached, so --yolo must not be able to run a command outside the chosen
// scope.
func TestGuardBashScopeSurvivesYolo(t *testing.T) {
	guard := New(testPermissions(), true /* yolo */, nil)
	guard.SetBashScope([]string{"node", "npm", "git"})

	err := guard.Authorize(context.Background(), "bash", json.RawMessage(`{"command":"cargo build"}`))
	if err == nil {
		t.Fatal("Authorize() = nil under --yolo, want the tool scope to still refuse")
	}
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("Authorize() error = %v, want errors.Is(err, ErrDenied)", err)
	}
}

// TestGuardBashScopeComposesWithMissionDeny proves the two Step 31
// mechanisms compose correctly: a mission's own deny rule ("no
// Playwright") must still refuse a matching command even when that
// command's leading word ("npx", say) is inside the chosen bash scope —
// §21.6's own mockup line for "Everything installed", "invariants still
// apply", generalizes to every scope option, not just the widest one.
func TestGuardBashScopeComposesWithMissionDeny(t *testing.T) {
	guard := New(testPermissions(), false, &recordingReviewer{decision: Decision{Allow: true}})
	guard.AddMissionRules([]MissionRule{{Capability: "bash", Pattern: "**playwright**"}})
	guard.SetBashScope([]string{"npx", "node", "npm"})

	err := guard.Authorize(context.Background(), "bash", json.RawMessage(`{"command":"npx playwright test"}`))
	if err == nil {
		t.Fatal("Authorize() = nil, want the mission-deny rule to still refuse even though npx is in scope")
	}
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("Authorize() error = %v, want errors.Is(err, ErrDenied)", err)
	}
}

// TestGuardBashScopeInheritedBySubAgentGuard mirrors
// TestGuardMissionRuleInheritedBySubAgentGuard exactly, for the scope
// mechanism instead of mission-deny: a sub-agent's *Guard is the same
// pointer as its parent's, so a scope set on the parent restricts a
// child's own bash calls automatically, with no dispatch-specific code
// needing to copy or re-apply it.
func TestGuardBashScopeInheritedBySubAgentGuard(t *testing.T) {
	parentGuard := New(testPermissions(), false, &recordingReviewer{decision: Decision{Allow: true}})
	parentGuard.SetBashScope([]string{"node", "npm", "git"})

	subAgentGuard := parentGuard

	err := subAgentGuard.Authorize(context.Background(), "bash", json.RawMessage(`{"command":"cargo build"}`))
	if err == nil {
		t.Fatal("sub-agent Authorize() = nil, want the parent's tool scope to still refuse")
	}
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("sub-agent Authorize() error = %v, want errors.Is(err, ErrDenied)", err)
	}

	if err := subAgentGuard.Authorize(context.Background(), "bash", json.RawMessage(`{"command":"npm install"}`)); err != nil {
		t.Fatalf("sub-agent Authorize(npm install) error = %v, want nil (inside the inherited scope)", err)
	}
}

// TestGuardBashScopeReportsCurrentValue covers BashScope's own read-back
// contract, mirroring MissionRules' own test coverage: nil before any
// SetBashScope call, the most recently set value afterward, and nil again
// after an explicit SetBashScope(nil).
func TestGuardBashScopeReportsCurrentValue(t *testing.T) {
	guard := New(testPermissions(), false, nil)
	if got := guard.BashScope(); got != nil {
		t.Fatalf("BashScope() = %v, want nil before any SetBashScope call", got)
	}

	guard.SetBashScope([]string{"node", "npm"})
	got := guard.BashScope()
	if len(got) != 2 || got[0] != "node" || got[1] != "npm" {
		t.Fatalf("BashScope() = %v, want [node npm]", got)
	}

	guard.SetBashScope(nil)
	if got := guard.BashScope(); got != nil {
		t.Fatalf("BashScope() = %v, want nil after SetBashScope(nil)", got)
	}
}

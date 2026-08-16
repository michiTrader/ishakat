package permissions

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// TestGuardMissionRuleDeniesBashCommand is §21.6's own worked example on
// the Guard side: a compiled "bash **playwright** deny" rule must actually
// refuse a matching bash invocation, ending the turn (errors.Is(err,
// ErrDenied)) rather than merely appearing in a dialog nobody enforces.
func TestGuardMissionRuleDeniesBashCommand(t *testing.T) {
	guard := New(testPermissions(), false, &recordingReviewer{decision: Decision{Allow: true}})
	guard.AddMissionRules([]MissionRule{{Capability: "bash", Pattern: "**playwright**"}})

	err := guard.Authorize(context.Background(), "bash", json.RawMessage(`{"command":"npx playwright test tests/e2e.spec.ts"}`))
	if err == nil {
		t.Fatal("Authorize() = nil, want a mission-deny error")
	}
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("Authorize() error = %v, want errors.Is(err, ErrDenied)", err)
	}
}

// TestGuardMissionRuleDeniesFetchURL covers the other half of §21.6's own
// worked example: "no Playwright" compiles to a fetch rule too, because a
// CDN download of the browser binary is not a bash invocation at all.
func TestGuardMissionRuleDeniesFetchURL(t *testing.T) {
	guard := New(testPermissions(), false, &recordingReviewer{decision: Decision{Allow: true}})
	guard.AddMissionRules([]MissionRule{{Capability: "fetch", Pattern: "**playwright**"}})

	err := guard.Authorize(context.Background(), "fetch", json.RawMessage(`{"url":"https://playwright.download.example/chromium.zip"}`))
	if err == nil {
		t.Fatal("Authorize() = nil, want a mission-deny error")
	}
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("Authorize() error = %v, want errors.Is(err, ErrDenied)", err)
	}
}

// TestGuardMissionRuleSurvivesYolo is the sharpest form of "a lower layer
// can never widen a higher one" (§21.4): --yolo turns Sensitive ask into
// allow for bash, but a mission constraint is checked inside hardDeny,
// which runs before --yolo's own bypass is ever reached, so --yolo must
// not be able to run a command a stated mission constraint forbids.
func TestGuardMissionRuleSurvivesYolo(t *testing.T) {
	guard := New(testPermissions(), true /* yolo */, nil)
	guard.AddMissionRules([]MissionRule{{Capability: "bash", Pattern: "**playwright**"}})

	err := guard.Authorize(context.Background(), "bash", json.RawMessage(`{"command":"playwright test"}`))
	if err == nil {
		t.Fatal("Authorize() = nil under --yolo, want the mission rule to still refuse")
	}
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("Authorize() error = %v, want errors.Is(err, ErrDenied)", err)
	}
}

// TestGuardMissionRuleSurvivesAutoAutonomy covers the same "narrows, never
// widens" property against Auto autonomy specifically (Step 30's own
// addition): Auto lets Safe/Controlled bash bypass review entirely, but a
// mission constraint must still refuse a matching command before
// Authorize's autonomy gate is ever consulted.
func TestGuardMissionRuleSurvivesAutoAutonomy(t *testing.T) {
	guard := New(testPermissions(), false, &recordingReviewer{decision: Decision{Allow: true}})
	guard.SetAutonomy(Auto)
	guard.AddMissionRules([]MissionRule{{Capability: "bash", Pattern: "**docker**"}})

	// "docker ps" would otherwise be Sensitive (not in any of the
	// safe/controlled prefix lists) and Auto lets Sensitive run only after
	// asking -- but the mission rule must refuse it before that ever
	// happens.
	err := guard.Authorize(context.Background(), "bash", json.RawMessage(`{"command":"docker ps"}`))
	if err == nil {
		t.Fatal("Authorize() = nil, want the mission rule to refuse")
	}
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("Authorize() error = %v, want errors.Is(err, ErrDenied)", err)
	}
}

// TestGuardMissionRuleDoesNotAffectUnrelatedCommands proves the rule is a
// narrow deny, not an accidental lockout of the whole tool: a bash command
// that does not match the pattern must run exactly as it would with no
// mission active at all.
func TestGuardMissionRuleDoesNotAffectUnrelatedCommands(t *testing.T) {
	reviewer := &recordingReviewer{decision: Decision{Allow: true}}
	guard := New(testPermissions(), false, reviewer)
	guard.AddMissionRules([]MissionRule{{Capability: "bash", Pattern: "**playwright**"}})

	// "ls" is in safeBashPrefixes, so it should bypass review entirely,
	// exactly as it would with no mission rule at all.
	if err := guard.Authorize(context.Background(), "bash", json.RawMessage(`{"command":"ls -la"}`)); err != nil {
		t.Fatalf("Authorize() error = %v, want nil (unrelated command)", err)
	}
	if reviewer.calls != 0 {
		t.Fatalf("reviewer calls = %d, want 0 (ls is Safe)", reviewer.calls)
	}
}

// TestGuardMissionRulesAreAdditiveNotReplacing covers AddMissionRules' own
// "appends, never replaces" contract (§21.16 decision 3's "don't touch
// audio mid-run" narrative): calling it twice must enforce both calls'
// rules, not just the most recent one.
func TestGuardMissionRulesAreAdditiveNotReplacing(t *testing.T) {
	guard := New(testPermissions(), false, &recordingReviewer{decision: Decision{Allow: true}})
	guard.AddMissionRules([]MissionRule{{Capability: "bash", Pattern: "**playwright**"}})
	guard.AddMissionRules([]MissionRule{{Capability: "bash", Pattern: "**docker**"}})

	if err := guard.Authorize(context.Background(), "bash", json.RawMessage(`{"command":"playwright test"}`)); err == nil {
		t.Error("Authorize(playwright) = nil, want denied (first rule still active)")
	}
	if err := guard.Authorize(context.Background(), "bash", json.RawMessage(`{"command":"docker build ."}`)); err == nil {
		t.Error("Authorize(docker) = nil, want denied (second rule active)")
	}

	rules := guard.MissionRules()
	if len(rules) != 2 {
		t.Fatalf("MissionRules() = %+v, want 2 entries from both calls", rules)
	}
}

// TestGuardMissionRuleInheritedBySubAgentGuard is §21.11's own closing
// property, proven at the exact seam that makes it true: a sub-agent's
// Guard is the very same *Guard pointer as its parent's (the shape
// internal/app/dispatch.go's newSubAgentRunner actually uses, threading
// the identical guard argument into a second buildAgentOptions call
// rather than constructing a new one) — so a mission rule added to the
// parent is visible to "the child" without any dispatch-specific code
// needing to copy or re-apply it. This test does not import
// internal/app (that would need a real *engine.Engine and violate this
// package's own boundary); it proves the one property dispatch.go's own
// doc comment claims and this package can verify on its own: the same
// *Guard value enforces the same mission rules for any caller holding it,
// which is the entire mechanism §21.11 depends on.
func TestGuardMissionRuleInheritedBySubAgentGuard(t *testing.T) {
	parentGuard := New(testPermissions(), false, &recordingReviewer{decision: Decision{Allow: true}})
	parentGuard.AddMissionRules([]MissionRule{{Capability: "bash", Pattern: "**playwright**"}})

	// The "sub-agent" in this test is simply a second reference to the
	// same *Guard -- exactly what newSubAgentRunner passes through to its
	// own buildAgentOptions call (see dispatch.go's own guard parameter).
	subAgentGuard := parentGuard

	err := subAgentGuard.Authorize(context.Background(), "bash", json.RawMessage(`{"command":"playwright test"}`))
	if err == nil {
		t.Fatal("sub-agent Authorize() = nil, want the parent's mission rule to still refuse")
	}
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("sub-agent Authorize() error = %v, want errors.Is(err, ErrDenied)", err)
	}

	// A capability the mission never restricted (fetch, say, for a URL
	// with no match) must still work on the child -- inheritance narrows,
	// it does not additionally lock out everything.
	if err := subAgentGuard.Authorize(context.Background(), "fetch", json.RawMessage(`{"url":"https://example.com/docs"}`)); err != nil {
		t.Fatalf("sub-agent Authorize(fetch, unrelated url) error = %v, want nil", err)
	}
}

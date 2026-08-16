package permissions

import (
	"context"
	"encoding/json"
	"testing"
)

// TestAskUserTierIsSafeAndNative pins ask_user's own entry in
// tierFor/isNativeToolName, the same shape TestGuardDispatchTierIsCriticalAndNative
// already pins for dispatch: the fixed tier a manifest cannot lower via
// SetToolTiers, even though Authorize's own bypass (see its doc comment)
// means this tier is never actually consulted for the real tool.
func TestAskUserTierIsSafeAndNative(t *testing.T) {
	if got := tierFor(askUserToolName); got != Safe {
		t.Fatalf("tierFor(%q) = %v, want Safe", askUserToolName, got)
	}
	if !isNativeToolName(askUserToolName) {
		t.Fatalf("isNativeToolName(%q) = false, want true", askUserToolName)
	}
	guard := New(testPermissions(), false, &recordingReviewer{})
	guard.SetToolTiers(map[string]Tier{askUserToolName: Critical})
	if got := guard.tierFor(askUserToolName, json.RawMessage(`{}`)); got != Safe {
		t.Fatalf("guard.tierFor(%q) after SetToolTiers(Critical) = %v, want Safe (native tier cannot be raised or lowered)", askUserToolName, got)
	}
}

// TestAuthorizeNeverDeniesAskUserDespiteHardDeny is the closing criterion
// for §21.16 decision 1's "never denyable": a WriteDeny pattern broad
// enough to match anything (path "*") would ordinarily refuse every tool
// whose arguments carry a matching path field, but ask_user carries no
// such field and, more importantly, is bypassed before hardDeny ever runs
// at all -- this test proves the bypass, not merely that ask_user happens
// not to trip this particular pattern.
func TestAuthorizeNeverDeniesAskUserDespiteHardDeny(t *testing.T) {
	perm := testPermissions()
	perm.ShellDeny = []string{"*"}
	perm.WriteDeny = []string{"*"}
	guard := New(perm, false, &recordingReviewer{})
	if err := guard.Authorize(context.Background(), askUserToolName, json.RawMessage(`{"question":"ok?"}`)); err != nil {
		t.Fatalf("Authorize(ask_user) with a blanket hard-deny configured = %v, want nil", err)
	}
}

// TestAuthorizeNeverDeniesAskUserUnderReadonly proves the Readonly
// autonomy gate (Step 30), which refuses Sensitive/Critical outright with
// no reviewer consulted, does not reach ask_user at all -- the bypass in
// Authorize runs before the autonomy gate is even read.
func TestAuthorizeNeverDeniesAskUserUnderReadonly(t *testing.T) {
	guard := New(testPermissions(), false, &recordingReviewer{})
	guard.SetAutonomy(Readonly)
	if err := guard.Authorize(context.Background(), askUserToolName, json.RawMessage(`{"question":"ok?"}`)); err != nil {
		t.Fatalf("Authorize(ask_user) under Readonly = %v, want nil", err)
	}
}

// TestAuthorizeNeverDeniesAskUserWithShellDenyMode proves a Shell mode of
// "deny" -- which ordinarily refuses bash/dispatch outright via mode() --
// has no effect on ask_user, since the bypass runs before mode() is even
// consulted.
func TestAuthorizeNeverDeniesAskUserWithShellDenyMode(t *testing.T) {
	perm := testPermissions()
	perm.Shell = "deny"
	perm.Write = "deny"
	perm.Read = "deny"
	guard := New(perm, false, &recordingReviewer{})
	if err := guard.Authorize(context.Background(), askUserToolName, json.RawMessage(`{"question":"ok?"}`)); err != nil {
		t.Fatalf("Authorize(ask_user) with every mode set to deny = %v, want nil", err)
	}
}

// TestAuthorizeAskUserNeverConsultsTheReviewer proves ask_user's bypass is
// a true short-circuit, not merely "always resolves to allow after asking"
// -- a Reviewer that would panic or fail if ever called stays uncalled.
func TestAuthorizeAskUserNeverConsultsTheReviewer(t *testing.T) {
	reviewer := &recordingReviewer{decision: Decision{Allow: false}}
	guard := New(testPermissions(), false, reviewer)
	if err := guard.Authorize(context.Background(), askUserToolName, json.RawMessage(`{"question":"ok?"}`)); err != nil {
		t.Fatalf("Authorize(ask_user) = %v, want nil even though the reviewer would deny", err)
	}
	if reviewer.calls != 0 {
		t.Fatalf("reviewer calls = %d, want 0 (ask_user must never reach the reviewer)", reviewer.calls)
	}
}

// TestAuthorizeAskUserSurvivesWithNoReviewerAtAll proves the bypass runs
// even when no reviewer exists (the identical shape headless mode uses),
// which would otherwise be exactly the case that ends the turn per
// Authorize's own "no reviewer is available" refusal further down.
func TestAuthorizeAskUserSurvivesWithNoReviewerAtAll(t *testing.T) {
	guard := New(testPermissions(), false, nil)
	if err := guard.Authorize(context.Background(), askUserToolName, json.RawMessage(`{"question":"ok?"}`)); err != nil {
		t.Fatalf("Authorize(ask_user) with reviewer=nil = %v, want nil", err)
	}
}

// TestAuthorizeAskUserBypassIsUnaffectedByYoloOrItsAbsence proves the
// bypass is unconditional -- identical outcome whether --yolo is set or
// not, unlike every other tool's Authorize path which behaves differently
// under each.
func TestAuthorizeAskUserBypassIsUnaffectedByYoloOrItsAbsence(t *testing.T) {
	for _, yolo := range []bool{false, true} {
		guard := New(testPermissions(), yolo, &recordingReviewer{decision: Decision{Allow: false}})
		if err := guard.Authorize(context.Background(), askUserToolName, json.RawMessage(`{"question":"ok?"}`)); err != nil {
			t.Fatalf("Authorize(ask_user) with yolo=%v = %v, want nil", yolo, err)
		}
	}
}

// TestAuthorizeAskUserNeverGrantsASessionEntry proves the bypass returns
// before requestKey/session-grant bookkeeping ever runs -- ask_user has no
// session-grant concept because it is never denied in the first place, so
// there is nothing for a grant to remember.
func TestAuthorizeAskUserNeverGrantsASessionEntry(t *testing.T) {
	guard := New(testPermissions(), false, &recordingReviewer{})
	if err := guard.Authorize(context.Background(), askUserToolName, json.RawMessage(`{"question":"ok?"}`)); err != nil {
		t.Fatalf("Authorize(ask_user) = %v, want nil", err)
	}
	if guard.hasSessionGrant(requestKey(Request{Name: askUserToolName, Arguments: json.RawMessage(`{"question":"ok?"}`)})) {
		t.Fatal("ask_user must never leave a session grant behind")
	}
}

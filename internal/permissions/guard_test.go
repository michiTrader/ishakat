package permissions

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/MichiTrader/ishakat/internal/config"
)

type recordingReviewer struct {
	decision Decision
	calls    int
	request  Request
}

func (r *recordingReviewer) Review(_ context.Context, request Request) (Decision, error) {
	r.calls++
	r.request = request
	return r.decision, nil
}

func testPermissions() config.Permissions {
	return config.Permissions{
		Read: "allow", Write: "ask", Shell: "ask", AllowSession: true,
		ShellDeny: []string{"rm -rf /", "git push --force*"},
		WriteDeny: []string{"**/.env", "~/.ssh/**"},
	}
}

func TestGuardAllowsReadWithoutReview(t *testing.T) {
	reviewer := &recordingReviewer{}
	guard := New(testPermissions(), false, reviewer)
	if err := guard.Authorize(context.Background(), "read_file", json.RawMessage(`{"path":"notes.txt"}`)); err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}
	if reviewer.calls != 0 {
		t.Fatalf("reviewer calls = %d, want 0", reviewer.calls)
	}
}

func TestGuardAsksThenRemembersExactMediumRequest(t *testing.T) {
	reviewer := &recordingReviewer{decision: Decision{Allow: true, AllowSession: true}}
	guard := New(testPermissions(), false, reviewer)
	args := json.RawMessage(`{"path":"notes.txt","content":"hello"}`)
	for i := 0; i < 2; i++ {
		if err := guard.Authorize(context.Background(), "write_file", args); err != nil {
			t.Fatalf("Authorize() error = %v", err)
		}
	}
	if reviewer.calls != 1 {
		t.Fatalf("reviewer calls = %d, want 1", reviewer.calls)
	}
	if reviewer.request.Tier != Medium {
		t.Fatalf("tier = %v, want Medium", reviewer.request.Tier)
	}
}

func TestGuardDoesNotShareApprovalWithDifferentArguments(t *testing.T) {
	reviewer := &recordingReviewer{decision: Decision{Allow: true, AllowSession: true}}
	guard := New(testPermissions(), false, reviewer)
	if err := guard.Authorize(context.Background(), "bash", json.RawMessage(`{"command":"pwd"}`)); err != nil {
		t.Fatal(err)
	}
	if err := guard.Authorize(context.Background(), "bash", json.RawMessage(`{"command":"ls"}`)); err != nil {
		t.Fatal(err)
	}
	if reviewer.calls != 2 {
		t.Fatalf("reviewer calls = %d, want 2", reviewer.calls)
	}
}

func TestGuardHardDeniesBeforeYoloOrReview(t *testing.T) {
	reviewer := &recordingReviewer{decision: Decision{Allow: true}}
	guard := New(testPermissions(), true, reviewer)
	for _, request := range []struct {
		name string
		args string
	}{
		{"bash", `{"command":"git push --force origin main"}`},
		{"write_file", `{"path":"project/.env","content":"secret"}`},
		{"read_file", `{"path":"~/.ssh/id_rsa"}`},
	} {
		err := guard.Authorize(context.Background(), request.name, json.RawMessage(request.args))
		if !errors.Is(err, ErrDenied) {
			t.Errorf("Authorize(%s) error = %v, want ErrDenied", request.name, err)
		}
	}
	if reviewer.calls != 0 {
		t.Fatalf("reviewer calls = %d, want 0", reviewer.calls)
	}
}

func TestGuardYoloAllowsAskButNotConfiguredDeny(t *testing.T) {
	permissions := testPermissions()
	permissions.Write = "deny"
	guard := New(permissions, true, nil)
	err := guard.Authorize(context.Background(), "write_file", json.RawMessage(`{"path":"notes.txt","content":"ok"}`))
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("Authorize() error = %v, want ErrDenied", err)
	}
}

func TestGuardYoloDoesNotAllowHighRiskTools(t *testing.T) {
	guard := New(testPermissions(), true, nil)
	err := guard.Authorize(context.Background(), "tool_create", json.RawMessage(`{}`))
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("Authorize() error = %v, want ErrDenied", err)
	}
}

func TestGuardAllowsFetchWithoutReview(t *testing.T) {
	// fetch is danger:low (§19.1) and shares Read's policy knob (guard.go's
	// mode() doc comment): the egress allowlist, not this guard, is what
	// stops an unwanted host, so a Read="allow" configuration must not
	// additionally prompt for a fetch call the way it does not for
	// read_file/glob/grep.
	reviewer := &recordingReviewer{}
	guard := New(testPermissions(), false, reviewer)
	if err := guard.Authorize(context.Background(), "fetch", json.RawMessage(`{"url":"https://example.com"}`)); err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}
	if reviewer.calls != 0 {
		t.Fatalf("reviewer calls = %d, want 0", reviewer.calls)
	}
}

func TestGuardFetchTierIsLow(t *testing.T) {
	if got := tierFor("fetch"); got != Low {
		t.Fatalf("tierFor(%q) = %v, want Low", "fetch", got)
	}
}

// TestGuardDispatchTierIsHighAndNative pins dispatch's (Step 22) explicit
// case in tierFor/isNativeToolName: High like bash, and a manifest naming
// itself "dispatch" cannot reduce that tier via SetToolTiers, the same
// guarantee TestGuardSetToolTiersCannotLowerNativeToolTier already checks
// for bash.
func TestGuardDispatchTierIsHighAndNative(t *testing.T) {
	if got := tierFor("dispatch"); got != High {
		t.Fatalf("tierFor(%q) = %v, want High", "dispatch", got)
	}
	if !isNativeToolName("dispatch") {
		t.Fatal("isNativeToolName(\"dispatch\") = false, want true")
	}
	guard := New(testPermissions(), false, &recordingReviewer{})
	guard.SetToolTiers(map[string]Tier{"dispatch": Low})
	if got := guard.tierFor("dispatch"); got != High {
		t.Fatalf("guard.tierFor(%q) after SetToolTiers(Low) = %v, want High (native tier cannot be lowered)", "dispatch", got)
	}
}

func TestGuardSetToolTiersLowSkipsReview(t *testing.T) {
	reviewer := &recordingReviewer{}
	guard := New(testPermissions(), false, reviewer)
	guard.SetToolTiers(map[string]Tier{"greet": Low})
	if err := guard.Authorize(context.Background(), "greet", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}
	if reviewer.calls != 0 {
		t.Fatalf("reviewer calls = %d, want 0 (Low tier should never be reviewed)", reviewer.calls)
	}
}

func TestGuardSetToolTiersMediumUsesWritePolicy(t *testing.T) {
	permissions := testPermissions()
	permissions.Write = "allow"
	reviewer := &recordingReviewer{}
	guard := New(permissions, false, reviewer)
	guard.SetToolTiers(map[string]Tier{"greet": Medium})
	if err := guard.Authorize(context.Background(), "greet", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}
	if reviewer.calls != 0 {
		t.Fatalf("reviewer calls = %d, want 0 (write=allow should skip review for a Medium-tier declarative tool)", reviewer.calls)
	}
}

func TestGuardSetToolTiersCannotLowerNativeToolTier(t *testing.T) {
	reviewer := &recordingReviewer{decision: Decision{Allow: true}}
	guard := New(testPermissions(), false, reviewer)
	// A manifest (or any caller) naming itself "bash" must not reduce
	// bash's own hardcoded High tier -- tierFor's fixed switch always
	// wins for the seven native names.
	guard.SetToolTiers(map[string]Tier{"bash": Low})
	if err := guard.Authorize(context.Background(), "bash", json.RawMessage(`{"command":"pwd"}`)); err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}
	if reviewer.calls != 1 {
		t.Fatalf("reviewer calls = %d, want 1 (bash must still be reviewed despite SetToolTiers)", reviewer.calls)
	}
}

func TestGuardNilToolTiersBehavesAsBeforeStep20(t *testing.T) {
	reviewer := &recordingReviewer{decision: Decision{Allow: true, AllowSession: true}}
	guard := New(testPermissions(), false, reviewer)
	// SetToolTiers never called: an unrecognized name must still default
	// to High and still consult the reviewer via Shell's policy, exactly
	// like TestGuardUnknownToolIsHighAndCannotGainSessionApproval already
	// pins for the pre-Step-20 case.
	if err := guard.Authorize(context.Background(), "future_tool", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if reviewer.calls != 1 {
		t.Fatalf("reviewer calls = %d, want 1", reviewer.calls)
	}
	if reviewer.request.Tier != High {
		t.Fatalf("tier = %v, want High", reviewer.request.Tier)
	}
}

func TestGuardUnknownToolIsHighAndCannotGainSessionApproval(t *testing.T) {
	reviewer := &recordingReviewer{decision: Decision{Allow: true, AllowSession: true}}
	guard := New(testPermissions(), false, reviewer)
	args := json.RawMessage(`{}`)
	for i := 0; i < 2; i++ {
		if err := guard.Authorize(context.Background(), "future_tool", args); err != nil {
			t.Fatal(err)
		}
	}
	if reviewer.calls != 2 {
		t.Fatalf("reviewer calls = %d, want 2", reviewer.calls)
	}
}

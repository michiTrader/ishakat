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

// --- §21.9 fix 1: which refusals end the turn -------------------------------
//
// The tests below pin the distinction refusal()/deniedError draw. They matter
// more than their size suggests: getting this line wrong in either direction
// reintroduces a defect that already reached a real user. Too narrow, and a
// human's "no" comes back as data the model routes around, one provider
// request per attempt (docs/BUG-rate-limit-amplifier.md). Too wide, and an
// ordinary configuration boundary kills the turn instead of letting the model
// write somewhere legal — which would break the error-is-data mechanism §3
// relies on to avoid needing a Planner.

// asksDenied reports whether err carries the turn-ending contract
// internal/engine matches with errors.As. It is deliberately written the same
// way engine matches it, rather than with a type assertion on *deniedError, so
// these tests fail if the structural contract stops being satisfied even
// though the concrete type is unchanged.
func asksDenied(err error) bool {
	var d interface{ Denied() bool }
	return errors.As(err, &d) && d.Denied()
}

func TestUserDeclineEndsTheTurn(t *testing.T) {
	reviewer := &recordingReviewer{decision: Decision{Allow: false}}
	guard := New(testPermissions(), false, reviewer)

	err := guard.Authorize(context.Background(), "bash", json.RawMessage(`{"command":"ls"}`))
	if err == nil {
		t.Fatal("a declined request must return an error")
	}
	if !errors.Is(err, ErrDenied) {
		t.Errorf("errors.Is(err, ErrDenied) = false, want true: every refusal stays recognizable to existing call sites")
	}
	if !asksDenied(err) {
		t.Error("a human pressing \"no\" must end the turn: the model must not be handed the refusal as data to route around")
	}
}

func TestNoReviewerEndsTheTurn(t *testing.T) {
	// No reviewer is the headless/serve-without-a-human case. Nothing in this
	// turn can produce an approval, so retrying variants is pure cost.
	guard := New(testPermissions(), false, nil)

	err := guard.Authorize(context.Background(), "bash", json.RawMessage(`{"command":"ls"}`))
	if err == nil {
		t.Fatal("no reviewer must refuse an ask-tier request")
	}
	if !asksDenied(err) {
		t.Error("with nobody to ask, the turn must end rather than loop asking")
	}
}

func TestReviewerFailureEndsTheTurn(t *testing.T) {
	guard := New(testPermissions(), false, failingReviewer{})

	err := guard.Authorize(context.Background(), "bash", json.RawMessage(`{"command":"ls"}`))
	if err == nil {
		t.Fatal("a failing reviewer must refuse")
	}
	if !asksDenied(err) {
		t.Error("if asking itself is broken, asking again in the same turn cannot work: the turn must end")
	}
}

// TestConfigurationDenialsStayData is the other half, and the one that keeps
// fix 1 from overreaching. A boundary that refused *these arguments* leaves
// legal alternatives open, and the model picking one is correct recovery
// rather than a loop. These must NOT end the turn.
func TestConfigurationDenialsStayData(t *testing.T) {
	perms := testPermissions()
	perms.Write = "deny"
	guard := New(perms, false, &recordingReviewer{decision: Decision{Allow: true}})

	cases := []struct {
		name string
		tool string
		args string
		why  string
	}{
		{
			name: "hard deny on a shell pattern",
			tool: "bash",
			args: `{"command":"rm -rf /"}`,
			why:  "another command may be perfectly legal",
		},
		{
			name: "hard deny on a protected path",
			tool: "write_file",
			args: `{"path":"/home/u/.ssh/config","content":"x"}`,
			why:  "writing to a different path is the right next move",
		},
		{
			name: "tool disabled by configuration",
			tool: "write_file",
			args: `{"path":"/tmp/ok.txt","content":"x"}`,
			why:  "the model can still read, search and report instead",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := guard.Authorize(context.Background(), tc.tool, json.RawMessage(tc.args))
			if err == nil {
				t.Fatalf("%s must be refused", tc.tool)
			}
			if !errors.Is(err, ErrDenied) {
				t.Errorf("errors.Is(err, ErrDenied) = false, want true")
			}
			if asksDenied(err) {
				t.Errorf("this refusal must stay tool-error data, not end the turn: %s", tc.why)
			}
		})
	}
}

type failingReviewer struct{}

func (failingReviewer) Review(context.Context, Request) (Decision, error) {
	return Decision{}, errors.New("no tty")
}

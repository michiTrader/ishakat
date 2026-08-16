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

func TestGuardAsksThenRemembersExactSensitiveRequest(t *testing.T) {
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
	if reviewer.request.Tier != Sensitive {
		t.Fatalf("tier = %v, want Sensitive", reviewer.request.Tier)
	}
}

func TestGuardDoesNotShareApprovalWithDifferentArguments(t *testing.T) {
	// "echo one"/"echo two" rather than pwd/ls -- ls and pwd are Safe under
	// bashTier and never reach the reviewer at all, so they cannot exercise
	// this test's actual point (different arguments must not share an
	// approval).
	reviewer := &recordingReviewer{decision: Decision{Allow: true, AllowSession: true}}
	guard := New(testPermissions(), false, reviewer)
	if err := guard.Authorize(context.Background(), "bash", json.RawMessage(`{"command":"echo one"}`)); err != nil {
		t.Fatal(err)
	}
	if err := guard.Authorize(context.Background(), "bash", json.RawMessage(`{"command":"echo two"}`)); err != nil {
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

func TestGuardYoloDoesNotAllowCriticalRiskTools(t *testing.T) {
	guard := New(testPermissions(), true, nil)
	err := guard.Authorize(context.Background(), "tool_create", json.RawMessage(`{}`))
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("Authorize() error = %v, want ErrDenied", err)
	}
}

// TestGuardYoloDoesNotBypassCriticalBashCommand pins §21.16 decision 2 at
// the bash level specifically: --yolo turning ask into allow for bash
// commands in general must still stop short of a Critical-shaped one like
// git push, exactly as it already does for the "tool_create is Critical"
// case above.
func TestGuardYoloDoesNotBypassCriticalBashCommand(t *testing.T) {
	reviewer := &recordingReviewer{decision: Decision{Allow: true}}
	guard := New(testPermissions(), true, reviewer)
	err := guard.Authorize(context.Background(), "bash", json.RawMessage(`{"command":"git push origin main"}`))
	if err != nil {
		t.Fatalf("Authorize() error = %v, want nil (reviewer allowed it)", err)
	}
	if reviewer.calls != 1 {
		t.Fatalf("reviewer calls = %d, want 1 (yolo must not bypass a Critical bash command)", reviewer.calls)
	}
	if reviewer.request.Tier != Critical {
		t.Fatalf("tier = %v, want Critical", reviewer.request.Tier)
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

func TestGuardFetchTierIsSafe(t *testing.T) {
	if got := tierFor("fetch"); got != Safe {
		t.Fatalf("tierFor(%q) = %v, want Safe", "fetch", got)
	}
}

// TestGuardDispatchTierIsCriticalAndNative pins dispatch's (Step 22)
// explicit case in tierFor/isNativeToolName: Critical like bash's own
// fallback, and a manifest naming itself "dispatch" cannot reduce that tier
// via SetToolTiers, the same guarantee
// TestGuardSetToolTiersCannotLowerNativeToolTier already checks for bash.
func TestGuardDispatchTierIsCriticalAndNative(t *testing.T) {
	if got := tierFor("dispatch"); got != Critical {
		t.Fatalf("tierFor(%q) = %v, want Critical", "dispatch", got)
	}
	if !isNativeToolName("dispatch") {
		t.Fatal("isNativeToolName(\"dispatch\") = false, want true")
	}
	guard := New(testPermissions(), false, &recordingReviewer{})
	guard.SetToolTiers(map[string]Tier{"dispatch": Safe})
	if got := guard.tierFor("dispatch", json.RawMessage(`{}`)); got != Critical {
		t.Fatalf("guard.tierFor(%q) after SetToolTiers(Safe) = %v, want Critical (native tier cannot be lowered)", "dispatch", got)
	}
}

func TestGuardSetToolTiersSafeSkipsReview(t *testing.T) {
	reviewer := &recordingReviewer{}
	guard := New(testPermissions(), false, reviewer)
	guard.SetToolTiers(map[string]Tier{"greet": Safe})
	if err := guard.Authorize(context.Background(), "greet", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}
	if reviewer.calls != 0 {
		t.Fatalf("reviewer calls = %d, want 0 (Safe tier should never be reviewed)", reviewer.calls)
	}
}

func TestGuardSetToolTiersSensitiveUsesWritePolicy(t *testing.T) {
	permissions := testPermissions()
	permissions.Write = "allow"
	reviewer := &recordingReviewer{}
	guard := New(permissions, false, reviewer)
	guard.SetToolTiers(map[string]Tier{"greet": Sensitive})
	if err := guard.Authorize(context.Background(), "greet", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}
	if reviewer.calls != 0 {
		t.Fatalf("reviewer calls = %d, want 0 (write=allow should skip review for a Sensitive-tier declarative tool)", reviewer.calls)
	}
}

func TestGuardSetToolTiersCannotLowerNativeToolTier(t *testing.T) {
	reviewer := &recordingReviewer{decision: Decision{Allow: true}}
	guard := New(testPermissions(), false, reviewer)
	// A manifest (or any caller) naming itself "bash" must not reduce
	// bash's own tier -- (*Guard).tierFor special-cases bash before ever
	// consulting g.tiers, so SetToolTiers cannot affect it regardless of
	// what it maps "bash" to. "echo hi" is a Sensitive-shaped command
	// (not one of the safe/controlled prefixes), so this still reaches
	// the reviewer as the test expects.
	guard.SetToolTiers(map[string]Tier{"bash": Safe})
	if err := guard.Authorize(context.Background(), "bash", json.RawMessage(`{"command":"echo hi"}`)); err != nil {
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
	// to Critical and still consult the reviewer via Shell's policy,
	// exactly like TestGuardUnknownToolIsCriticalAndCannotGainSessionApproval
	// already pins for the pre-Step-20 case.
	if err := guard.Authorize(context.Background(), "future_tool", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if reviewer.calls != 1 {
		t.Fatalf("reviewer calls = %d, want 1", reviewer.calls)
	}
	if reviewer.request.Tier != Critical {
		t.Fatalf("tier = %v, want Critical", reviewer.request.Tier)
	}
}

func TestGuardUnknownToolIsCriticalAndCannotGainSessionApproval(t *testing.T) {
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

// --- Step 28 closing criterion: ls/git status/go build never prompt; git
// push always does (docs/PLAN.md §21.14) -----------------------------------

// TestClosingCriterionSafeBashCommandsNeverPrompt is this step's own
// closing criterion, half one: read-only bash commands must never reach a
// reviewer regardless of configuration mode (testPermissions sets
// Shell="ask", the strictest ordinary setting).
func TestClosingCriterionSafeBashCommandsNeverPrompt(t *testing.T) {
	for _, cmd := range []string{
		"ls", "ls -la", "pwd", "cat notes.txt",
		"git status", "git status --short",
		"git diff", "git diff HEAD~1",
		"git log", "git log --oneline",
		"node -v", "node --version",
	} {
		t.Run(cmd, func(t *testing.T) {
			reviewer := &recordingReviewer{decision: Decision{Allow: true}}
			guard := New(testPermissions(), false, reviewer)
			args, _ := json.Marshal(map[string]string{"command": cmd})
			if err := guard.Authorize(context.Background(), "bash", args); err != nil {
				t.Fatalf("Authorize(%q) error = %v", cmd, err)
			}
			if reviewer.calls != 0 {
				t.Errorf("Authorize(%q) called the reviewer %d times, want 0", cmd, reviewer.calls)
			}
		})
	}
}

// TestClosingCriterionControlledBashCommandsNeverPrompt is the closing
// criterion's second half: go build (and its siblings) must never prompt
// either, since Controlled bypasses review the same as Safe until Step 30
// introduces autonomy (see Tier's own doc comment).
func TestClosingCriterionControlledBashCommandsNeverPrompt(t *testing.T) {
	for _, cmd := range []string{
		"go test ./...", "go build ./...", "go vet ./...", "make", "npm test",
	} {
		t.Run(cmd, func(t *testing.T) {
			reviewer := &recordingReviewer{decision: Decision{Allow: true}}
			guard := New(testPermissions(), false, reviewer)
			args, _ := json.Marshal(map[string]string{"command": cmd})
			if err := guard.Authorize(context.Background(), "bash", args); err != nil {
				t.Fatalf("Authorize(%q) error = %v", cmd, err)
			}
			if reviewer.calls != 0 {
				t.Errorf("Authorize(%q) called the reviewer %d times, want 0", cmd, reviewer.calls)
			}
		})
	}
}

// TestClosingCriterionGitPushAlwaysAsks is the closing criterion's third
// clause: git push always asks, under both ordinary and --yolo operation.
func TestClosingCriterionGitPushAlwaysAsks(t *testing.T) {
	for _, yolo := range []bool{false, true} {
		t.Run("", func(t *testing.T) {
			reviewer := &recordingReviewer{decision: Decision{Allow: true}}
			guard := New(testPermissions(), yolo, reviewer)
			err := guard.Authorize(context.Background(), "bash", json.RawMessage(`{"command":"git push origin main"}`))
			if err != nil {
				t.Fatalf("Authorize() error = %v, want nil", err)
			}
			if reviewer.calls != 1 {
				t.Errorf("yolo=%v: reviewer calls = %d, want exactly 1", yolo, reviewer.calls)
			}
			if reviewer.request.Tier != Critical {
				t.Errorf("yolo=%v: tier = %v, want Critical", yolo, reviewer.request.Tier)
			}
		})
	}
}

// TestGitPushCannotGainSessionApproval pins §21.16 decision 2's other
// half: even if a reviewer offers AllowSession for a git push (which a
// well-behaved UI never should, per tierLabel/newToolApproveDialog only
// offering that row for Sensitive), Guard itself must not honor it.
func TestGitPushCannotGainSessionApproval(t *testing.T) {
	reviewer := &recordingReviewer{decision: Decision{Allow: true, AllowSession: true}}
	guard := New(testPermissions(), false, reviewer)
	args := json.RawMessage(`{"command":"git push origin main"}`)
	for i := 0; i < 2; i++ {
		if err := guard.Authorize(context.Background(), "bash", args); err != nil {
			t.Fatalf("Authorize() error = %v", err)
		}
	}
	if reviewer.calls != 2 {
		t.Fatalf("reviewer calls = %d, want 2 (git push must never gain a session grant)", reviewer.calls)
	}
}

// TestBashTierClassifiesUnrecognizedCommandsAsSensitive also pins the
// compound-command safety guard: a naive prefix check would have
// classified "ls && rm -rf /tmp/x" as Safe merely because it starts with
// "ls" -- this table caught that real bug during development.
func TestBashTierClassifiesUnrecognizedCommandsAsSensitive(t *testing.T) {
	for _, cmd := range []string{
		"echo hi",
		"npm install left-pad",
		"ls && rm -rf /tmp/x",
		"lsof -i",
	} {
		t.Run(cmd, func(t *testing.T) {
			args, _ := json.Marshal(map[string]string{"command": cmd})
			if got := bashTier(args); got != Sensitive {
				t.Errorf("bashTier(%q) = %v, want Sensitive", cmd, got)
			}
		})
	}
}

// TestBashTierCatchesGitPushAfterSequencing pins containsAfterMeta: a git
// push embedded after a sequencing operator must still classify as
// Critical, not fall through to Sensitive via the compound-command guard.
func TestBashTierCatchesGitPushAfterSequencing(t *testing.T) {
	for _, cmd := range []string{
		"go build ./... && git push origin main",
		"echo done; git push origin main",
	} {
		t.Run(cmd, func(t *testing.T) {
			args, _ := json.Marshal(map[string]string{"command": cmd})
			if got := bashTier(args); got != Critical {
				t.Errorf("bashTier(%q) = %v, want Critical", cmd, got)
			}
		})
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

	// "echo hi", not "ls" -- ls is Safe under bashTier and never reaches
	// the reviewer, so it cannot exercise this test's point (a decline
	// must end the turn).
	err := guard.Authorize(context.Background(), "bash", json.RawMessage(`{"command":"echo hi"}`))
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

	err := guard.Authorize(context.Background(), "bash", json.RawMessage(`{"command":"echo hi"}`))
	if err == nil {
		t.Fatal("no reviewer must refuse an ask-tier request")
	}
	if !asksDenied(err) {
		t.Error("with nobody to ask, the turn must end rather than loop asking")
	}
}

func TestReviewerFailureEndsTheTurn(t *testing.T) {
	guard := New(testPermissions(), false, failingReviewer{})

	err := guard.Authorize(context.Background(), "bash", json.RawMessage(`{"command":"echo hi"}`))
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

// --- Step 29 closing criterion: a session grant for a bash command covers
// a flag variant, and bash can gain a session grant at all (docs/PLAN.md
// §21.14, fixing §21.3 defects 2 and 3) -------------------------------------

// TestClosingCriterionSessionGrantCoversFlagVariant is this step's own
// closing criterion, first half. "ls" itself is Safe under bashTier (Step
// 28) and never reaches the reviewer, so the literal example §21.3/§21.14
// name has to be read against a Sensitive-tier command instead -- "npm
// install left-pad" granted for the session must cover "npm install
// left-pad --save-dev", the same flag-only variation defect 2's own worked
// example describes for ls/ls -la.
func TestClosingCriterionSessionGrantCoversFlagVariant(t *testing.T) {
	reviewer := &recordingReviewer{decision: Decision{Allow: true, AllowSession: true}}
	guard := New(testPermissions(), false, reviewer)
	if err := guard.Authorize(context.Background(), "bash", json.RawMessage(`{"command":"npm install left-pad"}`)); err != nil {
		t.Fatal(err)
	}
	if err := guard.Authorize(context.Background(), "bash", json.RawMessage(`{"command":"npm install left-pad --save-dev"}`)); err != nil {
		t.Fatal(err)
	}
	if reviewer.calls != 1 {
		t.Fatalf("reviewer calls = %d, want 1 (a session grant must cover a flag-only variant, §21.3 defect 2)", reviewer.calls)
	}
}

// TestClosingCriterionBashSessionGrantIsHonoured is the closing criterion's
// second half, defect 3 by name: "the branch requires Tier == Medium; bash
// is High (defect 1). So the one tool that generates most of the dialogs is
// the one tool for which 'allow for session' is silently ignored." Step 28
// already made bash's ordinary case Sensitive (not the old unconditional
// High), which happens to satisfy Authorize's req.Tier == Sensitive guard
// as a side effect; this test pins that the grant is genuinely honoured for
// bash specifically, rather than merely assumed from Step 28's own tests
// (none of which exercised AllowSession for a Sensitive-tier bash command
// at all).
func TestClosingCriterionBashSessionGrantIsHonoured(t *testing.T) {
	reviewer := &recordingReviewer{decision: Decision{Allow: true, AllowSession: true}}
	guard := New(testPermissions(), false, reviewer)
	args := json.RawMessage(`{"command":"echo hi"}`)
	for i := 0; i < 2; i++ {
		if err := guard.Authorize(context.Background(), "bash", args); err != nil {
			t.Fatal(err)
		}
	}
	if reviewer.calls != 1 {
		t.Fatalf("reviewer calls = %d, want 1 (a bash session grant must be honoured, §21.3 defect 3)", reviewer.calls)
	}
}

// TestBashFamilyGeneralizesOverFlagsOnly pins bashFamily's own scope
// directly: it strips flag tokens (leading "-") but must never merge two
// commands that disagree on a non-flag token, keeping
// TestGuardDoesNotShareApprovalWithDifferentArguments's own guarantee true
// at the unit level, not just observed through Authorize.
func TestBashFamilyGeneralizesOverFlagsOnly(t *testing.T) {
	if got := bashFamily("npm install left-pad"); got != bashFamily("npm install left-pad --save-dev") {
		t.Errorf("bashFamily disagreed on a flag-only variant: %q vs %q", bashFamily("npm install left-pad"), bashFamily("npm install left-pad --save-dev"))
	}
	if got := bashFamily("echo one"); got == bashFamily("echo two") {
		t.Errorf("bashFamily(%q) = %q must differ from bashFamily(\"echo two\"): different positional arguments must not collapse into one family", "echo one", got)
	}
}

// TestGuardSessionGrantDoesNotLeakAcrossToolNames pins requestKey's own
// boundary: bashSessionKey's generalization is bash-specific, and a
// non-bash Sensitive tool (write_file) keeps the exact-byte key defect 2's
// fix does not touch -- widening every tool's grant is future work
// (§21.12), not this step's closing criterion.
func TestGuardSessionGrantDoesNotLeakAcrossToolNames(t *testing.T) {
	reviewer := &recordingReviewer{decision: Decision{Allow: true, AllowSession: true}}
	guard := New(testPermissions(), false, reviewer)
	if err := guard.Authorize(context.Background(), "write_file", json.RawMessage(`{"path":"a.txt","content":"one"}`)); err != nil {
		t.Fatal(err)
	}
	if err := guard.Authorize(context.Background(), "write_file", json.RawMessage(`{"path":"a.txt","content":"two"}`)); err != nil {
		t.Fatal(err)
	}
	if reviewer.calls != 2 {
		t.Fatalf("reviewer calls = %d, want 2 (write_file must keep its exact-byte session key)", reviewer.calls)
	}
}

// TestParseAutonomyRoundTrips pins ParseAutonomy/Autonomy.String's shared
// vocabulary ("auto", "agile", "readonly") and ParseAutonomy's own
// "unrecognized or empty defaults to Auto" contract, matching the type's
// zero-value rule.
func TestParseAutonomyRoundTrips(t *testing.T) {
	cases := []struct {
		in   string
		want Autonomy
	}{
		{"auto", Auto},
		{"agile", Agile},
		{"readonly", Readonly},
		{"", Auto},
		{"bogus", Auto},
	}
	for _, c := range cases {
		if got := ParseAutonomy(c.in); got != c.want {
			t.Errorf("ParseAutonomy(%q) = %v, want %v", c.in, got, c.want)
		}
	}
	for _, a := range []Autonomy{Auto, Agile, Readonly} {
		if got := ParseAutonomy(a.String()); got != a {
			t.Errorf("ParseAutonomy(%v.String()) = %v, want %v", a, got, a)
		}
	}
}

// TestClosingCriterionReadonlyRefusesSensitiveAndCriticalWithoutAsking is
// this step's own §21.5 pin: under Readonly, a Sensitive or Critical
// request is refused outright -- the reviewer is never even consulted,
// which is the "quieter, not just stricter" property an audit session
// needs (see Authorize's own doc comment).
func TestClosingCriterionReadonlyRefusesSensitiveAndCriticalWithoutAsking(t *testing.T) {
	reviewer := &recordingReviewer{decision: Decision{Allow: true}}
	guard := New(testPermissions(), false, reviewer)
	guard.SetAutonomy(Readonly)

	if err := guard.Authorize(context.Background(), "write_file", json.RawMessage(`{"path":"a.txt","content":"x"}`)); !errors.Is(err, ErrDenied) {
		t.Fatalf("write_file under readonly: err = %v, want ErrDenied", err)
	}
	if err := guard.Authorize(context.Background(), "bash", json.RawMessage(`{"command":"git push origin main"}`)); !errors.Is(err, ErrDenied) {
		t.Fatalf("git push under readonly: err = %v, want ErrDenied", err)
	}
	if reviewer.calls != 0 {
		t.Fatalf("reviewer calls = %d, want 0 (readonly must refuse before asking)", reviewer.calls)
	}
}

// TestReadonlyStillRunsSafeButAsksControlled pins the other two rows of
// §21.5's Readonly column: reads/safe commands keep running with no
// dialog, but a Controlled command (go test, go build, ...) -- which
// bypasses review under every other autonomy -- now asks, since Readonly
// is the one autonomy that does not trust an unattended build/test run.
func TestReadonlyStillRunsSafeButAsksControlled(t *testing.T) {
	reviewer := &recordingReviewer{decision: Decision{Allow: true}}
	guard := New(testPermissions(), false, reviewer)
	guard.SetAutonomy(Readonly)

	if err := guard.Authorize(context.Background(), "bash", json.RawMessage(`{"command":"ls -la"}`)); err != nil {
		t.Fatalf("safe bash under readonly: err = %v, want nil", err)
	}
	if reviewer.calls != 0 {
		t.Fatalf("reviewer calls after safe command = %d, want 0", reviewer.calls)
	}

	if err := guard.Authorize(context.Background(), "bash", json.RawMessage(`{"command":"go test ./..."}`)); err != nil {
		t.Fatalf("controlled bash under readonly: err = %v, want nil (allowed, after asking)", err)
	}
	if reviewer.calls != 1 {
		t.Fatalf("reviewer calls after controlled command = %d, want 1 (readonly must ask, not silently run, a Controlled command)", reviewer.calls)
	}
}

// TestAutonomyZeroValueIsAutoAndUnchangedBehaviour guards the
// non-regression contract every pre-Step-30 test in this file already
// exercises implicitly: a Guard built by New, with SetAutonomy never
// called, authorizes exactly as it did before this type existed.
func TestAutonomyZeroValueIsAutoAndUnchangedBehaviour(t *testing.T) {
	guard := New(testPermissions(), false, nil)
	if got := guard.Autonomy(); got != Auto {
		t.Fatalf("Autonomy() on a fresh Guard = %v, want Auto (the zero value)", got)
	}
	if err := guard.Authorize(context.Background(), "bash", json.RawMessage(`{"command":"go build ./..."}`)); err != nil {
		t.Fatalf("controlled bash under (default) auto: err = %v, want nil", err)
	}
}

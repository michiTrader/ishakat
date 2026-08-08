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

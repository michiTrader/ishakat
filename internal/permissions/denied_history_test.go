package permissions

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// TestGuardRecordsDenialOnHardDeny covers the two Authorize refusal points
// that never call refusal() at all (hardDeny's reason and mode=="deny"'s
// literal) -- recordDenial must still see them, since a human auditing
// "what was refused" wants every refusal, not only the turn-ending kind
// (refusal's own doc comment explains why that distinction exists, and why
// it does not matter here).
func TestGuardRecordsDenialOnHardDeny(t *testing.T) {
	guard := New(testPermissions(), false, &recordingReviewer{})
	err := guard.Authorize(context.Background(), "bash", json.RawMessage(`{"command":"rm -rf /"}`))
	if err == nil {
		t.Fatalf("Authorize() error = nil, want a denial")
	}
	got := guard.RecentDenials()
	if len(got) != 1 {
		t.Fatalf("RecentDenials() = %d entries, want 1", len(got))
	}
	if got[0].Tool != "bash" {
		t.Errorf("Tool = %q, want %q", got[0].Tool, "bash")
	}
	if got[0].When.IsZero() {
		t.Errorf("When is zero, want a real timestamp")
	}
}

// TestGuardRecordsDenialOnReadonlyRefusal covers the Readonly-autonomy
// refusal path, one of the four that do call refusal().
func TestGuardRecordsDenialOnReadonlyRefusal(t *testing.T) {
	guard := New(testPermissions(), false, &recordingReviewer{})
	guard.SetAutonomy(Readonly)
	args := json.RawMessage(`{"path":"notes.txt","content":"x"}`)
	if err := guard.Authorize(context.Background(), "write_file", args); err == nil {
		t.Fatalf("Authorize() error = nil, want a denial")
	}
	got := guard.RecentDenials()
	if len(got) != 1 || got[0].Tool != "write_file" || got[0].Tier != Sensitive {
		t.Fatalf("RecentDenials() = %+v, want one Sensitive write_file entry", got)
	}
}

// TestGuardRecordsDenialOnReviewerDeclined covers the "user declined"
// refusal path.
func TestGuardRecordsDenialOnReviewerDeclined(t *testing.T) {
	guard := New(testPermissions(), false, &recordingReviewer{decision: Decision{Allow: false}})
	args := json.RawMessage(`{"path":"notes.txt","content":"x"}`)
	if err := guard.Authorize(context.Background(), "write_file", args); err == nil {
		t.Fatalf("Authorize() error = nil, want a denial")
	}
	got := guard.RecentDenials()
	if len(got) != 1 {
		t.Fatalf("RecentDenials() = %d entries, want 1", len(got))
	}
	if got[0].Reason != "user declined write_file" {
		t.Errorf("Reason = %q, want %q", got[0].Reason, "user declined write_file")
	}
}

// TestGuardRecentDenialsIsBoundedAndOldestFirst confirms deniedHistoryLimit
// caps growth by evicting the oldest entry, and that RecentDenials keeps
// chronological (oldest-first) order among what remains.
func TestGuardRecentDenialsIsBoundedAndOldestFirst(t *testing.T) {
	guard := New(testPermissions(), false, &recordingReviewer{})
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < deniedHistoryLimit+5; i++ {
		i := i
		guard.now = func() time.Time { return base.Add(time.Duration(i) * time.Minute) }
		_ = guard.Authorize(context.Background(), "bash", json.RawMessage(`{"command":"rm -rf /"}`))
	}
	got := guard.RecentDenials()
	if len(got) != deniedHistoryLimit {
		t.Fatalf("len(RecentDenials()) = %d, want %d", len(got), deniedHistoryLimit)
	}
	// The oldest surviving entry should be the 6th call (index 5), since
	// the first five were evicted.
	wantFirst := base.Add(5 * time.Minute)
	if !got[0].When.Equal(wantFirst) {
		t.Errorf("got[0].When = %v, want %v", got[0].When, wantFirst)
	}
	wantLast := base.Add(time.Duration(deniedHistoryLimit+4) * time.Minute)
	if !got[len(got)-1].When.Equal(wantLast) {
		t.Errorf("got[last].When = %v, want %v", got[len(got)-1].When, wantLast)
	}
}

// TestGuardRecentDenialsReturnsDefensiveCopy mirrors MissionRules' own
// mutation-safety test shape: mutating the returned slice must never
// corrupt the Guard's own state.
func TestGuardRecentDenialsReturnsDefensiveCopy(t *testing.T) {
	guard := New(testPermissions(), false, &recordingReviewer{})
	_ = guard.Authorize(context.Background(), "bash", json.RawMessage(`{"command":"rm -rf /"}`))
	got := guard.RecentDenials()
	got[0].Tool = "tampered"
	again := guard.RecentDenials()
	if again[0].Tool != "bash" {
		t.Fatalf("RecentDenials() mutated by caller's own copy: got %q", again[0].Tool)
	}
}

// TestGuardAllowedRequestsAreNotRecorded confirms an ordinary allowed
// request never pollutes the denial history.
func TestGuardAllowedRequestsAreNotRecorded(t *testing.T) {
	guard := New(testPermissions(), false, &recordingReviewer{})
	if err := guard.Authorize(context.Background(), "read_file", json.RawMessage(`{"path":"notes.txt"}`)); err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}
	if got := guard.RecentDenials(); len(got) != 0 {
		t.Fatalf("RecentDenials() = %+v, want empty", got)
	}
}

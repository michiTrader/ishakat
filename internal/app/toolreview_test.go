package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/permissions"
)

func TestToolReviewerDeniesBeforeProgramIsAttached(t *testing.T) {
	reviewer := newToolReviewer()
	decision, err := reviewer.Review(context.Background(), permissions.Request{Name: "bash"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Review error = %v, want context.Canceled", err)
	}
	if decision.Allow || decision.AllowSession {
		t.Fatalf("unattached reviewer returned an approval: %+v", decision)
	}
}

func TestToolReviewerSetProgramAttachesTheProgram(t *testing.T) {
	reviewer := newToolReviewer()
	program := tea.NewProgram(nil)

	reviewer.SetProgram(program)

	if reviewer.program != program {
		t.Fatalf("reviewer program = %p, want %p", reviewer.program, program)
	}
}

func TestToolReviewerHonorsContextCancellation(t *testing.T) {
	// A nil program is intentionally used here: cancellation must be checked
	// before attempting to publish a request, and the fail-closed path must
	// remain deterministic even when no Bubble Tea loop exists.
	reviewer := newToolReviewer()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	decision, err := reviewer.Review(ctx, permissions.Request{
		Name:      "write_file",
		Arguments: json.RawMessage(`{"path":"notes.txt"}`),
		Tier:      permissions.Sensitive,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Review error = %v, want context.Canceled", err)
	}
	if decision != (permissions.Decision{}) {
		t.Fatalf("cancelled review decision = %+v, want zero decision", decision)
	}
}

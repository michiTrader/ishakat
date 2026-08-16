package app

import (
	"context"
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/ask"
)

func TestTUIAskerFailsClosedBeforeProgramIsAttached(t *testing.T) {
	asker := newTUIAsker()
	answers, err := asker.Ask(context.Background(), ask.Form{
		Questions: []ask.Question{{ID: "answer", Prompt: "¿cuál preferís?"}},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Ask error = %v, want context.Canceled", err)
	}
	if answers != nil {
		t.Fatalf("unattached asker returned answers: %+v", answers)
	}
}

func TestTUIAskerSetProgramAttachesTheProgram(t *testing.T) {
	asker := newTUIAsker()
	program := tea.NewProgram(nil)

	asker.SetProgram(program)

	if asker.program != program {
		t.Fatalf("asker program = %p, want %p", asker.program, program)
	}
}

func TestTUIAskerHonorsContextCancellation(t *testing.T) {
	// A nil program is intentionally used here, the same reasoning
	// toolReviewer's own cancellation test gives: cancellation must be
	// checked before attempting to publish a request, and the fail-closed
	// path must remain deterministic even when no Bubble Tea loop exists.
	asker := newTUIAsker()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	answers, err := asker.Ask(ctx, ask.Form{
		Questions: []ask.Question{{ID: "answer", Prompt: "¿cuál preferís?"}},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Ask error = %v, want context.Canceled", err)
	}
	if answers != nil {
		t.Fatalf("cancelled ask answers = %+v, want nil", answers)
	}
}

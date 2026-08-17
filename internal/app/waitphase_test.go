package app

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestTUIWaitNotifierOnWaitIsANoOpBeforeProgramIsAttached(t *testing.T) {
	// A nil program (OnWait called before SetProgram) must not panic —
	// see waitphase.go's own comment on why this degrades to a silent
	// no-op rather than the fail-closed error toolReviewer/tuiAsker return
	// for the identical case: nothing here is blocked waiting for an
	// answer, so there is nothing to fail closed on.
	notifier := newTUIWaitNotifier()
	notifier.OnWait(22*time.Second, 1)
}

func TestTUIWaitNotifierSetProgramAttachesTheProgram(t *testing.T) {
	notifier := newTUIWaitNotifier()
	program := tea.NewProgram(nil)

	notifier.SetProgram(program)

	if notifier.program != program {
		t.Fatalf("notifier program = %p, want %p", notifier.program, program)
	}
}

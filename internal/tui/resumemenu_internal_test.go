package tui

import (
	"errors"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/convo"
)

func withSessionLister(root Root, sl SessionLister) Root {
	root.sessionLister = sl
	return root
}

// TestRunResumeCommandWithNoListerNotifies is the "cannot resume at all"
// case: [session] save = false, or a store that never opened — the same
// nil-is-supported rule Recorder's own comment documents for the write
// side.
func TestRunResumeCommandWithNoListerNotifies(t *testing.T) {
	var m tea.Model = newHeadlessRoot() // sessionLister left nil
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	m = typeAndEnter(m, "/resume")

	root := m.(Root)
	if root.mode != ModeChat {
		t.Fatalf("mode = %v, want ModeChat (no lister means no overlay)", root.mode)
	}
	if len(root.transcript) == 0 || root.transcript[len(root.transcript)-1].role != "assistant" {
		t.Fatal("expected a slashNotice explaining there is nothing to resume")
	}
}

// TestRunResumeCommandWithNoSessionsNotifies is the "lister exists but
// nothing is saved yet" case — an empty List() result is not itself a
// failure (SessionLister's own comment), and must read the same as no
// lister at all from the user's point of view.
func TestRunResumeCommandWithNoSessionsNotifies(t *testing.T) {
	sl := &fakeSessionLister{}
	var m tea.Model = withSessionLister(newHeadlessRoot(), sl)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	m = typeAndEnter(m, "/resume")

	root := m.(Root)
	if root.mode != ModeChat {
		t.Fatalf("mode = %v, want ModeChat (no sessions means no overlay)", root.mode)
	}
}

// TestRunResumeCommandOpensTheMenu is the ordinary case: at least one saved
// session opens ModeResume with its rows loaded from List().
func TestRunResumeCommandOpensTheMenu(t *testing.T) {
	sl := &fakeSessionLister{rows: []SessionSummary{
		{ID: "a", Title: "primera sesión", UpdatedAt: time.Now()},
		{ID: "b", Title: "segunda sesión", UpdatedAt: time.Now()},
	}}
	var m tea.Model = withSessionLister(newHeadlessRoot(), sl)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	m = typeAndEnter(m, "/resume")

	root := m.(Root)
	if root.mode != ModeResume {
		t.Fatalf("mode = %v, want ModeResume", root.mode)
	}
	if len(root.resume.rows) != 2 {
		t.Fatalf("resume.rows = %d, want 2", len(root.resume.rows))
	}
}

// TestRunResumeCommandReportsListFailure is the disk-error case: a List()
// failure is a real problem, distinct from "nothing saved yet", and must
// not silently swallow the error.
func TestRunResumeCommandReportsListFailure(t *testing.T) {
	sl := &fakeSessionLister{listErr: errors.New("disco lleno")}
	var m tea.Model = withSessionLister(newHeadlessRoot(), sl)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	m = typeAndEnter(m, "/resume")

	root := m.(Root)
	if root.mode != ModeResume {
		last := root.transcript[len(root.transcript)-1]
		if !containsWarn(last.text, "disco lleno") {
			t.Fatalf("expected the List error surfaced, transcript = %+v", root.transcript)
		}
	}
}

func containsWarn(text, substr string) bool {
	for i := 0; i+len(substr) <= len(text); i++ {
		if text[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// TestResumeMenuEnterEmitsSessionChosen is the menu's own keyboard contract:
// enter on the highlighted row emits sessionChosenMsg with that row's ID,
// never mutates Root directly — the same message-driven rule
// modelChosenMsg already follows for the picker.
func TestResumeMenuEnterEmitsSessionChosen(t *testing.T) {
	root := newHeadlessRoot()
	root.mode = ModeResume
	root.resume = resumeMenu{rows: []SessionSummary{
		{ID: "only-one", Title: "sesión única", UpdatedAt: time.Now()},
	}}

	next, cmd := root.updateResumeMenu(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected a command emitting sessionChosenMsg")
	}
	msg := cmd()
	chosen, ok := msg.(sessionChosenMsg)
	if !ok {
		t.Fatalf("cmd() = %T, want sessionChosenMsg", msg)
	}
	if chosen.ID != "only-one" {
		t.Errorf("chosen.ID = %q, want %q", chosen.ID, "only-one")
	}
	if next.(Root).mode != ModeResume {
		t.Error("emitting the message should not itself change mode — applySessionChosen does that")
	}
}

// TestResumeMenuEscCloses is esc's ordinary "abandon the overlay" behaviour,
// the same as updateConfirm and updatePicker's own esc handling.
func TestResumeMenuEscCloses(t *testing.T) {
	root := newHeadlessRoot()
	root.mode = ModeResume
	root.resume = resumeMenu{rows: []SessionSummary{{ID: "x", Title: "x"}}}

	next, _ := root.updateResumeMenu(tea.KeyPressMsg{Code: tea.KeyEscape})
	if next.(Root).mode != ModeChat {
		t.Errorf("mode after esc = %v, want ModeChat", next.(Root).mode)
	}
}

// TestApplySessionChosenReplacesConversationAndTranscript is §13's own
// two-place-write rule (NewRoot's comment on Options.History) applied to
// the /resume path: choosing a row must update both what the next request
// sends (m.conv) and what the screen shows (m.transcript), or the two end
// up disagreeing from the very next turn.
func TestApplySessionChosenReplacesConversationAndTranscript(t *testing.T) {
	loaded := &convo.Conversation{
		Header:   convo.Header{ID: "s1", Model: "openai/gpt-5"},
		Messages: []convo.Message{convo.User("hola"), convo.Assistant("¡hola!", "openai/gpt-5")},
	}
	sl := &fakeSessionLister{convs: map[string]*convo.Conversation{"s1": loaded}}
	root := withSessionLister(newHeadlessRoot(), sl)
	root.mode = ModeResume
	root.resume = resumeMenu{rows: []SessionSummary{{ID: "s1", Title: "s1"}}}
	// Pre-existing state that must be discarded, not merged.
	root.conv.Add(convo.User("mensaje viejo"))
	root.transcript = append(root.transcript, transcriptEntry{role: "user", text: "mensaje viejo"})

	next, _ := root.applySessionChosen("s1")
	got := next.(Root)

	if got.mode != ModeChat {
		t.Errorf("mode = %v, want ModeChat", got.mode)
	}
	if len(got.conv.Messages) != 2 {
		t.Fatalf("conv.Messages = %d, want 2 (the loaded conversation, not the old one)", len(got.conv.Messages))
	}
	if len(got.transcript) != 2 {
		t.Fatalf("transcript = %d, want 2", len(got.transcript))
	}
	if got.model != "openai/gpt-5" {
		t.Errorf("model = %q, want %q", got.model, "openai/gpt-5")
	}
}

// TestApplySessionChosenReportsLoadFailure is the rare "disappeared between
// listing and choosing" case: Load failing must surface as a notice, not a
// panic or a silent no-op.
func TestApplySessionChosenReportsLoadFailure(t *testing.T) {
	sl := &fakeSessionLister{}
	root := withSessionLister(newHeadlessRoot(), sl)
	root.mode = ModeResume

	next, _ := root.applySessionChosen("gone")
	got := next.(Root)
	if got.mode != ModeChat {
		t.Errorf("mode = %v, want ModeChat", got.mode)
	}
	if len(got.transcript) == 0 {
		t.Fatal("expected a slashNotice reporting the Load failure")
	}
}

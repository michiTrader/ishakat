package tui

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/convo"
)

// fakeRecorder is the three-line test double session.go's own comment
// promises: it records what was appended, in order, and can be told to fail
// on the next call.
type fakeRecorder struct {
	appended []convo.Message
	failNext bool
}

func (f *fakeRecorder) Append(m convo.Message) error {
	if f.failNext {
		f.failNext = false
		return errors.New("disco lleno")
	}
	f.appended = append(f.appended, m)
	return nil
}

func withRecorder(root Root, rec Recorder) Root {
	root.recorder = rec
	return root
}

// TestOptionsRecorderIsWiredIntoRoot is the regression test for the bug
// every other test in this file missed: they all build their Root through
// withRecorder, which assigns the private field directly and never exercises
// NewRoot's own wiring. internal/app.Run only ever has Options — it cannot
// reach the private field — so if NewRoot drops Options.Recorder on the
// floor, every real session silently goes unsaved while every test here
// keeps passing. This is the one test in the file that goes through
// NewRoot(Options{...}) instead of withRecorder, which is the whole point.
func TestOptionsRecorderIsWiredIntoRoot(t *testing.T) {
	rec := &fakeRecorder{}
	root := NewRoot(Options{Recorder: rec})
	if root.recorder == nil {
		t.Fatal("NewRoot did not wire Options.Recorder into Root.recorder — real sessions go unsaved")
	}

	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "hola mundo")

	if len(rec.appended) != 1 {
		t.Fatalf("appended = %d messages via a Root built from NewRoot(Options{Recorder: rec}), want 1", len(rec.appended))
	}
}

// fakeSessionLister is SessionLister's own three-line test double, the same
// shape fakeRecorder above already follows for the write side.
type fakeSessionLister struct {
	rows    []SessionSummary
	convs   map[string]*convo.Conversation
	listErr error
}

func (f *fakeSessionLister) List() ([]SessionSummary, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.rows, nil
}

func (f *fakeSessionLister) Load(id string) (*convo.Conversation, error) {
	if c, ok := f.convs[id]; ok {
		return c, nil
	}
	return nil, errors.New("sesión no encontrada")
}

// TestOptionsSessionListerIsWiredIntoRoot is SessionLister's own regression
// test, the exact mirror of TestOptionsRecorderIsWiredIntoRoot above:
// internal/app.Run only ever has Options, so if NewRoot drops
// Options.SessionLister on the floor, /resume silently has nothing to list
// while every test that builds a Root by assigning the private field
// directly keeps passing regardless.
func TestOptionsSessionListerIsWiredIntoRoot(t *testing.T) {
	sl := &fakeSessionLister{}
	root := NewRoot(Options{SessionLister: sl})
	if root.sessionLister == nil {
		t.Fatal("NewRoot did not wire Options.SessionLister into Root.sessionLister — /resume would have nothing to list")
	}
}

// TestRecorderGetsUserMessageBeforeTheTurnStarts is §10's ordering
// requirement: what the user typed is persisted before the request is sent,
// not after the answer comes back, so killing the process mid-turn never
// loses the question itself.
func TestRecorderGetsUserMessageBeforeTheTurnStarts(t *testing.T) {
	rec := &fakeRecorder{}
	var m tea.Model = withRecorder(newHeadlessRoot(), rec)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	m = typeAndEnter(m, "hola mundo")

	// Still ModeBusy: the answer has not streamed back yet, but the user's
	// own message must already be recorded.
	if len(rec.appended) != 1 {
		t.Fatalf("appended = %d messages right after submit, want 1 (the user's)", len(rec.appended))
	}
	if rec.appended[0].Role != convo.RoleUser {
		t.Errorf("first recorded message role = %q, want %q", rec.appended[0].Role, convo.RoleUser)
	}
	if rec.appended[0].Text() != "hola mundo" {
		t.Errorf("first recorded message text = %q, want %q", rec.appended[0].Text(), "hola mundo")
	}
}

// TestRecorderGetsAssistantMessageOnlyOnceTheTurnFinishes is the streaming
// half of the same rule: nothing is appended while tokens are still
// arriving, only once drainTurn (finishTurn) closes the turn — a JSONL file
// must never grow token by token.
func TestRecorderGetsAssistantMessageOnlyOnceTheTurnFinishes(t *testing.T) {
	rec := &fakeRecorder{}
	var m tea.Model = withRecorder(newHeadlessRoot(), rec)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	m = typeAndEnter(m, "hola mundo")
	if len(rec.appended) != 1 {
		t.Fatalf("appended = %d before the turn finished, want 1 (user only)", len(rec.appended))
	}

	m = drainTurn(m)

	if len(rec.appended) != 2 {
		t.Fatalf("appended = %d after the turn finished, want 2 (user + assistant)", len(rec.appended))
	}
	if rec.appended[1].Role != convo.RoleAssistant {
		t.Errorf("second recorded message role = %q, want %q", rec.appended[1].Role, convo.RoleAssistant)
	}
}

// TestNilRecorderIsSilentlyANoOp is Options.Recorder's own documented
// default: [session] save = false must not require a special code path in
// submit/finishTurn, and must not panic.
func TestNilRecorderIsSilentlyANoOp(t *testing.T) {
	var m tea.Model = newHeadlessRoot() // recorder left nil
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	m = typeAndEnter(m, "hola mundo")
	m = drainTurn(m)

	if m.(Root).sessionWarned {
		t.Error("a nil recorder must never be treated as a failed one")
	}
}

// TestRecorderFailureIsReportedOnceThenSuppressed is the full-disk case:
// every Append after the first failure would also fail, and warning on each
// one would bury the transcript under identical notices.
func TestRecorderFailureIsReportedOnceThenSuppressed(t *testing.T) {
	rec := &fakeRecorder{failNext: true}
	var m tea.Model = withRecorder(newHeadlessRoot(), rec)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	m = typeAndEnter(m, "hola mundo")

	root := m.(Root)
	if !root.sessionWarned {
		t.Fatal("a failing recorder should set sessionWarned")
	}
	if root.sessionErr == nil {
		t.Error("sessionErr should carry the failure")
	}
	firstErr := root.sessionErr

	m = drainTurn(m)
	// The assistant message's Append succeeds (failNext was consumed), so
	// sessionErr must still be the first failure, not overwritten by a
	// second call — there was no second failure to overwrite it with, and
	// the point being tested is that the flag itself only ever flips once.
	root = m.(Root)
	if root.sessionErr != firstErr {
		t.Error("sessionErr changed after a second, successful Append — it should stay the first failure")
	}
}

// fakeMissionRecorder is MissionRecorder's own three-line test double, the
// same shape fakeRecorder above already follows for the other event kind.
type fakeMissionRecorder struct {
	appended []convo.MissionEvent
}

func (f *fakeMissionRecorder) AppendMission(ev convo.MissionEvent) error {
	f.appended = append(f.appended, ev)
	return nil
}

// TestOptionsMissionRecorderIsWiredIntoRoot is MissionRecorder's own
// regression test, mirroring TestOptionsRecorderIsWiredIntoRoot above: it
// goes through NewRoot(Options{...}) rather than assigning the private
// field directly, so a future refactor that drops Options.MissionRecorder
// on the floor fails a test instead of silently going unsaved in every
// real session that resolves a mission.
func TestOptionsMissionRecorderIsWiredIntoRoot(t *testing.T) {
	rec := &fakeMissionRecorder{}
	root := NewRoot(Options{MissionRecorder: rec})
	if root.missionRecorder == nil {
		t.Fatal("NewRoot did not wire Options.MissionRecorder into Root.missionRecorder — real mission resolutions go unsaved")
	}

	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	got, ok := m.(Root)
	if !ok {
		t.Fatal("Update did not return a Root")
	}
	m, _ = got.resolveToolScope(toolScopeAsProposed)
	got, ok = m.(Root)
	if !ok {
		t.Fatal("resolveToolScope did not return a Root")
	}
	_ = got

	if len(rec.appended) != 1 {
		t.Fatalf("appended = %d mission events via a Root built from NewRoot(Options{MissionRecorder: rec}), want 1", len(rec.appended))
	}
}

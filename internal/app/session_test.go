package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/convo"
)

// TestNewSessionRecorderHonoursSaveFalse is [session] save's contract: false
// means no recorder at all, not a recorder that silently discards — the
// caller (app.Run) tells the difference apart from a warning string, and a
// non-nil-but-inert Recorder would make that impossible to observe.
func TestNewSessionRecorderHonoursSaveFalse(t *testing.T) {
	cfg := &config.Config{}
	cfg.Session.Save = false

	rec, warn := NewSessionRecorder(cfg, "openai/gpt-4o", nil)
	if rec != nil {
		t.Error("save = false should return a nil Recorder")
	}
	if warn != "" {
		t.Errorf("save = false is not a failure, should not warn: %q", warn)
	}
}

// TestNewSessionRecorderNilConfigDoesNotSave mirrors headless's own
// nil-safety: NewRoot can be reached with cfg == nil (see app.go's own
// comment on why a bad config is not fatal), and the recorder must degrade
// the same way Engine already does rather than panic.
func TestNewSessionRecorderNilConfigDoesNotSave(t *testing.T) {
	rec, warn := NewSessionRecorder(nil, "openai/gpt-4o", nil)
	if rec != nil || warn != "" {
		t.Errorf("nil cfg: rec = %v, warn = %q, want (nil, \"\")", rec, warn)
	}
}

// TestSessionRecorderCreatesFileOnlyOnFirstAppend is session.go's whole
// reason for lazy creation: at NewRoot time nothing has been typed, so there
// is no title yet — unlike headless.go's openSession, which has the entire
// prompt already on the command line.
func TestSessionRecorderCreatesFileOnlyOnFirstAppend(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{}
	cfg.Session.Save = true
	cfg.Session.Dir = dir

	rec, warn := NewSessionRecorder(cfg, "openai/gpt-4o", nil)
	if warn != "" {
		t.Fatalf("unexpected warning: %q", warn)
	}
	if rec == nil {
		t.Fatal("save = true with a writable dir should return a Recorder")
	}

	if files, _ := filepath.Glob(filepath.Join(dir, "*.jsonl")); len(files) != 0 {
		t.Fatalf("a file was created before the first Append: %v", files)
	}

	if err := rec.Append(convo.User("primera pregunta\nsegunda linea")); err != nil {
		t.Fatalf("Append: %v", err)
	}

	files, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if len(files) != 1 {
		t.Fatalf("session files after the first Append = %d, want 1", len(files))
	}
	raw, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2 (header, user):\n%s", len(lines), raw)
	}
	if !strings.Contains(lines[0], `"type":"header"`) {
		t.Errorf("first line must be the header: %s", lines[0])
	}
	// Same titleFrom rule headless.go uses: first line of the prompt only.
	if !strings.Contains(lines[0], `"title":"primera pregunta"`) {
		t.Errorf("unexpected title: %s", lines[0])
	}
	if !strings.Contains(lines[1], `"role":"user"`) {
		t.Errorf("second line must be the user message: %s", lines[1])
	}

	// A second Append must not create a second file or a second header.
	if err := rec.Append(convo.Assistant("respuesta", "openai/gpt-4o")); err != nil {
		t.Fatalf("second Append: %v", err)
	}
	files, _ = filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if len(files) != 1 {
		t.Fatalf("session files after the second Append = %d, want still 1", len(files))
	}
	raw, _ = os.ReadFile(files[0])
	lines = strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines after the second Append = %d, want 3 (header, user, assistant):\n%s", len(lines), raw)
	}
}

// TestSessionRecorderNeverCreatesAFileIfNothingWasSent is the flip side: a
// user who opens the TUI and closes it without typing anything must not
// leave an empty session file behind — there is nothing to resume.
func TestSessionRecorderNeverCreatesAFileIfNothingWasSent(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{}
	cfg.Session.Save = true
	cfg.Session.Dir = dir

	rec, _ := NewSessionRecorder(cfg, "openai/gpt-4o", nil)
	if rec == nil {
		t.Fatal("expected a non-nil recorder")
	}
	_ = rec // never Append'd to

	if files, _ := filepath.Glob(filepath.Join(dir, "*.jsonl")); len(files) != 0 {
		t.Fatalf("a session file was created without a single Append: %v", files)
	}
}

// TestSessionRecorderRotatesOnKeepLast checks the §5.2 pruning is wired to
// the same point headless.go's own Rotate call sits at: after every Append,
// not only at process exit, since a long TUI session never calls Run twice.
func TestSessionRecorderRotatesOnKeepLast(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{}
	cfg.Session.Save = true
	cfg.Session.Dir = dir
	cfg.Session.KeepLast = 1

	// Two independent sessions, each with one Append, so Rotate(1) has
	// something to prune down to.
	for i := 0; i < 2; i++ {
		rec, warn := NewSessionRecorder(cfg, "openai/gpt-4o", nil)
		if warn != "" {
			t.Fatalf("unexpected warning: %q", warn)
		}
		if err := rec.Append(convo.User("pregunta")); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	files, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if len(files) != 1 {
		t.Fatalf("session files after keep_last=1 across two sessions = %d, want 1", len(files))
	}
}

// TestNewSessionRecorderWarnsWhenTheDirCannotBeOpened is BuildEngine's own
// pattern applied here: a store that fails to open is not fatal to starting
// the TUI, it is a warning the caller decides what to do with.
func TestNewSessionRecorderWarnsWhenTheDirCannotBeOpened(t *testing.T) {
	dir := t.TempDir()
	blocked := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{}
	cfg.Session.Save = true
	// A file in the way of the directory convo.NewStore needs to create.
	cfg.Session.Dir = filepath.Join(blocked, "sessions")

	rec, warn := NewSessionRecorder(cfg, "openai/gpt-4o", nil)
	if rec != nil {
		t.Error("a store that failed to open should not return a usable Recorder")
	}
	if warn == "" {
		t.Error("expected a non-empty warning when the session dir cannot be created")
	}
}

// TestNewSessionListerHonoursSaveFalse mirrors
// TestNewSessionRecorderHonoursSaveFalse for the read side: [session] save
// = false must mean no lister at all, and a nil existing store must not
// override that.
func TestNewSessionListerHonoursSaveFalse(t *testing.T) {
	cfg := &config.Config{}
	cfg.Session.Save = false

	lister, warn := NewSessionLister(cfg, nil)
	if lister != nil {
		t.Error("save = false should return a nil SessionLister")
	}
	if warn != "" {
		t.Errorf("save = false is not a failure, should not warn: %q", warn)
	}
}

// TestNewSessionListerNilConfigDoesNotList mirrors
// TestNewSessionRecorderNilConfigDoesNotSave.
func TestNewSessionListerNilConfigDoesNotList(t *testing.T) {
	lister, warn := NewSessionLister(nil, nil)
	if lister != nil || warn != "" {
		t.Errorf("nil cfg: lister = %v, warn = %q, want (nil, \"\")", lister, warn)
	}
}

// TestNewSessionListerListsAndLoads is the adapter's own end-to-end
// contract: List must report the seeded sessions most-recently-updated
// first (convo.Store.List's own order) with the fields the TUI's menu
// actually draws, and Load must return the full conversation behind
// whichever ID List handed back.
func TestNewSessionListerListsAndLoads(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{}
	cfg.Session.Save = true
	cfg.Session.Dir = dir

	seed, err := convo.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	older, err := seed.New("primera charla", "openai/gpt-4o")
	if err != nil {
		t.Fatal(err)
	}
	if err := seed.Append(older.ID, convo.User("hola")); err != nil {
		t.Fatal(err)
	}
	// List orders by mtime (convo.Store.List's own comment); a filesystem's
	// timestamp resolution is not always finer than the two New calls take
	// to run back-to-back, so the older file's mtime is nudged into the
	// past explicitly rather than trusting wall-clock ordering here.
	if err := os.Chtimes(filepath.Join(dir, older.ID+".jsonl"), time.Now().Add(-time.Hour), time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	newer, err := seed.New("segunda charla", "openai/gpt-4o")
	if err != nil {
		t.Fatal(err)
	}
	if err := seed.Append(newer.ID, convo.User("hola de nuevo")); err != nil {
		t.Fatal(err)
	}

	lister, warn := NewSessionLister(cfg, nil)
	if warn != "" {
		t.Fatalf("unexpected warning: %q", warn)
	}
	if lister == nil {
		t.Fatal("save = true with a writable dir should return a SessionLister")
	}

	rows, err := lister.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	// convo.Store.List orders most-recently-updated first.
	if rows[0].ID != newer.ID {
		t.Errorf("rows[0].ID = %q, want %q (most recent first)", rows[0].ID, newer.ID)
	}
	if rows[0].Title != "segunda charla" {
		t.Errorf("rows[0].Title = %q, want %q", rows[0].Title, "segunda charla")
	}

	conv, err := lister.Load(older.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(conv.Messages) != 1 {
		t.Fatalf("loaded messages = %d, want 1", len(conv.Messages))
	}
}

// TestNewSessionListerReusesAnExistingStore is the reuse contract app.go's
// own comment documents: when a store is already open (--resume,
// resume_last already ran), NewSessionLister must not open a second one —
// existing is used unconditionally, even with save = false, mirroring the
// same-run reuse app.go's recorder wiring already does.
func TestNewSessionListerReusesAnExistingStore(t *testing.T) {
	dir := t.TempDir()
	store, err := convo.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	conv, err := store.New("charla", "openai/gpt-4o")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Append(conv.ID, convo.User("hola")); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{}
	cfg.Session.Save = false // deliberately off — existing must still win

	lister, warn := NewSessionLister(cfg, store)
	if warn != "" {
		t.Fatalf("unexpected warning: %q", warn)
	}
	if lister == nil {
		t.Fatal("an existing store must be reused regardless of [session] save")
	}
	rows, err := lister.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
}

// TestNewSessionListerWarnsWhenTheDirCannotBeOpened mirrors
// TestNewSessionRecorderWarnsWhenTheDirCannotBeOpened for the read side.
func TestNewSessionListerWarnsWhenTheDirCannotBeOpened(t *testing.T) {
	dir := t.TempDir()
	blocked := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{}
	cfg.Session.Save = true
	cfg.Session.Dir = filepath.Join(blocked, "sessions")

	lister, warn := NewSessionLister(cfg, nil)
	if lister != nil {
		t.Error("a store that failed to open should not return a usable SessionLister")
	}
	if warn == "" {
		t.Error("expected a non-empty warning when the session dir cannot be created")
	}
}

// TestResumeSessionNothingToResume covers the ordinary case: neither the
// --resume flag nor resume_last is set, or the flag is set but there is no
// prior session on disk. Neither is a warning — ErrNotFound is the state of
// a brand-new install, not a failure — and both must return a nil
// conversation so app.Run falls through to a fresh session unmodified.
func TestResumeSessionNothingToResume(t *testing.T) {
	t.Run("resume not requested at all", func(t *testing.T) {
		dir := t.TempDir()
		cfg := &config.Config{}
		cfg.Session.Save = true
		cfg.Session.Dir = dir

		conv, store, warn := ResumeSession(cfg, false)
		if conv != nil || store != nil || warn != "" {
			t.Fatalf("got (%v, %v, %q), want (nil, nil, \"\")", conv, store, warn)
		}
	})

	t.Run("--resume with an empty sessions dir", func(t *testing.T) {
		dir := t.TempDir()
		cfg := &config.Config{}
		cfg.Session.Save = true
		cfg.Session.Dir = dir

		conv, store, warn := ResumeSession(cfg, true)
		if conv != nil || store != nil || warn != "" {
			t.Fatalf("got (%v, %v, %q), want (nil, nil, \"\") — ErrNotFound must not warn", conv, store, warn)
		}
	})

	t.Run("session save = false disables resume too", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.Session.Save = false
		cfg.Session.ResumeLast = true

		conv, store, warn := ResumeSession(cfg, true)
		if conv != nil || store != nil || warn != "" {
			t.Fatalf("got (%v, %v, %q), want (nil, nil, \"\")", conv, store, warn)
		}
	})
}

// TestResumeSessionLoadsTheLatestConversation is --resume's main contract:
// given a sessions directory with a prior conversation in it, ResumeSession
// must return that conversation with its messages intact.
func TestResumeSessionLoadsTheLatestConversation(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{}
	cfg.Session.Save = true
	cfg.Session.Dir = dir

	seed, err := convo.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	conv, err := seed.New("charla previa", "openai/gpt-4o")
	if err != nil {
		t.Fatal(err)
	}
	if err := seed.Append(conv.ID, convo.User("hola")); err != nil {
		t.Fatal(err)
	}
	if err := seed.Append(conv.ID, convo.Assistant("hola de vuelta", "openai/gpt-4o")); err != nil {
		t.Fatal(err)
	}

	got, store, warn := ResumeSession(cfg, true)
	if warn != "" {
		t.Fatalf("unexpected warning: %q", warn)
	}
	if store == nil {
		t.Fatal("expected a non-nil store to reuse for the recorder")
	}
	if got == nil {
		t.Fatal("expected a resumed conversation, got nil")
	}
	if got.ID != conv.ID {
		t.Errorf("resumed ID = %q, want %q", got.ID, conv.ID)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("resumed messages = %d, want 2", len(got.Messages))
	}
}

// TestResumeSessionHonoursResumeLastWithoutTheFlag mirrors the flag-driven
// test above but through [session] resume_last, since ResumeSession's
// contract is "either one triggers resume", not "the flag always wins".
func TestResumeSessionHonoursResumeLastWithoutTheFlag(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{}
	cfg.Session.Save = true
	cfg.Session.Dir = dir
	cfg.Session.ResumeLast = true

	seed, err := convo.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	conv, err := seed.New("charla previa", "openai/gpt-4o")
	if err != nil {
		t.Fatal(err)
	}
	if err := seed.Append(conv.ID, convo.User("hola")); err != nil {
		t.Fatal(err)
	}

	got, _, warn := ResumeSession(cfg, false)
	if warn != "" {
		t.Fatalf("unexpected warning: %q", warn)
	}
	if got == nil || got.ID != conv.ID {
		t.Fatalf("resume_last did not resume the seeded conversation: got %v", got)
	}
}

// TestSessionRecorderAppendsToAResumedConversation is sessionRecorder's half
// of --resume: when conv is already set (the caller passed a resumed
// conversation), Append must write to that same file from its very first
// call instead of creating a new one — no second header, no second file.
func TestSessionRecorderAppendsToAResumedConversation(t *testing.T) {
	dir := t.TempDir()
	store, err := convo.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	seeded, err := store.New("charla previa", "openai/gpt-4o")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Append(seeded.ID, convo.User("hola")); err != nil {
		t.Fatal(err)
	}

	rec := &sessionRecorder{store: store, conv: seeded, model: "openai/gpt-4o"}
	if err := rec.Append(convo.Assistant("respuesta", "openai/gpt-4o")); err != nil {
		t.Fatalf("Append: %v", err)
	}

	files, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if len(files) != 1 {
		t.Fatalf("session files = %d, want still 1 (no new conversation created)", len(files))
	}
	raw, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines = %d, want 3 (header, seeded user, new assistant):\n%s", len(lines), raw)
	}
	if strings.Count(string(raw), `"type":"header"`) != 1 {
		t.Errorf("expected exactly one header line, got:\n%s", raw)
	}
}

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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

	rec, warn := NewSessionRecorder(cfg, "openai/gpt-4o")
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
	rec, warn := NewSessionRecorder(nil, "openai/gpt-4o")
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

	rec, warn := NewSessionRecorder(cfg, "openai/gpt-4o")
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

	rec, _ := NewSessionRecorder(cfg, "openai/gpt-4o")
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
		rec, warn := NewSessionRecorder(cfg, "openai/gpt-4o")
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

	rec, warn := NewSessionRecorder(cfg, "openai/gpt-4o")
	if rec != nil {
		t.Error("a store that failed to open should not return a usable Recorder")
	}
	if warn == "" {
		t.Error("expected a non-empty warning when the session dir cannot be created")
	}
}

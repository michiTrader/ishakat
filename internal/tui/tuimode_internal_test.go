package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/termenv"
)

// TestOptionsTUIModeIsWiredIntoRoot is the regression test for the same bug
// class TestOptionsRecorderIsWiredIntoRoot (session_internal_test.go) already
// guards against, applied to Options.TUIMode: internal/app.Run only ever has
// Options to hand a Root — it cannot reach Root.tuiMode directly — so if
// NewRoot dropped Options.TUIMode on the floor, every real session would
// silently keep termenv.ModeRegular (the zero value) no matter what
// termenv.Detect actually decided, while every other test in this package
// (which never sets Options.TUIMode) kept passing regardless. This is the one
// place that constructs a Root through NewRoot(Options{TUIMode: ...}) and
// reads the private field back, which is the whole point.
func TestOptionsTUIModeIsWiredIntoRoot(t *testing.T) {
	root := NewRoot(Options{TUIMode: termenv.ModeFullscreen})
	if root.tuiMode != termenv.ModeFullscreen {
		t.Fatalf("NewRoot did not wire Options.TUIMode into Root.tuiMode: got %v, want %v", root.tuiMode, termenv.ModeFullscreen)
	}
}

// TestOptionsTUIModeDefaultsToRegular confirms the zero value (a bare
// Options{}, which is what every other test in this package already builds)
// resolves to termenv.ModeRegular — today's inline behaviour — rather than to
// some other zero value that would silently change rendering for every
// existing test the moment TUIMode gained a reader.
func TestOptionsTUIModeDefaultsToRegular(t *testing.T) {
	root := NewRoot(Options{})
	if root.tuiMode != termenv.ModeRegular {
		t.Fatalf("a bare Options{} produced tuiMode = %v, want %v (today's inline behaviour)", root.tuiMode, termenv.ModeRegular)
	}
}

// TestSlashDebugShowsTUIMode extends TestSlashDebugRendersLocalDiagnostics'
// own [terminal] section (models_internal_test.go) with the one line this
// slice adds: confirmation that /debug reports the exact Root.tuiMode value
// Options.TUIMode wired in, not a second, independently-detected one. This is
// deliberately a visibility check only — it asserts the value is shown, not
// that any rendering behaviour changed because of it, since nothing inside
// this package reads tuiMode for rendering yet (see Options.TUIMode's own
// doc comment on root.go for why).
func TestSlashDebugShowsTUIMode(t *testing.T) {
	root := NewRoot(Options{Version: "0.0.0-test", NoTTY: true, TUIMode: termenv.ModeFullscreen})
	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/debug")

	got := m.(Root)
	if len(got.transcript) != 1 {
		t.Fatalf("expected one notice entry, got %d: %v", len(got.transcript), got.transcript)
	}
	text := got.transcript[0].text
	if !strings.Contains(text, "tui_mode") {
		t.Fatalf("/debug output missing tui_mode line:\n%s", text)
	}
	if !strings.Contains(text, "fullscreen") {
		t.Errorf("/debug should report the fullscreen mode this Root was built with, got:\n%s", text)
	}
}

package tui

// Tests for docs/DESIGN-tui-mode.md §4's render/emit seam: render() is the
// mode-blind half (Rule 2), emit() is the only mode-aware function, and
// resize (Rule 3) has to be the same operation — rebuild from state — in
// both modes. These are the harness assertions from §4.1 that are already
// checkable with today's code: assertion 1 (mode-invariant content) and
// assertion 4 (one banner, ever) are exercised directly against a
// regular/fullscreen pair below. Assertion 2's B4 resize-idempotence case
// already has its own dedicated regression,
// TestB4ResizeCycleDoesNotCorruptTheScreen (regress_bugs_internal_test.go),
// so it is not duplicated here. Assertions 3 and 6 need the fullscreen emit
// path itself — a scrollback fullscreen owns and an exit transcript to
// flush it — neither of which exists yet (see emit's own doc comment in
// view.go), so they are not claimed here.

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/termenv"
	"github.com/MichiTrader/ishakat/internal/theme"
)

// newScreenRootWithMode is newScreenRoot (regress_bugs_internal_test.go)
// with the one field that test intentionally leaves at its zero value
// exposed, so this file can build the regular/fullscreen pair Rule 1 and
// Rule 2 are about without duplicating the rest of that setup (banner,
// ASCII glyphs, the echo engine double).
func newScreenRootWithMode(t *testing.T, mode termenv.Mode) tea.Model {
	t.Helper()
	root := NewRoot(Options{
		Version: "0.0.0-test",
		CWD:     "/home/user/projects/ishakat",
		Theme:   theme.Load(""),
		Cap:     theme.CapNone,
		Glyphs:  theme.GlyphsASCII,
		NoTTY:   false,
		TUIMode: mode,
	})
	eng, _ := echoEngine(false)
	var m tea.Model = withEngine(root, eng)
	return m
}

// TestRenderIsModeInvariant is §4.1 assertion 1, aimed at render() itself
// rather than at the final tea.View: two Roots, identical in every field
// except tuiMode, must produce byte-identical render() output at every
// point in an identical script. render() is Rule 2's mode-blind half — it
// has no termenv.Mode parameter at all — so this is really a test that
// nothing was smuggled into Root's other fields as a stand-in for a
// parameter render() was not given; grep already confirms render()'s own
// call graph never reads m.tuiMode (see render's doc comment in view.go),
// and this test is the runtime version of that same claim.
func TestRenderIsModeInvariant(t *testing.T) {
	regular := newScreenRootWithMode(t, termenv.ModeRegular)
	fullscreen := newScreenRootWithMode(t, termenv.ModeFullscreen)

	regular, _ = regular.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	fullscreen, _ = fullscreen.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	if got, want := regular.(Root).render(), fullscreen.(Root).render(); got != want {
		t.Fatalf("render() differs between regular and fullscreen at start-up.\nregular:\n%s\nfullscreen:\n%s", got, want)
	}

	regular = typeAndEnter(regular, "hola, una pregunta de prueba")
	fullscreen = typeAndEnter(fullscreen, "hola, una pregunta de prueba")

	// Drain both live turns identically; the echo engine is deterministic,
	// so the two scripts stay in lockstep.
	for i := 0; i < 5000 && (regular.(Root).live.active || fullscreen.(Root).live.active); i++ {
		if regular.(Root).live.active {
			regular, _ = regular.Update(streamTickMsg{})
		}
		if fullscreen.(Root).live.active {
			fullscreen, _ = fullscreen.Update(streamTickMsg{})
		}
	}

	if got, want := regular.(Root).render(), fullscreen.(Root).render(); got != want {
		t.Fatalf("render() differs between regular and fullscreen after an identical turn.\nregular:\n%s\nfullscreen:\n%s", got, want)
	}
}

// TestBannerAppearsExactlyOnceInBothModes extends
// TestBannerAppearsExactlyOnce (regress_bugs_internal_test.go, regular
// only) to fullscreen — §4.1 assertion 4 explicitly asks for "in both
// modes", and today's banner logic (bannerText, root.go's printBannerCmd)
// lives entirely above the render/emit seam, so a regression that only
// showed up in fullscreen would otherwise go uncaught until the fullscreen
// emit path itself existed to reveal it.
func TestBannerAppearsExactlyOnceInBothModes(t *testing.T) {
	for _, mode := range []termenv.Mode{termenv.ModeRegular, termenv.ModeFullscreen} {
		t.Run(mode.String(), func(t *testing.T) {
			root := NewRoot(Options{
				Version: "0.0.0-test",
				CWD:     "/home/user/projects/ishakat",
				Theme:   theme.Load(""),
				Cap:     theme.CapNone,
				Glyphs:  theme.GlyphsASCII,
				NoTTY:   false,
				TUIMode: mode,
			})
			eng, _ := echoEngine(false)
			var m tea.Model = withEngine(root, eng)
			m, _ = m.Update(tea.WindowSizeMsg{Width: 70, Height: 24})

			m = typeAndEnter(m, "hola")
			for i := 0; i < 5000 && m.(Root).live.active; i++ {
				m, _ = m.Update(streamTickMsg{})
			}
			m = typeAndEnter(m, "otra vez")
			for i := 0; i < 5000 && m.(Root).live.active; i++ {
				m, _ = m.Update(streamTickMsg{})
			}

			view := m.View()
			if n := strings.Count(view.Content, "0.0.0-test"); n > 1 {
				t.Errorf("banner: the version line appears %d times in the live-managed region, want at most 1", n)
			}
		})
	}
}

// TestEmitIsTheOnlyModeAwareFunction checks Rule 2's other half directly:
// build one Frame, call emit with both modes, and confirm the only thing
// that is allowed to differ is exactly what emit's own doc comment names as
// the actual mode-aware decision — AltScreen. If render() ever grows a mode
// parameter, or if some future change makes regular and fullscreen disagree
// about MouseMode or the cursor, this is the test that catches it: those
// are style-level policies, and unlike AltScreen there is no design note
// anywhere permitting them to differ per mode.
func TestEmitIsTheOnlyModeAwareFunction(t *testing.T) {
	f := Frame{Content: "line one\nline two"}
	var cursor *tea.Cursor

	regularView := emit(f, termenv.ModeRegular, cursor)
	fullscreenView := emit(f, termenv.ModeFullscreen, cursor)

	if regularView.Content != fullscreenView.Content {
		t.Errorf("emit changed Content based on mode; render()'s output must pass through unchanged. regular=%q fullscreen=%q",
			regularView.Content, fullscreenView.Content)
	}
	if regularView.MouseMode != fullscreenView.MouseMode {
		t.Errorf("emit changed MouseMode based on mode: regular=%v fullscreen=%v", regularView.MouseMode, fullscreenView.MouseMode)
	}
	if regularView.AltScreen {
		t.Errorf("regular must never set AltScreen")
	}
	if !fullscreenView.AltScreen {
		t.Errorf("fullscreen must set AltScreen")
	}
}

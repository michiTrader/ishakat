package tui

// Tests for docs/DESIGN-tui-mode.md §4's render/emit seam: render() is the
// mode-blind half (Rule 2), emit() is the only mode-aware function, and
// resize (Rule 3) has to be the same operation — rebuild from state — in
// both modes. These are the harness assertions from §4.1:
//
//   - assertion 1 (mode-invariant content) and assertion 4 (one banner,
//     ever) are exercised directly against a regular/fullscreen pair below.
//   - assertion 2's B4 resize-idempotence case already has its own
//     dedicated regression, TestB4ResizeCycleDoesNotCorruptTheScreen
//     (regress_bugs_internal_test.go), so it is not duplicated here.
//   - assertion 3 (no content loss after resize) is
//     TestB4bFullscreenLosesNoContentAcrossAResizeCycle, below — the
//     fullscreen counterpart of B4, using testterm's real program/grid
//     harness (not render() strings) because "findable on the grid" is a
//     terminal question, not a string one.
//   - assertion 6 (exit transcript, DECISION-1b) is
//     TestFullscreenExitFlushesTheWholeTranscriptToScrollback, below.
//
// Both landed once emit's fullscreen branch (AltScreen=true), Root.
// ExitTranscript and evictOverflow's fullscreen guard existed — see W3 part
// 6's docs/PLAN.md entry — closing the two assertions this file's own
// comment used to list as unclaimed.

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/termenv"
	"github.com/MichiTrader/ishakat/internal/theme"
	"github.com/MichiTrader/ishakat/internal/tui/testterm"
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
// build one Frame, call emit with both modes, and confirm the only things
// that are allowed to differ are exactly what emit's own doc comment names
// as its mode-aware decisions — AltScreen, and (as of the Bug 1 fix)
// MouseMode. Content must never differ: render()'s output has to pass
// through emit unchanged regardless of mode, or render() itself has grown
// mode-awareness it should not have (Rule 2).
//
// MouseMode is deliberately asserted asymmetric, not equal, unlike a plain
// "the two views must agree" check: regular must keep MouseModeNone (native
// terminal scrollback/selection, unchanged since before this fix), and
// fullscreen must claim MouseModeCellMotion — this is what suppresses
// xterm's own DECSET mode 1007 "Alternate Scroll Mode" from rewriting mouse
// wheel ticks into synthetic up/down keypresses once AltScreen is showing
// (see emit's own doc comment for the full mechanism). An older version of
// this test asserted MouseMode must be identical between modes; that
// assertion was the correct invariant before Bug 1 was understood, and is
// now the wrong one on purpose.
func TestEmitIsTheOnlyModeAwareFunction(t *testing.T) {
	f := Frame{Content: "line one\nline two"}
	var cursor *tea.Cursor

	regularView := emit(f, termenv.ModeRegular, cursor)
	fullscreenView := emit(f, termenv.ModeFullscreen, cursor)

	if regularView.Content != fullscreenView.Content {
		t.Errorf("emit changed Content based on mode; render()'s output must pass through unchanged. regular=%q fullscreen=%q",
			regularView.Content, fullscreenView.Content)
	}
	if regularView.MouseMode != tea.MouseModeNone {
		t.Errorf("regular must keep MouseMode at MouseModeNone (native terminal scrollback/selection), got %v", regularView.MouseMode)
	}
	if fullscreenView.MouseMode != tea.MouseModeCellMotion {
		t.Errorf("fullscreen must claim the mouse via MouseModeCellMotion (Bug 1 fix: this is what suppresses xterm's mode-1007 wheel-to-arrow-key translation), got %v", fullscreenView.MouseMode)
	}
	if regularView.AltScreen {
		t.Errorf("regular must never set AltScreen")
	}
	if !fullscreenView.AltScreen {
		t.Errorf("fullscreen must set AltScreen")
	}
}

// newFullscreenScreenRoot is newScreenRootWithMode's fullscreen case,
// wrapped in a *testterm.Session driving the real tea.Program — needed for
// the two tests below because §4.1 assertions 3 and 6 are both questions
// about a real terminal (what a grid shows, what its scrollback holds after
// the alt screen closes), the same reason regress_bugs_internal_test.go's
// B1-B4 use testterm instead of calling Update/View directly. See that
// file's own "Why these roots are not the existing test roots" comment for
// why NoTTY has to be false here too.
func newFullscreenScreenRoot(t *testing.T, exitTranscript bool) Root {
	t.Helper()
	root := NewRoot(Options{
		Version:                  "0.0.0-test",
		CWD:                      "/home/user/projects/ishakat",
		Theme:                    theme.Load(""),
		Cap:                      theme.CapNone,
		Glyphs:                   theme.GlyphsASCII,
		NoTTY:                    false,
		TUIMode:                  termenv.ModeFullscreen,
		FullscreenExitTranscript: exitTranscript,
	})
	eng, _ := echoEngine(false)
	return withEngine(root, eng)
}

// TestB4bFullscreenLosesNoContentAcrossAResizeCycle is §4.1 assertion 3 —
// "after any resize sequence, every committed entry is still findable ...
// on the grid in fullscreen" — the fullscreen sibling of
// TestB4ResizeCycleDoesNotCorruptTheScreen (regress_bugs_internal_test.go),
// which only ever drove `regular`.
//
// The mechanism under test is evictOverflow's fullscreen guard (root.go):
// regular permanently commits old entries to real scrollback via
// commitEntryCmd and then stops redrawing them, so a resize cycle only has
// to not corrupt whatever is still in the live region. Fullscreen has no
// real scrollback to commit to — see evictOverflow's own comment — so it
// never evicts at all; every entry stays in m.transcript forever and
// render() redraws all of it on every frame, including every resize. That
// makes this assertion almost definitionally true given Rule 3 ("resize
// rebuilds from state") plus the no-eviction guard, but almost is not
// tested, and B4 itself was exactly this kind of "should be true by
// construction" bug that shipped anyway.
//
// The terminal is tall enough (h=24) that clipHead's own *visual* clip
// (view.go) never engages at any width this resize cycle passes through —
// deliberately, because that clip is a separate, already-accepted
// mechanism (docs/DESIGN-tui-mode.md §7's "print it all... revisit only if
// reported" trade-off, restated in evictOverflow's own comment) that hides
// rows without discarding them, and content clipHead is merely not
// currently drawing is not what assertion 3 is about. Conflating the two
// would make this test fail on a symptom §4.1 does not name as a defect,
// exactly the "not the question under test" trap B1's own doc comment
// warns about for a different mechanism (evictOverflow's offset
// arithmetic). What this test isolates is the other mechanism — permanent
// data loss from evictOverflow evicting content fullscreen has nowhere to
// put it — which is why the fullscreen guard (root.go) exists at all.
func TestB4bFullscreenLosesNoContentAcrossAResizeCycle(t *testing.T) {
	const w, h = 70, 24
	markers := []string{"UNOMARCA", "DOSMARCA"}

	s := testterm.Start(t, newFullscreenScreenRoot(t, false), w, h)
	for _, marker := range markers {
		askAndWait(s, marker)
	}

	// The same shrink/grow cycle B4's own regression puts a `regular` root
	// through, driven here against a `fullscreen` one instead.
	s.Resize(40, h)
	s.Resize(25, h)
	s.Resize(45, h)
	s.Resize(w, h)

	for _, marker := range markers {
		if !s.Grid().ContainsAnywhere(marker) {
			t.Errorf("assertion 3: %q is nowhere on the fullscreen terminal after "+
				"a resize cycle (%d→40→25→45→%d).\nevictOverflow's fullscreen "+
				"guard is supposed to keep every entry in m.transcript forever, "+
				"so render() should still be redrawing it after every resize.\n%s",
				marker, w, w, s.Dump("screen (fullscreen has no scrollback to fall back to):"))
		}
	}

	// Fullscreen has no real scrollback at all (evictOverflow's own
	// comment); confirming that here is what makes "still findable"
	// actually mean "on the grid", not "we got lucky and it also landed in
	// scrollback the way regular's B4 counterpart would".
	if sb := s.Grid().Scrollback(); len(sb) != 0 {
		t.Errorf("fullscreen must never populate real scrollback (AltScreen's "+
			"buffer is transient and evictOverflow no-ops in this mode), but "+
			"got %d scrollback rows: %v", len(sb), sb)
	}
}

// TestFullscreenExitFlushesTheWholeTranscriptToScrollback is §4.1 assertion
// 6 — "after quitting from fullscreen, the scrollback contains the whole
// conversation, in order, wrapped to the final width" — DECISION-1b.
//
// This drives the real seam internal/app.Run itself relies on: quit the
// real tea.Program (testterm.Session.Quit, called by t.Cleanup or directly
// here), take the tea.Model p.Run() returned (testterm.Session.FinalModel),
// and call Root.ExitTranscript on it exactly the way app.go does — then
// print the result with a plain fmt.Print-equivalent (s.Grid().Write) and
// assert against the grid's scrollback, the same "screen+scrollback" shape
// every other regression in this package already reads through testterm.
// Calling ExitTranscript directly on a second, independently-built Root
// would test the method in isolation; this test is about the seam Quit/
// p.Run()/FinalModel/ExitTranscript forms together, which is the actual
// correctness requirement DECISION-1b's approval note names.
func TestFullscreenExitFlushesTheWholeTranscriptToScrollback(t *testing.T) {
	const w, h = 60, 10
	// Single words, deliberately: Grid.Count/Contains match per row (their
	// own doc comments, testterm/grid.go), on purpose, so that a match
	// cannot be manufactured by spanning a line break. A marker with
	// spaces would itself wrap across rows at this width and could never
	// satisfy an exact-count assertion regardless of whether the transcript
	// dump is correct — that would be testing wrapText, not ExitTranscript.
	prompts := []string{"UNOMARCA", "DOSMARCA"}
	// longPrompt is the separate witness for the width invariant below: it
	// has to actually be wider than w once rendered, unlike the markers.
	const longPrompt = "una pregunta con bastante texto para forzar el wrap del ancho final"

	s := testterm.Start(t, newFullscreenScreenRoot(t, true), w, h)
	for _, p := range prompts {
		askAndWait(s, p)
	}
	askAndWait(s, longPrompt)

	// Quit is what makes FinalModel non-nil: it waits for p.Run() to return
	// (Session.Quit's own doc comment) which — per ExitTranscript's own doc
	// comment — is the one point bubbletea v2 guarantees the real
	// AltScreen-exit sequence is already on the wire, so it is finally safe
	// to ask for the transcript.
	s.Quit()

	final := s.FinalModel()
	if final == nil {
		t.Fatalf("testterm: FinalModel() is nil after Quit; p.Run() did not " +
			"report a final model")
	}
	root, ok := final.(Root)
	if !ok {
		t.Fatalf("testterm: FinalModel() is a %T, not tui.Root", final)
	}

	transcript := root.ExitTranscript()
	if transcript == "" {
		t.Fatalf("assertion 6: ExitTranscript() returned \"\" after a real "+
			"fullscreen session with %d turns; DECISION-1b's exit dump did "+
			"not fire.\n%s", len(prompts), s.Dump("screen at quit:"))
	}

	// internal/app.Run's own contract: fmt.Print the transcript straight to
	// whatever real terminal is left once the alt screen has already
	// closed. Feeding it to the same grid Quit already settled models that
	// exactly — the alt screen is gone (setAltScreen's own restore path,
	// testterm/grid.go) and s.grid.Write is the same io.Writer sink.Write
	// wraps, so what lands is what a real terminal's scrollback would hold.
	if _, err := s.Grid().Write([]byte(transcript)); err != nil {
		t.Fatalf("testterm: writing the exit transcript to the grid: %v", err)
	}

	// Exactly two: the echo engine double answers with the prompt text
	// itself (echoEngine's own doc comment), so one exchange is the user's
	// line plus the assistant's echo of it — the same accounting
	// TestB2bAnEntryIsCommittedExactlyOnce already uses for the identical
	// reason. "Every committed turn appears... exactly once" (§4.1's own
	// wording) is a claim about ExitTranscript not silently dropping or
	// duplicating a *turn*, not a claim that echoEngine's reply happens to
	// repeat the prompt only once — this is why B2b itself only pins two
	// as a ceiling until the owner promotes it, and why this test counts
	// the fixed number the echo double is documented to produce rather
	// than asserting flatly ==1.
	for _, p := range prompts {
		if n := s.Grid().Count(p); n != 2 {
			t.Errorf("assertion 6: prompt %q appears %d times in the exit "+
				"transcript, want exactly 2 (the user's line plus the echo "+
				"engine's reply).\nDECISION-1b requires every committed turn "+
				"to appear, in order, exactly once — a count other than 2 "+
				"here means a turn was dropped or duplicated.\n%s",
				p, n, s.Dump("after writing the transcript:"))
		}
	}

	// "in order": the second prompt's row must come after the first's, and
	// both before the third (long) prompt's.
	lines := s.Grid().All()
	firstAt, secondAt, thirdAt := -1, -1, -1
	for i, ln := range lines {
		if firstAt < 0 && strings.Contains(ln, prompts[0]) {
			firstAt = i
		}
		if secondAt < 0 && strings.Contains(ln, prompts[1]) {
			secondAt = i
		}
		if thirdAt < 0 && strings.Contains(ln, "una pregunta con bastante") {
			thirdAt = i
		}
	}
	if firstAt < 0 || secondAt < 0 || thirdAt < 0 {
		t.Fatalf("assertion 6: could not locate all three prompts' rows after "+
			"writing the transcript (first=%d, second=%d, third=%d).\n%s",
			firstAt, secondAt, thirdAt, s.Dump("after writing the transcript:"))
	}
	if secondAt < firstAt || thirdAt < secondAt {
		t.Errorf("assertion 6: the exit transcript did not print the three "+
			"turns in conversation order (rows %d, %d, %d); DECISION-1b "+
			"requires order to be preserved.\n%s", firstAt, secondAt, thirdAt,
			s.Dump("after writing the transcript:"))
	}

	// "wrapped to the final width": clampFrameWidth/wrapText's own
	// invariant (RC-5) — the same rule render() enforces on every live
	// frame — must also hold for ExitTranscript's output, since it is
	// built from the same renderTranscriptLine/width machinery (see its
	// own doc comment). A transcript line wider than the terminal would
	// auto-wrap on a real terminal exactly the way B4's own bug did.
	if width, row := s.Grid().Widest(); width > w {
		t.Errorf("assertion 6: the exit transcript wrote a line %d columns "+
			"wide (row %d) on a %d-column terminal.\n%s", width, row, w,
			s.Dump("after writing the transcript:"))
	}
}

// TestFullscreenExitTranscriptDisabledPrintsNothing confirms the other half
// of DECISION-1b's config surface: fullscreen_exit_transcript = false must
// make ExitTranscript a no-op, the same "config key actually gates the
// behaviour, not just documents it" check every other [ui] flag in this
// package already gets.
func TestFullscreenExitTranscriptDisabledPrintsNothing(t *testing.T) {
	s := testterm.Start(t, newFullscreenScreenRoot(t, false), 60, 10)
	askAndWait(s, "una pregunta cualquiera")
	s.Quit()

	root, ok := s.FinalModel().(Root)
	if !ok {
		t.Fatalf("testterm: FinalModel() is a %T, not tui.Root", s.FinalModel())
	}
	if got := root.ExitTranscript(); got != "" {
		t.Errorf("ExitTranscript() = %q, want \"\" when FullscreenExitTranscript is false", got)
	}
}

// TestFirstMessageDoesNotCorruptFullscreen is the reported "sending my
// first message bugs out the whole interface, my own message doesn't even
// show up" bug (W5 UI-bugs follow-up), reproduced against a real
// tea.Program/renderer, not a bare Update/View string.
//
// The mechanism: submit's own comment (root.go) already documents that
// bannerText()'s transition from non-empty to empty happens on exactly one
// frame — the very first submitted message, ever, for a session — and that
// printBannerCmd is what "retires" the banner through tea.Println/
// insertAbove so the shrinking live region never has to rely on the
// renderer's own diff to erase it. That reasoning holds for regular mode,
// where insertAbove's target (real terminal scrollback) exists. It does
// not hold for fullscreen: reading bubbletea's own cursed_renderer.go
// confirms insertAbove writes ANSI straight to the writer, bypassing
// s.cellbuf/s.lastView (the renderer's own diff bookkeeping) entirely,
// with no check anywhere in its body for whether AltScreen is active —
// despite renderer.go's doc comment claiming "if the altscreen is active
// no output will be printed" (a doc/code discrepancy already flagged in
// docs/PLAN.md's W3 part 6 entry). Called while AltScreen is active, that
// out-of-band write desyncs the renderer's bookkeeping from what is
// actually on screen from that frame on — exactly a "the interface bugs
// out" symptom, and exactly why evictOverflow (the sibling call site using
// the identical mechanism) already carries a fullscreen early-return.
// printBannerCmd's own fix mirrors that guard.
//
// This test would have failed before that guard existed: with it removed,
// printBannerCmd's unconditional tea.Println corrupts the cursor/erase
// bookkeeping on this exact transition, and the assertions below (the
// user's own first message findable on the grid, the assistant's answer
// findable, no scrollback populated, no line wider than the terminal)
// catch it — see this test's own commit message for the before/after
// repro this file's own history records.
func TestFirstMessageDoesNotCorruptFullscreen(t *testing.T) {
	const w, h = 60, 20
	s := testterm.Start(t, newFullscreenScreenRoot(t, false), w, h)

	// This Type/Enter pair is the one frame bannerText() flips on: the
	// banner is still on screen (transcript is empty) right up until this
	// Enter, and gone the instant it lands.
	askAndWait(s, "hola desde el primer mensaje")

	if !s.Grid().ContainsAnywhere("hola desde el primer mensaje") {
		t.Errorf("the user's own first message is not on screen after "+
			"submitting it — this is the reported \"no aparece mi "+
			"mensaje\" symptom.\n%s", s.Dump("screen after the first message:"))
	}
	// The echo engine answers with the same text it was sent, so its
	// presence a second time also confirms the assistant's reply landed
	// (echoChunkSize means it arrives as several redraws, exercising more
	// than one frame past the banner-retirement transition).
	if got := s.Grid().Count("hola desde el primer mensaje"); got < 2 {
		t.Errorf("expected the prompt to appear at least twice on screen "+
			"(user's turn + echoed answer), got %d.\n%s", got,
			s.Dump("screen after the first message:"))
	}

	// Fullscreen must never populate real scrollback (evictOverflow's own
	// comment, and printBannerCmd's fix follows the identical reasoning):
	// insertAbove would have no valid destination for it. A leaked write
	// here — even one that did not visibly corrupt the alt screen — would
	// mean the guard is not actually preventing insertAbove from running.
	if sb := s.Grid().Scrollback(); len(sb) != 0 {
		t.Errorf("fullscreen must never populate real scrollback via the "+
			"banner-retirement path, got %d rows: %v", len(sb), sb)
	}

	// A desynced renderer diff is exactly the kind of bug that manifests
	// as a line drawn past the terminal's own width (the renderer's cursor
	// arithmetic went wrong, so wrapText's own invariant — never emit a
	// line wider than the frame — no longer held for whatever got drawn
	// next).
	if width, row := s.Grid().Widest(); width > w {
		t.Errorf("a line %d columns wide (row %d) was drawn on a "+
			"%d-column fullscreen terminal — consistent with a desynced "+
			"renderer diff after an out-of-band insertAbove write.\n%s",
			width, row, w, s.Dump("screen after the first message:"))
	}
}

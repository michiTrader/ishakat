package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/theme"
)

// This is the guarantee the whole repertoire mechanism exists for, and it is
// deliberately end to end rather than per component. The bug was never "one
// function picked a bad character": it was that every render site picked its own
// characters and nothing ever checked the result, so each fix only moved the
// boxes somewhere else on screen. Playing a real turn covers the logo, the
// transcript, the streaming cursor, the thinking animation, the input box and
// the footer in one pass, which means a literal added to any of them later fails
// here instead of on a user's console.
func TestTheWholeViewStaysInsideTheRepertoire(t *testing.T) {
	var m tea.Model = newRootWithGlyphs(theme.GlyphsASCII)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 40})

	// The banner is only on screen before the first turn, so it is checked
	// first and on its own.
	assertASCII(t, "start-up screen", m.View().Content)

	m = typeInto(m, "una pregunta lo bastante larga para ver el streaming")
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	// Mid-stream is when the most decoration is on screen at once: the stream
	// cursor, the animation strip and the live counters exist only while
	// generating, and the animation cycles, so several frames are sampled.
	for i := 0; i < 12 && m.(Root).live.active; i++ {
		m, _ = m.Update(streamTickMsg{})
		m, _ = m.Update(animTickMsg{})
		assertASCII(t, "mid-stream", m.View().Content)
	}

	for i := 0; i < 5000 && m.(Root).live.active; i++ {
		m, _ = m.Update(streamTickMsg{})
	}
	assertASCII(t, "committed transcript", m.View().Content)
}

// The help screen is reached with /help, which the slash-command registry of
// Step 9 will wire up; until then no key opens it, so it is rendered directly
// rather than left uncovered.
func TestHelpScreenStaysInsideTheRepertoire(t *testing.T) {
	m := newRootWithGlyphs(theme.GlyphsASCII)
	m.mode = ModeHelp
	assertASCII(t, "help screen", m.render())
}

// The two headings used to be hand-counted runs of U+2500 and came out
// different lengths, which is visible on screen and impossible to keep right
// when either title is edited. They are padded to one width now, in both
// repertoires.
func TestHelpHeadingsShareOneWidth(t *testing.T) {
	for _, set := range []theme.GlyphSet{theme.GlyphsUnicode, theme.GlyphsASCII} {
		t.Run(set.String(), func(t *testing.T) {
			g := glyphsFor(set)
			out := newRootWithGlyphs(set).renderHelp()

			var widths []int
			for _, line := range strings.Split(out, "\n") {
				if strings.HasPrefix(line, g.rule) {
					widths = append(widths, lipglossWidth(line))
				}
			}
			if len(widths) != 2 {
				t.Fatalf("expected two ruled headings, found %d:\n%s", len(widths), out)
			}
			if widths[0] != widths[1] {
				t.Errorf("the headings measure %v columns; they should be equal", widths)
			}
		})
	}
}

// The animation is a fixed-width strip so the text after it never shifts
// sideways between frames, and that has to hold in both repertoires: an ASCII
// table with a different number of frames would still have to yield the same
// number of columns.
func TestCrushFrameKeepsItsWidthInBothRepertoires(t *testing.T) {
	for _, set := range []theme.GlyphSet{theme.GlyphsUnicode, theme.GlyphsASCII} {
		lay := NewLayout(80, 24, 0, false, false).WithGlyphs(set)
		for offset := 0; offset < 20; offset++ {
			if got := lipglossWidth(CrushFrame(lay, offset)); got != crushWidth {
				t.Errorf("CrushFrame(%s, %d) is %d columns wide, want %d",
					set, offset, got, crushWidth)
			}
		}
	}
}

func assertASCII(t *testing.T, where, view string) {
	t.Helper()
	for _, r := range view {
		if r > 127 {
			t.Fatalf("the ASCII %s draws %q (U+%04X):\n%s", where, r, r, view)
		}
	}
}

// newRootWithGlyphs is newVisibleRoot restricted to a repertoire: it claims a
// TTY so the banner is drawn, because the logo is where the report started.
func newRootWithGlyphs(set theme.GlyphSet) Root {
	root := NewRoot(Options{
		Version: "0.0.0-test",
		CWD:     "~/projects/ishakat",
		Theme:   theme.Load(""),
		Cap:     theme.CapNone,
		Glyphs:  set,
	})
	eng, _ := echoEngine(false)
	return withEngine(root, eng)
}

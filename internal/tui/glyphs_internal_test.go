package tui

import (
	"fmt"
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

// F14 (roadmap W3): the headings used to be padded to a literal 38 columns no
// matter how wide the terminal actually was — correct at the one width
// anyone happened to test it at, and wrong (too narrow on a wide terminal,
// silently clipped by clampFrameWidth on a narrow one) everywhere else. This
// pins the fix: the rule has to track m.lay.ContentWidth() at each of §9.1's
// four breakpoints, not just continue to agree with itself.
func TestHelpHeadingsFollowTheRealWidth(t *testing.T) {
	g := glyphsFor(theme.GlyphsASCII)
	for _, width := range []int{40, 60, 80, 120} {
		t.Run(fmt.Sprintf("%dcols", width), func(t *testing.T) {
			var m tea.Model = newRootWithGlyphs(theme.GlyphsASCII)
			m, _ = m.Update(tea.WindowSizeMsg{Width: width, Height: 30})
			r := m.(Root)
			out := r.renderHelp()

			var widths []int
			for _, line := range strings.Split(out, "\n") {
				if strings.HasPrefix(line, g.rule) {
					widths = append(widths, lipglossWidth(line))
				}
			}
			if len(widths) != 2 {
				t.Fatalf("expected two ruled headings, found %d:\n%s", len(widths), out)
			}
			want := r.lay.ContentWidth()
			for i, got := range widths {
				if got != want {
					t.Errorf("heading %d measures %d columns at a %d-column terminal, want %d (lay.ContentWidth())", i, got, width, want)
				}
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

// F15 (roadmap W5, "prefer a single rotating glyph over the current
// animation"): CrushFrame used to crawl a nine-column wave of shading
// blocks, which read as a loading bar rather than a spinner. This pins the
// fix at the width axis — crushWidth itself, not a hardcoded literal, so a
// future change to the constant does not silently desync this test from
// TestCrushFrameKeepsItsWidthInBothRepertoires above.
func TestCrushFrameIsNowASingleRotatingGlyph(t *testing.T) {
	if crushWidth != 1 {
		t.Fatalf("crushWidth is %d, want 1 (F15: one rotating glyph, not a crawling strip)", crushWidth)
	}
	for _, set := range []theme.GlyphSet{theme.GlyphsUnicode, theme.GlyphsASCII} {
		lay := NewLayout(80, 24, 0, false, false).WithGlyphs(set)
		if got := lipglossWidth(CrushFrame(lay, 0)); got != 1 {
			t.Errorf("CrushFrame(%s, 0) is %d columns wide, want 1", set, got)
		}
	}
}

// The animation has to keep moving frame to frame — a "spinner" whose glyph
// never changes is a static icon, not motion — and it has to eventually
// repeat, since CrushFrame only ever indexes into a small fixed table
// (glyphs.spinner). This pins both properties without pinning the exact
// characters chosen, so retheming the glyph itself later does not require
// touching this test.
func TestCrushFrameActuallyRotates(t *testing.T) {
	for _, set := range []theme.GlyphSet{theme.GlyphsUnicode, theme.GlyphsASCII} {
		t.Run(set.String(), func(t *testing.T) {
			lay := NewLayout(80, 24, 0, false, false).WithGlyphs(set)
			seen := map[string]bool{}
			for offset := 0; offset < 8; offset++ {
				seen[CrushFrame(lay, offset)] = true
			}
			if len(seen) < 2 {
				t.Errorf("CrushFrame(%s, ...) drew only %d distinct frame(s) across 8 offsets; the spinner never moves", set, len(seen))
			}
			if got := CrushFrame(lay, 0); got != CrushFrame(lay, len(lay.glyphs().spinner)) {
				t.Errorf("CrushFrame(%s, 0) = %q but CrushFrame at one full cycle later = %q; the rotation should repeat", set, got, CrushFrame(lay, len(lay.glyphs().spinner)))
			}
		})
	}
}

// The braille glyph the roadmap's own report quoted verbatim ("⠴ Working...")
// is outside WGL4 (the whole U+2800..U+28FF Braille Patterns block is), so
// F15 replaced it with a turning-arrow rotation instead. This is not a check
// on the exact characters — a future retheme is free to change them — it is
// a check that whatever the Unicode table's spinner is, it stays inside the
// same WGL4 promise every other decorative glyph in this table already
// makes (glyphs.go's own header comment), so this repertoire's whole reason
// to exist does not quietly regress one field at a time.
func TestSpinnerGlyphsStayInsideWGL4(t *testing.T) {
	// The WGL4 ranges this project already relies on elsewhere (box drawing,
	// shading blocks, the scrollHint arrows) plus the arrow block F15 draws
	// from — deliberately narrow rather than the full ~650-character list,
	// because the point is "did this field's author reach for something
	// documented", not re-deriving WGL4 from scratch inside a test.
	inWGL4 := func(r rune) bool {
		return r <= 0x00FF || // Latin-1
			(r >= 0x2010 && r <= 0x2027) || // general punctuation
			(r >= 0x2190 && r <= 0x2195) || // the arrows scrollHint already uses
			(r >= 0x2500 && r <= 0x25FF) // box drawing + block elements + geometric (WGL4 subset)
	}
	for _, r := range unicodeGlyphs.spinner {
		if !inWGL4(r) {
			t.Errorf("unicodeGlyphs.spinner has %q (U+%04X), which is outside this test's WGL4 allow-list", r, r)
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

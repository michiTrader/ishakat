package tui

import (
	"fmt"
	"strings"

	"github.com/MichiTrader/ishakat/internal/theme"
)

// GlyphSample is the interface's own character set, printed as a few lines so
// the user can check the program's guess against their own eyes.
//
// It exists because "unicode" or "ascii" on its own is not actionable. The
// report from the first hands-on session was "the logo is illegible, the
// characters look like blocks" — a sentence nobody can act on without knowing
// whether the program chose the wrong repertoire or the font is missing what it
// chose. Printing the actual glyphs answers that in one glance: boxes or
// question marks mean the guess was too generous, a garbled pair of accented
// letters means the console is decoding our UTF-8 as its OEM code page, and a
// clean row of blocks means the guess was right and the problem is elsewhere.
//
// It is built from glyphsFor and Wordmark, the same table and the same function
// the running interface uses, so a sample that looks fine while the interface
// does not is impossible. That is the reason this lives in tui and is exported
// for cmd rather than being a handful of literals inside doctor.
func GlyphSample(set theme.GlyphSet) []string {
	g := glyphsFor(set)
	lay := Layout{Glyphs: set, Breakpoint: BPNormal, Width: 80}

	out := append([]string(nil), Wordmark(lay)...)

	// One line per group, labelled with where it is drawn, because a user who
	// can only read half the sample can then say which half.
	out = append(out,
		"",
		fmt.Sprintf("prompt  %s   marks  %s %s   cursor  %s",
			g.inputPrefix, g.userMark, g.assistantMark, g.streamCursor),
		fmt.Sprintf("footer  %s %s   %s   context  %s%s",
			g.modelMark, g.gitMark, g.dot,
			g.barLead+strings.Repeat(g.barFull, 6), strings.Repeat(g.barEmpty, 4)),
		fmt.Sprintf("rule    %s   scroll  %s   spinner  %s",
			strings.Repeat(g.rule, 8), g.scrollHint, string(g.spinner)),
		fmt.Sprintf("error   %s   fold  %s   clip  %s", g.warnMark, g.foldMark, g.clipMark),
	)
	return out
}

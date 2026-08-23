package tui

import "github.com/MichiTrader/ishakat/internal/theme"

// glyphs is every decorative character the interface draws, in one table per
// repertoire. Having them in a table rather than as literals sprinkled through
// six files is the actual fix for the "logo shows as boxes" report: a literal
// in the middle of a render function is a decision nobody can review, whereas
// this list can be read top to bottom and checked against a font.
//
// The unicode column is restricted to WGL4 (see theme.GlyphsUnicode). Anything
// that used to reach outside it is noted below with what it was, because the
// temptation to put "◆" back is real and the reason it left should not have to
// be rediscovered.
type glyphs struct {
	// inputPrefix is the prompt at the left of the entry box. U+203A is in
	// WGL4's punctuation block, so it stays.
	inputPrefix string

	// userMark and assistantMark head each transcript bubble. The assistant
	// used to be "◆" (U+25C6), which is not in WGL4; "●" (U+25CF) is, and
	// reads as clearly against "▌".
	userMark      string
	assistantMark string

	// streamCursor is the block trailing the text being generated. It used to
	// be "▊" (U+258A, an eighth block — not in WGL4); the full block is.
	streamCursor string

	// modelMark and gitMark label those two footer items. They used to be "◍"
	// (U+25CD) and "⎇" (U+2387), neither of which is in WGL4 and the second of
	// which is missing from most fonts on any platform.
	modelMark string
	gitMark   string

	// barLead, barFull and barEmpty draw the context meter. The lead used to be
	// "▍" (U+258D, another eighth block).
	barLead  string
	barFull  string
	barEmpty string

	// rule is the horizontal line of the help screen, drawn repeated.
	rule string

	// dot is the separator between fields on one line. U+00B7 is Latin-1, which
	// every font has — but on a console decoding our UTF-8 as cp437 it arrives
	// as two wrong characters, which is why ASCII gets its own.
	dot string

	// scrollHint names the keys that scroll, spelled out when the arrows
	// themselves cannot be drawn.
	scrollHint string

	// spinner is the frames of the "thinking" animation, cycled one rune per
	// tick (spinner.go's CrushFrame). It used to be built from quadrant
	// blocks (▚ ▞ ▘ ▝ ▗) — the exact family Consolas is missing — then from
	// nine shading blocks (░ ▒ ▓ █ ▓ ▒ ░ ▒ ▓) crawling across a nine-column
	// strip, which were in WGL4 and in cp437 but, per F15 (roadmap W5), read
	// as a loading bar rather than a spinner. This is the single rotating
	// glyph that replaced it: a turning arrow, which is also in WGL4 and in
	// cp437 (U+2190..U+2195, the same block DetectGlyphsEnv's own arrow
	// checks already rely on).
	spinner []rune

	// warnMark prefixes an error that belongs to a completed turn.
	warnMark string

	// foldMark prefixes a folded code block's one-line summary (codeblock.go,
	// §17 2026-08-18 "code blocks fill the terminal" entry). It used to be
	// "▸" (U+25B8, not in WGL4); this repertoire's own picker.go already
	// found the same triangle missing and settled on "v"/">" for its own
	// collapse/expand toggle — foldMark follows that precedent rather than
	// inventing a third glyph for the same idea.
	foldMark string

	// clipMark prefixes the "N rows above" affordance RC-3's height
	// invariant draws when the live region has to clip something to keep
	// the whole frame inside the terminal (view.go's clipHead). "…" is
	// Latin-1 punctuation, already in asciiFolds as a fallback for prose
	// that reaches foldASCII — but a decorative affordance is not prose
	// that happened to contain the character, it is the character chosen on
	// purpose for this one job, so it gets its own explicit ASCII spelling
	// ("...") rather than depending on the fold ever seeing it.
	clipMark string
}

var unicodeGlyphs = glyphs{
	inputPrefix:   "›",
	userMark:      "▌",
	assistantMark: "●",
	streamCursor:  "█",
	modelMark:     "•",
	gitMark:       "▪",
	barLead:       "│",
	barFull:       "▓",
	barEmpty:      "░",
	rule:          "─",
	dot:           "·",
	scrollHint:    "↑↓",
	spinner:       []rune("↑→↓←"),
	warnMark:      "⚠",
	foldMark:      "▸",
	clipMark:      "…",
}

var asciiGlyphs = glyphs{
	inputPrefix:   ">",
	userMark:      "|",
	assistantMark: "*",
	streamCursor:  "_",
	modelMark:     "*",
	gitMark:       "#",
	barLead:       "|",
	barFull:       "#",
	barEmpty:      ".",
	rule:          "-",
	dot:           "-",
	scrollHint:    "up/down",
	spinner:       []rune(`|/-\`),
	warnMark:      "!",
	foldMark:      ">",
	clipMark:      "...",
}

// glyphsFor returns the table for a set. Every field of both tables is filled
// in, so no caller has to handle an empty string.
func glyphsFor(set theme.GlyphSet) glyphs {
	if set.ASCII() {
		return asciiGlyphs
	}
	return unicodeGlyphs
}

// glyphs is the accessor the components use: they already consult Layout for
// every other rendering decision, so they consult it for this one too.
func (l Layout) glyphs() glyphs { return glyphsFor(l.Glyphs) }

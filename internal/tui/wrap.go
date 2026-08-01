package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// wrapText folds text to fit within width columns, breaking on spaces where
// it can and inside a word where it must (a single "word" longer than width
// has nowhere else to go). It is the fix for the second half of the reported
// bug: chat.go used to hand the transcript's rows to Bubble Tea verbatim, and
// Bubble Tea's inline renderer clips an overlong row instead of wrapping it —
// so a message longer than the terminal showed its first row and nothing
// after. ansi.Wrap already does exactly this while keeping ANSI escapes and
// wide runes intact, so wrapText is a thin, defensive wrapper around it
// rather than a reimplementation.
//
// width <= 0 is treated as "no limit": callers that have not yet measured a
// terminal (or are laying out something that is not screen-bound) get the
// text back unchanged instead of a wrap against a nonsensical limit.
func wrapText(text string, width int) string {
	if width <= 0 || text == "" {
		return text
	}
	// ansi.Wrap treats "" as it does any other word and, on an empty input,
	// still returns "" — but it is called once per line below, so an empty
	// line must stay empty rather than gain leftover state from a previous
	// call. Splitting first and wrapping each line keeps a blank line blank
	// and keeps a line that was already deliberately broken (a paragraph
	// break, a hard-wrapped ctrl+j newline) from being merged into the ones
	// around it — wordwrap across a "\n" would treat it as just more
	// whitespace and could reflow two separate lines into one.
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if line == "" {
			continue
		}
		lines[i] = ansi.Wrap(line, width, "")
	}
	return strings.Join(lines, "\n")
}

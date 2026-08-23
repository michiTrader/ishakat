// atmenu.go implements F18's own dropdown (docs/ROADMAP-ux-2026-08-20.md's
// W5): "@" autocompletes a path to reference a file. The roadmap's own note
// that "the path.go/suggest.go machinery is already there" does not hold up
// under inspection — path.go is ShortenPath, a display-truncation helper
// with no notion of completion, and suggest.go is ModeSuggest's tool-
// creation overlay (§19.7) — so this file, and atrun.go next to it, are new
// machinery, built as the direct structural sibling of slashmenu.go: the
// state that decides which directory entries currently match what is being
// typed, and the rendering of the box drawn above the input, exactly the
// same way slashMenu decides which commands match and renderSlashMenu draws
// them. Key handling lives in atrun.go, mirroring slashrun.go/slashmenu.go's
// own split for the identical reason: "everything the dropdown needs to
// know about itself is kept here so that file only has to call in, not
// reimplement it".
//
// The one real difference from slashMenu is deliberate and load-bearing: a
// slash command occupies the *entire* input (slash.IsCommand checks the
// whole text, slashMenuFor closes the moment a space appears), but an "@"
// reference can sit anywhere inside an otherwise ordinary chat message
// ("see @src/tui/root.go for details"). This first slice recognises it only
// at the true end of the buffer — the cursor sitting after the very last
// character typed, on the very last line — the same kind of deliberate
// narrowing F4's own first slice (4 of ~50 [ui] keys) already applies
// rather than solving "@ anywhere, including mid-edit of an older line" in
// one pass. currentWordAtEnd (below) is what enforces that boundary; the
// common case it is built for — type some text, "@", a partial path, then
// Tab — never needs anything more.
package tui

import (
	"sort"
	"strings"
	"unicode"

	"charm.land/bubbles/v2/textarea"
	"github.com/charmbracelet/x/ansi"

	"github.com/MichiTrader/ishakat/internal/theme"
)

// atMenuRows mirrors slashMenuRows: five visible rows with scroll, the same
// §9.6 convention applied to this second dropdown rather than inventing a
// different window size for no reason.
const atMenuRows = 5

// PathLister lists the entries of a directory for F18's "@" completion, the
// §6.1 seam this package's own filesystem-free rule forces: internal/tui
// does no disk I/O of its own (confirmed empty by
// `grep -rn '"os"' internal/tui/*.go` — the same absence CatalogRefreshFactory's
// and ReloadFactory's own doc comments already lean on for config/skills),
// so listing a directory has to run on the far side of a function-typed
// seam internal/app implements with a plain os.ReadDir call
// (internal/app/pathlister.go's own NewPathLister).
//
// dir is always relative to the process's working directory: "" means that
// directory itself, "src/tui" means the subdirectory of that name. The
// returned names carry a trailing "/" for directories — the same
// convention a shell's own completion uses — so atMenuFor can tell "worth
// listing further" from "worth completing as a finished reference" without
// a second seam call. nil (directory does not exist, a permission error, a
// nil PathLister — every test in this package and any caller with nothing
// wired) is treated exactly like "no matches", the identical
// nil-means-empty contract theme.Available already establishes for its own
// os.ReadDir call.
type PathLister func(dir string) []string

// atMenu is the "@" dropdown's own state: which directory entries currently
// match what has been typed after the "@", and which one is selected. Like
// slashMenu it has no notion of an engine, a conversation or a terminal —
// Root reads Selected() and decides what completing it means.
type atMenu struct {
	token   string // exactly what was typed after "@", e.g. "src/tui/ro"
	dir     string // token's directory part, "" for the CWD itself
	entries []string
	sel     int
}

// Active reports whether the dropdown has anything to draw.
func (m atMenu) Active() bool { return len(m.entries) > 0 }

// Selected is the entry the current selection points at — a bare name, or a
// name with a trailing "/" for a directory. Callers must check Active
// first, the same deliberate panic-instead-of-guess contract
// slashMenu.Selected already documents.
func (m atMenu) Selected() string { return m.entries[m.sel] }

// moveDown/moveUp cycle the selection, wrapping at either end — identical
// to slashMenu's own move.
func (m atMenu) moveDown() atMenu { return m.move(1) }
func (m atMenu) moveUp() atMenu   { return m.move(-1) }

func (m atMenu) move(delta int) atMenu {
	if len(m.entries) == 0 {
		return m
	}
	n := len(m.entries)
	m.sel = ((m.sel+delta)%n + n) % n
	return m
}

// atMenuFor derives the dropdown state from the word currently at the end
// of the input (currentWordAtEnd, below) and the menu that was open the
// moment before, the same "selection survives one more keystroke of the
// same name" contract slashMenuFor already gives "/".
//
// The zero value (Active() == false) comes back whenever word no longer
// looks like an in-progress "@" reference: it does not start with "@", the
// lister is nil (no seam wired), the directory named by word's own
// directory part does not exist or is not readable, or nothing in it
// matches the typed prefix.
func atMenuFor(word string, lister PathLister, prev atMenu) atMenu {
	if lister == nil || !strings.HasPrefix(word, "@") {
		return atMenu{}
	}
	token := strings.TrimPrefix(word, "@")
	dir, prefix := splitAtToken(token)

	all := lister(dir)
	if len(all) == 0 {
		return atMenu{}
	}
	var entries []string
	for _, e := range all {
		if strings.HasPrefix(e, prefix) {
			entries = append(entries, e)
		}
	}
	if len(entries) == 0 {
		return atMenu{}
	}
	sort.Strings(entries)

	// Selection survives typing one more character of the same token,
	// mirroring slashMenuFor's own "same list, narrower text" contract:
	// preserved by index as long as the directory being listed has not
	// changed (an index into a different directory's entries would point
	// at an unrelated file) and the previous index still fits the
	// narrowed set.
	sel := prev.sel
	if prev.dir != dir || sel < 0 || sel >= len(entries) {
		sel = 0
	}
	return atMenu{token: token, dir: dir, entries: entries, sel: sel}
}

// splitAtToken splits "src/tui/ro" into ("src/tui", "ro"): the directory to
// list and the partial filename to filter its entries by. A token with no
// "/" at all (e.g. "ro", or "" right after a bare "@") lists the CWD itself
// ("") with the whole token as the prefix.
func splitAtToken(token string) (dir, prefix string) {
	i := strings.LastIndex(token, "/")
	if i < 0 {
		return "", token
	}
	return token[:i], token[i+1:]
}

// currentWordAtEnd returns the trailing run of non-space characters in ta's
// value, but only when the cursor sits at the true end of the whole buffer
// — the last row, at the last column. That restriction is this file's own
// package comment's "recognised only at the true end" rule made literal: it
// is what lets applyAtCompletion (atrun.go) simply call ta.SetValue on the
// whole value and trust the cursor lands in the right place afterwards
// (textarea's own SetValue always leaves the cursor at the end of what it
// just inserted), instead of having to splice a replacement into the
// middle of a multi-line draft and re-derive a cursor position by hand.
//
// "" comes back when the cursor is not at that position, or the trailing
// run is empty (the buffer is empty, or ends in whitespace) — both read the
// same way to atMenuFor: nothing here looks like an in-progress reference.
func currentWordAtEnd(ta textarea.Model) string {
	if ta.Line() != ta.LineCount()-1 {
		return ""
	}
	lines := strings.Split(ta.Value(), "\n")
	last := lines[len(lines)-1]
	if ta.Column() != len([]rune(last)) {
		return ""
	}
	idx := strings.LastIndexFunc(last, unicode.IsSpace)
	return last[idx+1:]
}

// renderAtMenu draws the dropdown exactly the way renderSlashMenu draws
// its own: up to atMenuRows entries, the selected one highlighted, boxed
// the same way the input is everywhere except BPMinimo.
func renderAtMenu(lay Layout, st theme.Styles, m atMenu) string {
	if !m.Active() {
		return ""
	}

	width := lay.ContentWidth()
	if lay.ShowBoxedInput() {
		width -= 2
	}
	if width < 1 {
		width = 1
	}

	items, offset := visibleAtRows(m.entries, m.sel, atMenuRows)

	lines := make([]string, len(items))
	for i, e := range items {
		row := ansi.Truncate(e, width, "…")
		if offset+i == m.sel {
			row = st.Accent.Render(row)
		}
		lines[i] = row
	}
	body := strings.Join(lines, "\n")

	if !lay.ShowBoxedInput() {
		return body
	}
	return st.RenderBox(body, lay.ContentWidth())
}

// visibleAtRows is visibleSlashRows's own windowing logic, duplicated
// rather than shared for the same reason applyParam is duplicated across
// the three provider adapters: a five-line function is cheaper to keep in
// sync by inspection than to force two otherwise-unrelated dropdowns
// (commands vs. path entries) through one generic collection type.
func visibleAtRows(items []string, sel, rows int) ([]string, int) {
	if len(items) <= rows {
		return items, 0
	}
	start := sel - rows/2
	if start < 0 {
		start = 0
	}
	if start > len(items)-rows {
		start = len(items) - rows
	}
	return items[start : start+rows], start
}

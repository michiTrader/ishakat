// Package testterm is a terminal that a test can look at.
//
// It exists because of a gap that let four rendering bugs ship green. Every
// existing test in internal/tui drives Update and View directly and inspects
// the string View returns. That answers "what did the model intend to draw",
// which is not the question any of the four bugs were about. B1 and B2 are
// about a frame that is taller than the screen, B3 is about scrollback the
// program never actually cleared, and B4 is about what a *terminal* does to
// previously printed lines when the width changes. None of those are visible in
// a string, because in a string there is no width, no cursor, no scrollback and
// nothing to overflow. §17's 2026-08-18 entry had to diagnose a renderer bug by
// reading upstream Bubble Tea source, for exactly this reason.
//
// So this package models the other side of the pipe: a cell grid that consumes
// the bytes a real tea.Program emits and applies them the way a terminal would.
// Assertions then read the grid, not the intent.
//
// # The one rule that matters
//
// The grid is a *reporter*, never a corrector. It models what a terminal
// actually does, including the parts that are inconvenient — auto-wrap at the
// right margin, scrolling at the bottom, the cursor pinned inside bounds. It is
// tempting, when a line arrives wider than the grid, to wrap it "properly" and
// move on. That would be fatal: B1 and B2 *are* content overflowing its
// bounds, and a grid that tidied up while parsing would show a clean screen for
// the exact input that corrupts a real one. The bug would be invisible again,
// only now with a test suite claiming otherwise.
//
// Everything here therefore errs towards doing the dumb, literal thing.
//
// # Why no new dependency
//
// DESIGN-tui-mode.md §1.2 committed to this: the parser is
// charmbracelet/x/ansi, already a direct dependency, and the program is driven
// through tea.WithInput/tea.WithOutput over ordinary buffers. No pty, therefore
// no CGO, therefore nothing new to install on Termux. This was verified with a
// disposable probe before the design was approved rather than assumed.
package testterm

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Cell is one character position. Style bytes are deliberately not modelled:
// every W0 assertion is about geometry and content — where text landed, whether
// it survived, whether it fit — and carrying SGR state would add a large amount
// of surface for no assertion that needs it. Rune width is honoured, though,
// because a CJK transcript that "fits" by counting runes does not fit on screen.
type Cell struct {
	Rune  rune
	Width int
}

// Grid is a terminal screen plus its scrollback.
//
// Scrollback is a first-class part of the model rather than an afterthought,
// because two of the things W0 must be able to see live there and nowhere else:
// B3's "did /clear actually clear the history, or just paint over it", and
// DECISION-1b's exit transcript, which is by definition the thing that is in
// the scrollback after the program has gone.
type Grid struct {
	W, H int

	cells [][]Cell

	// cursor, 0-based. Kept inside bounds at all times, the way a terminal
	// does: writing past the last column wraps, and past the last row scrolls.
	cx, cy int

	// scrollback holds rows that left the top of the screen, oldest first.
	scrollback []string

	// savedX/savedY back ESC 7 / ESC 8 (DECSC/DECRC), which the Bubble Tea
	// renderer uses around some of its cursor work.
	savedX, savedY int

	// wrapNext models the deferred-wrap ("last column") state that real
	// terminals implement and that naive emulators get wrong. After printing
	// into the final column the cursor stays put and a pending flag is set; the
	// wrap happens only when the *next* printable arrives. Without this, a line
	// exactly as wide as the screen scrolls one row too early and every
	// full-width assertion is off by one.
	wrapNext bool

	// autowrap is DECAWM (CSI ?7h / ?7l). Bubble Tea disables it in some
	// paths, and a grid that wrapped anyway would disagree with the terminal.
	autowrap bool

	// onlcr models the tty output line discipline (termios ONLCR), which
	// translates LF into CRLF on the way to the screen. It is emphatically not a
	// convenience: Bubble Tea *chooses* which of the two conventions to emit, and
	// it decides from whether its input is a real tty. In tea.go:
	//
	//	mapNl := runtime.GOOS != "windows" && p.ttyInput == nil
	//
	// Driven over pipes — the only way to drive it without a pty, which §1.2
	// ruled out for Termux — ttyInput is nil, so the renderer emits a bare LF and
	// relies on the discipline to supply the carriage return. On a real tty
	// MakeRaw clears OPOST, no translation happens, and the renderer emits the CR
	// itself. Both are correct; they are different layers doing the same job.
	//
	// Modelling it here is what lets the parser stay a faithful VT parser, where
	// LF means "down one row" and only CR returns to column 0, while still
	// reproducing the column arithmetic the real terminal performs. Teaching the
	// parser that LF means CRLF instead would have made the grid lie about a
	// distinction B4's assertions depend on.
	onlcr bool

	// altScreen tracks CSI ?1049h/l. The alt screen gets its own cells and,
	// crucially, does not feed the scrollback — which is the whole reason
	// fullscreen loses native scrolling, and something W3's tests must be able
	// to observe rather than take on faith.
	altScreen  bool
	mainCells  [][]Cell
	mainCX     int
	mainCY     int
	mainScroll []string

	parser *ansi.Parser
}

// SetONLCR turns the LF→CRLF output line discipline on or off.
//
// The default is off, i.e. strict VT semantics, because that is the stricter
// reading: a test asserting a column is then asserting against a terminal that
// will not silently forgive a missing CR. Start turns it on for the driven
// program, since that matches the byte convention Bubble Tea selects when its
// input is a pipe rather than a tty.
func (g *Grid) SetONLCR(on bool) { g.onlcr = on }

// New returns a blank w×h grid with a fresh parser wired to it.
func New(w, h int) *Grid {
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	g := &Grid{W: w, H: h, autowrap: true}
	g.cells = blank(w, h)

	p := ansi.NewParser()
	p.SetHandler(ansi.Handler{
		Print:     g.print,
		Execute:   g.execute,
		HandleCsi: g.csi,
		HandleEsc: g.esc,
		// DCS/OSC/PM/APC/SOS carry no geometry. They are consumed and dropped
		// on purpose: an OSC title change must not be mistaken for output, and
		// silently swallowing them is what a terminal does with ones it does
		// not implement.
	})
	g.parser = p
	return g
}

func blank(w, h int) [][]Cell {
	rows := make([][]Cell, h)
	for y := range rows {
		rows[y] = blankRow(w)
	}
	return rows
}

func blankRow(w int) []Cell {
	row := make([]Cell, w)
	for x := range row {
		row[x] = Cell{Rune: ' ', Width: 1}
	}
	return row
}

// Write feeds bytes to the terminal. Grid implements io.Writer so it can be
// handed straight to tea.WithOutput.
func (g *Grid) Write(p []byte) (int, error) {
	g.parser.Parse(p)
	return len(p), nil
}

// Resize changes the terminal size the way a terminal emulator does when a
// window is dragged: the *terminal* does not reflow what was already printed.
//
// This is the honest and load-bearing part. A real xterm, on widening, does not
// re-wrap the lines already on screen — which is precisely why B4 exists at
// all: the application has to redraw, and if it redraws inline without
// accounting for what is already there, the user sees the text twice. An
// emulator that helpfully reflowed here would make B4 untestable and would be
// modelling a terminal nobody has.
//
// Rows are kept top-aligned and truncated or padded at the right and bottom,
// which is what the common emulators do.
func (g *Grid) Resize(w, h int) {
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}

	next := blank(w, h)
	for y := 0; y < h && y < len(g.cells); y++ {
		copy(next[y], g.cells[y][:min(w, len(g.cells[y]))])
	}
	g.cells = next
	g.W, g.H = w, h
	g.cx = min(g.cx, w-1)
	g.cy = min(g.cy, h-1)
	g.wrapNext = false
}

// --- handlers -------------------------------------------------------------

func (g *Grid) print(r rune) {
	w := runeWidth(r)

	if g.wrapNext && g.autowrap {
		g.wrapNext = false
		g.newline()
		g.cx = 0
	}
	// A double-width rune that does not fit in the last column wraps rather
	// than being split, which is what terminals do and what makes a CJK
	// transcript's width assertions meaningful.
	if w == 2 && g.cx == g.W-1 && g.autowrap {
		g.setCell(g.cx, g.cy, Cell{Rune: ' ', Width: 1})
		g.newline()
		g.cx = 0
	}

	g.setCell(g.cx, g.cy, Cell{Rune: r, Width: w})
	// The trailing half of a wide rune is a placeholder, so String() does not
	// emit it twice and width arithmetic stays honest.
	if w == 2 && g.cx+1 < g.W {
		g.setCell(g.cx+1, g.cy, Cell{Rune: 0, Width: 0})
	}

	g.cx += w
	if g.cx >= g.W {
		g.cx = g.W - 1
		// Deferred wrap: stay in the last column until the next printable.
		g.wrapNext = true
	}
}

func (g *Grid) execute(b byte) {
	switch b {
	case '\n':
		g.wrapNext = false
		if g.onlcr {
			g.cx = 0
		}
		g.newline()
	case '\r':
		g.wrapNext = false
		g.cx = 0
	case '\b':
		g.wrapNext = false
		if g.cx > 0 {
			g.cx--
		}
	case '\t':
		g.wrapNext = false
		next := ((g.cx / 8) + 1) * 8
		g.cx = min(next, g.W-1)
	}
}

// newline moves down one row, scrolling if already on the last one. Scrolling
// is where the top row enters the scrollback — on the main screen only, since
// the alternate screen has none. That asymmetry is the mechanical reason
// fullscreen costs native scrolling.
func (g *Grid) newline() {
	if g.cy < g.H-1 {
		g.cy++
		return
	}
	if !g.altScreen {
		g.scrollback = append(g.scrollback, rowString(g.cells[0]))
	}
	copy(g.cells, g.cells[1:])
	g.cells[g.H-1] = blankRow(g.W)
}

func (g *Grid) esc(cmd ansi.Cmd) {
	switch cmd.Final() {
	case '7': // DECSC
		g.savedX, g.savedY = g.cx, g.cy
	case '8': // DECRC
		g.cx, g.cy = min(g.savedX, g.W-1), min(g.savedY, g.H-1)
	case 'M': // RI, reverse index
		if g.cy > 0 {
			g.cy--
		}
	}
}

func (g *Grid) csi(cmd ansi.Cmd, params ansi.Params) {
	arg := func(i, def int) int {
		v, _, ok := params.Param(i, def)
		if !ok || v == 0 {
			// A zero parameter means "default" for every sequence here, and
			// defaults are 1 for movement.
			if def != 0 {
				return def
			}
		}
		return v
	}

	switch cmd.Final() {
	case 'A': // CUU
		g.cy = max(0, g.cy-arg(0, 1))
		g.wrapNext = false
	case 'B': // CUD
		g.cy = min(g.H-1, g.cy+arg(0, 1))
		g.wrapNext = false
	case 'C': // CUF
		g.cx = min(g.W-1, g.cx+arg(0, 1))
		g.wrapNext = false
	case 'D': // CUB
		g.cx = max(0, g.cx-arg(0, 1))
		g.wrapNext = false
	case 'E': // CNL
		g.cy = min(g.H-1, g.cy+arg(0, 1))
		g.cx = 0
		g.wrapNext = false
	case 'F': // CPL
		g.cy = max(0, g.cy-arg(0, 1))
		g.cx = 0
		g.wrapNext = false
	case 'G': // CHA
		g.cx = clamp(arg(0, 1)-1, 0, g.W-1)
		g.wrapNext = false
	case 'H', 'f': // CUP
		g.cy = clamp(arg(0, 1)-1, 0, g.H-1)
		g.cx = clamp(arg(1, 1)-1, 0, g.W-1)
		g.wrapNext = false
	case 'J': // ED
		g.eraseDisplay(arg(0, 0))
	case 'K': // EL
		g.eraseLine(arg(0, 0))
	case 'L': // IL, insert lines
		g.insertLines(arg(0, 1))
	case 'M': // DL, delete lines
		g.deleteLines(arg(0, 1))
	case 'X': // ECH, erase characters
		n := arg(0, 1)
		for i := 0; i < n && g.cx+i < g.W; i++ {
			g.setCell(g.cx+i, g.cy, Cell{Rune: ' ', Width: 1})
		}
	case 'd': // VPA
		g.cy = clamp(arg(0, 1)-1, 0, g.H-1)
		g.wrapNext = false
	case 'h', 'l':
		g.mode(cmd, params, cmd.Final() == 'h')
	}
}

// mode handles the private (CSI ?) modes that change how output is interpreted.
// Only the two that alter geometry are implemented; the rest (bracketed paste,
// key encodings, cursor visibility) are consumed, because they are noise for
// every assertion here and modelling them would invite assertions that depend
// on them.
func (g *Grid) mode(cmd ansi.Cmd, params ansi.Params, set bool) {
	if cmd.Prefix() != '?' {
		return
	}
	for i := 0; ; i++ {
		v, _, ok := params.Param(i, 0)
		if !ok {
			break
		}
		switch v {
		case 7: // DECAWM
			g.autowrap = set
		case 1049, 1047, 47: // alternate screen
			g.setAltScreen(set)
		}
		if i > 8 {
			break
		}
	}
}

// setAltScreen switches buffers. The main screen's cells, cursor and scrollback
// are put aside intact and restored on exit — which is what makes the "quitting
// fullscreen must leave the conversation behind" requirement (DECISION-1b) a
// real, observable property: without an exit transcript the scrollback that
// comes back is the one from *before* the session, and the conversation is
// simply gone.
func (g *Grid) setAltScreen(on bool) {
	if on == g.altScreen {
		return
	}
	if on {
		g.mainCells, g.mainCX, g.mainCY = g.cells, g.cx, g.cy
		g.mainScroll = g.scrollback
		g.cells = blank(g.W, g.H)
		g.cx, g.cy = 0, 0
		g.scrollback = nil
		g.altScreen = true
		return
	}
	g.altScreen = false
	if g.mainCells == nil {
		g.cells = blank(g.W, g.H)
		g.cx, g.cy = 0, 0
		return
	}
	// The main buffer was saved at the old size; if the window changed while
	// the alt screen was up, re-fit it rather than restoring a wrong shape.
	restored := blank(g.W, g.H)
	for y := 0; y < g.H && y < len(g.mainCells); y++ {
		copy(restored[y], g.mainCells[y][:min(g.W, len(g.mainCells[y]))])
	}
	g.cells = restored
	g.cx, g.cy = min(g.mainCX, g.W-1), min(g.mainCY, g.H-1)
	g.scrollback = g.mainScroll
	g.mainCells, g.mainScroll = nil, nil
}

func (g *Grid) eraseDisplay(mode int) {
	switch mode {
	case 0: // cursor to end
		g.eraseLine(0)
		for y := g.cy + 1; y < g.H; y++ {
			g.cells[y] = blankRow(g.W)
		}
	case 1: // start to cursor
		for y := 0; y < g.cy; y++ {
			g.cells[y] = blankRow(g.W)
		}
		g.eraseLine(1)
	case 2: // whole display, scrollback untouched
		g.cells = blank(g.W, g.H)
	case 3:
		// ESC[3J — erase scrollback. This is the sequence B3 is about, and
		// modelling it separately from mode 2 is the entire point: a program
		// that only sends 2J looks identical on screen and leaves every
		// previous line reachable by scrolling up, which is what "/clear does
		// not really clear" means. Only 3J empties this slice.
		g.scrollback = nil
	}
}

func (g *Grid) eraseLine(mode int) {
	row := g.cells[g.cy]
	switch mode {
	case 0:
		for x := g.cx; x < g.W; x++ {
			row[x] = Cell{Rune: ' ', Width: 1}
		}
	case 1:
		for x := 0; x <= g.cx && x < g.W; x++ {
			row[x] = Cell{Rune: ' ', Width: 1}
		}
	case 2:
		g.cells[g.cy] = blankRow(g.W)
	}
}

func (g *Grid) insertLines(n int) {
	for i := 0; i < n; i++ {
		copy(g.cells[g.cy+1:], g.cells[g.cy:])
		g.cells[g.cy] = blankRow(g.W)
	}
}

func (g *Grid) deleteLines(n int) {
	for i := 0; i < n; i++ {
		copy(g.cells[g.cy:], g.cells[g.cy+1:])
		g.cells[g.H-1] = blankRow(g.W)
	}
}

func (g *Grid) setCell(x, y int, c Cell) {
	if x < 0 || y < 0 || y >= len(g.cells) || x >= len(g.cells[y]) {
		return
	}
	g.cells[y][x] = c
}

// --- inspection -----------------------------------------------------------

// Lines returns the visible screen, right-trimmed, one string per row.
func (g *Grid) Lines() []string {
	out := make([]string, g.H)
	for y := range g.cells {
		out[y] = rowString(g.cells[y])
	}
	return out
}

// String is the visible screen as a block of text: what a photograph of the
// terminal would show.
func (g *Grid) String() string { return strings.Join(g.Lines(), "\n") }

// Scrollback returns the rows that have left the top of the screen, oldest
// first. This is where B3 and the exit transcript are decided.
func (g *Grid) Scrollback() []string {
	out := make([]string, len(g.scrollback))
	copy(out, g.scrollback)
	return out
}

// All is scrollback plus screen: everything the user could reach by scrolling
// up. "Is the user's message still there" is a question about All, not about
// the visible screen.
func (g *Grid) All() []string {
	return append(g.Scrollback(), g.Lines()...)
}

// Cursor returns the cursor position, 0-based, column first.
func (g *Grid) Cursor() (x, y int) { return g.cx, g.cy }

// InAltScreen reports whether the alternate screen is currently active.
func (g *Grid) InAltScreen() bool { return g.altScreen }

// Contains reports whether s appears on any visible row. Rows are matched
// individually rather than against the joined screen, so a match cannot be
// manufactured by spanning a line break — a substring that is only present
// because two unrelated rows happen to abut is not "on the screen".
func (g *Grid) Contains(s string) bool { return containsIn(g.Lines(), s) }

// ContainsAnywhere is Contains over scrollback and screen together.
func (g *Grid) ContainsAnywhere(s string) bool { return containsIn(g.All(), s) }

// Count returns how many rows contain s, across scrollback and screen. This is
// the "banner appears exactly once, ever" assertion.
func (g *Grid) Count(s string) int {
	n := 0
	for _, l := range g.All() {
		if strings.Contains(l, s) {
			n++
		}
	}
	return n
}

// Widest returns the display width of the widest visible row, and its index.
// A frame that emits a line wider than the terminal is RC-5, and the symptom is
// wrap-around corruption rather than a clean truncation.
func (g *Grid) Widest() (width, row int) {
	for y, l := range g.Lines() {
		if w := ansi.StringWidth(l); w > width {
			width, row = w, y
		}
	}
	return width, row
}

func containsIn(lines []string, s string) bool {
	for _, l := range lines {
		if strings.Contains(l, s) {
			return true
		}
	}
	return false
}

func rowString(row []Cell) string {
	var b strings.Builder
	for _, c := range row {
		switch {
		case c.Width == 0 && c.Rune == 0:
			// Placeholder for the second half of a wide rune.
			continue
		case c.Rune == 0:
			b.WriteRune(' ')
		default:
			b.WriteRune(c.Rune)
		}
	}
	return strings.TrimRight(b.String(), " ")
}

// runeWidth uses the same measurement the renderer uses, so the grid and the
// program agree on what "fits". Measuring differently here would produce
// failures that are the harness's fault, which is the fastest way to make a
// harness distrusted and then ignored.
func runeWidth(r rune) int {
	if r < 32 {
		return 0
	}
	return ansi.StringWidth(string(r))
}

func clamp(v, lo, hi int) int { return max(lo, min(v, hi)) }

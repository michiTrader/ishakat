package testterm

import (
	"strings"
	"testing"
)

// The grid is the instrument every other W0 assertion is read from, so it is
// tested first and on its own. An instrument nobody has calibrated produces
// failures that get blamed on the code under test, and the second time that
// happens the harness stops being trusted — which is how a repo ends up with a
// test suite it routes around. These tests exist so that when a B1 case fails,
// the grid is not a suspect.

func TestPrintAndCursor(t *testing.T) {
	g := New(10, 3)
	g.Write([]byte("hola"))
	if got := g.Lines()[0]; got != "hola" {
		t.Errorf("row 0 = %q, want %q", got, "hola")
	}
	if x, y := g.Cursor(); x != 4 || y != 0 {
		t.Errorf("cursor = (%d,%d), want (4,0)", x, y)
	}
}

func TestNewlineAndCarriageReturn(t *testing.T) {
	g := New(10, 3)
	g.Write([]byte("uno\r\ndos"))
	lines := g.Lines()
	if lines[0] != "uno" || lines[1] != "dos" {
		t.Errorf("lines = %q, want [uno dos]", lines[:2])
	}
}

// TestBareLineFeedDoesNotReturnToColumnZero pins a distinction that caught the
// author of this file while writing it, which is the best argument for having
// it as a test: LF moves down, it does not move left. Only CR does that.
//
// It matters beyond pedantry. The renderer emits "\r\x1b[J" between rows, and
// several of the sequences W0 has to reason about depend on the cursor column
// surviving a line feed. A grid that quietly did CRLF for every LF would agree
// with a sloppy expectation and disagree with every real terminal, and the
// resulting harness would be confidently wrong in the one direction nobody
// double-checks.
func TestBareLineFeedDoesNotReturnToColumnZero(t *testing.T) {
	g := New(10, 3)
	g.Write([]byte("uno\ndos"))
	if got := g.Lines()[1]; got != "   dos" {
		t.Errorf("row 1 = %q, want %q: LF is a line feed, not a newline", got, "   dos")
	}
}

// TestDeferredWrapAtTheLastColumn is the off-by-one every naive emulator gets
// wrong. A line exactly as wide as the screen must not scroll until something
// else arrives; if it did, every full-width assertion in this suite would be
// one row out and the harness would be inventing overflow that is not there.
func TestDeferredWrapAtTheLastColumn(t *testing.T) {
	g := New(5, 3)
	g.Write([]byte("abcde")) // exactly the width
	if got := g.Lines()[0]; got != "abcde" {
		t.Errorf("row 0 = %q, want %q", got, "abcde")
	}
	if _, y := g.Cursor(); y != 0 {
		t.Errorf("cursor row = %d, want 0: a full line must not wrap until the next printable", y)
	}
	g.Write([]byte("f"))
	if got := g.Lines()[1]; got != "f" {
		t.Errorf("row 1 = %q, want %q — the wrap should happen on the next rune", got, "f")
	}
}

func TestAutoWrapOntoTheNextRow(t *testing.T) {
	g := New(4, 3)
	g.Write([]byte("abcdefg"))
	lines := g.Lines()
	if lines[0] != "abcd" || lines[1] != "efg" {
		t.Errorf("lines = %q, want [abcd efg]", lines[:2])
	}
}

// TestScrollFeedsScrollback is the mechanism B2 is detected with: content that
// leaves the top of the screen must still be findable, because "the user's
// message scrolled away" and "the user's message was destroyed" are different
// bugs and only one of them is B2.
func TestScrollFeedsScrollback(t *testing.T) {
	g := New(6, 2)
	g.Write([]byte("uno\r\ndos\r\ntres"))

	if sb := g.Scrollback(); len(sb) != 1 || sb[0] != "uno" {
		t.Errorf("scrollback = %q, want [uno]", sb)
	}
	if !g.ContainsAnywhere("uno") {
		t.Error("uno must still be reachable after scrolling off the top")
	}
	if g.Contains("uno") {
		t.Error("uno is no longer on the visible screen and Contains must say so")
	}
}

// TestEraseDisplayModesAreDistinct is B3 in miniature, and the reason mode 2
// and mode 3 are modelled separately. A program that sends only ESC[2J looks
// identical on screen while leaving every previous line one scroll away. If the
// grid collapsed the two, "/clear does not really clear" would be untestable.
func TestEraseDisplayModesAreDistinct(t *testing.T) {
	newFull := func() *Grid {
		g := New(6, 2)
		g.Write([]byte("uno\r\ndos\r\ntres"))
		return g
	}

	only2J := newFull()
	only2J.Write([]byte("\x1b[2J"))
	if only2J.String() != strings.Repeat("\n", only2J.H-1) {
		t.Errorf("after 2J the screen = %q, want blank", only2J.String())
	}
	if len(only2J.Scrollback()) == 0 {
		t.Error("2J must not touch the scrollback: that is exactly the bug B3 describes")
	}

	with3J := newFull()
	with3J.Write([]byte("\x1b[2J\x1b[3J"))
	if len(with3J.Scrollback()) != 0 {
		t.Errorf("after 3J scrollback = %q, want empty", with3J.Scrollback())
	}
}

func TestCursorAddressing(t *testing.T) {
	g := New(10, 4)
	g.Write([]byte("\x1b[3;5Hx"))
	if got := g.Lines()[2]; got != "    x" {
		t.Errorf("row 2 = %q, want 4 spaces then x", got)
	}

	// Out-of-range addressing is clamped, not ignored and not panicking: a
	// renderer that asks for row 99 of a 4-row terminal gets row 4, which is
	// what a terminal does.
	g.Write([]byte("\x1b[99;99Hy"))
	if x, y := g.Cursor(); y != g.H-1 || x < 0 || x >= g.W {
		t.Errorf("cursor = (%d,%d) after out-of-range CUP, want clamped inside %dx%d", x, y, g.W, g.H)
	}
}

func TestRelativeCursorMovement(t *testing.T) {
	// The renderer moves relatively (\r, ESC[A, ESC[B) far more than it
	// addresses absolutely, so these paths carry most of the real traffic.
	g := New(10, 4)
	g.Write([]byte("a\r\nb\r\nc"))
	g.Write([]byte("\x1b[2A\x1b[0GX"))
	if got := g.Lines()[0]; got != "X" {
		t.Errorf("row 0 = %q, want X: two rows up and to column 0", got)
	}
}

func TestEraseLineFromCursor(t *testing.T) {
	g := New(10, 2)
	g.Write([]byte("abcdefgh"))
	g.Write([]byte("\r\x1b[3C\x1b[K"))
	if got := g.Lines()[0]; got != "abc" {
		t.Errorf("row 0 = %q, want abc", got)
	}
}

// TestResizeDoesNotReflow pins the honest behaviour, and it is the assertion
// this whole file exists to protect. A real terminal does not re-wrap lines that
// are already on screen when the window is dragged — that is *why* the
// application has to redraw, and why redrawing inline without accounting for
// what is already there shows the text twice. That is B4.
//
// A helpful grid that reflowed here would make B4 pass while the bug remained,
// which is worse than having no harness at all.
func TestResizeDoesNotReflow(t *testing.T) {
	g := New(6, 3)
	g.Write([]byte("abcdef"))
	g.Resize(3, 3)

	if got := g.Lines()[0]; got != "abc" {
		t.Errorf("row 0 = %q, want abc: narrowing truncates, it does not reflow", got)
	}
	if g.Contains("def") {
		t.Error("the terminal must not have re-wrapped 'def' onto row 1 by itself; only the application redrawing can put it there")
	}
}

func TestResizeKeepsCursorInBounds(t *testing.T) {
	g := New(20, 6)
	g.Write([]byte("\x1b[6;20Hx"))
	g.Resize(5, 2)
	x, y := g.Cursor()
	if x >= 5 || y >= 2 || x < 0 || y < 0 {
		t.Errorf("cursor = (%d,%d) after shrink, want inside 5x2", x, y)
	}
}

// TestAltScreenIsIsolatedAndHasNoScrollback models the mechanical reason
// fullscreen costs native scrolling, and it is what W3's exit-transcript test
// will stand on: without an explicit dump, leaving the alt screen brings back
// the scrollback from *before* the session and the conversation is simply gone.
// Note on how this test is built, because the first version of it was subtly
// worthless. It originally set up a main screen that never scrolled, so its
// scrollback was empty before entering the alternate screen — which meant the
// "alt screen has no scrollback" assertion passed no matter what the code did.
// A mutation that made the alt screen inherit the main scrollback outright did
// not fail it. The setup below scrolls the main screen *first*, so there is a
// non-empty scrollback to wrongly inherit, and the assertion has something to
// actually catch.
func TestAltScreenIsIsolatedAndHasNoScrollback(t *testing.T) {
	g := New(6, 2)
	// Scroll the main screen so it has real scrollback to be leaked.
	g.Write([]byte("antes\r\nviejo\r\nahora"))
	if len(g.Scrollback()) == 0 {
		t.Fatal("setup failed: the main screen must have scrollback for this test to mean anything")
	}

	g.Write([]byte("\x1b[?1049h"))
	if !g.InAltScreen() {
		t.Fatal("CSI ?1049h did not enter the alternate screen")
	}
	if g.Contains("ahora") {
		t.Error("the alternate screen must start blank, not inherit the main screen")
	}
	if len(g.Scrollback()) != 0 {
		t.Errorf("alt screen inherited the main scrollback %q: it must start with none", g.Scrollback())
	}

	// And scrolling inside it must not produce any.
	g.Write([]byte("uno\r\ndos\r\ntres\r\ncuatro"))
	if len(g.Scrollback()) != 0 {
		t.Errorf("alt-screen scrollback = %q, want none: this is why fullscreen loses native scrolling", g.Scrollback())
	}

	g.Write([]byte("\x1b[?1049l"))
	if g.InAltScreen() {
		t.Fatal("CSI ?1049l did not leave the alternate screen")
	}
	if !g.ContainsAnywhere("antes") {
		t.Error("leaving the alternate screen must restore the main screen and its scrollback")
	}
	if g.ContainsAnywhere("cuatro") {
		t.Error("alt-screen content leaked into the main screen; a real terminal discards it, which is precisely why DECISION-1b needs an explicit exit transcript")
	}
}

func TestAutoWrapCanBeDisabled(t *testing.T) {
	g := New(4, 2)
	g.Write([]byte("\x1b[?7l"))
	g.Write([]byte("abcdefgh"))
	if got := g.Lines()[1]; got != "" {
		t.Errorf("row 1 = %q, want empty with DECAWM off", got)
	}
}

func TestWideRunesCountAsTwoColumns(t *testing.T) {
	g := New(6, 2)
	g.Write([]byte("日本語"))
	if got := g.Lines()[0]; got != "日本語" {
		t.Errorf("row 0 = %q, want 日本語", got)
	}
	if x, _ := g.Cursor(); x != 5 {
		// Three double-width runes fill columns 0..5, so the cursor is at the
		// last column with a deferred wrap pending.
		t.Errorf("cursor x = %d, want 5 after three double-width runes on a 6-column grid", x)
	}
}

// TestWideRuneWrapsRatherThanSplitting: half a CJK glyph in the last column is
// not something a terminal produces, and a grid that allowed it would report
// widths that no real screen would show.
func TestWideRuneWrapsRatherThanSplitting(t *testing.T) {
	g := New(3, 2)
	g.Write([]byte("ab日"))
	if got := g.Lines()[0]; got != "ab" {
		t.Errorf("row 0 = %q, want ab", got)
	}
	if got := g.Lines()[1]; got != "日" {
		t.Errorf("row 1 = %q, want 日", got)
	}
}

func TestWidestMeasuresDisplayWidth(t *testing.T) {
	g := New(10, 3)
	g.Write([]byte("ab\r\n日本語\r\nx"))
	w, row := g.Widest()
	if w != 6 || row != 1 {
		t.Errorf("Widest() = (%d,%d), want (6,1): three double-width runes are six columns", w, row)
	}
}

// TestCountSpansScrollback is the "one banner, ever" instrument. Counting only
// the visible screen would report 1 for a banner printed six times, since five
// of them would have scrolled away.
func TestCountSpansScrollback(t *testing.T) {
	g := New(8, 2)
	g.Write([]byte("banner\r\nx\r\nbanner\r\ny\r\nbanner\r\n"))
	if n := g.Count("banner"); n != 3 {
		t.Errorf("Count = %d, want 3: counting must include scrollback or a repeated banner hides itself", n)
	}
}

// TestContainsDoesNotMatchAcrossRows: a substring that only exists because two
// unrelated rows abut is not on the screen, and matching against the joined
// block would produce exactly that false positive.
func TestContainsDoesNotMatchAcrossRows(t *testing.T) {
	g := New(4, 2)
	g.Write([]byte("ab\r\ncd"))
	if g.Contains("abcd") {
		t.Error(`Contains("abcd") matched across a line break`)
	}
}

func TestStyleSequencesDoNotOccupyCells(t *testing.T) {
	// SGR is parsed and dropped; if it landed on the grid every content
	// assertion would be comparing escape codes.
	g := New(10, 2)
	g.Write([]byte("\x1b[31mrojo\x1b[0m"))
	if got := g.Lines()[0]; got != "rojo" {
		t.Errorf("row 0 = %q, want rojo with no escape bytes", got)
	}
}

func TestOSCAndDCSAreConsumed(t *testing.T) {
	g := New(10, 2)
	g.Write([]byte("\x1b]0;título\x07ok"))
	if got := g.Lines()[0]; got != "ok" {
		t.Errorf("row 0 = %q, want ok: an OSC title must not become output", got)
	}
}

func TestSaveAndRestoreCursor(t *testing.T) {
	g := New(10, 3)
	g.Write([]byte("\x1b[2;3H\x1b7"))
	g.Write([]byte("\x1b[1;1Hx"))
	g.Write([]byte("\x1b8y"))
	if got := g.Lines()[1]; got != "  y" {
		t.Errorf("row 1 = %q, want two spaces then y", got)
	}
}

func TestInsertAndDeleteLines(t *testing.T) {
	g := New(6, 4)
	g.Write([]byte("uno\r\ndos\r\ntres"))
	g.Write([]byte("\x1b[2;1H\x1b[L"))
	if lines := g.Lines(); lines[1] != "" || lines[2] != "dos" {
		t.Errorf("after IL lines = %q, want dos pushed down", lines)
	}

	g2 := New(6, 4)
	g2.Write([]byte("uno\r\ndos\r\ntres"))
	g2.Write([]byte("\x1b[2;1H\x1b[M"))
	if lines := g2.Lines(); lines[1] != "tres" {
		t.Errorf("after DL lines = %q, want tres pulled up", lines)
	}
}

func TestTabStops(t *testing.T) {
	g := New(20, 2)
	g.Write([]byte("a\tb"))
	if got := g.Lines()[0]; got != "a       b" {
		t.Errorf("row 0 = %q, want a then a tab stop then b", got)
	}
}

func TestBackspace(t *testing.T) {
	g := New(10, 2)
	g.Write([]byte("abc\b\bX"))
	if got := g.Lines()[0]; got != "aXc" {
		t.Errorf("row 0 = %q, want aXc", got)
	}
}

// TestGridNeverPanicsOnHostileInput: the harness will be fed whatever the
// renderer emits, including sequences nobody anticipated. A panicking grid turns
// an interesting rendering bug into an unreadable stack trace.
func TestGridNeverPanicsOnHostileInput(t *testing.T) {
	inputs := []string{
		"\x1b[999999;999999H",
		"\x1b[-1;-1H",
		"\x1b[0;0H",
		"\x1b[",
		"\x1b",
		"\x1b[?",
		"\x1b[99J",
		"\x1b[99K",
		"\x1b[99999L",
		"\x1b[99999M",
		"\x1b[99999X",
		"\x1b8",
		"\x1bM",
		strings.Repeat("\x1b[A", 100),
		strings.Repeat("x", 10000),
		"\x00\x01\x02\x7f",
	}
	for _, in := range inputs {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("panic on %q: %v", in, r)
				}
			}()
			g := New(5, 3)
			g.Write([]byte(in))
			_ = g.String()
			_ = g.All()
			g.Resize(1, 1)
			_ = g.String()
		}()
	}
}

func TestResizeToOneByOne(t *testing.T) {
	g := New(10, 5)
	g.Write([]byte("hola\r\nmundo"))
	g.Resize(1, 1)
	if x, y := g.Cursor(); x != 0 || y != 0 {
		t.Errorf("cursor = (%d,%d), want (0,0)", x, y)
	}
	if len(g.Lines()) != 1 {
		t.Errorf("Lines() = %d rows, want 1", len(g.Lines()))
	}
}

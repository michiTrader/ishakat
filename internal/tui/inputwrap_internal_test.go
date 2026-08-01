package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// The reported symptom, typed by hand into the bug report: hold a key down until
// the input box grows, and the box comes out as alternating rows of text and
// blank rows, with the text starting at column zero instead of under the prompt,
// only the very last row keeping its two-space indent, and the terminal cursor
// sitting a row or two above the line being typed. Pressing ctrl+j (an explicit
// newline) instead of letting the text soft-wrap made all of it go away — which
// is the clue: the damage was done to *wrapped* rows only.
//
// The cause was one number. theme.Styles.RenderBox subtracted the two borders
// from a width lipgloss v2 already counts the borders in, so the box asked
// lipgloss to fit an 86-column widget into 84 columns and lipgloss word-wrapped
// it: a full row of one repeated letter is a single long word, it did not fit
// after the two-column prompt, so the whole word moved down a row — leaving the
// prompt row blank and the text row unindented, every other row, for as many
// rows as the widget had. The cursor came from textarea.Cursor(), which reports
// the position in the widget as the widget laid it out, i.e. before that
// re-wrap.
//
// These tests assert properties rather than pixels: the frame stays inside the
// terminal, the box reproduces the widget's own rows verbatim instead of
// re-flowing them, and the cursor lands on the cell where the next character
// will appear.
func TestTypingPastTheEdgeKeepsTheBoxAndTheCursorAligned(t *testing.T) {
	for _, width := range []int{120, 88, 60, 40, 32} {
		t.Run(fmt.Sprintf("%dcols", width), func(t *testing.T) {
			var m tea.Model = newVisibleRoot()
			m, _ = m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
			// "z" is the filler because it appears nowhere else in the frame:
			// the banner, the footer and the hint all contain an "s".
			// 400 runes fill the widget's MaxHeight at every width tested, so
			// the growing-box path is the one under test.
			m = typeInto(m, strings.Repeat("z", 400))

			assertInputBoxMatchesTheWidget(t, m, width)
			assertCursorIsOneCellPast(t, m, "z")
		})
	}
}

// TestSoftWrapMatchesAnExplicitNewline is the other half of the report: the
// user's workaround was to press ctrl+j so the text never had to soft-wrap.
// Whatever the box does to a hard-wrapped line it must also do to a
// soft-wrapped one, so the workaround stops being one.
func TestSoftWrapMatchesAnExplicitNewline(t *testing.T) {
	const width = 60

	var soft tea.Model = newVisibleRoot()
	soft, _ = soft.Update(tea.WindowSizeMsg{Width: width, Height: 24})
	// One row of text plus a bit, so the widget has to wrap once.
	inner := soft.(Root).input.Width()
	soft = typeInto(soft, strings.Repeat("z", inner+10))

	var hard tea.Model = newVisibleRoot()
	hard, _ = hard.Update(tea.WindowSizeMsg{Width: width, Height: 24})
	hard = typeInto(hard, strings.Repeat("z", inner-1))
	hard, _ = hard.Update(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl})
	hard = typeInto(hard, strings.Repeat("z", 10))

	for name, m := range map[string]tea.Model{"soft-wrapped": soft, "hard-wrapped": hard} {
		t.Run(name, func(t *testing.T) {
			assertInputBoxMatchesTheWidget(t, m, width)
			assertCursorIsOneCellPast(t, m, "z")
		})
	}
}

// assertInputBoxMatchesTheWidget is the invariant the width bug broke: the box
// draws the textarea's rows as the textarea laid them out. Anything that
// re-wraps, pads or drops one of them shows up here as a row that does not
// match, and the cursor — which is reported in the widget's coordinates — is
// wrong by exactly as much.
func assertInputBoxMatchesTheWidget(t *testing.T, m tea.Model, width int) {
	t.Helper()
	r := m.(Root)
	content := m.View().Content
	lines := strings.Split(content, "\n")

	for i, line := range lines {
		if got := lipgloss.Width(line); got > width {
			t.Fatalf("row %d is %d columns wide in a %d-column terminal: %q",
				i, got, width, stripANSI(line))
		}
	}

	widget := strings.Split(strings.TrimRight(r.input.View(), "\n"), "\n")
	boxRows := len(widget)
	left, right := "", ""
	if r.lay.ShowBoxedInput() {
		boxRows += 2 // top and bottom border
		left, right = "│", "│"
		if r.lay.ASCII() {
			left, right = "|", "|"
		}
	}
	footerRows := len(strings.Split(RenderFooter(r.lay, r.footerState(), r.footerItems), "\n"))

	first := len(lines) - footerRows - boxRows
	if r.lay.ShowBoxedInput() {
		first++ // the first row of the widget sits under the top border
	}
	if first < 0 || first+len(widget) > len(lines) {
		t.Fatalf("a %d-row box and a %d-row footer do not fit in a %d-row frame:\n%s",
			boxRows, footerRows, len(lines), frameDump(lines))
	}
	for i, want := range widget {
		got := stripANSI(lines[first+i])
		if got != left+stripANSI(want)+right {
			t.Fatalf("row %d of the box is\n  %q\nbut row %d of the widget is\n  %q\nframe:\n%s",
				first+i, got, i, stripANSI(want), frameDump(lines))
		}
	}
}

// assertCursorIsOneCellPast checks the only cursor property the user can see:
// it sits on the cell where the next character will land, i.e. immediately after
// the last one typed.
func assertCursorIsOneCellPast(t *testing.T, m tea.Model, filler string) {
	t.Helper()
	v := m.View()
	if v.Cursor == nil {
		t.Fatal("ModeChat must expose a terminal cursor")
	}
	lines := strings.Split(v.Content, "\n")
	last := -1
	for i, line := range lines {
		if strings.Contains(stripANSI(line), filler) {
			last = i
		}
	}
	if last < 0 {
		t.Fatalf("no row of the frame holds the typed text:\n%s", frameDump(lines))
	}
	if v.Cursor.Position.Y != last {
		t.Fatalf("cursor is on row %d; the typed text ends on row %d:\n%s",
			v.Cursor.Position.Y, last, frameDump(lines))
	}
	row := stripANSI(lines[last])
	want := lipgloss.Width(row[:strings.LastIndex(row, filler)+1])
	if got := v.Cursor.Position.X; got != want {
		t.Errorf("cursor column = %d, want %d (row %q)", got, want, row)
	}
}

func frameDump(lines []string) string {
	var b strings.Builder
	for i, line := range lines {
		fmt.Fprintf(&b, "%3d |%s|\n", i, stripANSI(line))
	}
	return b.String()
}

// stripANSI drops the escape sequences the styles add, so a test can talk about
// columns and characters instead of bytes.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			i++
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// slashmenu.go implements the slash-command autocomplete dropdown of §9.6:
// the state that decides which commands currently match, and the rendering
// of the box drawn above the input. Key handling lives in root.go next to
// the rest of ModeChat's dispatch, but everything the dropdown needs to know
// about itself is kept here so that file only has to call in, not
// reimplement it inline.
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/MichiTrader/ishakat/internal/slash"
	"github.com/MichiTrader/ishakat/internal/theme"
)

// slashMenuRows is how many rows the dropdown shows before it scrolls (§9.6:
// "cinco filas visibles con scroll").
const slashMenuRows = 5

// slashMenu is the dropdown's own state: which commands currently match what
// has been typed, and which one is selected. It has no notion of an engine,
// a conversation or a terminal — Root reads Selected() and decides what
// running it means.
type slashMenu struct {
	items []slash.Command
	sel   int
}

// slashMenuFor derives the dropdown state from the current input text and
// the menu that was open the moment before (so a selection survives typing
// one more character of the same command name, rather than resetting to the
// top of the list on every keystroke).
//
// The zero value (Active() == false) comes back once the line no longer
// looks like a command name still being typed: it does not start with "/",
// it already has a space in it (the user has moved on to an argument), or
// nothing in the table matches what was typed.
func slashMenuFor(text string, r slash.Registry, prev slashMenu) slashMenu {
	if !slash.IsCommand(text) || strings.ContainsAny(text, " \t\n") {
		return slashMenu{}
	}
	items := r.Filter(strings.TrimPrefix(text, "/"))
	if len(items) == 0 {
		return slashMenu{}
	}
	sel := prev.sel
	if sel < 0 || sel >= len(items) {
		sel = 0
	}
	return slashMenu{items: items, sel: sel}
}

// Active reports whether the dropdown has anything to draw.
func (m slashMenu) Active() bool { return len(m.items) > 0 }

// Selected is the command the current selection points at. Callers must
// check Active first — an empty menu has nothing to select, and this
// deliberately panics rather than guess, so a caller that forgets the check
// fails loudly in a test instead of quietly running the wrong command.
func (m slashMenu) Selected() slash.Command { return m.items[m.sel] }

// moveDown/moveUp cycle the selection, wrapping at either end so the arrow
// keys are never stuck against a boundary.
func (m slashMenu) moveDown() slashMenu { return m.move(1) }
func (m slashMenu) moveUp() slashMenu   { return m.move(-1) }

func (m slashMenu) move(delta int) slashMenu {
	if len(m.items) == 0 {
		return m
	}
	n := len(m.items)
	m.sel = ((m.sel+delta)%n + n) % n
	return m
}

// renderSlashMenu draws the dropdown of §9.6: up to slashMenuRows commands,
// the selected one highlighted, boxed the same way the input is everywhere
// except BPMinimo, where nothing gets a border (§9.1) and every column
// matters more than the frame around it.
func renderSlashMenu(lay Layout, st theme.Styles, m slashMenu) string {
	if !m.Active() {
		return ""
	}

	width := lay.ContentWidth()
	if lay.ShowBoxedInput() {
		width -= 2 // the two rounded vertical borders InputBox also accounts for
	}
	if width < 1 {
		width = 1
	}

	items, offset := visibleSlashRows(m.items, m.sel, slashMenuRows)

	nameWidth := 0
	for _, c := range items {
		if w := lipglossWidth(c.Usage()); w > nameWidth {
			nameWidth = w
		}
	}

	lines := make([]string, len(items))
	for i, c := range items {
		row := fmt.Sprintf("%-*s  %s", nameWidth, c.Usage(), c.Describe)
		row = ansi.Truncate(row, width, "…")
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

// visibleSlashRows returns the window of items that keeps sel on screen when
// there are more matches than slashMenuRows can show at once, plus the index
// the window starts at (so the caller can tell which visible row is
// selected).
func visibleSlashRows(items []slash.Command, sel, rows int) ([]slash.Command, int) {
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

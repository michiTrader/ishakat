// themepicker.go implements the §9.7 ctrl+t overlay: /theme [nombre]'s own
// second access path, named in §9.7's wireframe alongside the slash command
// and left explicitly unwired when /theme itself closed (theme.go's own
// package comment named this the one piece of the feature's story still
// missing — keys.go's ThemePicker: "ctrl+t" existed since Step 13 with
// nothing behind it).
//
// Like resumemenu.go (ModeResume) this is a flat list with no grouping —
// themes have no provider/tier split the model catalog does, so there is
// nothing for ←/→ to collapse — so it follows resumeMenu/confirmDialog's
// simpler shape rather than Picker's, with its own moveSel/selected pair
// and update/render methods on Root. The listing itself is exactly
// theme.go's listThemes' own theme.Available(m.themesDir) call, sorted the
// same way, so /theme and ctrl+t can never disagree about what is
// available or which one is active.
package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/theme"
)

// themePickerState is ModeThemePicker's own state, live only while mode ==
// ModeThemePicker.
type themePickerState struct {
	names []string
	sel   int
}

// moveSel moves the selection by delta rows, wrapping like Picker.moveSel
// and resumeMenu.moveSel.
func (p themePickerState) moveSel(delta int) themePickerState {
	if len(p.names) == 0 {
		return p
	}
	n := len(p.names)
	p.sel = ((p.sel+delta)%n + n) % n
	return p
}

// selected is the name under the cursor. Callers must check len(names) != 0
// first, the same contract resumeMenu.selected already asks its callers to
// follow.
func (p themePickerState) selected() string { return p.names[p.sel] }

// openThemePicker switches to ModeThemePicker, listing exactly what
// listThemes (theme.go) would print for a bare /theme, with the cursor
// starting on whichever theme is active right now — so opening and
// immediately pressing esc (or enter without moving) is a true no-op,
// the same guarantee openPicker's own active-row seeding gives /model.
func (m Root) openThemePicker() (tea.Model, tea.Cmd) {
	names := sortedThemeNames(theme.Available(m.themesDir))
	sel := 0
	for i, n := range names {
		if n == m.styles.Theme.Name {
			sel = i
			break
		}
	}
	m.themePicker = themePickerState{names: names, sel: sel}
	m.mode = ModeThemePicker
	return m, nil
}

// updateThemePicker handles every key while mode == ModeThemePicker.
// Like updateResumeMenu it owns the keyboard outright — there is no
// textarea underneath it to fall through to.
func (m Root) updateThemePicker(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	switch keyPressString(key) {
	case m.keys.Cancel:
		m.mode = ModeChat
		m.themePicker = themePickerState{}
		return m, nil
	case "up":
		m.themePicker = m.themePicker.moveSel(-1)
		return m, nil
	case "down":
		m.themePicker = m.themePicker.moveSel(1)
		return m, nil
	case m.keys.Submit:
		if len(m.themePicker.names) == 0 {
			return m, nil
		}
		name := m.themePicker.selected()
		m.themePicker = themePickerState{}
		m.mode = ModeChat
		// switchTheme (theme.go) is the exact same apply-and-persist path
		// /theme [nombre] uses — the overlay is a second door into the
		// same room, never a parallel implementation that could drift
		// from it.
		return m.switchTheme(name)
	}
	return m, nil
}

// renderThemePicker draws the overlay. Like renderResumeMenu it replaces
// the whole live region and draws no box border — this package's glyph
// table has none, and a flat theme list is exactly the kind of one-off
// screen that comment warns against inventing a border for.
func (m Root) renderThemePicker() string {
	g := m.lay.glyphs()
	width := m.lay.ContentWidth()
	p := m.themePicker

	var b strings.Builder
	fmt.Fprintf(&b, " temas %s %d\n", g.dot, len(p.names))
	b.WriteString(" " + strings.Repeat(g.rule, max(width-2, 1)) + "\n")

	if len(p.names) == 0 {
		b.WriteString(" no hay temas disponibles\n")
	}
	for i, name := range p.names {
		pointer := " "
		if i == p.sel {
			pointer = g.inputPrefix
		}
		mark := " "
		if name == m.styles.Theme.Name {
			mark = g.assistantMark
		}
		line := pointer + " " + mark + " " + name
		if i == p.sel {
			line = m.styles.Accent.Render(line)
		}
		b.WriteString(" " + line + "\n")
	}

	b.WriteString(" " + strings.Repeat(g.rule, max(width-2, 1)) + "\n")
	fmt.Fprintf(&b, " %s move  enter use  esc cancel\n", g.scrollHint)
	return b.String()
}

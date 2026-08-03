// resumemenu.go implements the §13 /resume overlay: a flat list of
// previously saved sessions to reopen. Unlike Picker it needs no filtering
// or grouping — sessions have no provider/tier split the model catalog
// does — so it follows confirmDialog's simpler shape instead: a value type
// carrying its own rows and selection index, with a moveSel/selected pair
// and its own update/render methods on Root.
package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// resumeMenu is ModeResume's own state, live only while mode == ModeResume.
type resumeMenu struct {
	rows []SessionSummary
	sel  int
	// err is set when SessionLister.List itself failed (a corrupted
	// directory, a permission error) — distinct from "no sessions yet",
	// which is rows == nil with err == nil and is not a failure at all
	// (see runResumeCommand's own handling of both cases before the menu
	// ever opens).
	err error
}

// moveSel moves the selection by delta rows, wrapping like Picker.moveSel
// and confirmDialog.moveSel.
func (r resumeMenu) moveSel(delta int) resumeMenu {
	if len(r.rows) == 0 {
		return r
	}
	n := len(r.rows)
	r.sel = ((r.sel+delta)%n + n) % n
	return r
}

// selected is the row under the cursor. Callers must check len(rows) != 0
// first — unlike confirmDialog.selected, an empty menu is a real state here
// (runResumeCommand only opens the overlay when there is at least one row,
// but a session could in principle disappear from disk while the menu is
// already open).
func (r resumeMenu) selected() SessionSummary { return r.rows[r.sel] }

// runResumeCommand opens the §13 /resume overlay, or reports why it cannot:
// no SessionLister at all ([session] save = false, or a store that failed
// to open — see NewSessionLister's own comment), a List failure, or simply
// nothing saved yet. Only ModeChat's slashNotice is used for all three —
// same weight runRetry's own "no hay nada que reintentar" guard gives an
// empty conversation — there is nothing left to fall back to once List
// itself cannot answer.
func (m Root) runResumeCommand() (tea.Model, tea.Cmd) {
	if m.sessionLister == nil {
		return m.slashNotice(m.lay.glyphs().warnMark + " no hay sesiones guardadas para reabrir")
	}
	rows, err := m.sessionLister.List()
	if err != nil {
		return m.slashNotice(m.lay.glyphs().warnMark + " " + err.Error())
	}
	if len(rows) == 0 {
		return m.slashNotice(m.lay.glyphs().warnMark + " no hay sesiones guardadas para reabrir")
	}
	m.mode = ModeResume
	m.resume = resumeMenu{rows: rows}
	return m, nil
}

// updateResumeMenu handles every key while mode == ModeResume (§13). Like
// updatePicker and updateConfirm it owns the keyboard outright.
func (m Root) updateResumeMenu(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	switch keyPressString(key) {
	case m.keys.Cancel:
		m.mode = ModeChat
		m.resume = resumeMenu{}
		return m, nil
	case "up":
		m.resume = m.resume.moveSel(-1)
		return m, nil
	case "down":
		m.resume = m.resume.moveSel(1)
		return m, nil
	case m.keys.Submit:
		if len(m.resume.rows) == 0 {
			return m, nil
		}
		id := m.resume.selected().ID
		return m, func() tea.Msg { return sessionChosenMsg{ID: id} }
	}
	return m, nil
}

// renderResumeMenu draws the §13 overlay. Like renderConfirm it replaces the
// whole live region and draws no box border — this package's glyph table
// has none, and a session list is exactly the kind of one-off screen that
// comment warns against inventing one for.
func (m Root) renderResumeMenu() string {
	g := m.lay.glyphs()
	width := m.lay.ContentWidth()
	r := m.resume

	var b strings.Builder
	b.WriteString(" reabrir una sesión\n")
	b.WriteString(" " + strings.Repeat(g.rule, width-1) + "\n")

	if r.err != nil {
		fmt.Fprintf(&b, " %s %s\n", g.warnMark, r.err.Error())
	} else if len(r.rows) == 0 {
		b.WriteString(" no hay sesiones guardadas\n")
	}

	for i, row := range r.rows {
		pointer := " "
		if i == r.sel {
			pointer = g.inputPrefix
		}
		line := pointer + " " + row.Title + "  " + m.lay.glyphs().dot + " " + resumeAge(row.UpdatedAt)
		if i == r.sel {
			line = m.styles.Accent.Render(line)
		}
		b.WriteString(" " + line + "\n")
	}

	b.WriteString(" " + strings.Repeat(g.rule, width-1) + "\n")
	fmt.Fprintf(&b, " %s move  enter choose  esc cancel\n", g.scrollHint)
	return b.String()
}

// resumeAge renders how long ago t was, the way each row of the §13 menu
// reads: "3 days", "5 hours", "12 minutes", "moments" — this package's own
// version of internal/app.humanAge (§4.4's staleness strip), duplicated
// rather than imported because internal/app depends on internal/tui, not
// the other way around (§6.1), and this is three lines, not a shared
// library worth inverting that boundary for.
func resumeAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%d days", int(d.Hours()/24))
	case d >= 2*time.Hour:
		return fmt.Sprintf("%d hours", int(d.Hours()))
	case d >= 2*time.Minute:
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	default:
		return "moments"
	}
}

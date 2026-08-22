// queueedit.go implements W2 item 4's own alt+up overlay (F13, DECISION-2
// consequence 3, docs/ROADMAP-ux-2026-08-20.md): "alt+up re-opens the
// follow-up queue for editing." Follows resumeMenu.go's own shape — a
// value type carrying its own rows and selection index, a moveSel/
// selected pair, and its own update/render methods on Root — rather than
// Picker's own filtering/grouping machinery: the follow-up queue has no
// provider/tier split to filter by, only a flat, ordered list of strings.
package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// queueEditDialog is ModeQueueEdit's own state, live only while
// mode == ModeQueueEdit.
type queueEditDialog struct {
	// rows is a snapshot (steeringQueue.peekFollowups' own copy), not a
	// live view: this dialog's own "d" key (removeFollowupAt) mutates the
	// real queue directly and then re-snapshots, rather than rendering
	// through a pointer that could drift out from under a half-drawn
	// frame — see peekFollowups' own comment for why a copy, not an
	// alias, is what it hands back.
	rows []string
	sel  int

	// returnMode is which mode this dialog was opened from — ModeBusy (a
	// turn is still running underneath, alt+up pressed from updateBusy)
	// or ModeChat (alt+up pressed with no turn in flight, updateChat's own
	// case) — so closing it goes back to exactly that, the same way
	// ModeToolApprove always knows to return to ModeBusy without asking:
	// see ModeQueueEdit's own doc comment (root.go) for why both opens
	// are legitimate.
	returnMode Mode
}

// moveSel moves the selection by delta rows, wrapping like Picker.moveSel,
// confirmDialog.moveSel and resumeMenu.moveSel.
func (q queueEditDialog) moveSel(delta int) queueEditDialog {
	if len(q.rows) == 0 {
		return q
	}
	n := len(q.rows)
	q.sel = ((q.sel+delta)%n + n) % n
	return q
}

// openQueueEdit opens ModeQueueEdit from whichever mode called it
// (ModeBusy's own alt+enter/EditQueue case, or ModeChat's identical
// case) — see queueEditDialog.returnMode's own comment for why that
// caller's mode, not a hard-coded one, is what closing this dialog
// returns to.
func (m Root) openQueueEdit() (tea.Model, tea.Cmd) {
	returnMode := m.mode
	m.queueEdit = queueEditDialog{
		rows:       m.steeringQueue().peekFollowups(),
		returnMode: returnMode,
	}
	m.mode = ModeQueueEdit
	return m, nil
}

// closeQueueEdit returns to whichever mode openQueueEdit recorded,
// clearing the dialog's own state — mirroring updateResumeMenu's own
// Cancel case, just with a caller-chosen return mode instead of a
// hard-coded ModeChat.
func (m Root) closeQueueEdit() (tea.Model, tea.Cmd) {
	m.mode = m.queueEdit.returnMode
	m.queueEdit = queueEditDialog{}
	return m, nil
}

// updateQueueEdit handles every key while mode == ModeQueueEdit. Like
// updateResumeMenu it owns the keyboard outright: up/down moves the
// cursor, "d" deletes the selected row (removeFollowupAt, steering.go),
// and Cancel/Submit both close the dialog — there is nothing to
// "submit" here (the queue is not resubmitted from this dialog; it
// stays queued and checkFollowup drains it in its own time, per
// DECISION-2 consequence 3), so Submit is simply this dialog's other
// way to say "done looking," identical to Cancel.
func (m Root) updateQueueEdit(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	switch keyPressString(key) {
	case m.keys.Cancel, m.keys.Submit:
		return m.closeQueueEdit()
	case "up":
		m.queueEdit = m.queueEdit.moveSel(-1)
		return m, nil
	case "down":
		m.queueEdit = m.queueEdit.moveSel(1)
		return m, nil
	case "d":
		if len(m.queueEdit.rows) == 0 {
			return m, nil
		}
		m.steeringQueue().removeFollowupAt(m.queueEdit.sel)
		m.queueEdit.rows = m.steeringQueue().peekFollowups()
		if m.queueEdit.sel >= len(m.queueEdit.rows) && m.queueEdit.sel > 0 {
			m.queueEdit.sel--
		}
		return m, nil
	}
	return m, nil
}

// renderQueueEdit draws the overlay. Like renderResumeMenu/renderConfirm
// it replaces the whole live region and draws no box border.
func (m Root) renderQueueEdit() string {
	g := m.lay.glyphs()
	width := m.lay.ContentWidth()
	q := m.queueEdit

	var b strings.Builder
	b.WriteString(" cola de mensajes para después\n")
	b.WriteString(" " + strings.Repeat(g.rule, max(width-2, 1)) + "\n")

	if len(q.rows) == 0 {
		b.WriteString(" no hay mensajes en espera\n")
	}

	for i, row := range q.rows {
		pointer := " "
		if i == q.sel {
			pointer = g.inputPrefix
		}
		line := pointer + " " + row
		if i == q.sel {
			line = m.styles.Accent.Render(line)
		}
		b.WriteString(" " + line + "\n")
	}

	b.WriteString(" " + strings.Repeat(g.rule, max(width-2, 1)) + "\n")
	fmt.Fprintf(&b, " %s move  d eliminar  enter/esc volver\n", g.scrollHint)
	return b.String()
}

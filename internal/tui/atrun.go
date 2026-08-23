// atrun.go is the only place that knows what completing an "@" reference
// means (F18, atmenu.go's own package comment) — the exact mirror of
// slashrun.go's role for the "/" dropdown. atmenu.go stays ignorant of
// engines, conversations and terminals; this file is UI/state work Root
// already owns elsewhere.
package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// updateAtMenu handles the keys the "@" dropdown claims while it is open:
// up/down move the selection, tab and enter both accept it (unlike
// updateSlashMenu, there is no separate "run it" action here — completing a
// path is the only thing this dropdown ever does, so enter completing
// instead of submitting the whole message is the point, not a shortcut),
// and esc closes it without touching the input. Any other key returns
// handled=false so the caller's normal dispatch — and the textarea
// underneath it — still sees it, which is what keeps the list narrowing as
// more of the path is typed.
//
// This is deliberately checked *after* updateSlashMenu at every call site
// (see updateChat's own ordering): the two dropdowns are never active at
// the same time (slashMenuFor's own zero-value contract only fires while
// the whole line still looks like a bare command name, atMenuFor's only
// once an "@" appears at the true end of the buffer — a line cannot be
// "/model" and end in "@partial" simultaneously), so the order between them
// only matters for which one Cancel/Submit/tab/up/down are read against
// when, by construction, at most one is ever Active().
func (m Root) updateAtMenu(key string) (bool, tea.Model, tea.Cmd) {
	switch key {
	case "up":
		m.atMenu = m.atMenu.moveUp()
		return true, m, nil
	case "down":
		m.atMenu = m.atMenu.moveDown()
		return true, m, nil
	case "tab", m.keys.Submit:
		next, cmd := m.applyAtCompletion()
		return true, next, cmd
	case m.keys.Cancel:
		m.atMenu = atMenu{}
		return true, m, nil
	}
	return false, m, nil
}

// applyAtCompletion replaces the in-progress "@token" at the end of the
// input with "@" plus the selected entry, and re-derives the dropdown
// state for what that completion produced.
//
// A directory entry (Selected() ends in "/", per PathLister's own
// contract) is applied *without* a trailing space and the menu is
// recomputed against the new, longer token — completing into a directory
// should immediately offer that directory's own contents, the same way a
// shell's path completion keeps going rather than stopping halfway. A file
// entry gets a trailing space and the menu closes: there is nothing further
// to complete once a file has been named, and a space is what lets the
// user keep typing the rest of the message right away, mirroring
// updateSlashMenu's own tab case ("/model" + " ") for exactly the same
// reason.
//
// ta.SetValue (not a manual splice into the middle of the string) is what
// currentWordAtEnd's own "true end of the buffer only" restriction buys:
// the token being completed is always the trailing run of the whole value,
// so replacing that suffix and calling SetValue on the result leaves the
// textarea's cursor exactly where it belongs (SetValue always ends at the
// tail of what it just set) with no separate cursor-repositioning step.
func (m Root) applyAtCompletion() (tea.Model, tea.Cmd) {
	if !m.atMenu.Active() {
		return m, nil
	}
	sel := m.atMenu.Selected()

	value := m.input.Value()
	word := currentWordAtEnd(m.input)
	base := strings.TrimSuffix(value, word)

	completed := "@"
	if m.atMenu.dir != "" {
		completed += m.atMenu.dir + "/"
	}
	completed += sel

	if strings.HasSuffix(sel, "/") {
		m.input.SetValue(base + completed)
		m.atMenu = atMenuFor(currentWordAtEnd(m.input), m.pathLister, atMenu{})
		return m, nil
	}
	m.input.SetValue(base + completed + " ")
	m.atMenu = atMenu{}
	return m, nil
}

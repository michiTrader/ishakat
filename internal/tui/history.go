// history.go implements the input history navigable with up/down that Step
// 13 (§11's closing table, "historial de input navegable con flechas") asks
// for: every line actually submitted to the engine — chat text or a slash
// command — can be recalled with m.keys.HistoryPrev/HistoryNext, the same
// up/down chord a shell's own line editor uses.
//
// This is deliberately a different history from convo.Conversation: that one
// is what travels to the model on the next request, this one is only what
// the user typed into the box, purely a client-side editing convenience with
// no bearing on any turn's content.
package tui

// recordHistory appends text to the input history and resets the browsing
// cursor to "not navigating" (historyIdx == len(inputHistory)), so the very
// next up-arrow starts from the newest entry rather than replaying whatever
// position a previous browse left behind.
//
// Consecutive duplicates are folded into one entry: pressing enter twice on
// the same retried line (or /retry itself, which never goes through this
// path) should not make history navigation step through the same text twice.
func (m Root) recordHistory(text string) Root {
	if text == "" {
		return m
	}
	if n := len(m.inputHistory); n == 0 || m.inputHistory[n-1] != text {
		m.inputHistory = append(m.inputHistory, text)
	}
	m.historyIdx = len(m.inputHistory)
	m.historyDraft = ""
	return m
}

// historyPrev recalls the entry before the one currently shown (§ up arrow).
// The first call while not already browsing saves the textarea's current
// content as historyDraft, so a later historyNext can hand it back instead
// of losing whatever the user had half-typed before reaching for history.
//
// ok is false when there is nothing to recall — an empty history, or the
// cursor already sitting on the oldest entry — which is the caller's signal
// to let the keypress fall through to the textarea's own cursor movement
// instead of consuming it.
func (m Root) historyPrev() (Root, bool) {
	if len(m.inputHistory) == 0 || m.historyIdx == 0 {
		return m, false
	}
	if m.historyIdx == len(m.inputHistory) {
		m.historyDraft = m.input.Value()
	}
	m.historyIdx--
	m.input.SetValue(m.inputHistory[m.historyIdx])
	return m, true
}

// historyNext moves towards the newest entry (§ down arrow) and, once past
// the last recorded one, restores historyDraft — what was being typed before
// the browse started. ok is false when the cursor is already at that "not
// navigating" position, which lets a plain down arrow move the cursor inside
// a multi-line draft instead of being swallowed here.
func (m Root) historyNext() (Root, bool) {
	if m.historyIdx >= len(m.inputHistory) {
		return m, false
	}
	m.historyIdx++
	if m.historyIdx == len(m.inputHistory) {
		m.input.SetValue(m.historyDraft)
		m.historyDraft = ""
	} else {
		m.input.SetValue(m.inputHistory[m.historyIdx])
	}
	return m, true
}

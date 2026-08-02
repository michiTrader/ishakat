// retry.go implements /retry (§13, Step 13): ask the same last question
// again without retyping it, most useful right after a cancelled turn or an
// answer that missed the point.
package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/convo"
)

// runRetry drops the trailing assistant message (if there is one — a turn
// that ended in a provider error before producing anything is never
// recorded at all, see finishTurn's own "body != "" || aborted" guard) and
// restarts the engine against the same conversation, unchanged otherwise.
//
// The trailing message is removed from m.conv rather than left in place:
// the whole point of asking again is that the previous answer was not
// good enough, and the next request should not have to explain to the
// model why an earlier turn of its own is being second-guessed by the
// exact same prompt. It is deliberately *not* removed from m.transcript —
// once a bubble has reached the terminal's real scrollback (see
// commitEntryCmd) there is no erasing it, and leaving it on screen is
// simply "here is what you asked and got before"; the fresh answer lands
// as a new bubble right after it, same as any other turn.
func (m Root) runRetry() (tea.Model, tea.Cmd) {
	if m.mode != ModeChat {
		return m, nil
	}
	if n := len(m.conv.Messages); n > 0 && m.conv.Messages[n-1].Role == convo.RoleAssistant {
		m.conv.Messages = m.conv.Messages[:n-1]
	}
	if !hasUserMessage(m.conv.Messages) {
		return m.slashNotice(m.lay.glyphs().warnMark + " no hay nada que reintentar")
	}
	// /retry never draws the startup banner: reaching this point requires
	// at least one user message already in history, which means the
	// transcript cannot be the empty, banner-eligible state bannerText()
	// checks for.
	return m.startEngineTurn("")
}

// hasUserMessage reports whether msgs contains at least one user turn —
// the guard against /retry firing on a brand-new conversation (right after
// /new, or at startup) where there is nothing to ask again.
func hasUserMessage(msgs []convo.Message) bool {
	for _, m := range msgs {
		if m.Role == convo.RoleUser {
			return true
		}
	}
	return false
}

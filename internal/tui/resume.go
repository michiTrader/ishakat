// resume.go is the TUI's half of §13's remaining items: reopening a
// previously saved conversation (--resume, resume_last, /resume). This file
// currently holds only the piece that lands with no CLI plumbing at all —
// turning Options.History into what the live region already knows how to
// draw — because NewRoot is where every other resolved value (Engine,
// Catalog, Alias, Favorites…) already gets put where the rest of the
// package expects it, and History was documented there since the previous
// session but never actually wired in.
package tui

import "github.com/MichiTrader/ishakat/internal/convo"

// historyToTranscript turns a previously saved conversation's messages into
// the transcriptEntry rows renderTranscriptLine already knows how to draw.
//
// Only convo.RoleUser and convo.RoleAssistant produce a row: those markers
// are the only two renderTranscriptLine recognises (role == "assistant" gets
// g.assistantMark, anything else gets g.userMark), and they are also the
// only two roles this package — or headless.go — has ever written to a
// session file. A system or tool message predates Step 14 having any way to
// exist on disk in the first place, so there is nothing to skip today; the
// switch's default case exists so Step 14 does not have to touch this
// function to stay correct, only to decide what such a row should look like
// once one can actually appear.
func historyToTranscript(history []convo.Message) []transcriptEntry {
	entries := make([]transcriptEntry, 0, len(history))
	for _, m := range history {
		var role, name string
		switch m.Role {
		case convo.RoleUser:
			role, name = "user", "tú"
		case convo.RoleAssistant:
			role, name = "assistant", m.Model
		default:
			continue
		}
		if name == "" {
			// Defensive only: every assistant message this package or
			// headless.go has ever written sets Model. A session file
			// edited by hand, or written by a future version that forgot
			// to, must still render something instead of a blank column
			// where the model name goes.
			name = "asistente"
		}

		text := m.Text()
		// The "[cancelado]" suffix is presentation added by finishTurn at
		// render time, not something convo.Message ever stores (Aborted is
		// the durable fact; the suffix is how it is worded on screen) — so
		// reopening a session has to add it back the same way finishTurn
		// does, or a cancelled turn would silently look identical to a
		// completed one once reloaded from disk.
		if m.Aborted {
			text += " [cancelado]"
		}

		entries = append(entries, transcriptEntry{role: role, name: name, text: text, ts: m.Ts})
	}
	return entries
}

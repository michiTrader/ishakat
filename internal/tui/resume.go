// resume.go is the TUI's half of §13's remaining items: reopening a
// previously saved conversation (--resume, resume_last, /resume). This file
// currently holds only the piece that lands with no CLI plumbing at all —
// turning Options.History into what the live region already knows how to
// draw — because NewRoot is where every other resolved value (Engine,
// Catalog, Alias, Favorites…) already gets put where the rest of the
// package expects it, and History was documented there since the previous
// session but never actually wired in.
package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/MichiTrader/ishakat/internal/convo"
)

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

// restoredMissionsNotice turns Options.RestoredMissions into the one
// transcript entry §21.16 decision 3's own first consequence requires: "on
// resume, the restored constraints are displayed, not merely reloaded".
// Returns nil for the overwhelmingly common case, a resumed session (or a
// fresh one) that never recorded a MissionEvent at all — mirroring
// historyToTranscript's own "nothing to show" degradation for an empty
// history, rather than appending an empty notice nobody asked for.
//
// One notice for the whole session, not one per event: what a human
// re-opening a session needs to see is the constraints now in effect, the
// same "the restored constraints are displayed" wording the decision uses
// (singular final state, not a replay of every intermediate step) — the
// full per-event replay onto the live Guard still happens exactly as
// recorded (internal/app's own replayMissions), this is purely the
// display half. Rules accumulate across every event (mirroring
// MissionEvent's own doc comment and replayMissions' own accumulation),
// deduplicated the same way missionConstraintLine already deduplicates a
// live session's repeated rule; BashScope takes only the last event's
// value, mirroring replayMissions' own "replaces, never accumulates" rule
// for that field.
func restoredMissionsNotice(g glyphs, events []convo.MissionEvent) *transcriptEntry {
	if len(events) == 0 {
		return nil
	}
	seen := make(map[convo.MissionRule]bool)
	var parts []string
	var lastScope []string
	haveScope := false
	for _, ev := range events {
		for _, r := range ev.Rules {
			if seen[r] {
				continue
			}
			seen[r] = true
			parts = append(parts, "no "+r.Capability+"("+r.Pattern+")")
		}
		lastScope = ev.BashScope
		haveScope = true
	}
	if len(parts) == 0 && !haveScope {
		return nil
	}

	var b strings.Builder
	b.WriteString(g.warnMark + " restored mission constraints from this session:")
	if len(parts) > 0 {
		b.WriteString("\n  " + strings.Join(parts, " · "))
	}
	if haveScope {
		if len(lastScope) > 0 {
			fmt.Fprintf(&b, "\n  bash(%s)", strings.Join(lastScope, ", "))
		} else {
			b.WriteString("\n  bash: no subcommand restriction")
		}
	}

	return &transcriptEntry{role: "assistant", name: "ishakat", text: b.String(), ts: time.Now()}
}

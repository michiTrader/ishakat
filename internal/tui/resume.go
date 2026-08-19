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
// session file. A system message predates Step 14 having any way to exist
// on disk in the first place, so there is nothing to skip today; the
// switch's default case exists so Step 14 does not have to touch this
// function to stay correct, only to decide what such a row should look like
// once one can actually appear.
//
// A run of assistant/tool messages between two user messages (or between a
// user message and the end of history) becomes exactly ONE transcript
// entry, not one per convo.Message — mirroring finishAgentTurn's own shape
// (agentturn.go): a tools-enabled turn can append several assistant
// messages (one per agent-loop iteration, each possibly carrying only tool
// calls and no text) plus a RoleTool result for every call, and the live
// interface only ever draws the *last* assistant message in that run as
// the visible answer, prefixed with toolActivityLines' own summary of
// everything the turn did. Mapping each convo.Message to its own row
// instead (the previous shape of this function) reproduced that summary
// nowhere: an assistant message whose only content was a tool call — no
// BlockText — rendered as a near-blank bubble on resume, silently dropping
// the one visible record of what ran. This is the root cause behind the
// "loading a past conversation does not load all past messages" report:
// the messages were always fully present in conv.Messages (the model's own
// context), just invisible on screen once historyToTranscript threw the
// tool-activity information away.
func historyToTranscript(g glyphs, history []convo.Message) []transcriptEntry {
	entries := make([]transcriptEntry, 0, len(history))

	for i := 0; i < len(history); {
		m := history[i]
		switch m.Role {
		case convo.RoleUser:
			entries = append(entries, transcriptEntry{role: "user", name: "tú", text: m.Text(), ts: m.Ts})
			i++

		case convo.RoleAssistant:
			// Collect the whole turn: this message and every following
			// non-user message (further assistant iterations, tool
			// results) up to but not including the next user message or
			// the end of history — the same span finishAgentTurn's own
			// hist.Messages[before:] covers. `last` tracks the final
			// assistant message in that span, the one whose text is the
			// turn's actual answer (finishAgentTurn's own `body`); the
			// messages before it in the same span carried only tool calls
			// (asstBlocks in agentloop.go's RunAgentTurn has no BlockText
			// until the natural-termination iteration).
			start := i
			last := m
			for i < len(history) && history[i].Role != convo.RoleUser {
				if history[i].Role == convo.RoleAssistant {
					last = history[i]
				}
				i++
			}

			name := last.Model
			if name == "" {
				// Defensive only: every assistant message this package or
				// headless.go has ever written sets Model. A session file
				// edited by hand, or written by a future version that
				// forgot to, must still render something instead of a
				// blank column where the model name goes.
				name = "asistente"
			}

			text := last.Text()
			// The "[cancelado]" suffix is presentation added by
			// finishTurn/finishAgentTurn at render time, not something
			// convo.Message ever stores (Aborted is the durable fact; the
			// suffix is how it is worded on screen) — so reopening a
			// session has to add it back the same way, or a cancelled
			// turn would silently look identical to a completed one once
			// reloaded from disk.
			if last.Aborted {
				text += " [cancelado]"
			}

			// toolActivityLines (toolactivity.go) is the exact function
			// finishAgentTurn calls live; handing it just this turn's own
			// slice — not the whole history — keeps its "from" parameter
			// meaningful (0) without needing a second, end-bounded
			// signature purely for this one caller. missionRules is nil
			// here: a resumed session has no live *permissions.Guard to
			// ask (Options carries RestoredMissions for §21.16's own
			// notice, not a Guard to query MissionRules() on), so the
			// dispatch sub-line is the one piece of the live summary this
			// reconstruction cannot recover — every other line (which
			// tool, on what, did it fail) still can, from the same
			// BlockToolCall/BlockToolResult data that survives on disk.
			turn := &convo.Conversation{Messages: history[start:i]}
			if summary := toolActivityLines(g, turn, 0, nil); summary != "" {
				if text == "" {
					text = summary
				} else {
					text = summary + "\n" + text
				}
			}

			entries = append(entries, transcriptEntry{role: "assistant", name: name, text: text, ts: last.Ts})

		default:
			// A RoleTool or RoleSystem message reaching here directly
			// (not already consumed as part of a preceding RoleAssistant
			// turn above) predates any turn this function knows how to
			// group — e.g. a hand-edited session file. Skipped, same as
			// this function's previous "nothing else has ever been
			// written" default case.
			i++
		}
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

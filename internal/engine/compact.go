// compact.go is Step 12's model-calling half of /compact: convo.PlanCompact
// (§4's pure "moneda común") already decided *what* to replace, without
// ever importing a Streamer; this file is only the "ask a model to
// summarize it" half, which is why it lives here — internal/convo has no
// business calling a model, and internal/tui has no business doing
// anything that is not presentation (§6.1).
package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/MichiTrader/ishakat/internal/convo"
)

// summarySystemPrompt instructs compact_model on what to produce. Written in
// English on purpose, unlike the rest of this repository's user-facing
// copy: it is never shown to a person, only sent over the wire to whichever
// model compact_model resolves to, and an instruction prompt reads more
// reliably to a model in English regardless of which language the
// conversation itself is in — which is also why it explicitly asks for the
// summary to match that language instead of assuming one.
const summarySystemPrompt = "You are compacting a chat transcript so the conversation can continue with a much shorter context. Read the transcript below and write a concise summary that preserves every concrete fact, decision, open question, and piece of context a future reply would need to stay coherent. Write the summary in the same language the transcript itself is written in. Output only the summary itself — no preamble, no meta-commentary about the task."

// roleLabel names a role for the plain-text transcript Summarize builds.
// Deliberately not convo.Role.String() (there isn't one): this is prose for
// a model prompt, not a serialization format, and the two are free to
// diverge.
func roleLabel(r convo.Role) string {
	switch r {
	case convo.RoleUser:
		return "User"
	case convo.RoleAssistant:
		return "Assistant"
	case convo.RoleTool:
		return "Tool"
	case convo.RoleSystem:
		return "System"
	default:
		return string(r)
	}
}

// blockPlaceholder names a block kind a plain-text transcript cannot carry
// verbatim, the same "degrade to descriptive text instead of breaking the
// request" rule §4.6 already applies to a hot swap that loses a capability
// — compact_model never receives raw image bytes or tool-call JSON, only a
// note that one happened, which is enough context for the summary to
// mention it without silently dropping it.
func blockPlaceholder(b convo.Block) (text string, ok bool) {
	switch b.Kind {
	case convo.BlockImage:
		name := b.Name
		if name == "" {
			name = "unnamed"
		}
		return fmt.Sprintf("[image attached: %s]", name), true
	case convo.BlockToolCall:
		name := b.Name
		if name == "" {
			name = "unnamed tool"
		}
		return fmt.Sprintf("[called tool: %s]", name), true
	case convo.BlockToolResult:
		name := b.Name
		if name == "" {
			name = "unnamed tool"
		}
		return fmt.Sprintf("[result from tool: %s]", name), true
	default:
		return "", false
	}
}

// renderTranscript turns the messages at idx (in order) into the plain-text
// transcript summarySystemPrompt asks compact_model to read. Reasoning
// blocks are skipped on purpose — they are the model's own scratch space,
// never a fact the conversation depends on — and empty messages (a
// cancelled turn with nothing in it) contribute nothing rather than a bare
// "Assistant:" line.
func renderTranscript(msgs []convo.Message, idx []int) string {
	var b strings.Builder
	for _, i := range idx {
		if i < 0 || i >= len(msgs) {
			continue // defensive: a caller-supplied idx must never panic here
		}
		m := msgs[i]
		var line strings.Builder
		for _, blk := range m.Blocks {
			var piece string
			switch {
			case blk.Kind == convo.BlockText || blk.Kind == convo.BlockSummary:
				piece = blk.Text
			default:
				if ph, ok := blockPlaceholder(blk); ok {
					piece = ph
				}
			}
			if piece == "" {
				continue
			}
			if line.Len() > 0 {
				line.WriteString("\n")
			}
			line.WriteString(piece)
		}
		if line.Len() == 0 {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(roleLabel(m.Role))
		b.WriteString(": ")
		b.WriteString(line.String())
	}
	return b.String()
}

// Summarize asks eng (already bound to compact_model's provider) to
// summarize the messages plan.Replace names, and returns the trimmed answer
// convo.ApplySummary should store. It never returns a placeholder: a Plan
// with nothing to replace is an empty string and no error (the caller's cue
// to skip ApplySummary, same as before this function existed), a model
// call that fails returns that error unwrapped so the caller can decide
// between retrying and falling back to convo.DropOldest per
// [compact].on_error, and a model that answers with only whitespace is
// treated as a failure too — an empty BlockSummary would make
// convo.Conversation.Active() forget those messages ever happened for
// nothing.
func Summarize(ctx context.Context, eng *Engine, model string, msgs []convo.Message, plan convo.Plan) (string, error) {
	if plan.Empty() {
		return "", nil
	}
	transcript := renderTranscript(msgs, plan.Replace)
	if transcript == "" {
		// Every replaced message was empty (aborted turns with nothing in
		// them, say) — nothing worth spending a model call on, and nothing
		// a summary could truthfully describe.
		return "", nil
	}

	ans, err := eng.RunToCompletion(ctx, Request{
		Model:    model,
		System:   summarySystemPrompt,
		Messages: []convo.Message{convo.User(transcript)},
	})
	if err != nil {
		return "", err
	}
	text := strings.TrimSpace(ans.Text)
	if text == "" {
		return "", fmt.Errorf("compact_model %q returned an empty summary", model)
	}
	return text, nil
}

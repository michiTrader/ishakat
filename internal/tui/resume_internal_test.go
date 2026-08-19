package tui

import (
	"strings"
	"testing"

	"github.com/MichiTrader/ishakat/internal/convo"
)

// TestOptionsHistoryIsWiredIntoRoot is the regression test for the second
// half of §13's "reopen a session" plumbing: NewRoot has to put a previously
// saved conversation both where the next request reads it from (m.conv) and
// where the user sees it on screen (m.transcript). Missing either one
// reproduces the exact "pieza existida, no conectada" pattern the Recorder
// bug in this same file already demonstrated once.
func TestOptionsHistoryIsWiredIntoRoot(t *testing.T) {
	hist := []convo.Message{
		convo.User("hola"),
		convo.Assistant("¡hola! ¿en qué ayudo?", "openai/gpt-5"),
	}
	root := NewRoot(Options{History: hist})

	if len(root.conv.Messages) != 2 {
		t.Fatalf("root.conv.Messages = %d, want 2 (History must reach the request path)", len(root.conv.Messages))
	}
	if len(root.transcript) != 2 {
		t.Fatalf("root.transcript = %d, want 2 (History must reach the screen)", len(root.transcript))
	}
	if root.transcript[0].role != "user" || root.transcript[0].text != "hola" {
		t.Errorf("transcript[0] = %+v, want the user's message", root.transcript[0])
	}
	if root.transcript[1].role != "assistant" || root.transcript[1].name != "openai/gpt-5" {
		t.Errorf("transcript[1] = %+v, want the assistant's message with its model as name", root.transcript[1])
	}
}

// TestHistoryToTranscriptMarksAbortedTurns is resume.go's own documented
// rule: the "[cancelado]" suffix is presentation, not something stored on
// disk, so reopening a session has to re-derive it from Aborted the same way
// finishTurn worded it live — otherwise a cancelled turn looks identical to
// a completed one once reloaded.
func TestHistoryToTranscriptMarksAbortedTurns(t *testing.T) {
	m := convo.Assistant("respuesta parcial", "openai/gpt-5")
	m.Aborted = true
	entries := historyToTranscript(unicodeGlyphs, []convo.Message{m})
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if got := entries[0].text; got != "respuesta parcial [cancelado]" {
		t.Errorf("text = %q, want the [cancelado] suffix appended", got)
	}
}

// TestHistoryToTranscriptSkipsUnknownRoles documents today's boundary: a
// system message reaching historyToTranscript on its own (not as part of a
// preceding assistant turn — see the function's own doc comment) produces
// no row, and the default case exists so a future role does not silently
// crash instead of being a deliberate decision made when it is actually
// added.
func TestHistoryToTranscriptSkipsUnknownRoles(t *testing.T) {
	entries := historyToTranscript(unicodeGlyphs, []convo.Message{convo.System("sistema")})
	if len(entries) != 0 {
		t.Fatalf("entries = %d, want 0 (system messages produce no transcript row today)", len(entries))
	}
}

// TestHistoryToTranscriptGroupsToolTurnIntoOneEntry is the regression test
// for the "loading a past conversation does not load all past messages"
// report: a tools-enabled turn's assistant-with-tool-call message (no
// BlockText of its own — see agentloop.go's own asstBlocks comment) must
// not resume as a near-blank bubble. The whole turn — the tool-call-only
// message, the tool result, and the final answer — has to collapse into
// ONE transcript entry whose text carries both the tool-activity summary
// and the answer, the same shape finishAgentTurn draws live.
func TestHistoryToTranscriptGroupsToolTurnIntoOneEntry(t *testing.T) {
	user := convo.User("crea un archivo hola.txt")
	asst1 := convo.NewMessage(convo.RoleAssistant,
		convo.ToolCallBlock("tc1", "write_file", argsFor(t, map[string]string{"path": "hola.txt"})))
	asst1.Model = "openai/gpt-5"
	toolRes := convo.NewMessage(convo.RoleTool, convo.ToolResultBlock("tc1", "write_file", "ok"))
	asst2 := convo.Assistant("listo, creé el archivo", "openai/gpt-5")

	entries := historyToTranscript(unicodeGlyphs, []convo.Message{user, asst1, toolRes, asst2})
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2 (one user row, one grouped assistant-turn row)", len(entries))
	}
	if entries[0].role != "user" {
		t.Errorf("entries[0].role = %q, want %q", entries[0].role, "user")
	}
	got := entries[1]
	if got.role != "assistant" || got.name != "openai/gpt-5" {
		t.Errorf("entries[1] = %+v, want the assistant turn with its model as name", got)
	}
	if !strings.Contains(got.text, "write_file") {
		t.Errorf("text = %q, want the tool-activity summary naming write_file", got.text)
	}
	if !strings.Contains(got.text, "hola.txt") {
		t.Errorf("text = %q, want the tool-activity summary naming the target path", got.text)
	}
	if !strings.Contains(got.text, "listo, creé el archivo") {
		t.Errorf("text = %q, want the turn's final answer", got.text)
	}
}

// TestHistoryToTranscriptToolOnlyTurnStillShowsSummary covers the exact
// shape the original report reproduced: a turn whose only assistant
// message is a bare tool call, with no follow-up text at all (e.g. the
// engine stopped, or the session was captured mid-turn). The tool-activity
// summary alone must still render — an empty text would be the same
// near-blank bubble the fix exists to eliminate.
func TestHistoryToTranscriptToolOnlyTurnStillShowsSummary(t *testing.T) {
	user := convo.User("lista los archivos")
	asst := convo.NewMessage(convo.RoleAssistant,
		convo.ToolCallBlock("tc1", "glob", argsFor(t, map[string]string{"pattern": "**/*.go"})))
	asst.Model = "openai/gpt-5"
	toolRes := convo.NewMessage(convo.RoleTool, convo.ToolResultBlock("tc1", "glob", "main.go"))

	entries := historyToTranscript(unicodeGlyphs, []convo.Message{user, asst, toolRes})
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	if entries[1].text == "" {
		t.Errorf("entries[1].text is empty, want the tool-activity summary")
	}
	if !strings.Contains(entries[1].text, "glob") {
		t.Errorf("text = %q, want it to name glob", entries[1].text)
	}
}

// TestHistoryToTranscriptMultiTurnConversation guards against the grouping
// logic accidentally merging separate turns together, or losing a plain
// (tool-free) turn sandwiched between two tool-using ones.
func TestHistoryToTranscriptMultiTurnConversation(t *testing.T) {
	u1 := convo.User("primer mensaje")
	a1 := convo.NewMessage(convo.RoleAssistant,
		convo.ToolCallBlock("tc1", "write_file", argsFor(t, map[string]string{"path": "a.txt"})))
	a1.Model = "openai/gpt-5"
	r1 := convo.NewMessage(convo.RoleTool, convo.ToolResultBlock("tc1", "write_file", "ok"))
	a1b := convo.Assistant("listo con a.txt", "openai/gpt-5")

	u2 := convo.User("segundo mensaje, sin herramientas")
	a2 := convo.Assistant("respuesta simple", "openai/gpt-5")

	entries := historyToTranscript(unicodeGlyphs, []convo.Message{u1, a1, r1, a1b, u2, a2})
	if len(entries) != 4 {
		t.Fatalf("entries = %d, want 4 (u1, grouped a1 turn, u2, a2)", len(entries))
	}
	if entries[0].role != "user" || entries[0].text != "primer mensaje" {
		t.Errorf("entries[0] = %+v, want the first user message", entries[0])
	}
	if !strings.Contains(entries[1].text, "write_file") || !strings.Contains(entries[1].text, "listo con a.txt") {
		t.Errorf("entries[1].text = %q, want both the tool summary and the first turn's answer", entries[1].text)
	}
	if entries[2].role != "user" || entries[2].text != "segundo mensaje, sin herramientas" {
		t.Errorf("entries[2] = %+v, want the second user message", entries[2])
	}
	if entries[3].role != "assistant" || entries[3].text != "respuesta simple" {
		t.Errorf("entries[3] = %+v, want the tool-free second turn's plain answer", entries[3])
	}
}

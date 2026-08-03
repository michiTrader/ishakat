package tui

import (
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
	entries := historyToTranscript([]convo.Message{m})
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if got := entries[0].text; got != "respuesta parcial [cancelado]" {
		t.Errorf("text = %q, want the [cancelado] suffix appended", got)
	}
}

// TestHistoryToTranscriptSkipsUnknownRoles documents today's boundary: only
// user/assistant messages have ever been written to a session file, and the
// default case exists so a future role does not silently crash instead of
// being a deliberate decision made when it is actually added.
func TestHistoryToTranscriptSkipsUnknownRoles(t *testing.T) {
	entries := historyToTranscript([]convo.Message{convo.System("sistema")})
	if len(entries) != 0 {
		t.Fatalf("entries = %d, want 0 (system messages produce no transcript row today)", len(entries))
	}
}

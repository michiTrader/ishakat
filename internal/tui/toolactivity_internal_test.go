package tui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichiTrader/ishakat/internal/convo"
)

// argsFor is a small helper so each case below reads as the tool call it
// describes rather than as a JSON literal.
func argsFor(t *testing.T, fields map[string]string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return raw
}

// TestToolActivityLinesIsEmptyWithoutToolCalls pins the case that must stay
// completely invisible: an ordinary question-and-answer turn. This is the
// overwhelming majority of turns, and adding even one line to them would
// make the summary a permanent tax on the transcript instead of a report on
// the exceptional event of a tool running.
func TestToolActivityLinesIsEmptyWithoutToolCalls(t *testing.T) {
	hist := &convo.Conversation{Messages: []convo.Message{
		convo.User("hello"),
		mustAssistantText("hi there"),
	}}
	if got := toolActivityLines(unicodeGlyphs, hist, 0); got != "" {
		t.Errorf("toolActivityLines = %q, want empty for a turn with no tool calls", got)
	}
	if n := toolActivityCount(hist, 0); n != 0 {
		t.Errorf("toolActivityCount = %d, want 0", n)
	}
}

// TestToolActivityLinesNamesTheToolAndItsTarget is the line the bug report
// needed: proof on screen that write_file ran, and on which path.
func TestToolActivityLinesNamesTheToolAndItsTarget(t *testing.T) {
	msg := convo.NewMessage(convo.RoleAssistant)
	msg.Blocks = append(msg.Blocks, convo.ToolCallBlock("c1", "write_file",
		argsFor(t, map[string]string{"path": "step16-approval.txt", "content": "Step 16 approval works."})))
	hist := &convo.Conversation{Messages: []convo.Message{convo.User("create it"), msg}}

	got := toolActivityLines(unicodeGlyphs, hist, 0)
	if !strings.Contains(got, "write_file") {
		t.Errorf("toolActivityLines = %q, want it to name write_file", got)
	}
	if !strings.Contains(got, "step16-approval.txt") {
		t.Errorf("toolActivityLines = %q, want it to name the target path", got)
	}
	// The file's content must never reach the summary line: write_file's
	// content argument can be an entire file, and pasting it into the
	// transcript would bury the answer the user is reading.
	if strings.Contains(got, "Step 16 approval works.") {
		t.Errorf("toolActivityLines = %q, want it to omit the file content", got)
	}
	if n := strings.Count(got, "\n"); n != 0 {
		t.Errorf("toolActivityLines has %d newlines, want a single line for a single call", n)
	}
}

// TestToolActivityLinesMarksAFailedCall covers the case where silence is
// worst: a denied or failed tool call. "The model did nothing" and "the
// model tried and was refused" look identical otherwise, and only the
// second means the user should answer the approval dialog differently or
// fix a path.
func TestToolActivityLinesMarksAFailedCall(t *testing.T) {
	call := convo.NewMessage(convo.RoleAssistant)
	call.Blocks = append(call.Blocks, convo.ToolCallBlock("c1", "write_file",
		argsFor(t, map[string]string{"path": "/root/nope.txt"})))
	result := convo.NewMessage(convo.RoleTool)
	result.Blocks = append(result.Blocks, convo.ToolErrorBlock("c1", "write_file",
		"tool permission denied: user declined write_file\nsecond line must not appear"))
	hist := &convo.Conversation{Messages: []convo.Message{convo.User("write it"), call, result}}

	got := toolActivityLines(unicodeGlyphs, hist, 0)
	if !strings.Contains(got, unicodeGlyphs.warnMark) {
		t.Errorf("toolActivityLines = %q, want the warn glyph for a failed call", got)
	}
	if !strings.Contains(got, "user declined write_file") {
		t.Errorf("toolActivityLines = %q, want the failure reason", got)
	}
	if strings.Contains(got, "second line must not appear") {
		t.Errorf("toolActivityLines = %q, want only the first line of a multi-line error", got)
	}
}

// TestToolActivityLinesOnlySummarizesThisTurn pins the `from` cut: a long
// conversation's earlier tool calls must not be re-summarized on every
// later turn, which would make each answer longer than the last.
func TestToolActivityLinesOnlySummarizesThisTurn(t *testing.T) {
	old := convo.NewMessage(convo.RoleAssistant)
	old.Blocks = append(old.Blocks, convo.ToolCallBlock("old", "read_file",
		argsFor(t, map[string]string{"path": "already-summarized.txt"})))
	fresh := convo.NewMessage(convo.RoleAssistant)
	fresh.Blocks = append(fresh.Blocks, convo.ToolCallBlock("new", "glob",
		argsFor(t, map[string]string{"pattern": "**/*.go"})))
	hist := &convo.Conversation{Messages: []convo.Message{convo.User("a"), old, fresh}}

	got := toolActivityLines(unicodeGlyphs, hist, 2) // only `fresh` is new.
	if strings.Contains(got, "already-summarized.txt") {
		t.Errorf("toolActivityLines = %q, want no earlier turn's tool calls", got)
	}
	if !strings.Contains(got, "**/*.go") {
		t.Errorf("toolActivityLines = %q, want this turn's glob pattern", got)
	}
}

// TestToolActivityLinesTruncatesALongCommand keeps a shell one-liner from
// turning the summary into a wall of text — Termux at 40 columns is this
// project's own worst case, and the summary is meant to be one line per
// call.
func TestToolActivityLinesTruncatesALongCommand(t *testing.T) {
	long := strings.Repeat("echo hello && ", 40)
	call := convo.NewMessage(convo.RoleAssistant)
	call.Blocks = append(call.Blocks, convo.ToolCallBlock("c1", "bash",
		argsFor(t, map[string]string{"command": long})))
	hist := &convo.Conversation{Messages: []convo.Message{call}}

	got := toolActivityLines(unicodeGlyphs, hist, 0)
	if runeLen(got) > 80 {
		t.Errorf("toolActivityLines is %d runes long, want a truncated single line", runeLen(got))
	}
	if !strings.Contains(got, "bash") {
		t.Errorf("toolActivityLines = %q, want it to name bash", got)
	}
}

// TestToolActivityLinesHandlesNilAndUnknownArgs is the defensive case: a
// hallucinated tool name with an argument shape this code has never seen
// must still produce a true line (the tool's name) instead of panicking or
// inventing a target.
func TestToolActivityLinesHandlesNilAndUnknownArgs(t *testing.T) {
	if got := toolActivityLines(unicodeGlyphs, nil, 0); got != "" {
		t.Errorf("toolActivityLines(nil) = %q, want empty", got)
	}

	call := convo.NewMessage(convo.RoleAssistant)
	call.Blocks = append(call.Blocks,
		convo.ToolCallBlock("c1", "invented_tool", json.RawMessage(`{"weird":true}`)),
		convo.ToolCallBlock("c2", "broken_args", json.RawMessage(`not json at all`)),
	)
	hist := &convo.Conversation{Messages: []convo.Message{call}}

	got := toolActivityLines(unicodeGlyphs, hist, 0)
	if !strings.Contains(got, "invented_tool") || !strings.Contains(got, "broken_args") {
		t.Errorf("toolActivityLines = %q, want both tool names present", got)
	}
	if lines := strings.Count(got, "\n") + 1; lines != 2 {
		t.Errorf("toolActivityLines produced %d lines, want one per call", lines)
	}
}

// mustAssistantText is a plain text assistant message, the shape every
// non-tool turn produces.
func mustAssistantText(text string) convo.Message {
	m := convo.NewMessage(convo.RoleAssistant)
	m.Blocks = append(m.Blocks, convo.TextBlock(text))
	return m
}

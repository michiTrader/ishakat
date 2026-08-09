// toolactivity.go turns the tool calls and results a finished agent turn
// left in the conversation into the short, human-readable summary
// finishAgentTurn prepends to the answer.
//
// It exists because of the second half of the Step 16 bug report. Once tools
// actually reached the wire, a turn that wrote a file was still
// indistinguishable on screen from one that merely talked about writing a
// file: the transcript only ever showed the final assistant text, so
// "created step16-approval.txt" and "here is the echo command you could
// run" rendered identically, and the user's only recourse was to leave the
// interface and run `ls`. A tool call is the most consequential thing a turn
// can do — it changes the filesystem — and it was the one thing the
// interface never mentioned.
//
// This deliberately summarizes rather than dumping tool output. A tool
// result can be a whole file or 32 KiB of `bash` output (engine's own
// MaxOutputBytes ceiling); pasting that into the transcript would bury the
// answer the user is actually reading. What matters is: which tool ran, on
// what, and did it fail.
package tui

import (
	"encoding/json"
	"strings"

	"github.com/MichiTrader/ishakat/internal/convo"
)

// toolActivityLines renders one line per tool call in hist.Messages[from:],
// or "" when the turn made none — the overwhelmingly common case (a plain
// question), which must add nothing at all to the transcript.
//
// from is finishAgentTurn's own agentTurnState.before: the index the turn
// started appending at, so a long conversation's earlier tool calls are
// never re-summarized on every subsequent turn.
//
// Failures are marked with warnMark and carry the tool's own error text,
// because a denied or failed call is exactly the case where silence is
// worst: "the model did nothing" and "the model tried and was refused" look
// the same otherwise, and only the second one means the user should answer
// the approval dialog differently or fix a path.
func toolActivityLines(g glyphs, hist *convo.Conversation, from int) string {
	if hist == nil || from < 0 {
		return ""
	}

	// errors maps a tool_call_id to that call's failure text, gathered
	// first: results arrive in later messages than the calls they answer,
	// and a call's line has to be able to say it failed on the same line
	// it reports the call.
	errors := make(map[string]string)
	for i := from; i < len(hist.Messages); i++ {
		for _, b := range hist.Messages[i].Blocks {
			if b.Kind == convo.BlockToolResult && b.IsError {
				errors[b.ToolCallID] = strings.TrimSpace(b.Text)
			}
		}
	}

	var lines []string
	for i := from; i < len(hist.Messages); i++ {
		for _, b := range hist.Messages[i].Blocks {
			if b.Kind != convo.BlockToolCall {
				continue
			}
			line := g.modelMark + " " + b.Name
			if target := toolTarget(b.Args); target != "" {
				line += " " + target
			}
			if failure, failed := errors[b.ToolCallID]; failed {
				line += "  " + g.warnMark
				if failure != "" {
					line += " " + firstLine(failure)
				}
			}
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
}

// toolTarget is the one argument worth showing next to a tool's name: the
// path for the file tools, the command for bash, the pattern for glob/grep.
// Every core tool (§19.1) names its subject with one of these three keys, so
// this stays a fixed lookup rather than dumping the whole argument object —
// which would put a file's entire new content on the transcript line for
// write_file.
//
// An unrecognized or unparseable argument shape yields "": the tool's name
// alone is still a true and useful line, and inventing a rendering for an
// argument set this function does not know is how a summary starts lying.
func toolTarget(args json.RawMessage) string {
	if len(args) == 0 {
		return ""
	}
	var parsed struct {
		Path    string `json:"path"`
		Command string `json:"command"`
		Pattern string `json:"pattern"`
	}
	if err := json.Unmarshal(args, &parsed); err != nil {
		return ""
	}
	switch {
	case parsed.Path != "":
		return parsed.Path
	case parsed.Command != "":
		// truncateRunes is path.go's own helper (runes, not bytes: Termux at
		// 40 columns is this project's worst case for anything that assumes
		// one byte is one column). A shell one-liner can be arbitrarily
		// long, and the summary is a single line.
		return truncateRunes(firstLine(parsed.Command), 60)
	case parsed.Pattern != "":
		return parsed.Pattern
	}
	return ""
}

// firstLine is the text up to the first newline. A multi-line failure (a
// stack trace, a shell's stderr) must not turn one summary line into twelve.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

// toolActivityCount is toolActivityLines' own count, exposed for the tests
// that assert "a turn with no tools adds nothing" without depending on the
// exact wording of the lines.
func toolActivityCount(hist *convo.Conversation, from int) int {
	if hist == nil || from < 0 {
		return 0
	}
	n := 0
	for i := from; i < len(hist.Messages); i++ {
		for _, b := range hist.Messages[i].Blocks {
			if b.Kind == convo.BlockToolCall {
				n++
			}
		}
	}
	return n
}

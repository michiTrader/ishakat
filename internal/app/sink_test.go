package app

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// --- P3: textSink/jsonSink warn dedupe -----------------------------------

// TestTextSinkWarnDedupesExactRepeats mirrors WarningPrinter's own
// regression test (warnings_test.go) at the headless-mode sink level:
// headless.go's step 4 can call warn with the identical string more than
// once in a single run, and textSink must not print the same sentence to
// stderr twice.
func TestTextSinkWarnDedupesExactRepeats(t *testing.T) {
	var errb bytes.Buffer
	s := &textSink{err: &errb}

	s.warn("app.default_model (omniroute/auto/coding) is disabled; using openai/gpt-4o-mini instead")
	s.warn("app.default_model (omniroute/auto/coding) is disabled; using openai/gpt-4o-mini instead")

	got := errb.String()
	if n := strings.Count(got, "⚠"); n != 1 {
		t.Fatalf("the identical warning was printed %d times, want exactly 1. Output:\n%s", n, got)
	}
}

// TestTextSinkWarnKeepsDistinctWarnings is the flip side: two different
// warning strings must both be printed.
func TestTextSinkWarnKeepsDistinctWarnings(t *testing.T) {
	var errb bytes.Buffer
	s := &textSink{err: &errb}

	s.warn("first warning")
	s.warn("second warning")

	got := errb.String()
	if n := strings.Count(got, "⚠"); n != 2 {
		t.Fatalf("want both distinct warnings printed, got %d. Output:\n%s", n, got)
	}
}

// TestTextSinkWarnQuietSuppressesEverything: quiet must still silence all
// warnings regardless of dedupe state — the two concerns are independent.
func TestTextSinkWarnQuietSuppressesEverything(t *testing.T) {
	var errb bytes.Buffer
	s := &textSink{err: &errb, quiet: true}

	s.warn("should not appear")
	if errb.Len() != 0 {
		t.Errorf("quiet textSink wrote %q, want nothing", errb.String())
	}
}

// TestJSONSinkWarnDedupesExactRepeats is textSink's own test, applied to
// the --json sink: a jq consumer has just as little use for the same
// "warning" event encoded twice.
func TestJSONSinkWarnDedupesExactRepeats(t *testing.T) {
	var buf bytes.Buffer
	s := newJSONSink(&buf)

	s.warn("duplicate warning")
	s.warn("duplicate warning")

	got := buf.String()
	if n := strings.Count(got, "duplicate warning"); n != 1 {
		t.Fatalf("the identical warning was encoded %d times, want exactly 1. Output:\n%s", n, got)
	}
}

// --- Step 17: headless tool-result reporting -----------------------------
//
// The gap this pins: tool() alone only ever says a call was requested.
// Before toolResult existed, `ishakat -p` (and its --json sibling) had no
// way to say whether that call actually succeeded — a denied write_file and
// a successful one printed the identical single line on stderr. These tests
// fail against the pre-fix textSink/jsonSink (neither had a toolResult
// method at all, so this file would not even compile) and pin the exact
// signal each mode must carry once it exists.

// TestTextSinkToolResultMarksFailureDistinctlyFromSuccess is the textSink
// half: a failed/denied call must be visually distinguishable (the warning
// glyph) from a successful one, and must carry the tool's own error text so
// the user knows *why* — not just that something went wrong.
func TestTextSinkToolResultMarksFailureDistinctlyFromSuccess(t *testing.T) {
	var okBuf, failBuf bytes.Buffer
	ok := &textSink{err: &okBuf}
	fail := &textSink{err: &failBuf}

	ok.toolResult("read_file", false, "file contents here")
	fail.toolResult("write_file", true, "tool permission denied: write is disabled by configuration")

	okOut, failOut := okBuf.String(), failBuf.String()
	if strings.Contains(okOut, "⚠") {
		t.Errorf("a successful tool result must not carry the warning glyph, got: %q", okOut)
	}
	if !strings.Contains(failOut, "⚠") {
		t.Errorf("a failed tool result must carry the warning glyph, got: %q", failOut)
	}
	if !strings.Contains(failOut, "write_file") || !strings.Contains(failOut, "permission denied") {
		t.Errorf("a failure line must name the tool and the reason, got: %q", failOut)
	}
	// The success line must not dump the tool's whole output onto stderr —
	// toolactivity.go's TUI sibling makes exactly this same call for exactly
	// this same reason (write_file's new content, a whole file's worth of
	// bash output).
	if strings.Contains(okOut, "file contents here") {
		t.Errorf("a successful tool result must summarize, not dump its output, got: %q", okOut)
	}
}

// TestTextSinkToolResultTruncatesMultilineFailureToFirstLine mirrors
// internal/tui/toolactivity.go's own regression for the identical reason: a
// stack trace or a shell's stderr must not turn one progress line into a
// dozen.
func TestTextSinkToolResultTruncatesMultilineFailureToFirstLine(t *testing.T) {
	var errb bytes.Buffer
	s := &textSink{err: &errb}

	s.toolResult("bash", true, "exit status 1\nfull stack trace\nmore noise\nand more")

	got := errb.String()
	if strings.Contains(got, "full stack trace") {
		t.Errorf("only the first line of a failure should be printed, got: %q", got)
	}
	if !strings.Contains(got, "exit status 1") {
		t.Errorf("the first line of the failure must be printed, got: %q", got)
	}
}

// TestJSONSinkToolResultEncodesNameTextAndError is the --json half: a jq
// consumer needs a structured way to tell a failed call from a successful
// one and to read its (already engine-truncated) output, not just a
// "something happened" line.
func TestJSONSinkToolResultEncodesNameTextAndError(t *testing.T) {
	var buf bytes.Buffer
	s := newJSONSink(&buf)

	s.toolResult("read_file", false, "file contents")
	s.toolResult("write_file", true, "tool permission denied: write is disabled by configuration")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 JSON lines, got %d: %q", len(lines), buf.String())
	}

	var okEvent, failEvent jsonEvent
	if err := json.Unmarshal([]byte(lines[0]), &okEvent); err != nil {
		t.Fatalf("line 1 is not valid JSON: %v", err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &failEvent); err != nil {
		t.Fatalf("line 2 is not valid JSON: %v", err)
	}

	if okEvent.Type != "tool_result" || okEvent.Name != "read_file" || okEvent.Text != "file contents" || okEvent.Error {
		t.Errorf("success event wrong shape: %+v", okEvent)
	}
	if failEvent.Type != "tool_result" || failEvent.Name != "write_file" || !failEvent.Error {
		t.Errorf("failure event wrong shape: %+v", failEvent)
	}
	if !strings.Contains(failEvent.Text, "permission denied") {
		t.Errorf("failure event must carry the tool's own error text, got: %q", failEvent.Text)
	}
}

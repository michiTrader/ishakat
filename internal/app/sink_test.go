package app

import (
	"bytes"
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

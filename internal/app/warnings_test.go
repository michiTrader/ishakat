package app

import (
	"bytes"
	"strings"
	"testing"

	"github.com/MichiTrader/ishakat/internal/config"
)

func TestFilterWarningsForProvidersKeepsUnscopedWarnings(t *testing.T) {
	warns := []config.Warning{
		{Where: "tools", Msg: "max_calls_per_turn = 0"},
		{Where: "config", Msg: "ignored key: foo"},
		{Where: "credentials.toml", Msg: "insecure permissions"},
		{Where: "provider[2]", Msg: `kind "weird" no soportado`},
	}
	got := FilterWarningsForProviders(warns, "google")
	if len(got) != len(warns) {
		t.Fatalf("FilterWarningsForProviders dropped an unscoped warning: got %d, want %d\n%+v",
			len(got), len(warns), got)
	}
}

func TestFilterWarningsForProvidersDropsUnwantedProviderWarning(t *testing.T) {
	warns := []config.Warning{
		{Where: "provider[openai]", Msg: "missing $OPENAI_API_KEY; the provider is left unauthenticated"},
		{Where: "provider[anthropic]", Msg: "missing $ANTHROPIC_API_KEY; the provider is left unauthenticated"},
		{Where: "provider[google]", Msg: "missing $GEMINI_API_KEY; the provider is left unauthenticated"},
	}
	got := FilterWarningsForProviders(warns, "google")
	if len(got) != 1 {
		t.Fatalf("got %d warnings, want exactly 1 (only google): %+v", len(got), got)
	}
	if got[0].Where != "provider[google]" {
		t.Errorf("got %+v, want the google warning kept", got[0])
	}
}

func TestFilterWarningsForProvidersNoWantedIDsDropsAllProviderWarnings(t *testing.T) {
	warns := []config.Warning{
		{Where: "provider[omniroute]", Msg: "falta $OMNIROUTE_API_KEY"},
		{Where: "tools.evolve", Msg: "allow_without_tty = true"},
	}
	got := FilterWarningsForProviders(warns)
	if len(got) != 1 || got[0].Where != "tools.evolve" {
		t.Fatalf("got %+v, want only the unscoped tools.evolve warning", got)
	}
}

func TestFilterWarningsForProvidersKeepsMultipleWantedProviders(t *testing.T) {
	warns := []config.Warning{
		{Where: "provider[omniroute]", Msg: "a"},
		{Where: "provider[openai]", Msg: "b"},
	}
	got := FilterWarningsForProviders(warns, "omniroute", "openai")
	if len(got) != 2 {
		t.Fatalf("got %d, want both kept when both are wanted: %+v", len(got), got)
	}
}

// --- P3: WarningPrinter dedupe ------------------------------------------

// TestWarningPrinterDedupesExactRepeats is P3's own regression test for the
// original bug report's two identical "missing $OMNIROUTE_API_KEY" lines:
// app.go's startup path calls BuildEngine twice (once for the
// conversation's own model, once for compact_model), and before
// WarningPrinter existed, a warning that happened to be textually identical
// between the two calls was printed twice, unconditionally.
func TestWarningPrinterDedupesExactRepeats(t *testing.T) {
	var buf bytes.Buffer
	p := NewWarningPrinter()
	p.Warn(&buf, "missing $OMNIROUTE_API_KEY; the provider is left unauthenticated")
	p.Warn(&buf, "missing $OMNIROUTE_API_KEY; the provider is left unauthenticated")

	got := buf.String()
	if n := strings.Count(got, "OMNIROUTE_API_KEY"); n != 1 {
		t.Fatalf("the identical warning was printed %d times, want exactly 1. Output:\n%s", n, got)
	}
}

// TestWarningPrinterKeepsDistinctWarnings is the flip side: two warnings
// that are NOT textually identical — even if they mention the same
// provider — must both be printed. This is exact-string dedupe, not
// semantic grouping (see WarningPrinter's own doc comment).
func TestWarningPrinterKeepsDistinctWarnings(t *testing.T) {
	var buf bytes.Buffer
	p := NewWarningPrinter()
	p.Warn(&buf, "missing $OMNIROUTE_API_KEY; the provider is left unauthenticated")
	p.Warn(&buf, "app.default_model (omniroute/auto/coding) is disabled; using openai/gpt-4o-mini instead")

	got := buf.String()
	if strings.Count(got, "⚠") != 2 {
		t.Fatalf("want both distinct warnings printed, got:\n%s", got)
	}
}

// TestWarningPrinterIgnoresEmptyString mirrors every call site's existing
// `if warn != ""` guard, so app.go no longer needs to repeat that check
// itself before calling Warn.
func TestWarningPrinterIgnoresEmptyString(t *testing.T) {
	var buf bytes.Buffer
	NewWarningPrinter().Warn(&buf, "")
	if buf.Len() != 0 {
		t.Errorf("Warn(\"\") wrote %q, want nothing", buf.String())
	}
}

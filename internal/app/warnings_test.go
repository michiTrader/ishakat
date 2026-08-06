package app

import (
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
	got := FilterWarningsForProviders(warns, "gemini-direct")
	if len(got) != len(warns) {
		t.Fatalf("FilterWarningsForProviders dropped an unscoped warning: got %d, want %d\n%+v",
			len(got), len(warns), got)
	}
}

func TestFilterWarningsForProvidersDropsUnwantedProviderWarning(t *testing.T) {
	warns := []config.Warning{
		{Where: "provider[openai]", Msg: "missing $OPENAI_API_KEY; the provider is left unauthenticated"},
		{Where: "provider[anthropic]", Msg: "missing $ANTHROPIC_API_KEY; the provider is left unauthenticated"},
		{Where: "provider[gemini-direct]", Msg: "missing $GEMINI_API_KEY; the provider is left unauthenticated"},
	}
	got := FilterWarningsForProviders(warns, "gemini-direct")
	if len(got) != 1 {
		t.Fatalf("got %d warnings, want exactly 1 (only gemini-direct): %+v", len(got), got)
	}
	if got[0].Where != "provider[gemini-direct]" {
		t.Errorf("got %+v, want the gemini-direct warning kept", got[0])
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

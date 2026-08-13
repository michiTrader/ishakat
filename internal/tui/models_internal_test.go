package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/config"
)

func TestSlashModelsListsTheCatalogGroupedByProvider(t *testing.T) {
	root := rootWithCatalog(catalogWithModels("a/gpt-5", "a/gpt-5-nano", "b/sonnet"))
	root.model = "a/gpt-5"

	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/models")

	got := m.(Root)
	if got.mode != ModeChat {
		t.Fatalf("mode = %v, want ModeChat: /models reports inline, it does not open an overlay", got.mode)
	}
	if len(got.transcript) != 1 {
		t.Fatalf("expected one notice entry, got %d: %v", len(got.transcript), got.transcript)
	}
	text := got.transcript[0].text
	for _, want := range []string{"A (2)", "B (1)", "gpt-5", "gpt-5-nano", "sonnet"} {
		if !strings.Contains(text, want) {
			t.Errorf("notice missing %q, got:\n%s", want, text)
		}
	}
	// The active model gets the assistant mark, so it can be told apart
	// from the rest of its provider group without opening the picker.
	if !strings.Contains(text, got.lay.glyphs().assistantMark+" gpt-5 ") {
		t.Errorf("active model should be marked, got:\n%s", text)
	}
}

func TestSlashModelsWithNoCatalogSaysSo(t *testing.T) {
	var m tea.Model = newHeadlessRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/models")

	root := m.(Root)
	if len(root.transcript) != 1 {
		t.Fatalf("expected one notice entry, got %d: %v", len(root.transcript), root.transcript)
	}
	if !strings.Contains(root.transcript[0].text, "no hay catalogo") {
		t.Errorf("notice should explain there is no catalog yet, got %q", root.transcript[0].text)
	}
}

// TestSlashConfigRendersRedactedProvidersAndMasksAPIKey closes the loop
// this increment opened: internal/config.Redacted()/Mask() (validate.go)
// used to be tested but never called from anywhere in the tree
// (docs/PLAN.md's Phase 4 paragraph flagged this) — /config's own runner
// (configcmd.go) is that first real caller, and this test is what proves
// the wiring end to end, not just that Redacted() itself works (which
// internal/config's own TestRedacted already covers).
func TestSlashConfigRendersRedactedProvidersAndMasksAPIKey(t *testing.T) {
	root := newHeadlessRoot()
	root.cfg = &config.Config{
		Files: []string{"/home/user/.config/ishakat/config.toml"},
		App:   config.App{DefaultModel: "omni/son45"},
		Providers: []config.Provider{{
			ID: "omniroute", Kind: "openai", BaseURL: "http://localhost:20128/v1",
			APIKey: "secret-1234567890", Enabled: true, AuthOK: true,
		}},
	}

	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/config")

	got := m.(Root)
	if len(got.transcript) != 1 {
		t.Fatalf("expected one notice entry, got %d: %v", len(got.transcript), got.transcript)
	}
	text := got.transcript[0].text
	for _, want := range []string{"omniroute", "openai", "omni/son45", "config.toml"} {
		if !strings.Contains(text, want) {
			t.Errorf("notice missing %q, got:\n%s", want, text)
		}
	}
	if strings.Contains(text, "secret-1234567890") {
		t.Errorf("notice must never contain the raw api_key, got:\n%s", text)
	}
}

// TestSlashConfigWithNoConfigSaysSo mirrors
// TestSlashModelsWithNoCatalogSaysSo (models.go's own "no hay catalogo"
// case): newHeadlessRoot never sets Options.Cfg, so m.cfg is nil, and
// that must report instead of panicking on a nil Redacted() receiver.
func TestSlashConfigWithNoConfigSaysSo(t *testing.T) {
	var m tea.Model = newHeadlessRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/config")

	root := m.(Root)
	if len(root.transcript) != 1 {
		t.Fatalf("expected one notice entry, got %d: %v", len(root.transcript), root.transcript)
	}
	if !strings.Contains(root.transcript[0].text, "no hay configuracion") {
		t.Errorf("notice should explain there is no config yet, got %q", root.transcript[0].text)
	}
}

func TestSlashDebugAndLoginPointAtTheirBinaryEquivalent(t *testing.T) {
	cases := []struct {
		line string
		want string
	}{
		{"/debug", "ishakat doctor"},
		{"/login", "ishakat login <proveedor>"},
	}
	for _, tc := range cases {
		var m tea.Model = newHeadlessRoot()
		m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
		m = typeAndEnter(m, tc.line)

		root := m.(Root)
		if len(root.transcript) != 1 {
			t.Fatalf("%s: expected one notice entry, got %d: %v", tc.line, len(root.transcript), root.transcript)
		}
		if !strings.Contains(root.transcript[0].text, tc.want) {
			t.Errorf("%s: notice should point at %q, got %q", tc.line, tc.want, root.transcript[0].text)
		}
	}
}

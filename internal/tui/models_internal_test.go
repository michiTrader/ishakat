package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
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

func TestSlashConfigAndDebugPointAtTheirBinaryEquivalent(t *testing.T) {
	cases := []struct {
		line string
		want string
	}{
		{"/config", "ishakat config check"},
		{"/debug", "ishakat doctor"},
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

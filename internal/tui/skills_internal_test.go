package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/skills"
)

// rootWithSkills is newHeadlessRoot plus a skills.Result attached directly
// to the unexported field: Options.Skills' own wiring from internal/app is
// exercised separately (wiring_test.go's TestSystemPromptAppendsSkillsSummaryWhenToolsEnabled
// covers the Discover call itself), this only needs Root to actually hold
// one to dispatch /skills against.
func rootWithSkills(res skills.Result) Root {
	root := newHeadlessRoot()
	root.skills = res
	return root
}

func TestSlashSkillsListsNameAndDescription(t *testing.T) {
	root := rootWithSkills(skills.Result{Skills: []skills.Skill{
		{Name: "demo", Description: "does demo things", File: "/tmp/skills/demo/SKILL.md"},
		{Name: "no-desc", Description: "", File: "/tmp/skills/no-desc/SKILL.md"},
	}})

	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/skills")

	got := m.(Root)
	if got.mode != ModeChat {
		t.Fatalf("mode = %v, want ModeChat: /skills reports inline, it does not open an overlay", got.mode)
	}
	if len(got.transcript) != 1 {
		t.Fatalf("expected one notice entry, got %d: %v", len(got.transcript), got.transcript)
	}
	text := got.transcript[0].text
	for _, want := range []string{"demo", "does demo things", "no-desc", "(sin descripcion)"} {
		if !strings.Contains(text, want) {
			t.Errorf("notice missing %q, got:\n%s", want, text)
		}
	}
	// Progressive disclosure (§19.4): the body never loads for a listing,
	// only Name+Description — there is nothing in a skills.Skill besides
	// those two plus Dir/File to leak here, but this pins the intent so a
	// future field addition does not accidentally start printing paths.
	if strings.Contains(text, "SKILL.md") {
		t.Errorf("notice must not leak the skill's file path, got:\n%s", text)
	}
}

func TestSlashSkillsWithNoneLoadedSaysSo(t *testing.T) {
	var m tea.Model = newHeadlessRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/skills")

	root := m.(Root)
	if len(root.transcript) != 1 {
		t.Fatalf("expected one notice entry, got %d: %v", len(root.transcript), root.transcript)
	}
	if !strings.Contains(root.transcript[0].text, "no hay skills") {
		t.Errorf("notice should explain there are no skills loaded, got %q", root.transcript[0].text)
	}
}

func TestSlashSkillsSurfacesTheDiscoverWarning(t *testing.T) {
	root := rootWithSkills(skills.Result{Warn: "could not parse frontmatter in /tmp/skills/broken/SKILL.md: missing closing ---"})

	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/skills")

	got := m.(Root)
	if len(got.transcript) != 1 {
		t.Fatalf("expected one notice entry, got %d: %v", len(got.transcript), got.transcript)
	}
	if !strings.Contains(got.transcript[0].text, "could not parse frontmatter") {
		t.Errorf("notice should surface Discover's warning, got %q", got.transcript[0].text)
	}
}

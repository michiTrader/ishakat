package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichiTrader/ishakat/internal/config"
)

// TestSystemPromptAppendsAgentsMDRules is Step 18's core wiring test: the
// three AGENTS.md layers, once resolved, must land after the base system
// prompt rather than replacing it — the two are answering different
// questions (§11's own framing: "rules without repeating them every
// message" is additional context, not a persona override).
func TestSystemPromptAppendsAgentsMDRules(t *testing.T) {
	xdgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdgHome)
	globalPath := filepath.Join(xdgHome, "ishakat", "AGENTS.md")
	if err := os.MkdirAll(filepath.Dir(globalPath), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(globalPath, []byte("Always answer concisely."), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	projectDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectDir, "AGENTS.md"), []byte("This repo uses Go."), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Chdir(projectDir)

	cfg := &config.Config{App: config.App{SystemPrompt: "You are ishakat.", AgentsMD: true}}
	system, warn := SystemPrompt(cfg)

	if warn != "" {
		t.Errorf("warn = %q, want empty", warn)
	}
	if !strings.HasPrefix(system, "You are ishakat.") {
		t.Errorf("system = %q, want it to start with the base prompt", system)
	}
	gi := strings.Index(system, "Always answer concisely.")
	pi := strings.Index(system, "This repo uses Go.")
	if gi == -1 || pi == -1 {
		t.Fatalf("system is missing a layer: %q", system)
	}
	if gi > pi {
		t.Errorf("global rule should come before project rule, got: %q", system)
	}
}

// TestSystemPromptAgentsMDOffLeavesPromptUnchanged is the escape hatch:
// agents_md = false must behave exactly as if AGENTS.md never existed, even
// when the file is right there in the working directory.
func TestSystemPromptAgentsMDOffLeavesPromptUnchanged(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	projectDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectDir, "AGENTS.md"), []byte("Should never be read."), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Chdir(projectDir)

	cfg := &config.Config{App: config.App{SystemPrompt: "base", AgentsMD: false}}
	system, warn := SystemPrompt(cfg)

	if system != "base" {
		t.Errorf("system = %q, want unchanged %q", system, "base")
	}
	if warn != "" {
		t.Errorf("warn = %q, want empty", warn)
	}
}

// TestSystemPromptAgentsMDWithNoBasePromptIsJustTheRules covers the common
// case: most users never set system_prompt, so AGENTS.md content becomes the
// entire prompt rather than an addendum with a stray leading blank line.
func TestSystemPromptAgentsMDWithNoBasePromptIsJustTheRules(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	projectDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectDir, "AGENTS.md"), []byte("Project rule only."), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Chdir(projectDir)

	cfg := &config.Config{App: config.App{AgentsMD: true}}
	system, _ := SystemPrompt(cfg)

	if system != "Project rule only." {
		t.Errorf("system = %q, want exactly the project rule with no leading blank line", system)
	}
}

// TestSystemPromptAgentsMDWithNothingOnDiskIsUnchanged is the no-op case that
// matters most in practice: almost every project has none of the three
// files, and turning the feature on must not print noise or alter behaviour.
func TestSystemPromptAgentsMDWithNothingOnDiskIsUnchanged(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Chdir(t.TempDir())

	cfg := &config.Config{App: config.App{SystemPrompt: "base", AgentsMD: true}}
	system, warn := SystemPrompt(cfg)

	if system != "base" {
		t.Errorf("system = %q, want unchanged %q", system, "base")
	}
	if warn != "" {
		t.Errorf("warn = %q, want empty", warn)
	}
}

// TestSystemPromptSurfacesAnAgentsMDWarningAlongsideSystemPromptFileWarning
// covers both warnings firing at once, mirroring how BuildEngine's own
// warn-combining already stacks multiple non-fatal problems into one line.
func TestSystemPromptSurfacesAnAgentsMDWarningAlongsideSystemPromptFileWarning(t *testing.T) {
	xdgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdgHome)
	// A directory instead of a file: agentsmd.Resolve reports this as a real
	// read error, not a missing file.
	globalPath := filepath.Join(xdgHome, "ishakat", "AGENTS.md")
	if err := os.MkdirAll(globalPath, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	t.Chdir(t.TempDir())

	cfg := &config.Config{App: config.App{
		SystemPromptFile: filepath.Join(t.TempDir(), "does-not-exist.txt"),
		AgentsMD:         true,
	}}
	_, warn := SystemPrompt(cfg)

	if !strings.Contains(warn, "system_prompt_file") {
		t.Errorf("warn = %q, want it to mention system_prompt_file", warn)
	}
	if !strings.Contains(warn, "global") {
		t.Errorf("warn = %q, want it to mention the unreadable global AGENTS.md layer", warn)
	}
}

// TestSystemPromptAppendsSkillsSummaryWhenToolsEnabled is Step 19's core
// wiring test: internal/skills.Discover existed since PR #97 but nothing
// ever called it, so a SKILL.md on disk had no effect on any turn. This
// pins the fix — the progressive-disclosure listing (name + description
// only, never the body) lands after the base prompt/AGENTS.md rules,
// exactly like Step 18's own appendSystemBlock pattern.
func TestSystemPromptAppendsSkillsSummaryWhenToolsEnabled(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Chdir(t.TempDir())

	skillsDir := filepath.Join(t.TempDir(), "skills", "demo")
	if err := os.MkdirAll(skillsDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	body := "---\nname: demo\ndescription: does demo things\n---\nFull body, must not appear in the prompt.\n"
	if err := os.WriteFile(filepath.Join(skillsDir, "SKILL.md"), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := &config.Config{
		App:   config.App{SystemPrompt: "You are ishakat."},
		Tools: config.Tools{Enabled: true, SkillsDir: filepath.Dir(skillsDir)},
	}
	system, warn := SystemPrompt(cfg)

	if warn != "" {
		t.Errorf("warn = %q, want empty", warn)
	}
	if !strings.Contains(system, "demo: does demo things") {
		t.Errorf("system = %q, want it to contain the skill's name+description", system)
	}
	if strings.Contains(system, "Full body") {
		t.Errorf("system = %q, must not contain the skill's body (progressive disclosure)", system)
	}
}

// TestSystemPromptSkipsSkillsWhenToolsDisabled covers the gate: a skill
// points the model at read_file to load its body, so listing one when
// cfg.Tools.Enabled is false would name a capability nothing can reach.
func TestSystemPromptSkipsSkillsWhenToolsDisabled(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Chdir(t.TempDir())

	skillsDir := filepath.Join(t.TempDir(), "skills", "demo")
	if err := os.MkdirAll(skillsDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	body := "---\nname: demo\ndescription: does demo things\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(skillsDir, "SKILL.md"), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := &config.Config{
		App:   config.App{SystemPrompt: "base"},
		Tools: config.Tools{Enabled: false, SkillsDir: filepath.Dir(skillsDir)},
	}
	system, _ := SystemPrompt(cfg)

	if system != "base" {
		t.Errorf("system = %q, want unchanged %q (tools disabled)", system, "base")
	}
}

// TestDiscoverSkillsMirrorsSystemPromptsGate is /skills' own wiring test
// (Step 19): tui.Options.Skills (app.go) has to see the exact same skills
// SystemPrompt already folded into the prompt, or /skills could list a
// capability the model was never actually told about, or vice versa. This
// pins DiscoverSkills' gate against cfg.Tools.Enabled directly, the same
// on/off split TestSystemPromptAppendsSkillsSummaryWhenToolsEnabled and
// TestSystemPromptSkipsSkillsWhenToolsDisabled already cover from
// SystemPrompt's own side.
func TestDiscoverSkillsMirrorsSystemPromptsGate(t *testing.T) {
	skillsDir := filepath.Join(t.TempDir(), "skills", "demo")
	if err := os.MkdirAll(skillsDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	body := "---\nname: demo\ndescription: does demo things\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(skillsDir, "SKILL.md"), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	enabled := &config.Config{Tools: config.Tools{Enabled: true, SkillsDir: filepath.Dir(skillsDir)}}
	res := DiscoverSkills(enabled)
	if len(res.Skills) != 1 || res.Skills[0].Name != "demo" {
		t.Errorf("DiscoverSkills(tools enabled) = %+v, want one skill named demo", res)
	}

	disabled := &config.Config{Tools: config.Tools{Enabled: false, SkillsDir: filepath.Dir(skillsDir)}}
	if res := DiscoverSkills(disabled); len(res.Skills) != 0 {
		t.Errorf("DiscoverSkills(tools disabled) = %+v, want empty (same gate as SystemPrompt)", res)
	}
}

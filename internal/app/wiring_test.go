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

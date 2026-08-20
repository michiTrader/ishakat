package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichiTrader/ishakat/internal/config"
)

// TestReportAgentsMDListsAllThreeLayersAndTheirState is Step 18's own closing
// criterion: which AGENTS.md files were found must be visible from `doctor`
// without reading the source, for all three layers, whether or not they
// exist.
func TestReportAgentsMDListsAllThreeLayersAndTheirState(t *testing.T) {
	xdgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdgHome)
	globalPath := filepath.Join(xdgHome, "ishakat", "AGENTS.md")
	if err := os.MkdirAll(filepath.Dir(globalPath), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(globalPath, []byte("global rule"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	projectDir := t.TempDir()
	t.Chdir(projectDir)
	if err := os.WriteFile(filepath.Join(projectDir, "AGENTS.md"), []byte("project rule"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// AGENTS.local.md deliberately not created: the "not found" case.

	var buf bytes.Buffer
	reportAgentsMD(&buf, &config.Config{App: config.App{AgentsMD: true}})
	out := buf.String()

	if !strings.Contains(out, "agents.md    true") {
		t.Errorf("output does not report agents.md as enabled:\n%s", out)
	}
	if !strings.Contains(out, "global") || !strings.Contains(out, "found") {
		t.Errorf("output missing the global layer's found state:\n%s", out)
	}
	if !strings.Contains(out, "project") {
		t.Errorf("output missing the project layer:\n%s", out)
	}
	if !strings.Contains(out, "local") || !strings.Contains(out, "not found") {
		t.Errorf("output missing the local layer's not-found state:\n%s", out)
	}
}

// TestReportAgentsMDOffSkipsThePathList covers agents_md = false: the report
// should say the feature is off and stop there, not list paths for a feature
// that is not even resolving them.
func TestReportAgentsMDOffSkipsThePathList(t *testing.T) {
	var buf bytes.Buffer
	reportAgentsMD(&buf, &config.Config{App: config.App{AgentsMD: false}})
	out := buf.String()

	if !strings.Contains(out, "agents.md    false") {
		t.Errorf("output does not report agents.md as disabled:\n%s", out)
	}
	if strings.Contains(out, "global") || strings.Contains(out, "project") || strings.Contains(out, "local") {
		t.Errorf("output lists layers while the feature is off:\n%s", out)
	}
}

// TestReportAgentsMDWithNilConfigDefaultsToOn covers doctorConfig's own
// documented failure mode: a broken config.toml returns cfg == nil, and
// doctor must keep reporting something sensible rather than crashing.
func TestReportAgentsMDWithNilConfigDefaultsToOn(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Chdir(t.TempDir())

	var buf bytes.Buffer
	reportAgentsMD(&buf, nil)
	out := buf.String()

	if !strings.Contains(out, "agents.md    true") {
		t.Errorf("output does not default to enabled with a nil config:\n%s", out)
	}
}

// TestReportTerminalShowsTUIModeWithItsReason is DESIGN-tui-mode.md §5's own
// closing line for `doctor`: "prints the resolved mode with its reason and
// the override that changes it — the same contract theme.Diagnosis.Advice
// already honours". reportTerminal writes to a bytes.Buffer here, which is
// never a terminal, so this also exercises the honesty rule (§3.1): an
// explicit fullscreen override is refused when stdout is redirected, and the
// refusal must say so rather than silently falling back.
func TestReportTerminalShowsTUIModeWithItsReason(t *testing.T) {
	var buf bytes.Buffer
	reportTerminal(&buf, &config.Config{UI: config.UI{TUIMode: "auto"}})
	out := buf.String()

	if !strings.Contains(out, "tui_mode") {
		t.Fatalf("output does not report tui_mode at all:\n%s", out)
	}
	if !strings.Contains(out, "regular") {
		t.Errorf("a buffer is never a terminal; tui_mode should resolve to regular:\n%s", out)
	}
}

// TestReportTerminalExplainsARefusedFullscreenOverride covers the one case
// theme.Diagnose's own contract exists to serve: a decision that came out
// "wrong" (regular, when the user asked for fullscreen) must say why, not
// just what.
func TestReportTerminalExplainsARefusedFullscreenOverride(t *testing.T) {
	var buf bytes.Buffer
	reportTerminal(&buf, &config.Config{UI: config.UI{TUIMode: "fullscreen"}})
	out := buf.String()

	if !strings.Contains(out, "not a terminal") {
		t.Errorf("output does not explain why fullscreen was refused for a non-terminal writer:\n%s", out)
	}
}

// TestReportTerminalSurvivesANilConfig mirrors
// TestReportAgentsMDWithNilConfigDefaultsToOn for the terminal half of the
// report: doctorConfig's documented nil-on-error path must not panic here
// either.
func TestReportTerminalSurvivesANilConfig(t *testing.T) {
	var buf bytes.Buffer
	reportTerminal(&buf, nil)
	if !strings.Contains(buf.String(), "tui_mode") {
		t.Errorf("output does not report tui_mode with a nil config:\n%s", buf.String())
	}
}

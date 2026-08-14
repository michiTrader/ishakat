package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichiTrader/ishakat/internal/tools"
)

func writeToolManifest(t *testing.T, toolDir, name, description string) {
	t.Helper()
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", toolDir, err)
	}
	manifest := "name = \"" + name + "\"\n" +
		"description = \"" + description + "\"\n\n" +
		"[request]\n" +
		"method = \"GET\"\n" +
		"url = \"https://example.com/x\"\n"
	if err := os.WriteFile(filepath.Join(toolDir, tools.ManifestFileName), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

func TestNewToolsListerDisabledReturnsNil(t *testing.T) {
	if l := NewToolsLister(t.TempDir(), false); l != nil {
		t.Errorf("NewToolsLister(dir, false) = %v, want nil", l)
	}
}

func TestNewToolsListerEmptyDirReturnsNil(t *testing.T) {
	if l := NewToolsLister("", true); l != nil {
		t.Errorf("NewToolsLister(\"\", true) = %v, want nil", l)
	}
}

func TestNewToolsListerEnabledWithDirReturnsNonNil(t *testing.T) {
	if l := NewToolsLister(t.TempDir(), true); l == nil {
		t.Error("NewToolsLister(dir, true) = nil, want a usable ToolsLister")
	}
}

func TestToolsListerListToolsEmptyDir(t *testing.T) {
	l := NewToolsLister(filepath.Join(t.TempDir(), "does-not-exist"), true)
	res := l.ListTools()
	if len(res.Tools) != 0 {
		t.Errorf("ListTools() = %d tools, want 0 for a missing dir", len(res.Tools))
	}
}

func TestToolsListerListToolsUnverifiedByDefault(t *testing.T) {
	dir := t.TempDir()
	writeToolManifest(t, filepath.Join(dir, "greet"), "greet", "say hello")

	l := NewToolsLister(dir, true)
	res := l.ListTools()
	if len(res.Tools) != 1 {
		t.Fatalf("ListTools() = %d tools, want 1", len(res.Tools))
	}
	got := res.Tools[0]
	if got.Name != "greet" || got.Description != "say hello" {
		t.Errorf("got %+v, want name=greet description=\"say hello\"", got)
	}
	if got.State != "unverified" {
		t.Errorf("State = %q, want unverified for a never-probed tool", got.State)
	}
	if got.LastUsed != "" {
		t.Errorf("LastUsed = %q, want empty for a never-used tool", got.LastUsed)
	}
	if got.Danger == "" {
		t.Error("Danger must not be empty")
	}
}

func TestToolsListerListToolsVerifiedStateAndUsage(t *testing.T) {
	dir := t.TempDir()
	toolDir := filepath.Join(dir, "greet")
	writeToolManifest(t, toolDir, "greet", "say hello")
	if err := tools.SaveState(toolDir, tools.ToolState{
		State:    tools.StateVerified,
		Hash:     "abc",
		UseCount: 3,
		LastUsed: "2026-08-01",
	}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	l := NewToolsLister(dir, true)
	res := l.ListTools()
	if len(res.Tools) != 1 {
		t.Fatalf("ListTools() = %d tools, want 1", len(res.Tools))
	}
	got := res.Tools[0]
	if got.State != "verified" {
		t.Errorf("State = %q, want verified", got.State)
	}
	if got.UseCount != 3 {
		t.Errorf("UseCount = %d, want 3", got.UseCount)
	}
	if got.LastUsed != "2026-08-01" {
		t.Errorf("LastUsed = %q, want 2026-08-01", got.LastUsed)
	}
}

func TestToolsListerListToolsBrokenStateWithLastError(t *testing.T) {
	dir := t.TempDir()
	toolDir := filepath.Join(dir, "flaky")
	writeToolManifest(t, toolDir, "flaky", "sometimes fails")
	if err := tools.SaveState(toolDir, tools.ToolState{
		State:     tools.StateBroken,
		UseCount:  5,
		LastUsed:  "2026-08-02",
		FailCount: 2,
		LastError: "connection refused",
	}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	l := NewToolsLister(dir, true)
	res := l.ListTools()
	if len(res.Tools) != 1 {
		t.Fatalf("ListTools() = %d tools, want 1", len(res.Tools))
	}
	got := res.Tools[0]
	if got.State != "broken" {
		t.Errorf("State = %q, want broken", got.State)
	}
	if got.LastError != "connection refused" {
		t.Errorf("LastError = %q, want \"connection refused\"", got.LastError)
	}
}

func TestToolsListerListToolsMultipleTools(t *testing.T) {
	dir := t.TempDir()
	writeToolManifest(t, filepath.Join(dir, "zzz"), "zzz", "last alphabetically")
	writeToolManifest(t, filepath.Join(dir, "aaa"), "aaa", "first alphabetically")

	l := NewToolsLister(dir, true)
	res := l.ListTools()
	if len(res.Tools) != 2 {
		t.Fatalf("ListTools() = %d tools, want 2", len(res.Tools))
	}
}

func TestToolsListerListToolsSurfacesDiscoveryWarnWithoutFailing(t *testing.T) {
	dir := t.TempDir()
	toolDir := filepath.Join(dir, "broken")
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(toolDir, tools.ManifestFileName), []byte("not valid toml [["), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	writeToolManifest(t, filepath.Join(dir, "greet"), "greet", "say hello")

	l := NewToolsLister(dir, true)
	res := l.ListTools()
	if len(res.Tools) != 1 || res.Tools[0].Name != "greet" {
		t.Errorf("ListTools() = %+v, want the valid tool still listed", res.Tools)
	}
	if res.Warn == "" {
		t.Error("Warn must be non-empty when a manifest fails to parse")
	}
}

func TestToolsListerToolManifestFound(t *testing.T) {
	dir := t.TempDir()
	toolDir := filepath.Join(dir, "greet")
	writeToolManifest(t, toolDir, "greet", "say hello")

	l := NewToolsLister(dir, true)
	body, err := l.ToolManifest("greet")
	if err != nil {
		t.Fatalf("ToolManifest: %v", err)
	}
	if !strings.Contains(body, "greet") {
		t.Errorf("ToolManifest body = %q, want it to contain the tool's own name", body)
	}
}

func TestToolsListerToolManifestNotFound(t *testing.T) {
	dir := t.TempDir()
	writeToolManifest(t, filepath.Join(dir, "greet"), "greet", "say hello")

	l := NewToolsLister(dir, true)
	if _, err := l.ToolManifest("ghost"); err == nil {
		t.Error("ToolManifest(\"ghost\") should error when no such tool exists")
	}
}

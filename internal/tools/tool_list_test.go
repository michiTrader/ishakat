package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestToolListEmptyDirReturnsNothingYetMessage(t *testing.T) {
	tl := ToolList{Dir: filepath.Join(t.TempDir(), "does-not-exist")}
	res, err := tl.Run(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Text)
	}
	if !strings.Contains(res.Text, "no layer-2 tools") {
		t.Errorf("Text = %q, want it to mention no tools exist yet", res.Text)
	}
}

func TestToolListEmptyDirNameReturnsNothingYetMessage(t *testing.T) {
	tl := ToolList{Dir: ""}
	res, err := tl.Run(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Text)
	}
	if !strings.Contains(res.Text, "no layer-2 tools") {
		t.Errorf("Text = %q, want it to mention no tools exist yet", res.Text)
	}
}

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
	if err := os.WriteFile(filepath.Join(toolDir, ManifestFileName), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

func TestToolListReportsUnverifiedByDefault(t *testing.T) {
	dir := t.TempDir()
	writeToolManifest(t, filepath.Join(dir, "greet"), "greet", "say hello")

	tl := ToolList{Dir: dir}
	res, err := tl.Run(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Text)
	}
	if !strings.Contains(res.Text, "greet") {
		t.Errorf("Text = %q, want it to mention the tool name", res.Text)
	}
	if !strings.Contains(res.Text, "state=unverified") {
		t.Errorf("Text = %q, want state=unverified for a never-probed tool", res.Text)
	}
	if !strings.Contains(res.Text, "last_used=never") {
		t.Errorf("Text = %q, want last_used=never for a never-used tool", res.Text)
	}
}

func TestToolListReportsVerifiedStateAndUsage(t *testing.T) {
	dir := t.TempDir()
	toolDir := filepath.Join(dir, "greet")
	writeToolManifest(t, toolDir, "greet", "say hello")
	if err := SaveState(toolDir, ToolState{
		State:    StateVerified,
		Hash:     "abc",
		UseCount: 3,
		LastUsed: "2026-08-01",
	}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	tl := ToolList{Dir: dir}
	res, err := tl.Run(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(res.Text, "state=verified") {
		t.Errorf("Text = %q, want state=verified", res.Text)
	}
	if !strings.Contains(res.Text, "use_count=3") {
		t.Errorf("Text = %q, want use_count=3", res.Text)
	}
	if !strings.Contains(res.Text, "last_used=2026-08-01") {
		t.Errorf("Text = %q, want last_used=2026-08-01", res.Text)
	}
}

func TestToolListReportsBrokenStateWithLastError(t *testing.T) {
	dir := t.TempDir()
	toolDir := filepath.Join(dir, "flaky")
	writeToolManifest(t, toolDir, "flaky", "sometimes fails")
	if err := SaveState(toolDir, ToolState{
		State:     StateBroken,
		UseCount:  5,
		LastUsed:  "2026-08-02",
		FailCount: 2,
		LastError: "connection refused",
	}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	tl := ToolList{Dir: dir}
	res, err := tl.Run(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(res.Text, "state=broken") {
		t.Errorf("Text = %q, want state=broken", res.Text)
	}
	if !strings.Contains(res.Text, `last_error="connection refused"`) {
		t.Errorf("Text = %q, want last_error to be surfaced for a broken tool", res.Text)
	}
}

func TestToolListListsMultipleToolsSortedByName(t *testing.T) {
	dir := t.TempDir()
	writeToolManifest(t, filepath.Join(dir, "zzz"), "zzz", "last alphabetically")
	writeToolManifest(t, filepath.Join(dir, "aaa"), "aaa", "first alphabetically")

	tl := ToolList{Dir: dir}
	res, err := tl.Run(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	aIdx := strings.Index(res.Text, "aaa:")
	zIdx := strings.Index(res.Text, "zzz:")
	if aIdx == -1 || zIdx == -1 {
		t.Fatalf("both tools should be listed, got %q", res.Text)
	}
	if aIdx > zIdx {
		t.Errorf("expected aaa to be listed before zzz, got %q", res.Text)
	}
}

func TestToolListSurfacesDiscoveryWarnWithoutFailing(t *testing.T) {
	dir := t.TempDir()
	toolDir := filepath.Join(dir, "broken")
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(toolDir, ManifestFileName), []byte("not valid toml [["), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	writeToolManifest(t, filepath.Join(dir, "greet"), "greet", "say hello")

	tl := ToolList{Dir: dir}
	res, err := tl.Run(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError {
		t.Fatalf("a discovery warning must not turn the whole call into an error result: %s", res.Text)
	}
	if !strings.Contains(res.Text, "greet") {
		t.Errorf("Text = %q, want the valid tool still listed", res.Text)
	}
	if !strings.Contains(res.Text, "warning:") {
		t.Errorf("Text = %q, want the discovery warning surfaced", res.Text)
	}
}

func TestToolListNameDescriptionDanger(t *testing.T) {
	tl := ToolList{}
	if tl.Name() != "tool_list" {
		t.Errorf("Name() = %q, want tool_list", tl.Name())
	}
	if tl.Description() == "" {
		t.Error("Description() must not be empty")
	}
	if tl.Danger() != DangerLow {
		t.Errorf("Danger() = %v, want DangerLow", tl.Danger())
	}
}

func TestToolListCancelledContextIsGoError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tl := ToolList{Dir: t.TempDir()}
	_, err := tl.Run(ctx, json.RawMessage(`{}`))
	if err == nil {
		t.Error("expected the cancelled context's error to surface")
	}
}

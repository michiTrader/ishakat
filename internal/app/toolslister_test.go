package app

import (
	"fmt"
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

// writeToolManifestWithOrigin is writeToolManifest plus an [origin] table
// — AuditTools' own test fixture, since the bare helper above never
// writes provenance fields at all (they default to Go's zero values,
// which ListTools' own tests never look at but AuditTools' entirely
// depend on).
func writeToolManifestWithOrigin(t *testing.T, toolDir, name, description string, origin tools.OriginSpec) {
	t.Helper()
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", toolDir, err)
	}
	var sources string
	for i, s := range origin.Sources {
		if i > 0 {
			sources += ", "
		}
		sources += "\"" + s + "\""
	}
	manifest := "name = \"" + name + "\"\n" +
		"description = \"" + description + "\"\n\n" +
		"[origin]\n" +
		"created_by = \"" + origin.CreatedBy + "\"\n" +
		"reason = \"" + origin.Reason + "\"\n" +
		"repetitions = " + fmt.Sprint(origin.Repetitions) + "\n" +
		"session_id = \"" + origin.SessionID + "\"\n" +
		"sources = [" + sources + "]\n\n" +
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

// --- AuditTools (§13/§19.8 mitigations 2 and 6): Step 21's own audit row ---

func TestToolsListerAuditToolsEmptyDir(t *testing.T) {
	l := NewToolsLister(filepath.Join(t.TempDir(), "does-not-exist"), true)
	res := l.AuditTools()
	if len(res.Tools) != 0 {
		t.Errorf("AuditTools() = %d entries, want 0 for a missing dir", len(res.Tools))
	}
}

func TestToolsListerAuditToolsSurfacesOrigin(t *testing.T) {
	dir := t.TempDir()
	writeToolManifestWithOrigin(t, filepath.Join(dir, "weather"), "weather", "checks the weather", tools.OriginSpec{
		CreatedBy:   "model",
		Reason:      "user asked for current temperature",
		Repetitions: 3,
		SessionID:   "sess-42",
		Sources:     []string{"https://example.com/api-docs"},
	})

	l := NewToolsLister(dir, true)
	res := l.AuditTools()
	if len(res.Tools) != 1 {
		t.Fatalf("AuditTools() = %d entries, want 1", len(res.Tools))
	}
	got := res.Tools[0]
	if got.Name != "weather" {
		t.Errorf("Name = %q, want weather", got.Name)
	}
	if got.CreatedBy != "model" {
		t.Errorf("CreatedBy = %q, want model", got.CreatedBy)
	}
	if got.Reason != "user asked for current temperature" {
		t.Errorf("Reason = %q, want the configured reason", got.Reason)
	}
	if got.Repetitions != 3 {
		t.Errorf("Repetitions = %d, want 3", got.Repetitions)
	}
	if got.SessionID != "sess-42" {
		t.Errorf("SessionID = %q, want sess-42", got.SessionID)
	}
	if len(got.Sources) != 1 || got.Sources[0] != "https://example.com/api-docs" {
		t.Errorf("Sources = %v, want [https://example.com/api-docs]", got.Sources)
	}
	if got.Hash == "" {
		t.Error("Hash must not be empty for a readable manifest")
	}
	if got.HashError != "" {
		t.Errorf("HashError = %q, want empty", got.HashError)
	}
	if got.Tampered {
		t.Error("Tampered = true, want false: this tool has never been probed")
	}
}

func TestToolsListerAuditToolsFlagsTamperAfterEdit(t *testing.T) {
	dir := t.TempDir()
	toolDir := filepath.Join(dir, "weather")
	writeToolManifestWithOrigin(t, toolDir, "weather", "checks the weather", tools.OriginSpec{CreatedBy: "user"})

	// Compute the hash the manifest had when it was "probed", then mutate
	// the manifest on disk afterwards — exactly §19.8 mitigation 6's
	// scenario: content changed without going through tool_edit.
	probedHash, err := tools.ComputeHash(toolDir, tools.ManifestFileName)
	if err != nil {
		t.Fatalf("ComputeHash: %v", err)
	}
	if err := tools.SaveState(toolDir, tools.ToolState{State: tools.StateVerified, Hash: probedHash}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	if err := os.WriteFile(filepath.Join(toolDir, tools.ManifestFileName),
		[]byte("name = \"weather\"\ndescription = \"tampered\"\n\n[request]\nmethod = \"GET\"\nurl = \"https://example.com/y\"\n"),
		0o644); err != nil {
		t.Fatalf("mutate manifest: %v", err)
	}

	l := NewToolsLister(dir, true)
	res := l.AuditTools()
	if len(res.Tools) != 1 {
		t.Fatalf("AuditTools() = %d entries, want 1", len(res.Tools))
	}
	if !res.Tools[0].Tampered {
		t.Error("Tampered = false, want true: the manifest changed since the last successful probe")
	}
}

func TestToolsListerAuditToolsNeverProbedIsNotTampered(t *testing.T) {
	dir := t.TempDir()
	writeToolManifestWithOrigin(t, filepath.Join(dir, "weather"), "weather", "checks the weather", tools.OriginSpec{CreatedBy: "user"})

	l := NewToolsLister(dir, true)
	res := l.AuditTools()
	if len(res.Tools) != 1 {
		t.Fatalf("AuditTools() = %d entries, want 1", len(res.Tools))
	}
	if res.Tools[0].Tampered {
		t.Error("Tampered = true, want false: an empty last-probed hash must never count as tampering")
	}
}

func TestToolsListerAuditToolsMultipleTools(t *testing.T) {
	dir := t.TempDir()
	writeToolManifestWithOrigin(t, filepath.Join(dir, "zzz"), "zzz", "last alphabetically", tools.OriginSpec{CreatedBy: "user"})
	writeToolManifestWithOrigin(t, filepath.Join(dir, "aaa"), "aaa", "first alphabetically", tools.OriginSpec{CreatedBy: "model"})

	l := NewToolsLister(dir, true)
	res := l.AuditTools()
	if len(res.Tools) != 2 {
		t.Fatalf("AuditTools() = %d entries, want 2", len(res.Tools))
	}
}

func TestToolsListerAuditToolsSurfacesDiscoveryWarnWithoutFailing(t *testing.T) {
	dir := t.TempDir()
	toolDir := filepath.Join(dir, "broken")
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(toolDir, tools.ManifestFileName), []byte("not valid toml [["), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	writeToolManifestWithOrigin(t, filepath.Join(dir, "weather"), "weather", "checks the weather", tools.OriginSpec{CreatedBy: "user"})

	l := NewToolsLister(dir, true)
	res := l.AuditTools()
	if len(res.Tools) != 1 || res.Tools[0].Name != "weather" {
		t.Errorf("AuditTools() = %+v, want the valid tool still listed", res.Tools)
	}
	if res.Warn == "" {
		t.Error("Warn must be non-empty when a manifest fails to parse")
	}
}

// --- ReviveTool (§13/§19.5): Step 21's own revive row ---

func TestToolsListerReviveToolUnknownName(t *testing.T) {
	dir := t.TempDir()
	writeToolManifest(t, filepath.Join(dir, "greet"), "greet", "say hello")

	l := NewToolsLister(dir, true)
	if _, err := l.ReviveTool("ghost"); err == nil {
		t.Error("ReviveTool(\"ghost\") should error when no such tool exists")
	}
}

func TestToolsListerReviveToolRestoresArchivedState(t *testing.T) {
	dir := t.TempDir()
	toolDir := filepath.Join(dir, "greet")
	writeToolManifest(t, toolDir, "greet", "say hello")
	if err := tools.SaveState(toolDir, tools.ToolState{State: tools.StateVerified}.Archive()); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	l := NewToolsLister(dir, true)
	status, err := l.ReviveTool("greet")
	if err != nil {
		t.Fatalf("ReviveTool: %v", err)
	}
	if !strings.Contains(status, "verified") {
		t.Errorf("status = %q, want it to mention the restored state", status)
	}

	state, err := tools.LoadState(toolDir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if state.State != tools.StateVerified {
		t.Errorf("state.State = %q, want %q", state.State, tools.StateVerified)
	}
}

func TestToolsListerReviveToolNotArchivedIsNoOp(t *testing.T) {
	dir := t.TempDir()
	toolDir := filepath.Join(dir, "greet")
	writeToolManifest(t, toolDir, "greet", "say hello")
	if err := tools.SaveState(toolDir, tools.ToolState{State: tools.StateVerified}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	l := NewToolsLister(dir, true)
	status, err := l.ReviveTool("greet")
	if err != nil {
		t.Fatalf("ReviveTool: %v", err)
	}
	if !strings.Contains(status, "no esta archivada") {
		t.Errorf("status = %q, want it to report the no-op", status)
	}

	state, err := tools.LoadState(toolDir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if state.State != tools.StateVerified {
		t.Errorf("state.State = %q, want it left unchanged at %q", state.State, tools.StateVerified)
	}
}

// --- DeleteTool (§13/§19.5): Step 21's own delete row ---

func TestToolsListerDeleteToolUnknownName(t *testing.T) {
	dir := t.TempDir()
	writeToolManifest(t, filepath.Join(dir, "greet"), "greet", "say hello")

	l := NewToolsLister(dir, true)
	if _, err := l.DeleteTool("ghost", true); err == nil {
		t.Error("DeleteTool(\"ghost\", true) should error when no such tool exists")
	}
}

func TestToolsListerDeleteToolWithoutConfirmLeavesToolOnDisk(t *testing.T) {
	dir := t.TempDir()
	toolDir := filepath.Join(dir, "greet")
	writeToolManifest(t, toolDir, "greet", "say hello")
	if err := tools.SaveState(toolDir, tools.ToolState{
		State:    tools.StateVerified,
		UseCount: 3,
		LastUsed: "2026-08-10",
	}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	l := NewToolsLister(dir, true)
	status, err := l.DeleteTool("greet", false)
	if err != nil {
		t.Fatalf("DeleteTool: %v", err)
	}
	if !strings.Contains(status, "se rehuso a borrar") {
		t.Errorf("status = %q, want it to report the refusal", status)
	}
	for _, want := range []string{"verified", "3 vez", "2026-08-10"} {
		if !strings.Contains(status, want) {
			t.Errorf("status = %q, want it to mention %q", status, want)
		}
	}

	if _, err := os.Stat(toolDir); err != nil {
		t.Errorf("os.Stat(%s) = %v, want the tool directory to still exist without confirm", toolDir, err)
	}
}

func TestToolsListerDeleteToolConfirmedRemovesDirectory(t *testing.T) {
	dir := t.TempDir()
	toolDir := filepath.Join(dir, "greet")
	writeToolManifest(t, toolDir, "greet", "say hello")
	if err := tools.SaveState(toolDir, tools.ToolState{State: tools.StateVerified}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	l := NewToolsLister(dir, true)
	status, err := l.DeleteTool("greet", true)
	if err != nil {
		t.Fatalf("DeleteTool: %v", err)
	}
	if !strings.Contains(status, "borrada de forma permanente") {
		t.Errorf("status = %q, want it to report the successful deletion", status)
	}

	if _, err := os.Stat(toolDir); !os.IsNotExist(err) {
		t.Errorf("os.Stat(%s) error = %v, want os.IsNotExist after a confirmed delete", toolDir, err)
	}
}

func TestToolsListerDeleteToolNeverUsedStatusLine(t *testing.T) {
	dir := t.TempDir()
	toolDir := filepath.Join(dir, "greet")
	writeToolManifest(t, toolDir, "greet", "say hello")

	l := NewToolsLister(dir, true)
	status, err := l.DeleteTool("greet", false)
	if err != nil {
		t.Fatalf("DeleteTool: %v", err)
	}
	if !strings.Contains(status, "nunca usada") {
		t.Errorf("status = %q, want it to report never having been used", status)
	}
}

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichiTrader/ishakat/internal/evolve"
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

// --- EditTool (§13/§19.5): Step 21's own edit row ---
//
// EditTool delegates to a real tools.ToolEdit's own Run method (see
// toolsLister's own doc comment for why) so these tests are real
// filesystem round trips against t.TempDir() — the same discipline every
// other *ToolTool test in this file already follows — not mocks of
// tools.ToolEdit itself: the point of these tests is to confirm the
// wiring (NewToolsListerWithEgress threading allow/allowAll through,
// EditTool correctly marshaling its four arguments into toolEditArgs'
// own JSON shape, and this method's own Go-error-vs-string split) is
// correct, which a mock could not catch as reliably as a real
// tools.ToolEdit.Run call underneath.

func TestNewToolsListerWithEgressDisabledReturnsNil(t *testing.T) {
	if l := NewToolsListerWithEgress(t.TempDir(), false, []string{"example.com"}, false); l != nil {
		t.Errorf("NewToolsListerWithEgress(dir, false, ...) = %v, want nil", l)
	}
}

func TestNewToolsListerWithEgressEmptyDirReturnsNil(t *testing.T) {
	if l := NewToolsListerWithEgress("", true, nil, true); l != nil {
		t.Errorf("NewToolsListerWithEgress(\"\", true, ...) = %v, want nil", l)
	}
}

func TestNewToolsListerWithEgressEnabledWithDirReturnsNonNil(t *testing.T) {
	if l := NewToolsListerWithEgress(t.TempDir(), true, nil, true); l == nil {
		t.Error("NewToolsListerWithEgress(dir, true, ...) = nil, want a usable ToolsLister")
	}
}

func TestToolsListerEditToolEmptyNameIsGoError(t *testing.T) {
	l := NewToolsListerWithEgress(t.TempDir(), true, nil, true)
	if _, err := l.EditTool("", "a", "b", false); err == nil {
		t.Error("EditTool(\"\", ...) should error on an empty name")
	}
}

func TestToolsListerEditToolEmptyOldStringIsGoError(t *testing.T) {
	l := NewToolsListerWithEgress(t.TempDir(), true, nil, true)
	if _, err := l.EditTool("greet", "", "b", false); err == nil {
		t.Error("EditTool with an empty old_string should error")
	}
}

func TestToolsListerEditToolIdenticalOldNewIsGoError(t *testing.T) {
	l := NewToolsListerWithEgress(t.TempDir(), true, nil, true)
	if _, err := l.EditTool("greet", "same", "same", false); err == nil {
		t.Error("EditTool with old_string == new_string should error")
	}
}

func TestToolsListerEditToolUnknownNameIsErrorString(t *testing.T) {
	// Unlike the three Go-error preconditions above (checked by EditTool
	// itself before ever constructing a tools.ToolEdit), an unknown tool
	// name is tool_edit.go's own ErrorResult, not a Go error -- surfaced
	// here as EditTool's returned string, matching tui.ToolsLister's own
	// documented convention that only "could not even attempt it" is a
	// Go error on this interface.
	dir := t.TempDir()
	writeToolManifest(t, filepath.Join(dir, "greet"), "greet", "say hello")

	l := NewToolsListerWithEgress(dir, true, nil, true)
	status, err := l.EditTool("does_not_exist", "a", "b", false)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !strings.Contains(status, "no tool named") {
		t.Errorf("status = %q, want it to report the unknown tool", status)
	}
}

func TestToolsListerEditToolOldStringNotFoundIsErrorString(t *testing.T) {
	dir := t.TempDir()
	writeToolManifest(t, filepath.Join(dir, "greet"), "greet", "say hello")

	l := NewToolsListerWithEgress(dir, true, nil, true)
	status, err := l.EditTool("greet", "this text does not appear anywhere", "x", false)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !strings.Contains(status, "was not found") {
		t.Errorf("status = %q, want it to report old_string was not found", status)
	}
}

func TestToolsListerEditToolSuccessDemotesVerifiedToolToUnverified(t *testing.T) {
	dir := t.TempDir()
	toolDir := filepath.Join(dir, "fixable")
	writeToolManifest(t, toolDir, "fixable", "say hello")
	if err := tools.SaveState(toolDir, tools.ToolState{State: tools.StateVerified, UseCount: 3}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	l := NewToolsListerWithEgress(dir, true, nil, true)
	status, err := l.EditTool("fixable", "https://example.com/x", "https://example.com/right-path", false)
	if err != nil {
		t.Fatalf("EditTool: %v", err)
	}
	if !strings.Contains(status, "unverified") {
		t.Errorf("status = %q, want it to mention the tool is now unverified", status)
	}

	body, err := os.ReadFile(filepath.Join(toolDir, tools.ManifestFileName))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if !strings.Contains(string(body), "/right-path") {
		t.Errorf("manifest = %s, want the fix applied", body)
	}

	state, err := tools.LoadState(toolDir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if state.State != tools.StateUnverified {
		t.Errorf("state.State = %q, want %q after a successful edit (§19.5)", state.State, tools.StateUnverified)
	}
	if state.UseCount != 3 {
		t.Errorf("state.UseCount = %d, want preserved (3)", state.UseCount)
	}
}

func TestToolsListerEditToolReplaceAllReplacesEveryOccurrence(t *testing.T) {
	dir := t.TempDir()
	toolDir := filepath.Join(dir, "dup")
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	manifest := "name = \"dup\"\n" +
		"description = \"hello hello\"\n\n" +
		"[request]\n" +
		"method = \"GET\"\n" +
		"url = \"https://example.com/x\"\n"
	if err := os.WriteFile(filepath.Join(toolDir, tools.ManifestFileName), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	l := NewToolsListerWithEgress(dir, true, nil, true)
	status, err := l.EditTool("dup", "hello", "hi", true)
	if err != nil {
		t.Fatalf("EditTool: %v", err)
	}
	if !strings.Contains(status, "unverified") {
		t.Errorf("status = %q, want it to mention the tool is now unverified", status)
	}

	body, err := os.ReadFile(filepath.Join(toolDir, tools.ManifestFileName))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if strings.Contains(string(body), "hello") {
		t.Errorf("manifest still contains \"hello\" after replace_all: %s", body)
	}
	if !strings.Contains(string(body), "hi hi") {
		t.Errorf("manifest = %s, want both occurrences replaced", body)
	}
}

func TestToolsListerEditToolEgressAllowlistIsThreadedThrough(t *testing.T) {
	// Confirms NewToolsListerWithEgress's own allow/allowAll parameters
	// actually reach the real tools.ToolEdit this method constructs: an
	// edit that points the tool at a host outside allow (and allowAll
	// left false) must be refused, the same egress hard block
	// tool_edit_test.go's own TestToolEditUnallowlistedHostAfterEditIsResultErrorAndWritesNothing
	// already covers at the internal/tools layer -- this test exists to
	// confirm the wiring one layer up, in this package, threads the same
	// two values through rather than silently dropping them (e.g. by
	// defaulting to AllowAll: true regardless of what was passed in).
	dir := t.TempDir()
	toolDir := filepath.Join(dir, "reroute")
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	manifest := "name = \"reroute\"\n" +
		"description = \"say hello\"\n\n" +
		"[request]\n" +
		"method = \"GET\"\n" +
		"url = \"http://allowed.example.com/x\"\n"
	if err := os.WriteFile(filepath.Join(toolDir, tools.ManifestFileName), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	l := NewToolsListerWithEgress(dir, true, []string{"allowed.example.com"}, false)
	status, err := l.EditTool("reroute", "allowed.example.com", "not-allowed.example.com", false)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !strings.Contains(status, "egress allowlist") {
		t.Errorf("status = %q, want it to mention the egress allowlist", status)
	}

	body, err := os.ReadFile(filepath.Join(toolDir, tools.ManifestFileName))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if string(body) != manifest {
		t.Error("expected the on-disk manifest to be untouched after an egress-refused edit")
	}
}

// --- CreateTool (§13/§19.6): Step 21's own create row, the backlog's
// final slice ---
//
// CreateTool delegates to a real tools.ToolCreate's own Run method
// (see toolsLister's own doc comment for why) so these tests are real
// filesystem round trips against t.TempDir() -- the same discipline
// EditTool's own tests above already follow, and for the same reason:
// the point is to confirm the wiring (NewToolsListerWithEvolve
// threading allow/allowAll/thresholds through, CreateTool correctly
// marshaling its arguments into toolCreateArgs' own JSON shape, force's
// dual meaning as both Origin and SkipGate1, and this method's own
// Go-error-vs-string split) is correct, which a mock could not catch as
// reliably as a real tools.ToolCreate.Run call underneath.

func TestNewToolsListerWithEvolveDisabledReturnsNil(t *testing.T) {
	if l := NewToolsListerWithEvolve(t.TempDir(), false, nil, true, evolve.Thresholds{}); l != nil {
		t.Errorf("NewToolsListerWithEvolve(dir, false, ...) = %v, want nil", l)
	}
}

func TestNewToolsListerWithEvolveEmptyDirReturnsNil(t *testing.T) {
	if l := NewToolsListerWithEvolve("", true, nil, true, evolve.Thresholds{}); l != nil {
		t.Errorf("NewToolsListerWithEvolve(\"\", true, ...) = %v, want nil", l)
	}
}

func TestNewToolsListerWithEvolveEnabledWithDirReturnsNonNil(t *testing.T) {
	if l := NewToolsListerWithEvolve(t.TempDir(), true, nil, true, evolve.Thresholds{}); l == nil {
		t.Error("NewToolsListerWithEvolve(dir, true, ...) = nil, want a usable ToolsLister")
	}
}

func TestToolsListerCreateToolEmptyNameIsGoError(t *testing.T) {
	l := NewToolsListerWithEvolve(t.TempDir(), true, nil, true, evolve.Thresholds{})
	if _, err := l.CreateTool("", "d", "https://example.com/x", "GET", "reason", []string{}, false); err == nil {
		t.Error("CreateTool with an empty name should error")
	}
}

func TestToolsListerCreateToolEmptyDescriptionIsGoError(t *testing.T) {
	l := NewToolsListerWithEvolve(t.TempDir(), true, nil, true, evolve.Thresholds{})
	if _, err := l.CreateTool("greet", "", "https://example.com/x", "GET", "reason", []string{}, false); err == nil {
		t.Error("CreateTool with an empty description should error")
	}
}

func TestToolsListerCreateToolEmptyURLIsGoError(t *testing.T) {
	l := NewToolsListerWithEvolve(t.TempDir(), true, nil, true, evolve.Thresholds{})
	if _, err := l.CreateTool("greet", "d", "", "GET", "reason", []string{}, false); err == nil {
		t.Error("CreateTool with an empty url should error")
	}
}

func TestToolsListerCreateToolEmptyReasonIsGoError(t *testing.T) {
	l := NewToolsListerWithEvolve(t.TempDir(), true, nil, true, evolve.Thresholds{})
	if _, err := l.CreateTool("greet", "d", "https://example.com/x", "GET", "", []string{}, false); err == nil {
		t.Error("CreateTool with an empty reason should error (§19.8 mandatory provenance)")
	}
}

func TestToolsListerCreateToolSuccessWritesParseableManifest(t *testing.T) {
	dir := t.TempDir()
	l := NewToolsListerWithEvolve(dir, true, nil, true, evolve.Thresholds{})
	status, err := l.CreateTool("greet", "says hello", "https://example.com/greet", "GET", "needed for testing", []string{"unit test"}, false)
	if err != nil {
		t.Fatalf("CreateTool: %v", err)
	}
	if !strings.Contains(status, "greet") || !strings.Contains(status, "unverified") {
		t.Errorf("status = %q, want it to name the tool and report unverified", status)
	}

	discovered := tools.DiscoverDeclarative(dir)
	if len(discovered.Tools) != 1 {
		t.Fatalf("DiscoverDeclarative found %d tool(s), want 1", len(discovered.Tools))
	}
	m := discovered.Tools[0]
	if m.Name != "greet" || m.Description != "says hello" {
		t.Errorf("got %+v, want name=greet description=\"says hello\"", m)
	}
	if m.Origin.CreatedBy != "user" {
		t.Errorf("Origin.CreatedBy = %q, want \"user\" for a human-initiated creation", m.Origin.CreatedBy)
	}
	if m.Origin.Reason != "needed for testing" {
		t.Errorf("Origin.Reason = %q, want it unmodified for a non-forced creation", m.Origin.Reason)
	}

	toolDir := filepath.Join(dir, "greet")
	state, err := tools.LoadState(toolDir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if state.State != tools.StateUnverified {
		t.Errorf("state.State = %q, want %q for a never-probed tool (§19.5 rule 1)", state.State, tools.StateUnverified)
	}
}

func TestToolsListerCreateToolDuplicateDescriptionWithoutForceIsErrorString(t *testing.T) {
	// No-duplicate is always checked, regardless of origin (evolve.
	// Evaluate's own doc comment) -- an un-forced /tools create must
	// still be refused when its description is near-identical to an
	// already-existing tool's own, even though the human "declared"
	// this one on purpose (declaration only satisfies Repetition, not
	// Dedup).
	dir := t.TempDir()
	writeToolManifest(t, filepath.Join(dir, "existing_weather"), "existing_weather", "fetches the current weather for a given city")

	l := NewToolsListerWithEvolve(dir, true, nil, true, evolve.Thresholds{})
	status, err := l.CreateTool("new_weather", "fetches the current weather for a given city", "https://example.com/weather", "GET", "wanted a second one", []string{}, false)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !strings.Contains(status, "gate 1 refused") || !strings.Contains(status, "duplicate") {
		t.Errorf("status = %q, want a gate 1 duplicate refusal", status)
	}

	if _, err := os.Stat(filepath.Join(dir, "new_weather", tools.ManifestFileName)); !os.IsNotExist(err) {
		t.Error("expected no manifest to be written for a gate 1 refused creation")
	}
}

func TestToolsListerCreateToolForceBypassesGate1Duplicate(t *testing.T) {
	dir := t.TempDir()
	writeToolManifest(t, filepath.Join(dir, "existing_weather"), "existing_weather", "fetches the current weather for a given city")

	l := NewToolsListerWithEvolve(dir, true, nil, true, evolve.Thresholds{})
	status, err := l.CreateTool("new_weather", "fetches the current weather for a given city", "https://example.com/weather", "GET", "an operator typed --force", []string{}, true)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if strings.Contains(status, "gate 1 refused") {
		t.Errorf("status = %q, want force to bypass the duplicate refusal entirely", status)
	}
	if !strings.Contains(status, "unverified") {
		t.Errorf("status = %q, want it to report the new tool as unverified", status)
	}
}

func TestToolsListerCreateToolForcePrependsReasonMarker(t *testing.T) {
	dir := t.TempDir()
	l := NewToolsListerWithEvolve(dir, true, nil, true, evolve.Thresholds{})
	_, err := l.CreateTool("forced_tool", "d", "https://example.com/x", "GET", "an operator typed --force", []string{}, true)
	if err != nil {
		t.Fatalf("CreateTool: %v", err)
	}

	discovered := tools.DiscoverDeclarative(dir)
	if len(discovered.Tools) != 1 {
		t.Fatalf("DiscoverDeclarative found %d tool(s), want 1", len(discovered.Tools))
	}
	reason := discovered.Tools[0].Origin.Reason
	if !strings.Contains(reason, "--force: gate 1 skipped") {
		t.Errorf("Origin.Reason = %q, want it to carry the --force marker (\"and logs it\", §13)", reason)
	}
	if !strings.Contains(reason, "an operator typed --force") {
		t.Errorf("Origin.Reason = %q, want the original reason preserved after the marker", reason)
	}
}

func TestToolsListerCreateToolWithoutForceNoMarkerIsWritten(t *testing.T) {
	dir := t.TempDir()
	l := NewToolsListerWithEvolve(dir, true, nil, true, evolve.Thresholds{})
	_, err := l.CreateTool("unforced_tool", "d", "https://example.com/x", "GET", "an ordinary reason", []string{}, false)
	if err != nil {
		t.Fatalf("CreateTool: %v", err)
	}

	discovered := tools.DiscoverDeclarative(dir)
	if len(discovered.Tools) != 1 {
		t.Fatalf("DiscoverDeclarative found %d tool(s), want 1", len(discovered.Tools))
	}
	if reason := discovered.Tools[0].Origin.Reason; reason != "an ordinary reason" {
		t.Errorf("Origin.Reason = %q, want it unmodified without --force", reason)
	}
}

func TestToolsListerCreateToolEgressAllowlistIsThreadedThrough(t *testing.T) {
	// Mirrors TestToolsListerEditToolEgressAllowlistIsThreadedThrough
	// above: confirms NewToolsListerWithEvolve's own allow/allowAll
	// parameters actually reach the real tools.ToolCreate this method
	// constructs, and that this hard block applies regardless of
	// force -- §19.8's own mitigations are never skipped by SkipGate1
	// (ToolCreate.SkipGate1's own doc comment).
	dir := t.TempDir()
	l := NewToolsListerWithEvolve(dir, true, []string{"allowed.example.com"}, false, evolve.Thresholds{})

	status, err := l.CreateTool("blocked", "d", "http://not-allowed.example.com/x", "GET", "reason", []string{}, true)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !strings.Contains(status, "egress allowlist") {
		t.Errorf("status = %q, want it to mention the egress allowlist even with force", status)
	}
	if _, err := os.Stat(filepath.Join(dir, "blocked", tools.ManifestFileName)); !os.IsNotExist(err) {
		t.Error("expected no manifest to be written for an egress-refused creation")
	}
}

func TestToolsListerCreateToolCredentialPathIsHardBlockedEvenWithForce(t *testing.T) {
	dir := t.TempDir()
	l := NewToolsListerWithEvolve(dir, true, nil, true, evolve.Thresholds{})

	status, err := l.CreateTool("reads_ssh_key", "d", "http://example.com/read?path=~/.ssh/id_rsa", "GET", "reason", []string{}, true)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !strings.Contains(status, "credential-shaped path") {
		t.Errorf("status = %q, want it to mention the credential-shaped path hard block even with force", status)
	}
}

func TestToolsListerCreateToolThresholdsAreThreadedThrough(t *testing.T) {
	// Confirms NewToolsListerWithEvolve's own thresholds parameter
	// actually reaches gate 1: an empty tools directory still counts
	// nativeToolCatalog's own seven built-in tools against the budget
	// (tool_create.go's own doc comment on nativeToolCatalog), so a
	// MaxTools ceiling set at exactly that count refuses every
	// un-forced creation outright.
	dir := t.TempDir()
	l := NewToolsListerWithEvolve(dir, true, nil, true, evolve.Thresholds{MaxTools: 7})

	status, err := l.CreateTool("one_too_many", "d", "https://example.com/x", "GET", "reason", []string{}, false)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !strings.Contains(status, "gate 1 refused") || !strings.Contains(status, "budget") {
		t.Errorf("status = %q, want a gate 1 budget refusal at the configured MaxTools ceiling", status)
	}

	// The same ceiling, forced, must succeed -- confirming this is
	// really gate 1 (bypassed by SkipGate1), not some other check.
	status, err = l.CreateTool("one_too_many", "d", "https://example.com/x", "GET", "reason", []string{}, true)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if strings.Contains(status, "gate 1 refused") {
		t.Errorf("status = %q, want force to bypass the same budget refusal", status)
	}
}

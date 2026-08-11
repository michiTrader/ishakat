package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestToolDeleteNameDescriptionDanger(t *testing.T) {
	td := ToolDelete{}
	if td.Name() != "tool_delete" {
		t.Errorf("Name() = %q, want tool_delete", td.Name())
	}
	if td.Description() == "" {
		t.Error("Description() must not be empty")
	}
	if td.Danger() != DangerHigh {
		t.Errorf("Danger() = %v, want DangerHigh", td.Danger())
	}
}

func TestToolDeleteEmptyNameIsGoError(t *testing.T) {
	td := ToolDelete{Dir: t.TempDir()}
	_, err := td.Run(context.Background(), mustArgs(t, toolDeleteArgs{Name: "", Confirm: true}))
	if err == nil {
		t.Error("expected a Go error for an empty name")
	}
}

func TestToolDeleteCancelledContextIsGoError(t *testing.T) {
	td := ToolDelete{Dir: t.TempDir()}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := td.Run(ctx, mustArgs(t, toolDeleteArgs{Name: "anything", Confirm: true}))
	if err == nil {
		t.Error("expected a Go error for a cancelled context")
	}
}

func TestToolDeleteNoDirConfiguredIsResultError(t *testing.T) {
	td := ToolDelete{Dir: ""}
	res, err := td.Run(context.Background(), mustArgs(t, toolDeleteArgs{Name: "anything", Confirm: true}))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError when no tools directory is configured")
	}
}

func TestToolDeleteUnknownNameIsResultError(t *testing.T) {
	td := ToolDelete{Dir: t.TempDir()}
	res, err := td.Run(context.Background(), mustArgs(t, toolDeleteArgs{Name: "does_not_exist", Confirm: true}))
	if err != nil {
		t.Fatalf("an unknown tool name must be Result.IsError data, not a Go error: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected IsError for an unknown tool, got: %s", res.Text)
	}
}

// seedTool writes a minimal, parseable tool.toml (plus optional state.json
// via saveOpt) under dir/name, returning the tool's own directory for the
// caller's own follow-up assertions (e.g. checking it no longer exists).
func seedDeleteTool(t *testing.T, dir, name string, state *ToolState) string {
	t.Helper()
	toolDir := filepath.Join(dir, name)
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	manifest := `name = "` + name + `"
description = "a tool for testing tool_delete"

[request]
method = "GET"
url = "https://example.com/ping"
`
	if err := os.WriteFile(filepath.Join(toolDir, ManifestFileName), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if state != nil {
		if err := SaveState(toolDir, *state); err != nil {
			t.Fatalf("SaveState: %v", err)
		}
	}
	return toolDir
}

func TestToolDeleteWithoutConfirmRefusesAndWritesNothing(t *testing.T) {
	dir := t.TempDir()
	toolDir := seedDeleteTool(t, dir, "greet", &ToolState{State: StateVerified, UseCount: 5, LastUsed: "2026-08-01"})

	td := ToolDelete{Dir: dir}
	res, err := td.Run(context.Background(), mustArgs(t, toolDeleteArgs{Name: "greet", Confirm: false}))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError when confirm is false, got success: %s", res.Text)
	}
	if !strings.Contains(res.Text, "confirm") {
		t.Errorf("refusal message should mention confirm, got: %s", res.Text)
	}
	if !strings.Contains(res.Text, "verified") || !strings.Contains(res.Text, "5") {
		t.Errorf("refusal message should report current state/use_count, got: %s", res.Text)
	}
	if _, err := os.Stat(filepath.Join(toolDir, ManifestFileName)); err != nil {
		t.Errorf("manifest must still exist after a refused delete: %v", err)
	}
}

func TestToolDeleteConfirmOmittedDefaultsToRefused(t *testing.T) {
	dir := t.TempDir()
	seedDeleteTool(t, dir, "greet", nil)

	td := ToolDelete{Dir: dir}
	// Confirm zero-value (false) via a raw JSON object that omits the field
	// entirely -- confirming the "no safe reading, only the refusal" default
	// this file's own doc comment promises, not just Confirm:false spelled
	// out explicitly by the test.
	res, err := td.Run(context.Background(), []byte(`{"name":"greet"}`))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("omitting confirm must refuse, got success: %s", res.Text)
	}
}

func TestToolDeleteWithConfirmRemovesToolDirectory(t *testing.T) {
	dir := t.TempDir()
	toolDir := seedDeleteTool(t, dir, "greet", &ToolState{State: StateVerified, UseCount: 2, LastUsed: "2026-08-01"})

	td := ToolDelete{Dir: dir}
	res, err := td.Run(context.Background(), mustArgs(t, toolDeleteArgs{Name: "greet", Confirm: true}))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got error: %s", res.Text)
	}
	if !strings.Contains(res.Text, "deleted") {
		t.Errorf("success message should say it deleted the tool, got: %s", res.Text)
	}
	if _, err := os.Stat(toolDir); !os.IsNotExist(err) {
		t.Errorf("tool directory must be gone after a confirmed delete, stat err = %v", err)
	}
}

func TestToolDeleteSuccessMessageReportsPriorUsage(t *testing.T) {
	dir := t.TempDir()
	seedDeleteTool(t, dir, "greet", &ToolState{State: StateVerified, UseCount: 7, LastUsed: "2026-07-15"})

	td := ToolDelete{Dir: dir}
	res, err := td.Run(context.Background(), mustArgs(t, toolDeleteArgs{Name: "greet", Confirm: true}))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got error: %s", res.Text)
	}
	for _, want := range []string{"7", "2026-07-15"} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("success message should report prior usage %q, got: %s", want, res.Text)
		}
	}
}

func TestToolDeleteNeverUsedToolReportsNeverUsed(t *testing.T) {
	dir := t.TempDir()
	seedDeleteTool(t, dir, "greet", nil) // no state.json at all -- zero-value state

	td := ToolDelete{Dir: dir}
	res, err := td.Run(context.Background(), mustArgs(t, toolDeleteArgs{Name: "greet", Confirm: false}))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected refusal, got success: %s", res.Text)
	}
	if !strings.Contains(res.Text, "never used") {
		t.Errorf("expected 'never used' for a tool with no recorded state, got: %s", res.Text)
	}
}

func TestToolDeleteConfirmedDeletionOfUnverifiedToolAlsoWorks(t *testing.T) {
	// Confirm is the only gate this file enforces (see its own doc comment
	// on why an unconditional "in use" block is deliberately not one) --
	// deleting an unverified or broken tool must succeed exactly like a
	// verified one, once confirmed.
	dir := t.TempDir()
	toolDir := seedDeleteTool(t, dir, "broken_tool", &ToolState{State: StateBroken, UseCount: 3, FailCount: 2})

	td := ToolDelete{Dir: dir}
	res, err := td.Run(context.Background(), mustArgs(t, toolDeleteArgs{Name: "broken_tool", Confirm: true}))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success deleting a broken tool once confirmed, got error: %s", res.Text)
	}
	if _, err := os.Stat(toolDir); !os.IsNotExist(err) {
		t.Errorf("tool directory must be gone, stat err = %v", err)
	}
}

func TestToolDeleteRemovesSidecarStateFileToo(t *testing.T) {
	dir := t.TempDir()
	toolDir := seedDeleteTool(t, dir, "greet", &ToolState{State: StateVerified, UseCount: 1})
	statePath := filepath.Join(toolDir, StateFileName)
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("state.json should exist before delete: %v", err)
	}

	td := ToolDelete{Dir: dir}
	if _, err := td.Run(context.Background(), mustArgs(t, toolDeleteArgs{Name: "greet", Confirm: true})); err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Errorf("state.json must be gone too (whole directory removed), stat err = %v", err)
	}
}

func TestToolDeleteDoesNotAffectOtherTools(t *testing.T) {
	dir := t.TempDir()
	seedDeleteTool(t, dir, "keep_me", &ToolState{State: StateVerified, UseCount: 1})
	seedDeleteTool(t, dir, "delete_me", &ToolState{State: StateVerified, UseCount: 1})

	td := ToolDelete{Dir: dir}
	if _, err := td.Run(context.Background(), mustArgs(t, toolDeleteArgs{Name: "delete_me", Confirm: true})); err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "delete_me")); !os.IsNotExist(err) {
		t.Errorf("delete_me should be gone")
	}
	if _, err := os.Stat(filepath.Join(dir, "keep_me", ManifestFileName)); err != nil {
		t.Errorf("keep_me must be untouched: %v", err)
	}
}

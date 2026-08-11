package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seedArchiveTool mirrors tool_delete_test.go's own seedDeleteTool: a
// minimal, parseable tool.toml (plus an optional state.json) under
// dir/name, returning the tool's own directory.
func seedArchiveTool(t *testing.T, dir, name string, state *ToolState) string {
	t.Helper()
	toolDir := filepath.Join(dir, name)
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	manifest := `name = "` + name + `"
description = "a tool for testing tool_archive/tool_revive"

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

// -- ToolArchive --------------------------------------------------------

func TestToolArchiveNameDescriptionDanger(t *testing.T) {
	ta := ToolArchive{}
	if ta.Name() != "tool_archive" {
		t.Errorf("Name() = %q, want tool_archive", ta.Name())
	}
	if ta.Description() == "" {
		t.Error("Description() must not be empty")
	}
	if ta.Danger() != DangerLow {
		t.Errorf("Danger() = %v, want DangerLow", ta.Danger())
	}
}

func TestToolArchiveEmptyNameIsGoError(t *testing.T) {
	ta := ToolArchive{Dir: t.TempDir()}
	_, err := ta.Run(context.Background(), mustArgs(t, toolArchiveRevive{Name: ""}))
	if err == nil {
		t.Error("expected a Go error for an empty name")
	}
}

func TestToolArchiveCancelledContextIsGoError(t *testing.T) {
	ta := ToolArchive{Dir: t.TempDir()}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ta.Run(ctx, mustArgs(t, toolArchiveRevive{Name: "anything"}))
	if err == nil {
		t.Error("expected a Go error for a cancelled context")
	}
}

func TestToolArchiveNoDirConfiguredIsResultError(t *testing.T) {
	ta := ToolArchive{Dir: ""}
	res, err := ta.Run(context.Background(), mustArgs(t, toolArchiveRevive{Name: "anything"}))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError when no tools directory is configured")
	}
}

func TestToolArchiveUnknownNameIsResultError(t *testing.T) {
	ta := ToolArchive{Dir: t.TempDir()}
	res, err := ta.Run(context.Background(), mustArgs(t, toolArchiveRevive{Name: "does_not_exist"}))
	if err != nil {
		t.Fatalf("an unknown tool name must be Result.IsError data, not a Go error: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected IsError for an unknown tool, got: %s", res.Text)
	}
}

func TestToolArchiveVerifiedToolMovesToArchivedAndRemembersPrevious(t *testing.T) {
	dir := t.TempDir()
	toolDir := seedArchiveTool(t, dir, "greet", &ToolState{State: StateVerified, UseCount: 3, LastUsed: "2026-08-01"})

	ta := ToolArchive{Dir: dir}
	res, err := ta.Run(context.Background(), mustArgs(t, toolArchiveRevive{Name: "greet"}))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got error: %s", res.Text)
	}
	if !strings.Contains(res.Text, "archived") {
		t.Errorf("success message should say it archived the tool, got: %s", res.Text)
	}

	got, err := LoadState(toolDir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if got.State != StateArchived {
		t.Errorf("State = %v, want StateArchived", got.State)
	}
	if got.PreviousState != StateVerified {
		t.Errorf("PreviousState = %v, want StateVerified", got.PreviousState)
	}
	// Archiving must not touch usage bookkeeping -- only the lifecycle
	// state (and PreviousState) changes, matching this file's own doc
	// comment ("neither touches ... UseCount/LastUsed").
	if got.UseCount != 3 || got.LastUsed != "2026-08-01" {
		t.Errorf("archiving must not alter UseCount/LastUsed, got %+v", got)
	}
}

func TestToolArchiveBrokenToolRemembersBrokenAsPrevious(t *testing.T) {
	dir := t.TempDir()
	toolDir := seedArchiveTool(t, dir, "flaky", &ToolState{State: StateBroken, FailCount: 2})

	ta := ToolArchive{Dir: dir}
	if _, err := ta.Run(context.Background(), mustArgs(t, toolArchiveRevive{Name: "flaky"})); err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}

	got, err := LoadState(toolDir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if got.State != StateArchived {
		t.Errorf("State = %v, want StateArchived", got.State)
	}
	if got.PreviousState != StateBroken {
		t.Errorf("PreviousState = %v, want StateBroken", got.PreviousState)
	}
}

func TestToolArchiveAlreadyArchivedIsNoOp(t *testing.T) {
	dir := t.TempDir()
	toolDir := seedArchiveTool(t, dir, "greet", &ToolState{State: StateArchived, PreviousState: StateVerified})

	ta := ToolArchive{Dir: dir}
	res, err := ta.Run(context.Background(), mustArgs(t, toolArchiveRevive{Name: "greet"}))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.IsError {
		t.Fatalf("archiving an already-archived tool must succeed as a no-op, got error: %s", res.Text)
	}
	if !strings.Contains(res.Text, "already archived") {
		t.Errorf("expected a no-op message mentioning 'already archived', got: %s", res.Text)
	}

	got, err := LoadState(toolDir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if got.State != StateArchived || got.PreviousState != StateVerified {
		t.Errorf("state must be unchanged by a no-op archive, got %+v", got)
	}
}

func TestToolArchiveNeverProbedToolDefaultsFromUnverified(t *testing.T) {
	// No state.json at all -- the zero-value LoadState result, StateUnverified.
	dir := t.TempDir()
	toolDir := seedArchiveTool(t, dir, "fresh", nil)

	ta := ToolArchive{Dir: dir}
	res, err := ta.Run(context.Background(), mustArgs(t, toolArchiveRevive{Name: "fresh"}))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success archiving a never-probed tool, got error: %s", res.Text)
	}

	got, err := LoadState(toolDir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if got.State != StateArchived || got.PreviousState != StateUnverified {
		t.Errorf("expected archived with previous=unverified, got %+v", got)
	}
}

func TestToolArchiveDoesNotAffectOtherTools(t *testing.T) {
	dir := t.TempDir()
	seedArchiveTool(t, dir, "keep_me", &ToolState{State: StateVerified})
	seedArchiveTool(t, dir, "archive_me", &ToolState{State: StateVerified})

	ta := ToolArchive{Dir: dir}
	if _, err := ta.Run(context.Background(), mustArgs(t, toolArchiveRevive{Name: "archive_me"})); err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}

	keepState, err := LoadState(filepath.Join(dir, "keep_me"))
	if err != nil {
		t.Fatalf("LoadState(keep_me): %v", err)
	}
	if keepState.State != StateVerified {
		t.Errorf("keep_me must be untouched, got state=%v", keepState.State)
	}
}

// -- ToolRevive -----------------------------------------------------------

func TestToolReviveNameDescriptionDanger(t *testing.T) {
	tr := ToolRevive{}
	if tr.Name() != "tool_revive" {
		t.Errorf("Name() = %q, want tool_revive", tr.Name())
	}
	if tr.Description() == "" {
		t.Error("Description() must not be empty")
	}
	if tr.Danger() != DangerLow {
		t.Errorf("Danger() = %v, want DangerLow", tr.Danger())
	}
}

func TestToolReviveEmptyNameIsGoError(t *testing.T) {
	tr := ToolRevive{Dir: t.TempDir()}
	_, err := tr.Run(context.Background(), mustArgs(t, toolArchiveRevive{Name: ""}))
	if err == nil {
		t.Error("expected a Go error for an empty name")
	}
}

func TestToolReviveCancelledContextIsGoError(t *testing.T) {
	tr := ToolRevive{Dir: t.TempDir()}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := tr.Run(ctx, mustArgs(t, toolArchiveRevive{Name: "anything"}))
	if err == nil {
		t.Error("expected a Go error for a cancelled context")
	}
}

func TestToolReviveNoDirConfiguredIsResultError(t *testing.T) {
	tr := ToolRevive{Dir: ""}
	res, err := tr.Run(context.Background(), mustArgs(t, toolArchiveRevive{Name: "anything"}))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError when no tools directory is configured")
	}
}

func TestToolReviveUnknownNameIsResultError(t *testing.T) {
	tr := ToolRevive{Dir: t.TempDir()}
	res, err := tr.Run(context.Background(), mustArgs(t, toolArchiveRevive{Name: "does_not_exist"}))
	if err != nil {
		t.Fatalf("an unknown tool name must be Result.IsError data, not a Go error: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected IsError for an unknown tool, got: %s", res.Text)
	}
}

func TestToolReviveRestoresPreviousState(t *testing.T) {
	dir := t.TempDir()
	toolDir := seedArchiveTool(t, dir, "greet", &ToolState{
		State:         StateArchived,
		PreviousState: StateVerified,
		UseCount:      4,
		LastUsed:      "2026-07-20",
	})

	tr := ToolRevive{Dir: dir}
	res, err := tr.Run(context.Background(), mustArgs(t, toolArchiveRevive{Name: "greet"}))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got error: %s", res.Text)
	}
	if !strings.Contains(res.Text, "revived") || !strings.Contains(res.Text, "verified") {
		t.Errorf("success message should say it revived to verified, got: %s", res.Text)
	}

	got, err := LoadState(toolDir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if got.State != StateVerified {
		t.Errorf("State = %v, want StateVerified", got.State)
	}
	if got.PreviousState != "" {
		t.Errorf("PreviousState must be cleared after revive, got %v", got.PreviousState)
	}
	// Revive must not clobber usage bookkeeping either.
	if got.UseCount != 4 || got.LastUsed != "2026-07-20" {
		t.Errorf("revive must not alter UseCount/LastUsed, got %+v", got)
	}
}

func TestToolReviveWithEmptyPreviousStateFallsBackToVerified(t *testing.T) {
	// A hand-edited or otherwise malformed state.json with State=archived
	// but no PreviousState -- lifecycle.go's own Revive doc comment names
	// this as the defensive fallback case.
	dir := t.TempDir()
	toolDir := seedArchiveTool(t, dir, "weird", &ToolState{State: StateArchived})

	tr := ToolRevive{Dir: dir}
	if _, err := tr.Run(context.Background(), mustArgs(t, toolArchiveRevive{Name: "weird"})); err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}

	got, err := LoadState(toolDir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if got.State != StateVerified {
		t.Errorf("State = %v, want StateVerified fallback", got.State)
	}
}

func TestToolReviveNotArchivedIsNoOp(t *testing.T) {
	dir := t.TempDir()
	toolDir := seedArchiveTool(t, dir, "greet", &ToolState{State: StateVerified, UseCount: 1})

	tr := ToolRevive{Dir: dir}
	res, err := tr.Run(context.Background(), mustArgs(t, toolArchiveRevive{Name: "greet"}))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.IsError {
		t.Fatalf("reviving a non-archived tool must succeed as a no-op, got error: %s", res.Text)
	}
	if !strings.Contains(res.Text, "not archived") {
		t.Errorf("expected a no-op message mentioning 'not archived', got: %s", res.Text)
	}

	got, err := LoadState(toolDir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if got.State != StateVerified {
		t.Errorf("state must be unchanged by a no-op revive, got %+v", got)
	}
}

func TestToolReviveDoesNotAffectOtherTools(t *testing.T) {
	dir := t.TempDir()
	seedArchiveTool(t, dir, "keep_me", &ToolState{State: StateArchived, PreviousState: StateVerified})
	seedArchiveTool(t, dir, "revive_me", &ToolState{State: StateArchived, PreviousState: StateVerified})

	tr := ToolRevive{Dir: dir}
	if _, err := tr.Run(context.Background(), mustArgs(t, toolArchiveRevive{Name: "revive_me"})); err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}

	keepState, err := LoadState(filepath.Join(dir, "keep_me"))
	if err != nil {
		t.Fatalf("LoadState(keep_me): %v", err)
	}
	if keepState.State != StateArchived {
		t.Errorf("keep_me must be untouched, got state=%v", keepState.State)
	}
}

func TestToolArchiveThenToolReviveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	toolDir := seedArchiveTool(t, dir, "greet", &ToolState{State: StateVerified, UseCount: 9, LastUsed: "2026-08-05"})

	ta := ToolArchive{Dir: dir}
	if _, err := ta.Run(context.Background(), mustArgs(t, toolArchiveRevive{Name: "greet"})); err != nil {
		t.Fatalf("archive: unexpected Go error: %v", err)
	}
	mid, err := LoadState(toolDir)
	if err != nil {
		t.Fatalf("LoadState after archive: %v", err)
	}
	if mid.State != StateArchived {
		t.Fatalf("expected archived after tool_archive, got %v", mid.State)
	}

	tr := ToolRevive{Dir: dir}
	if _, err := tr.Run(context.Background(), mustArgs(t, toolArchiveRevive{Name: "greet"})); err != nil {
		t.Fatalf("revive: unexpected Go error: %v", err)
	}
	final, err := LoadState(toolDir)
	if err != nil {
		t.Fatalf("LoadState after revive: %v", err)
	}
	if final.State != StateVerified {
		t.Errorf("expected verified after the round trip, got %v", final.State)
	}
	if final.UseCount != 9 || final.LastUsed != "2026-08-05" {
		t.Errorf("round trip must preserve usage bookkeeping, got %+v", final)
	}
}

package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteFileCreatesNewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "new.txt")
	res, err := WriteFile{}.Run(context.Background(), mustArgs(t, writeFileArgs{Path: path, Content: "hello\n"}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Text)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "hello\n" {
		t.Errorf("content = %q, want %q", got, "hello\n")
	}
}

func TestWriteFileOverwritesExistingContentEntirely(t *testing.T) {
	path := writeTemp(t, "old content that is much longer than the new one\n")
	res, err := WriteFile{}.Run(context.Background(), mustArgs(t, writeFileArgs{Path: path, Content: "new\n"}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Text)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "new\n" {
		t.Errorf("content = %q, want %q (old content must be fully replaced, not appended to)", got, "new\n")
	}
}

func TestWriteFilePreservesExistingPermissions(t *testing.T) {
	path := writeTemp(t, "x")
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	res, err := WriteFile{}.Run(context.Background(), mustArgs(t, writeFileArgs{Path: path, Content: "y"}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Text)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Errorf("permissions = %o, want %o (must be preserved across the rename)", info.Mode().Perm(), 0o640)
	}
}

func TestWriteFileMissingParentDirIsResultError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent-subdir", "file.txt")
	res, err := WriteFile{}.Run(context.Background(), mustArgs(t, writeFileArgs{Path: path, Content: "x"}))
	if err != nil {
		t.Fatalf("a missing parent dir must be Result.IsError data, not a Go error: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected IsError for a missing parent directory, got: %s", res.Text)
	}
}

func TestWriteFileEmptyPathIsArgError(t *testing.T) {
	_, err := WriteFile{}.Run(context.Background(), mustArgs(t, writeFileArgs{Path: "", Content: "x"}))
	if err == nil {
		t.Error("expected an error for an empty path")
	}
}

func TestWriteFileNoTempFileLeftBehindOnSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clean.txt")
	wf := WriteFile{}
	if _, err := wf.Run(context.Background(), mustArgs(t, writeFileArgs{Path: path, Content: "z"})); err != nil {
		t.Fatalf("Run: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".ishakat-write-") {
			t.Errorf("temp file %s left behind after a successful write", e.Name())
		}
	}
	if len(entries) != 1 || entries[0].Name() != "clean.txt" {
		t.Errorf("directory should contain exactly clean.txt, got %v", entries)
	}
}

func TestWriteFileCancelledBeforeRenameLeavesOriginalUntouched(t *testing.T) {
	path := writeTemp(t, "original\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled before Run even starts
	_, err := WriteFile{}.Run(ctx, mustArgs(t, writeFileArgs{Path: path, Content: "should never land"}))
	if err == nil {
		t.Error("expected the cancelled context's error to surface")
	}
	got, rerr := os.ReadFile(path)
	if rerr != nil {
		t.Fatalf("ReadFile: %v", rerr)
	}
	if string(got) != "original\n" {
		t.Errorf("original content was modified despite cancellation: %q", got)
	}

	// No stray temp file left in the directory either.
	dir := filepath.Dir(path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".ishakat-write-") {
			t.Errorf("temp file %s left behind after cancellation", e.Name())
		}
	}
}

func TestWriteFileNameDescriptionDanger(t *testing.T) {
	wf := WriteFile{}
	if wf.Name() != "write_file" {
		t.Errorf("Name() = %q, want write_file", wf.Name())
	}
	if wf.Description() == "" {
		t.Error("Description() must not be empty")
	}
	if wf.Danger() != DangerMedium {
		t.Errorf("Danger() = %v, want DangerMedium", wf.Danger())
	}
}

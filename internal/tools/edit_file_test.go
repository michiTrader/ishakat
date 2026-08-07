package tools

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestEditFileReplacesUniqueMatch(t *testing.T) {
	path := writeTemp(t, "func foo() {\n\treturn 1\n}\n")
	res, err := EditFile{}.Run(context.Background(), mustArgs(t, editFileArgs{
		Path: path, OldString: "return 1", NewString: "return 2",
	}))
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
	if string(got) != "func foo() {\n\treturn 2\n}\n" {
		t.Errorf("content = %q", got)
	}
}

func TestEditFileNoMatchIsResultError(t *testing.T) {
	path := writeTemp(t, "hello world\n")
	res, err := EditFile{}.Run(context.Background(), mustArgs(t, editFileArgs{
		Path: path, OldString: "not present", NewString: "x",
	}))
	if err != nil {
		t.Fatalf("a missing match must be Result.IsError data, not a Go error: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected IsError for a missing match, got: %s", res.Text)
	}
	// The file must be untouched.
	got, _ := os.ReadFile(path)
	if string(got) != "hello world\n" {
		t.Errorf("file was modified despite no match: %q", got)
	}
}

func TestEditFileAmbiguousMatchWithoutReplaceAllIsResultError(t *testing.T) {
	path := writeTemp(t, "x = 1\nx = 1\n")
	res, err := EditFile{}.Run(context.Background(), mustArgs(t, editFileArgs{
		Path: path, OldString: "x = 1", NewString: "x = 2",
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected IsError for an ambiguous (2x) match without replace_all, got: %s", res.Text)
	}
	if !strings.Contains(res.Text, "2") {
		t.Errorf("expected the error to mention the match count, got: %s", res.Text)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "x = 1\nx = 1\n" {
		t.Errorf("file was modified despite an ambiguous match: %q", got)
	}
}

func TestEditFileReplaceAllReplacesEveryOccurrence(t *testing.T) {
	path := writeTemp(t, "a a a\n")
	res, err := EditFile{}.Run(context.Background(), mustArgs(t, editFileArgs{
		Path: path, OldString: "a", NewString: "b", ReplaceAll: true,
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Text)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "b b b\n" {
		t.Errorf("content = %q, want %q", got, "b b b\n")
	}
	if !strings.Contains(res.Text, "3") {
		t.Errorf("expected the result to mention the replacement count, got: %s", res.Text)
	}
}

func TestEditFileMissingFileIsResultError(t *testing.T) {
	res, err := EditFile{}.Run(context.Background(), mustArgs(t, editFileArgs{
		Path: "/nonexistent/path/x.txt", OldString: "a", NewString: "b",
	}))
	if err != nil {
		t.Fatalf("a missing file must be Result.IsError data, not a Go error: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected IsError for a missing file, got: %s", res.Text)
	}
}

func TestEditFileEmptyOldStringIsArgError(t *testing.T) {
	_, err := EditFile{}.Run(context.Background(), mustArgs(t, editFileArgs{
		Path: "x", OldString: "", NewString: "y",
	}))
	if err == nil {
		t.Error("expected an error for an empty old_string")
	}
}

func TestEditFileIdenticalOldAndNewIsArgError(t *testing.T) {
	_, err := EditFile{}.Run(context.Background(), mustArgs(t, editFileArgs{
		Path: "x", OldString: "same", NewString: "same",
	}))
	if err == nil {
		t.Error("expected an error when old_string equals new_string")
	}
}

func TestEditFilePreservesPermissions(t *testing.T) {
	path := writeTemp(t, "keep me\n")
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	res, err := EditFile{}.Run(context.Background(), mustArgs(t, editFileArgs{
		Path: path, OldString: "keep", NewString: "kept",
	}))
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
		t.Errorf("permissions = %o, want %o", info.Mode().Perm(), 0o640)
	}
}

func TestEditFileNoTempFileLeftBehindOnSuccess(t *testing.T) {
	path := writeTemp(t, "a\n")
	ef := EditFile{}
	if _, err := ef.Run(context.Background(), mustArgs(t, editFileArgs{Path: path, OldString: "a", NewString: "b"})); err != nil {
		t.Fatalf("Run: %v", err)
	}
	dir := strings.TrimSuffix(path, "sample.txt")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".ishakat-write-") {
			t.Errorf("temp file %s left behind after a successful edit", e.Name())
		}
	}
}

func TestEditFileCancelledBeforeWriteLeavesOriginalUntouched(t *testing.T) {
	path := writeTemp(t, "original\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := EditFile{}.Run(ctx, mustArgs(t, editFileArgs{
		Path: path, OldString: "original", NewString: "changed",
	}))
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
}

func TestEditFileNameDescriptionDanger(t *testing.T) {
	ef := EditFile{}
	if ef.Name() != "edit_file" {
		t.Errorf("Name() = %q, want edit_file", ef.Name())
	}
	if ef.Description() == "" {
		t.Error("Description() must not be empty")
	}
	if ef.Danger() != DangerMedium {
		t.Errorf("Danger() = %v, want DangerMedium", ef.Danger())
	}
}

package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.txt")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writeTemp: %v", err)
	}
	return path
}

func mustArgs(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("mustArgs: %v", err)
	}
	return b
}

func TestReadFileWholeFile(t *testing.T) {
	path := writeTemp(t, "one\ntwo\nthree\n")
	res, err := ReadFile{}.Run(context.Background(), mustArgs(t, readFileArgs{Path: path}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Text)
	}
	for _, want := range []string{"1\tone", "2\ttwo", "3\tthree"} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("output missing %q, got:\n%s", want, res.Text)
		}
	}
}

func TestReadFileOffsetAndLimit(t *testing.T) {
	path := writeTemp(t, "a\nb\nc\nd\ne\n")
	res, err := ReadFile{}.Run(context.Background(), mustArgs(t, readFileArgs{Path: path, Offset: 2, Limit: 2}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(res.Text, "\ta\n") || strings.Contains(res.Text, "\te\n") {
		t.Errorf("offset/limit did not exclude lines outside the window: %s", res.Text)
	}
	if !strings.Contains(res.Text, "2\tb") || !strings.Contains(res.Text, "3\tc") {
		t.Errorf("offset/limit window missing expected lines: %s", res.Text)
	}
}

func TestReadFileMissingPathIsResultError(t *testing.T) {
	res, err := ReadFile{}.Run(context.Background(), mustArgs(t, readFileArgs{Path: filepath.Join(t.TempDir(), "nope.txt")}))
	if err != nil {
		t.Fatalf("a missing file must be Result.IsError data, not a Go error: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected IsError for a missing file, got: %s", res.Text)
	}
}

func TestReadFileEmptyPathIsArgError(t *testing.T) {
	_, err := ReadFile{}.Run(context.Background(), mustArgs(t, readFileArgs{Path: ""}))
	if err == nil {
		t.Error("expected an error for an empty path (the call could not even be attempted)")
	}
}

func TestReadFileNegativeOffsetIsArgError(t *testing.T) {
	_, err := ReadFile{}.Run(context.Background(), mustArgs(t, readFileArgs{Path: "x", Offset: -1}))
	if err == nil {
		t.Error("expected an error for a negative offset")
	}
}

func TestReadFileOffsetPastEndIsResultError(t *testing.T) {
	path := writeTemp(t, "only one line\n")
	res, err := ReadFile{}.Run(context.Background(), mustArgs(t, readFileArgs{Path: path, Offset: 50}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected IsError for an offset past the end, got: %s", res.Text)
	}
}

func TestReadFileEmptyFile(t *testing.T) {
	path := writeTemp(t, "")
	res, err := ReadFile{}.Run(context.Background(), mustArgs(t, readFileArgs{Path: path}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError {
		t.Errorf("an empty file is not an error: %s", res.Text)
	}
	if !strings.Contains(res.Text, "empty") {
		t.Errorf("expected an 'empty' notice, got: %s", res.Text)
	}
}

func TestReadFileOnDirectoryIsResultError(t *testing.T) {
	dir := t.TempDir()
	res, err := ReadFile{}.Run(context.Background(), mustArgs(t, readFileArgs{Path: dir}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected IsError for a directory path, got: %s", res.Text)
	}
}

func TestReadFileOutputCeiling(t *testing.T) {
	// One line per byte-ish, enough lines to exceed maxReadFileBytes so the
	// truncation marker path is exercised rather than the whole file fitting.
	var b strings.Builder
	for i := 0; i < 6000; i++ {
		b.WriteString("this line has some padding to make it wider than one byte\n")
	}
	path := writeTemp(t, b.String())
	res, err := ReadFile{}.Run(context.Background(), mustArgs(t, readFileArgs{Path: path}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Text)
	}
	if len(res.Text) > maxReadFileBytes+512 { // marker adds a little overhead
		t.Errorf("output %d bytes exceeds the ceiling by more than the marker's own overhead", len(res.Text))
	}
	if !strings.Contains(res.Text, "truncated") {
		t.Errorf("expected a truncation marker, got tail: %s", res.Text[len(res.Text)-200:])
	}
}

func TestReadFileContextCancelled(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 1000; i++ {
		b.WriteString("line\n")
	}
	path := writeTemp(t, b.String())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ReadFile{}.Run(ctx, mustArgs(t, readFileArgs{Path: path}))
	if err == nil {
		t.Error("expected the cancelled context's error to surface")
	}
}

func TestReadFileNameDescriptionDanger(t *testing.T) {
	rf := ReadFile{}
	if rf.Name() != "read_file" {
		t.Errorf("Name() = %q, want read_file", rf.Name())
	}
	if rf.Description() == "" {
		t.Error("Description() must not be empty")
	}
	if rf.Danger() != DangerLow {
		t.Errorf("Danger() = %v, want DangerLow", rf.Danger())
	}
	var schema map[string]any
	if err := json.Unmarshal(rf.Parameters(), &schema); err != nil {
		t.Fatalf("Parameters() is not valid JSON: %v", err)
	}
	if schema["type"] != "object" {
		t.Errorf("schema type = %v, want object", schema["type"])
	}
}

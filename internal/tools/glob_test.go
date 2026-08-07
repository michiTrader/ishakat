package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", path, err)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

// writeRaw writes arbitrary bytes (e.g. containing a NUL byte, to simulate
// a binary file) without going through mustWriteFile's string content —
// used by grep_test.go's binary-file-skipping test.
func writeRaw(path string, data []byte) error {
	return os.WriteFile(path, data, 0o644)
}

func TestGlobMatchesFlatPattern(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "a.go"), "")
	mustWriteFile(t, filepath.Join(dir, "b.go"), "")
	mustWriteFile(t, filepath.Join(dir, "c.txt"), "")

	res, err := Glob{}.Run(context.Background(), mustArgs(t, globArgs{Pattern: "*.go", Path: dir}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Text)
	}
	if !strings.Contains(res.Text, "a.go") || !strings.Contains(res.Text, "b.go") {
		t.Errorf("expected both .go files, got: %s", res.Text)
	}
	if strings.Contains(res.Text, "c.txt") {
		t.Errorf("did not expect c.txt to match *.go, got: %s", res.Text)
	}
}

func TestGlobDoubleStarMatchesAcrossDirectories(t *testing.T) {
	dir := t.TempDir()
	mustMkdirAll(t, filepath.Join(dir, "internal", "tools"))
	mustMkdirAll(t, filepath.Join(dir, "internal", "engine"))
	mustWriteFile(t, filepath.Join(dir, "internal", "tools", "bash_test.go"), "")
	mustWriteFile(t, filepath.Join(dir, "internal", "engine", "agentloop_test.go"), "")
	mustWriteFile(t, filepath.Join(dir, "internal", "tools", "bash.go"), "")

	res, err := Glob{}.Run(context.Background(), mustArgs(t, globArgs{Pattern: "**/*_test.go", Path: dir}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Text)
	}
	if !strings.Contains(res.Text, "bash_test.go") || !strings.Contains(res.Text, "agentloop_test.go") {
		t.Errorf("expected both nested _test.go files, got: %s", res.Text)
	}
	if strings.Contains(res.Text, "bash.go\n") {
		t.Errorf("did not expect bash.go (no _test suffix) to match, got: %s", res.Text)
	}
}

func TestGlobDoubleStarMatchesZeroDirectories(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "top.go"), "")
	mustMkdirAll(t, filepath.Join(dir, "sub"))
	mustWriteFile(t, filepath.Join(dir, "sub", "nested.go"), "")

	res, err := Glob{}.Run(context.Background(), mustArgs(t, globArgs{Pattern: "**/*.go", Path: dir}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(res.Text, "top.go") {
		t.Errorf("expected ** to also match a file directly at the root (zero directories consumed), got: %s", res.Text)
	}
	if !strings.Contains(res.Text, "nested.go") {
		t.Errorf("expected ** to match a nested file too, got: %s", res.Text)
	}
}

func TestGlobNoMatchesIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "a.txt"), "")

	res, err := Glob{}.Run(context.Background(), mustArgs(t, globArgs{Pattern: "*.go", Path: dir}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError {
		t.Errorf("no matches is not an error condition, got IsError with: %s", res.Text)
	}
	if !strings.Contains(res.Text, "no files matched") {
		t.Errorf("expected a 'no files matched' notice, got: %s", res.Text)
	}
}

func TestGlobSortedByModTimeMostRecentFirst(t *testing.T) {
	dir := t.TempDir()
	older := filepath.Join(dir, "older.go")
	newer := filepath.Join(dir, "newer.go")
	mustWriteFile(t, older, "")
	time.Sleep(20 * time.Millisecond)
	mustWriteFile(t, newer, "")

	res, err := Glob{}.Run(context.Background(), mustArgs(t, globArgs{Pattern: "*.go", Path: dir}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	iNewer := strings.Index(res.Text, "newer.go")
	iOlder := strings.Index(res.Text, "older.go")
	if iNewer < 0 || iOlder < 0 {
		t.Fatalf("expected both files listed, got: %s", res.Text)
	}
	if iNewer > iOlder {
		t.Errorf("expected newer.go to be listed before older.go (most recent first), got: %s", res.Text)
	}
}

func TestGlobEmptyPatternIsArgError(t *testing.T) {
	_, err := Glob{}.Run(context.Background(), mustArgs(t, globArgs{Pattern: ""}))
	if err == nil {
		t.Error("expected an error for an empty pattern")
	}
}

func TestGlobNonexistentPathIsResultError(t *testing.T) {
	res, err := Glob{}.Run(context.Background(), mustArgs(t, globArgs{
		Pattern: "*.go",
		Path:    filepath.Join(t.TempDir(), "does-not-exist"),
	}))
	if err != nil {
		t.Fatalf("a bad path must be Result.IsError data, not a Go error: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected IsError for a nonexistent path, got: %s", res.Text)
	}
}

func TestGlobPathIsAFileIsResultError(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.txt")
	mustWriteFile(t, file, "")

	res, err := Glob{}.Run(context.Background(), mustArgs(t, globArgs{Pattern: "*.go", Path: file}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected IsError when path is a file, not a directory, got: %s", res.Text)
	}
}

func TestGlobContextCancelled(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 50; i++ {
		mustWriteFile(t, filepath.Join(dir, string(rune('a'+i%26))+string(rune('0'+i%10))+".go"), "")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Glob{}.Run(ctx, mustArgs(t, globArgs{Pattern: "*.go", Path: dir}))
	if err == nil {
		t.Error("expected the cancelled context's error to surface")
	}
}

func TestGlobMatchDoubleStarInMiddle(t *testing.T) {
	cases := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"src/**/*.ts", "src/a.ts", true},
		{"src/**/*.ts", "src/sub/a.ts", true},
		{"src/**/*.ts", "src/sub/deep/a.ts", true},
		{"src/**/*.ts", "other/a.ts", false},
		{"src/**/*.ts", "src/a.js", false},
		{"*.go", "sub/a.go", false},
		{"**", "anything/at/all.txt", true},
	}
	for _, c := range cases {
		got := globMatch(c.pattern, c.path)
		if got != c.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

func TestGlobNameDescriptionDanger(t *testing.T) {
	g := Glob{}
	if g.Name() != "glob" {
		t.Errorf("Name() = %q, want glob", g.Name())
	}
	if g.Description() == "" {
		t.Error("Description() must not be empty")
	}
	if g.Danger() != DangerLow {
		t.Errorf("Danger() = %v, want DangerLow", g.Danger())
	}
}

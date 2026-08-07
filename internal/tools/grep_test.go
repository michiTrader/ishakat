package tools

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGrepFindsMatchInSingleFile(t *testing.T) {
	path := writeTemp(t, "hello world\nfoo bar\nhello again\n")

	res, err := Grep{}.Run(context.Background(), mustArgs(t, grepArgs{Pattern: "hello", Path: path}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Text)
	}
	if !strings.Contains(res.Text, "1:hello world") {
		t.Errorf("expected line 1 match, got: %s", res.Text)
	}
	if !strings.Contains(res.Text, "3:hello again") {
		t.Errorf("expected line 3 match, got: %s", res.Text)
	}
	if strings.Contains(res.Text, "2:foo bar") {
		t.Errorf("did not expect line 2 (no match) to appear, got: %s", res.Text)
	}
}

func TestGrepRegexSyntax(t *testing.T) {
	path := writeTemp(t, "func Foo() {}\nfunc bar() {}\ntype Baz struct{}\n")

	res, err := Grep{}.Run(context.Background(), mustArgs(t, grepArgs{Pattern: `func [A-Z]\w*`, Path: path}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(res.Text, "func Foo") {
		t.Errorf("expected the capitalized func to match, got: %s", res.Text)
	}
	if strings.Contains(res.Text, "func bar") {
		t.Errorf("did not expect lowercase func to match, got: %s", res.Text)
	}
}

func TestGrepWalksDirectoryRecursively(t *testing.T) {
	dir := t.TempDir()
	mustMkdirAll(t, filepath.Join(dir, "sub"))
	mustWriteFile(t, filepath.Join(dir, "top.txt"), "needle here\n")
	mustWriteFile(t, filepath.Join(dir, "sub", "nested.txt"), "needle there too\n")
	mustWriteFile(t, filepath.Join(dir, "sub", "unrelated.txt"), "nothing to see\n")

	res, err := Grep{}.Run(context.Background(), mustArgs(t, grepArgs{Pattern: "needle", Path: dir}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(res.Text, "top.txt") || !strings.Contains(res.Text, "nested.txt") {
		t.Errorf("expected matches from both files, got: %s", res.Text)
	}
	if strings.Contains(res.Text, "unrelated.txt") {
		t.Errorf("did not expect the non-matching file listed, got: %s", res.Text)
	}
}

func TestGrepGlobRestrictsSearchedFiles(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "a.go"), "target\n")
	mustWriteFile(t, filepath.Join(dir, "a.txt"), "target\n")

	res, err := Grep{}.Run(context.Background(), mustArgs(t, grepArgs{Pattern: "target", Path: dir, Glob: "*.go"}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(res.Text, "a.go") {
		t.Errorf("expected a.go to match, got: %s", res.Text)
	}
	if strings.Contains(res.Text, "a.txt") {
		t.Errorf("expected a.txt to be excluded by glob=*.go, got: %s", res.Text)
	}
}

func TestGrepSkipsBinaryFiles(t *testing.T) {
	dir := t.TempDir()
	binPath := filepath.Join(dir, "bin.dat")
	if err := writeRaw(binPath, []byte("needle\x00binarystuff")); err != nil {
		t.Fatalf("writeRaw: %v", err)
	}
	mustWriteFile(t, filepath.Join(dir, "text.txt"), "needle in text\n")

	res, err := Grep{}.Run(context.Background(), mustArgs(t, grepArgs{Pattern: "needle", Path: dir}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(res.Text, "bin.dat") {
		t.Errorf("expected the binary file to be skipped, got: %s", res.Text)
	}
	if !strings.Contains(res.Text, "text.txt") {
		t.Errorf("expected the text file to match, got: %s", res.Text)
	}
}

func TestGrepNoMatchesIsNotAnError(t *testing.T) {
	path := writeTemp(t, "nothing relevant here\n")

	res, err := Grep{}.Run(context.Background(), mustArgs(t, grepArgs{Pattern: "zzz_not_present", Path: path}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError {
		t.Errorf("no matches is not an error condition, got IsError with: %s", res.Text)
	}
	if !strings.Contains(res.Text, "no matches") {
		t.Errorf("expected a 'no matches' notice, got: %s", res.Text)
	}
}

func TestGrepInvalidRegexIsArgError(t *testing.T) {
	_, err := Grep{}.Run(context.Background(), mustArgs(t, grepArgs{Pattern: "(unclosed"}))
	if err == nil {
		t.Error("expected an error for an invalid regex")
	}
}

func TestGrepEmptyPatternIsArgError(t *testing.T) {
	_, err := Grep{}.Run(context.Background(), mustArgs(t, grepArgs{Pattern: ""}))
	if err == nil {
		t.Error("expected an error for an empty pattern")
	}
}

func TestGrepNonexistentPathIsResultError(t *testing.T) {
	res, err := Grep{}.Run(context.Background(), mustArgs(t, grepArgs{
		Pattern: "x",
		Path:    filepath.Join(t.TempDir(), "does-not-exist"),
	}))
	if err != nil {
		t.Fatalf("a bad path must be Result.IsError data, not a Go error: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected IsError for a nonexistent path, got: %s", res.Text)
	}
}

func TestGrepSortedByFileModTimeMostRecentFirst(t *testing.T) {
	dir := t.TempDir()
	older := filepath.Join(dir, "older.txt")
	newer := filepath.Join(dir, "newer.txt")
	mustWriteFile(t, older, "needle\n")
	time.Sleep(20 * time.Millisecond)
	mustWriteFile(t, newer, "needle\n")

	res, err := Grep{}.Run(context.Background(), mustArgs(t, grepArgs{Pattern: "needle", Path: dir}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	iNewer := strings.Index(res.Text, "newer.txt")
	iOlder := strings.Index(res.Text, "older.txt")
	if iNewer < 0 || iOlder < 0 {
		t.Fatalf("expected both files listed, got: %s", res.Text)
	}
	if iNewer > iOlder {
		t.Errorf("expected newer.txt matches before older.txt (most recent first), got: %s", res.Text)
	}
}

func TestGrepContextCancelled(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 1000; i++ {
		b.WriteString("line without the needle\n")
	}
	path := writeTemp(t, b.String())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Grep{}.Run(ctx, mustArgs(t, grepArgs{Pattern: "x", Path: path}))
	if err == nil {
		t.Error("expected the cancelled context's error to surface")
	}
}

func TestGrepNameDescriptionDanger(t *testing.T) {
	g := Grep{}
	if g.Name() != "grep" {
		t.Errorf("Name() = %q, want grep", g.Name())
	}
	if g.Description() == "" {
		t.Error("Description() must not be empty")
	}
	if g.Danger() != DangerLow {
		t.Errorf("Danger() = %v, want DangerLow", g.Danger())
	}
}

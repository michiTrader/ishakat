package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// runGit is this test file's own tiny helper for building fixture
// repositories -- t.Fatal on any failure, since a fixture that failed to
// set up correctly would otherwise produce a confusing assertion failure
// further down instead of a clear one right here.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

func TestDetectGitNotARepo(t *testing.T) {
	dir := t.TempDir()
	info := DetectGit(dir)
	if info.InGit {
		t.Errorf("DetectGit(%q).InGit = true, expected false for a plain directory", dir)
	}
	if info.Clean {
		t.Errorf("DetectGit(%q).Clean = true, expected false (meaningless outside a repo)", dir)
	}
	if info.Branch != "" {
		t.Errorf("DetectGit(%q).Branch = %q, expected empty outside a repo", dir, info.Branch)
	}
}

func TestDetectGitCleanRepo(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	runGit(t, dir, "-c", "user.email=a@b.com", "-c", "user.name=test", "commit", "--allow-empty", "-q", "-m", "init")

	info := DetectGit(dir)
	if !info.InGit {
		t.Fatalf("DetectGit(%q).InGit = false, expected true for an initialized repository", dir)
	}
	if !info.Clean {
		t.Errorf("DetectGit(%q).Clean = false, expected true for a freshly committed repo with no changes", dir)
	}
	if info.Branch != "main" {
		t.Errorf("DetectGit(%q).Branch = %q, expected %q", dir, info.Branch, "main")
	}
}

func TestDetectGitDirtyRepo(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	runGit(t, dir, "-c", "user.email=a@b.com", "-c", "user.name=test", "commit", "--allow-empty", "-q", "-m", "init")
	if err := os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("could not write fixture file: %v", err)
	}

	info := DetectGit(dir)
	if !info.InGit {
		t.Fatalf("DetectGit(%q).InGit = false, expected true", dir)
	}
	if info.Clean {
		t.Errorf("DetectGit(%q).Clean = true, expected false with an untracked file present", dir)
	}
	if info.Branch != "main" {
		t.Errorf("DetectGit(%q).Branch = %q, expected %q", dir, info.Branch, "main")
	}
}

// TestDetectGitNoCommitsYet covers the repository-just-initialized case: no
// commit exists yet, so `git status --porcelain` sees nothing to report
// (there is nothing tracked or untracked) but `git branch --show-current`
// still names the not-yet-committed-to branch (init -b names it before the
// first commit ever lands). This is the exact state a human's very first
// `git init` in a brand new project leaves them in, and the dialog must not
// treat it as "not a repo" or crash on an empty status.
func TestDetectGitNoCommitsYet(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")

	info := DetectGit(dir)
	if !info.InGit {
		t.Fatalf("DetectGit(%q).InGit = false, expected true even with zero commits", dir)
	}
	if !info.Clean {
		t.Errorf("DetectGit(%q).Clean = false, expected true: an empty repo has nothing to report", dir)
	}
	if info.Branch != "main" {
		t.Errorf("DetectGit(%q).Branch = %q, expected %q", dir, info.Branch, "main")
	}
}

func TestDetectGitNestedDirInsideRepo(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init", "-q", "-b", "main")
	runGit(t, root, "-c", "user.email=a@b.com", "-c", "user.name=test", "commit", "--allow-empty", "-q", "-m", "init")
	nested := filepath.Join(root, "sub", "dir")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("could not create nested fixture dir: %v", err)
	}

	info := DetectGit(nested)
	if !info.InGit {
		t.Errorf("DetectGit(%q).InGit = false, expected true: git recognizes a work tree from any subdirectory", nested)
	}
	if info.Branch != "main" {
		t.Errorf("DetectGit(%q).Branch = %q, expected %q", nested, info.Branch, "main")
	}
}

func TestDetectGitMissingBinaryIsNotAnError(t *testing.T) {
	// Point PATH somewhere with no `git` at all, so exec.LookPath("git")
	// fails inside DetectGit — this is the "git itself is not installed"
	// case DetectGit's own doc comment promises never blocks the trust
	// question, only reports GitInfo{}.
	empty := t.TempDir()
	t.Setenv("PATH", empty)

	info := DetectGit(t.TempDir())
	if info.InGit {
		t.Errorf("DetectGit with no git on PATH: InGit = true, expected false")
	}
}

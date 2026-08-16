// gitstatus.go answers the one factual question §21.4 layer 2's own trust
// dialog mockup needs before it can draw its "git: yes · clean · branch
// main" line (docs/PLAN.md §21.4): is dir a git repository, is its worktree
// clean, and what branch is checked out. No package in this codebase
// answered this before Step 30 -- internal/tools/bash.go can *run* `git
// status` as an arbitrary command, but nothing parsed the answer into a
// structured fact a dialog could render.
//
// This lives in internal/app, not internal/tui, for the same §6.1 reason
// buildAgentOptions and every *Store bridge in this package do: shelling
// out is exactly the kind of filesystem/process knowledge the TUI must
// stay ignorant of (TestToolsNoImportaTUI's sibling rule for `internal/tui`
// itself), so Root receives an already-resolved GitInfo value, never a
// directory it would have to inspect on its own.
package app

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// GitInfo is what the trust dialog's own mockup line needs, and nothing
// more: whether dir is inside a git worktree at all, whether that worktree
// has anything to commit, and which branch is checked out. A struct rather
// than three separate return values because every call site (today, the
// trust dialog; conceivably later, a future status-line git indicator)
// wants all three together or none at all.
type GitInfo struct {
	// InGit is false for a plain directory with no repository -- the
	// dialog's mockup shows "git: no" and drops the other two fields
	// rather than a misleading "clean · branch " with nothing to say.
	InGit bool

	// Clean is true when `git status --porcelain` prints nothing:
	// no staged, unstaged or untracked changes. Meaningless when InGit
	// is false.
	Clean bool

	// Branch is `git branch --show-current`'s own output, trimmed.
	// Empty for a detached HEAD (that command deliberately prints
	// nothing in that case) -- the dialog then omits "· branch ..."
	// entirely rather than showing a blank name, the same
	// "supported empty means omit" convention FooterState.Autonomy's
	// own doc comment already establishes for this step.
	Branch string
}

// gitStatusTimeout bounds each of the two git invocations below. A
// dialog opening at startup must not hang the whole program because a
// project sits on a slow network filesystem or a repository with a
// pathological number of untracked files -- 2s matches netfix.go's own
// getprop timeout for the same "a diagnostic probe must fail fast, not
// hang" reasoning.
const gitStatusTimeout = 2 * time.Second

// DetectGit inspects dir and reports what §21.4 layer 2's dialog needs to
// show. It never returns an error: a directory that is not a git
// repository, a git binary that is missing, or a command that times out
// are all the identical, ordinary "nothing to report" case from this
// function's own point of view -- GitInfo{} (InGit false) -- because none
// of them should ever block the trust question itself from being asked.
// The one thing this function must never do is guess; an inconclusive
// probe reports InGit false rather than a fabricated "yes, clean, main".
func DetectGit(dir string) GitInfo {
	if _, err := exec.LookPath("git"); err != nil {
		return GitInfo{}
	}
	if !gitRun(dir, "rev-parse", "--is-inside-work-tree") {
		return GitInfo{}
	}

	branch, _ := gitOutput(dir, "branch", "--show-current")
	status, statusOK := gitOutput(dir, "status", "--porcelain")

	return GitInfo{
		InGit:  true,
		Clean:  statusOK && strings.TrimSpace(status) == "",
		Branch: strings.TrimSpace(branch),
	}
}

// gitRun runs a git subcommand in dir purely for its exit status --
// `rev-parse --is-inside-work-tree` prints "true"/"false" on success and
// fails outright outside any repository, so the exit code alone already
// answers "is dir in a worktree" without this caller needing to parse the
// stdout text at all.
func gitRun(dir string, args ...string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), gitStatusTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	return cmd.Run() == nil
}

// gitOutput runs a git subcommand in dir and returns its stdout, ok=false
// on any failure (missing binary already ruled out by DetectGit's own
// LookPath check, but a mid-repository corruption or a timeout still land
// here) -- the caller treats ok=false exactly like "nothing to report",
// never like a reason to fabricate a value.
func gitOutput(dir string, args ...string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), gitStatusTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return string(out), true
}

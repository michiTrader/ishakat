package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// maxGlobMatches caps how many paths a single glob call can return. A
// pattern like "**/*.go" (once expanded — see globArgs.Pattern's own doc
// comment on the ** rewrite) against a large tree could otherwise return
// tens of thousands of paths, which is not useful to a model and would blow
// straight through the output ceiling in one call anyway.
const maxGlobMatches = 500

// globArgs is glob's argument shape.
type globArgs struct {
	// Pattern is a filepath.Match-style pattern (§19.1: `path/filepath`, no
	// shelling out to find), with one extension: a "**" path segment means
	// "this directory and every directory beneath it", the de facto
	// standard glob convention filepath.Match itself does not support
	// (filepath.Match's own doc comment: "Match does not use PathSeparator
	// to indicate a segment boundary"). "**/*.go" is walked as
	// "*.go directly here, and *.go under every subdirectory" rather than
	// requiring the model to already know how deep the tree is.
	Pattern string `json:"pattern"`
	// Path is the directory the pattern is matched relative to. Defaults to
	// the current working directory.
	Path string `json:"path,omitempty"`
}

// Glob is the glob core tool (§19.1): find files by pattern. Pure Go
// (path/filepath, no shelling out to `find`), the same rationale grep.go's
// own doc comment states in full — Pi depends on external binaries being
// installed; ishakat must work identically on a freshly installed Termux
// with no `pkg install` at all.
type Glob struct{}

var _ Tool = Glob{}

func (Glob) Name() string   { return "glob" }
func (Glob) Danger() Danger { return DangerLow }
func (Glob) Description() string {
	return "Find files by glob pattern (e.g. \"*.go\", \"src/**/*.ts\"). \"**\" matches any number of directories, including zero. Returns matching paths sorted by modification time, most recent first."
}

func (Glob) Parameters() json.RawMessage {
	return objectSchema(map[string]prop{
		"pattern": {
			Type:        "string",
			Description: "Glob pattern to match, e.g. \"*.go\" or \"internal/**/*_test.go\". \"**\" matches any number of directories.",
		},
		"path": {
			Type:        "string",
			Description: "Directory the pattern is matched relative to. Omit to use the current working directory.",
		},
	}, "pattern")
}

func (Glob) Run(ctx context.Context, rawArgs json.RawMessage) (Result, error) {
	var args globArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return Result{}, fmt.Errorf("glob: invalid arguments: %w", err)
	}
	if args.Pattern == "" {
		return Result{}, fmt.Errorf("glob: pattern is required")
	}

	root := args.Path
	if root == "" {
		root = "."
	}
	info, err := os.Stat(root)
	if err != nil {
		return ErrorResult(fmt.Sprintf("could not access %s: %v", root, err)), nil
	}
	if !info.IsDir() {
		return ErrorResult(fmt.Sprintf("%s is not a directory", root)), nil
	}

	type match struct {
		path    string
		modTime time.Time
	}
	var matches []match

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			// An unreadable subtree (permissions, a broken symlink) is
			// skipped rather than aborting the whole walk — one bad
			// directory must not hide every match found elsewhere.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		if !globMatch(args.Pattern, rel) {
			return nil
		}
		fi, statErr := d.Info()
		var mt time.Time
		if statErr == nil {
			mt = fi.ModTime()
		}
		matches = append(matches, match{path: path, modTime: mt})
		return nil
	})
	if err != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		return ErrorResult(fmt.Sprintf("error walking %s: %v", root, err)), nil
	}

	sort.SliceStable(matches, func(i, j int) bool {
		return matches[i].modTime.After(matches[j].modTime)
	})

	truncated := false
	if len(matches) > maxGlobMatches {
		matches = matches[:maxGlobMatches]
		truncated = true
	}

	if len(matches) == 0 {
		return OKResult(fmt.Sprintf("no files matched %q under %s", args.Pattern, root)), nil
	}

	out := ""
	for _, m := range matches {
		out += m.path + "\n"
	}
	if truncated {
		out += fmt.Sprintf("\n…[truncated: %d matches is the ceiling — narrow the pattern or path]", maxGlobMatches)
	}
	return OKResult(out), nil
}

// globMatch reports whether rel (a path already relative to the search
// root, using forward slashes as filepath.ToSlash would produce) matches
// pattern, with "**" treated as "zero or more path segments" — the
// extension over filepath.Match documented on globArgs.Pattern.
//
// It works by splitting both pattern and rel into segments and matching
// segment by segment with backtracking on "**", the standard recursive-glob
// algorithm: a plain segment must match filepath.Match against the
// corresponding path segment, and "**" may consume any number of remaining
// path segments (including zero) before the rest of the pattern continues
// matching from wherever it stops. A pattern with no "**" anywhere degrades
// to an exact segment-count match, so "*.go" behaves exactly as
// filepath.Match already would.
func globMatch(pattern, rel string) bool {
	pattern = filepath.ToSlash(pattern)
	rel = filepath.ToSlash(rel)
	return matchSegments(splitSlash(pattern), splitSlash(rel))
}

func splitSlash(s string) []string {
	if s == "" {
		return nil
	}
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	parts = append(parts, s[start:])
	return parts
}

func matchSegments(pat, path []string) bool {
	if len(pat) == 0 {
		return len(path) == 0
	}
	if pat[0] == "**" {
		// "**" may consume 0..len(path) segments; try every split, shortest
		// consumption first so a pattern like "**/*.go" prefers matching a
		// file directly under root before descending, though either order
		// is correct — this only affects which recursive call returns
		// first, not the boolean result.
		for consume := 0; consume <= len(path); consume++ {
			if matchSegments(pat[1:], path[consume:]) {
				return true
			}
		}
		return false
	}
	if len(path) == 0 {
		return false
	}
	ok, err := filepath.Match(pat[0], path[0])
	if err != nil || !ok {
		return false
	}
	return matchSegments(pat[1:], path[1:])
}

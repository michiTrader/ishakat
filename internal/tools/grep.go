package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

// maxGrepMatches caps how many matching lines a single grep call reports,
// same rationale as maxGlobMatches: an unbounded regex over a large tree
// could return far more than is useful to a model or than the output
// ceiling would let through anyway.
const maxGrepMatches = 300

// maxGrepFileSize skips a file larger than this rather than reading it
// whole into memory line by line — the same shape of protection
// read_file's own maxReadFileBytes gives one file at a time, applied here
// per file across a whole tree so one huge binary blob or log file
// encountered mid-walk cannot dominate the call's time or memory.
const maxGrepFileSize = 8 << 20 // 8 MiB

// grepArgs is grep's argument shape.
type grepArgs struct {
	// Pattern is a Go regexp (RE2 syntax, via regexp — §19.1: no shelling
	// out to `grep`/`rg`).
	Pattern string `json:"pattern"`
	// Path is the file or directory to search. Defaults to the current
	// working directory. A directory is walked recursively.
	Path string `json:"path,omitempty"`
	// Glob restricts which files are searched when Path is a directory,
	// using the same pattern language as the glob tool (including "**").
	// Empty means every regular file that looks like text.
	Glob string `json:"glob,omitempty"`
}

// Grep is the grep core tool (§19.1): find content by regex. Pure Go
// (regexp, no shelling out to `grep`/`rg`) — the same Termux-portability
// rationale glob.go's own doc comment states in full.
type Grep struct{}

var _ Tool = Grep{}

func (Grep) Name() string   { return "grep" }
func (Grep) Danger() Danger { return DangerLow }
func (Grep) Description() string {
	return "Search file contents using a regular expression (RE2 syntax). Searches one file or recursively walks a directory. Returns matching lines as path:line:text, sorted by file modification time (most recent first)."
}

func (Grep) Parameters() json.RawMessage {
	return objectSchema(map[string]prop{
		"pattern": {
			Type:        "string",
			Description: "Regular expression to search for (RE2 syntax, as used by Go's regexp package).",
		},
		"path": {
			Type:        "string",
			Description: "File or directory to search. Omit to search the current working directory. A directory is walked recursively.",
		},
		"glob": {
			Type:        "string",
			Description: "Restrict the search to files matching this glob pattern when path is a directory, e.g. \"*.go\" or \"**/*.ts\". Omit to search every file that looks like text.",
		},
	}, "pattern")
}

func (Grep) Run(ctx context.Context, rawArgs json.RawMessage) (Result, error) {
	var args grepArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return Result{}, fmt.Errorf("grep: invalid arguments: %w", err)
	}
	if args.Pattern == "" {
		return Result{}, fmt.Errorf("grep: pattern is required")
	}
	re, err := regexp.Compile(args.Pattern)
	if err != nil {
		return Result{}, fmt.Errorf("grep: invalid pattern: %w", err)
	}

	root := args.Path
	if root == "" {
		root = "."
	}
	info, err := os.Stat(root)
	if err != nil {
		return ErrorResult(fmt.Sprintf("could not access %s: %v", root, err)), nil
	}

	type hit struct {
		file    string
		line    int
		text    string
		modTime int64
	}
	var hits []hit
	total := 0
	truncated := false

	searchFile := func(path string, modTime int64) error {
		if fi, statErr := os.Stat(path); statErr == nil && fi.Size() > maxGrepFileSize {
			return nil // too large — skipped, not an error for the whole call
		}
		f, openErr := os.Open(path)
		if openErr != nil {
			return nil // an unreadable file is skipped, same as glob's WalkDir error handling
		}
		defer f.Close()

		if looksBinary(f) {
			return nil
		}
		if _, err := f.Seek(0, 0); err != nil {
			return nil
		}

		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0, 4096), 1<<20) // 1 MiB max line, generous but bounded
		lineNo := 0
		for scanner.Scan() {
			if err := ctx.Err(); err != nil {
				return err
			}
			lineNo++
			if total >= maxGrepMatches {
				truncated = true
				return nil
			}
			if re.MatchString(scanner.Text()) {
				hits = append(hits, hit{file: path, line: lineNo, text: scanner.Text(), modTime: modTime})
				total++
			}
		}
		return nil
	}

	if !info.IsDir() {
		if err := searchFile(root, info.ModTime().UnixNano()); err != nil {
			return Result{}, err
		}
	} else {
		walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			if d.IsDir() {
				return nil
			}
			if truncated {
				return nil
			}
			if args.Glob != "" {
				rel, relErr := filepath.Rel(root, path)
				if relErr != nil || !globMatch(args.Glob, rel) {
					return nil
				}
			}
			fi, statErr := d.Info()
			var mt int64
			if statErr == nil {
				mt = fi.ModTime().UnixNano()
			}
			return searchFile(path, mt)
		})
		if walkErr != nil {
			if ctx.Err() != nil {
				return Result{}, ctx.Err()
			}
			return ErrorResult(fmt.Sprintf("error walking %s: %v", root, walkErr)), nil
		}
	}

	if len(hits) == 0 {
		return OKResult(fmt.Sprintf("no matches for %q in %s", args.Pattern, root)), nil
	}

	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].modTime != hits[j].modTime {
			return hits[i].modTime > hits[j].modTime
		}
		if hits[i].file != hits[j].file {
			return hits[i].file < hits[j].file
		}
		return hits[i].line < hits[j].line
	})

	out := ""
	for _, h := range hits {
		out += fmt.Sprintf("%s:%d:%s\n", h.file, h.line, h.text)
	}
	if truncated {
		out += fmt.Sprintf("\n…[truncated: %d matches is the ceiling — narrow the pattern, path or glob]", maxGrepMatches)
	}
	return OKResult(out), nil
}

// looksBinary sniffs the first 512 bytes of f (already open, positioned at
// the start) for a NUL byte, the same heuristic git and most grep
// implementations use to decide "this file is binary, don't search it" —
// searching a compiled binary or an image line by line produces useless,
// potentially huge single "lines" and matches that mean nothing.
func looksBinary(f *os.File) bool {
	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	for i := 0; i < n; i++ {
		if buf[i] == 0 {
			return true
		}
	}
	return false
}

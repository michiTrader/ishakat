package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
)

// maxReadFileBytes caps how much a single read_file call can return, using
// the same 32 KiB figure engine's own max_tool_output_bytes default uses
// (§12bis) — reading less than the ceiling that would truncate it anyway
// means read_file's own offset/limit parameters, not a silent mid-line cut,
// are what decide where a large file gets split across calls.
const maxReadFileBytes = 32 << 10

// readFileArgs is read_file's argument shape. Offset and Limit are 1-based
// line numbers, matching how a human reads a file listing (and how error
// messages and editors report line numbers) rather than a 0-based byte
// range, which is what edit_file's exact-string match makes irrelevant
// anyway.
type readFileArgs struct {
	Path   string `json:"path"`
	Offset int    `json:"offset,omitempty"` // 1-based; 0 or omitted means start at line 1
	Limit  int    `json:"limit,omitempty"`  // 0 or omitted means no limit (subject to maxReadFileBytes)
}

// ReadFile is the read_file core tool (§19.1): read a file's content, with
// optional offset/limit so a model can page through a file too large to see
// all at once instead of the tool truncating blindly.
type ReadFile struct{}

var _ Tool = ReadFile{}

func (ReadFile) Name() string   { return "read_file" }
func (ReadFile) Danger() Danger { return DangerLow }
func (ReadFile) Description() string {
	return "Read a text file's content, optionally starting at a given line (offset) and reading at most limit lines. Returns each line prefixed with its 1-based line number, the shape edit_file's error messages and a human reader both expect."
}

func (ReadFile) Parameters() json.RawMessage {
	return objectSchema(map[string]prop{
		"path": {
			Type:        "string",
			Description: "Path to the file to read, absolute or relative to the current working directory.",
		},
		"offset": {
			Type:        "integer",
			Description: "1-based line number to start reading from. Omit to start at line 1.",
		},
		"limit": {
			Type:        "integer",
			Description: "Maximum number of lines to return. Omit to read until the end of the file or the output ceiling, whichever comes first.",
		},
	}, "path")
}

func (ReadFile) Run(ctx context.Context, rawArgs json.RawMessage) (Result, error) {
	var args readFileArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return Result{}, fmt.Errorf("read_file: invalid arguments: %w", err)
	}
	if args.Path == "" {
		return Result{}, fmt.Errorf("read_file: path is required")
	}
	if args.Offset < 0 {
		return Result{}, fmt.Errorf("read_file: offset must not be negative")
	}
	if args.Limit < 0 {
		return Result{}, fmt.Errorf("read_file: limit must not be negative")
	}

	f, err := os.Open(args.Path)
	if err != nil {
		return ErrorResult(fmt.Sprintf("could not open %s: %v", args.Path, err)), nil
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return ErrorResult(fmt.Sprintf("could not stat %s: %v", args.Path, err)), nil
	}
	if info.IsDir() {
		return ErrorResult(fmt.Sprintf("%s is a directory, not a file", args.Path)), nil
	}

	start := args.Offset
	if start == 0 {
		start = 1
	}

	var out []byte
	lineNo := 0
	linesRead := 0
	sawAny := false
	scanner := bufio.NewScanner(f)
	// A line longer than bufio.Scanner's default 64 KiB (e.g. a minified JS
	// bundle on one line) must not abort the whole read — grow the buffer to
	// this tool's own byte ceiling, which already bounds the total output.
	scanner.Buffer(make([]byte, 0, 4096), maxReadFileBytes)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		lineNo++
		if lineNo < start {
			continue
		}
		if args.Limit > 0 && linesRead >= args.Limit {
			break
		}
		sawAny = true
		line := fmt.Sprintf("%6d\t%s\n", lineNo, scanner.Text())
		if len(out)+len(line) > maxReadFileBytes {
			out = append(out, []byte(fmt.Sprintf(
				"\n…[truncated: output ceiling of %d bytes reached at line %d — use offset to continue from here]",
				maxReadFileBytes, lineNo))...)
			return OKResult(string(out)), nil
		}
		out = append(out, line...)
		linesRead++
	}
	if err := scanner.Err(); err != nil {
		return ErrorResult(fmt.Sprintf("error reading %s: %v", args.Path, err)), nil
	}
	if !sawAny {
		if lineNo == 0 {
			return OKResult(fmt.Sprintf("%s is empty", args.Path)), nil
		}
		return ErrorResult(fmt.Sprintf(
			"offset %d is past the end of %s, which has %d line(s)", start, args.Path, lineNo)), nil
	}
	return OKResult(string(out)), nil
}

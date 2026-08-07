package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// editFileArgs is edit_file's argument shape, deliberately mirroring the
// MultiEdit-style "old_string/new_string" convention already familiar from
// this project's own tool-use surface (see this repo's own Edit tool
// description) rather than a line-range replacement — an exact-string match
// is robust to a file having changed by a line or two since the model last
// read it, where a line number would silently edit the wrong place.
type editFileArgs struct {
	Path       string `json:"path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
}

// EditFile is the edit_file core tool (§19.1): an exact-string replacement
// within an existing file. Danger: medium, same tier as write_file — both
// change local state but are scoped and undoable in principle (the model can
// always edit back).
//
// Like write_file, this satisfies §12bis's "never a half-written file"
// guarantee via write-temp-then-rename: EditFile.Run reads the whole file,
// performs the replacement in memory, and delegates the actual write to the
// exact same temp-file-then-atomic-rename path WriteFile.Run uses, so a
// cancellation between the read and the rename leaves the original on disk
// untouched — see writeStringAtomic's own doc comment, shared by both tools
// rather than duplicated.
type EditFile struct{}

var _ Tool = EditFile{}

func (EditFile) Name() string   { return "edit_file" }
func (EditFile) Danger() Danger { return DangerMedium }
func (EditFile) Description() string {
	return "Replace an exact, verbatim occurrence of old_string with new_string in an existing file. old_string must match the file's content byte for byte, including whitespace, and must be unique in the file unless replace_all is set — an ambiguous or missing match is reported as an error instead of guessing."
}

func (EditFile) Parameters() json.RawMessage {
	return objectSchema(map[string]prop{
		"path": {
			Type:        "string",
			Description: "Path to the file to edit. Must already exist.",
		},
		"old_string": {
			Type:        "string",
			Description: "The exact text to find and replace, including all whitespace and indentation exactly as it appears in the file.",
		},
		"new_string": {
			Type:        "string",
			Description: "The text to replace old_string with.",
		},
		"replace_all": {
			Type:        "boolean",
			Description: "Replace every occurrence of old_string instead of requiring exactly one. Defaults to false.",
		},
	}, "path", "old_string", "new_string")
}

func (EditFile) Run(ctx context.Context, rawArgs json.RawMessage) (Result, error) {
	var args editFileArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return Result{}, fmt.Errorf("edit_file: invalid arguments: %w", err)
	}
	if args.Path == "" {
		return Result{}, fmt.Errorf("edit_file: path is required")
	}
	if args.OldString == "" {
		return Result{}, fmt.Errorf("edit_file: old_string is required (use write_file to create a new file)")
	}
	if args.OldString == args.NewString {
		return Result{}, fmt.Errorf("edit_file: old_string and new_string are identical, nothing to do")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	raw, err := os.ReadFile(args.Path)
	if err != nil {
		return ErrorResult(fmt.Sprintf("could not read %s: %v", args.Path, err)), nil
	}
	content := string(raw)

	count := strings.Count(content, args.OldString)
	switch {
	case count == 0:
		return ErrorResult(fmt.Sprintf(
			"old_string was not found in %s — it must match the file's content exactly, including whitespace", args.Path)), nil
	case count > 1 && !args.ReplaceAll:
		return ErrorResult(fmt.Sprintf(
			"old_string matches %d locations in %s, not 1 — make it more specific (add surrounding context) or set replace_all", count, args.Path)), nil
	}

	var updated string
	if args.ReplaceAll {
		updated = strings.ReplaceAll(content, args.OldString, args.NewString)
	} else {
		updated = strings.Replace(content, args.OldString, args.NewString, 1)
	}

	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	// args.Path was just read successfully above, so it exists; Stat here is
	// only to recover its mode for writeStringAtomic to preserve, and a
	// failure to do that (e.g. removed between the read and here) falls back
	// to a sane default rather than aborting an edit that already has its
	// replacement computed.
	perm := defaultNewFilePerm
	if info, statErr := os.Stat(args.Path); statErr == nil {
		perm = info.Mode().Perm()
	}
	if err := writeStringAtomic(ctx, args.Path, updated, perm); err != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		return ErrorResult(fmt.Sprintf("could not save %s: %v", args.Path, err)), nil
	}

	if args.ReplaceAll {
		return OKResult(fmt.Sprintf("replaced %d occurrence(s) in %s", count, args.Path)), nil
	}
	return OKResult(fmt.Sprintf("replaced 1 occurrence in %s", args.Path)), nil
}

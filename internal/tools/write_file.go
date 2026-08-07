package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// defaultNewFilePerm is what a brand-new file gets when there is no
// existing target to preserve permissions from. Deliberately more
// restrictive than the umask-derived 0644 os.WriteFile would use — a tool a
// model calls should not create world/group-readable files by default.
const defaultNewFilePerm os.FileMode = 0o600

// writeFileArgs is write_file's argument shape.
type writeFileArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// WriteFile is the write_file core tool (§19.1): create or overwrite a file
// with the given content.
//
// §12bis's cancellation contract — "Never a half-written file: write_file/
// edit_file write to a temp file and rename, so cancellation cannot leave a
// truncated target" — is why Run below never calls os.WriteFile(args.Path,
// ...) directly. It delegates the actual write to writeStringAtomic
// (shared with EditFile), which writes to a sibling temp file and only
// renames it into place after the write and its fsync-equivalent flush have
// both succeeded. A ctx cancellation caught before the rename leaves the
// original file, if any, completely untouched.
type WriteFile struct{}

var _ Tool = WriteFile{}

func (WriteFile) Name() string   { return "write_file" }
func (WriteFile) Danger() Danger { return DangerMedium }
func (WriteFile) Description() string {
	return "Create a new file or overwrite an existing one with the given content. Parent directories are not created automatically — the path's directory must already exist."
}

func (WriteFile) Parameters() json.RawMessage {
	return objectSchema(map[string]prop{
		"path": {
			Type:        "string",
			Description: "Path to the file to write, absolute or relative to the current working directory.",
		},
		"content": {
			Type:        "string",
			Description: "The full content to write. Replaces the file's existing content entirely; this is not an append.",
		},
	}, "path", "content")
}

func (WriteFile) Run(ctx context.Context, rawArgs json.RawMessage) (Result, error) {
	var args writeFileArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return Result{}, fmt.Errorf("write_file: invalid arguments: %w", err)
	}
	if args.Path == "" {
		return Result{}, fmt.Errorf("write_file: path is required")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	dir := filepath.Dir(args.Path)
	if info, err := os.Stat(dir); err != nil {
		return ErrorResult(fmt.Sprintf("parent directory %s does not exist: %v", dir, err)), nil
	} else if !info.IsDir() {
		return ErrorResult(fmt.Sprintf("%s is not a directory", dir)), nil
	}

	// Preserve an existing target's permissions across the rename; a brand
	// new file gets defaultNewFilePerm instead of os.CreateTemp's own 0600
	// or the umask-derived 0644 os.WriteFile would use.
	perm := defaultNewFilePerm
	if info, statErr := os.Stat(args.Path); statErr == nil {
		perm = info.Mode().Perm()
	}

	if err := writeStringAtomic(ctx, args.Path, args.Content, perm); err != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		return ErrorResult(fmt.Sprintf("could not write to %s: %v", args.Path, err)), nil
	}
	return OKResult(fmt.Sprintf("wrote %d bytes to %s", len(args.Content), args.Path)), nil
}

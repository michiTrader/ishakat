package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

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
// ...) directly. It writes to a sibling temp file in the same directory
// (so the rename stays on one filesystem and is atomic on every platform
// this project targets — Linux, Darwin, Windows, Android/Termux) and only
// renames it into place after the write and its fsync-equivalent flush have
// both succeeded. A ctx cancellation caught before the rename leaves the
// original file, if any, completely untouched; the half-written data lives
// only in the temp file, which Run removes before returning.
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

	// os.CreateTemp in the target's own directory, not the OS-wide tmp dir:
	// os.Rename requires both paths on the same filesystem to be atomic, and
	// a phone's /tmp can be a different mount than the app's data directory.
	tmp, err := os.CreateTemp(dir, ".ishakat-write-*")
	if err != nil {
		return ErrorResult(fmt.Sprintf("could not create a temp file in %s: %v", dir, err)), nil
	}
	tmpPath := tmp.Name()
	// If Run returns before the rename below (an error, or ctx cancelled),
	// the temp file must not linger — os.Remove after a successful rename is
	// a harmless no-op (ErrNotExist), which is exactly the shape a deferred
	// cleanup that "might already have happened" should take.
	defer os.Remove(tmpPath)

	_, writeErr := tmp.WriteString(args.Content)
	syncErr := tmp.Sync()
	closeErr := tmp.Close()
	if writeErr != nil {
		return ErrorResult(fmt.Sprintf("could not write to %s: %v", args.Path, writeErr)), nil
	}
	if syncErr != nil {
		return ErrorResult(fmt.Sprintf("could not flush %s to disk: %v", args.Path, syncErr)), nil
	}
	if closeErr != nil {
		return ErrorResult(fmt.Sprintf("could not close temp file for %s: %v", args.Path, closeErr)), nil
	}

	// Preserve the target's existing permissions across the rename, if it
	// already exists. os.CreateTemp creates its file at 0600; a new file's
	// permissions default to that instead — deliberately restrictive rather
	// than the umask-derived 0644 os.WriteFile would use, since a tool a
	// model calls should not create world/group-readable files by default.
	if info, statErr := os.Stat(args.Path); statErr == nil {
		if err := os.Chmod(tmpPath, info.Mode().Perm()); err != nil {
			return ErrorResult(fmt.Sprintf("could not preserve permissions on %s: %v", args.Path, err)), nil
		}
	}

	if err := ctx.Err(); err != nil {
		// Cancelled after the write but before the rename: the temp file is
		// cleaned up by the deferred os.Remove above, and the original
		// target (if any) was never touched.
		return Result{}, err
	}

	if err := os.Rename(tmpPath, args.Path); err != nil {
		return ErrorResult(fmt.Sprintf("could not finalize write to %s: %v", args.Path, err)), nil
	}
	return OKResult(fmt.Sprintf("wrote %d bytes to %s", len(args.Content), args.Path)), nil
}

package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// writeStringAtomic writes content to path via a sibling temp file and an
// atomic rename, shared by WriteFile and EditFile so §12bis's guarantee —
// "Never a half-written file: write_file/edit_file write to a temp file and
// rename, so cancellation cannot leave a truncated target" — is implemented
// once, not twice.
//
// The temp file is created in path's own directory (os.CreateTemp there,
// not the OS-wide tmp dir), because os.Rename requires both paths to be on
// the same filesystem to be atomic — a phone's /tmp can be a different
// mount than the app's data directory. perm is applied to the temp file
// before the rename, so the caller decides what permissions the target ends
// up with (WriteFile preserves an existing target's mode, or falls back to
// os.CreateTemp's own restrictive 0600 default for a brand new file, rather
// than the umask-derived 0644 os.WriteFile would use); EditFile always has
// an existing target and preserves its mode.
//
// A ctx cancellation observed before the rename returns ctx.Err() with the
// temp file already cleaned up (deferred os.Remove, a harmless no-op if the
// rename already happened) and the target, if it existed, completely
// untouched — the half-written data lives only in the removed temp file.
func writeStringAtomic(ctx context.Context, path, content string, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".ishakat-write-*")
	if err != nil {
		return fmt.Errorf("could not create a temp file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	_, writeErr := tmp.WriteString(content)
	syncErr := tmp.Sync()
	closeErr := tmp.Close()
	if writeErr != nil {
		return writeErr
	}
	if syncErr != nil {
		return syncErr
	}
	if closeErr != nil {
		return closeErr
	}
	if perm != 0 {
		if err := os.Chmod(tmpPath, perm); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

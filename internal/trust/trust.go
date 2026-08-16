// Package trust implements §21.4 layer 2: a single, persisted decision per
// project folder -- Pi's own /trust, adapted (docs/PLAN.md §21.2/§21.4).
// On the first interactive run in a directory with no saved decision, the
// human answers exactly one question ("how should I work here?"); the
// answer is stored here, keyed by the project's absolute path, and never
// asked again for that path -- nor for anything underneath it, since "a
// parent-directory decision covers children" is Pi's own rule and §21.4
// states it verbatim.
//
// This package deliberately stays independent of internal/config,
// internal/xdg and internal/permissions, the same boundary
// internal/evolve/ledger.go already draws for a sibling on-disk store: a
// caller supplies the file path as a plain string (xdg.TrustFile() in
// practice) and the config.Permissions-shaped autonomy string
// (permissions.Autonomy.String() in practice) rather than this package
// importing either. Store.Lookup returns the raw string a caller then
// hands to permissions.ParseAutonomy -- this package has no opinion on
// what a valid autonomy value is, only on which path answers which
// question.
package trust

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Record is one persisted trust decision: which autonomy the human chose
// for the project at Path (already filepath.Clean'd, absolute), and when.
// Autonomy is stored as the plain string a permissions.Autonomy.String()
// call already produces, rather than this package importing
// internal/permissions for a type it has no other use for -- the same
// reasoning bashObserveArgs (internal/app/ledger.go) already gives for a
// sibling boundary.
type Record struct {
	Path      string `json:"path"`
	Autonomy  string `json:"autonomy"`
	DecidedAt string `json:"decided_at"`
}

// Store is the in-memory form of trust.json: one Record per project path
// that has ever answered the layer 2 question, in the order they were
// added -- append-only from the reader's own point of view, except that
// Set moves an updated record to the end, the same recency-ordering
// convention internal/evolve.Ledger.Observe already applies for the same
// reason (a human re-deciding a project they had already trusted is the
// most likely record to be looked at next).
type Store struct {
	Records []Record
}

// Load reads path's JSON-lines content into a Store. A missing file is not
// an error -- the ordinary case for a project that has never been asked --
// and returns an empty Store, matching LoadLedger's own "absence is not
// failure" contract for a sibling on-disk, optional store. A line that
// fails to parse as one Record is skipped rather than failing the whole
// load, for the identical reason: a corrupted line should not cost every
// other project's already-answered decision.
func Load(path string) (*Store, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Store{}, nil
		}
		return nil, fmt.Errorf("could not open %s: %w", path, err)
	}
	defer f.Close()

	var s Store
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec Record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		s.Records = append(s.Records, rec)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("could not read %s: %w", path, err)
	}
	return &s, nil
}

// Save writes s back to path as one JSON object per line, atomically (a
// sibling temp file plus rename), mirroring internal/evolve.Save's own
// shape exactly -- see that function's doc comment for why this package
// does not instead import internal/tools for one shared helper.
func Save(path string, s *Store) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("could not create %s: %w", dir, err)
	}

	var b strings.Builder
	for _, rec := range s.Records {
		line, err := json.Marshal(rec)
		if err != nil {
			return fmt.Errorf("could not encode record %q: %w", rec.Path, err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}

	tmp, err := os.CreateTemp(dir, ".ishakat-trust-*")
	if err != nil {
		return fmt.Errorf("could not create a temp file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	_, writeErr := tmp.WriteString(b.String())
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
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// cleanPath normalizes dir the same way for every Lookup/Set call, so a
// path reaching this package with or without a trailing separator, or with
// ".." segments, keys identically. It does not resolve symlinks
// (filepath.EvalSymlinks) on purpose: the project root a human trusted is
// the path they typed or ishakat resolved via os.Getwd, and resolving
// through a symlink could silently answer for a different, unreviewed
// directory the link happens to point at.
func cleanPath(dir string) string {
	return filepath.Clean(dir)
}

// Lookup returns the Record governing dir: the record for dir itself if it
// has exactly answered, or, failing that, the nearest ancestor directory
// that has -- "a parent-directory decision covers children" (§21.4 layer
// 2, Pi's own rule, restated verbatim in this package's own doc comment).
// ok is false when neither dir nor any ancestor up to the filesystem root
// has ever answered, which is the caller's own signal to show the trust
// dialog.
func (s *Store) Lookup(dir string) (Record, bool) {
	byPath := make(map[string]Record, len(s.Records))
	for _, rec := range s.Records {
		byPath[rec.Path] = rec
	}

	cur := cleanPath(dir)
	for {
		if rec, ok := byPath[cur]; ok {
			return rec, true
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			// Reached the filesystem root (or a relative path's own
			// fixed point, e.g. "." -- filepath.Dir(".") == ".") without
			// a match anywhere in the chain.
			return Record{}, false
		}
		cur = parent
	}
}

// Set inserts or updates the Record for path with autonomy, decided at at
// (UTC, RFC 3339 -- the same wall-clock precision internal/oauth's token
// expiry already uses elsewhere in this codebase). An existing record for
// the exact same cleaned path is replaced in place and moved to the end,
// matching Ledger.Observe's own "recency order" convention; a path with no
// existing record is appended. now is injectable the same way
// ledgerObservingRunner's own now parameter is (internal/app/ledger.go),
// so a test does not depend on the real clock.
func (s *Store) Set(path, autonomy string, at time.Time) {
	clean := cleanPath(path)
	rec := Record{Path: clean, Autonomy: autonomy, DecidedAt: at.UTC().Format(time.RFC3339)}
	for i, existing := range s.Records {
		if existing.Path == clean {
			s.Records = append(s.Records[:i], s.Records[i+1:]...)
			break
		}
	}
	s.Records = append(s.Records, rec)
}

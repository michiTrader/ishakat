// Package curation implements docs/DESIGN-model-curation.md's Layer 2
// persistence: the user's own per-model hide/keep decisions, made one
// keystroke at a time from the picker (ctrl+x/ctrl+h, F5/F11's own
// "interactive hide/keep" ask), or via /model hide|keep.
//
// This is deliberately its own file, its own package, and its own on-disk
// store — never config.toml — for the design doc's §2.2 reason, which is
// load-bearing enough to restate here: BurntSushi/toml's encoder cannot
// preserve config.toml's own hand-written comments (SaveProviderConnection
// decodes into map[string]any and re-encodes from it), so a key pressed
// casually inside the picker must never risk stripping the prose that
// makes config.toml readable. Interactive state goes to its own
// machine-written file instead, following internal/trust's exact shape
// (Store/Load/Save, one JSON document, atomic write) — the same "separate
// package, no dependency on internal/config or internal/xdg" boundary that
// package's own doc comment draws for a sibling on-disk store, so a caller
// supplies the file path as a plain string (xdg.StateFile-shaped, in
// practice) rather than this package importing either.
package curation

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Version is the "v" field of the on-disk file (design doc §2.2's own
// example JSON). A file written by a future, incompatible version is
// discarded rather than migrated — the same "cost one redo, avoid a whole
// class of decoding bugs" trade catalog.CacheVersion already makes.
const Version = 1

// Entry is one hidden or kept model: which ref, and when the decision was
// made. At is RFC3339 UTC, matching trust.Record.DecidedAt's own
// convention.
type Entry struct {
	Ref string `json:"ref"`
	At  string `json:"at"`
}

// Store is the in-memory form of curation.json: the user's own hide/keep
// decisions, keyed by ref (case-insensitively — see refKey). A ref that
// appears in both lists is a contradiction Set/Hide/Keep themselves never
// produce (each removes the ref from the other list before adding it), so
// Store's own invariant is "a ref is in at most one of Hidden/Kept at a
// time" — Kept still wins over any hide rule at the catalog.Rules level
// regardless, per design doc §2.2's precedence table, but that resolution
// happens one layer up (internal/catalog.Rules), not here.
type Store struct {
	Hidden []Entry `json:"hidden,omitempty"`
	Kept   []Entry `json:"kept,omitempty"`

	// Note explains why an existing file was ignored (unreadable,
	// corrupt, wrong version) — never swallowed, the same contract
	// catalog.Cache.Note already gives its own callers, and Layer 2's
	// own closing criterion (design doc §2.3): "curation.json missing,
	// empty, corrupt, or carrying a future v degrades to 'nothing
	// hidden' plus a note — never a startup failure."
	Note string `json:"-"`
}

// refKey normalizes a ref for lookup/dedup the same way
// catalog.Catalog.Get already does (strings.ToLower(strings.TrimSpace(ref))),
// so a curation decision and a catalog lookup never disagree about whether
// two refs are "the same one" by case or incidental whitespace alone.
func refKey(ref string) string {
	return strings.ToLower(strings.TrimSpace(ref))
}

// Load reads path's JSON content into a Store. A missing file is not an
// error — the ordinary case for a user who has never pressed ctrl+x — and
// returns an empty, usable Store. A corrupt file, or one written by a
// version this build does not understand, degrades to an empty Store with
// Note explaining why, per this package's own closing criterion above;
// only a genuinely unexpected I/O error (permission denied on an existing
// file, say) is returned as an error, mirroring catalog.LoadCache's own
// "programmer/environment mistake vs. ordinary absence" split.
func Load(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return &Store{Note: "no curation path configured"}, nil
	}

	raw, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		return &Store{}, nil
	case err != nil:
		return &Store{Note: fmt.Sprintf("curation.json unreadable (%v); nothing hidden", err)}, nil
	}

	var doc struct {
		V      int     `json:"v"`
		Hidden []Entry `json:"hidden,omitempty"`
		Kept   []Entry `json:"kept,omitempty"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return &Store{Note: fmt.Sprintf("curation.json corrupt (%v); nothing hidden", err)}, nil
	}
	if doc.V != Version {
		return &Store{Note: fmt.Sprintf("curation.json version %d, this build writes %d; nothing hidden", doc.V, Version)}, nil
	}

	return &Store{Hidden: doc.Hidden, Kept: doc.Kept}, nil
}

// Save writes s to path atomically (a sibling temp file plus rename,
// 0600), mirroring trust.Save/catalog.Cache.Save's identical shape — a
// picker key press must not risk a torn file on a mid-write interruption
// any more than either of those two on-disk stores do.
func Save(path string, s *Store) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("curation: no path to save to")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("curation: could not create %s: %w", dir, err)
	}

	doc := struct {
		V      int     `json:"v"`
		Hidden []Entry `json:"hidden,omitempty"`
		Kept   []Entry `json:"kept,omitempty"`
	}{V: Version, Hidden: s.Hidden, Kept: s.Kept}

	body, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("curation: could not serialize: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".curation-*.tmp")
	if err != nil {
		return fmt.Errorf("curation: could not create a temp file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return fmt.Errorf("curation: could not write %s: %w", tmpPath, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("curation: could not flush %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("curation: could not close %s: %w", tmpPath, err)
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		return fmt.Errorf("curation: could not set permissions on %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("curation: could not replace %s: %w", path, err)
	}
	return nil
}

// IsHidden reports whether ref is in the user's own hide list.
func (s *Store) IsHidden(ref string) bool {
	if s == nil {
		return false
	}
	return indexOf(s.Hidden, ref) >= 0
}

// IsKept reports whether ref is in the user's own keep list.
func (s *Store) IsKept(ref string) bool {
	if s == nil {
		return false
	}
	return indexOf(s.Kept, ref) >= 0
}

// Hide adds ref to the hide list (moving it there from Kept if it was
// there), at time at. A ref already hidden is left alone rather than
// duplicated or bumped, so pressing ctrl+x twice in a row without an
// un-hide in between does not change its original "at" timestamp.
func (s *Store) Hide(ref string, at time.Time) {
	if strings.TrimSpace(ref) == "" {
		return
	}
	s.Kept = removeEntry(s.Kept, ref)
	if indexOf(s.Hidden, ref) >= 0 {
		return
	}
	s.Hidden = append(s.Hidden, Entry{Ref: ref, At: at.UTC().Format(time.RFC3339)})
}

// Keep adds ref to the keep list (moving it there from Hidden if it was
// there) — the picker's un-hide action, and /model keep's own explicit
// pin against every automatic rule (design doc §2.2: "keep wins over hide
// at the same level, always").
func (s *Store) Keep(ref string, at time.Time) {
	if strings.TrimSpace(ref) == "" {
		return
	}
	s.Hidden = removeEntry(s.Hidden, ref)
	if indexOf(s.Kept, ref) >= 0 {
		return
	}
	s.Kept = append(s.Kept, Entry{Ref: ref, At: at.UTC().Format(time.RFC3339)})
}

// Unhide removes ref from the hide list without adding it to Kept — the
// picker's ctrl+x-on-a-hidden-row toggle (design doc's own wording: "same
// key, reads as a toggle"), as opposed to Keep, which also pins the model
// against every automatic rule. A model unhidden this way is simply back
// to "no user opinion", still subject to catalog.Rules' automatic
// deprecated/superseded/chat-only checks like any other model that was
// never touched.
func (s *Store) Unhide(ref string) {
	s.Hidden = removeEntry(s.Hidden, ref)
}

// indexOf returns the index of ref in entries (by refKey), or -1.
func indexOf(entries []Entry, ref string) int {
	key := refKey(ref)
	for i, e := range entries {
		if refKey(e.Ref) == key {
			return i
		}
	}
	return -1
}

// removeEntry returns entries with ref's entry dropped, preserving order
// of what remains. entries with no matching ref are returned unchanged
// (same underlying slice), so a Hide/Keep call that never touched the
// other list allocates nothing extra.
func removeEntry(entries []Entry, ref string) []Entry {
	i := indexOf(entries, ref)
	if i < 0 {
		return entries
	}
	out := make([]Entry, 0, len(entries)-1)
	out = append(out, entries[:i]...)
	out = append(out, entries[i+1:]...)
	return out
}

// curationstore.go implements tui.CurationStore (internal/tui/curation.go's
// own interface) over internal/curation.Store/Load/Save — the same "only
// internal/app touches this package's own write path" rule truststore.go
// already follows for internal/trust, and themestore.go follows for
// config.SetTheme.
package app

import (
	"sync"
	"time"

	"github.com/MichiTrader/ishakat/internal/curation"
	"github.com/MichiTrader/ishakat/internal/tui"
)

// fileCurationStore is the concrete tui.CurationStore backing every real
// interactive run. Unlike fileTrustStore (which reloads from disk on every
// Save to avoid clobbering another writer) this one keeps its own
// in-memory *curation.Store cached after the first Load and mutates that
// cache directly: tui.CurationStore.IsHidden is called on literally every
// picker keystroke (Picker.rebuild → splitCurationHidden), so re-reading
// curation.json from disk that often would turn every rebuild into a
// filesystem call for no benefit — curation.json has exactly one writer,
// this running process's own picker, so there is nothing else to reconcile
// against between one Hide/Unhide and the next.
type fileCurationStore struct {
	path string

	mu    sync.Mutex
	store *curation.Store
}

var _ tui.CurationStore = (*fileCurationStore)(nil)

// newFileCurationStore loads path once (best-effort — curation.Load never
// actually returns a non-nil error for any file-shaped failure; see its
// own doc comment on degrading to an empty Store with a Note instead) and
// returns a store ready to answer IsHidden/Reason immediately, with no
// further disk read until the next Hide/Unhide.
func newFileCurationStore(path string) *fileCurationStore {
	store, err := curation.Load(path)
	if err != nil || store == nil {
		store = &curation.Store{}
	}
	return &fileCurationStore{path: path, store: store}
}

// IsHidden reports whether ref is in the cached hide list.
func (s *fileCurationStore) IsHidden(ref string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.IsHidden(ref)
}

// Reason is always "hidden by you": every ref this store tracks got there
// through an explicit ctrl+x or /model hide, never a catalog.curate
// heuristic (see tui.CurationStore.Reason's own doc comment for why that
// distinction holds).
func (s *fileCurationStore) Reason(ref string) string {
	if !s.IsHidden(ref) {
		return ""
	}
	return "hidden by you"
}

// Hide adds ref to the cache and persists it immediately — curation.json
// auto-saves on every keystroke with no batching step (docs/PLAN.md's part
// 8 entry records this as a deliberate choice: there is no natural place
// for a separate "save" gesture to apply to).
func (s *fileCurationStore) Hide(ref string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.store.Hide(ref, time.Now())
	return curation.Save(s.path, s.store)
}

// Unhide removes ref from the cache and persists it immediately.
func (s *fileCurationStore) Unhide(ref string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.store.Unhide(ref)
	return curation.Save(s.path, s.store)
}

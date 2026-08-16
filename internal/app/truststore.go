// truststore.go implements tui.TrustStore (internal/tui/trust.go's own
// interface) over internal/trust.Store — the same "only internal/app
// touches this package's own write path" rule themestore.go's
// fileThemeStore already follows for config.SetTheme, and evolvestore.go's
// fileEvolveStore follows for config.SetEvolveMode.
package app

import (
	"time"

	"github.com/MichiTrader/ishakat/internal/permissions"
	"github.com/MichiTrader/ishakat/internal/trust"
	"github.com/MichiTrader/ishakat/internal/tui"
)

// fileTrustStore is the concrete tui.TrustStore backing every real
// interactive run. Unlike fileThemeStore it does carry state of its own —
// the project path the decision is *for*, since tui.TrustStore.Save's own
// single-argument shape (mirroring ThemeStore.Save's) never receives one —
// captured once at construction (app.go's Run already resolved it via
// os.Getwd before xdg.Pretty rewrote the display copy) and closed over for
// every Save call thereafter.
type fileTrustStore struct {
	// path is the project's own absolute, pre-xdg.Pretty path — trust.Set's
	// own doc comment on cleanPath is what actually normalizes it, so this
	// need not be filepath.Clean'd again here.
	path string

	// trustFile is xdg.TrustFile() in every real run — a plain string
	// rather than a second xdg import into this small file, since app.go
	// already has the one call it needs and can pass the result straight
	// through, the same "resolve once, hand the value down" shape
	// NewSessionRecorder's own cfg-derived path arguments already follow.
	trustFile string

	// guard is the same *permissions.Guard buildAgentOptions already wired
	// into this session's tool runner, or nil when cfg.Tools.Enabled is
	// false (app.go's own guard is scoped to that block). Save calls
	// guard.SetAutonomy so a decision made mid-session — not one merely
	// read back from a previous run's trust.json — takes effect on the
	// very next tool call, not only after a restart. internal/tui never
	// sees this field or the *permissions.Guard type it names: the bridge
	// lives entirely on this side of the §6.1 seam, the same as
	// toolReviewer already bridges Guard.Authorize back to the TUI in the
	// opposite direction.
	guard *permissions.Guard
}

var _ tui.TrustStore = (*fileTrustStore)(nil)

// Save persists autonomy for the project path fixed at construction,
// reading the existing trust.json first so this write never clobbers any
// other project's own already-recorded decision — trust.Store.Set's own
// "insert or update, in place" contract does the actual merge; this
// function only supplies the load/save round trip around it, the exact
// shape trust.Load/trust.Save's own doc comments describe for a caller.
func (s *fileTrustStore) Save(autonomy string) error {
	store, err := trust.Load(s.trustFile)
	if err != nil {
		// A corrupted or unreadable trust.json is not a reason to lose this
		// decision outright -- an empty Store still lets Set/Save record
		// exactly the one path being decided right now, the same
		// "degrade, do not refuse" rule trust.Load's own doc comment
		// already applies to a missing file (returned as ok, not err,
		// there); a genuine read failure here still gets *something*
		// written rather than silently discarding the human's answer.
		store = &trust.Store{}
	}
	store.Set(s.path, autonomy, time.Now())

	if s.guard != nil {
		s.guard.SetAutonomy(permissions.ParseAutonomy(autonomy))
	}

	return trust.Save(s.trustFile, store)
}

// evolvestore.go implements tui.EvolveStore (internal/tui/suggest.go's own
// interface) over the two files §19.7's suggestion feature actually reads
// and writes: xdg.UsageFile() (the ledger of observed patterns, already
// touched by ledger.go's own observeLedger) and xdg.SuggestStateFile()
// (the suggestion feature's own budget/decay bookkeeping). This is the one
// place internal/tui's own filesystem-ignorance (§6.1, EvolveStore's doc
// comment) is actually discharged, the same role sessionRecorder/
// sessionLister (session.go) already play for [session]'s own persistence.
package app

import (
	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/evolve"
	"github.com/MichiTrader/ishakat/internal/tui"
	"github.com/MichiTrader/ishakat/internal/xdg"
)

// fileEvolveStore is the concrete tui.EvolveStore backing every real run —
// see NewEvolveStore for the one caller (Run, app.go) that constructs it,
// and its own gating comment for why a real session may still end up with
// no store at all (nil) rather than one of these.
type fileEvolveStore struct {
	ledgerPath string
	statePath  string
}

var _ tui.EvolveStore = (*fileEvolveStore)(nil)

func (s *fileEvolveStore) LoadLedger() (*evolve.Ledger, error) {
	return evolve.LoadLedger(s.ledgerPath)
}

func (s *fileEvolveStore) SaveLedger(l *evolve.Ledger) error {
	return evolve.Save(s.ledgerPath, l)
}

func (s *fileEvolveStore) LoadSuggestState() (*evolve.SuggestState, error) {
	return evolve.LoadSuggestState(s.statePath)
}

func (s *fileEvolveStore) SaveSuggestState(st *evolve.SuggestState) error {
	return evolve.SaveSuggestState(s.statePath, st)
}

// Decay is §19.7 rule 4's own write-back: [tools.evolve].mode -> "on_request"
// once dismissSuggestion's own rejection streak just crossed
// DecayAfterRejects. config.SetEvolveMode already handles [tools.evolve]'s
// nested-table TOML shape (unlike SetAppModel's flat [app]) — see its own
// doc comment.
func (s *fileEvolveStore) Decay() error {
	return config.SetEvolveMode("on_request")
}

// NewEvolveStore builds the real tui.EvolveStore for an interactive run —
// gated exactly the same way agentOpts/reviewer are gated above it in Run
// (app.go): only when tools are enabled, the configured mode is actually
// "suggest" (any other value means the user asked for something other
// than this proactive overlay — off/on_request/auto are all handled
// elsewhere or not at all, never by silently offering it anyway), and
// there is a real TTY to draw the dialog on (§19.7 rule 5: total silence
// with none). Every other combination returns nil, the same "nothing
// wired, nothing happens" contract Root.evolveStore's own comment
// documents — checkSuggest simply never fires.
func NewEvolveStore(cfgTools config.Tools, hasTTY bool) tui.EvolveStore {
	if !cfgTools.Enabled || !hasTTY || cfgTools.Evolve.Mode != "suggest" {
		return nil
	}
	return &fileEvolveStore{
		ledgerPath: xdg.UsageFile(),
		statePath:  xdg.SuggestStateFile(),
	}
}

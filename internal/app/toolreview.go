// toolreview.go is Step 16's last missing link: the concrete
// permissions.Reviewer that internal/tui's ModeToolApprove overlay
// (toolapprove.go) and RunAgentTurn bridge (agentturn.go) exist to answer.
// It has to live here, not in internal/tui, because Reviewing means
// reaching a running *tea.Program from a goroutine that is not the one
// Bubble Tea's own event loop drives — engine.RunAgentTurn calls
// Guard.Authorize calls Reviewer.Review, all from inside the goroutine
// agentTurnCmd's tea.Cmd runs on — and only internal/app is allowed to
// import both permissions and tui (§6.1: tui never imports permissions'
// caller side, only the Request/Decision vocabulary toolapprove.go
// renders).
package app

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/permissions"
	"github.com/MichiTrader/ishakat/internal/tui"
)

// toolReviewer implements permissions.Reviewer by round-tripping through
// the running TUI: Review sends a tui.ToolApproveRequestMsg carrying a
// reply channel, then blocks on either that channel or ctx.Done(),
// whichever comes first. Root's own updateToolApprove/resolveToolApprove
// (internal/tui) is the only code that ever sends on reply — see
// ToolApproveRequestMsg's own doc comment for the full round trip.
//
// program is set once, by SetProgram, after app.Run has actually called
// tea.NewProgram: the Guard (and therefore this reviewer) has to be built
// and threaded into engine.AgentOptions *before* NewRoot/NewProgram run —
// tui.Options.AgentOptions is one of NewRoot's own arguments — so
// program cannot be a constructor parameter. A Review call that somehow
// raced ahead of SetProgram (there is no such path today: no turn can
// start before p.Run() begins pumping Update, and AgentOptions is only
// ever exercised from inside a turn) would have nothing to send to; it
// denies instead of sending on a nil channel, the same fail-closed
// default Guard.Authorize itself already applies when reviewer is nil.
type toolReviewer struct {
	program *tea.Program
}

// newToolReviewer returns a reviewer with no program yet attached — see
// SetProgram.
func newToolReviewer() *toolReviewer {
	return &toolReviewer{}
}

// SetProgram attaches the running program. Called exactly once, from
// Run, immediately after tea.NewProgram(root) returns.
func (r *toolReviewer) SetProgram(p *tea.Program) {
	r.program = p
}

// Review implements permissions.Reviewer. reply is unbuffered and never
// read from again once this call returns: resolveToolApproveWith
// (internal/tui) sends to it exactly once per dialog, and the select
// below drains that single send (or gives up on ctx.Done()) before
// returning, so there is nothing left for a later, stray send to block
// on.
func (r *toolReviewer) Review(ctx context.Context, req permissions.Request) (permissions.Decision, error) {
	if r.program == nil {
		return permissions.Decision{}, context.Canceled
	}

	reply := make(chan permissions.Decision)
	r.program.Send(tui.ToolApproveRequestMsg{Req: req, Reply: reply})

	select {
	case decision := <-reply:
		return decision, nil
	case <-ctx.Done():
		// cancelAgentTurn (internal/tui) never sends on reply itself —
		// see its own comment — precisely so this branch, not a racing
		// send, is what unblocks Review on cancellation.
		return permissions.Decision{}, ctx.Err()
	}
}

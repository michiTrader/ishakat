// askuser.go is Step 32 part 3's own closing piece: the concrete
// ask.Asker that internal/tui's ModeAskUser overlay (askuser.go, that
// package) and RunAgentTurn's own ask_user tool call exist to answer.
// Like toolreview.go it has to live here, not in internal/tui, because
// Asking means reaching a running *tea.Program from a goroutine that is
// not the one Bubble Tea's own event loop drives — internal/tools'
// AskUser.Run calls this Asker's Ask method from inside
// engine.RunAgentTurn's own goroutine — and only internal/app is allowed
// to import both internal/ask's producer side and internal/tui (§6.1:
// tui never imports a concrete Asker, only the Form/Answers vocabulary
// askuser.go (internal/tui) renders).
//
// §21.7's own diagram names two independent producers reaching one
// ask.Asker ("the model asks ask_user (a tool)" and "the runtime asks
// (Guard needs a decision)") — this file answers only the first one.
// permissions.Reviewer (toolReviewer, above) remains the runtime's own,
// separate bridge; §21.16 decision 1's own closing paragraph is explicit
// that collapsing the two would make every runtime question depend on the
// model having chosen to call a tool, so this file is not, and is not
// meant to become, a replacement for toolReviewer.
package app

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/ask"
	"github.com/MichiTrader/ishakat/internal/tui"
)

// tuiAsker implements ask.Asker by round-tripping through the running
// TUI: Ask sends a tui.AskUserRequestMsg carrying a reply channel, then
// blocks on either that channel or ctx.Done(), whichever comes first —
// the identical shape toolReviewer.Review already gives permissions'
// Reviewer, this time built on top of ask.AwaitReply (internal/ask/ask.go)
// instead of hand-rolling the same publish-then-select a third time.
// AwaitReply existed with zero callers before this file — see its own
// doc comment — precisely because this bridge, and serveReviewer's future
// equivalent, are what it was written for; toolReviewer.Review itself is
// deliberately left hand-rolled here (converging it onto AwaitReply too is
// a separate, later cleanup, not part of wiring this new bridge).
//
// program is set once, by SetProgram, after app.Run has actually called
// tea.NewProgram — the same two-step construction toolReviewer's own
// comment explains in full: buildAgentOptions needs an ask.Asker before
// tui.Options (and therefore the *tea.Program) exists at all.
type tuiAsker struct {
	program *tea.Program
}

// newTUIAsker returns an asker with no program yet attached — see
// SetProgram.
func newTUIAsker() *tuiAsker {
	return &tuiAsker{}
}

// SetProgram attaches the running program. Called exactly once, from Run,
// immediately after tea.NewProgram(root) returns, right alongside
// reviewer.SetProgram.
func (a *tuiAsker) SetProgram(p *tea.Program) {
	a.program = p
}

// Ask implements ask.Asker. reply is unbuffered and never read from again
// once this call returns: resolveAskUserWith (internal/tui) sends to it
// exactly once per dialog, and AwaitReply's own select drains that single
// send (or gives up on ctx.Done()) before returning, so there is nothing
// left for a later, stray send to block on — the identical contract
// toolReviewer.Review's own comment states for its reply channel.
//
// A nil program (Ask called before SetProgram — there is no such path
// today: no turn can start before p.Run() begins pumping Update, and
// AskUser is only ever exercised from inside a turn) fails closed with the
// same context.Canceled sentinel toolReviewer.Review returns for the
// identical case, rather than sending on a nil channel.
func (a *tuiAsker) Ask(ctx context.Context, form ask.Form) (ask.Answers, error) {
	if a.program == nil {
		return nil, context.Canceled
	}

	reply := make(chan ask.Answers)
	return ask.AwaitReply(ctx, reply, func() {
		a.program.Send(tui.AskUserRequestMsg{Form: form, Reply: reply})
	})
}

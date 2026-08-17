// waitphase.go is Step 32's own closing piece: the concrete
// engine.AgentOptions.OnWait implementation that surfaces §21.1's "wait"
// phase (acceptance narrative item 6, "auto·wait 22s appears... No retry
// storm.") in the TUI, the one bridge PR #165's own description named as
// still open after that PR shipped the exec/ask half. It has to live here,
// not in internal/tui, for the identical reason toolreview.go/askuser.go
// do: reaching a running *tea.Program from a goroutine that is not the one
// Bubble Tea's own event loop drives — engine.RunAgentTurn's own retry loop
// (engine.go's Engine.open) calls OnWait from inside the same goroutine
// agentTurnCmd's tea.Cmd runs on — and only internal/app is allowed to
// import both internal/tui and hold the concrete OnWait closure
// buildAgentOptions' caller wires into engine.AgentOptions (§6.1: tui never
// imports engine.AgentOptions itself, only the PhaseWaitMsg vocabulary
// agentturn.go's applyPhaseWait renders).
//
// Unlike toolReviewer/tuiAsker this bridge answers nothing: OnWait's own
// contract (engine.AgentOptions' doc comment) is fire-and-forget — the loop
// sleeps out wait on its own and resumes, with no decision for a human to
// make and therefore no reply channel to block on. That makes this the
// simplest of the three bridges in this file family: SetProgram, then a
// single p.Send with no select, no ctx, no channel at all.
package app

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/tui"
)

// tuiWaitNotifier implements the func(time.Duration, int) shape
// engine.AgentOptions.OnWait expects, by forwarding each call to the
// running TUI as a tui.PhaseWaitMsg.
//
// program is set once, by SetProgram, after app.Run has actually called
// tea.NewProgram — the same two-step construction toolReviewer/tuiAsker's
// own comments explain in full: buildAgentOptions needs an OnWait closure
// before tui.Options (and therefore the *tea.Program) exists at all.
type tuiWaitNotifier struct {
	program *tea.Program
}

// newTUIWaitNotifier returns a notifier with no program yet attached — see
// SetProgram.
func newTUIWaitNotifier() *tuiWaitNotifier {
	return &tuiWaitNotifier{}
}

// SetProgram attaches the running program. Called exactly once, from Run,
// immediately after tea.NewProgram(root) returns, right alongside
// reviewer.SetProgram/asker.SetProgram.
func (n *tuiWaitNotifier) SetProgram(p *tea.Program) {
	n.program = p
}

// OnWait is the func(wait time.Duration, attempt int) engine.AgentOptions.
// OnWait expects. attempt is not threaded into PhaseWaitMsg: §21.1's own
// mockup shows only the duration ("auto·wait 22s"), and the footer's
// single status line already has no room to spare for a second number
// (§2) that would tell a person nothing they could not already read off
// the wait itself.
//
// A nil program (OnWait called before SetProgram — there is no such path
// today: no turn can start before p.Run() begins pumping Update, and
// OnWait is only ever exercised from inside a turn already running through
// that program) is a silent no-op rather than a panic on a nil Program:
// unlike toolReviewer.Review/tuiAsker.Ask, nothing downstream is blocked
// waiting for this call to do anything — the retry loop sleeps and resumes
// regardless of whether a status line ever changed to say so.
func (n *tuiWaitNotifier) OnWait(wait time.Duration, attempt int) {
	if n.program == nil {
		return
	}
	n.program.Send(tui.PhaseWaitMsg{Wait: wait})
}

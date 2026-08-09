// agentturn.go is Step 16's other half of toolapprove.go: the plumbing that
// makes startEngineTurn (root.go) able to run a turn through
// engine.RunAgentTurn instead of m.eng.Start's plain StreamBuf streaming,
// which is what has to happen for there to be anything for
// ModeToolApprove/toolApprove.go to intercept in the first place —
// RunAgentTurn is what actually calls the Runner that internal/app bound to
// a *permissions.Guard, and Guard.Authorize is what actually calls the
// Reviewer whose Review method is what actually sends
// ToolApproveRequestMsg (see internal/app's own reviewer bridge).
//
// RunAgentTurn blocks until the whole loop finishes — unlike engine.Start,
// it never spawns its own goroutine (see its own doc comment) — so, exactly
// like compact.go's summarizeCmd wraps engine.Summarize, agentTurnCmd below
// wraps it as a tea.Cmd: Bubble Tea already runs every Cmd in its own
// goroutine (Program.handleCommands) before delivering its Msg back through
// Update, so nothing here needs a second goroutine of its own.
package tui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/convo"
	"github.com/MichiTrader/ishakat/internal/engine"
)

// agentTurnState is ModeBusy's own extra bookkeeping for a tools-enabled
// turn, live only between startAgentTurn and finishAgentTurn. It mirrors
// compactState's own shape and reason for existing: a couple of values one
// async call needs to hand to its own completion handler, kept off Root's
// hot path everywhere else.
//
// hist is the *convo.Conversation RunAgentTurn was actually handed —
// &m.conv at the moment startAgentTurn ran, not the m.conv of whichever
// later Root value Update happens to be holding when agentTurnDoneMsg
// finally arrives. Those are different values: Bubble Tea's Update takes
// and returns Root by value, so the Root in flight while the turn runs
// (busy re-rendering ModeBusy/ModeToolApprove on every tick) is a *copy*
// that stopped tracking hist's own conv the instant it was copied, while
// the background goroutine keeps appending straight into the original via
// this pointer. finishAgentTurn's first job is folding hist.Messages back
// into the Root it is actually returning — see its own comment.
//
// before is len(m.conv.Messages) at that same moment: everything at or
// after that index in hist.Messages once the turn finishes is new, and
// gets persisted individually (session.go's recordMessage), the same
// per-message persistence runAgentTurnHeadless already does for the
// headless path — a tool-using turn can add several messages (an
// assistant turn with tool calls, the tool results, a second assistant
// turn, …), and only the last of them is the "final answer" the transcript
// shows.
type agentTurnState struct {
	hist   *convo.Conversation
	before int
}

// agentTurnCmd wraps engine.RunAgentTurn as a tea.Cmd, the same shape
// summarizeCmd already establishes for engine.Summarize: Bubble Tea's own
// goroutine (started for us the moment this Cmd is returned from Update)
// is where the blocking call actually happens, so nothing here needs `go`
// of its own.
func agentTurnCmd(ctx context.Context, eng *engine.Engine, req engine.Request, opts engine.AgentOptions, hist *convo.Conversation) tea.Cmd {
	return func() tea.Msg {
		result, err := eng.RunAgentTurn(ctx, req, opts, hist)
		return agentTurnDoneMsg{result: result, err: err}
	}
}

// startAgentTurn is startEngineTurn's tools-enabled branch (§16): everything
// past "the request's messages are already in m.conv" for a turn that may
// pause, possibly more than once, on ModeToolApprove before it produces a
// final answer. bannerText is threaded through unchanged from submit, the
// same way startEngineTurn's own parameter already is.
func (m Root) startAgentTurn(bannerText string) (tea.Model, tea.Cmd) {
	m.mode = ModeBusy
	m.live.start(m.footer.Model)

	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel

	// hist is &m.conv, captured now, while m is still the Root value
	// startEngineTurn/submit built — see agentTurnState's own comment for
	// why this specific pointer, not "whatever m.conv is by the time the
	// turn finishes", is what has to be threaded through to
	// finishAgentTurn.
	hist := &m.conv
	m.agentTurn = agentTurnState{hist: hist, before: len(hist.Messages)}

	req := engine.Request{
		Model:  wireModel(m.cat, m.model),
		System: m.system,
		// Messages is left empty: RunAgentTurn rebuilds it every iteration
		// from hist.Active() (see agentloop.go's own comment on iterReq),
		// so nothing set here would ever reach the wire.
	}

	cmds := []tea.Cmd{agentTurnCmd(ctx, m.eng, req, m.agentOpts, hist)}
	if !m.lay.AnimationsOff {
		cmds = append(cmds, tickAnim(m.fps))
	}
	if bannerText != "" {
		cmds = append(cmds, tea.Println(bannerText+"\n"))
	}
	return m, tea.Batch(cmds...)
}

// openToolApprove is ToolApproveRequestMsg's handler: the agent loop's own
// goroutine (blocked inside permissions.Guard.Authorize's call to Review,
// itself blocked on msg.Reply) is waiting on the far side of msg.Reply for
// whatever resolveToolApprove eventually sends. Opening while mode is
// anything but ModeBusy should not happen in practice — a turn has to be
// running for the Guard to be asking at all — but if it somehow did, this
// still leaves the dialog open rather than silently dropping the request:
// dropping it would leave the goroutine (and its channel) blocked forever
// with nothing on screen ever able to answer it, which is strictly worse.
func (m Root) openToolApprove(msg toolApproveRequestMsg) (tea.Model, tea.Cmd) {
	m.toolApprove = newToolApproveDialog(msg.req, msg.reply)
	m.mode = ModeToolApprove
	return m, nil
}

// finishAgentTurn is agentTurnDoneMsg's handler: fold the background
// goroutine's private history back into the Root actually being returned,
// persist every message it added, then close the turn out with the same
// transcript shape finishTurn already draws for a plain streamed one —
// deliberately not by calling finishTurn itself, since finishTurn's own
// m.conv.Add(msg) would double-append the final assistant message
// RunAgentTurn already appended into hist before returning result.Text.
func (m Root) finishAgentTurn(result engine.AgentResult, err error) (tea.Model, tea.Cmd) {
	hist := m.agentTurn.hist
	before := m.agentTurn.before
	m.agentTurn = agentTurnState{}

	// Fold hist's own Messages (base history plus everything this turn
	// added: assistant/tool-call turns, tool results, the final answer)
	// back into the Root this call is about to return — see
	// agentTurnState's own comment for why this Root's m.conv would
	// otherwise still show only what existed before startAgentTurn ran.
	if hist != nil {
		m.conv.Messages = hist.Messages

		// Persist every new message individually, in order, mirroring
		// runAgentTurnHeadless's own loop (internal/app/agentturn.go) —
		// a --resume of this same session has to see the tool calls and
		// their results, not just the final text, and §10's own rule is
		// one line per finished message rather than one line for the
		// whole turn.
		for i := before; i < len(hist.Messages); i++ {
			m = m.recordMessage(hist.Messages[i])
		}
	}

	body := result.Text
	text := body
	switch {
	case result.Aborted:
		text += " [cancelado]"
	case err != nil:
		if text != "" {
			text += "\n"
		}
		text += m.lay.glyphs().warnMark + " " + err.Error()
	case result.Stopped != "":
		// A cap/budget/loop-detection stop is not an error and not a
		// cancellation — the loop ended on purpose, short of a natural
		// text-only termination — so it gets the same warnMark treatment
		// headless's own textSink.warn already gives result.Stopped
		// (agentturn.go's runAgentTurnHeadless), surfaced here instead of
		// on stderr since there is no stderr in the TUI.
		if text != "" {
			text += "\n"
		}
		text += m.lay.glyphs().warnMark + " " + result.Stopped
	}

	m.transcript = append(m.transcript, transcriptEntry{
		role: "assistant", name: m.live.model, text: text, ts: time.Now(),
	})

	m.releaseTurn()
	m.live = liveTurn{}
	m.mode = ModeChat
	m.animOffset = 0

	return m.checkAutoCompact()
}

// cancelAgentTurn implements §7.4 for a tools-enabled turn: closing m.cancel
// unblocks both agentTurnCmd's own ctx.Err() checks inside RunAgentTurn and,
// when a ModeToolApprove dialog happens to be open at the moment, the
// paused Guard.Authorize call underneath it — the reviewer bridge's own
// Review method (internal/app) selects on ctx.Done() alongside its reply
// channel for exactly this, so cancelling ctx alone is enough to unblock
// it; this function never writes to toolApprove.reply itself, which would
// otherwise risk racing that same select from the wrong side (Update
// sending on a channel the bridge may already have stopped reading from
// the instant ctx.Done() won the race). Either way the loop notices on its
// own and finishes normally through finishAgentTurn/agentTurnDoneMsg, the
// same "cancellation takes the ordinary ending's path" contract cancelTurn
// already documents for a plain streamed turn.
func (m Root) cancelAgentTurn() (tea.Model, tea.Cmd) {
	m.live.aborted = true
	m.toolApprove = toolApproveDialog{}
	if m.cancel != nil {
		m.cancel()
	}
	m.mode = ModeBusy
	return m, nil
}

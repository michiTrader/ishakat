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

// agentTurnCmd wraps engine.RunAgentTurnStreaming as a tea.Cmd, the same
// shape summarizeCmd already establishes for engine.Summarize: Bubble Tea's
// own goroutine (started for us the moment this Cmd is returned from
// Update) is where the blocking call actually happens, so nothing here
// needs `go` of its own.
//
// W2 item 2 (docs/ROADMAP-ux-2026-08-20.md): this used to call the plain
// blocking eng.RunAgentTurn; it now calls RunAgentTurnStreaming with sink,
// the AgentSink startAgentTurn builds from its own agentStreamBuf
// (agentstream.go). A zero AgentSink makes RunAgentTurnStreaming identical
// to RunAgentTurn field for field (RunAgentTurnStreaming's own doc
// comment), so agentTurnCmdTests that pass engine.AgentSink{} keep passing
// unchanged — this is purely an additive event surface, not a behaviour
// change to the loop itself.
func agentTurnCmd(ctx context.Context, eng *engine.Engine, req engine.Request, opts engine.AgentOptions, hist *convo.Conversation, sink engine.AgentSink) tea.Cmd {
	return func() tea.Msg {
		result, err := eng.RunAgentTurnStreaming(ctx, req, opts, hist, sink)
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
	// §21.1's "exec" phase: this turn is now running the loop's ordinary
	// tool-call/answer cycle with no dialog open. openToolApprove/
	// openAskUser overwrite this to "ask" the moment either pauses on a
	// human; resolveToolApproveWith/resolveAskUserWith set it back to
	// "exec" on return, and finishAgentTurn clears it once the turn itself
	// ends. See FooterState.Phase's own doc comment for why "plan"/
	// "check"/"wait" are not produced here.
	m.footer.Phase = "exec"

	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel

	// hist is &m.conv, captured now, while m is still the Root value
	// startEngineTurn/submit built — see agentTurnState's own comment for
	// why this specific pointer, not "whatever m.conv is by the time the
	// turn finishes", is what has to be threaded through to
	// finishAgentTurn.
	hist := &m.conv
	m.agentTurn = agentTurnState{hist: hist, before: len(hist.Messages)}

	// agentStream is agentTurnCmd's own goroutine's landing zone for live
	// events (W2 item 2, docs/ROADMAP-ux-2026-08-20.md; agentstream.go):
	// mirrors startEngineTurn's plain-path m.buf = &engine.StreamBuf{}
	// immediately below, just on the tools-enabled fork. Its .sink() is
	// what actually reaches RunAgentTurnStreaming below; drainAgentStream
	// (root.go's streamTickMsg dispatch) is the only reader.
	streamBuf := &agentStreamBuf{}
	m.agentStream = streamBuf

	req := engine.Request{
		Model:  wireModel(m.cat, m.model),
		System: m.system,
		// Messages is left empty: RunAgentTurn rebuilds it every iteration
		// from hist.Active() (see agentloop.go's own comment on iterReq),
		// so nothing set here would ever reach the wire.
	}

	// tickStream() (root.go) arms the same 50ms repaint clock the plain
	// path uses (StreamIntervalMS, layout.go) — reused rather than a
	// second ticker of its own, keeping §14's "zero CPU at idle" test
	// suite (idle_internal_test.go) satisfied with no new ticker kind to
	// account for.
	cmds := []tea.Cmd{agentTurnCmd(ctx, m.eng, req, m.agentOpts, hist, streamBuf.sink()), tickStream()}
	if !m.lay.AnimationsOff {
		cmds = append(cmds, tickAnim(m.fps))
	}
	// printBannerCmd (root.go): the one shared banner-to-scrollback producer,
	// so this fork and startEngineTurn's plain path can never drift apart on
	// what "retire the banner" means (RC-5, "one banner producer").
	cmds = append(cmds, printBannerCmd(bannerText))
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
func (m Root) openToolApprove(msg ToolApproveRequestMsg) (tea.Model, tea.Cmd) {
	m.toolApprove = newToolApproveDialog(msg.Req, msg.Reply)
	m.mode = ModeToolApprove
	// §21.1's "ask" phase: the loop is paused on a human decision, not
	// executing — see FooterState.Phase's own doc comment.
	m.footer.Phase = "ask"
	return m, nil
}

// openAskUser is AskUserRequestMsg's handler: the agent loop's own
// goroutine (blocked inside internal/app's tuiAsker.Ask, itself blocked on
// msg.Reply) is waiting on the far side of msg.Reply for whatever
// resolveAskUserWith eventually sends. Mirrors openToolApprove's own
// reasoning for opening regardless of mode: dropping the request would
// leave that goroutine (and its channel) blocked forever with nothing on
// screen able to answer it.
func (m Root) openAskUser(msg AskUserRequestMsg) (tea.Model, tea.Cmd) {
	m.askUser = newAskUserDialog(msg.Form, msg.Reply)
	m.mode = ModeAskUser
	// §21.1's "ask" phase: same reasoning as openToolApprove above.
	m.footer.Phase = "ask"
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

	// Tool activity is prepended to the answer instead of being left
	// invisible. Without this the interface is indistinguishable from a
	// model that simply chose not to use tools: the user asked for a file,
	// the transcript showed only prose, and there was no way to tell
	// "wrote the file" from "explained how to write the file" — which is
	// precisely the confusion the original Step 16 report described
	// (`ls` showed no file, and nothing on screen said whether a tool had
	// even run). A turn that touched the filesystem has to say so.
	if summary := toolActivityLines(m.lay.glyphs(), hist, before, m.missionRulesOr()); summary != "" {
		if text == "" {
			text = summary
		} else {
			text = summary + "\n" + text
		}
	}

	m.transcript = append(m.transcript, transcriptEntry{
		role: "assistant", name: m.live.model, text: text, ts: time.Now(),
		reasoning: result.Reasoning,
	})

	// checkFallback's own counter (§11 Phase 4, root.go's finishTurn has
	// the full comment): a real provider failure extends the streak, and
	// nothing else does — result.Aborted is the user's own cancellation,
	// and result.Stopped is a cap/budget/loop-detection stop the loop chose
	// on purpose, neither of which says anything about whether the
	// provider itself is working.
	if err != nil {
		m.consecutiveFailures++
	} else {
		m.consecutiveFailures = 0
	}

	m.releaseTurn()
	m.live = liveTurn{}
	m.mode = ModeChat
	m.animOffset = 0
	// The turn is over: no phase to report until the next one starts —
	// see FooterState.Phase's own doc comment on empty being "no turn
	// running".
	m.footer.Phase = ""

	return m.checkEndOfTurn()
}

// applyPhaseWait is PhaseWaitMsg's handler: §21.1's "wait" phase, the last
// of the three named in the acceptance narrative's item 6 ("auto·wait 22s
// appears; ishakat waits exactly what was asked, then resumes... No retry
// storm."). Unlike openToolApprove/openAskUser this never changes m.mode —
// OnWait fires from inside the loop's own retry, still mid-"exec", with no
// dialog to open and nothing for a human to answer — it only overwrites the
// same m.footer.Phase word those two already set, this time to a snapshot
// of how long the loop is about to sleep.
//
// There is deliberately no follow-up message that clears this back to
// "exec" once Wait elapses: OnWait's own contract (engine.AgentOptions'
// doc comment) is a single call right before the sleep, with no matching
// "wait ended" signal from the engine to key a second message off. Rather
// than invent a tea.Tick countdown purely to clear a label — which would
// mean a timer running for the sleep's own duration, on top of the retry
// itself, the exact kind of always-on ticker §14 asks this codebase not to
// add — the phase is left as a static snapshot: the next real phase event
// (another OnWait call, an ask-tier pause, or the turn simply finishing
// through finishAgentTurn) is what overwrites or clears it. A stale
// "wait 22s" lingering for the tail of that same wait is a label slightly
// behind the clock, not a lie about what the loop is doing — it is still
// waiting — and finishAgentTurn's own unconditional `m.footer.Phase = ""`
// guarantees it never survives past the turn that produced it.
func (m Root) applyPhaseWait(msg PhaseWaitMsg) (tea.Model, tea.Cmd) {
	m.footer.Phase = "wait " + roundWait(msg.Wait).String()
	return m, nil
}

// roundWait renders a retry wait at a granularity a person can read,
// mirroring internal/app/agentturn.go's own roundWait (the headless path's
// identical helper) — duplicated rather than shared because internal/tui
// does not import internal/app (§6.1 draws that boundary the other way
// around), and formatting one time.Duration is not enough logic to justify
// a third package just to hold it once. A raw time.Duration prints as
// "22.317849213s", nine digits of noise the footer's single status line
// has no room for (§2). Sub-second waits keep millisecond resolution
// because "0s" would be a lie about why the loop paused.
func roundWait(d time.Duration) time.Duration {
	if d < time.Second {
		return d.Round(time.Millisecond)
	}
	return d.Round(time.Second)
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
	m.askUser = askUserDialog{}
	if m.cancel != nil {
		m.cancel()
	}
	m.mode = ModeBusy
	// Still "exec" here, not cleared: the turn is not over yet (it ends
	// normally through finishAgentTurn/agentTurnDoneMsg, per this
	// function's own doc comment above), only whichever dialog was open
	// closes — mirroring resolveToolApproveWith/resolveAskUserWith's own
	// "back to exec" transition rather than agentTurnDoneMsg's own "turn
	// over, clear it" one.
	m.footer.Phase = "exec"
	return m, nil
}

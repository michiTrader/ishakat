// agentloop.go is Step 14 (§12bis): the tool-calling loop that turns Engine
// from a single-turn runner into an agent runtime. One iteration is: open the
// stream, drain it into the caller's sink (a StreamBuf for the TUI, a
// strings.Builder for headless), and if the assistant message ended with tool
// calls, run each one through the injected ToolRunner, append BlockToolResult
// messages to the history, and iterate. The loop terminates when a turn
// produces no tool calls — that is the model's own signal that it is done.
//
// Engine never knows what a tool *is*: ToolRunner is a function type bound by
// internal/app, and the concrete implementations live in internal/tools
// (Step 15). That keeps internal/tools out of engine's import graph, which is
// what makes Step 21's auto-extension injectable without a refactor.
//
// The six semantics below are part of the contract, not implementation
// details (§12bis, §21.9): the hard cap, loop detection, cancellation,
// error-is-data, output truncation, and — added by step 26 — refusal ends the
// turn. Each is small on its own; together they are what stops a stuck loop
// from burning real money on an expensive model.
//
// The sixth is the exception that keeps the fourth honest. Error-is-data is
// what lets the reactive loop handle the unforeseen without a Planner (§3), so
// it is deliberately broad: a tool that ran and failed, a tool that does not
// exist, a malformed argument, all of it goes back to the model to react to.
// A human's refusal is the one case where that is wrong, because the thing
// blocking progress is a decision rather than a fact about the world, and no
// further request in this turn can change it. Treating it as data is what
// turned an over-asking agent into a rate-limited one for a real user
// (docs/BUG-rate-limit-amplifier.md).
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/MichiTrader/ishakat/internal/convo"
)

// Defaults for the agent loop. The cap is the §12bis hard limit; the output
// ceiling is what keeps a 40 MB `bash` run from destroying the context window,
// which is the resource that is actually scarce.
const (
	defaultMaxToolCalls   = 25
	defaultMaxOutputBytes = 32 << 10 // 32 KiB

	// maxFutileAttempts is how many consecutive tool calls may fail with the
	// same error before the loop stops (step 26, fix 3). Three is chosen to
	// match the shape the bug report actually observed — ls, ls -la, find .
	// — and because two is a legitimate retry (a transient failure, a
	// corrected argument) while three identical failures in a row is a model
	// that has stopped learning from the result.
	maxFutileAttempts = 3
)

// AgentOptions configures one agent turn. The zero value is valid: no tools,
// no runner — the loop runs exactly one iteration and behaves like
// RunToCompletion, which is also the safe fallback for a model whose Caps.Tools
// is false.
type AgentOptions struct {
	// Tools is the tool catalogue offered to the model this turn. Empty means
	// no tools: the request carries no `tools` array and the loop runs once.
	Tools []ToolDef

	// Runner executes one tool call. nil is equivalent to "no tools": even if
	// the model produces a tool call (a misbehaving or hallucinating model),
	// the loop reports it as a tool error in the context rather than crashing.
	Runner ToolRunner

	// MaxToolCalls is the hard cap per turn (§12bis). Zero means the default
	// (25). A negative value disables the cap — intended only for tests.
	MaxToolCalls int

	// MaxOutputBytes truncates a tool result above this size, keeping a marker
	// that names how much was dropped (§12bis). Zero means the default (32 KiB).
	// A negative value disables truncation — intended only for tests.
	MaxOutputBytes int

	// OnWait, when set, is called before the loop sleeps on a retryable
	// handshake failure — in practice a 429 carrying Retry-After (step 26,
	// fix 2). The loop already honoured that wait; what it did not do was
	// say so, and a 22-second silent pause is indistinguishable from a hang
	// to the person holding the phone. The caller decides how to render it
	// (§21.2's `auto·wait 22s`); engine only reports the duration.
	//
	// It is called from the loop's own goroutine, so an implementation must
	// not block.
	OnWait func(wait time.Duration, attempt int)

	// MinInterval is a floor on the time between one iteration's provider
	// request and the next (step 26, fix 5). Zero -- the default -- disables
	// it, and that default is deliberate.
	//
	// The bug report sequences this last and warns why: "a sleep that hides
	// an amplification defect is worse than no fix: it makes the defect
	// harder to observe and it will come back at scale". Fixes 1-3 remove
	// the amplification itself, and the suite proves they do so with this
	// at zero (closing criterion 4). What remains for this knob is the
	// honest case it was always meant for -- a provider whose limit is
	// requests-per-minute rather than tokens, where even a correct agent
	// wants a floor -- not the defects above.
	MinInterval time.Duration

	// BudgetUSD is the maximum estimated provider spend for this session. Zero
	// disables the budget. Prices are USD per million tokens; unknown prices are
	// represented by all-zero rates and do not consume the budget.
	BudgetUSD float64
	// SpentUSD is the amount already consumed before this turn. Callers that
	// persist sessions can initialize it from prior conversation usage.
	SpentUSD          float64
	InputCostUSD      float64
	OutputCostUSD     float64
	CacheReadCostUSD  float64
	CacheWriteCostUSD float64
}

// AgentResult is the outcome of RunAgentTurn: the final assistant text, the
// accumulated usage across every iteration, how many tool calls ran, and —
// when the loop did not terminate naturally — why it stopped.
type AgentResult struct {
	Text  string
	Usage *convo.Usage
	Calls int

	// Reasoning is the reasoning stream (EventReasoning deltas) collected
	// during the same iteration that produced Text (§17 point 6a, "the
	// thinking preview"). It is set at every exit that also sets Text —
	// natural termination, the cap/budget/loop-detection/futile stops, and
	// a mid-batch cancellation or refusal — mirroring Text's own
	// unconditional text.String() assignment at each of those sites.
	//
	// Before this field existed, reasoning was silently dropped on every
	// non-cancelled iteration: the loop's own per-iteration `reasoning`
	// builder (below) was read only by abortedAssistant, exclusively on the
	// ctx.Err() != nil path. A tool-enabled turn that finished normally, or
	// that stopped on its own (cap, budget, loop detection), never
	// surfaced anything the model "thought" on its way to that outcome, and
	// neither TUI nor headless had any field on this struct to read it
	// from even if they wanted to. This makes the plain-streaming path
	// (internal/tui/root.go's drainStream, already fed from liveTurn.reason)
	// and the tool-enabled path (finishAgentTurn/runAgentTurnHeadless)
	// symmetric: both now have real reasoning data at the point a turn
	// ends, not just the cancelled-mid-loop case.
	Reasoning string

	// Stopped is empty when the model ended on a text turn (the natural
	// termination). It carries a short reason when the cap, budget, or loop
	// detection ended the turn early, so the caller can surface it honestly.
	Stopped string
	// CostUSD is the estimated spend accumulated by this turn, including
	// SpentUSD supplied in AgentOptions.
	CostUSD float64

	// Aborted is true when ctx was cancelled mid-loop. The partial assistant
	// message is still in Messages and in Text; a tool already running got its
	// ctx cancelled, and write_file/edit_file's write-temp+rename (Step 15) is
	// what guarantees no half-written file survives that.
	Aborted bool
}

// AgentSink is RunAgentTurnStreaming's event surface (DECISION-2,
// docs/ROADMAP-ux-2026-08-20.md's W2 section, item 1: "Streaming agent-turn
// API in internal/engine, blocking form preserved for headless/serve").
// Every field is optional — a nil field is simply never called, exactly
// like AgentOptions.OnWait's existing contract — so the zero AgentSink
// makes RunAgentTurnStreaming behave identically to RunAgentTurn.
//
// Every callback fires synchronously, on the same goroutine
// RunAgentTurnStreaming itself runs on (the caller's own — nothing in this
// package starts a goroutine for a turn; see RunAgentTurn's own doc
// comment). An implementation MUST NOT block, for the same reason
// AgentOptions.OnWait's own doc comment already gives: this goroutine is
// mid-turn, and blocking it stalls the loop, not just the display. A
// caller that wants to coalesce these into a repaint clock (§7.3's own
// discipline — "the model for the buffering discipline" DECISION-2 names
// StreamBuf as) should push into a buffer here and drain it on its own
// tick, never do the repaint from inside the callback itself.
type AgentSink struct {
	// OnReasoningDelta fires for every EventReasoning delta, in arrival
	// order, at the same point the loop's own per-iteration reasoning
	// builder appends it — so a caller sees exactly the text
	// AgentResult.Reasoning eventually carries, just live rather than only
	// once the turn ends.
	OnReasoningDelta func(delta string)

	// OnTextDelta is OnReasoningDelta's sibling for EventDelta.
	OnTextDelta func(delta string)

	// OnToolCallStart fires once per tool call, right after that call has
	// already passed the cap and loop-detection checks for this batch —
	// never for a call the loop is about to refuse instead of running, so
	// a caller can pair every Start it sees with exactly one End, never a
	// Start with no matching End.
	OnToolCallStart func(id, name string, args json.RawMessage)

	// OnToolCallEnd fires once per tool call OnToolCallStart already
	// announced, after that call's BlockToolResult/BlockToolErrorResult
	// has already been appended to history — so a caller that reacts by
	// reading history sees the result already there, never a call still
	// in flight. result is the same text (already truncated per
	// AgentOptions.MaxOutputBytes) the persisted block carries.
	OnToolCallEnd func(id, name, result string, isError bool)

	// OnUsage fires whenever the running Usage total changes, mirroring
	// StreamBuf.setUsage's own "either call overwrites, both carry the
	// provider's running total" contract — never a delta.
	OnUsage func(usage *convo.Usage)

	// OnPhase fires "exec" once per iteration (mirroring
	// startAgentTurn's own footer.Phase = "exec", §21.1's vocabulary) so a
	// caller's phase/footer display can be driven from a real event
	// instead of an inferred one (W2 item 2). AgentOptions.OnWait remains
	// the separate, existing source for the "wait <duration>" phase — this
	// field is additive to it, not a replacement.
	OnPhase func(phase string)

	// Inject is the steering hook (DECISION-2 consequence 2, W2 item 4):
	// if set, called once per iteration, right before that iteration's
	// request is rebuilt from history.Active() — i.e. after the previous
	// iteration's assistant/tool messages already landed in history, and
	// before the next provider request is opened. Whatever messages it
	// returns (nil or empty: nothing to add) are appended to history
	// before that request is built, so the model's very next turn already
	// sees them in context — the same "the request is the history"
	// discipline root.go's submit already documents for the very first
	// user message of a turn.
	//
	// Inject must not block: it is polled once per iteration, never
	// awaited, so a caller wanting to inject a message typed a moment ago
	// should keep it in a queue this callback drains, mirroring OnWait's
	// own "must not block" requirement and for the identical reason.
	//
	// What Inject must NEVER be a path to (§21's own security property,
	// DECISION-2 consequence 2, explicitly a W2 gate and not a
	// code-review note): approving a pending tool call, changing
	// autonomy, or lifting a §21.4 invariant. Those resolve exclusively
	// through their own existing dialogs (ToolApproveRequestMsg,
	// AskUserRequestMsg and their resolve* handlers), which this field has
	// no path into — an injected message becomes an ordinary
	// convo.RoleUser history entry the model reads on its next turn like
	// any other user message, never consulted by
	// permissions.Guard.Authorize. A test asserting the negative case (a
	// steering message arriving while a tool call is pending must leave
	// that call pending, unapproved) is required before the caller-side
	// steering feature this hook enables can be considered closed.
	Inject func() []convo.Message
}

// RunAgentTurn runs the tool-calling loop to completion (or until the cap, loop
// detection, or cancellation stops it). It blocks: the TUI path that needs to
// stream as it goes will wrap each iteration's channel drain around its own
// StreamBuf, but the contract and the closing criterion of §12bis is the
// headless path — `ishakat -p "…"` with one fake tool producing a correct
// answer through a real tool call.
//
// history is appended to in place: each iteration's assistant message (with its
// BlockToolCall blocks) and each tool's BlockToolResult message are added to
// *history before the next iteration, so the model sees the full loop in
// context. The caller owns history and persists it; engine never does.
//
// This is DECISION-2's "blocking form... preserved for headless/serve" half
// (docs/ROADMAP-ux-2026-08-20.md's W2 section, item 1): it is runAgentTurn
// below with a nil sink, so every existing headless/serve call site
// (internal/app/agentturn.go's runAgentTurnHeadless) sees byte-for-byte the
// same behaviour it always has — no new field to set, no new import, no
// new goroutine. RunAgentTurnStreaming, just below, is the exact same loop
// with the event surface DECISION-2 asks for wired in.
func (e *Engine) RunAgentTurn(ctx context.Context, req Request, opts AgentOptions, history *convo.Conversation) (AgentResult, error) {
	return e.runAgentTurn(ctx, req, opts, history, nil)
}

// RunAgentTurnStreaming is RunAgentTurn's streaming sibling (DECISION-2,
// docs/ROADMAP-ux-2026-08-20.md's W2 section, item 1): the same tool-calling
// loop and the same blocking contract — it returns only once the turn is
// over, exactly like RunAgentTurn; nothing here spawns a goroutine of its
// own — but with sink's callbacks fired as the loop produces reasoning/text
// deltas, starts and finishes tool calls, moves through phases, and reaches
// the one point per iteration where a caller may inject a message into
// history. A zero AgentSink (every field nil) makes this call identical to
// RunAgentTurn, field for field.
func (e *Engine) RunAgentTurnStreaming(ctx context.Context, req Request, opts AgentOptions, history *convo.Conversation, sink AgentSink) (AgentResult, error) {
	return e.runAgentTurn(ctx, req, opts, history, &sink)
}

// runAgentTurn is RunAgentTurn's and RunAgentTurnStreaming's shared body —
// one loop, not two copies of §12bis's six semantics (the hard cap, loop
// detection, cancellation, error-is-data, output truncation, refusal-ends-
// the-turn) to keep in sync, the same "no second copy" reasoning already
// applied to e.open above. sink is nil for the blocking form; every site
// below that reports an event guards on sink != nil, and per field on that
// specific callback being non-nil, before calling it — a nil sink costs
// this function nothing beyond those checks.
func (e *Engine) runAgentTurn(ctx context.Context, req Request, opts AgentOptions, history *convo.Conversation, sink *AgentSink) (AgentResult, error) {
	maxCalls := opts.MaxToolCalls
	if maxCalls == 0 {
		maxCalls = defaultMaxToolCalls
	}
	maxOut := opts.MaxOutputBytes
	if maxOut == 0 {
		maxOut = defaultMaxOutputBytes
	}

	// No tools or no runner: the loop runs exactly one iteration. This is the
	// path a tools-incapable model takes (Caps.Tools false → app leaves
	// req.Tools empty), and it is also the safe fallback for a caller that
	// has not bound a runner yet.
	hasTools := len(opts.Tools) > 0 && opts.Runner != nil
	if !hasTools {
		opts.Tools = nil
	}

	var result AgentResult
	// lastToolName/lastToolArgs deliberately live outside the per-iteration
	// loop below: loop detection (Bug 4, §12bis) compares a batch's first
	// call against the *previous iteration's last* call, so this state must
	// survive across iterations, not reset each one — see the i == 0 check
	// where it is read.
	var lastToolName string
	var lastToolArgs []byte
	callsThisTurn := 0

	// Futility tracking (step 26, fix 3). Byte-exact loop detection above
	// catches a model repeating itself verbatim; it cannot catch a model
	// working its way through variants that all fail the same way — ls,
	// ls -la, find . — because every attempt has different arguments. That
	// variant hunt is what the bug report measured, and it costs one full
	// provider request per attempt.
	//
	// The discriminator is deliberately not argument similarity. Normalizing
	// arguments cannot separate "grep foo then grep bar", which is ordinary
	// progress, from "ls then ls -la", which is not: both are the same tool
	// with different arguments. What separates them is whether the attempts
	// are getting anywhere. An unchanging error means the world is not
	// responding to the variation, so the next variant will not either.
	var lastFailure string
	futileRun := 0

	// lastRequest paces the loop when MinInterval is set (fix 5). It is the
	// zero Time on the first iteration, so the first request is never
	// delayed: the floor is between requests, not before the user's own.
	var lastRequest time.Time

	// The loop. One body = one model turn + its tool executions.
	for iteration := 0; ; iteration++ {
		if err := ctx.Err(); err != nil {
			result.Aborted = true
			return result, err
		}

		// §21.1's "exec" phase, once per iteration (W2 item 2: "phase/footer
		// updates from real events rather than inferred ones"). This is a
		// real per-iteration event, unlike root.go's startAgentTurn setting
		// footer.Phase = "exec" once before the loop even starts — a caller
		// wired to this can distinguish "iteration 1 of the loop is
		// running" from "iteration 4 is", which the single pre-loop write
		// never could.
		if sink != nil && sink.OnPhase != nil {
			sink.OnPhase("exec")
		}

		// The steering hook (DECISION-2 consequence 2, W2 item 4): polled
		// once per iteration, right here — after the previous iteration's
		// own messages already landed in history (or, on iteration 0,
		// after the caller's own pre-turn history), and before iterReq
		// below rebuilds the request from history.Active(). Whatever
		// Inject returns joins history now, so the very next provider
		// request already carries it in context, exactly like any other
		// user message. See AgentSink.Inject's own doc comment for the
		// hard security boundary this must never cross.
		if sink != nil && sink.Inject != nil {
			for _, injected := range sink.Inject() {
				history.Add(injected)
			}
		}

		if opts.MinInterval > 0 && !lastRequest.IsZero() {
			if since := time.Since(lastRequest); since < opts.MinInterval {
				// Interruptible: a user hitting esc during the floor must not
				// have to wait it out (§7.4).
				select {
				case <-time.After(opts.MinInterval - since):
				case <-ctx.Done():
					result.Aborted = true
					return result, ctx.Err()
				}
			}
		}
		lastRequest = time.Now()

		// Rebuild the request each iteration with the grown history. The model
		// has to see the tool calls and results from the previous iteration;
		// without them it would ask for the same tool again.
		iterReq := req
		iterReq.Messages = history.Active()
		iterReq.Tools = opts.Tools

		ch, err := e.open(ctx, iterReq, opts.OnWait)
		if err != nil {
			if ctx.Err() != nil {
				result.Aborted = true
				return result, ctx.Err()
			}
			return result, err
		}

		// Drain this iteration's stream: collect text for the final answer,
		// the model's reasoning (§17 point 6a: kept, not dropped, so the
		// caller can show a "thinking" preview — see AgentResult.Reasoning's
		// own doc comment for why this used to be thrown away), and
		// accumulate tool calls.
		var text strings.Builder
		var reasoning strings.Builder
		var toolCalls []toolCallOut
		var iterUsage *convo.Usage
		var turnErr error
		for ev := range ch {
			switch ev.Kind {
			case EventDelta:
				text.WriteString(ev.Text)
				if sink != nil && sink.OnTextDelta != nil {
					sink.OnTextDelta(ev.Text)
				}
			case EventReasoning:
				reasoning.WriteString(ev.Text)
				if sink != nil && sink.OnReasoningDelta != nil {
					sink.OnReasoningDelta(ev.Text)
				}
			case EventToolCall:
				toolCalls = append(toolCalls, toolCallOut{
					id:        ev.ID,
					name:      ev.Name,
					args:      ev.Args,
					signature: ev.Signature,
				})
			case EventUsage:
				iterUsage = ev.Usage
				if sink != nil && sink.OnUsage != nil {
					sink.OnUsage(ev.Usage)
				}
			case EventError:
				if turnErr == nil {
					turnErr = ev.Err
				}
				if ev.Usage != nil {
					iterUsage = ev.Usage
					if sink != nil && sink.OnUsage != nil {
						sink.OnUsage(ev.Usage)
					}
				}
			case EventDone:
				if ev.Usage != nil {
					iterUsage = ev.Usage
					if sink != nil && sink.OnUsage != nil {
						sink.OnUsage(ev.Usage)
					}
				}
			}
		}

		// Accumulate usage across iterations so /stats and the footer see the
		// whole turn, not just the last leg.
		if iterUsage != nil {
			if result.Usage == nil {
				result.Usage = &convo.Usage{}
			}
			result.Usage.Add(iterUsage)
		}
		// CostUSD on Usage is the current turn's durable accounting record;
		// result.CostUSD additionally includes prior-session spend for the
		// budget decision.
		if result.Usage != nil {
			result.Usage.CostUSD = estimateCost(result.Usage, opts)
		}
		result.CostUSD = opts.SpentUSD + estimateCost(result.Usage, opts)

		// Cancellation wins over any in-flight error, exactly as run() does for
		// a text turn (§7.4): the user hit esc, that is not a failure.
		if ctx.Err() != nil {
			// Persist the partial assistant message as Aborted, so --resume
			// restores what the user saw rather than nothing.
			if text.Len() > 0 || len(toolCalls) > 0 {
				partial := abortedAssistant(text.String(), reasoning.String(), toolCalls, req.Model)
				history.Add(partial)
				result.Text = text.String()
				result.Reasoning = reasoning.String()
			}
			result.Aborted = true
			return result, ctx.Err()
		}

		if turnErr != nil {
			// A mid-stream error ends the loop. The partial text is still
			// returned so the caller can show what arrived before the failure,
			// matching run()'s own contract.
			result.Text = text.String()
			result.Reasoning = reasoning.String()
			return result, turnErr
		}

		// Record the assistant turn in history: its reasoning, its text (so
		// the saved session is faithful) and its tool calls (so the dialect
		// can re-serialize them on the next iteration). Even an iteration that
		// produced only tool calls has to land in history — the next request
		// needs the assistant's tool_calls message to precede the tool results.
		//
		// The ReasoningBlock added here (§17 point 6a) is new: before this,
		// asstBlocks only ever carried TextBlock/ToolCallBlock, and
		// abortedAssistant's own ReasoningBlock (cancellation only) was the
		// sole place in this file that ever attached one — every
		// non-cancelled iteration's reasoning vanished the moment this slice
		// went out of scope. Persisting it here is what makes --resume able
		// to show reasoning for a tool-enabled turn too, the same as a
		// cancelled one already could; it also finally feeds
		// internal/app/agentturn.go's `case convo.BlockReasoning:` branch,
		// which was well-formed but dead code until now (see that file's own
		// comment on this history-walking loop). Ordered before the text
		// block, mirroring abortedAssistant's own block order.
		asstBlocks := make([]convo.Block, 0, 2+len(toolCalls))
		if reasoning.Len() > 0 {
			asstBlocks = append(asstBlocks, convo.ReasoningBlock(reasoning.String()))
		}
		if text.Len() > 0 {
			asstBlocks = append(asstBlocks, convo.TextBlock(text.String()))
		}
		for _, tc := range toolCalls {
			asstBlocks = append(asstBlocks,
				convo.ToolCallBlock(tc.id, tc.name, tc.args).WithSignature(tc.signature))
		}
		if len(asstBlocks) > 0 {
			asst := convo.NewMessage(convo.RoleAssistant, asstBlocks...)
			asst.Model = req.Model
			if iterUsage != nil {
				u := *iterUsage
				u.CostUSD = estimateCost(iterUsage, opts)
				asst.Usage = &u
			}
			history.Add(asst)
		}

		// Natural termination: the model produced text and asked for no tools.
		// This is the only path that yields a "complete" answer.
		if len(toolCalls) == 0 {
			result.Text = text.String()
			result.Reasoning = reasoning.String()
			return result, nil
		}

		// The model asked for tools. Run each one, append its result, and
		// loop. A budget stop happens before another tool can trigger another
		// provider request. Close every call in this batch so the saved history
		// remains valid for the next turn.
		if opts.BudgetUSD > 0 && result.CostUSD >= opts.BudgetUSD {
			result.Stopped = fmt.Sprintf("cost budget reached: estimated spend $%.4f (limit $%.4f)", result.CostUSD, opts.BudgetUSD)
			result.Text = text.String()
			result.Reasoning = reasoning.String()
			notExecuted(history, toolCalls, "cost budget reached before this call ran")
			return result, nil
		}

		// The model asked for tools. Run each one, append its result, and
		// loop. A tool error is data (§12bis): it becomes a BlockToolResult
		// with IsError and enters the context for the model to react to —
		// this is the entire mechanism by which the reactive loop handles the
		// unforeseen (§3), and the reason no Planner is needed.
		//
		// Every exit from this loop that leaves calls in toolCalls unexecuted
		// (the cap, loop detection, or a cancellation mid-batch) MUST close
		// out the remaining ones with a synthetic BlockToolResult before
		// returning. The OpenAI dialect requires that every tool_calls entry
		// on the assistant message it just persisted (above) have a matching
		// role:"tool" reply; an assistant message with an orphaned tool_call
		// makes the *next* request 400 at the provider, poisoning the session
		// for good (not just this turn) — see notExecuted below.
		for i, tc := range toolCalls {
			callsThisTurn++
			if maxCalls >= 0 && callsThisTurn > maxCalls {
				result.Stopped = fmt.Sprintf(
					"tool cap reached: %d calls this turn (limit %d). The model was still asking for tools; the last was %s.",
					callsThisTurn-1, maxCalls, tc.name)
				result.Text = text.String()
				result.Reasoning = reasoning.String()
				notExecuted(history, toolCalls[i:], "tool cap reached before this call ran")
				return result, nil
			}

			// Loop detection (§12bis): the same tool name with byte-identical
			// arguments twice *in a row across iterations* stops the loop and
			// asks the user. This is the cheap guard that catches the
			// overwhelming majority of stuck loops before the cap does.
			//
			// Bug 4: this must only ever compare a batch's *first* call
			// (i == 0) against the *previous iteration's last* call —
			// lastToolName/lastToolArgs are updated on every call below, so
			// by the time a batch finishes they hold that batch's last call,
			// exactly what the next iteration's i==0 check needs. Checking
			// at i > 0 would compare a call against its own batch-mate
			// (updated one line below on the previous trip through this same
			// for-loop), and a model asking for the same tool with the same
			// arguments twice *in parallel, in one batch* is not a stuck
			// loop — it is one decision, not a retry — so it must run.
			if i == 0 && tc.name == lastToolName && bytesEqual(tc.args, lastToolArgs) {
				result.Stopped = fmt.Sprintf(
					"loop detected: tool %q called twice in a row with the same arguments. Stopping to ask the user.",
					tc.name)
				result.Text = text.String()
				result.Reasoning = reasoning.String()
				notExecuted(history, toolCalls[i:], "loop detected before this call ran")
				return result, nil
			}
			lastToolName = tc.name
			lastToolArgs = append(lastToolArgs[:0], tc.args...)

			// toolCallEnd is this call's own OnToolCallEnd closure —
			// tc is scoped to this exact iteration of the range (Go 1.22+
			// per-iteration loop variables, matching go.mod's go 1.26.5),
			// so capturing it here is safe. Every terminal outcome below
			// (cancelled before starting, no runner bound, a runner
			// failure of every kind, or the ordinary success/failure
			// completion further down) calls this exactly once, pairing
			// it with the OnToolCallStart just below — see AgentSink's
			// own doc comment for why a Start always gets a matching End.
			toolCallEnd := func(outText string, isError bool) {
				if sink != nil && sink.OnToolCallEnd != nil {
					sink.OnToolCallEnd(tc.id, tc.name, outText, isError)
				}
			}
			if sink != nil && sink.OnToolCallStart != nil {
				sink.OnToolCallStart(tc.id, tc.name, tc.args)
			}

			runErr := ctx.Err()
			var outText string
			var isError bool
			if runErr != nil {
				// Cancelled before the tool started: record a tool error so the
				// history is faithful, then bail — and close out every call
				// still left in this batch, not just this one, or the calls
				// after it orphan the assistant message (Bug 2).
				outText = "tool run cancelled by the user"
				isError = true
				result.Aborted = true
				history.Add(convo.NewMessage(convo.RoleTool, convo.ToolErrorBlock(tc.id, tc.name, outText)))
				toolCallEnd(outText, isError)
				notExecuted(history, toolCalls[i+1:], "cancelled by the user")
				return result, runErr
			}

			// A nil Runner reaching here means the model emitted a tool call
			// even though hasTools (above) had already decided "no tools" and
			// cleared opts.Tools from the request — i.e. the model hallucinated
			// a call to something it was never offered, which real models do
			// often enough that AgentOptions.Runner's own doc comment promises
			// exactly this is handled without crashing. Without this check,
			// opts.Runner(ctx, ...) below is a nil function value and calling
			// it panics with a nil pointer dereference, taking the whole
			// process down mid-turn (Bug 1). Guarding here turns that into
			// ordinary tool-error data instead: the model sees the error and
			// can react on the next iteration, same as any other tool failure.
			if opts.Runner == nil {
				outText = truncateOutput(fmt.Sprintf("tool %q could not run: no tool runner is bound", tc.name), maxOut)
				isError = true
				result.Calls++
				history.Add(convo.NewMessage(convo.RoleTool, convo.ToolErrorBlock(tc.id, tc.name, outText)))
				toolCallEnd(outText, isError)
				continue
			}

			res, rerr := opts.Runner(ctx, tc.name, tc.args)
			if rerr != nil {
				// A runner failure (as opposed to a tool that ran and returned
				// an error) is still data: the model sees it and reacts. The
				// distinction from res.IsError is that a runner failure means
				// the tool never produced a result at all (panic, missing
				// binding, cancelled), while res.IsError means it ran and the
				// command/tool itself failed.
				if ctx.Err() != nil {
					outText = "tool run cancelled by the user"
					isError = true
					result.Aborted = true
					history.Add(convo.NewMessage(convo.RoleTool, convo.ToolErrorBlock(tc.id, tc.name, outText)))
					toolCallEnd(outText, isError)
					notExecuted(history, toolCalls[i+1:], "cancelled by the user")
					return result, ctx.Err()
				}

				// A human's refusal is the one runner failure that is NOT data
				// (§21.9 fix 1, docs/BUG-rate-limit-amplifier.md). Handing it
				// back to the model invites a variant of the same request, and
				// every variant is another provider request carrying the whole
				// grown history — the amplifier that took a real user's account
				// offline. The turn ends here instead.
				//
				//	A denial is a decision, not a hint. When the human says no,
				//	the turn ends.
				//
				// This is not the loop refusing to handle the unforeseen: the
				// user has already considered the alternatives and declined. If
				// they want one, "find another way" starts a new turn —
				// explicitly, and at human speed.
				//
				// Returning a nil error (like the cap and loop detection do) is
				// deliberate: a denial is a normal, expected outcome of a turn,
				// not a failure of the engine. The caller surfaces Stopped.
				var denied deniedHint
				if errors.As(rerr, &denied) && denied.Denied() {
					result.Stopped = fmt.Sprintf("stopped: %s", rerr.Error())
					result.Text = text.String()
					result.Reasoning = reasoning.String()
					// The refused call still needs its own tool reply before
					// the rest are closed out, or the assistant message keeps
					// an orphaned tool_call and the next request 400s (Bug 2).
					history.Add(convo.NewMessage(convo.RoleTool,
						convo.ToolErrorBlock(tc.id, tc.name, rerr.Error())))
					toolCallEnd(rerr.Error(), true)
					notExecuted(history, toolCalls[i+1:], "the turn ended when permission was refused")
					return result, nil
				}

				outText = rerr.Error()
				isError = true
			} else {
				outText = res.Text
				isError = res.IsError
			}

			outText = truncateOutput(outText, maxOut)
			result.Calls++

			var blk convo.Block
			if isError {
				blk = convo.ToolErrorBlock(tc.id, tc.name, outText)
			} else {
				blk = convo.ToolResultBlock(tc.id, tc.name, outText)
			}
			history.Add(convo.NewMessage(convo.RoleTool, blk))
			toolCallEnd(outText, isError)

			// Futility (step 26, fix 3), evaluated here because this is the
			// one place every outcome converges — a tool that ran and
			// failed, a runner failure, a hallucinated tool — so no path can
			// slip past it. Any success resets the run: progress anywhere
			// means the model is still learning from what it gets back.
			if !isError {
				futileRun = 0
				lastFailure = ""
				continue
			}
			if outText == lastFailure {
				futileRun++
			} else {
				futileRun = 1
				lastFailure = outText
			}
			if futileRun >= maxFutileAttempts {
				result.Stopped = fmt.Sprintf(
					"stopped after %d consecutive attempts failed identically (last: %s). "+
						"Trying further variants would not change the result.",
					futileRun, firstLine(outText))
				result.Text = text.String()
				result.Reasoning = reasoning.String()
				notExecuted(history, toolCalls[i+1:], "the turn ended after repeated identical failures")
				return result, nil
			}
		}

		// Loop: the next iteration reopens with the grown history.
	}
}

// estimateCost converts provider-reported token usage into USD using the
// catalog rates supplied by the caller. Unknown prices remain zero rather than
// pretending a model is free; callers can choose a conservative policy before
// invoking the loop when rates are unavailable.
func estimateCost(u *convo.Usage, opts AgentOptions) float64 {
	if u == nil {
		return 0
	}
	return float64(u.In)*opts.InputCostUSD/1e6 +
		float64(u.Out+u.Reasoning)*opts.OutputCostUSD/1e6 +
		float64(u.CacheRead)*opts.CacheReadCostUSD/1e6 +
		float64(u.CacheWrite)*opts.CacheWriteCostUSD/1e6
}

// notExecuted closes out every tool call in calls with a synthetic
// BlockToolResult (IsError) naming why it never ran. It exists so the loop
// can stop mid-batch (the cap, loop detection, or a cancellation) without
// ever leaving a tool_calls entry on the just-persisted assistant message
// without a matching role:"tool" reply — an orphaned tool_call is invalid in
// the OpenAI dialect and 400s the *next* request built from this history,
// which is a session-poisoning bug (Bug 2), not merely a cosmetic one.
func notExecuted(history *convo.Conversation, calls []toolCallOut, reason string) {
	for _, tc := range calls {
		history.Add(convo.NewMessage(convo.RoleTool,
			convo.ToolErrorBlock(tc.id, tc.name, "not executed: "+reason)))
	}
}

// toolCallOut is the engine-side view of a tool call collected from one
// iteration's stream, before it becomes a convo.BlockToolCall.
type toolCallOut struct {
	id   string
	name string
	args json.RawMessage

	// signature is the provider's opaque continuation token for this call. It
	// has to survive into the recorded BlockToolCall, because the next
	// iteration re-sends the whole history and Gemini 3 rejects a history
	// whose tool calls lost their signatures.
	signature string
}

// abortedAssistant builds the partial assistant message persisted when the user
// cancels mid-loop, mirroring Step 8's Aborted handling for a cancelled text
// turn (§7.4): --resume restores what the user saw, not nothing.
func abortedAssistant(text, reasoning string, calls []toolCallOut, model string) convo.Message {
	blocks := make([]convo.Block, 0, 2+len(calls))
	if reasoning != "" {
		blocks = append(blocks, convo.ReasoningBlock(reasoning))
	}
	if text != "" {
		blocks = append(blocks, convo.TextBlock(text))
	}
	for _, tc := range calls {
		blocks = append(blocks,
			convo.ToolCallBlock(tc.id, tc.name, tc.args).WithSignature(tc.signature))
	}
	m := convo.NewMessage(convo.RoleAssistant, blocks...)
	m.Model = model
	m.Aborted = true
	return m
}

// truncateOutput clips a tool result above max, leaving an explicit marker that
// names how much was dropped (§12bis). A 40 MB `bash` run cannot destroy the
// context window because it never reaches it. A negative or zero max (the
// latter only when a caller explicitly disables truncation for a test) passes
// the output through unchanged.
func truncateOutput(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	dropped := len(s) - max
	marker := fmt.Sprintf("\n…[truncated: %d bytes dropped — tool output exceeded the %d-byte ceiling]", dropped, max)
	// Keep the head and the marker; the head is usually enough for the model to
	// act on, and the marker tells it that there is more it can ask for
	// explicitly (e.g. with a more specific query).
	return s[:max] + marker
}

// bytesEqual compares two json.RawMessage values byte-for-byte. The loop
// detector uses it to recognize the same tool called with the same arguments
// twice in a row.
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// firstLine keeps a Stopped reason to one readable line on a 40-column
// screen (§2): tool failures are frequently multi-line, and the reason a
// turn ended should not scroll the user's own question off the display.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i]) + " […]"
	}
	const max = 120
	if len(s) > max {
		s = s[:max] + " […]"
	}
	return s
}

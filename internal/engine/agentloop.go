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
// The five semantics below are part of the contract, not implementation
// details (§12bis): the hard cap, loop detection, cancellation, error-is-data
// and output truncation. Each is small on its own; together they are what
// stops a stuck loop from burning real money on an expensive model.
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MichiTrader/ishakat/internal/convo"
)

// Defaults for the agent loop. The cap is the §12bis hard limit; the output
// ceiling is what keeps a 40 MB `bash` run from destroying the context window,
// which is the resource that is actually scarce.
const (
	defaultMaxToolCalls   = 25
	defaultMaxOutputBytes = 32 << 10 // 32 KiB
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
}

// AgentResult is the outcome of RunAgentTurn: the final assistant text, the
// accumulated usage across every iteration, how many tool calls ran, and —
// when the loop did not terminate naturally — why it stopped.
type AgentResult struct {
	Text  string
	Usage *convo.Usage
	Calls int

	// Stopped is empty when the model ended on a text turn (the natural
	// termination). It carries a short reason when the cap or loop detection
	// ended the turn early, so the caller can surface it honestly instead of
	// pretending the answer is complete.
	Stopped string

	// Aborted is true when ctx was cancelled mid-loop. The partial assistant
	// message is still in Messages and in Text; a tool already running got its
	// ctx cancelled, and write_file/edit_file's write-temp+rename (Step 15) is
	// what guarantees no half-written file survives that.
	Aborted bool
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
func (e *Engine) RunAgentTurn(ctx context.Context, req Request, opts AgentOptions, history *convo.Conversation) (AgentResult, error) {
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
	var lastToolName string
	var lastToolArgs []byte
	callsThisTurn := 0

	// The loop. One body = one model turn + its tool executions.
	for iteration := 0; ; iteration++ {
		if err := ctx.Err(); err != nil {
			result.Aborted = true
			return result, err
		}

		// Rebuild the request each iteration with the grown history. The model
		// has to see the tool calls and results from the previous iteration;
		// without them it would ask for the same tool again.
		iterReq := req
		iterReq.Messages = history.Active()
		iterReq.Tools = opts.Tools

		ch, err := e.open(ctx, iterReq)
		if err != nil {
			if ctx.Err() != nil {
				result.Aborted = true
				return result, ctx.Err()
			}
			return result, err
		}

		// Drain this iteration's stream: collect text for the final answer and
		// accumulate tool calls. Reasoning is dropped, exactly as
		// RunToCompletion drops it: nothing that calls this wants the model's
		// scratch space, only its final text and its tool requests.
		var text strings.Builder
		var reasoning strings.Builder
		var toolCalls []toolCallOut
		var iterUsage *convo.Usage
		var turnErr error
		for ev := range ch {
			switch ev.Kind {
			case EventDelta:
				text.WriteString(ev.Text)
			case EventReasoning:
				reasoning.WriteString(ev.Text)
			case EventToolCall:
				toolCalls = append(toolCalls, toolCallOut{
					id:   ev.ID,
					name: ev.Name,
					args: ev.Args,
				})
			case EventUsage:
				iterUsage = ev.Usage
			case EventError:
				if turnErr == nil {
					turnErr = ev.Err
				}
				if ev.Usage != nil {
					iterUsage = ev.Usage
				}
			case EventDone:
				if ev.Usage != nil {
					iterUsage = ev.Usage
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

		// Cancellation wins over any in-flight error, exactly as run() does for
		// a text turn (§7.4): the user hit esc, that is not a failure.
		if ctx.Err() != nil {
			// Persist the partial assistant message as Aborted, so --resume
			// restores what the user saw rather than nothing.
			if text.Len() > 0 || len(toolCalls) > 0 {
				partial := abortedAssistant(text.String(), reasoning.String(), toolCalls, req.Model)
				history.Add(partial)
				result.Text = text.String()
			}
			result.Aborted = true
			return result, ctx.Err()
		}

		if turnErr != nil {
			// A mid-stream error ends the loop. The partial text is still
			// returned so the caller can show what arrived before the failure,
			// matching run()'s own contract.
			result.Text = text.String()
			return result, turnErr
		}

		// Record the assistant turn in history: its text, its reasoning (so
		// the saved session is faithful) and its tool calls (so the dialect
		// can re-serialize them on the next iteration). Even an iteration that
		// produced only tool calls has to land in history — the next request
		// needs the assistant's tool_calls message to precede the tool results.
		asstBlocks := make([]convo.Block, 0, 1+len(toolCalls))
		if text.Len() > 0 {
			asstBlocks = append(asstBlocks, convo.TextBlock(text.String()))
		}
		for _, tc := range toolCalls {
			asstBlocks = append(asstBlocks, convo.ToolCallBlock(tc.id, tc.name, tc.args))
		}
		if len(asstBlocks) > 0 {
			asst := convo.NewMessage(convo.RoleAssistant, asstBlocks...)
			asst.Model = req.Model
			if iterUsage != nil {
				asst.Usage = iterUsage
			}
			history.Add(asst)
		}

		// Natural termination: the model produced text and asked for no tools.
		// This is the only path that yields a "complete" answer.
		if len(toolCalls) == 0 {
			result.Text = text.String()
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
				notExecuted(history, toolCalls[i:], "tool cap reached before this call ran")
				return result, nil
			}

			// Loop detection (§12bis): the same tool name with byte-identical
			// arguments twice in a row stops the loop and asks the user. This
			// is the cheap guard that catches the overwhelming majority of
			// stuck loops before the cap does.
			if tc.name == lastToolName && bytesEqual(tc.args, lastToolArgs) {
				result.Stopped = fmt.Sprintf(
					"loop detected: tool %q called twice in a row with the same arguments. Stopping to ask the user.",
					tc.name)
				result.Text = text.String()
				notExecuted(history, toolCalls[i:], "loop detected before this call ran")
				return result, nil
			}
			lastToolName = tc.name
			lastToolArgs = append(lastToolArgs[:0], tc.args...)

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
					notExecuted(history, toolCalls[i+1:], "cancelled by the user")
					return result, ctx.Err()
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
		}

		// Loop: the next iteration reopens with the grown history.
	}
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
		blocks = append(blocks, convo.ToolCallBlock(tc.id, tc.name, tc.args))
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

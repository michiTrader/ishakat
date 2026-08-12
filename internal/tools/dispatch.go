// dispatch.go is the eighth and last of §19.1's core tools: delegate a task
// to a sub-agent that runs its own tool-calling loop, in its own isolated
// history, and returns only its final text — not a transcript — to the
// parent turn that called it. §3's own CERRADA architecture decision names
// the whole mechanism in one line: "Sub-agents (dispatch, Step 22) are
// goroutines with isolated context, not a scheduler." There is no Planner
// here; dispatch is one more tool call the reactive loop can make, and the
// sub-agent it starts is itself just another reactive loop underneath.
//
// This file cannot import internal/engine (the package that actually knows
// how to run a tool-calling loop), internal/app (the one place allowed to
// import both internal/tools and internal/engine) or internal/provider —
// TestToolsNoImportaTUI's sibling rules (internal/arch_test.go) exist so a
// tool runs identically from the TUI, from -p and from serve, and importing
// the turn runner here would also risk an import cycle: internal/app is
// what builds a Registry that contains this very tool. So, exactly like
// fetch.go's HTTPClient field or Fetch's Allow/AllowAll, the actual
// capability to run a sub-agent turn is injected as a plain function value
// held on the Dispatch struct — Runner below — and internal/app is the
// package that closes over its own *engine.Engine, a fresh isolated
// *convo.Conversation and the model reference to build that closure (see
// docs/PLAN.md §19.1's own table: "dispatch | delegate to a sub-agent |
// goroutines").
package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

// SubAgentRunner runs one sub-agent turn to completion and returns its final
// text. task is the instruction the parent model wrote for the sub-agent —
// dispatch.go itself puts nothing else into the sub-agent's history, which
// is the entire meaning of "isolated context" (§3): the sub-agent does not
// see the parent conversation, only the task string it was handed, so a
// long parent history never bleeds into (or inflates the cost of) a
// delegated task that only needs a few lines of instruction.
//
// The returned string is the sub-agent's final answer, exactly the shape
// engine.AgentResult.Text already has for the parent's own turn — dispatch's
// Run wraps it in a Result, never in something structured, because the
// calling model only ever needs to read prose back, the same way it reads
// back read_file's or bash's output.
//
// A non-nil error means the sub-agent turn could not even be attempted or
// failed outright (no provider configured, the model's own context
// cancelled) — Tool.Run's own doc comment reserves a Go error for exactly
// that case, as opposed to a sub-agent that ran and produced an answer the
// parent model might still find unsatisfying, which is ordinary Result
// data, not a Go error.
type SubAgentRunner func(ctx context.Context, task string) (string, error)

// dispatchArgs is dispatch's argument shape: one instruction, nothing else.
// A sub-agent's own model, tool subset and turn limits are all decided by
// the Runner's closure (internal/app), not by the calling model — the same
// "minimal, purpose-built arguments" shape every tool in this package
// already follows (see fetch.go's doc comment on why Fetch takes a plain
// []string rather than a whole config.Egress).
type dispatchArgs struct {
	Task string `json:"task"`
}

// Dispatch is the dispatch core tool (§19.1): delegate task to a sub-agent
// and return its final answer.
//
// Danger: high. Not because dispatch itself touches the filesystem or the
// network — it does neither directly — but because whatever the sub-agent
// runs underneath is, by construction, an entire second tool-calling loop
// with its own registry, and §19.5 rule #2 says a tier is inferred from
// what a tool *can* do, never self-declared: a tool that can cause bash,
// write_file or another dispatch to run cannot be safely lower than the
// riskiest thing it might trigger. This matches permissions.Guard's own
// unknown-tool default (tierFor's High fallback) and bash's own hardcoded
// tier, so a Guard that never learns dispatch's name explicitly still
// treats it exactly this cautiously.
type Dispatch struct {
	// Runner is the injected capability that actually starts and drains a
	// sub-agent turn. nil means dispatch is registered (visible in the
	// system prompt, per §19.4) but cannot run — Run below reports that as
	// tool-error data, the same "reports the reason, lets the model react"
	// contract every other tool in this package already follows for a
	// precondition it cannot satisfy, rather than a panic or a Go error a
	// caller would have to specifically guard against.
	Runner SubAgentRunner
}

var _ Tool = Dispatch{}

func (Dispatch) Name() string   { return "dispatch" }
func (Dispatch) Danger() Danger { return DangerHigh }
func (Dispatch) Description() string {
	return "Delegate a self-contained task to a sub-agent and return its final answer. The sub-agent runs its own tool-calling loop in an isolated context — it does not see this conversation's history, only the task description given here — so use this for work that can be described completely in one instruction (e.g. \"summarize what internal/tools/fetch.go does\", \"find every TODO in the repository\"), not for anything that needs this conversation's own context to make sense."
}

func (Dispatch) Parameters() json.RawMessage {
	return objectSchema(map[string]prop{
		"task": {
			Type:        "string",
			Description: "The complete instruction for the sub-agent. Must be self-contained: the sub-agent starts with no memory of this conversation, only this string.",
		},
	}, "task")
}

func (d Dispatch) Run(ctx context.Context, rawArgs json.RawMessage) (Result, error) {
	var args dispatchArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return Result{}, fmt.Errorf("dispatch: invalid arguments: %w", err)
	}
	if args.Task == "" {
		return Result{}, fmt.Errorf("dispatch: task is required")
	}

	if d.Runner == nil {
		return ErrorResult("dispatch is not available in this session: no sub-agent runner is configured"), nil
	}

	text, err := d.Runner(ctx, args.Task)
	if err != nil {
		if ctx.Err() != nil {
			// The caller's own context was cancelled, not just something
			// the sub-agent itself failed at — surface it as a Go error so
			// the parent agent loop's cancellation path handles it, the
			// same distinction fetch.go and bash.go both already draw
			// between ctx.Err() and an ordinary operational failure
			// (§12bis: cancellation is not "the tool failed").
			return Result{}, ctx.Err()
		}
		return ErrorResult(fmt.Sprintf("sub-agent task failed: %v", err)), nil
	}
	if text == "" {
		return OKResult("(the sub-agent produced no text answer)"), nil
	}
	return OKResult(text), nil
}

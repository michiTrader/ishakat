// dispatch.go builds the tools.SubAgentRunner closure Step 22's dispatch
// tool needs (see internal/tools/dispatch.go's own doc comment for why
// internal/tools cannot build this itself: it would need to import
// internal/engine/internal/provider, which arch_test.go's boundary tests
// forbid). This is the one place that capability is actually assembled,
// closing over the parent turn's already-resolved *engine.Engine, wire
// model ID and system prompt — the same three things buildAgentOptions's
// two real call sites (app.go's Run, agentturn.go's runAgentTurnHeadless)
// already have in scope for their own top-level engine.Request.
package app

import (
	"context"
	"fmt"

	"github.com/MichiTrader/ishakat/internal/catalog"
	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/convo"
	"github.com/MichiTrader/ishakat/internal/engine"
	"github.com/MichiTrader/ishakat/internal/permissions"
	"github.com/MichiTrader/ishakat/internal/tools"
)

// newSubAgentRunner returns the tools.SubAgentRunner a Dispatch{Runner: ...}
// value needs. eng is reused exactly as-is for the sub-agent's own nested
// turn: *engine.Engine holds no mutable per-call state (a Streamer closure
// and a retry count, see engine.go's own struct), so a second,
// independent RunAgentTurn call through it is no different from two
// ordinary turns happening to use the same Engine value one after another.
//
// eng == nil (no provider resolved for this session, matching every other
// place in this package that checks it — see app.go's own buildErr
// handling) is not a programmer error here: the returned closure reports it
// as an ordinary Go error, which dispatch.go's own Run translates into
// Result.IsError=true tool data the model can react to, not a panic.
//
// model and system are the same wire ID and effective system prompt the
// caller's own top-level engine.Request already carries — a sub-agent
// answers with the conversation's own model and instructions, never a
// second, independently-resolved one, since dispatch has no argument of its
// own for either (see dispatchArgs' own doc comment: "task", nothing else).
//
// cfgTools/guard/cost/caps/hasTTY are threaded straight into a second
// buildAgentOptions call, exactly the way the caller already built its own
// top-level AgentOptions — with one deliberate difference: that inner call
// always passes a nil SubAgentRunner (dispatchRunner's own last argument),
// which is what caps dispatch's own recursion at exactly one level. A
// sub-agent gets every other tool the parent's configuration would offer
// it (same registry, same permissions.Guard, so the same approval surface
// authorizes its writes/bash calls too — nothing about being a sub-agent
// lowers the bar §19.5's tiers already set), but it can never itself see a
// "dispatch" entry in its own tool list to call, matching §3's own framing
// of dispatch as one level of delegation, not an open-ended tree of agents
// spawning agents.
//
// caps is §20.11 item 4's own addition: the same tools.Caps the parent's
// own buildAgentOptions call was built with, so a sub-agent's own
// declarative-tool visibility (which of them Manifest.Unsatisfied hides)
// matches the parent's exactly — a sub-agent answers with the same model
// as the parent (see this function's own doc comment above on model/
// system), so it must see the same set of tools that model can actually
// use, not a second, independently-derived one.
func newSubAgentRunner(
	eng *engine.Engine,
	model string,
	system string,
	cfgTools config.Tools,
	guard *permissions.Guard,
	cost *catalog.Cost,
	caps tools.Caps,
	hasTTY bool,
) tools.SubAgentRunner {
	return func(ctx context.Context, task string) (string, error) {
		if eng == nil {
			return "", fmt.Errorf("dispatch: no engine is configured for this session")
		}

		// dispatchRunner is nil here on purpose -- see this function's own
		// doc comment on why a sub-agent's own registry never contains
		// dispatch itself.
		opts, _ := buildAgentOptions(cfgTools, guard, cost, caps, hasTTY, nil)

		// The sub-agent's history starts with nothing but task -- not the
		// parent's own hist, not even the parent's most recent message.
		// This is the entire meaning of "isolated context" (§3's own
		// framing): a long parent conversation never bleeds into, or
		// inflates the cost of, a delegated task that only needs the
		// instruction dispatchArgs.Task already carries in full.
		hist := &convo.Conversation{}
		hist.Add(convo.User(task))

		req := engine.Request{Model: model, System: system}

		// RunAgentTurn already honors ctx cancellation internally (it
		// checks ctx.Err() at the top of every iteration, exactly like the
		// parent turn's own loop), so this goroutine is not strictly
		// required for correctness on the happy path -- it exists so a
		// caller whose ctx is cancelled while a sub-agent's own tool call
		// (its own bash, its own fetch) is blocked in a call this package
		// does not control gets an immediate answer here instead of
		// waiting on that call to notice cancellation on its own schedule.
		// §6.4's own budget for this whole step ("goroutines, context,
		// sync") is spent exactly here, and nowhere else in this file.
		type outcome struct {
			result engine.AgentResult
			err    error
		}
		done := make(chan outcome, 1)
		go func() {
			result, err := eng.RunAgentTurn(ctx, req, opts, hist)
			done <- outcome{result: result, err: err}
		}()

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case out := <-done:
			if out.err != nil {
				return "", out.err
			}
			return out.result.Text, nil
		}
	}
}

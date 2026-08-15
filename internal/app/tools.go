// tools.go is the boundary this session's plan names in docs/PLAN.md §12bis:
// internal/tools (Step 15) defines tools.Tool/tools.Registry without ever
// importing internal/engine (so engine's import graph never grows a
// dependency Step 21's auto-extension would have to work around), and
// internal/engine's agent loop only knows engine.ToolDef/engine.ToolRunner
// without ever importing internal/tools. internal/app already imports both,
// exactly like streamer.go already adapts provider.Event into engine.Event —
// this file is the same shape of adapter, one level up the tool-calling
// stack.
package app

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/MichiTrader/ishakat/internal/engine"
	"github.com/MichiTrader/ishakat/internal/permissions"
	"github.com/MichiTrader/ishakat/internal/tools"
)

// ToolDefsFrom converts a Registry's tools into the []engine.ToolDef a
// Request carries to the model. engine.ToolDef and tools.Tool describe the
// same three facts (name, description, JSON-schema parameters) through two
// different interfaces — one per package, so neither package has to import
// the other — and this is the straight field-by-field copy between them,
// the same pattern NewStreamer's own doc comment already establishes for
// engine.ToolDef vs. provider.ToolDef.
func ToolDefsFrom(reg *tools.Registry) []engine.ToolDef {
	ts := reg.Tools()
	if len(ts) == 0 {
		return nil
	}
	defs := make([]engine.ToolDef, len(ts))
	for i, t := range ts {
		defs[i] = engine.ToolDef{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters:  t.Parameters(),
		}
	}
	return defs
}

// ToolRunnerFrom adapts reg.Run into the engine.ToolRunner function type
// the agent loop calls without a permission gate. Production callers should use
// ToolRunnerWithGuard; this helper remains useful for focused adapter tests.
func ToolRunnerFrom(reg *tools.Registry) engine.ToolRunner {
	return ToolRunnerWithGuard(reg, nil)
}

// ToolRunnerWithGuard checks a tool request before dispatching it.
//
// A refusal takes one of two paths, and choosing between them is this
// function's whole responsibility (§21.9 fix 1,
// docs/BUG-rate-limit-amplifier.md):
//
//   - A refusal a human made, or that no human was available to make, is
//     returned as a Go **error**. engine's loop recognizes it structurally and
//     ends the turn. Nothing further in this turn could be approved, so any
//     additional provider request is pure cost.
//
//   - Every other refusal is returned as normal tool-error **data**: the model
//     receives the reason and can choose a legal alternative on the next
//     iteration, which is §12bis's error-is-data contract and the reason §3
//     needs no Planner.
//
// This function used to return every refusal as data, and its own doc comment
// stated that intent — which is precisely the defect. Returning `nil` as the
// error means "this tool ran and produced a result", so nothing upstream could
// tell that a person had said no. The model then tried variants (`ls` →
// `ls -la` → `find .`), each one a fresh provider request carrying the whole
// grown history, and a real user's account was rate-limited into an outage.
//
// The discrimination is deliberately NOT `errors.Is(err, permissions.ErrDenied)`:
// that sentinel is true for both kinds. It is the narrower Denied() contract,
// matched the same way engine matches it.
func ToolRunnerWithGuard(reg *tools.Registry, guard *permissions.Guard) engine.ToolRunner {
	return func(ctx context.Context, name string, args json.RawMessage) (engine.ToolResult, error) {
		if guard != nil {
			if err := guard.Authorize(ctx, name, args); err != nil {
				var denied interface{ Denied() bool }
				if errors.As(err, &denied) && denied.Denied() {
					// Ends the turn. The message still reaches the user and
					// the transcript through AgentResult.Stopped.
					return engine.ToolResult{}, err
				}
				return engine.ToolResult{Text: err.Error(), IsError: true}, nil
			}
		}
		res, err := reg.Run(ctx, name, args)
		if err != nil {
			return engine.ToolResult{}, err
		}
		return engine.ToolResult{Text: res.Text, IsError: res.IsError}, nil
	}
}

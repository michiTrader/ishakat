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

	"github.com/MichiTrader/ishakat/internal/engine"
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
// the agent loop calls. The two signatures already match field-for-field
// (tools.Result mirrors engine.ToolResult on purpose, per tools.Result's own
// doc comment), so this is a direct call, not a translation.
func ToolRunnerFrom(reg *tools.Registry) engine.ToolRunner {
	return func(ctx context.Context, name string, args json.RawMessage) (engine.ToolResult, error) {
		res, err := reg.Run(ctx, name, args)
		if err != nil {
			return engine.ToolResult{}, err
		}
		return engine.ToolResult{Text: res.Text, IsError: res.IsError}, nil
	}
}

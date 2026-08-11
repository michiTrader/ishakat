// ledger.go wires §19.7's "crystallization by observation" into the live
// tool-calling path: every bash/fetch call the model actually issues (the
// raw command or URL, extracted from the same JSON args reg.Run itself
// parses) is folded into the on-disk usage ledger, so gate 1
// (internal/evolve.Evaluate) eventually has real repetition evidence to
// check a proposal against, instead of only the model's own claimed
// Candidate.Repetitions field (tool_create.go's own doc comment already
// flags that field as unverified -- this file is the observation half of
// closing that gap; a second, separate change is needed on tool_create's
// own read side to actually cross-check against what this file records,
// see internal/tools/tool_create.go's TODO-shaped doc comment once that
// lands).
//
// internal/evolve.Ledger/Observe/LoadLedger/Save (internal/evolve/ledger.go)
// already implement the whole mechanism and are fully unit-tested in
// isolation; nothing outside internal/evolve called any of them before this
// file. The hook point is ToolRunnerWithGuard's own result, not reg.Run or
// Bash/Fetch themselves, for the same reason ToolRunnerWithGuard itself
// wraps reg.Run rather than reaching into the registry: this is the one
// place internal/app already sees every tool call by name with its raw
// args, after permission but before/after execution, regardless of which
// concrete Tool implementation is registered under that name (a
// declarative or future script-tool sharing the "bash"/"fetch" name would
// still be observed correctly).
package app

import (
	"context"
	"encoding/json"
	"time"

	"github.com/MichiTrader/ishakat/internal/engine"
	"github.com/MichiTrader/ishakat/internal/evolve"
)

// bashObserveArgs/fetchObserveArgs mirror bash.go/fetch.go's own unexported
// bashArgs/fetchArgs shapes, duplicated here rather than imported: this
// file only ever needs to read the one string field each tool's ledger
// entry is keyed on, never the tools' own argument validation, and
// internal/tools deliberately keeps those types unexported (tool.go's own
// doc comment: a tool's arguments are that tool's concern) so no other
// package is meant to depend on their shape. Duplicating a two-field
// struct is cheaper than widening that boundary for one field each.
type bashObserveArgs struct {
	Command string `json:"command"`
}

type fetchObserveArgs struct {
	URL string `json:"url"`
}

// rawInvocation extracts the string §19.7's ledger keys on for name's own
// argument shape -- args.Command for bash, args.URL for fetch -- reporting
// ok=false for every other tool name, or for a body that fails to decode or
// carries an empty value. reg.Run (via the concrete Tool's own Run method)
// is what actually validates a call's arguments and reports a Go error for
// a malformed one; this function's only job is deciding whether there is
// anything worth observing, so it fails silently rather than duplicating
// that validation.
func rawInvocation(name string, args json.RawMessage) (string, bool) {
	switch name {
	case "bash":
		var a bashObserveArgs
		if err := json.Unmarshal(args, &a); err != nil || a.Command == "" {
			return "", false
		}
		return a.Command, true
	case "fetch":
		var a fetchObserveArgs
		if err := json.Unmarshal(args, &a); err != nil || a.URL == "" {
			return "", false
		}
		return a.URL, true
	default:
		return "", false
	}
}

// ledgerObservingRunner wraps next so every bash/fetch call it dispatches is
// also folded into the usage ledger at path, independent of whether guard
// authorized the call or the tool itself succeeded: §19.7's worked examples
// key on what the model asked to run, not on whether that one attempt
// happened to succeed -- a denied or failed call is equally real evidence
// that the pattern was asked for again. next == nil or path == "" returns
// next unchanged (nothing to wrap, or nowhere to write), matching
// ToolRunnerWithGuard's own "nil guard just calls through" shape. now is
// injectable so tests do not depend on the real clock, the same seam
// declarative.go's own DeclarativeTool.Now field already establishes for
// this codebase; nil means time.Now.
func ledgerObservingRunner(next engine.ToolRunner, path string, now func() time.Time) engine.ToolRunner {
	if next == nil || path == "" {
		return next
	}
	if now == nil {
		now = time.Now
	}
	return func(ctx context.Context, name string, args json.RawMessage) (engine.ToolResult, error) {
		res, err := next(ctx, name, args)
		if raw, ok := rawInvocation(name, args); ok {
			observeLedger(path, raw, now())
		}
		return res, err
	}
}

// observeLedger loads path, folds raw into it dated at (UTC,
// YYYY-MM-DD, matching lifecycle.go's own today-string convention), and
// saves -- best-effort: a load or save failure (e.g. an unwritable
// $XDG_STATE_HOME, a full disk) is swallowed rather than surfaced, matching
// ledger.go's own "a ledger is disposable, best-effort memory" framing. A
// tool call the model correctly issued must never fail, nor even carry a
// visible side-effect warning, because this best-effort bookkeeping could
// not be persisted.
func observeLedger(path, raw string, at time.Time) {
	l, err := evolve.LoadLedger(path)
	if err != nil {
		return
	}
	l.Observe(raw, at.UTC().Format("2006-01-02"))
	_ = evolve.Save(path, l)
}

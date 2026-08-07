// Package tools implements docs/PLAN.md's §19 layer 1: the core tools that
// live in the binary, stdlib only. This package deliberately does not know
// about engine.ToolRunner or convo.Block — internal/app is what adapts
// Registry.Run into the function-type shape engine's agent loop calls
// (§12bis's "engine never knows what a tool is"), so this package stays out
// of engine's import graph entirely, matching TestEngineNoImportaProvider's
// sibling rule for tools in internal/arch_test.go.
//
// Step 15 (docs/PLAN.md §11) ships the first six of the eight tools named in
// §19.1's table — read_file, write_file, edit_file, bash, glob, grep — all
// pure Go against the standard library. fetch (Step 19) and dispatch (Step
// 22) are not local operations and arrive with the steps that need them.
//
// TestToolsNoImportaTUI (internal/arch_test.go) is the boundary this package
// must never cross: a tool has to run identically from the TUI, from -p, and
// from serve (§1's "third door"), so nothing here may import internal/tui or
// any Bubble Tea/Lipgloss package. A tool that needs to ask a human something
// (§19.6's permission gate, Step 16) does it through an interface the caller
// implements — never by reaching into the interface directly.
package tools

import (
	"context"
	"encoding/json"
)

// Danger is the tier §19.5's rule #2 assigns to a tool: inferred from what it
// can do, never self-declared by a manifest or a model. The eight core tools
// in this package have their tier fixed in code, one per file, so there is
// no inference to perform yet — that machinery (permission.go, Step 16) is
// for the declarative and script tools of rungs 1 and 2, where a manifest
// could otherwise claim to be safer than it is.
type Danger int

const (
	// DangerLow is read-only or otherwise reversible: read_file, glob, grep.
	DangerLow Danger = iota
	// DangerMedium changes local state but is scoped and undoable in
	// principle: write_file, edit_file.
	DangerMedium
	// DangerHigh can do anything the invoking user's shell can do, including
	// network access, process spawning and irreversible changes: bash.
	DangerHigh
)

// String names a Danger tier for display and for embedding into a tool's own
// ToolDef.Description-adjacent metadata. Never used to decide behaviour —
// only Danger's own value is.
func (d Danger) String() string {
	switch d {
	case DangerLow:
		return "low"
	case DangerMedium:
		return "medium"
	case DangerHigh:
		return "high"
	default:
		return "unknown"
	}
}

// Result is the model-visible outcome of running one tool. It mirrors
// engine.ToolResult field for field on purpose — internal/app's binding
// between Registry.Run and engine.ToolRunner is a straight copy, not a
// translation, exactly like the Streamer adapter already does for
// provider.Event/engine.Event (see internal/app/streamer.go).
type Result struct {
	// Text is what the model sees: the tool's output, or an error
	// description when IsError is true. Truncation to
	// engine's max_tool_output_bytes ceiling (§12bis) happens in the agent
	// loop, not here — a tool reports its full result and lets the one place
	// that knows the ceiling apply it.
	Text string
	// IsError marks Text as an error description rather than a normal
	// result. Per §12bis, "a tool error is data, not a failure": a non-zero
	// exit or a caught exception becomes a Result with IsError set, not a Go
	// error — the model sees it and reacts. A Go error return from Run is
	// reserved for the tool never having been able to attempt the operation
	// at all (bad arguments JSON, an unregistered name), which the caller
	// (engine's agent loop) already treats as tool-error data too, just
	// through a different field.
	IsError bool
}

// ErrorResult builds a Result whose Text is msg and IsError is true — the
// common case of "the operation failed for a reason worth telling the model
// about", spelled out once so every tool's error path reads the same way.
func ErrorResult(msg string) Result {
	return Result{Text: msg, IsError: true}
}

// OKResult builds a successful Result. Named symmetrically with ErrorResult
// rather than a bare Result{Text: text} literal so a reader scanning a tool's
// Run method sees the two possible outcomes named the same way at every
// return site.
func OKResult(text string) Result {
	return Result{Text: text}
}

// Tool is one native tool: enough to build its ToolDef (name, description,
// JSON-schema parameters — see engine.ToolDef, which this mirrors) and to run
// it against arguments the model produced.
//
// Implementations in this package take no constructor arguments beyond what
// Step 16's permission gate will need to inject (a working directory root,
// primarily, to keep a path-based tool from escaping the session's sandbox —
// see fs.go's own doc comment for why that specific guard is deferred rather
// than skipped). Until Step 16 lands, every tool trusts its arguments as far
// as the standard library itself allows and no further.
type Tool interface {
	// Name is the identifier the model calls this tool by, e.g. "read_file".
	// Stable across versions — renaming a tool the model has already learned
	// to call breaks every session that used to reference it by name.
	Name() string
	// Description is the one-sentence, model-facing explanation of what the
	// tool does. Kept in the system prompt at all times per §19.4's
	// progressive-disclosure rule (~15 tokens each) — the tool's *behaviour*
	// is documented here in Go doc comments instead, which cost nothing per
	// turn.
	Description() string
	// Danger is this tool's fixed tier (§19.5's rule #2). A native tool's
	// tier never changes at runtime.
	Danger() Danger
	// Parameters is the tool's JSON-schema parameters object, matching
	// engine.ToolDef.Parameters's own json.RawMessage shape so a Tool can be
	// turned into an engine.ToolDef (via internal/app) by copying fields,
	// not by re-deriving a schema.
	Parameters() json.RawMessage
	// Run executes the tool against args (the model's call arguments,
	// unmarshalled from the wire's JSON). A returned error means the call
	// could not even be attempted (malformed args, a precondition Run itself
	// checks before doing any work); an operation that was attempted and
	// failed is reported through Result.IsError instead — see Result's own
	// doc comment for why the two are kept apart.
	Run(ctx context.Context, args json.RawMessage) (Result, error)
}

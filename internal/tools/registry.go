// registry.go groups the native tools this package ships (Step 15) into one
// lookup-and-dispatch surface, so a caller outside this package (internal/app,
// per tool.go's own doc comment) never has to know the concrete Tool types by
// name to build the []engine.ToolDef the model sees or the engine.ToolRunner
// that executes a call. Registry itself stays ignorant of engine — it only
// deals in this package's own Tool and Result types, matching the boundary
// tool.go's doc comment draws (internal/tools never imports internal/engine).
package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

// Registry is an ordered, name-indexed set of Tools. Ordered because the
// system prompt's tool list (§19.4) is more legible when it doesn't reshuffle
// between runs; name-indexed because Run's whole job is O(1) dispatch by the
// name the model called.
type Registry struct {
	order  []Tool
	byName map[string]Tool
}

// NewRegistry builds a Registry over tools, in the order given. A later tool
// with a name already seen replaces the earlier one in byName but does not
// move its position in order — the first registration wins the slot, the
// last registration wins the lookup. This mirrors how a caller would expect
// "register my own tool under a stdlib name to override it" to behave, without
// this package needing an explicit override API for a case Step 15 doesn't
// need yet.
func NewRegistry(tools ...Tool) *Registry {
	r := &Registry{byName: make(map[string]Tool, len(tools))}
	for _, t := range tools {
		if _, seen := r.byName[t.Name()]; !seen {
			r.order = append(r.order, t)
		}
		r.byName[t.Name()] = t
	}
	return r
}

// Core builds the Registry over layer 1's eight tools (§19.1): the six
// Step 15 shipped unconditionally — read_file, write_file, edit_file, bash,
// glob, grep — plus fetch (Step 19), which alone needs constructor data of
// its own. fetch's egress allowlist lives in config.Tools.Egress; Core takes
// it apart into egressAllow/egressAllowAll rather than accepting a
// config.Tools value, for the same reason Fetch itself doesn't import
// internal/config (see fetch.go's doc comment): this package's tools take
// the minimal, purpose-built arguments a cross-cutting concern needs, not
// whole configuration types. dispatch (Step 22) will be the eighth and last
// to land.
//
// NewRegistry itself stays general so tests (and the permission-gated
// variants Step 16 already wires through internal/app) can build smaller
// registries over fakes without dragging the real set along.
func Core(egressAllow []string, egressAllowAll bool) *Registry {
	return NewRegistry(
		ReadFile{},
		WriteFile{},
		EditFile{},
		Bash{},
		Glob{},
		Grep{},
		Fetch{Allow: egressAllow, AllowAll: egressAllowAll},
	)
}

// DeclarativeTools converts DiscoverDeclarative(dir)'s findings into Tools,
// each sharing the same egress allowlist Core's own Fetch uses — a
// declarative tool's [origin] check (declarative.go's hostAllowed call) is
// the same boundary fetch's is, so both must agree on what "allowed" means
// for a given install. dir == "" (Tools.dir unset) returns a nil slice and
// an empty warn, matching DiscoverDeclarative's own no-op contract for that
// case, so a caller does not need its own branch to skip this step when
// layer 2's tool directory is not configured.
//
// This is a plain []Tool, not a *Registry, on purpose: Core's own 7-tool
// contract (TestCoreRegistersAllSevenToolsByName) must never depend on
// whether a caller also wants declarative tools, so a caller merges the two
// with NewRegistry(append(Core(...).Tools(), DeclarativeTools(...)...)...)
// rather than this function returning a competing Registry constructor.
func DeclarativeTools(dir string, egressAllow []string, egressAllowAll bool) ([]Tool, string) {
	res := DiscoverDeclarative(dir)
	if len(res.Tools) == 0 {
		return nil, res.Warn
	}
	out := make([]Tool, 0, len(res.Tools))
	for _, m := range res.Tools {
		out = append(out, DeclarativeTool{
			Manifest: m,
			Allow:    egressAllow,
			AllowAll: egressAllowAll,
		})
	}
	return out, res.Warn
}

// WithDeclarative builds a Registry over Core's own seven tools plus every
// tool DeclarativeTools(dir, ...) finds, in that order — native tools first,
// matching §19.1's own layer ordering (layer 1 before layer 2). A manifest
// naming a native tool (e.g. a tool.toml with name = "fetch") loses the
// Lookup/Run slot to it per NewRegistry's own last-registration-wins rule,
// but keeps its position in Tools() at the native tool's spot — an unlikely
// case Step 20 does not need to guard against further, since §19.5's own
// area (danger inference) does not depend on name collisions being
// impossible, only on danger never being under-counted.
func WithDeclarative(egressAllow []string, egressAllowAll bool, declarativeDir string) (*Registry, string) {
	reg := Core(egressAllow, egressAllowAll)
	extra, warn := DeclarativeTools(declarativeDir, egressAllow, egressAllowAll)
	if len(extra) == 0 {
		return reg, warn
	}
	return NewRegistry(append(reg.Tools(), extra...)...), warn
}

// Tools returns the registered Tools in registration order. The caller (§12bis
// bindTools in internal/app) walks this once at boot to build the
// []engine.ToolDef the model sees; nothing here mutates after construction, so
// the slice is safe to hold onto.
func (r *Registry) Tools() []Tool {
	if r == nil {
		return nil
	}
	return r.order
}

// Lookup returns the Tool registered under name, and whether one was found.
func (r *Registry) Lookup(name string) (Tool, bool) {
	if r == nil {
		return nil, false
	}
	t, ok := r.byName[name]
	return t, ok
}

// Run dispatches to the Tool registered under name, matching the shape
// engine.ToolRunner needs (see internal/app's binding). An unrecognized name
// is a Go error, not a Result{IsError: true} — the model asked for a tool that
// does not exist, which is the same "could not even attempt the operation"
// case tool.go's Run doc comment reserves for a Go error, not tool-level
// failure data.
func (r *Registry) Run(ctx context.Context, name string, args json.RawMessage) (Result, error) {
	t, ok := r.Lookup(name)
	if !ok {
		return Result{}, fmt.Errorf("tools: no tool registered under name %q", name)
	}
	return t.Run(ctx, args)
}

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
	"strings"

	"github.com/MichiTrader/ishakat/internal/evolve"
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
// whole configuration types. dispatch (Step 22, the eighth and last of the
// eight) does not land here: unlike fetch's egress allowlist, dispatch's
// injected capability (a SubAgentRunner) has nothing to do with Core's own
// "engine-agnostic, network-agnostic native six" contract, so it is added
// by WithMetaTools instead, gated on MetaToolsOptions.DispatchRunner rather
// than on the layer-2 tools directory every other thing WithMetaTools adds
// is gated on.
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

// MetaToolsOptions decides which of §19.5's five meta-tools WithMetaTools
// adds on top of WithDeclarative's own catalogue. The gating logic itself
// belongs here rather than in internal/app, because "which meta-tools does
// this install even offer" is a question about the tool layer's own
// governance (§19.6/§19.7), not about wiring one call site — a future
// second caller (e.g. a `/tools` TUI surface that lists what could be
// created) reads the identical decision by calling this function again,
// rather than re-deriving it.
type MetaToolsOptions struct {
	// Dir is the same layer-2 tools directory every meta-tool takes. Empty
	// means "no tools directory configured" -- tool_list/probe/edit/archive/
	// revive/delete have nothing to act on yet, so none of the seven are
	// added at all (matching DeclarativeTools' own "dir == \"\" is a no-op"
	// contract): offering tool_create with no Dir to write into would let
	// gate 1 pass and then fail on the actual write, which is a worse
	// failure mode than never offering it.
	Dir string

	// Allow/AllowAll are the same egress allowlist Core's own Fetch and
	// DeclarativeTools already take, reused by tool_probe (a probe's own
	// real HTTP call) and tool_create/tool_edit (checked against the
	// manifest a write would produce, §19.8 mitigation 4).
	Allow    []string
	AllowAll bool

	// EvolveMode is config.Evolve.Mode's raw string ("off" | "on_request" |
	// "suggest" | "auto"), taken as a plain string rather than an
	// evolve-package type so this package still never imports
	// internal/config (see Core's own doc comment on why Fetch's egress
	// allowlist is unpacked the same way). "off" is the one value with a
	// registry-shape consequence, spelled out verbatim in §19.7's own
	// table: "`tool_create` is absent from the registry" -- not merely
	// refused at call time, absent, so a model reading its own tool list
	// under `off` sees no hint that self-extension exists at all. Every
	// other value (including unrecognized ones, and the Go zero value "")
	// behaves like "on_request" here: gate 1/2/3 and this file's own
	// TTY rule are still the real gate on whether a call succeeds;
	// on_request/suggest/auto only differ in whether the agent may
	// *propose* it unprompted, a civility question §19.7 leaves entirely
	// to the system prompt and the agent's own judgement, not to whether
	// tool_create exists to be called.
	EvolveMode string

	// AllowWithoutTTY is config.Evolve.AllowWithoutTTY -- must stay false
	// in every real install per that field's own doc comment, flipped only
	// by a human writing allow_without_tty = true into config.toml itself
	// (a persistent, install-wide override). The equivalent per-invocation
	// escape hatch is --allow-tool-create (cmd/ishakat/main.go), which
	// internal/app.buildAgentOptions instead feeds straight into HasTTY
	// below -- see runAgentTurnHeadless's own doc comment for why the CLI
	// flag substitutes for HasTTY rather than for this field. Threaded
	// through as a plain bool for the same import-boundary reason
	// EvolveMode is a string.
	AllowWithoutTTY bool

	// HasTTY reports whether a human is actually present to authorize
	// gate 2 for a tool_create call -- §19.6's own rule, quoted in
	// docs/PLAN.md §19.7 verbatim: "With no TTY, tool_create is denied.
	// Full stop." The caller (internal/app) is the one place that already
	// knows this (term.IsTerminal, threaded through headless.go/app.go),
	// so this field is the same "minimal, purpose-built argument" this
	// package's whole API already prefers over accepting a config.Config
	// or reaching for os.Stdout itself.
	HasTTY bool

	// Thresholds is gate 1's own configuration, passed straight through to
	// ToolCreate -- see ToolCreate.Thresholds' own doc comment for why a
	// zero value is still a fully-defined, documented default rather than
	// a caller error.
	Thresholds evolve.Thresholds

	// LedgerPath is passed straight through to ToolCreate.LedgerPath --
	// see that field's own doc comment. Empty (the zero value) means "no
	// ledger configured", matching every caller's behavior before this
	// field existed.
	LedgerPath string

	// DispatchRunner is the sub-agent-turn capability Dispatch.Runner needs
	// (Step 22, §19.1's eighth and last core tool) -- see dispatch.go's own
	// doc comment for why this package cannot build that capability itself
	// (it would need to import internal/engine/internal/app/internal/
	// provider, which arch_test.go's boundary tests forbid). nil means
	// "no sub-agent capability wired for this session": dispatch is not
	// added to the returned Registry at all, the same "absent, not merely
	// denied" shape §19.7 already uses for tool_create under Mode == "off"
	// -- a model that never sees dispatch in its own tool list cannot be
	// talked into asking for it. internal/app is the only real caller that
	// sets this field, closing over its own *engine.Engine/provider and a
	// fresh *convo.Conversation to build the closure.
	DispatchRunner SubAgentRunner
}

// WithMetaTools builds a Registry over WithDeclarative's own catalogue plus
// whichever of §19.5's meta-tools opts.Dir/EvolveMode/HasTTY currently
// allow, in the fixed order tool_list, tool_probe, tool_create, tool_edit,
// tool_archive, tool_revive, tool_delete -- alphabetical by lifecycle stage
// (list before probe before create before edit before the two archive-state
// transitions before delete), not alphabetical by name, so the system
// prompt's own tool list reads in the same "what exists, then the ways to
// change it, then remove it" order §19.5's own table states them in.
// tool_archive/tool_revive sit between tool_edit and tool_delete because
// neither changes a tool's content (tool_edit's job) nor removes it
// (tool_delete's job) -- they only move it along the lifecycle diagram's
// "unused N days -> archived" edge and back, which is closer to tool_probe's
// own "moves the state, touches nothing else" shape than to either
// neighbor's.
//
// tool_list/tool_probe/tool_edit/tool_archive/tool_revive/tool_delete are
// added whenever opts.Dir is set, with no further gate: all six are
// read-only or act only on a tool that already exists on disk, the same
// "acting on what is already there changes nothing new" reasoning
// tool_list.go's own doc comment states for itself, and tool_probe/
// tool_edit/tool_archive/tool_revive/tool_delete's own Danger()/
// Description() doc comments make the identical case for a self-test, a
// targeted string replacement, an archive, a revive and a confirmed
// deletion respectively -- none of the three governance concerns (§19.6's
// gates, §19.7's Mode dial, §19.8's threat model) exists to guard "may this
// agent look at, quiet down or remove a tool a human or an earlier turn
// already wrote", only "may it acquire a brand new capability", which is
// tool_create's question alone.
//
// dispatch (Step 22, §19.1's eighth and last core tool) is added, ahead of
// every meta-tool, whenever opts.DispatchRunner != nil -- regardless of
// opts.Dir, since dispatch has nothing to do with the layer-2 tools
// directory the meta-tools and declarative discovery both act on. Like
// tool_create under Mode == "off", a nil DispatchRunner omits dispatch from
// the registry entirely rather than adding a tool that would always fail:
// see MetaToolsOptions.DispatchRunner's own doc comment for why this
// package cannot build that capability itself.
//
// tool_create is added only when both of §19.6/§19.7's own conditions hold:
// opts.EvolveMode != "off" (§19.7's table, verbatim: "off" means
// "tool_create is absent from the registry", not merely refused), and a
// human is actually present to authorize gate 2 (opts.HasTTY, or
// opts.AllowWithoutTTY -- config.toml's persistent override -- or a caller
// passing --allow-tool-create's value as opts.HasTTY itself, which is what
// internal/app.buildAgentOptions/runAgentTurnHeadless actually do; this
// package stays agnostic to which of the two the caller used). Failing
// either condition omits tool_create from the returned Registry entirely --
// the same "absent, not
// merely denied" shape §19.7 states for Mode == "off", extended here to the
// TTY case for the identical reason: a model that cannot see a tool in its
// own catalogue cannot be talked into asking for it by anything in its
// context, where a tool that exists but always errors is still a standing
// invitation a sufficiently adversarial prompt could keep proposing.
func WithMetaTools(opts MetaToolsOptions) (*Registry, string) {
	reg, warn := WithDeclarative(opts.Allow, opts.AllowAll, opts.Dir)

	if opts.DispatchRunner != nil {
		reg = NewRegistry(append(reg.Tools(), Dispatch{Runner: opts.DispatchRunner})...)
	}

	if strings.TrimSpace(opts.Dir) == "" {
		return reg, warn
	}

	extra := []Tool{
		ToolList{Dir: opts.Dir},
		ToolProbe{Dir: opts.Dir, Allow: opts.Allow, AllowAll: opts.AllowAll},
	}

	if !strings.EqualFold(strings.TrimSpace(opts.EvolveMode), "off") && (opts.HasTTY || opts.AllowWithoutTTY) {
		extra = append(extra, ToolCreate{
			Dir:        opts.Dir,
			Allow:      opts.Allow,
			AllowAll:   opts.AllowAll,
			Thresholds: opts.Thresholds,
			LedgerPath: opts.LedgerPath,
		})
	}

	extra = append(extra,
		ToolEdit{Dir: opts.Dir, Allow: opts.Allow, AllowAll: opts.AllowAll},
		ToolArchive{Dir: opts.Dir},
		ToolRevive{Dir: opts.Dir},
		ToolDelete{Dir: opts.Dir},
	)

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

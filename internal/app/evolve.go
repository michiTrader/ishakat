// evolve.go translates config.Evolve/config.Tools.MaxTools and a live
// *tools.Registry into internal/evolve's own Thresholds/[]ExistingTool
// shapes — the same "minimal, purpose-built argument" boundary
// buildAgentOptions (agentturn.go) already draws for tools.Fetch's egress
// allowlist and tools.WithDeclarative's directory/allowlist arguments.
// internal/evolve stays ignorant of config and tools so its gate 1 logic
// stays trivially unit-testable without a real registry or config file in
// play (see internal/evolve's own doc comment); this file is where the two
// sides meet, exactly like agentturn.go is for the tools package.
//
// capsForTools (below) draws the identical boundary one layer over, for
// §20.11 item 4: internal/tools.Caps out of a resolved catalog.Model,
// field by field, the same translation CapsFor (caps.go) already performs
// for provider.Caps.Tools -- internal/tools must never import
// internal/catalog (Caps' own doc comment), so this file is where a real
// model's capabilities become the minimal vector tools.Manifest.Unsatisfied
// checks against.
package app

import (
	"github.com/MichiTrader/ishakat/internal/catalog"
	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/evolve"
	"github.com/MichiTrader/ishakat/internal/tools"
)

// evolveThresholds builds evolve.Thresholds from cfg's own two relevant
// tables. cfgTools.MaxTools feeds Thresholds.MaxTools directly (§19.6's
// budget criterion is stated per-tool-count, not per-Evolve-table, which is
// why it lives on config.Tools rather than config.Evolve); cfgEvolve's
// MinRepeats/DedupThreshold map straight across. MaxVaryingArgs has no
// TOML knob yet (see evolve.Thresholds' own doc comment) so it is left at
// its Go zero value, which evolve.Evaluate's own normalization step fills
// from evolve.DefaultThresholds() -- the same "a caller that forgets a
// field gets §19.6's documented default" contract Thresholds' doc comment
// promises.
func evolveThresholds(cfgTools config.Tools, cfgEvolve config.Evolve) evolve.Thresholds {
	return evolve.Thresholds{
		MinRepeats:     cfgEvolve.MinRepeats,
		DedupThreshold: cfgEvolve.DedupThreshold,
		MaxTools:       cfgTools.MaxTools,
	}
}

// existingToolsFrom lists reg's own tools as evolve.ExistingTool values, for
// gate 1's dedup and budget checks. reg == nil (a caller that has not built
// a registry yet, e.g. a config-only dry run) returns nil rather than
// panicking -- an empty catalogue is a legitimate, if unusual, starting
// state, not a caller error.
//
// Every tool currently returned by (*tools.Registry).Tools() counts toward
// the budget check today, native and declarative alike -- §19.6's own text
// only carves out archived tools ("archived tools do not count against
// it"), and Step 20's Registry has no notion of archived yet (that is
// Step 21's own lifecycle work, §19.5). When archival lands, this function
// is where the carve-out belongs: it is already the single place that
// turns "what the registry currently holds" into "what gate 1 sees".
func existingToolsFrom(reg *tools.Registry) []evolve.ExistingTool {
	if reg == nil {
		return nil
	}
	all := reg.Tools()
	out := make([]evolve.ExistingTool, 0, len(all))
	for _, t := range all {
		out = append(out, evolve.ExistingTool{Name: t.Name(), Description: t.Description()})
	}
	return out
}

// capsForTools translates a resolved catalog.Model's real capabilities and
// context window into the tools.Caps vector Manifest.Unsatisfied checks a
// declarative tool.toml's requires_caps/min_context against (§20.11 item
// 4). Field-by-field, exactly like config.Egress -> Fetch.Allow/AllowAll
// already is (tools.Caps' own doc comment): both catalog.Caps and
// tools.Caps declare the same six boolean names on purpose, so this
// function is a straight copy, not a remapping.
//
// m.EffectiveContext() (never m.Context directly) feeds Context, matching
// engine.CheckSwap's own missingCaps/window comparison, which already uses
// EffectiveContext for the identical "what window does this model actually
// offer" question (hotswap.go): a model with an unknown Context still gets
// catalog.ContextFloor's conservative estimate rather than reading as
// Unsatisfied's own "unknown, never fails min_context" case -- the same
// choice CheckSwap already made, applied consistently here rather than
// re-litigated per caller.
//
// A zero-value catalog.Model (e.g. a ref the local catalog has never heard
// of, the same "unknown trusts the explicit opt-in" case CapsFor's own doc
// comment describes) produces a zero-value tools.Caps, which satisfies
// every manifest that declares no requires_caps/min_context of its own --
// the common case, and every tool.toml written before this item existed.
func capsForTools(m catalog.Model) tools.Caps {
	return tools.Caps{
		Tools:       m.Caps.Tools,
		Vision:      m.Caps.Vision,
		Reasoning:   m.Caps.Reasoning,
		Streaming:   m.Caps.Streaming,
		JSONSchema:  m.Caps.JSONSchema,
		Attachments: m.Caps.Attachments,
		Context:     m.EffectiveContext(),
	}
}

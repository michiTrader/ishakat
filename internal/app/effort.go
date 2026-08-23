// effort.go is F9's per-dialect wire mapping (docs/ROADMAP-ux-2026-08-20.md,
// W5): the function that turns "the user asked for effort level X" into the
// engine.Request.Params override that actually reaches the request body for
// the model's own dialect.
//
// This exists as its own small file, not a method on ModelRef or a case
// inline in root.go/headless.go, for the same reason CapsFor and
// ReasoningWanted are their own functions in caps.go: every call site (the
// interactive engine, the headless `-p` path, `serve.go`'s WebSocket door,
// and any sub-agent dispatch that ends up wanting its own effort) must reach
// the exact same three-way dialect switch, or a future site will reinvent it
// slightly differently and the "some routes honour /effort and some
// silently don't" bug becomes possible again — precisely the failure class
// caps.go's own doc comment describes for tools/reasoning.
//
// # Wire shape per dialect (confirmed against each provider's own docs)
//
//   - openai (and the "responses" alias): a flat top-level field,
//     "reasoning_effort", whose value is the level string verbatim
//     (openai.com's own reasoning_effort levels, and — this is the important
//     shared case — Google's OpenAI-compatible shim at
//     generativelanguage.googleapis.com/v1beta/openai/ *also* accepts this
//     same flat field and maps it internally onto thinkingLevel/
//     thinkingBudget behind the scenes: see
//     https://ai.google.dev/gemini-api/docs/openai#thinking's own mapping
//     table. So a Gemini model reached through the openai dialect (the
//     "gemini-direct" preset's default kind, per credentials.go) uses this
//     same flat key, NOT the nested gemini-native one below.
//   - gemini (the native generateContent/streamGenerateContent adapter):
//     a nested field, "generationConfig.thinkingConfig.thinkingLevel",
//     confirmed via https://ai.google.dev/gemini-api/docs/generate-content/thinking
//     — the generateContent-specific view of Google's own docs (not the
//     newer Interactions API view, which uses a different endpoint and a
//     different snake_case field name). Values are lowercase strings
//     matching the model's own EffortLevels vocabulary verbatim: "minimal",
//     "low", "medium", "high" (that same page's own curl example shows
//     "thinkingLevel": "low"). Gemini 2.5-series models don't support this
//     field at all — they only take a numeric thinkingBudget — but they also
//     never populate catalog.Model.EffortLevels with named levels (models.dev
//     tags their reasoning_options as "budget_tokens", never "effort" — see
//     MDModel.EffortLevels's own doc comment), so a model whose EffortLevels
//     is non-empty is, by construction, one that accepts this field.
//   - anthropic (the native Messages API adapter): a nested field,
//     "output_config.effort", confirmed via Anthropic's own docs
//     (platform.claude.com/docs/en/build-with-claude/effort) and its own
//     curl examples (`"output_config": {"effort": "high"}`). Values are
//     "low", "medium", "high", "xhigh", "max" — again the model's own
//     EffortLevels string verbatim, no translation. This is a genuinely
//     different axis from `thinking.budget_tokens` (extended thinking's own
//     token cap): effort shapes the whole response, thinking shapes only
//     the reasoning phase, and the two compose rather than substitute for
//     each other (effort's own doc: "it works alongside budget_tokens").
//     Nothing here touches budget_tokens; that is a separate, not-yet-built
//     concern this field's own name does not cover.
//
// # Why no numeric translation table
//
// Every dialect above takes the *level string itself* — never a numeric
// budget the level has to be translated into. This is precisely why
// EffortParams needs no per-model lookup table of its own: the level a user
// picks (already constrained to catalog.Model.EffortLevels's own per-model
// vocabulary by whatever picks it — the /effort command, the cycle chord)
// is the value that goes on the wire, unchanged, just addressed at a
// different key per dialect. If some future dialect only accepts a numeric
// budget (Gemini 2.5's thinkingBudget, mentioned above, is exactly this
// shape, but no live catalog model exposes it through EffortLevels so this
// function is never asked to handle it), that would need a real per-model
// numeric table — deliberately out of scope until a live case demands it,
// per the same "don't build unneeded code paths" discipline
// ToolsSupported/CapsFor already follow.
package app

import (
	"strings"

	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/tui"
)

// EffortParams returns the engine.Request.Params entry that asks kind's
// dialect for effort level, or nil if level is empty (nothing to ask for —
// callers should simply not set Params in that case) or kind names a
// dialect this function does not know how to address.
//
// kind is the provider's own dialect (config.Provider.Kind, already
// lower-cased the way wiring.Settings lower-cases it — see FindProvider's
// caller in whatever assembles ModelRef+provider.Kind together), not the
// provider id: "gemini-direct" is a preset id whose default kind is
// "openai" (its OpenAI-compatible shim), and only a hand-written
// kind = "gemini" in the TOML reaches this function's "gemini" branch.
//
// An unrecognized kind returns nil rather than an error: F9 is additive
// (see engine.Request.Params's own doc comment on the escape hatch this
// builds on) and a provider dialect this function has not been taught about
// yet must not fail the turn — it should simply not carry an effort
// override, exactly as if the user had never asked. This mirrors
// ToolsSupported's "the catalog has never heard of ref → true, with no
// reason" branch: silence, not refusal, for the unknown case.
func EffortParams(kind, level string) map[string]any {
	level = strings.TrimSpace(level)
	if level == "" {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "", "openai", "responses":
		// The empty-kind case mirrors provider.New's own default ("el
		// dialecto por defecto de §5.2" — registry.go): an unset
		// [[provider]].kind in the TOML is the openai dialect, so a
		// model reached through it must resolve effort the same way an
		// explicit kind = "openai" would, not fall through to "unknown
		// dialect, no override".
		return map[string]any{"reasoning_effort": level}
	case "gemini":
		return map[string]any{
			"generationConfig.thinkingConfig.thinkingLevel": level,
		}
	case "anthropic":
		return map[string]any{"output_config.effort": level}
	default:
		return nil
	}
}

// EffortParamsFor is EffortParams's convenience form for a call site that
// already has the config.Provider (rather than just its Kind string) and a
// level: it exists so root.go/agentturn.go/headless.go don't each have to
// spell out FindProvider's own lower-casing convention a fourth time.
//
// pc is passed by value (config.Provider is already a copied-by-value
// struct everywhere else it travels — see FindProvider's own doc comment),
// so a zero-value config.Provider{} (found == false at the call site) is a
// safe, ordinary argument here: its Kind is "", which EffortParams's own
// empty-kind case already treats as the openai dialect default, exactly
// matching what provider.New would do with the same zero value.
func EffortParamsFor(pc config.Provider, level string) map[string]any {
	return EffortParams(pc.Kind, level)
}

// NewEffortResolver returns a tui.EffortResolver closed over cfg: the real
// implementation of effortcmd.go's own seam, walking the exact same
// ResolveModel/FindProvider path NewEngineFactory already uses for the
// same ref, so a turn's effort override and the engine it runs on can
// never disagree about which provider dialect they are both addressing.
//
// A ref that fails to resolve (a disabled/undeclared provider, a stale Ref
// left over from before a config edit) returns nil rather than an error —
// mirroring EffortParams' own "silence, not refusal" rule for the
// unrecognized-dialect case: F9 is additive, so a turn must never fail
// just because its effort override could not be resolved.
func NewEffortResolver(cfg *config.Config) tui.EffortResolver {
	return func(ref, level string) map[string]any {
		r, err := ResolveModel(cfg, ref)
		if err != nil {
			return nil
		}
		pc, ok := FindProvider(cfg, r.Provider)
		if !ok {
			return nil
		}
		return EffortParamsFor(pc, level)
	}
}

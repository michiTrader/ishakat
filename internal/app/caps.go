// caps.go decides the provider.Caps a turn is sent with — the single fact
// that decides whether the `tools` array reaches the wire at all.
//
// It exists because of a Step 16 bug that made the whole tool layer look
// implemented and do nothing: BuildEngine and NewEngineFactory both built
// their streamer with a bare provider.Caps{}, whose Tools field is false,
// and the OpenAI dialect only serializes the `tools` array when
// req.Caps.Tools is true (see openai.go's own comment on that check). So an
// interactive session with [tools].enabled = true offered the model no
// tools whatsoever: it could not call write_file even when asked point
// blank, and answered with prose explaining which shell command the user
// should run instead. No tool call meant no Guard.Authorize, which meant no
// Reviewer.Review, which meant the approval overlay Step 16 exists to draw
// could never open. Every unit test still passed, because each half of the
// bridge was tested against a fake on its own side of the seam and nothing
// asserted on what the request body actually carried.
//
// The rule this file implements: tools reach the wire only when the user
// asked for them ([tools].enabled) *and* the target model is not known to
// lack support for them. The catalog is authoritative when it has an entry;
// an unknown model trusts the explicit opt-in rather than silently
// downgrading, since silent downgrade is exactly the failure above.
package app

import (
	"fmt"
	"strings"

	"github.com/MichiTrader/ishakat/internal/catalog"
	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/provider"
)

// ToolsSupported reports whether ref may be offered tools, and why not when
// the answer is no.
//
// cat is the local snapshot (never the network — §4.4), and a nil or empty
// catalog is an ordinary case, not an error: a first run has no cache yet.
// The three outcomes are deliberately distinct:
//
//   - the catalog knows ref and says Caps.Tools → true.
//   - the catalog knows ref and says it has no tool support → false, with a
//     reason worth printing. This is the one case where refusing is right:
//     sending `tools` to a model that rejects it turns every turn into a
//     400 for a capability the user cannot use anyway.
//   - the catalog has never heard of ref → true, with no reason. An
//     explicit [tools].enabled is an instruction, and a missing catalog row
//     is ignorance rather than evidence of absence; guessing "no" here is
//     what produced a tool layer that silently did nothing. If the guess is
//     wrong the service says so out loud, which is strictly better than a
//     model that quietly cannot act.
func ToolsSupported(cat *catalog.Catalog, ref string) (bool, string) {
	model, found := cat.Get(ref)
	if !found {
		return true, ""
	}
	if model.Caps.Tools {
		return true, ""
	}
	return false, fmt.Sprintf(
		"[tools] enabled = true, but the catalog says %q has no tool-calling support; "+
			"this turn runs without tools (pick a model tagged `tools` with ctrl+p, "+
			"or run `ishakat models --refresh` if you believe the catalog is stale)", ref)
}

// ReasoningWanted reports whether a turn should ask the service to narrate the
// model's reasoning, which is what fills the preview the interface draws under
// each answer.
//
// It reads [ui].reasoning and nothing else, and the empty string means
// "collapsed" — the documented default in defaults.toml, not "off". A missing
// key is an unconfigured install, and an unconfigured install is exactly the
// one that reported seeing no reasoning at all.
//
// The catalog's Caps.Reasoning is deliberately NOT consulted, which is the
// opposite of the choice ToolsSupported makes above, for two reasons. Sending
// `tools` to a model that rejects them turns every turn into a 400, so there
// the catalog's "no" is worth obeying; asking a non-thinking model for thought
// summaries costs nothing and returns nothing, so a stale or missing catalog
// row would only recreate the silent-nothing failure this whole change exists
// to remove. And the dialects already narrow the blast radius themselves: the
// OpenAI adapter only sends Google's opt-in to a Google host.
//
// It is also not a cost decision, which is the objection this would otherwise
// invite. A thinking model thinks either way and bills those tokens either way
// (Gemini reports thoughtsTokenCount whether or not the request opts in); the
// flag only decides whether the user is allowed to read what they already paid
// for. Turning it off saves nothing and hides something.
func ReasoningWanted(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	return strings.TrimSpace(strings.ToLower(cfg.UI.Reasoning)) != "off"
}

// CapsFor builds the provider.Caps for a turn against ref.
//
// wantTools is the caller's own intent, not the configuration: the
// conversation's engine passes cfg.Tools.Enabled, while compact_model's
// separate engine passes false — summarizing a conversation has no business
// offering the model a way to write files (§10).
//
// Images and Reasoning stay at their zero value on purpose. Caps.Images
// only selects between two flattening messages today (serialize.go: real
// image parts are Phase 3 work, and both branches count the same
// Degradation.ImagesDropped either way), and no dialect reads
// Caps.Reasoning at all. Setting either one now would change wire output
// for a capability that is not implemented, which is how a "harmless"
// widening becomes the next silent bug. Tools is the one field that
// actually gates a request body today, so it is the one field this function
// decides.
//
// Asking for reasoning is deliberately not expressed here even though it
// looks like a capability, because it is not one: Caps describes what the
// model can do, while wanting to *read* the thinking is a preference of the
// person watching ([ui].reasoning). It travels as Request.IncludeReasoning
// instead — see ReasoningWanted and NewStreamer.
func CapsFor(cfg *config.Config, cat *catalog.Catalog, ref string, wantTools bool) (provider.Caps, string) {
	if !wantTools || !cfg.Tools.Enabled {
		return provider.Caps{}, ""
	}
	ok, reason := ToolsSupported(cat, ref)
	return provider.Caps{Tools: ok}, reason
}

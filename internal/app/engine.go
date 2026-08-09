// engine.go is Step 8's item (a): resolving the default model/provider and
// building the *engine.Engine that internal/tui.Root needs to run real
// turns, in place of the Step 3 mannequin (root.go's former
// pendingEcho/driveEcho).
//
// It deliberately reuses the exact same resolution path headless.go's step 4
// already walks (ResolveModel, FindProvider, NewProvider, SystemPrompt): the
// two entry points must agree on what "no -m given" means, and duplicating
// that logic here would be the first place they drift apart.
package app

import (
	"fmt"

	"github.com/MichiTrader/ishakat/internal/catalog"
	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/engine"
	"github.com/MichiTrader/ishakat/internal/provider"
	"github.com/MichiTrader/ishakat/internal/tui"
)

// BuildEngine resolves modelText (the -m/--model flag, or "" to fall back to
// app.default_model, same rule as ResolveModel) against cfg, builds the
// concrete provider.Provider it points at, and wraps it in an *engine.Engine
// via NewStreamer.
//
// Returns the resolved reference (its Ref is what a caller should show in
// the banner/footer, never WireID), the effective system prompt (§5.2's
// file-wins-over-inline rule already applied), a warning worth printing
// without aborting startup (either an unreadable system_prompt_file, same
// as Headless's own step 4, or P2's boot fallback notice below — "" means
// nothing to say), and err.
//
// err is fatal, exactly as it is in Headless's own step 4: there is nothing
// for the TUI to open a turn against, so the caller must report it and exit
// rather than start the interface with an engine that can never work.
//
// modelText == "" is resolved through ResolveModelForBoot rather than
// ResolveModel directly — this is P2: when app.default_model/compact_model
// itself is disabled or has no working credential, and some other declared
// provider does, BuildEngine now falls back to that provider automatically
// instead of returning err (and the caller starting with eng = nil, one
// ctrl+p away from working) for a configuration mistake this package can
// route around on its own. That silent-looking recovery is exactly why it
// is reported: warn carries one line naming what changed, prefixed
// "using X instead: ", so a caller printing it verbatim never has to know
// this happened to say something sensible about it. An explicit
// modelText (a real -m/--model, or a real compact_model, never "") keeps
// going through ResolveModel's ordinary, non-fallback path — see
// ResolveModelForBoot's own doc comment for why a caller-supplied choice
// is never second-guessed the way an unresolved default is.
//
// cat and wantTools are what decide provider.Caps (see CapsFor): together
// they are the fix for the Step 16 bug where Caps was hard-coded to its
// zero value here, which made the OpenAI dialect drop the `tools` array
// from every request and left [tools].enabled = true doing literally
// nothing — no tool call, therefore no permission check, therefore no
// approval overlay. cat may be nil (a first run with no cache); wantTools
// is the caller's intent, and the conversation's engine is the only one
// that passes true — compact_model's own engine must never be handed tools
// (§10: summarizing is not acting).
//
// Images/Reasoning stay at the zero value; CapsFor's own comment explains
// why widening them today would change wire output for capabilities that
// are not implemented yet.
func BuildEngine(cfg *config.Config, cat *catalog.Catalog, modelText, version string, wantTools bool) (eng *engine.Engine, ref ModelRef, system, warn string, err error) {
	var fb *BootFallback
	ref, fb, err = ResolveModelForBoot(cfg, cat, modelText)
	if err != nil {
		return nil, ModelRef{}, "", "", err
	}
	prov, err := providerFor(cfg, ref, version)
	if err != nil {
		return nil, ModelRef{}, "", "", err
	}

	system, warn = SystemPrompt(cfg)
	if fbLine := fb.Describe(); fbLine != "" {
		if warn == "" {
			warn = fbLine
		} else {
			warn = fbLine + "; " + warn
		}
	}

	caps, capsWarn := CapsFor(cfg, cat, ref.Ref, wantTools)
	if capsWarn != "" {
		if warn == "" {
			warn = capsWarn
		} else {
			warn = warn + "; " + capsWarn
		}
	}

	stream := NewStreamer(prov, caps)
	return engine.New(stream, cfg.App.MaxRetries), ref, system, warn, nil
}

// providerFor is BuildEngine's and NewEngineFactory's shared middle: once a
// Ref is in hand (however it got resolved), find its provider and build the
// adapter. Split out so both entry points fail with the exact same wording
// for "provider not declared", instead of BuildEngine's own copy drifting
// from a second one written for the factory.
func providerFor(cfg *config.Config, ref ModelRef, version string) (provider.Provider, error) {
	pc, ok := FindProvider(cfg, ref.Provider)
	if !ok {
		return nil, fmt.Errorf("provider %q for %q is not declared in %s",
			ref.Provider, ref.Ref, configOrigin(cfg))
	}
	return NewProvider(cfg, pc, version)
}

// NewEngineFactory returns a tui.EngineFactory closed over cfg and version:
// tui.Root calls it every time the user switches models (§4.2's Ref form,
// "provider/model") so the *engine.Engine actually making requests is
// rebuilt for the destination provider, not just the two display strings
// (m.model, m.footer.Model) that used to be all a switch touched — see
// tui.switchEngine's own comment for the bug this closes.
//
// It deliberately walks the exact same ResolveModel/FindProvider/NewProvider
// path BuildEngine uses at startup (via providerFor above): a model switch
// mid-session and the one at boot must resolve a given Ref identically, or
// "works after a restart but not from the picker" becomes a second bug
// layered on top of the first.
//
// maxRetries is bound to cfg.App.MaxRetries, same as BuildEngine's own
// engine.New call — a mid-session switch has no reason to retry differently
// than the session started.
//
// cat and wantTools are threaded through for the same reason BuildEngine
// takes them, and re-evaluated per destination ref rather than captured as
// a single boolean: switching from a tool-capable model to one the catalog
// says has no tool support has to stop sending the `tools` array, and
// switching back has to resume it. Binding the boot model's answer once
// would make the picker able to break tool calling — or to start sending
// tools to a model that rejects them — with no way to recover short of a
// restart.
//
// Unlike BuildEngine there is nowhere to return a warning to:
// tui.EngineFactory is func(ref) (*engine.Engine, error) by design (§6.1 —
// tui knows nothing about catalogs or configuration). A downgrade is
// therefore silent here; it is still correct, and the footer already shows
// which model is active.
func NewEngineFactory(cfg *config.Config, cat *catalog.Catalog, version string, wantTools bool) tui.EngineFactory {
	return func(refText string) (*engine.Engine, error) {
		ref, err := ResolveModel(cfg, refText)
		if err != nil {
			return nil, err
		}
		prov, err := providerFor(cfg, ref, version)
		if err != nil {
			return nil, err
		}
		caps, _ := CapsFor(cfg, cat, ref.Ref, wantTools)
		stream := NewStreamer(prov, caps)
		return engine.New(stream, cfg.App.MaxRetries), nil
	}
}

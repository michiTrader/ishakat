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
// Caps is deliberately the zero value (text-only): the model picker (Step
// 10) is what will thread the catalog's per-model capabilities through once
// it exists, and until then every request behaves as if the model supports
// only text, which is always safe (never sends an image or a tool the
// target can't take — see provider.Caps's own doc comment).
func BuildEngine(cfg *config.Config, modelText, version string) (eng *engine.Engine, ref ModelRef, system, warn string, err error) {
	var fb *BootFallback
	ref, fb, err = ResolveModelForBoot(cfg, modelText)
	if err != nil {
		return nil, ModelRef{}, "", "", err
	}
	prov, err := providerFor(cfg, ref, version)
	if err != nil {
		return nil, ModelRef{}, "", "", err
	}

	system, warn = SystemPrompt(cfg)
	if fb != nil {
		fbLine := fmt.Sprintf("app.default_model (%s) %s; using %s instead", fb.From, fb.Reason, fb.To)
		if warn == "" {
			warn = fbLine
		} else {
			warn = fbLine + "; " + warn
		}
	}

	stream := NewStreamer(prov, provider.Caps{})
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
func NewEngineFactory(cfg *config.Config, version string) tui.EngineFactory {
	return func(refText string) (*engine.Engine, error) {
		ref, err := ResolveModel(cfg, refText)
		if err != nil {
			return nil, err
		}
		prov, err := providerFor(cfg, ref, version)
		if err != nil {
			return nil, err
		}
		stream := NewStreamer(prov, provider.Caps{})
		return engine.New(stream, cfg.App.MaxRetries), nil
	}
}

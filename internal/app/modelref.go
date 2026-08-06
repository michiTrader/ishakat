package app

import (
	"fmt"
	"strings"

	"github.com/MichiTrader/ishakat/internal/config"
)

// ModelRef is a model reference already split into the two pieces the rest
// of the system needs: the one the user sees and the one that goes on the
// wire.
//
// The split is the one from §4.2 and it is not cosmetic: OmniRoute serves
// models whose own identifier already contains slashes
// ("anthropic/claude-sonnet-4-5"), so strings.Split(ref, "/") is a
// guaranteed bug. Here the reference is only cut on the first slash, with
// strings.Cut.
type ModelRef struct {
	Ref      string // "omniroute/anthropic/claude-sonnet-4-5"
	Provider string // "omniroute"
	WireID   string // "anthropic/claude-sonnet-4-5"

	// Via records where the reference came from, so it can be reported
	// honestly: "" (literal), "default", "alias" or "implicit-provider".
	Via string
}

// String returns the full reference.
func (r ModelRef) String() string { return r.Ref }

// ResolveModel turns whatever the user typed into a usable reference.
//
// DELIBERATE SCOPE: this is NOT the full §4.5 resolver. The complete four
// stages —exact, alias, unique suffix and fuzzy scoring— need a model
// catalog, which is Step 6, and the digit-bonus matcher is Step 7. What's
// here are the two stages that don't depend on the catalog (exact and
// alias) plus the provider/wire_id split, which is all headless mode needs
// to work today.
//
// Once internal/catalog exists, this function turns into a call to
// catalog.Resolve and the rest of the package never notices: that's why it
// returns its own type instead of a plain string.
//
// It never returns a bare "model not found": if the reference has no
// provider prefix, the first enabled provider is assumed (§5.3, "a
// default_model that doesn't resolve falls back to the first enabled
// provider") and that is recorded in Via.
func ResolveModel(cfg *config.Config, text string) (ModelRef, error) {
	pc, wire, via, err := lookupModelProvider(cfg, text)
	if err != nil {
		return ModelRef{}, err
	}
	if !pc.Enabled {
		return ModelRef{}, fmt.Errorf("provider %q is declared but disabled; "+
			"set enabled = true in %s or pick another model", pc.ID, configOrigin(cfg))
	}
	return ModelRef{
		Ref:      pc.ID + "/" + wire,
		Provider: pc.ID,
		WireID:   wire,
		Via:      via,
	}, nil
}

// lookupModelProvider is ResolveModel's actual resolution logic, split out
// so ResolveModelForBoot (P2) can inspect the config.Provider a reference
// pointed at even when it turns out to be unusable — ResolveModel itself
// only ever returns a zero-value ModelRef on any error, which is correct
// for its own callers (there is nothing partial to hand back) but leaves
// ResolveModelForBoot with no way to tell "disabled" apart from "missing
// credential" apart from "not declared at all" without re-parsing text
// itself. pc is returned found-but-disabled/found-but-unauthenticated
// rather than zeroed, specifically so the caller can read pc.Enabled/
// pc.AuthOK/pc.ID after an error.
//
// The one check ResolveModel itself performs afterwards — pc.Enabled  — is
// deliberately NOT duplicated here: this function's err is nil in that
// case, and callers that care about enabled/disabled (both ResolveModel and
// ResolveModelForBoot) look at pc.Enabled themselves.
func lookupModelProvider(cfg *config.Config, text string) (pc config.Provider, wire, via string, err error) {
	q := strings.TrimSpace(text)

	if q == "" {
		q = strings.TrimSpace(cfg.App.DefaultModel)
		via = "default"
	}
	if q == "" {
		return config.Provider{}, "", "", fmt.Errorf("no model to use: pass -m/--model or set " +
			"app.default_model in the configuration")
	}

	// Config aliases. A short hop chain (an alias pointing to another alias)
	// is allowed with a cap, because a cycle in the user's TOML must not
	// hang the program.
	for i := 0; i < 4; i++ {
		v, ok := lookupAlias(cfg, q)
		if !ok {
			break
		}
		q = strings.TrimSpace(v)
		if via == "" || via == "default" {
			via = "alias"
		}
	}
	if q == "" {
		return config.Provider{}, "", "", fmt.Errorf("alias %q points to an empty reference", strings.TrimSpace(text))
	}

	// Only the first slash separates provider from wire_id (§4.2).
	head, tail, hasSlash := strings.Cut(q, "/")

	switch {
	case hasSlash && isProviderID(cfg, head):
		pc, _ = FindProvider(cfg, head)
		wire = tail
	default:
		// No recognizable prefix: the turn goes to the first enabled
		// provider and the whole reference is sent as wire_id. This is what
		// makes `-m gpt-4o-mini` work without writing "openai/" in front.
		enabled := EnabledProviders(cfg)
		if len(enabled) == 0 {
			return config.Provider{}, "", "", fmt.Errorf("no provider is enabled: check the "+
				"[[provider]] entries in %s", configOrigin(cfg))
		}
		pc = enabled[0]
		wire = q
		if via == "" {
			via = "implicit-provider"
		}
	}

	wire = strings.TrimSpace(wire)
	if wire == "" {
		return pc, "", via, fmt.Errorf("reference %q has no model id after the "+
			"provider (example: %s/auto/coding)", q, pc.ID)
	}
	return pc, wire, via, nil
}

// lookupAlias looks up an alias case-insensitively. Returns false if it
// doesn't exist, and also if the alias points to itself: that's a
// configuration error that must not become a loop.
func lookupAlias(cfg *config.Config, q string) (string, bool) {
	if len(cfg.Alias) == 0 {
		return "", false
	}
	if v, ok := cfg.Alias[q]; ok && !strings.EqualFold(strings.TrimSpace(v), q) {
		return v, true
	}
	for k, v := range cfg.Alias {
		if strings.EqualFold(k, q) && !strings.EqualFold(strings.TrimSpace(v), q) {
			return v, true
		}
	}
	return "", false
}

func isProviderID(cfg *config.Config, id string) bool {
	_, ok := FindProvider(cfg, id)
	return ok
}

// BootFallback records what ResolveModelForBoot silently did instead of the
// literal ref the configuration or the -m flag asked for, so the caller can
// print exactly one line about it (headless.go's step 4, app.go's engine
// wiring) rather than the previous "eng = nil" with no explanation beyond
// whatever error text happened to be sitting in cfg.Warnings.
type BootFallback struct {
	// From is the reference that failed to resolve to a usable provider —
	// usually app.default_model verbatim, since ResolveModelForBoot is only
	// ever consulted when modelText == "" (an explicit -m/--model is never
	// second-guessed: see ResolveModelForBoot's own doc comment).
	From string
	// To is the reference the fallback picked instead.
	To string
	// Reason is a short, human phrase for why From didn't work: "is
	// disabled", "has no working credential" or "is not declared".
	Reason string
}

// ResolveModelForBoot is P2: ResolveModel's own doc comment already
// documents the no-prefix case falling back to "the first enabled
// provider", but that rule doesn't help when app.default_model itself names
// a provider that is declared yet unusable (disabled, or missing its
// credential) — ResolveModel correctly reports that as an error, and until
// this function existed the only caller of BuildEngine at boot
// (internal/app.app.go) had no choice but to start with eng = nil and make
// the user open the picker (ctrl+p) or fix config.toml by hand before every
// single launch, for a configuration mistake this package already has
// everything it needs to route around on its own.
//
// This is intentionally a SEPARATE function from ResolveModel, not a change
// to it: an explicit -m/--model or a config alias that fails to resolve
// must keep failing loudly (a typo silently landing on some other provider
// would be far more confusing than an error), and ResolveModel is also what
// a live model switch (tui's picker, NewEngineFactory) calls, where "silent
// fallback to something else" is never the right behaviour either — the
// user picked a specific model from the list. Boot time is different: there
// is no explicit choice being second-guessed, only app.default_model's own
// stale or misconfigured value, exactly the "warned twice, worked from the
// picker" gap from the original bug report this session responds to.
//
// modelText is passed through unchanged: a non-empty -m/--model always
// takes ResolveModel's ordinary path and fb is nil. Only when modelText is
// empty (falling back to app.default_model) AND that default fails to
// resolve to an enabled+credentialed provider does this look for another
// enabled+credentialed provider to use instead — chosen with
// EnabledProviders' existing "declaration order" rule, skipping any without
// AuthOK, and reported once via the returned *BootFallback rather than
// silently.
//
// The wire id for the fallback comes from config.VerifyModelFor: the exact
// model preset's `provider add` already proved answers for that provider's
// credential. A provider with no matching preset (added entirely by hand,
// under a kind/base_url this package has never verified) has no such
// wire id to guess, so it is skipped in favour of one that does — falling
// back to a plausible-looking but unverified model id would trade one
// startup failure for a different, harder to diagnose one.
func ResolveModelForBoot(cfg *config.Config, modelText string) (ModelRef, *BootFallback, error) {
	if strings.TrimSpace(modelText) != "" {
		ref, err := ResolveModel(cfg, modelText)
		return ref, nil, err
	}

	pc, wire, via, err := lookupModelProvider(cfg, modelText)
	if err != nil {
		// "no model to use" (app.default_model itself is empty) or "no
		// provider is enabled": there is nothing configured to fall back
		// away from, so there is nothing this function can do that
		// ResolveModel didn't already try.
		return ModelRef{}, nil, err
	}
	if pc.Enabled && pc.AuthOK {
		// The ordinary, unremarkable case: app.default_model already
		// resolves to a usable provider. No fallback needed.
		return ModelRef{Ref: pc.ID + "/" + wire, Provider: pc.ID, WireID: wire, Via: via}, nil, nil
	}

	reason := "has no working credential"
	if !pc.Enabled {
		reason = "is disabled"
	}
	from := pc.ID + "/" + wire

	for _, alt := range EnabledProviders(cfg) {
		if !alt.AuthOK || strings.EqualFold(alt.ID, pc.ID) {
			continue
		}
		altWire, ok := config.VerifyModelFor(alt.ID)
		if !ok {
			continue
		}
		fallbackRef, fbErr := ResolveModel(cfg, alt.ID+"/"+altWire)
		if fbErr != nil {
			continue
		}
		fallbackRef.Via = "fallback"
		return fallbackRef, &BootFallback{From: from, To: fallbackRef.Ref, Reason: reason}, nil
	}

	// Nothing else to fall back to: report the same error ResolveModel
	// would, exactly as if this function didn't exist.
	if !pc.Enabled {
		return ModelRef{}, nil, fmt.Errorf("provider %q is declared but disabled; "+
			"set enabled = true in %s or pick another model", pc.ID, configOrigin(cfg))
	}
	return ModelRef{}, nil, fmt.Errorf(
		"app.default_model (%s) %s, and no other configured provider has a working "+
			"credential either: check the [[provider]] entries in %s",
		from, reason, configOrigin(cfg))
}

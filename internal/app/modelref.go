package app

import (
	"errors"
	"fmt"
	"strings"

	"github.com/MichiTrader/ishakat/internal/catalog"
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
		return config.Provider{}, "", "", errNoModelConfigured
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
			// P3: a fresh install (P0/P1) ends up here with zero active
			// providers — an honest, expected state, not a broken config —
			// so the message names the actual fix (`provider add`) instead
			// of sending the user to hand-edit TOML entries that likely
			// don't even exist yet.
			return config.Provider{}, "", "", fmt.Errorf("no provider is enabled yet: run "+
				"`ishakat provider add <name>` (openai, anthropic, gemini, nvidia, omniroute) "+
				"or check the [[provider]] entries in %s", configOrigin(cfg))
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

// errNoModelConfigured is lookupModelProvider's "there is no model to
// resolve at all": no -m/--model was given AND app.default_model is empty.
//
// It is a sentinel rather than a fresh fmt.Errorf because
// ResolveModelForBoot has to tell this case apart from every other
// resolution failure, and matching on message text would be exactly the
// kind of brittle coupling that breaks the next time the wording is
// improved. ResolveModel (the strict path) still surfaces it verbatim; only
// ResolveModelForBoot treats it as "pick something sensible" — see
// pickBootModel.
//
// The wording stays the actionable one it always was, because it is still
// what an explicit `ishakat -m ""`-style dead end deserves to print.
var errNoModelConfigured = errors.New("no model to use: pass -m/--model or set " +
	"app.default_model in the configuration")

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
	// disabled", "has no working credential", "is not declared" or "is not
	// set".
	Reason string
}

// Unset reports whether this fallback happened because nothing was
// configured at all (app.default_model empty), as opposed to something
// configured that turned out to be unusable.
//
// Callers use it to phrase their one report line: "app.default_model (x) is
// disabled; using y instead" reads correctly for a broken default and
// absurdly for an absent one, where there is no x to name.
func (f *BootFallback) Unset() bool { return f != nil && f.From == "" }

// Describe is the single, shared phrasing of what a fallback did, so
// app.go's TUI startup and headless.go's step 4 cannot drift into saying it
// two different ways — they previously each held their own copy of the same
// fmt.Sprintf, which is how the "(%s)" would have ended up printing an
// empty pair of parentheses for the unset case in one entry point and not
// the other.
func (f *BootFallback) Describe() string {
	if f == nil {
		return ""
	}
	if f.Unset() {
		return fmt.Sprintf("app.default_model is not set; using %s for this session "+
			"(run `ishakat model set %s` to make it stick)", f.To, f.To)
	}
	return fmt.Sprintf("app.default_model (%s) %s; using %s instead", f.From, f.Reason, f.To)
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
// The wire id for the fallback comes from the local catalog when it has one
// for the provider, and from config.VerifyModelFor otherwise — see
// pickBootModel for why that order matters.
//
// cat is the local snapshot and may be nil (a first run with no cache is an
// ordinary state, not an error); nothing here ever touches the network,
// which §4.4 keeps off the startup path.
func ResolveModelForBoot(cfg *config.Config, cat *catalog.Catalog, modelText string) (ModelRef, *BootFallback, error) {
	if strings.TrimSpace(modelText) != "" {
		ref, err := ResolveModel(cfg, modelText)
		return ref, nil, err
	}

	pc, wire, via, err := lookupModelProvider(cfg, modelText)
	if err != nil {
		// app.default_model is empty AND no -m was given. Until this
		// branch existed, that was reported as the fatal-looking
		// "no model to use: pass -m/--model or set app.default_model",
		// which for the TUI meant a ⚠ on stderr and eng = nil on *every
		// single launch* of a configuration that is otherwise perfectly
		// usable — one enabled, credentialed provider and a full catalog
		// sitting right there. The session then only worked because the
		// user opened the picker with ctrl+p and re-chose a model by hand,
		// every time, which is where the "── now: … ──" line in the bug
		// report came from (picker.go). The warning was not describing a
		// broken configuration; it was describing an unmade decision this
		// function is already in the business of making.
		//
		// An absent default is strictly LESS ambiguous than the
		// disabled/uncredentialed default handled below, which this
		// function has always routed around silently-but-reported: there
		// is no user intent to second-guess at all. So it takes the same
		// path and gets the same one-line report.
		if errors.Is(err, errNoModelConfigured) {
			if ref, ok := pickBootModel(cfg, cat); ok {
				return ref, &BootFallback{To: ref.Ref, Reason: "is not set"}, nil
			}
			// Nothing usable to pick either. "set app.default_model" is now
			// the wrong advice: with no provider that can answer, naming a
			// model in the configuration fixes nothing, and sending the
			// user to edit TOML for a problem that needs a credential is
			// how the earlier startup messages wasted people's time.
			// Report what is actually missing instead.
			return ModelRef{}, nil, noUsableProviderError(cfg)
		}
		// A different failure entirely ("no provider is enabled yet", a
		// broken alias): report exactly what ResolveModel would.
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

	if fallbackRef, ok := pickBootModel(cfg, cat, pc.ID); ok {
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

// noUsableProviderError explains why boot found nothing to run when
// app.default_model was never set, and it deliberately does not mention
// app.default_model at all: that key is not what is missing here.
//
// The two states are distinguished because they need opposite actions, and
// telling them apart is the whole value of the message. "Nothing is
// declared" is a fresh install and wants `provider add`. "Declared but
// nothing can authenticate" already has the TOML and wants the credential —
// naming the environment variables the configuration itself asked for is
// the shortest path from the error to a working session.
func noUsableProviderError(cfg *config.Config) error {
	enabled := EnabledProviders(cfg)
	if len(enabled) == 0 {
		return fmt.Errorf("no provider is enabled yet: run "+
			"`ishakat provider add <name>` (openai, anthropic, gemini, nvidia, omniroute) "+
			"or check the [[provider]] entries in %s", configOrigin(cfg))
	}

	missing := make([]string, 0, len(enabled))
	for _, p := range enabled {
		if p.AuthOK {
			continue
		}
		if env := strings.TrimSpace(p.MissingEnv); env != "" {
			missing = append(missing, fmt.Sprintf("%s (%s)", p.ID, env))
			continue
		}
		missing = append(missing, p.ID)
	}
	if len(missing) > 0 {
		return fmt.Errorf("no provider has a working credential yet: %s. "+
			"Export the API key, or re-run `ishakat provider add` to store it",
			strings.Join(missing, ", "))
	}

	// Enabled and credentialed, yet pickBootModel still found nothing: every
	// such provider was declared by hand under an id with no preset, so
	// there is no model id to use and no catalog entry either. Guessing one
	// is what VerifyModelFor's doc comment refuses to do, so ask.
	return fmt.Errorf("no model could be chosen automatically: run "+
		"`ishakat models --refresh` to discover what the configured providers serve, "+
		"or set app.default_model in %s", configOrigin(cfg))
}

// pickBootModel finds a model that can actually serve a turn right now,
// without touching the network. It is ResolveModelForBoot's shared "and what
// should we use instead?" step, for both of that function's fallbacks: an
// app.default_model that is unusable, and one that was never set.
//
// skip lists provider ids that must not be chosen — the caller passes the
// provider that already failed, so the fallback cannot land back on it. It
// is variadic purely so the unset case, which has nothing to exclude, can
// call this without inventing a sentinel argument.
//
// Providers are considered in EnabledProviders' declaration order (the same
// order the rest of the package treats as the user's own preference) and
// skipped unless AuthOK: an enabled provider with no resolved credential
// cannot answer, so choosing it would swap a startup warning for a failing
// first turn.
//
// The model id for a chosen provider is looked for in two places, in this
// order, and the order is the point:
//
//  1. The local catalog, filtered to models that can serve a turn
//     (Health.Usable) and that are not deprecated. This is preferred
//     because it reflects what the provider itself said it serves, on this
//     machine, the last time discovery ran — so a Gemini user gets a Gemini
//     model that currently exists.
//  2. config.VerifyModelFor's preset id, as the catalog-less fallback (a
//     first run, or a cache that has not been written yet). This is a
//     compiled-in constant, so it is by definition the older, staler
//     answer of the two — `gemini-2.0-flash` is what this build happens to
//     have been written with, not necessarily what the account can serve
//     today — but it is a model id `provider add`'s verification probe
//     actually proved, which is far better than a guess.
//
// A provider offering neither is skipped rather than guessed at, for the
// reason VerifyModelFor's own doc comment gives: inventing a plausible model
// id for a service this package has never talked to trades one honest
// startup failure for a stranger one at first turn.
func pickBootModel(cfg *config.Config, cat *catalog.Catalog, skip ...string) (ModelRef, bool) {
	skipped := func(id string) bool {
		for _, s := range skip {
			if strings.EqualFold(s, id) {
				return true
			}
		}
		return false
	}

	for _, p := range EnabledProviders(cfg) {
		if !p.AuthOK || skipped(p.ID) {
			continue
		}
		wire, ok := catalogModelFor(cat, p.ID)
		if !ok {
			if wire, ok = config.VerifyModelFor(p.ID); !ok {
				continue
			}
		}
		// Resolved through ResolveModel rather than assembled by hand so
		// the choice passes the very same validation any other reference
		// does (provider declared, provider enabled, non-empty wire id).
		ref, err := ResolveModel(cfg, p.ID+"/"+wire)
		if err != nil {
			continue
		}
		ref.Via = "fallback"
		return ref, true
	}
	return ModelRef{}, false
}

// catalogModelFor returns a wire id from the local snapshot for providerID,
// preferring models that can actually be used.
//
// Catalog order is already the meaningful one (catalog.Build preserves
// provider declaration order and sorts within a provider), so this takes the
// first acceptable entry rather than imposing a second opinion about which
// model is "best" — that ranking is the picker's job (§4.5/§9.4), and this
// function only has to answer "something that works".
//
// Deprecated models are skipped: the provider has said out loud that they
// are going away, and starting a session on one is a slow-motion failure.
// Unusable health (HealthUnauthenticated) is skipped for the same reason
// pickBootModel checks AuthOK.
func catalogModelFor(cat *catalog.Catalog, providerID string) (string, bool) {
	if cat == nil {
		return "", false
	}
	for _, m := range cat.Models {
		if !strings.EqualFold(m.Provider, providerID) {
			continue
		}
		if !m.Health.Usable() || m.Deprecated() {
			continue
		}
		if strings.TrimSpace(m.WireID) == "" {
			continue
		}
		return m.WireID, true
	}
	return "", false
}

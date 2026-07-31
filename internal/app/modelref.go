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
	q := strings.TrimSpace(text)
	via := ""

	if q == "" {
		q = strings.TrimSpace(cfg.App.DefaultModel)
		via = "default"
	}
	if q == "" {
		return ModelRef{}, fmt.Errorf("no model to use: pass -m/--model or set " +
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
		return ModelRef{}, fmt.Errorf("alias %q points to an empty reference", strings.TrimSpace(text))
	}

	// Only the first slash separates provider from wire_id (§4.2).
	head, tail, hasSlash := strings.Cut(q, "/")

	var pc config.Provider
	var wire string

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
			return ModelRef{}, fmt.Errorf("no provider is enabled: check the "+
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
		return ModelRef{}, fmt.Errorf("reference %q has no model id after the "+
			"provider (example: %s/auto/coding)", q, pc.ID)
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

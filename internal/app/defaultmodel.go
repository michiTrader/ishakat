package app

import "github.com/MichiTrader/ishakat/internal/config"

// NeedsDefaultModel reports whether cfg.App.DefaultModel does not currently
// resolve to an enabled, credentialed provider — i.e. whether `provider
// add` (cmd/ishakat/provider.go) should offer to point it at the provider
// it just configured.
//
// SetDefaultModel already existed in internal/config/connection.go, with a
// doc comment describing exactly this use ("provider add offers this once
// discovery finds models... leaving the stock omniroute/auto/coding default
// in place is the single most common failure mode this audit found") but no
// caller ever invoked it — the audit that added it also documented the gap
// as unfinished. This is the missing predicate that lets `provider add`
// decide *when* to make that offer, instead of every time regardless of
// whether the existing default already works.
func NeedsDefaultModel(cfg *config.Config) bool {
	ref, err := ResolveModel(cfg, "")
	if err != nil {
		return true
	}
	pc, ok := FindProvider(cfg, ref.Provider)
	if !ok || !pc.Enabled || !pc.AuthOK {
		return true
	}
	return false
}

// projecttrust.go resolves §21.4 layer 2 (Step 30) for one interactive
// run: does this project already have a saved trust decision, and if not,
// what should the dialog show and where should its answer end up. This is
// the one function Run calls into for all of that, the same "one call site,
// one documented rule" shape evolveThresholds/DiscoverSkills already follow
// elsewhere in this package for their own pre-tui.Options resolution step.
package app

import (
	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/permissions"
	"github.com/MichiTrader/ishakat/internal/trust"
	"github.com/MichiTrader/ishakat/internal/tui"
	"github.com/MichiTrader/ishakat/internal/xdg"
)

// resolveProjectTrust decides whether tui.Root should open ModeTrust
// (needsTrust), what internal/app.DetectGit found for rawCWD (gitInfo,
// zero value when needsTrust is false — the dialog that would draw it
// never opens, so nothing computes it), what the footer should show from
// the very first frame for a project that does NOT need the dialog
// (initialAutonomy, empty when needsTrust is true — resolveTrust,
// trust.go, sets FooterState.Autonomy itself once the dialog closes
// instead), and the tui.TrustStore the dialog's own Save call persists
// through (trustStore, always non-nil: even a run with tools disabled --
// guard == nil -- still wants trust.json written, only the live
// Guard.SetAutonomy half of fileTrustStore.Save is skipped then).
//
// [autonomy].remember = false (config.Autonomy.Remember) means "never trust
// a saved decision, ask every run" — its own doc comment in
// internal/config/schema.go names this for a scripted or disposable
// environment where a stale trust.json would be actively wrong to honour —
// so this function short-circuits to needsTrust = true without even
// calling trust.Load, the identical "the config bit governs before the
// file is consulted at all" shape RemoveConversation's own [session] save
// check already follows for a sibling on/off switch.
func resolveProjectTrust(cfg *config.Config, rawCWD string, guard *permissions.Guard) (needsTrust bool, gitInfo GitInfo, initialAutonomy string, trustStore tui.TrustStore) {
	return resolveProjectTrustWithFile(cfg, rawCWD, guard, xdg.TrustFile())
}

// resolveProjectTrustWithFile is resolveProjectTrust's own testable core,
// taking trustFile explicitly rather than resolving xdg.TrustFile() itself
// -- the same "split the one line that touches a fixed real-world path out
// from the logic a test actually wants to drive" shape NewSessionRecorder's
// own tests already use for [session]'s directory.
func resolveProjectTrustWithFile(cfg *config.Config, rawCWD string, guard *permissions.Guard, trustFile string) (needsTrust bool, gitInfo GitInfo, initialAutonomy string, trustStore tui.TrustStore) {
	trustStore = &fileTrustStore{path: rawCWD, trustFile: trustFile, guard: guard}

	if !cfg.Autonomy.Remember {
		return true, DetectGit(rawCWD), "", trustStore
	}

	store, err := trust.Load(trustFile)
	if err != nil {
		// An unreadable trust.json is treated exactly like "nothing
		// recorded yet" -- the same "degrade to asking again, never to a
		// silently fabricated answer" reasoning DetectGit's own doc
		// comment gives for a git probe that cannot be trusted either.
		return true, DetectGit(rawCWD), "", trustStore
	}

	rec, found := store.Lookup(rawCWD)
	if !found {
		return true, DetectGit(rawCWD), "", trustStore
	}

	// A record exists: the dialog does not open, so DetectGit -- the one
	// call that only ever exists to feed that dialog's own "git: yes ·
	// clean · branch main" line -- is not run at all, matching
	// resolveProjectTrust's own doc comment above.
	return false, GitInfo{}, permissions.ParseAutonomy(rec.Autonomy).String(), trustStore
}

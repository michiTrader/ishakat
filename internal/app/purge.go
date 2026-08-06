// purge.go implements P3's `ishakat purge` / `ishakat purge --sessions`: a
// total, interactive-confirmed removal of every file this program writes on
// its own — config.toml, credentials.toml, the catalog cache, session
// transcripts, the last-error state file — modeled directly on the "where
// is the data and how do I actually delete it" section of the original bug
// report.
//
// That report's own finding is why this exists as a dedicated command
// instead of "just rm -rf it yourself": ishakat's data lives under FOUR
// separate XDG base directories (internal/xdg/xdg.go), not one, and in an
// environment like Termux with no XDG_* variables exported none of those
// paths are where a user would guess. Reinstalling the binary touches none
// of them; only this command (or the four `rm -rf` lines `doctor` can now
// be copy-pasted from) does.
package app

import (
	"fmt"
	"os"
	"strings"

	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/xdg"
)

// PurgeTargets returns the absolute directories a purge would remove.
//
// sessionsOnly restricts the result to the one directory session
// transcripts live in — resolved through cfg exactly the way
// NewSessionRecorder/NewSessionLister already do (cfg.Session.Dir wins over
// xdg.SessionsDir()), so `ishakat purge --sessions` honours a customized
// [session] dir instead of silently purging the XDG default while leaving
// the user's actual sessions untouched. cfg may be nil (config.toml itself
// failed to load, or the caller never needed the rest of it): the XDG
// default is used in that case, same as every session.go caller's own
// fallback.
//
// A full purge (sessionsOnly == false) returns the union of all four XDG
// base directories this program uses (xdg.ConfigDir/CacheDir/DataDir/
// StateDir) plus the resolved session directory, deduplicated — the last
// part matters because a customized [session] dir can point OUTSIDE
// xdg.DataDir() (an external drive, a synced folder), in which case it
// would otherwise survive a "purge everything" run for no reason a user
// would expect.
func PurgeTargets(cfg *config.Config, sessionsOnly bool) []string {
	sessionsDir := xdg.SessionsDir()
	if cfg != nil && strings.TrimSpace(cfg.Session.Dir) != "" {
		sessionsDir = cfg.Session.Dir
	}

	if sessionsOnly {
		return []string{sessionsDir}
	}

	seen := map[string]bool{}
	var out []string
	for _, d := range []string{xdg.ConfigDir(), xdg.CacheDir(), xdg.DataDir(), xdg.StateDir(), sessionsDir} {
		if d == "" || seen[d] {
			continue
		}
		// A dir nested under one already in the list (the common case:
		// sessionsDir == xdg.DataDir()+"/sessions") is not filtered out
		// here — os.RemoveAll on the parent already covers it, and
		// PurgeResult.Removed below reports the parent, which is the
		// answer a user actually wants ("was my config wiped?"), not an
		// internal implementation detail about which XDG dir contains
		// which.
		seen[d] = true
		out = append(out, d)
	}
	return out
}

// PurgeResult is what actually happened, split the same way
// CleanCatalogCache's own CleanResult is: a directory that didn't exist is
// not an error (nothing to do, the end state the caller wants was already
// true — the same "no-op on absence" rule the P3c config mutators
// (RemoveAlias, RemoveFavorite) already follow), it is simply reported
// separately from one that was actually removed.
type PurgeResult struct {
	Removed []string
	Missing []string
}

// Purge removes every directory in targets with os.RemoveAll, stopping at
// the first real error (permission denied, a mount that refuses removal,
// …) rather than best-effort continuing past a failure the caller needs to
// know about — this deletes a user's own data, so "partially failed,
// silently" is never acceptable; "failed here, here's exactly why, nothing
// past this point was touched" is.
func Purge(targets []string) (PurgeResult, error) {
	var res PurgeResult
	for _, dir := range targets {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			res.Missing = append(res.Missing, dir)
			continue
		} else if err != nil {
			return res, fmt.Errorf("stat %s: %w", dir, err)
		}
		if err := os.RemoveAll(dir); err != nil {
			return res, fmt.Errorf("remove %s: %w", dir, err)
		}
		res.Removed = append(res.Removed, dir)
	}
	return res, nil
}

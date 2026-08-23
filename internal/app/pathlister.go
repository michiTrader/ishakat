// pathlister.go is internal/app's real implementation of F18's own "@"
// path-completion seam (docs/ROADMAP-ux-2026-08-20.md's W5,
// internal/tui/atmenu.go's PathLister type): the actual os.ReadDir call
// atMenuFor needs, kept on this side of the §6.1 boundary for the same
// reason NewReloadFactory/NewCatalogRefreshFactory are — internal/tui does
// not do its own disk I/O (internal/arch_test.go's boundary tests, and the
// stricter "internal/tui makes zero direct os calls" coding convention
// that goes beyond what those tests literally require).
package app

import (
	"os"
	"sort"

	"github.com/MichiTrader/ishakat/internal/tui"
)

// NewPathLister returns a tui.PathLister closed over nothing at all,
// mirroring NewReloadFactory's own "re-reads fresh every call" shape:
// every call re-lists whatever directory atMenuFor asks for, relative to
// the process's current working directory, rather than a snapshot taken
// once at boot — a directory's contents can change at any point during a
// long session, and a stale listing would silently offer files that no
// longer exist or hide ones that were just created.
//
// An empty dir (the top-level "@partial" case, before any "/" has been
// typed) lists "." rather than "" — os.ReadDir("") fails outright, while
// os.ReadDir(".") lists the same cwd every relative path in this session
// is already resolved against (Options.CWD, app.Run's own os.Getwd call).
//
// Directory entries are returned with a trailing "/" — the exact marker
// atmenu.go's splitAtToken/applyAtCompletion read back to decide whether
// completing an entry should close the dropdown (a file) or keep
// descending into it (a directory) — sorted, mirroring theme.Available's
// own "seen once, sorted names" shape, adapted for filesystem entries
// instead of theme names.
//
// A directory that does not exist, or any other os.ReadDir error (no
// permission, not a directory at all), degrades to nil rather than
// propagating the error anywhere: atMenuFor's own "lister returned
// nothing" branch already closes the dropdown for exactly this "nothing
// matched" outcome, the same graceful-degradation contract every other
// §6.1 seam in this package (NewReloadFactory's own zero-Result case,
// NewCatalogRefreshFactory's own nil-Catalog case) already follows.
func NewPathLister() tui.PathLister {
	return func(dir string) []string {
		target := dir
		if target == "" {
			target = "."
		}
		entries, err := os.ReadDir(target)
		if err != nil {
			return nil
		}
		out := make([]string, 0, len(entries))
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() {
				name += "/"
			}
			out = append(out, name)
		}
		sort.Strings(out)
		return out
	}
}

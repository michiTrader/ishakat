// tools.go defines /tools' own read side (§13, Step 20's left-over UI
// half): a listing of every layer-2 (declarative/script) tool that exists
// right now, plus /tools code <name> to view one's manifest in full.
//
// internal/tui may never import internal/tools directly: declarative.go's
// HTTP client (and Fetch, registry.go's Core) pull net/http transitively
// into that package's dependency closure, and TestTUINoImportaHTTP
// (internal/arch_test.go, §6.1) forbids net/http anywhere in this
// package's own closure. So this file only defines the shape of the data
// (ToolSummary, ToolsListResult) and the interface a caller must satisfy
// (ToolsLister) — internal/app is the one place that already imports both
// internal/tools and internal/tui, so it is the one place that implements
// this interface concretely (see internal/app/toolslister.go).
//
// nil is a supported ToolsLister value, the same convention Recorder and
// SessionLister already establish for their own concern: [tools].enabled
// = false, an empty tools.dir, or any test in this package that never
// sets Options.ToolsLister all mean "cannot list layer-2 tools", not "this
// is a bug" — the runner (toolscmd.go) checks for nil and reports a
// friendly "nothing configured" message rather than panicking.
//
// ToolsLister is deliberately re-consulted on every /tools invocation
// rather than resolved once at startup and handed over as a static
// snapshot (contrast with Catalog/Skills): §19.7's self-extension
// (tool_create) can add a layer-2 tool mid-session, and a cached snapshot
// from startup would silently hide it from /tools.
package tui

// ToolSummary is one row of a /tools listing: enough to render the table
// §13's own row describes ("status, origin, times used, last used")
// without this package ever touching internal/tools' own richer types
// (Manifest, ToolState) directly.
type ToolSummary struct {
	Name        string
	Description string
	Danger      string // "low" / "medium" / "high" / "unknown"
	State       string // "unverified" / "verified" / "broken" / "archived" / "unknown"
	UseCount    int
	LastUsed    string // empty means never used
	LastError   string // only meaningful when State == "broken"
}

// ToolsListResult is what ListTools returns: the tools found, plus any
// non-fatal discovery warning (a bad manifest that was skipped rather
// than failing the whole listing) — the same "warn, don't fail" leniency
// DiscoverDeclarative already applies, surfaced here as data instead of
// a log line so the TUI can show it inline.
type ToolsListResult struct {
	Tools []ToolSummary
	Warn  string
}

// ToolsLister is /tools' own read side. ListTools powers the bare
// "/tools" listing; ToolManifest powers "/tools code <name>" (returns the
// manifest file's raw text, or an error if no tool by that name exists).
type ToolsLister interface {
	ListTools() ToolsListResult
	ToolManifest(name string) (string, error)
}

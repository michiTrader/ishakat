// tools.go defines /tools' own surface (§13, Step 20's own listing plus
// Step 21's own audit and write rows): a listing of every layer-2
// (declarative/script) tool that exists right now, /tools code <name> to
// view one's manifest in full, /tools audit to see each tool's provenance
// (origin, sources, session_id) and current SHA-256 (§19.8 mitigations 2
// and 6), /tools revive <name> to bring an archived tool back (§19.5),
// and /tools delete <name> [confirm] to remove one permanently.
// create/edit remain Step 21's still-open rows.
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

// ToolAuditEntry is one row of a "/tools audit" listing: §19.8 mitigation
// 2, verbatim — "Every tool records sources (URLs read to build it) and
// session_id. /tools audit lists everything with origin and SHA-256." —
// plus the tamper signal §19.8 mitigation 6 already computes elsewhere
// (tool_probe's own DetectTamper call), surfaced here read-only rather
// than re-run: a mismatch between the last-recorded (probed) hash and the
// tool's current on-disk content means it changed without going through
// tool_edit, exactly the event a provenance audit exists to catch.
type ToolAuditEntry struct {
	Name        string
	CreatedBy   string // "user" / "model" / "community" — never self-declared danger, only who/why
	Reason      string
	Repetitions int
	SessionID   string
	Sources     []string // URLs read to build the tool, as claimed by Origin — displayed, never verified

	// Hash is the tool's current on-disk SHA-256 (ComputeHash over its
	// manifest right now, not the last-probed value from state.json).
	// HashError is set instead when the hash could not even be computed
	// (e.g. the manifest went missing between discovery and this call) —
	// the two are mutually exclusive, matching ToolSummary's own
	// State/LastError split for a listing row that could not be read.
	Hash      string
	HashError string
	// Tampered is true when Hash differs from the tool's last successful
	// probe (an empty last-probed hash — never probed — never counts as
	// tampering, matching DetectTamper's own documented rule).
	Tampered bool
}

// ToolsAuditResult is what AuditTools returns, mirroring ToolsListResult's
// own Tools+Warn shape for the same "warn, don't fail" reason.
type ToolsAuditResult struct {
	Tools []ToolAuditEntry
	Warn  string
}

// ToolsLister is /tools' own surface. ListTools powers the bare "/tools"
// listing; ToolManifest powers "/tools code <name>" (returns the manifest
// file's raw text, or an error if no tool by that name exists);
// AuditTools powers "/tools audit" (§19.8 mitigation 2 and mitigation 7's
// "origin, use count, last used" together with mitigation 6's tamper
// check); ReviveTool powers "/tools revive <name>" (§19.5's Archive/
// Revive pair, tools.ToolArchive/ToolRevive's exact state transition,
// called here directly rather than through the JSON-args Tool interface
// since there is no model call involved in a human typing a slash
// command); DeleteTool powers "/tools delete <name> [confirm]" (§19.5's
// own text: "removes it, with confirmation" — the same
// tools.ToolDelete.Run flow, called from a second, human-initiated
// entry point).
//
// Every method's error return is reserved for "could not even attempt
// it" (unknown tool name, an unreadable state.json, a failed os.RemoveAll)
// — matching every meta-tool in internal/tools' own convention that a Go
// error means the operation was never attempted, not that it failed
// after being attempted. The string returned on success is a
// human-readable status line (e.g. "is now verified", "was not
// archived; nothing changed", or — DeleteTool's own unconfirmed case —
// "refused to delete ... without confirmation") for the slash command to
// show verbatim, the same "let the adapter phrase it" split
// ToolManifest's own (string, error) signature already establishes.
// DeleteTool's refusal-without-confirm path is deliberately *not* an
// error: like tool_delete.go's own ErrorResult (not a Go error) for the
// identical case, refusing without confirmation is an attempted,
// informative outcome, not a failure to attempt.
type ToolsLister interface {
	ListTools() ToolsListResult
	ToolManifest(name string) (string, error)
	AuditTools() ToolsAuditResult
	ReviveTool(name string) (string, error)
	DeleteTool(name string, confirm bool) (string, error)
}

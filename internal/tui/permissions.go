// permissions.go defines /permissions' own read-side surface (§13, §21.4,
// §21.14's own Step 32 closing criterion: "/permissions lists rules and
// invariants"). Like tools.go this package may never import
// internal/permissions to build a live view of a *permissions.Guard
// directly (§6.1: internal/app is the one place already trusted to import
// both internal/tui and internal/permissions) — so this file only defines
// the shape of the data (PermissionsSnapshot) and the interface a caller
// must satisfy (PermissionsLister), and internal/app/permissionslister.go
// is what implements it concretely over a real *permissions.Guard plus the
// session's own config.Permissions.
//
// This is deliberately the read side only, mirroring how /config and
// /tools both shipped their own read-only listing before any write-side
// command landed on top of it (KindConfig has no "edit" Kind at all, and
// /tools' own create/edit/delete rows were later, separate increments over
// tools.go's first read-only pass). §21.16 decision 4's own settled text —
// "/permissions is the whole interface for reading and changing autonomy"
// — is why a write half (changing autonomy) is still owed on top of this,
// not a reason to hold this slice back until that half exists too: a human
// being able to see what governs the session at all is valuable on its
// own, and is exactly what §21.13's acceptance narrative's own item 10
// ("Shows what is allowed, what is session-only, the invariants as
// non-editable, and a recently-denied list") describes as a *display*.
// "recently-denied" is deliberately not part of this first slice: no
// Guard field tracks denial history yet (confirmed by reading guard.go in
// full), so surfacing it here would mean inventing new Guard-side state
// under a docs/PLAN.md-driven read-side pass — that belongs in its own
// increment, named openly in this file's own doc comment rather than
// silently left out, the same "here is the remedy" honesty KindConfig's
// own docstring already establishes for its own network-probe gap.
//
// PermissionsLister is deliberately re-consulted on every /permissions
// invocation rather than resolved once at startup, the identical
// "mid-session state can change" reasoning ToolsLister's own doc comment
// gives for /tools: a mission confirmed through ModeMission, a tool scope
// chosen through ModeToolScope, or (once the write half lands) a /permissions
// autonomy change itself, can all happen between one /permissions call and
// the next, and a cached snapshot from startup would silently go stale.
//
// RecentDenials (added Step 32 part 7) closes the one gap this file's own
// doc comment above named openly: permissions.Guard now tracks a bounded
// denial history (Guard.RecentDenials/DeniedEntry, internal/permissions/
// guard.go), and PermissionsSnapshot carries a small presentation mirror
// of it below, the same "duplicate rather than import" choice
// PermissionsMissionRule already makes for permissions.MissionRule.
package tui

import "time"

// PermissionsSnapshot is what /permissions renders: a live read of every
// layer §21.4's own table names except layer 2 (Trust — that is /trust's
// own concern, not this command's) and layer 5 (Rule — a per-call session
// grant has no stable identity worth listing outside the moment it was
// granted).
type PermissionsSnapshot struct {
	// Autonomy is §21.4 layer 3's own current value, in
	// permissions.Autonomy.String()'s exact vocabulary ("auto", "agile",
	// "readonly") — the same lowercase word FooterState.Autonomy already
	// draws in the status line, so a human can cross-check /permissions'
	// own first line against what they see above the input box.
	Autonomy string

	// Read, Write and Shell are config.Permissions' own three policy
	// knobs ("ask" | "allow" | "deny"), the base floor autonomy narrows
	// further (§21.4: "a lower layer can never widen a higher one").
	Read  string
	Write string
	Shell string

	// AllowSession mirrors config.Permissions.AllowSession — whether an
	// approval may ever cover the rest of the session for a Sensitive-tier
	// request, the one config bit that changes what a "yes" in the
	// approval dialog actually means.
	AllowSession bool

	// MissionRules is Guard.MissionRules()'s own return value (§21.4
	// layer 4, Step 31): compiled deny rules a stated goal's constraint
	// produced, in effect for the rest of this session — empty when no
	// mission is active, the overwhelmingly common case.
	MissionRules []PermissionsMissionRule

	// BashScope is Guard.BashScope()'s own return value (Step 31 part 7):
	// the bash subcommand prefixes bash is currently scoped to, or nil
	// when unrestricted ("Everything installed", or no tool-scope dialog
	// has ever resolved this session).
	BashScope []string

	// ShellDeny and WriteDeny are config.Permissions' own two configured
	// deny lists — a project-wide, human-authored floor no mission or
	// scope choice ever widens (hardDeny checks these first, before
	// either). Shown for the same reason MissionRules/BashScope are:
	// they are rules currently enforced, in the exact form the human
	// wrote them.
	ShellDeny []string
	WriteDeny []string

	// RecentDenials is Guard.RecentDenials()'s own return value, §21.13's
	// acceptance-narrative item 10's own "recently-denied list" — the one
	// piece of that item this snapshot did not carry when the read-only
	// slice first landed (this file's own doc comment named that gap
	// openly rather than silently skipping it). Oldest first, capped at
	// permissions.Guard's own bounded history size; empty for a session
	// that has refused nothing yet, the overwhelmingly common case.
	RecentDenials []PermissionsDenial
}

// PermissionsDenial mirrors permissions.DeniedEntry's own four fields
// without this package importing that type directly — the identical
// "duplicate a small, presentation-free shape rather than import it"
// choice PermissionsMissionRule already makes for permissions.MissionRule.
type PermissionsDenial struct {
	Tool   string
	Reason string
	Tier   string
	When   time.Time
}

// PermissionsMissionRule mirrors permissions.MissionRule's own two fields
// without this package importing that type directly — the identical
// "duplicate a small, presentation-free shape rather than import it"
// choice ToolSummary/ToolAuditEntry already make for internal/tools' own
// richer types.
type PermissionsMissionRule struct {
	Capability string
	Pattern    string
}

// PermissionsLister is /permissions' own read side. Snapshot returns
// everything the command renders in one call, rather than one method per
// field, because every field comes from the same live *permissions.Guard
// (plus the same config.Permissions) at the same instant — splitting it
// into several calls would risk a caller observing a mission rule added
// between two calls that config.Permissions itself could never explain.
type PermissionsLister interface {
	Snapshot() PermissionsSnapshot
}

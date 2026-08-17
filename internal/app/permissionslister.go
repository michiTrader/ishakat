// permissionslister.go implements internal/tui.PermissionsLister (§13, Step
// 32's own left-over UI half) — the concrete adapter that bridges
// /permissions to a real *permissions.Guard plus this session's own
// config.Permissions, the exact role toolslister.go's toolsLister already
// plays for /tools' own read side. internal/app is the one place already
// trusted to import both internal/permissions and internal/tui (§6.1).
package app

import (
	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/permissions"
	"github.com/MichiTrader/ishakat/internal/tui"
)

// permissionsLister is the real tui.PermissionsLister, backed by the same
// *permissions.Guard already bound into this session's AgentOptions.Runner
// (buildAgentOptions' own guard variable) plus a static copy of
// config.Permissions for the three policy knobs (Read/Write/Shell/
// AllowSession/ShellDeny/WriteDeny) a *permissions.Guard has no exported
// getter for today — guard.go's own permissions field is unexported, and
// widening that package's surface with a getter used by exactly one caller
// here would be the same "do not export a security-sensitive internal
// purely for one reader" reasoning ToolsLister's own EditTool/CreateTool
// doc comment already applies to parseManifest/checkManifestSafety.
// config.Permissions itself is immutable for the life of a session (there
// is no live-reload of config.toml), so a value copied once at
// construction never goes stale the way a *permissions.Guard's own mutable
// fields (autonomy, missionDeny, bashScopeAllow) would if cached the same
// way — which is exactly why guard is read fresh on every Snapshot() call
// below but cfgPerms is not.
type permissionsLister struct {
	guard    *permissions.Guard
	cfgPerms config.Permissions
}

var _ tui.PermissionsLister = permissionsLister{}

// NewPermissionsLister returns nil when guard is nil (cfg.Tools.Enabled ==
// false, matching every other Guard-backed seam in this package — see
// missionGuardOrNil's own doc comment for the identical boxed-nil-interface
// hazard this constructor avoids the same way), matching
// tui.PermissionsLister's own documented nil-is-safe contract.
func NewPermissionsLister(guard *permissions.Guard, cfgPerms config.Permissions) tui.PermissionsLister {
	if guard == nil {
		return nil
	}
	return permissionsLister{guard: guard, cfgPerms: cfgPerms}
}

// Snapshot reads every field fresh off the live Guard (Autonomy/
// MissionRules/BashScope are all safe for concurrent access — see their
// own doc comments in guard.go), so a mission confirmed or a tool scope
// chosen between two /permissions calls in the same session is always
// reflected, matching PermissionsLister's own doc comment on why this
// interface is re-consulted rather than cached.
func (l permissionsLister) Snapshot() tui.PermissionsSnapshot {
	rules := l.guard.MissionRules()
	out := make([]tui.PermissionsMissionRule, len(rules))
	for i, r := range rules {
		out[i] = tui.PermissionsMissionRule{Capability: r.Capability, Pattern: r.Pattern}
	}
	denials := l.guard.RecentDenials()
	deniedOut := make([]tui.PermissionsDenial, len(denials))
	for i, d := range denials {
		deniedOut[i] = tui.PermissionsDenial{Tool: d.Tool, Reason: d.Reason, Tier: tierString(d.Tier), When: d.When}
	}
	return tui.PermissionsSnapshot{
		Autonomy:      l.guard.Autonomy().String(),
		Read:          l.cfgPerms.Read,
		Write:         l.cfgPerms.Write,
		Shell:         l.cfgPerms.Shell,
		AllowSession:  l.cfgPerms.AllowSession,
		MissionRules:  out,
		BashScope:     l.guard.BashScope(),
		ShellDeny:     l.cfgPerms.ShellDeny,
		WriteDeny:     l.cfgPerms.WriteDeny,
		RecentDenials: deniedOut,
	}
}

// tierString names a permissions.Tier in plain English for
// tui.PermissionsDenial's own Tier field -- a short identifier
// ("safe"/"controlled"/"sensitive"/"critical"), not toolapprove.go's own
// Spanish dialog prose (tierLabel), since a /permissions list is a plain
// audit view, not the approval dialog that copy was written for.
func tierString(t permissions.Tier) string {
	switch t {
	case permissions.Safe:
		return "safe"
	case permissions.Controlled:
		return "controlled"
	case permissions.Sensitive:
		return "sensitive"
	case permissions.Critical:
		return "critical"
	default:
		return "unknown"
	}
}

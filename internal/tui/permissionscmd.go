// permissionscmd.go implements /permissions' own runner (§13, §21.14's own
// Step 32 closing criterion), read side only for this first slice — see
// permissions.go's own package comment for why a write half (changing
// autonomy) and a recently-denied list are both deliberately left for a
// later increment rather than folded into this one.
//
// This mirrors runConfigCommand's own shape almost exactly: a single
// slashNotice built from one live snapshot, never a fresh read from inside
// Update beyond the one Snapshot() call itself.
package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// runPermissionsCommand renders §21.4's own layers 1, 3, 4 and 5 (skipping
// layer 2 — /trust's own concern) from one PermissionsLister.Snapshot()
// call. m.permissionsLister is nil for every test in this package that
// never sets Options.PermissionsLister, and for any real run with tools
// disabled (buildAgentOptions never builds a *permissions.Guard then) —
// reported instead of a nil dereference, the same "nothing wired, nothing
// happens" default runConfigCommand already follows for a nil m.cfg.
func (m Root) runPermissionsCommand() (tea.Model, tea.Cmd) {
	g := m.lay.glyphs()
	if m.permissionsLister == nil {
		return m.slashNotice(g.warnMark + " no hay una politica de permisos activa en esta sesion")
	}

	snap := m.permissionsLister.Snapshot()

	var b strings.Builder
	fmt.Fprintf(&b, "%s permissions %s autonomy: %s", g.assistantMark, g.dot, orDash(snap.Autonomy))

	fmt.Fprintf(&b, "\n\n[layer 3 -- autonomy]")
	fmt.Fprintf(&b, "\n  read   %s", orDash(snap.Read))
	fmt.Fprintf(&b, "\n  write  %s", orDash(snap.Write))
	fmt.Fprintf(&b, "\n  shell  %s", orDash(snap.Shell))
	fmt.Fprintf(&b, "\n  allow_session  %s", yesNo(snap.AllowSession))

	fmt.Fprintf(&b, "\n\n[layer 4 -- mission]")
	if len(snap.MissionRules) == 0 {
		b.WriteString("\n  (no active mission constraints)")
	} else {
		for _, r := range snap.MissionRules {
			fmt.Fprintf(&b, "\n  deny  %-6s %s", r.Capability, r.Pattern)
		}
	}
	if len(snap.BashScope) == 0 {
		b.WriteString("\n  bash scope: everything installed")
	} else {
		fmt.Fprintf(&b, "\n  bash scope: %s", strings.Join(snap.BashScope, ", "))
	}

	fmt.Fprintf(&b, "\n\n[layer 1 -- invariants, not editable]")
	if len(snap.ShellDeny) > 0 {
		fmt.Fprintf(&b, "\n  shell_deny  %s", strings.Join(snap.ShellDeny, ", "))
	}
	if len(snap.WriteDeny) > 0 {
		fmt.Fprintf(&b, "\n  write_deny  %s", strings.Join(snap.WriteDeny, ", "))
	}
	b.WriteString("\n  rm -rf /, curl|sh, git push --force and a sub-agent")
	b.WriteString("\n  requesting a capability its parent lacks are always refused")

	return m.slashNotice(b.String())
}

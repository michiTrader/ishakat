// permissionscmd.go implements /permissions' own runner (§13, §21.14's own
// Step 32 closing criterion, §21.16 decision 4). Bare "/permissions" is the
// read side landed first (see permissions.go's own package comment); this
// file now also carries the write half decision 4 names — "the whole
// interface for reading and changing autonomy" — as "/permissions autonomy
// <level>". The bare-command render now also carries §21.13's own
// acceptance-narrative item 10's "recently-denied list" (Step 32 part 7):
// permissions.Guard tracks a bounded denial history, and
// PermissionsSnapshot.RecentDenials (permissions.go) mirrors it.
//
// The write half deliberately mirrors resolveTrust (trust.go) rather than
// switchTheme (theme.go): /trust's own three-way choice (auto/agile/
// readonly) is exactly this command's own vocabulary, right down to
// reusing the same TrustStore.Save(autonomy string) error seam — Root
// already carries a non-nil m.trustStore in every real run (internal/app's
// resolveProjectTrust always builds one, tools enabled or not), and its
// Save already does everything a live autonomy change needs: persist to
// trust.json for this project's own path, and call guard.SetAutonomy on
// the session's live *permissions.Guard. Inventing a second write path
// through PermissionsLister for the exact same effect would just be two
// seams doing one job.
//
// Unlike ParseAutonomy's own lenient "unrecognized -> Auto" default,
// parsePermissionsAutonomyArg refuses an unrecognized level outright: an
// autonomy command silently downgrading a typo to Auto -- the *least*
// restrictive of the three -- would be exactly the wrong direction for a
// permissions surface to fail in.
package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// runPermissionsCommand dispatches on args: no args (or anything that
// isn't the "autonomy <level>" shape) renders §21.4's own layers 1, 3, 4
// and 5 (skipping layer 2 — /trust's own concern) from one
// PermissionsLister.Snapshot() call; "autonomy <level>" applies and
// persists a new layer-3 autonomy exactly as /trust's own resolveTrust
// does. m.permissionsLister is nil for every test in this package that
// never sets Options.PermissionsLister, and for any real run with tools
// disabled (buildAgentOptions never builds a *permissions.Guard then) —
// reported instead of a nil dereference, the same "nothing wired, nothing
// happens" default runConfigCommand already follows for a nil m.cfg. The
// autonomy-change branch does not require m.permissionsLister at all — it
// only needs m.trustStore, exactly like /trust's own dialog — so it is
// checked before the nil-lister guard below, not after.
func (m Root) runPermissionsCommand(args string) (tea.Model, tea.Cmd) {
	g := m.lay.glyphs()

	if level, ok := parsePermissionsAutonomyArg(args); ok {
		return m.applyPermissionsAutonomy(level)
	}
	if isPermissionsAutonomyArg(args) {
		return m.slashNotice(g.warnMark + " nivel desconocido -- usa auto, agile o readonly")
	}

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

	fmt.Fprintf(&b, "\n\n[recently denied]")
	if len(snap.RecentDenials) == 0 {
		b.WriteString("\n  (nothing refused this session)")
	} else {
		for _, d := range snap.RecentDenials {
			fmt.Fprintf(&b, "\n  %s  %-10s %s  %s", permissionsDenialAge(d.When), d.Tool, d.Tier, d.Reason)
		}
	}

	return m.slashNotice(b.String())
}

// permissionsDenialAge renders when in the same short "3 days"/"5 hours"/
// "12 minutes"/"moments" bucketing resumemenu.go's own resumeAge already
// uses -- duplicated here rather than exported and imported (that helper
// is unexported to resumemenu.go, and this file's own denial list is the
// only other reader in this package, so duplicating a three-line time
// bucketer is cheaper than widening resumeAge's own surface for one
// caller).
func permissionsDenialAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "moments"
	case d < time.Hour:
		return fmt.Sprintf("%d minutes", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d hours", int(d/time.Hour))
	default:
		return fmt.Sprintf("%d days", int(d/(24*time.Hour)))
	}
}

// permissionsAutonomyLevels is the write half's own vocabulary --
// permissions.Autonomy.String()'s exact three words, the same ones
// trustOptions (trust.go) already offers through the first-run dialog.
// Kept as a slice, not a map, so the rejection message below can list them
// in a stable, human-chosen order rather than Go's randomized map order.
var permissionsAutonomyLevels = []string{"auto", "agile", "readonly"}

// isPermissionsAutonomyArg reports whether args begins with the literal
// word "autonomy" at all, regardless of whether what follows it is a
// level parsePermissionsAutonomyArg recognizes. Used only to decide
// between "unknown level" (this word, bad argument) and "fall through to
// the read-only snapshot" (anything else entirely, e.g. a bare
// "/permissions" or a future sub-command this dispatch does not know yet)
// -- runPermissionsCommand's own two-call shape needs this distinction
// because parsePermissionsAutonomyArg's own false does not by itself say
// which of those two cases happened.
func isPermissionsAutonomyArg(args string) bool {
	fields := strings.Fields(args)
	return len(fields) > 0 && fields[0] == "autonomy"
}

// parsePermissionsAutonomyArg recognizes exactly "autonomy <level>" (any
// surrounding whitespace, matching slash.Parse's own already-trimmed
// args), returning the level and true only when it is one of
// permissionsAutonomyLevels' own three words. Anything else -- wrong
// word count, an unrecognized level, or args not starting with
// "autonomy" at all -- returns "", false, deliberately never falling
// back to permissions.ParseAutonomy's own lenient "default to auto"
// behavior: see this file's own package comment for why a typo silently
// becoming the least restrictive setting would be the wrong direction to
// fail in here.
func parsePermissionsAutonomyArg(args string) (level string, ok bool) {
	fields := strings.Fields(args)
	if len(fields) != 2 || fields[0] != "autonomy" {
		return "", false
	}
	for _, l := range permissionsAutonomyLevels {
		if fields[1] == l {
			return l, true
		}
	}
	return "", false
}

// applyPermissionsAutonomy applies level immediately (m.footer.Autonomy,
// exactly like resolveTrust) and persists it best-effort via
// m.trustStore.Save -- the identical seam and identical "the display
// already changed, hiding a write failure would be a worse surprise"
// reasoning resolveTrust (trust.go) already follows for /trust's own
// write. m.trustStore == nil is a supported value (its own doc comment on
// Root.trustStore): the level still applies for the running session, it
// just does not survive a restart, reported inline rather than silently.
func (m Root) applyPermissionsAutonomy(level string) (tea.Model, tea.Cmd) {
	g := m.lay.glyphs()
	m.footer.Autonomy = level

	msg := g.assistantMark + " permissions autonomy " + g.dot + " " + level
	switch {
	case m.trustStore == nil:
		msg += " (solo esta sesion -- no hay donde guardarlo)"
	default:
		if err := m.trustStore.Save(level); err != nil {
			msg += " (no se pudo guardar: " + err.Error() + ")"
		}
	}
	return m.slashNotice(msg)
}

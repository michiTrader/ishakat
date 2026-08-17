// trustcmd.go implements /trust's own runner (§13, §21.4 layer 2's own
// row: "review or change the project's trust decision"). Step 30 shipped
// everything a first-run project needs — ModeTrust's own dialog
// (trust.go), NewRoot opening it automatically exactly once when
// Options.NeedsTrust is true — but left no way to deliberately reopen
// that same question on a *later* run once a decision is already saved:
// the only way to make ishakat ask again was to delete trust.json by
// hand, outside the running program entirely. This file closes that gap.
//
// runTrustCommand deliberately does not re-detect git state (no second
// internal/app.DetectGit-shaped probe exists here, nor could one: §6.1
// forbids internal/tui from shelling out at all, confirmed by
// TestToolsNoImportaTUI's sibling rule). It reuses Root's own
// gitInGit/gitClean/gitBranch fields instead — Options' own three fields
// of the same name, captured once at startup by NewRoot (root.go) — the
// same "compute once, reuse for the life of the session" choice cwd
// itself already makes for ShortenPath's own budget decision.
package tui

import tea "charm.land/bubbletea/v2"

// runTrustCommand opens ModeTrust over Root's own already-known cwd/git
// facts, the identical dialog a first run with no saved decision would
// have shown — same trustOptions, same cursor start, same "Esc = 2"
// default (trust.go's own updateTrust/resolveTrust are unchanged; this
// command only ever decides *when* the dialog opens, never how it
// resolves). Unlike a first run, m.trust here may already hold a stale
// value from an earlier ModeTrust visit this same session — newTrustDialog
// rebuilds it fresh every call, so there is nothing to reset first.
func (m Root) runTrustCommand() (tea.Model, tea.Cmd) {
	m.mode = ModeTrust
	m.trust = newTrustDialog(m.cwd, m.gitInGit, m.gitClean, m.gitBranch)
	return m, nil
}

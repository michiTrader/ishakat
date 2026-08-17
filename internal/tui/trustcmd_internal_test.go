package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/theme"
)

// TestSlashTrustOpensModeTrust is /trust's own closing criterion: typed
// from an ordinary already-trusted chat session (newHeadlessRoot never
// sets Options.NeedsTrust), it must still reopen the identical dialog a
// first run would have shown -- the gap this command exists to close.
func TestSlashTrustOpensModeTrust(t *testing.T) {
	var m tea.Model = newHeadlessRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/trust")

	root := m.(Root)
	if root.mode != ModeTrust {
		t.Fatalf("mode = %v, want ModeTrust", root.mode)
	}
}

// TestSlashTrustCarriesRootsOwnGitFacts confirms runTrustCommand reuses
// Root's own persisted gitInGit/gitClean/gitBranch (set once by NewRoot
// from Options.GitInGit/GitClean/GitBranch) rather than defaulting to the
// zero-value "git: no" line every headless test would otherwise show.
func TestSlashTrustCarriesRootsOwnGitFacts(t *testing.T) {
	root := NewRoot(Options{
		Version:   "0.0.0-test",
		CWD:       "/home/user/projects/orbital-dash",
		Theme:     theme.Load(""),
		Cap:       theme.CapNone,
		NoTTY:     true,
		GitInGit:  true,
		GitClean:  true,
		GitBranch: "main",
	})
	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/trust")

	r := m.(Root)
	if r.mode != ModeTrust {
		t.Fatalf("mode = %v, want ModeTrust", r.mode)
	}
	out := r.renderTrust()
	if !contains(out, "branch main") {
		t.Fatalf("renderTrust missing the git line carried from Options: %s", out)
	}
}

// TestSlashTrustSubmitStillPersistsThroughTrustStore proves the reopened
// dialog is not a read-only preview: choosing an option still calls
// through to m.trustStore.Save exactly like a first-run dialog would,
// since runTrustCommand only decides when ModeTrust opens, never how
// updateTrust/resolveTrust behave once it has.
func TestSlashTrustSubmitStillPersistsThroughTrustStore(t *testing.T) {
	store := &fakeTrustStore{}
	root := withTrustStoreRoot(newHeadlessRoot(), store)

	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/trust")
	if m.(Root).mode != ModeTrust {
		t.Fatalf("mode = %v, want ModeTrust before submitting a choice", m.(Root).mode)
	}

	m, _ = m.(Root).updateTrust(tea.KeyPressMsg{Code: tea.KeyEnter})
	r := m.(Root)
	if r.mode != ModeChat {
		t.Fatalf("mode = %v, want ModeChat after resolving the reopened dialog", r.mode)
	}
	if len(store.saved) != 1 || store.saved[0] != "auto" {
		t.Fatalf("store.saved = %v, want [auto]", store.saved)
	}
}

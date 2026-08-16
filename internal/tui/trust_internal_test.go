package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/theme"
)

type fakeTrustStore struct {
	saved []string
	err   error
}

func (s *fakeTrustStore) Save(autonomy string) error {
	s.saved = append(s.saved, autonomy)
	return s.err
}

func newTrustRoot(store TrustStore) Root {
	return NewRoot(Options{
		Version:    "0.0.0-test",
		CWD:        "/home/user/projects/orbital-dash",
		Theme:      theme.Load(""),
		Cap:        theme.CapNone,
		NoTTY:      true,
		NeedsTrust: true,
		GitInGit:   true,
		GitClean:   true,
		GitBranch:  "main",
		TrustStore: store,
	})
}

func TestNewRootOpensModeTrustWhenNeedsTrust(t *testing.T) {
	r := newTrustRoot(nil)
	if r.mode != ModeTrust {
		t.Fatalf("mode = %v, want ModeTrust", r.mode)
	}
}

func TestNewRootStaysOnModeChatWithoutNeedsTrust(t *testing.T) {
	r := NewRoot(Options{Version: "0.0.0-test", CWD: "/x", Theme: theme.Load(""), Cap: theme.CapNone, NoTTY: true})
	if r.mode != ModeChat {
		t.Fatalf("mode = %v, want ModeChat", r.mode)
	}
}

func TestUpdateTrustMoveSelWraps(t *testing.T) {
	r := newTrustRoot(nil)
	m, _ := r.updateTrust(tea.KeyPressMsg{Code: tea.KeyUp})
	r = m.(Root)
	if r.trust.sel != len(trustOptions)-1 {
		t.Fatalf("sel = %d, want wrapped to %d", r.trust.sel, len(trustOptions)-1)
	}
}

func TestUpdateTrustEscResolvesToAgileNotCancel(t *testing.T) {
	store := &fakeTrustStore{}
	r := newTrustRoot(store)
	m, _ := r.updateTrust(tea.KeyPressMsg{Code: tea.KeyEsc})
	r = m.(Root)
	if r.mode != ModeChat {
		t.Fatalf("mode = %v, want ModeChat", r.mode)
	}
	if r.footer.Autonomy != "agile" {
		t.Fatalf("footer.Autonomy = %q, want %q", r.footer.Autonomy, "agile")
	}
	if len(store.saved) != 1 || store.saved[0] != "agile" {
		t.Fatalf("store.saved = %v, want [agile]", store.saved)
	}
}

func TestUpdateTrustSubmitAppliesHighlightedChoice(t *testing.T) {
	store := &fakeTrustStore{}
	r := newTrustRoot(store)
	// Move down once: row 0 (auto) -> row 1 (agile).
	m, _ := r.updateTrust(tea.KeyPressMsg{Code: tea.KeyDown})
	r = m.(Root)
	m, _ = r.updateTrust(tea.KeyPressMsg{Code: tea.KeyEnter})
	r = m.(Root)
	if r.mode != ModeChat {
		t.Fatalf("mode = %v, want ModeChat", r.mode)
	}
	if r.footer.Autonomy != "agile" {
		t.Fatalf("footer.Autonomy = %q, want %q", r.footer.Autonomy, "agile")
	}
	if len(store.saved) != 1 || store.saved[0] != "agile" {
		t.Fatalf("store.saved = %v, want [agile]", store.saved)
	}
}

func TestUpdateTrustSubmitAutoWhenCursorOnFirstRow(t *testing.T) {
	store := &fakeTrustStore{}
	r := newTrustRoot(store)
	m, _ := r.updateTrust(tea.KeyPressMsg{Code: tea.KeyEnter})
	r = m.(Root)
	if r.footer.Autonomy != "auto" {
		t.Fatalf("footer.Autonomy = %q, want %q", r.footer.Autonomy, "auto")
	}
}

func TestRenderTrustShowsGitLineAndOptions(t *testing.T) {
	r := newTrustRoot(nil)
	out := r.renderTrust()
	if !contains(out, "How should I work here?") {
		t.Fatalf("renderTrust missing question:\n%s", out)
	}
	if !contains(out, "branch main") {
		t.Fatalf("renderTrust missing git line:\n%s", out)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}

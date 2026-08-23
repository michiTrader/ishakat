package tui

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/skills"
)

// TestSlashReloadNilFactoryDegradesInsteadOfPanicking covers Root.reloadFor
// == nil (every other test in this package, and any caller with nothing
// wired) — the same "explain the gap instead of a silent no-op" rule
// startLogin's own nil check already follows for /login.
func TestSlashReloadNilFactoryDegradesInsteadOfPanicking(t *testing.T) {
	root := newHeadlessRoot()
	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/reload")
	got := m.(Root)
	if len(got.transcript) != 1 {
		t.Fatalf("expected one notice entry, got %d: %v", len(got.transcript), got.transcript)
	}
}

// TestSlashReloadCallsFactoryAndAppliesResult drives the whole round trip:
// /reload with a wired ReloadFactory returns a tea.Cmd, and feeding that
// Cmd's own ReloadedMsg back through Update must replace m.keys/m.skills/
// m.system and copy the fresh Cfg's fields into the existing *config.Config
// pointer — the same pointer-identity contract applyCatalogRefreshed
// already establishes for F2's own hot-apply seam.
func TestSlashReloadCallsFactoryAndAppliesResult(t *testing.T) {
	root := newHeadlessRoot()
	cfg := &config.Config{}
	root.cfg = cfg
	root.reloadFor = func(ctx context.Context) ReloadResult {
		return ReloadResult{
			Cfg:    &config.Config{App: config.App{SystemPrompt: "reloaded cfg marker"}},
			Keys:   config.Keys{Submit: "ctrl+x"},
			Skills: skills.Result{Skills: []skills.Skill{{Name: "demo"}}},
			System: "fresh system prompt",
		}
	}

	got := reloadCmd(context.Background(), root.reloadFor)()
	reloaded, ok := got.(ReloadedMsg)
	if !ok {
		t.Fatalf("reloadCmd produced %T, want ReloadedMsg", got)
	}

	next, _ := root.Update(reloaded)
	nr := next.(Root)

	if nr.keys.Submit != "ctrl+x" {
		t.Errorf("keys.Submit = %q, want ctrl+x after reload", nr.keys.Submit)
	}
	if len(nr.skills.Skills) != 1 || nr.skills.Skills[0].Name != "demo" {
		t.Errorf("skills = %+v, want the reloaded skill list", nr.skills)
	}
	if nr.system != "fresh system prompt" {
		t.Errorf("system = %q, want the reloaded prompt", nr.system)
	}
	if cfg.App.SystemPrompt != "reloaded cfg marker" {
		t.Errorf("cfg.App.SystemPrompt = %q, want the reloaded cfg copied into the original pointer", cfg.App.SystemPrompt)
	}
}

// TestApplyReloadedWithNilCfgIsANoOp covers ReloadFactory's documented
// failure answer (a corrupt config.toml): Root's own keys/skills/system
// must stay exactly as they were, the same no-op contract
// applyCatalogRefreshed's nil-Catalog case already establishes.
func TestApplyReloadedWithNilCfgIsANoOp(t *testing.T) {
	root := newHeadlessRoot()
	root.keys = NewMap(config.Keys{Submit: "enter"})
	root.skills = skills.Result{Skills: []skills.Skill{{Name: "kept"}}}
	root.system = "kept system prompt"

	next, _ := root.Update(ReloadedMsg{Result: ReloadResult{Warn: "config.toml invalido"}})
	nr := next.(Root)

	if nr.keys.Submit != "enter" {
		t.Errorf("keys.Submit changed on a failed reload: got %q", nr.keys.Submit)
	}
	if len(nr.skills.Skills) != 1 || nr.skills.Skills[0].Name != "kept" {
		t.Errorf("skills changed on a failed reload: got %+v", nr.skills)
	}
	if nr.system != "kept system prompt" {
		t.Errorf("system changed on a failed reload: got %q", nr.system)
	}
}

// TestOptionsReloadForIsWiredIntoRoot is the same
// NewRoot(Options{...})-not-the-private-field regression shape
// TestOptionsTitleStoreIsWiredIntoRoot/TestOptionsSettingsStoreIsWiredIntoRoot
// already establish for every other Options-carried seam.
func TestOptionsReloadForIsWiredIntoRoot(t *testing.T) {
	called := false
	factory := ReloadFactory(func(ctx context.Context) ReloadResult {
		called = true
		return ReloadResult{}
	})
	root := NewRoot(Options{ReloadFor: factory})
	if root.reloadFor == nil {
		t.Fatal("NewRoot did not wire Options.ReloadFor into Root.reloadFor")
	}
	root.reloadFor(context.Background())
	if !called {
		t.Fatal("Root.reloadFor is not the factory Options.ReloadFor supplied")
	}
}

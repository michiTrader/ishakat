package tui_test

import (
	"testing"

	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/tui"
)

func TestNewMapRespetaConfiguracion(t *testing.T) {
	k := config.Keys{
		Submit: "enter",
		Cancel: "esc",
		Quit:   "ctrl+c",
	}
	m := tui.NewMap(k)
	if m.Submit != "enter" || m.Cancel != "esc" || m.Quit != "ctrl+c" {
		t.Errorf("NewMap no respetó los valores configurados: %+v", m)
	}
}

func TestNewMapRellenaCamposVaciosConDefault(t *testing.T) {
	m := tui.NewMap(config.Keys{}) // todo vacío
	if m.Submit == "" || m.Cancel == "" || m.Quit == "" {
		t.Errorf("NewMap dejó campos vacíos sin default: %+v", m)
	}
	if m.Cancel != "esc" {
		t.Errorf("Cancel default = %q, want %q", m.Cancel, "esc")
	}
	if m.Quit != "ctrl+c" {
		t.Errorf("Quit default = %q, want %q", m.Quit, "ctrl+c")
	}
}

// TestNewMapToggleFoldDefaultsToCtrlR pins the keybinding chosen for
// codeblock folding (§17 2026-08-18 "code blocks fill the terminal" entry):
// ctrl+r, not ctrl+o, since ctrl+o is already reserved for ModelCycle (see
// tui.Map.ToggleFold's own doc comment for why reusing it would collide with
// that still-pending feature).
func TestNewMapToggleFoldDefaultsToCtrlR(t *testing.T) {
	m := tui.NewMap(config.Keys{})
	if m.ToggleFold != "ctrl+r" {
		t.Errorf("ToggleFold default = %q, want %q", m.ToggleFold, "ctrl+r")
	}
}

// TestNewMapToggleFoldRespectsConfiguration is ToggleFold's own counterpart
// to TestNewMapRespetaConfiguracion above: a configured value must survive
// unchanged, not be silently replaced by the default.
func TestNewMapToggleFoldRespectsConfiguration(t *testing.T) {
	m := tui.NewMap(config.Keys{ToggleFold: "ctrl+g"})
	if m.ToggleFold != "ctrl+g" {
		t.Errorf("ToggleFold = %q, want the configured %q", m.ToggleFold, "ctrl+g")
	}
}

// TestNewMapQuitRepeatDefaultsToTwo is RC-1's safety net when Load is
// skipped: an empty Keys (what newTestRoot used to feed, and what a
// caller that never set Cfg still does) must still require two presses.
func TestNewMapQuitRepeatDefaultsToTwo(t *testing.T) {
	m := tui.NewMap(config.Keys{})
	if m.Quit != "ctrl+c" {
		t.Errorf("Quit default = %q, want %q", m.Quit, "ctrl+c")
	}
	if m.QuitRepeat != 2 {
		t.Errorf("QuitRepeat default = %d, want 2", m.QuitRepeat)
	}
}

// TestNewMapRewritesLegacyMultiWordQuit is the last-resort parse that
// NewMap keeps even without going through validateKeys: the form that
// used to ship in defaults.toml must become a single chord plus a count.
func TestNewMapRewritesLegacyMultiWordQuit(t *testing.T) {
	m := tui.NewMap(config.Keys{Quit: "ctrl+c ctrl+c"})
	if m.Quit != "ctrl+c" {
		t.Errorf("Quit = %q, want the rewritten single chord %q", m.Quit, "ctrl+c")
	}
	if m.QuitRepeat != 2 {
		t.Errorf("QuitRepeat = %d, want 2 (token count of the legacy form)", m.QuitRepeat)
	}
}

// TestNewMapQuitRepeatOneQuitsOnFirstPress pins the data representation:
// a number of 1 is a first-press quit, not a special case.
func TestNewMapQuitRepeatOneQuitsOnFirstPress(t *testing.T) {
	m := tui.NewMap(config.Keys{Quit: "ctrl+c", QuitRepeat: 1})
	if m.QuitRepeat != 1 {
		t.Errorf("QuitRepeat = %d, want the configured 1", m.QuitRepeat)
	}
}

func TestNewMapQuitRepeatThreeIsHonored(t *testing.T) {
	m := tui.NewMap(config.Keys{QuitRepeat: 3})
	if m.Quit != "ctrl+c" {
		t.Errorf("Quit = %q, want the default %q", m.Quit, "ctrl+c")
	}
	if m.QuitRepeat != 3 {
		t.Errorf("QuitRepeat = %d, want the configured 3", m.QuitRepeat)
	}
}

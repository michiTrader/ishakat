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

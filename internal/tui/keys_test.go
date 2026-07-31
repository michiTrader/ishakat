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

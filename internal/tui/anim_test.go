package tui_test

import (
	"testing"

	"github.com/MichiTrader/ishakat/internal/theme"
	"github.com/MichiTrader/ishakat/internal/tui"
)

// The bug: ui.animations.mode = "off" changed nothing. NewRoot only ever
// checked for the literal string "off", so "auto" — the documented default —
// resolved to "animations on" regardless of TTY, colour or width, and the
// verdict it did compute for "off" was stored in a struct field nothing read.
// These are the table from docs/PLAN.md's comment on the key, restated as
// cases instead of prose:
//
//	mode = "auto"  # auto = off si !TTY, TERM=dumb, NO_COLOR o ancho<40
func TestAnimationsOffFor(t *testing.T) {
	cases := []struct {
		name  string
		mode  string
		cap   theme.Capability
		noTTY bool
		bp    tui.Breakpoint
		off   bool
	}{
		{"explicit off wins over a capable wide TTY", "off", theme.CapTruecolor, false, tui.BPAncho, true},
		{"explicit on wins over no TTY", "on", theme.CapTruecolor, true, tui.BPAncho, false},
		{"explicit on wins over NO_COLOR", "on", theme.CapNone, false, tui.BPAncho, false},
		{"explicit on wins under 40 columns", "on", theme.CapTruecolor, false, tui.BPMinimo, false},
		{"auto is on for a capable wide TTY", "auto", theme.CapTruecolor, false, tui.BPAncho, false},
		{"auto is off with no TTY", "auto", theme.CapTruecolor, true, tui.BPAncho, true},
		{"auto is off with NO_COLOR/TERM=dumb", "auto", theme.CapNone, false, tui.BPAncho, true},
		{"auto is off under 40 columns", "auto", theme.CapTruecolor, false, tui.BPMinimo, true},
		{"unset behaves like auto", "", theme.CapTruecolor, false, tui.BPAncho, false},
		{"garbage behaves like auto", "yes-please", theme.CapNone, false, tui.BPAncho, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := tui.AnimationsOffFor(c.mode, c.cap, c.noTTY, c.bp); got != c.off {
				t.Errorf("AnimationsOffFor(%q, %v, noTTY=%v, %v) = %v, want %v",
					c.mode, c.cap, c.noTTY, c.bp, got, c.off)
			}
		})
	}
}

// battery_saver's own rule from the same block of docs/PLAN.md:
//
//	battery_saver = "auto"  # auto = baja a 6 fps al detectar Android/Termux
func TestFPSFor(t *testing.T) {
	cases := []struct {
		name         string
		fps          int
		batterySaver string
		termux       bool
		want         int
	}{
		{"zero fps falls back to AnimFPS", 0, "off", false, tui.AnimFPS},
		{"negative fps falls back to AnimFPS", -1, "off", false, tui.AnimFPS},
		{"explicit on caps fps even on a desktop", 12, "on", false, tui.BatterySaverFPS},
		{"explicit off keeps fps even on Termux", 12, "off", true, 12},
		{"auto keeps fps on a desktop", 12, "auto", false, 12},
		{"auto caps fps on Termux", 12, "auto", true, tui.BatterySaverFPS},
		{"unset behaves like auto on Termux", 12, "", true, tui.BatterySaverFPS},
		{"on does not raise an already-low fps", 4, "on", false, 4},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := tui.FPSFor(c.fps, c.batterySaver, c.termux); got != c.want {
				t.Errorf("FPSFor(%d, %q, termux=%v) = %d, want %d", c.fps, c.batterySaver, c.termux, got, c.want)
			}
		})
	}
}

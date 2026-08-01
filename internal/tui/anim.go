package tui

import (
	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/theme"
)

// This file is the whole of what [ui.animations] means. It used to mean less
// than the config file promised: NewRoot read mode only far enough to notice
// "off" (never "auto", the documented default, and never the width/color/TTY
// rules docs/PLAN.md §ui.animations spells out for it), read battery_saver
// only far enough to notice "on" (never "auto", also the documented default,
// which is supposed to watch for Termux), and stored the "off" verdict in
// Layout.AnimationsOff for a reader that did not exist — grep found the field
// written and never once read. A user who wrote mode = "off" in their config
// got exactly the same animated spinner as one who wrote nothing.
//
// Two functions, one rule each, so each can be tested against the line of the
// config file it resolves instead of against root.go's construction order.

// animationsCfg is the [ui.animations] block, or the defaults of
// config/defaults.toml when there is no configuration at all — which happens
// in tests and only there, since the real program always loads the file
// before it builds a Root.
//
// The defaults are restated rather than left to the zero value because a
// zero-valued config.Animations is actively wrong here: its Mode is "" (which
// AnimationsOffFor and FPSFor both treat as "auto", so that part is harmless
// by construction) but its GradientScroll is false, and the documented
// default is true.
//
// GradientScroll itself has no consumer in this file, and that omission is
// deliberate rather than left over: the only thing on screen a "scroll" could
// describe is the banner's gradient, and the banner is only ever on screen
// before the first turn — while the model is idle. Animating it would mean
// arming a ticker from Init for a frame nobody has asked to see move, which is
// the exact defect idle_internal_test.go now exists to catch. Rather than
// wire the field to a behaviour that would fail that test, or silently drop
// the key, it is read here (so config.Load's round-trip and validation stay
// honest about it) and left for whoever designs the animation that is
// supposed to consume it.
func animationsCfg(cfg *config.Config) config.Animations {
	if cfg == nil {
		return config.Animations{
			Mode:           "auto",
			FPS:            AnimFPS,
			GradientScroll: true,
			BatterySaver:   "auto",
		}
	}
	return cfg.UI.Animations
}

// AnimationsOffFor resolves ui.animations.mode against the facts tui is
// allowed to know about the terminal (§6.1: it does not read the environment
// itself, o.Cap and o.NoTTY arrive already answered from internal/app).
//
// The rule is docs/PLAN.md's comment on the config key, restated as code
// instead of paraphrased from memory:
//
//	mode = "auto"  # auto = off si !TTY, TERM=dumb, NO_COLOR o ancho<40
//
// "TERM=dumb" and "NO_COLOR" are exactly theme.CapNone — that is its own
// doc comment ("NO_COLOR o TERM=dumb") — so this function does not read the
// environment a second time to reconstruct a distinction theme already made.
// "ancho<40" is bp == BPMinimo, the boundary §9.1 already draws there for
// borders and boxed input; animating a spinner in the one breakpoint that
// cannot afford a border is the same mistake with an extra step.
//
// "on" and "off" are absolute: a user who spelled out either one gets it
// regardless of what the terminal can do, which is what asking explicitly
// ought to mean.
func AnimationsOffFor(mode string, cap theme.Capability, noTTY bool, bp Breakpoint) bool {
	switch mode {
	case "off":
		return true
	case "on":
		return false
	default: // "auto", "" (unset), or anything unrecognised falls back to auto
		return noTTY || cap == theme.CapNone || bp == BPMinimo
	}
}

// FPSFor resolves ui.animations.fps and ui.animations.battery_saver into the
// one number tickAnim needs.
//
// battery_saver's own rule, restated the same way:
//
//	battery_saver = "auto"  # auto = baja a 6 fps al detectar Android/Termux
//
// termux is o.Termux, xdg.IsTermux() resolved by internal/app for the same
// §6.1 reason Cap and NoTTY are: tui does not know what /proc looks like.
func FPSFor(fps int, batterySaver string, termux bool) int {
	if fps <= 0 {
		fps = AnimFPS
	}
	var saving bool
	switch batterySaver {
	case "on":
		saving = true
	case "off":
		saving = false
	default: // "auto", "" (unset), or anything unrecognised falls back to auto
		saving = termux
	}
	if saving && fps > BatterySaverFPS {
		fps = BatterySaverFPS
	}
	return fps
}

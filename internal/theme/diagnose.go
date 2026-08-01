package theme

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
)

// Diagnosis is what `ishakat doctor` needs to say about the terminal: the two
// decisions the interface makes about it, and — more importantly — why.
//
// The reason is the whole point of this type. Three of the bugs reported after
// the first hands-on session were the same bug seen from different angles: the
// logo came out as boxes, the gradient was white in PowerShell but coloured in
// Termux, and there was no way to tell whether the program had misdetected the
// terminal or the terminal was genuinely missing a font. Two booleans printed
// without their justification would not have helped; "ascii, because this
// Windows console sets neither WT_SESSION nor TERM" tells the user exactly
// which knob to turn.
type Diagnosis struct {
	// Color and Glyphs are the resolved decisions, the same values the
	// interface itself will use.
	Color  Capability
	Glyphs GlyphSet

	// ColorReason and GlyphsReason are one sentence each, phrased so it can be
	// printed straight after the value.
	ColorReason  string
	GlyphsReason string

	// Signals are the environment variables the two decisions are made from,
	// in the order a human would want to read them.
	Signals []Signal

	// Advice is the set of overrides worth trying if the decision above is
	// wrong. It is empty when the user has already overridden that axis by
	// hand: telling someone to set what they just set is noise.
	Advice []string
}

// Signal is one environment variable as the detection saw it. Unset is not the
// same as empty — TERM missing entirely is the signature of a Windows console
// host, which is a fact worth showing rather than hiding behind a blank.
type Signal struct {
	Name  string
	Value string
	Set   bool
}

// signalNames are the variables that feed either decision, colour first, then
// locale. Anything consulted by DetectEnv, consoleHint or DetectGlyphsEnv
// belongs here; if a new rule reads a new variable, it goes in this list too,
// so that `doctor` never reports a decision it cannot show the input for.
var signalNames = []string{
	"TERM",
	"COLORTERM",
	"TERM_PROGRAM",
	"WT_SESSION",
	"ConEmuANSI",
	"ANSICON",
	"NO_COLOR",
	"CLICOLOR",
	"CLICOLOR_FORCE",
	"LC_ALL",
	"LC_CTYPE",
	"LANG",
}

// Diagnose resolves both axes for the running process, writing to out. The
// writer matters because "is this a terminal at all" is not a question the
// environment can answer, and a redirected stdout gets no colour.
func Diagnose(colorOverride, glyphOverride string, out io.Writer) Diagnosis {
	d := DiagnoseEnv(colorOverride, glyphOverride, runtime.GOOS, os.Environ(), isTerminalWriter(out))

	// DetectWriter is the authoritative answer, because on Windows it can ask
	// the console API something no variable exposes. When it disagrees with
	// what the environment alone suggested, the probe wins and says so.
	if probed := DetectWriter(colorOverride, out); probed != d.Color {
		d.Color = probed
		d.ColorReason = "asked the terminal itself"
	}
	return d
}

// DiagnoseEnv is Diagnose against an explicit OS, environment and TTY answer.
// Every rule below is therefore exercisable from a test on Linux, including the
// Windows ones — the platform whose console caused the reports in the first
// place and the one the test suite will never run on.
func DiagnoseEnv(colorOverride, glyphOverride, goos string, env []string, tty bool) Diagnosis {
	d := Diagnosis{Signals: signalsOf(env)}

	switch cap, ok := overrideCapability(colorOverride); {
	case ok:
		d.Color, d.ColorReason = cap, fmt.Sprintf("set by [ui] color = %q", strings.TrimSpace(colorOverride))
	case !tty:
		d.Color, d.ColorReason = CapNone, "stdout is not a terminal"
	default:
		d.Color = DetectEnv(colorOverride, env)
		d.ColorReason = colorWitness(env)
	}

	if set, ok := overrideGlyphs(glyphOverride); ok {
		d.Glyphs, d.GlyphsReason = set, fmt.Sprintf("set by [ui] glyphs = %q", strings.TrimSpace(glyphOverride))
	} else {
		d.Glyphs = DetectGlyphsEnv(glyphOverride, goos, env)
		d.GlyphsReason = glyphWitness(goos, env)
	}

	d.Advice = advice(d, colorOverride, glyphOverride)
	return d
}

// signalsOf reads the whole list in one pass over the environment.
func signalsOf(env []string) []Signal {
	out := make([]Signal, 0, len(signalNames))
	for _, name := range signalNames {
		v, ok := lookupEnv(env, name)
		out = append(out, Signal{Name: name, Value: v, Set: ok})
	}
	return out
}

// Set returns only the signals that are present, which is what a report wants
// to print: a dozen "unset" lines bury the two that decided anything.
func (d Diagnosis) Set() []Signal {
	out := make([]Signal, 0, len(d.Signals))
	for _, s := range d.Signals {
		if s.Set && s.Value != "" {
			out = append(out, s)
		}
	}
	return out
}

// colorWitness names the variable that decided the colour capability. It walks
// the same order the detection does, so the answer is the actual cause and not
// a plausible-looking coincidence.
func colorWitness(env []string) string {
	if v, ok := lookupEnv(env, "NO_COLOR"); ok && v != "" && v != "0" {
		return "NO_COLOR is set"
	}
	if v, _ := lookupEnv(env, "COLORTERM"); v != "" {
		return "COLORTERM=" + v
	}
	if v, _ := lookupEnv(env, "WT_SESSION"); v != "" {
		return "WT_SESSION is set (Windows Terminal)"
	}
	if v, _ := lookupEnv(env, "ConEmuANSI"); strings.EqualFold(v, "on") {
		return "ConEmuANSI=on"
	}
	if v, _ := lookupEnv(env, "TERM_PROGRAM"); v != "" {
		return "TERM_PROGRAM=" + v
	}
	if v, _ := lookupEnv(env, "ANSICON"); v != "" {
		return "ANSICON is set"
	}
	if v, ok := lookupEnv(env, "TERM"); ok && v != "" {
		return "TERM=" + v
	}
	// No TERM and none of the console hints: this is a bare console host, and
	// naming what is missing is more useful than saying "detected".
	return "no TERM and no console hint in the environment"
}

// glyphWitness explains the repertoire the same way, per platform, because the
// rule genuinely differs: on Windows the question is which console host we are
// in, everywhere else it is what the locale promises.
func glyphWitness(goos string, env []string) string {
	if term, _ := lookupEnv(env, "TERM"); strings.EqualFold(term, "dumb") {
		return "TERM=dumb"
	}

	if strings.EqualFold(goos, "windows") {
		if v, _ := lookupEnv(env, "WT_SESSION"); v != "" {
			return "WT_SESSION is set (Windows Terminal, UTF-8 and Cascadia Mono)"
		}
		if v, _ := lookupEnv(env, "ConEmuANSI"); strings.EqualFold(v, "on") {
			return "ConEmuANSI=on"
		}
		if v, _ := lookupEnv(env, "TERM_PROGRAM"); v != "" {
			return "TERM_PROGRAM=" + v
		}
		if v, ok := lookupEnv(env, "TERM"); ok && v != "" {
			return "TERM=" + v + " (an MSYS2/Cygwin pty, UTF-8 by default)"
		}
		return "a bare Windows console: its output code page is the system's OEM one, not UTF-8"
	}

	if loc, ok := localeWitness(env); ok {
		if strings.Contains(strings.ToLower(loc), "utf") {
			return loc + " is a UTF-8 locale"
		}
		return loc + " is not a UTF-8 locale"
	}
	return "no locale set in the environment; assuming UTF-8"
}

// localeWitness returns the deciding locale variable as "NAME=value", following
// the same POSIX precedence the detection uses.
func localeWitness(env []string) (string, bool) {
	for _, key := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if v, ok := lookupEnv(env, key); ok && v != "" {
			return key + "=" + v, true
		}
	}
	return "", false
}

// advice suggests the override that fixes each decision if it came out wrong.
// It only speaks about axes the user has not already pinned, and it phrases
// both directions, because either can be the mistake: a console that shows
// boxes was guessed too generously, and one that renders blocks perfectly but
// got the ASCII look was guessed too meanly.
func advice(d Diagnosis, colorOverride, glyphOverride string) []string {
	var out []string

	if _, pinned := overrideGlyphs(glyphOverride); !pinned {
		if d.Glyphs.ASCII() {
			out = append(out, `if the sample above draws as blocks and lines rather than punctuation, your terminal is better than we guessed: set [ui] glyphs = "unicode"`)
		} else {
			out = append(out, `if the sample above shows boxes, question marks or garbled pairs, set [ui] glyphs = "ascii"`)
		}
	}

	if _, pinned := overrideCapability(colorOverride); !pinned && d.Color == CapNone {
		out = append(out, `if colour does work in this terminal, set [ui] color = "truecolor"`)
	}

	return out
}

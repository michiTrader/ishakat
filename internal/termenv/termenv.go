// Package termenv answers one question — should the interface draw itself
// inline (regular) or take the whole screen (fullscreen) — and shows its work.
//
// It exists as a package rather than an `if` because of a distinction that is
// easy to miss and expensive to get wrong: WSL is not a terminal, it is a
// kernel interface. The same WSL kernel can be drawn by Windows Terminal, which
// handles the alternate screen perfectly, or by a bare legacy console host,
// which does not. "WSL means regular" is therefore not a rule, it is a guess
// that is wrong half the time, and it was rejected explicitly when this design
// was approved.
//
// So detection answers two orthogonal questions and keeps them apart:
//
//   - Platform — where the process is running (Termux, WSL, macOS, Windows…).
//   - Host     — what is actually drawing our bytes (Windows Terminal, tmux,
//     iTerm2, an xterm-like, an unknown console…).
//
// The mode is decided by Host. Platform is only ever a tie-breaker, and in
// practice it breaks exactly one tie: Termux reports TERM=xterm-256color and
// would otherwise land in fullscreen, but a phone's native scrolling and text
// selection are worth more than reflow. That carve-out is one line, and it is
// the only place the platform overrides the host.
//
// The API mirrors internal/theme's Diagnose/DiagnoseEnv pair for a reason that
// theme's own comment states better than a paraphrase would: a decision printed
// without its justification does not help anybody. `doctor` needs to be able to
// say "fullscreen, because WT_SESSION is set", so Reason and Signals are part
// of the result rather than a debug aid.
//
// Purity is not stylistic here either. Thirteen of the scenarios this package
// must get right involve Termux, WSL and two different Windows console hosts —
// platforms this test suite will never run on. The only way those rules can be
// tested at all is if goos, the environment, the TTY answer and the two
// filesystem facts all arrive as parameters. internal/arch_test.go enforces it.
package termenv

import (
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/MichiTrader/ishakat/internal/xdg"
)

// Mode is the rendering decision: the only thing the rest of the tree needs
// from this package in order to draw.
//
// It is deliberately a two-value type and not a bool. `mode == ModeFullscreen`
// reads as a fact about the terminal; `fullscreen == true` reads as a feature
// flag, and this is not one — it is a property of where we are running.
type Mode int

const (
	// ModeRegular draws inline, leaving scrollback and selection to the
	// terminal. It is also the answer whenever we are not sure, because taking
	// somebody's screen on a guess is not recoverable from inside a pipe.
	ModeRegular Mode = iota
	// ModeFullscreen owns the screen: our own viewport, our own scrollback,
	// reflow on resize.
	ModeFullscreen
)

func (m Mode) String() string {
	if m == ModeFullscreen {
		return "fullscreen"
	}
	return "regular"
}

// Platform is where the process runs. It is not the terminal, and the whole
// point of separating it from Host is that it must not be mistaken for one.
type Platform int

const (
	PlatformUnknown Platform = iota
	PlatformTermux
	PlatformAndroid
	PlatformWSL
	PlatformLinux
	PlatformMacOS
	PlatformWindows
)

func (p Platform) String() string {
	switch p {
	case PlatformTermux:
		return "Termux"
	case PlatformAndroid:
		return "Android"
	case PlatformWSL:
		return "WSL"
	case PlatformLinux:
		return "Linux"
	case PlatformMacOS:
		return "macOS"
	case PlatformWindows:
		return "Windows"
	default:
		return "unknown"
	}
}

// Host is what draws our output, which is the question that actually decides
// the mode. A host is a claim about capability: "this thing implements the
// alternate screen well enough that we can hand it a viewport and get it back".
type Host int

const (
	// HostUnknown is the honest default and it maps to ModeRegular. An
	// unrecognised terminal is not a reason to take the user's screen.
	HostUnknown Host = iota
	HostNotATTY
	HostDumb
	HostMultiplexer
	HostWindowsTerminal
	HostVSCode
	HostITerm2
	HostAppleTerminal
	HostModernTerminal
	HostConEmu
	HostXtermLike
	HostLegacyConhost
)

func (h Host) String() string {
	switch h {
	case HostNotATTY:
		return "not a terminal"
	case HostDumb:
		return "dumb"
	case HostMultiplexer:
		return "Multiplexer"
	case HostWindowsTerminal:
		return "WindowsTerminal"
	case HostVSCode:
		return "VSCode"
	case HostITerm2:
		return "ITerm2"
	case HostAppleTerminal:
		return "AppleTerminal"
	case HostModernTerminal:
		return "ModernTerminal"
	case HostConEmu:
		return "ConEmu"
	case HostXtermLike:
		return "XtermLike"
	case HostLegacyConhost:
		return "LegacyConhost"
	default:
		return "Unknown"
	}
}

// Signal is one environment variable as the detection saw it. Unset and empty
// are kept distinct on purpose: TERM missing entirely is the signature of a
// bare Windows console, which is a fact worth showing rather than blanking.
type Signal struct {
	Name  string
	Value string
	Set   bool
}

// signalNames are the variables any rule below is allowed to read. If a new
// rule consults a new variable it goes in this list too, so `doctor` can never
// report a decision without showing its input.
var signalNames = []string{
	"TERM",
	"TERM_PROGRAM",
	"COLORTERM",
	"TMUX",
	"STY",
	"WT_SESSION",
	"WSL_DISTRO_NAME",
	"WSL_INTEROP",
	"ConEmuANSI",
	"ANSICON",
	"CI",
	"PREFIX",
	"ISHAKAT_TUI_MODE",
}

// Detection is the whole answer: the decision, the two facts it was derived
// from, why, and what to set if it came out wrong.
type Detection struct {
	Mode     Mode
	Platform Platform
	Host     Host

	// Reason is one sentence, phrased so it can be printed straight after the
	// mode: `fullscreen — WT_SESSION is set (Windows Terminal)`.
	Reason string

	// Signals are the variables that fed the decision, in reading order.
	Signals []Signal

	// Advice is the override that fixes this if it is wrong. Empty when the
	// user already pinned the mode by hand: telling somebody to set what they
	// just set is noise.
	Advice []string
}

// Probe is the two facts detection needs that are not in the environment: the
// kernel release string (which is what actually identifies WSL) and whether a
// path exists (which is what identifies Termux when $PREFIX has been lost).
//
// It is a struct of funcs so a test can answer them, because the alternative is
// a rule that can only be exercised by shipping it — and that is not a rule.
type Probe struct {
	KernelRelease func() string
	Exists        func(string) bool
}

// RealProbe reads the actual machine. Kept separate from DetectEnv so that
// every rule stays reachable from a table test.
func RealProbe() Probe {
	return Probe{
		KernelRelease: func() string {
			b, err := os.ReadFile("/proc/sys/kernel/osrelease")
			if err != nil {
				return ""
			}
			return string(b)
		},
		Exists: func(p string) bool {
			_, err := os.Stat(p)
			return err == nil
		},
	}
}

// Detect resolves the mode for the running process. override is the value of
// [ui] tui_mode, passed in rather than read here, because a detector that loads
// its own configuration is a detector that cannot be tested.
func Detect(override string, tty bool) Detection {
	return DetectEnv(override, runtime.GOOS, os.Environ(), tty, RealProbe())
}

// DetectEnv is Detect against an explicit OS, environment, TTY answer and
// probe. Every rule in §3 of docs/DESIGN-tui-mode.md is reachable from here,
// including the Termux, WSL and Windows ones.
//
// Order matters and follows the document top to bottom: overrides and hard
// stops first, then platform, then host. First match wins.
func DetectEnv(override, goos string, env []string, tty bool, probe Probe) Detection {
	d := Detection{Signals: signalsOf(env)}
	d.Platform = detectPlatform(goos, env, probe)

	// Rule 1 — the explicit setting. It wins over everything except the
	// physical impossibility handled just below.
	if m, ok := parseMode(override); ok {
		d.Host = detectHost(goos, env, tty)
		if m == ModeFullscreen && !tty {
			// Refusing an explicit setting needs justifying: fullscreen writes
			// cursor addressing and an alt-screen switch. Into a pipe or a log
			// file that is not a degraded interface, it is corruption of the
			// file the user asked for. The setting is honoured everywhere it
			// can be, and overruled only where it cannot.
			d.Mode, d.Host = ModeRegular, HostNotATTY
			d.Reason = fmt.Sprintf("[ui] tui_mode = %q, but stdout is not a terminal", strings.TrimSpace(override))
			d.Advice = append(d.Advice, "fullscreen needs a terminal; redirect-safe output is always regular")
			return d
		}
		d.Mode = m
		d.Reason = fmt.Sprintf("set by [ui] tui_mode = %q", strings.TrimSpace(override))
		return d
	}

	// Rule 2 — the environment variable, same shape, one rung lower.
	if raw, ok := lookupEnv(env, "ISHAKAT_TUI_MODE"); ok {
		if m, ok := parseMode(raw); ok {
			d.Host = detectHost(goos, env, tty)
			if m == ModeFullscreen && !tty {
				d.Mode, d.Host = ModeRegular, HostNotATTY
				d.Reason = "ISHAKAT_TUI_MODE=fullscreen, but stdout is not a terminal"
				d.Advice = append(d.Advice, "fullscreen needs a terminal; redirect-safe output is always regular")
				return d
			}
			d.Mode = m
			d.Reason = "set by ISHAKAT_TUI_MODE"
			return d
		}
		// An unparseable override is worth saying out loud rather than
		// silently ignoring: a typo'd ISHAKAT_TUI_MODE=fullscren that quietly
		// does nothing is how somebody spends an afternoon.
		d.Advice = append(d.Advice, fmt.Sprintf("ISHAKAT_TUI_MODE=%q is not a mode; expected \"regular\" or \"fullscreen\"", raw))
	}

	// Rules 3–6 — the honesty rules. Each one is a case where we cannot verify
	// we could leave the alternate screen again, so we never enter it.
	switch {
	case !tty:
		d.Mode, d.Host = ModeRegular, HostNotATTY
		d.Reason = "stdout is not a terminal"
		return d
	case isDumb(env):
		d.Mode, d.Host = ModeRegular, HostDumb
		d.Reason = "TERM=dumb"
		d.Advice = append(d.Advice, modeAdvice(ModeRegular))
		return d
	case isCI(env):
		d.Mode, d.Host = ModeRegular, HostUnknown
		d.Reason = "CI is set"
		d.Advice = append(d.Advice, modeAdvice(ModeRegular))
		return d
	}
	if term, ok := lookupEnv(env, "TERM"); (!ok || term == "") && !strings.EqualFold(goos, "windows") {
		// No TERM off Windows means something stripped the environment — a
		// cron job, a bare exec, a container without a pty. On Windows the
		// same absence is normal and is handled as LegacyConhost below.
		d.Mode, d.Host = ModeRegular, HostUnknown
		d.Reason = "no TERM in the environment"
		d.Advice = append(d.Advice, modeAdvice(ModeRegular))
		return d
	}

	// §3.3 — the host decides.
	d.Host = detectHost(goos, env, tty)
	d.Mode, d.Reason = modeForHost(d.Host, d.Platform, env)
	d.Advice = append(d.Advice, modeAdvice(d.Mode))
	return d
}

// detectPlatform implements §3.2. The kernel string is authoritative for WSL,
// not WSL_DISTRO_NAME/WSL_INTEROP: those two are read as corroborating signals
// but they are absent inside a tmux started from a login shell that dropped
// them, and a detection that flips on a lost variable is not robust.
func detectPlatform(goos string, env []string, probe Probe) Platform {
	if isTermux(env, probe) {
		return PlatformTermux
	}
	if strings.EqualFold(goos, "android") {
		return PlatformAndroid
	}
	if isWSL(env, probe) {
		return PlatformWSL
	}
	switch strings.ToLower(goos) {
	case "darwin":
		return PlatformMacOS
	case "windows":
		return PlatformWindows
	case "linux":
		return PlatformLinux
	}
	return PlatformUnknown
}

// isTermux reuses internal/xdg's definition so there is exactly one answer to
// "are we on Termux" in the tree. The env/probe path exists for the tests: the
// real one has to consult the actual process, which is what xdg.IsTermux does.
func isTermux(env []string, probe Probe) bool {
	if v, _ := lookupEnv(env, "PREFIX"); strings.Contains(v, "com.termux") {
		return true
	}
	if probe.Exists != nil {
		return probe.Exists("/data/data/com.termux/files/usr")
	}
	return xdg.IsTermux()
}

func isWSL(env []string, probe Probe) bool {
	if probe.KernelRelease != nil {
		rel := strings.ToLower(probe.KernelRelease())
		if strings.Contains(rel, "microsoft") || strings.Contains(rel, "wsl") {
			return true
		}
	}
	// Only consulted when the kernel said nothing, per the note above.
	if v, _ := lookupEnv(env, "WSL_DISTRO_NAME"); v != "" {
		return true
	}
	v, _ := lookupEnv(env, "WSL_INTEROP")
	return v != ""
}

// detectHost implements §3.3's table, top to bottom, first match wins.
func detectHost(goos string, env []string, tty bool) Host {
	if !tty {
		return HostNotATTY
	}
	term, _ := lookupEnv(env, "TERM")
	termLower := strings.ToLower(term)
	prog, _ := lookupEnv(env, "TERM_PROGRAM")

	if strings.EqualFold(term, "dumb") {
		return HostDumb
	}
	if v, _ := lookupEnv(env, "TMUX"); v != "" {
		return HostMultiplexer
	}
	if v, _ := lookupEnv(env, "STY"); v != "" {
		return HostMultiplexer
	}
	if strings.HasPrefix(termLower, "screen") || strings.HasPrefix(termLower, "tmux") {
		return HostMultiplexer
	}
	if v, _ := lookupEnv(env, "WT_SESSION"); v != "" {
		return HostWindowsTerminal
	}
	switch {
	case strings.EqualFold(prog, "vscode"):
		return HostVSCode
	case strings.EqualFold(prog, "iTerm.app"):
		return HostITerm2
	case strings.EqualFold(prog, "Apple_Terminal"):
		return HostAppleTerminal
	case strings.EqualFold(prog, "ghostty"),
		strings.EqualFold(prog, "WezTerm"),
		strings.EqualFold(prog, "alacritty"),
		strings.EqualFold(prog, "kitty"):
		return HostModernTerminal
	}
	switch termLower {
	case "xterm-ghostty", "xterm-kitty", "alacritty", "wezterm":
		return HostModernTerminal
	}
	if v, _ := lookupEnv(env, "ConEmuANSI"); strings.EqualFold(v, "on") {
		return HostConEmu
	}
	if v, _ := lookupEnv(env, "ANSICON"); v != "" {
		return HostConEmu
	}
	if isXtermLike(termLower) {
		return HostXtermLike
	}
	if strings.EqualFold(goos, "windows") && term == "" {
		return HostLegacyConhost
	}
	return HostUnknown
}

// xtermLikePrefixes are the terminfo names that reliably implement the
// alternate screen. `linux` (the kernel VT) is in here and does support it.
var xtermLikePrefixes = []string{
	"xterm", "screen", "rxvt", "alacritty", "foot", "st-", "vte",
	"konsole", "gnome", "tmux", "contour", "wezterm", "kitty", "ghostty",
}

func isXtermLike(termLower string) bool {
	if termLower == "" {
		return false
	}
	if termLower == "linux" {
		return true
	}
	for _, p := range xtermLikePrefixes {
		if strings.HasPrefix(termLower, p) {
			return true
		}
	}
	return false
}

// modeForHost is §3.3's mode column plus the single Termux carve-out. Keeping
// the mapping in one function is what makes "the host decides" checkable rather
// than aspirational: there is exactly one place where a Host becomes a Mode.
func modeForHost(h Host, p Platform, env []string) (Mode, string) {
	// The carve-out. Termux reports TERM=xterm-256color, so by host alone it
	// would be fullscreen. DECISION-1 keeps §3's original promise on the
	// platform §3 was written for: scrolling and selecting with a finger beats
	// any reimplementation of them. A Termux user who disagrees sets
	// [ui] tui_mode = "fullscreen" and gets it — this is a default, not a verdict.
	if p == PlatformTermux {
		return ModeRegular, "Termux: native scrolling and selection are worth more than reflow"
	}

	switch h {
	case HostMultiplexer:
		if v, _ := lookupEnv(env, "TMUX"); v != "" {
			return ModeFullscreen, "TMUX is set"
		}
		if v, _ := lookupEnv(env, "STY"); v != "" {
			return ModeFullscreen, "STY is set (GNU screen)"
		}
		term, _ := lookupEnv(env, "TERM")
		return ModeFullscreen, "TERM=" + term
	case HostWindowsTerminal:
		return ModeFullscreen, "WT_SESSION is set (Windows Terminal)"
	case HostVSCode:
		return ModeFullscreen, "TERM_PROGRAM=vscode"
	case HostITerm2:
		return ModeFullscreen, "TERM_PROGRAM=iTerm.app"
	case HostAppleTerminal:
		return ModeFullscreen, "TERM_PROGRAM=Apple_Terminal"
	case HostModernTerminal:
		if prog, _ := lookupEnv(env, "TERM_PROGRAM"); prog != "" {
			return ModeFullscreen, "TERM_PROGRAM=" + prog
		}
		term, _ := lookupEnv(env, "TERM")
		return ModeFullscreen, "TERM=" + term
	case HostConEmu:
		if v, _ := lookupEnv(env, "ConEmuANSI"); strings.EqualFold(v, "on") {
			return ModeFullscreen, "ConEmuANSI=on"
		}
		return ModeFullscreen, "ANSICON is set"
	case HostXtermLike:
		term, _ := lookupEnv(env, "TERM")
		return ModeFullscreen, "TERM=" + term
	case HostLegacyConhost:
		return ModeRegular, "a bare Windows console"
	case HostDumb:
		return ModeRegular, "TERM=dumb"
	case HostNotATTY:
		return ModeRegular, "stdout is not a terminal"
	default:
		return ModeRegular, "no terminal hint: assuming a legacy console"
	}
}

// parseMode accepts the two valid values, case- and space-insensitively. "auto"
// is deliberately *not* a mode: it is the absence of an override, and returning
// ok=false for it is what lets detection continue.
func parseMode(s string) (Mode, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "regular":
		return ModeRegular, true
	case "fullscreen":
		return ModeFullscreen, true
	default:
		return ModeRegular, false
	}
}

func isDumb(env []string) bool {
	v, _ := lookupEnv(env, "TERM")
	return strings.EqualFold(strings.TrimSpace(v), "dumb")
}

// isCI treats "" and "0" as unset, matching how theme's NO_COLOR rule reads a
// boolean-ish variable. CI=0 in a shell profile should not disable the interface.
func isCI(env []string) bool {
	v, ok := lookupEnv(env, "CI")
	if !ok {
		return false
	}
	v = strings.TrimSpace(v)
	return v != "" && v != "0" && !strings.EqualFold(v, "false")
}

// modeAdvice phrases the escape hatch in the direction that can actually be
// wrong, the same way theme.advice does: somebody in regular who wanted the
// full screen needs a different sentence from somebody whose scrollback just
// got taken away.
func modeAdvice(m Mode) string {
	if m == ModeFullscreen {
		return `if you would rather keep your terminal's own scrollback and text selection, set [ui] tui_mode = "regular"`
	}
	return `if this terminal handles the alternate screen well and you want reflow on resize, set [ui] tui_mode = "fullscreen"`
}

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
func (d Detection) Set() []Signal {
	out := make([]Signal, 0, len(d.Signals))
	for _, s := range d.Signals {
		if s.Set && s.Value != "" {
			out = append(out, s)
		}
	}
	return out
}

// lookupEnv reads a KEY=VALUE slice the way os.LookupEnv reads the real
// environment, distinguishing unset from empty.
func lookupEnv(env []string, key string) (string, bool) {
	for _, kv := range env {
		if name, value, ok := strings.Cut(kv, "="); ok && name == key {
			return value, true
		}
	}
	return "", false
}

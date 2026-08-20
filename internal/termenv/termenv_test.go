package termenv

import (
	"strings"
	"testing"
)

// wslKernel is what /proc/sys/kernel/osrelease actually contains under WSL2.
// Using the real string rather than the substring the code greps for is the
// point: a test that probes with "microsoft" proves only that strings.Contains
// works.
const wslKernel = "5.15.153.1-microsoft-standard-WSL2\n"

func linuxKernel() string { return "6.8.0-45-generic\n" }

func noProbe() Probe {
	return Probe{
		KernelRelease: linuxKernel,
		Exists:        func(string) bool { return false },
	}
}

func wslProbe() Probe {
	return Probe{
		KernelRelease: func() string { return wslKernel },
		Exists:        func(string) bool { return false },
	}
}

func termuxProbe() Probe {
	return Probe{
		KernelRelease: func() string { return "4.14.190-perf+\n" },
		Exists: func(p string) bool {
			return p == "/data/data/com.termux/files/usr"
		},
	}
}

// TestDetectEnvWorkedScenarios is docs/DESIGN-tui-mode.md §3.4, verbatim. Every
// row of that table is a case here, including the ones for platforms this suite
// will never run on — which is the entire reason DetectEnv takes goos, env, tty
// and Probe as parameters instead of reading them.
//
// The two WSL rows are the point of the exercise: the same WSL kernel produces
// two different modes, decided by the terminal that is drawing it. That is the
// "no asumas que WSL = regular" requirement under test rather than asserted in
// a comment.
func TestDetectEnvWorkedScenarios(t *testing.T) {
	cases := []struct {
		name     string
		goos     string
		env      []string
		tty      bool
		probe    Probe
		wantMode Mode
		wantPlat Platform
		wantHost Host
		wantWhy  string
	}{
		{
			name:     "Termux on a phone",
			goos:     "android",
			env:      []string{"TERM=xterm-256color", "PREFIX=/data/data/com.termux/files/usr"},
			tty:      true,
			probe:    termuxProbe(),
			wantMode: ModeRegular,
			wantPlat: PlatformTermux,
			wantHost: HostXtermLike,
			wantWhy:  "Termux: native scrolling and selection are worth more than reflow",
		},
		{
			name:     "WSL Ubuntu in Windows Terminal",
			goos:     "linux",
			env:      []string{"TERM=xterm-256color", "WT_SESSION=abc-123", "WSL_DISTRO_NAME=Ubuntu"},
			tty:      true,
			probe:    wslProbe(),
			wantMode: ModeFullscreen,
			wantPlat: PlatformWSL,
			wantHost: HostWindowsTerminal,
			wantWhy:  "WT_SESSION is set (Windows Terminal)",
		},
		{
			name:     "WSL Ubuntu in bare conhost",
			goos:     "linux",
			env:      []string{"WSL_DISTRO_NAME=Ubuntu", "TERM=cygwin"},
			tty:      true,
			probe:    wslProbe(),
			wantMode: ModeRegular,
			wantPlat: PlatformWSL,
			wantHost: HostUnknown,
			wantWhy:  "no terminal hint: assuming a legacy console",
		},
		{
			name:     "WSL inside tmux",
			goos:     "linux",
			env:      []string{"TERM=screen-256color", "TMUX=/tmp/tmux-1000/default,123,0"},
			tty:      true,
			probe:    wslProbe(),
			wantMode: ModeFullscreen,
			wantPlat: PlatformWSL,
			wantHost: HostMultiplexer,
			wantWhy:  "TMUX is set",
		},
		{
			name:     "Windows native, Windows Terminal",
			goos:     "windows",
			env:      []string{"WT_SESSION=xyz"},
			tty:      true,
			probe:    noProbe(),
			wantMode: ModeFullscreen,
			wantPlat: PlatformWindows,
			wantHost: HostWindowsTerminal,
			wantWhy:  "WT_SESSION is set (Windows Terminal)",
		},
		{
			name:     "Windows native, cmd.exe",
			goos:     "windows",
			env:      []string{},
			tty:      true,
			probe:    noProbe(),
			wantMode: ModeRegular,
			wantPlat: PlatformWindows,
			wantHost: HostLegacyConhost,
			wantWhy:  "a bare Windows console",
		},
		{
			name:     "macOS iTerm2",
			goos:     "darwin",
			env:      []string{"TERM=xterm-256color", "TERM_PROGRAM=iTerm.app"},
			tty:      true,
			probe:    noProbe(),
			wantMode: ModeFullscreen,
			wantPlat: PlatformMacOS,
			wantHost: HostITerm2,
			wantWhy:  "TERM_PROGRAM=iTerm.app",
		},
		{
			name:     "Ubuntu desktop, GNOME Terminal",
			goos:     "linux",
			env:      []string{"TERM=xterm-256color", "COLORTERM=truecolor"},
			tty:      true,
			probe:    noProbe(),
			wantMode: ModeFullscreen,
			wantPlat: PlatformLinux,
			wantHost: HostXtermLike,
			wantWhy:  "TERM=xterm-256color",
		},
		{
			name:     "Linux TTY, no X",
			goos:     "linux",
			env:      []string{"TERM=linux"},
			tty:      true,
			probe:    noProbe(),
			wantMode: ModeFullscreen,
			wantPlat: PlatformLinux,
			wantHost: HostXtermLike,
			wantWhy:  "TERM=linux",
		},
		{
			name:     "VS Code integrated terminal",
			goos:     "linux",
			env:      []string{"TERM=xterm-256color", "TERM_PROGRAM=vscode"},
			tty:      true,
			probe:    noProbe(),
			wantMode: ModeFullscreen,
			wantPlat: PlatformLinux,
			wantHost: HostVSCode,
			wantWhy:  "TERM_PROGRAM=vscode",
		},
		{
			name:     "redirected to a file",
			goos:     "linux",
			env:      []string{"TERM=xterm-256color"},
			tty:      false,
			probe:    noProbe(),
			wantMode: ModeRegular,
			wantPlat: PlatformLinux,
			wantHost: HostNotATTY,
			wantWhy:  "stdout is not a terminal",
		},
		{
			name:     "GitHub Actions",
			goos:     "linux",
			env:      []string{"TERM=xterm-256color", "CI=true"},
			tty:      true,
			probe:    noProbe(),
			wantMode: ModeRegular,
			wantPlat: PlatformLinux,
			wantHost: HostUnknown,
			wantWhy:  "CI is set",
		},
		{
			name:     "TERM=dumb",
			goos:     "linux",
			env:      []string{"TERM=dumb"},
			tty:      true,
			probe:    noProbe(),
			wantMode: ModeRegular,
			wantPlat: PlatformLinux,
			wantHost: HostDumb,
			wantWhy:  "TERM=dumb",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DetectEnv("", tc.goos, tc.env, tc.tty, tc.probe)
			if got.Mode != tc.wantMode {
				t.Errorf("Mode = %v, want %v (reason given: %q)", got.Mode, tc.wantMode, got.Reason)
			}
			if got.Platform != tc.wantPlat {
				t.Errorf("Platform = %v, want %v", got.Platform, tc.wantPlat)
			}
			if got.Host != tc.wantHost {
				t.Errorf("Host = %v, want %v", got.Host, tc.wantHost)
			}
			if got.Reason != tc.wantWhy {
				t.Errorf("Reason = %q, want %q", got.Reason, tc.wantWhy)
			}
		})
	}
}

// TestWSLIsNotAVerdict is the requirement stated as its own test rather than
// left implicit in the table above, because it is the one thing that was asked
// for by name: the same kernel, the same distro, two hosts, two modes.
//
// If somebody ever "simplifies" detection by keying off Platform, this fails
// with a message that says why the shortcut is wrong.
func TestWSLIsNotAVerdict(t *testing.T) {
	base := []string{"TERM=xterm-256color", "WSL_DISTRO_NAME=Ubuntu"}

	inWT := DetectEnv("", "linux", append(append([]string{}, base...), "WT_SESSION=1"), true, wslProbe())
	bare := DetectEnv("", "linux", []string{"WSL_DISTRO_NAME=Ubuntu", "TERM=cygwin"}, true, wslProbe())

	if inWT.Platform != PlatformWSL || bare.Platform != PlatformWSL {
		t.Fatalf("both cases must be detected as WSL: got %v and %v", inWT.Platform, bare.Platform)
	}
	if inWT.Mode != ModeFullscreen {
		t.Errorf("WSL under Windows Terminal must reach fullscreen, got %v (%s)", inWT.Mode, inWT.Reason)
	}
	if bare.Mode != ModeRegular {
		t.Errorf("WSL under a bare console must stay regular, got %v (%s)", bare.Mode, bare.Reason)
	}
	if inWT.Mode == bare.Mode {
		t.Error("the same WSL kernel produced the same mode for two different hosts: the mode is being decided by Platform, not Host")
	}
}

// TestKernelStringIsAuthoritativeForWSL pins the note in §3.2: WSL_DISTRO_NAME
// and WSL_INTEROP are corroborating signals, not the definition. Both are absent
// inside a tmux started from a login shell that dropped them, and detection that
// flips on a lost variable is not robust.
func TestKernelStringIsAuthoritativeForWSL(t *testing.T) {
	// No WSL_* variables at all — only the kernel knows.
	d := DetectEnv("", "linux", []string{"TERM=xterm-256color"}, true, wslProbe())
	if d.Platform != PlatformWSL {
		t.Errorf("Platform = %v, want WSL from the kernel release alone", d.Platform)
	}

	// And the converse: the variable present but a stock Linux kernel still
	// resolves to WSL, because the variable is a positive signal too.
	d2 := DetectEnv("", "linux", []string{"TERM=xterm-256color", "WSL_INTEROP=/run/WSL/8_interop"}, true, noProbe())
	if d2.Platform != PlatformWSL {
		t.Errorf("Platform = %v, want WSL from WSL_INTEROP", d2.Platform)
	}
}

// TestTermuxCarveOutIsADefaultNotAVerdict checks both halves of the one place
// where Platform overrides Host. The carve-out must apply, and it must be
// overridable — the design says "the mode is a setting, not a verdict", and a
// Termux user who wants fullscreen has to be able to get it.
func TestTermuxCarveOutIsADefaultNotAVerdict(t *testing.T) {
	env := []string{"TERM=xterm-256color", "PREFIX=/data/data/com.termux/files/usr"}

	auto := DetectEnv("", "android", env, true, termuxProbe())
	if auto.Mode != ModeRegular {
		t.Errorf("Termux default = %v, want regular", auto.Mode)
	}
	// The host was still recognised as capable; only the mode was overridden.
	// This matters because a future reader must be able to see that the
	// carve-out is a policy choice, not a capability claim.
	if auto.Host != HostXtermLike {
		t.Errorf("Host = %v, want XtermLike: Termux does report an xterm-like TERM", auto.Host)
	}

	forced := DetectEnv("fullscreen", "android", env, true, termuxProbe())
	if forced.Mode != ModeFullscreen {
		t.Errorf("Termux with [ui] tui_mode = fullscreen = %v, want fullscreen: the carve-out is a default, not a verdict", forced.Mode)
	}
}

// TestPrefixVariableAloneIdentifiesTermux covers the case xdg.IsTermux's first
// branch exists for: $PREFIX set, but the filesystem probe answering no (a
// Termux-like environment where /data/data is not readable).
func TestPrefixVariableAloneIdentifiesTermux(t *testing.T) {
	d := DetectEnv("", "android", []string{"TERM=xterm-256color", "PREFIX=/data/data/com.termux/files/usr"}, true, noProbe())
	if d.Platform != PlatformTermux {
		t.Errorf("Platform = %v, want Termux from $PREFIX alone", d.Platform)
	}
}

// TestAndroidWithoutTermuxMarkers is the §3.2 row that distinguishes "some other
// Android shell" from Termux, so the carve-out does not silently widen to every
// Android device.
func TestAndroidWithoutTermuxMarkers(t *testing.T) {
	d := DetectEnv("", "android", []string{"TERM=xterm-256color"}, true, noProbe())
	if d.Platform != PlatformAndroid {
		t.Errorf("Platform = %v, want Android", d.Platform)
	}
	if d.Mode != ModeFullscreen {
		t.Errorf("Mode = %v, want fullscreen: the Termux carve-out must not widen to all of Android", d.Mode)
	}
}

// TestOverrideBeatsDetection covers §3.1 rules 1 and 2, in both directions and
// with the precedence between them.
func TestOverrideBeatsDetection(t *testing.T) {
	// A terminal that would otherwise be fullscreen, forced to regular.
	env := []string{"TERM=xterm-256color", "WT_SESSION=1"}
	if d := DetectEnv("regular", "linux", env, true, noProbe()); d.Mode != ModeRegular {
		t.Errorf("[ui] tui_mode = regular did not win: got %v (%s)", d.Mode, d.Reason)
	} else if !strings.Contains(d.Reason, "tui_mode") {
		t.Errorf("Reason = %q, want it to name the setting that decided", d.Reason)
	}

	// A terminal that would otherwise be regular, forced to fullscreen.
	if d := DetectEnv("fullscreen", "windows", []string{}, true, noProbe()); d.Mode != ModeFullscreen {
		t.Errorf("[ui] tui_mode = fullscreen did not win over LegacyConhost: got %v", d.Mode)
	}

	// The env var works on its own...
	if d := DetectEnv("", "windows", []string{"ISHAKAT_TUI_MODE=fullscreen"}, true, noProbe()); d.Mode != ModeFullscreen {
		t.Errorf("ISHAKAT_TUI_MODE did not apply: got %v (%s)", d.Mode, d.Reason)
	}

	// ...and the config file outranks it, because the file is the durable
	// statement of intent and the variable is the ad-hoc one.
	d := DetectEnv("regular", "linux", []string{"TERM=xterm-256color", "ISHAKAT_TUI_MODE=fullscreen"}, true, noProbe())
	if d.Mode != ModeRegular {
		t.Errorf("[ui] tui_mode must outrank ISHAKAT_TUI_MODE: got %v (%s)", d.Mode, d.Reason)
	}
}

// TestAutoIsNotAMode pins that "auto" falls through to detection rather than
// being parsed as a value. It is the default in the config file, so if it were
// ever accepted by parseMode every user would be pinned to regular.
func TestAutoIsNotAMode(t *testing.T) {
	env := []string{"TERM=xterm-256color", "WT_SESSION=1"}
	for _, override := range []string{"auto", "", "  ", "AUTO"} {
		d := DetectEnv(override, "linux", env, true, noProbe())
		if d.Mode != ModeFullscreen {
			t.Errorf("override %q = %v, want detection to run and give fullscreen", override, d.Mode)
		}
		if strings.Contains(d.Reason, "tui_mode") {
			t.Errorf("override %q reported %q: auto is the absence of an override, not a setting", override, d.Reason)
		}
	}
}

// TestFullscreenIsRefusedWithoutATTY is the one case where an explicit setting
// is overruled, and it is the reason the honesty rules exist. Into a pipe or a
// log file, cursor addressing and an alt-screen switch are not a degraded
// interface — they are corruption of the file the user asked for.
func TestFullscreenIsRefusedWithoutATTY(t *testing.T) {
	for _, src := range []struct{ override, env string }{
		{"fullscreen", ""},
		{"", "ISHAKAT_TUI_MODE=fullscreen"},
	} {
		env := []string{"TERM=xterm-256color"}
		if src.env != "" {
			env = append(env, src.env)
		}
		d := DetectEnv(src.override, "linux", env, false, noProbe())
		if d.Mode != ModeRegular {
			t.Errorf("forced fullscreen into a non-TTY = %v, want regular", d.Mode)
		}
		if d.Host != HostNotATTY {
			t.Errorf("Host = %v, want NotATTY", d.Host)
		}
		if !strings.Contains(d.Reason, "not a terminal") {
			t.Errorf("Reason = %q, want it to say stdout is not a terminal", d.Reason)
		}
		if len(d.Advice) == 0 {
			t.Error("overruling an explicit setting must come with advice explaining why")
		}
	}

	// Forcing *regular* without a TTY is not a conflict, so it is honoured
	// as a setting rather than reported as a refusal.
	d := DetectEnv("regular", "linux", []string{"TERM=xterm-256color"}, false, noProbe())
	if !strings.Contains(d.Reason, "tui_mode") {
		t.Errorf("Reason = %q: forcing regular without a TTY agrees with detection and should read as a setting", d.Reason)
	}
}

// TestUnparseableOverrideIsReported: a typo'd ISHAKAT_TUI_MODE=fullscren that
// silently does nothing is how somebody loses an afternoon.
func TestUnparseableOverrideIsReported(t *testing.T) {
	d := DetectEnv("", "linux", []string{"TERM=xterm-256color", "ISHAKAT_TUI_MODE=fullscren"}, true, noProbe())
	if d.Mode != ModeFullscreen {
		t.Errorf("Mode = %v: an invalid override must fall through to detection", d.Mode)
	}
	var found bool
	for _, a := range d.Advice {
		if strings.Contains(a, "fullscren") {
			found = true
		}
	}
	if !found {
		t.Errorf("Advice = %q, want the invalid value quoted back", d.Advice)
	}
}

// TestCIZeroIsNotCI: CI=0 in a shell profile must not disable the interface.
// theme's NO_COLOR rule already reads boolean-ish variables this way.
func TestCIZeroIsNotCI(t *testing.T) {
	for _, v := range []string{"0", "", "false"} {
		env := []string{"TERM=xterm-256color", "CI=" + v}
		if d := DetectEnv("", "linux", env, true, noProbe()); d.Mode != ModeFullscreen {
			t.Errorf("CI=%q forced %v; only a truthy CI means a build machine", v, d.Mode)
		}
	}
	if d := DetectEnv("", "linux", []string{"TERM=xterm-256color", "CI=1"}, true, noProbe()); d.Mode != ModeRegular {
		t.Errorf("CI=1 = %v, want regular", d.Mode)
	}
}

// TestNoTERMOffWindowsIsRegularButOnWindowsIsConhost pins the asymmetry in
// §3.1 rule 5. The same absence means two different things, and collapsing them
// would either break cmd.exe or mask a stripped environment.
func TestNoTERMOffWindowsIsRegularButOnWindowsIsConhost(t *testing.T) {
	off := DetectEnv("", "linux", []string{}, true, noProbe())
	if off.Mode != ModeRegular || !strings.Contains(off.Reason, "no TERM") {
		t.Errorf("linux without TERM = %v (%q), want regular naming the missing TERM", off.Mode, off.Reason)
	}

	win := DetectEnv("", "windows", []string{}, true, noProbe())
	if win.Host != HostLegacyConhost {
		t.Errorf("windows without TERM = host %v, want LegacyConhost: the absence is normal there", win.Host)
	}
}

// TestUnknownIsNeverFullscreen states the default direction as a test. Guessing
// wrong towards regular costs reflow; guessing wrong towards fullscreen can
// leave a terminal unusable after exit.
func TestUnknownIsNeverFullscreen(t *testing.T) {
	for _, term := range []string{"cygwin", "ansi", "vt100", "sun-color", "hurd"} {
		d := DetectEnv("", "linux", []string{"TERM=" + term}, true, noProbe())
		if d.Mode != ModeRegular {
			t.Errorf("TERM=%s = %v, want regular: an unrecognised terminal is not a reason to take the screen", term, d.Mode)
		}
	}
}

// TestMultiplexerIsDetectedWithoutTMUXVariable covers screen and the TERM-only
// path, since TMUX is not exported by every configuration.
func TestMultiplexerIsDetectedWithoutTMUXVariable(t *testing.T) {
	for _, env := range [][]string{
		{"TERM=screen.xterm-256color"},
		{"TERM=tmux-256color"},
		{"TERM=xterm-256color", "STY=1234.pts-0.host"},
	} {
		d := DetectEnv("", "linux", env, true, noProbe())
		if d.Host != HostMultiplexer {
			t.Errorf("env %v = host %v, want Multiplexer", env, d.Host)
		}
		if d.Mode != ModeFullscreen {
			t.Errorf("env %v = %v, want fullscreen: tmux and screen implement the alternate screen themselves", env, d.Mode)
		}
	}
}

// TestModernTerminalsReachFullscreen walks the §3.3 rows that exist because
// these terminals are common and none of them sets TERM_PROGRAM consistently.
func TestModernTerminalsReachFullscreen(t *testing.T) {
	for _, env := range [][]string{
		{"TERM=xterm-ghostty"},
		{"TERM=xterm-kitty"},
		{"TERM=alacritty"},
		{"TERM=wezterm"},
		{"TERM=xterm-256color", "TERM_PROGRAM=WezTerm"},
		{"TERM=xterm-256color", "TERM_PROGRAM=ghostty"},
		{"TERM=xterm-256color", "ConEmuANSI=ON"},
		{"TERM=xterm-256color", "ANSICON=1"},
		{"TERM=foot"},
		{"TERM=st-256color"},
		{"TERM=konsole-256color"},
		{"TERM=vte-256color"},
		{"TERM=rxvt-unicode-256color"},
	} {
		if d := DetectEnv("", "linux", env, true, noProbe()); d.Mode != ModeFullscreen {
			t.Errorf("env %v = %v (%s), want fullscreen", env, d.Mode, d.Reason)
		}
	}
}

// TestEveryConsultedVariableIsASignal is the guard the doc asks for: "if a new
// rule reads a new variable, it goes in this list too, so doctor never reports a
// decision it cannot show the input for". Without this, a rule added later reads
// a variable that `doctor` then cannot display, and the diagnosis becomes a
// decision with no visible cause — exactly what theme's Diagnosis exists to
// prevent.
func TestEveryConsultedVariableIsASignal(t *testing.T) {
	consulted := []string{
		"TERM", "TERM_PROGRAM", "TMUX", "STY", "WT_SESSION",
		"WSL_DISTRO_NAME", "WSL_INTEROP", "ConEmuANSI", "ANSICON",
		"CI", "PREFIX", "ISHAKAT_TUI_MODE",
	}
	have := make(map[string]bool, len(signalNames))
	for _, n := range signalNames {
		have[n] = true
	}
	for _, n := range consulted {
		if !have[n] {
			t.Errorf("%s is read by a detection rule but is not in signalNames: doctor would report a decision without showing its input", n)
		}
	}
}

// TestSetHidesUnsetSignals mirrors theme.Diagnosis.Set's contract: a dozen
// "unset" lines bury the two that decided anything.
func TestSetHidesUnsetSignals(t *testing.T) {
	d := DetectEnv("", "linux", []string{"TERM=xterm-256color", "TMUX="}, true, noProbe())
	for _, s := range d.Set() {
		if s.Value == "" {
			t.Errorf("Set() returned %s with an empty value", s.Name)
		}
	}
	if len(d.Set()) == 0 {
		t.Error("Set() returned nothing even though TERM was set")
	}
}

// TestAdviceNamesTheOppositeDirection: advice for somebody in fullscreen who
// wanted their scrollback back is not the same sentence as advice for somebody
// in regular who wanted reflow. theme.advice makes the same distinction.
func TestAdviceNamesTheOppositeDirection(t *testing.T) {
	full := DetectEnv("", "linux", []string{"TERM=xterm-256color"}, true, noProbe())
	if len(full.Advice) == 0 || !strings.Contains(strings.Join(full.Advice, " "), `"regular"`) {
		t.Errorf("advice in fullscreen = %q, want it to point at regular", full.Advice)
	}

	reg := DetectEnv("", "windows", []string{}, true, noProbe())
	if len(reg.Advice) == 0 || !strings.Contains(strings.Join(reg.Advice, " "), `"fullscreen"`) {
		t.Errorf("advice in regular = %q, want it to point at fullscreen", reg.Advice)
	}

	// Somebody who already pinned the mode does not need to be told to pin it.
	pinned := DetectEnv("regular", "linux", []string{"TERM=xterm-256color"}, true, noProbe())
	if len(pinned.Advice) != 0 {
		t.Errorf("advice with an explicit override = %q, want none: telling somebody to set what they just set is noise", pinned.Advice)
	}
}

// TestModeStringIsStableBecauseItIsWrittenToConfig: these two strings are the
// accepted values of [ui] tui_mode and are printed by doctor. Renaming one is a
// config break, so it is pinned rather than left to a refactor.
func TestModeStringIsStable(t *testing.T) {
	if ModeRegular.String() != "regular" || ModeFullscreen.String() != "fullscreen" {
		t.Errorf("mode names changed: %q/%q are the accepted values of [ui] tui_mode", ModeRegular, ModeFullscreen)
	}
}

// TestDetectEnvSurvivesANilProbe: Detect passes RealProbe, but a caller that
// builds a Detection in a test or a tool may not. Panicking on a zero Probe
// would make the package awkward to use from exactly the places that should be
// calling it.
func TestDetectEnvSurvivesANilProbe(t *testing.T) {
	d := DetectEnv("", "linux", []string{"TERM=xterm-256color"}, true, Probe{})
	if d.Mode != ModeFullscreen {
		t.Errorf("Mode = %v with a zero Probe, want detection to still work", d.Mode)
	}
}

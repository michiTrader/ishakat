package theme

import (
	"strings"
	"testing"
)

// env is a terse way to write an environment for a table row.
func env(kv ...string) []string { return kv }

func TestDiagnoseResolvesBothAxesWithAReason(t *testing.T) {
	cases := []struct {
		name        string
		colorOver   string
		glyphOver   string
		goos        string
		env         []string
		tty         bool
		wantColor   Capability
		wantGlyphs  GlyphSet
		colorReason string // substring
		glyphReason string // substring
	}{
		{
			name:        "termux is the target platform and gets everything",
			goos:        "android",
			env:         env("TERM=xterm-256color", "COLORTERM=truecolor", "LANG=en_US.UTF-8"),
			tty:         true,
			wantColor:   CapTruecolor,
			wantGlyphs:  GlyphsUnicode,
			colorReason: "COLORTERM=truecolor",
			glyphReason: "UTF-8 locale",
		},
		{
			// The reported case: a stock PowerShell/cmd console. Colour works
			// (that is what the white banner was about — it should have been a
			// gradient), the repertoire does not.
			name:        "a bare windows console gets colour but not the blocks",
			goos:        "windows",
			env:         env("USERNAME=michi"),
			tty:         true,
			wantColor:   CapNone,
			wantGlyphs:  GlyphsASCII,
			colorReason: "no TERM and no console hint",
			glyphReason: "OEM",
		},
		{
			name:        "windows terminal is a first-class host",
			goos:        "windows",
			env:         env("WT_SESSION=abc-123"),
			tty:         true,
			wantColor:   CapTruecolor,
			wantGlyphs:  GlyphsUnicode,
			colorReason: "WT_SESSION",
			glyphReason: "WT_SESSION",
		},
		{
			name:        "git bash sets TERM, which on windows means a UTF-8 pty",
			goos:        "windows",
			env:         env("TERM=xterm-256color"),
			tty:         true,
			wantColor:   Cap256,
			wantGlyphs:  GlyphsUnicode,
			colorReason: "TERM=xterm-256color",
			glyphReason: "pty",
		},
		{
			name:        "LANG=C promises single-byte output",
			goos:        "linux",
			env:         env("TERM=xterm-256color", "LANG=C"),
			tty:         true,
			wantColor:   Cap256,
			wantGlyphs:  GlyphsASCII,
			colorReason: "TERM=xterm-256color",
			glyphReason: "not a UTF-8 locale",
		},
		{
			name:        "NO_COLOR is a contract and it is named as the cause",
			goos:        "linux",
			env:         env("TERM=xterm-256color", "COLORTERM=truecolor", "NO_COLOR=1", "LANG=en_US.UTF-8"),
			tty:         true,
			wantColor:   CapNone,
			wantGlyphs:  GlyphsUnicode,
			colorReason: "NO_COLOR",
			glyphReason: "UTF-8 locale",
		},
		{
			name:        "a redirected stdout gets no colour whatever TERM says",
			goos:        "linux",
			env:         env("TERM=xterm-256color", "COLORTERM=truecolor", "LANG=en_US.UTF-8"),
			tty:         false,
			wantColor:   CapNone,
			wantGlyphs:  GlyphsUnicode,
			colorReason: "not a terminal",
			glyphReason: "UTF-8 locale",
		},
		{
			name:        "an override wins and says so, even over a redirect",
			colorOver:   "truecolor",
			glyphOver:   "ascii",
			goos:        "linux",
			env:         env("TERM=xterm-256color", "LANG=en_US.UTF-8"),
			tty:         false,
			wantColor:   CapTruecolor,
			wantGlyphs:  GlyphsASCII,
			colorReason: `[ui] color = "truecolor"`,
			glyphReason: `[ui] glyphs = "ascii"`,
		},
		{
			name:        "TERM=dumb is dumb on every axis",
			goos:        "linux",
			env:         env("TERM=dumb", "LANG=en_US.UTF-8"),
			tty:         true,
			wantColor:   CapNone,
			wantGlyphs:  GlyphsASCII,
			colorReason: "TERM=dumb",
			glyphReason: "TERM=dumb",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := DiagnoseEnv(tc.colorOver, tc.glyphOver, tc.goos, tc.env, tc.tty)

			if d.Color != tc.wantColor {
				t.Errorf("colour = %v (%s), want %v", d.Color, d.ColorReason, tc.wantColor)
			}
			if d.Glyphs != tc.wantGlyphs {
				t.Errorf("glyphs = %v (%s), want %v", d.Glyphs, d.GlyphsReason, tc.wantGlyphs)
			}
			if !strings.Contains(d.ColorReason, tc.colorReason) {
				t.Errorf("colour reason = %q, want it to mention %q", d.ColorReason, tc.colorReason)
			}
			if !strings.Contains(d.GlyphsReason, tc.glyphReason) {
				t.Errorf("glyph reason = %q, want it to mention %q", d.GlyphsReason, tc.glyphReason)
			}
		})
	}
}

// The decisions and the report must not be two implementations of the same
// rules: a report that drifts from the interface is worse than no report,
// because it sends the user to change the wrong setting.
func TestDiagnoseAgreesWithTheDetectionItReports(t *testing.T) {
	envs := [][]string{
		env(),
		env("TERM=dumb"),
		env("TERM=xterm-256color", "LANG=en_US.UTF-8"),
		env("TERM=xterm-256color", "COLORTERM=truecolor", "LC_ALL=C"),
		env("WT_SESSION=1"),
		env("ConEmuANSI=ON"),
		env("TERM_PROGRAM=vscode"),
		env("ANSICON=1"),
		env("NO_COLOR=1", "TERM=xterm-256color"),
	}
	for _, goos := range []string{"linux", "windows", "darwin", "android"} {
		for _, e := range envs {
			for _, over := range []string{"", "auto", "ascii", "unicode"} {
				d := DiagnoseEnv("", over, goos, e, true)
				if want := DetectGlyphsEnv(over, goos, e); d.Glyphs != want {
					t.Errorf("goos=%s env=%v glyphs=%q: report says %v, DetectGlyphsEnv says %v",
						goos, e, over, d.Glyphs, want)
				}
			}
			for _, over := range []string{"", "auto", "never", "256"} {
				d := DiagnoseEnv(over, "", goos, e, true)
				if want := DetectEnv(over, e); d.Color != want {
					t.Errorf("goos=%s env=%v color=%q: report says %v, DetectEnv says %v",
						goos, e, over, d.Color, want)
				}
			}
		}
	}
}

// Every variable a rule reads has to be printable, otherwise doctor reports a
// decision whose input the user cannot see — which is the situation this whole
// type exists to end.
func TestDiagnoseShowsTheSignalsItRead(t *testing.T) {
	d := DiagnoseEnv("", "", "windows", env("WT_SESSION=abc", "LANG=en_US.UTF-8"), true)

	set := map[string]string{}
	for _, s := range d.Set() {
		set[s.Name] = s.Value
	}
	if set["WT_SESSION"] != "abc" {
		t.Errorf("WT_SESSION missing from the report: %v", set)
	}
	if set["LANG"] != "en_US.UTF-8" {
		t.Errorf("LANG missing from the report: %v", set)
	}
	if _, ok := set["TERM"]; ok {
		t.Errorf("TERM is unset here and must not be listed as a signal: %v", set)
	}

	for _, want := range []string{"TERM", "COLORTERM", "NO_COLOR", "LANG", "LC_ALL", "LC_CTYPE", "WT_SESSION"} {
		found := false
		for _, s := range d.Signals {
			if s.Name == want {
				found = true
			}
		}
		if !found {
			t.Errorf("%s is consulted by the detection but never reported", want)
		}
	}
}

func TestAdviceOnlyTalksAboutAxesTheUserHasNotPinned(t *testing.T) {
	// Nothing pinned, bare Windows console: both knobs are worth mentioning.
	d := DiagnoseEnv("", "", "windows", env(), true)
	if len(d.Advice) != 2 {
		t.Fatalf("want advice on both axes, got %v", d.Advice)
	}
	if !strings.Contains(strings.Join(d.Advice, "\n"), `glyphs = "unicode"`) {
		t.Errorf("an ASCII guess should offer the way back up: %v", d.Advice)
	}

	// Both pinned: the user has already decided, and repeating their own
	// setting back at them is noise.
	d = DiagnoseEnv("never", "ascii", "windows", env(), true)
	if len(d.Advice) != 0 {
		t.Errorf("want no advice when both axes are pinned, got %v", d.Advice)
	}

	// A UTF-8 host guessed as unicode gets the opposite suggestion.
	d = DiagnoseEnv("truecolor", "", "linux", env("TERM=xterm-256color", "LANG=en_US.UTF-8"), true)
	if len(d.Advice) != 1 || !strings.Contains(d.Advice[0], `glyphs = "ascii"`) {
		t.Errorf("a unicode guess should offer the way down: %v", d.Advice)
	}
}

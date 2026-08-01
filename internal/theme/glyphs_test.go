package theme_test

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/MichiTrader/ishakat/internal/theme"
)

// The reported symptom was a logo made of boxes on PowerShell and a correct one
// on Termux. These are those two consoles, plus the hosts in between, written
// as the environments they actually present.
func TestDetectGlyphsEnv(t *testing.T) {
	cases := []struct {
		name string
		goos string
		env  []string
		want theme.GlyphSet
	}{
		{
			// The bug report: powershell.exe from the Start menu. No TERM, no
			// WT_SESSION, and an OEM code page underneath.
			name: "conhost powershell",
			goos: "windows",
			env:  []string{"OS=Windows_NT", "COMSPEC=C:\\WINDOWS\\system32\\cmd.exe"},
			want: theme.GlyphsASCII,
		},
		{
			name: "windows terminal",
			goos: "windows",
			env:  []string{"WT_SESSION=b9e1...", "OS=Windows_NT"},
			want: theme.GlyphsUnicode,
		},
		{
			name: "vscode integrated terminal on windows",
			goos: "windows",
			env:  []string{"TERM_PROGRAM=vscode", "OS=Windows_NT"},
			want: theme.GlyphsUnicode,
		},
		{
			name: "conemu with ansi enabled",
			goos: "windows",
			env:  []string{"ConEmuANSI=ON", "OS=Windows_NT"},
			want: theme.GlyphsUnicode,
		},
		{
			// Git Bash / mintty: a Windows host, but an MSYS2 pty that is
			// UTF-8 by default and sets TERM like any Unix terminal.
			name: "git bash",
			goos: "windows",
			env:  []string{"TERM=xterm", "MSYSTEM=MINGW64"},
			want: theme.GlyphsUnicode,
		},
		{
			// The other half of the report: Termux, where the logo looked right.
			name: "termux",
			goos: "android",
			env:  []string{"TERM=xterm-256color", "LANG=en_US.UTF-8"},
			want: theme.GlyphsUnicode,
		},
		{
			name: "linux with no locale set at all",
			goos: "linux",
			env:  []string{"TERM=xterm-256color"},
			want: theme.GlyphsUnicode,
		},
		{
			// LANG=C is a promise that the output is single-byte; writing UTF-8
			// into it is mojibake, not a missing glyph.
			name: "linux with a single-byte locale",
			goos: "linux",
			env:  []string{"TERM=xterm-256color", "LANG=C"},
			want: theme.GlyphsASCII,
		},
		{
			name: "linux with LC_ALL overriding a utf-8 LANG",
			goos: "linux",
			env:  []string{"TERM=xterm", "LANG=es_AR.UTF-8", "LC_ALL=POSIX"},
			want: theme.GlyphsASCII,
		},
		{
			name: "dumb terminal",
			goos: "linux",
			env:  []string{"TERM=dumb", "LANG=en_US.UTF-8"},
			want: theme.GlyphsASCII,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := theme.DetectGlyphsEnv("auto", tc.goos, tc.env); got != tc.want {
				t.Errorf("DetectGlyphsEnv(auto, %s) = %v, quería %v", tc.goos, got, tc.want)
			}
		})
	}
}

// The override exists because no heuristic gets every console right, and a user
// staring at boxes needs a way to say so without waiting for a release.
func TestDetectGlyphsOverride(t *testing.T) {
	utf8Console := []string{"WT_SESSION=1"}
	legacyConsole := []string{"OS=Windows_NT"}

	if got := theme.DetectGlyphsEnv("ascii", "windows", utf8Console); got != theme.GlyphsASCII {
		t.Errorf(`glyphs = "ascii" debe forzar ASCII incluso en Windows Terminal, obtuve %v`, got)
	}
	if got := theme.DetectGlyphsEnv("unicode", "windows", legacyConsole); got != theme.GlyphsUnicode {
		t.Errorf(`glyphs = "unicode" debe forzar Unicode incluso en conhost, obtuve %v`, got)
	}
	if got := theme.DetectGlyphsEnv("", "windows", legacyConsole); got != theme.GlyphsASCII {
		t.Errorf("un override vacío debe caer en la detección, obtuve %v", got)
	}
	if got := theme.DetectGlyphsEnv("moño", "linux", []string{"TERM=xterm"}); got != theme.GlyphsUnicode {
		t.Errorf("un override desconocido debe caer en la detección, obtuve %v", got)
	}
}

// The input box is the one frame the user looks at for the whole session, so
// its corners are the last place to spend characters the font may not have.
// lipgloss.RoundedBorder's corners are U+256D..U+2570 — not in WGL4, absent
// from Consolas — which is why this asserts they are never drawn.
func TestBoxBorderStaysInsideTheRepertoire(t *testing.T) {
	th := theme.Load("ascua")

	unicodeBox := stripANSI(theme.NewStyles(th, theme.CapTruecolor, theme.GlyphsUnicode).RenderBox("hola", 20))
	for _, forbidden := range []string{"╭", "╮", "╰", "╯"} {
		if strings.Contains(unicodeBox, forbidden) {
			t.Errorf("la caja dibuja %q, que no está en WGL4:\n%s", forbidden, unicodeBox)
		}
	}
	if !strings.Contains(unicodeBox, "┌") || !strings.Contains(unicodeBox, "─") {
		t.Errorf("la caja Unicode debería usar el borde recto de cp437:\n%s", unicodeBox)
	}

	asciiBox := stripANSI(theme.NewStyles(th, theme.CapTruecolor, theme.GlyphsASCII).RenderBox("hola", 20))
	for _, r := range asciiBox {
		if r > 127 {
			t.Fatalf("la caja ASCII contiene %q (U+%04X):\n%s", r, r, asciiBox)
		}
	}
	if !strings.Contains(asciiBox, "+") || !strings.Contains(asciiBox, "|") {
		t.Errorf("la caja ASCII debería usar +-|:\n%s", asciiBox)
	}
}

func TestGlyphSetString(t *testing.T) {
	if got := theme.GlyphsUnicode.String(); got != "unicode" {
		t.Errorf("GlyphsUnicode.String() = %q", got)
	}
	if got := theme.GlyphsASCII.String(); got != "ascii" {
		t.Errorf("GlyphsASCII.String() = %q", got)
	}
	if !theme.GlyphsASCII.ASCII() || theme.GlyphsUnicode.ASCII() {
		t.Error("ASCII() no distingue los dos sets")
	}
}

// TestRenderBoxIsExactlyAsWideAsAsked pins the contract the caller depends on:
// the number handed to RenderBox is the number of terminal columns the finished
// box occupies, borders included — because that is the number the caller
// budgeted for it, and because a box narrower than its content makes lipgloss
// re-wrap whatever was put inside it.
//
// The reported bug was the same subtraction applied twice: lipgloss v2's
// Style.Width already counts the frame, so RenderBox's own "width - 2" made
// every box two columns narrow. Two columns is invisible on the border and
// fatal to the content — see internal/tui's TestTypingPastTheEdge... for what it
// did to the input box.
func TestRenderBoxIsExactlyAsWideAsAsked(t *testing.T) {
	st := theme.NewStyles(theme.Load("ascua"), theme.CapTruecolor, theme.GlyphsUnicode)
	for _, width := range []int{20, 40, 60, 120} {
		// A line that exactly fills the inside of the box: it must come out
		// on one row, untouched. One column more and lipgloss would have to
		// wrap it, which is the failure this guards.
		content := strings.Repeat("z", width-2)
		box := stripANSI(st.RenderBox(content, width))
		for i, line := range strings.Split(box, "\n") {
			if got := lipgloss.Width(line); got != width {
				t.Errorf("RenderBox(_, %d): row %d is %d columns wide:\n%s", width, i, got, box)
			}
		}
		if rows := strings.Count(box, "\n") + 1; rows != 3 {
			t.Errorf("RenderBox(_, %d) re-wrapped a line that fitted: %d rows instead of 3:\n%s",
				width, rows, box)
		}
	}
}

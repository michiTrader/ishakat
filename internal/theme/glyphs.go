package theme

import (
	"os"
	"runtime"
	"strings"
)

// GlyphSet is which characters the interface may draw. It is a separate axis
// from Capability because the two questions have nothing to do with each other:
// a stock PowerShell console paints 24-bit colour happily and still shows a box
// instead of "▖", which is exactly why the start-up logo was reported as
// unreadable while the gradient problem was a colour problem.
type GlyphSet int

const (
	// GlyphsUnicode allows the WGL4 repertoire: the ~650 characters every
	// Windows monospace font has carried since Windows 95, plus everything
	// below it. That covers the half blocks (█ ▄ ▀ ▌ ▐), the shading blocks
	// (░ ▒ ▓), single and double box drawing, ‹ ›, · and the arrows — and it
	// deliberately excludes the quadrant blocks (▖ ▘ ▝ ▗ ▚ ▞), the eighth
	// blocks (▊ ▍), the rounded corners (╭ ╮ ╰ ╯) and the geometric shapes
	// (◆ ◍ ⎇) that Consolas does not ship and that therefore came out as
	// boxes. Choosing a documented repertoire instead of "whatever looked
	// nice" is the whole fix: it is a rule that can be checked.
	GlyphsUnicode GlyphSet = iota

	// GlyphsASCII allows nothing above U+007F. It is for a legacy console
	// whose output code page is not UTF-8 (cp437, cp850, cp1252 — the default
	// of conhost.exe), where our UTF-8 bytes are not merely missing a glyph
	// but decoded as the wrong characters entirely.
	GlyphsASCII
)

// ASCII reports whether this set is restricted to ASCII.
func (g GlyphSet) ASCII() bool { return g == GlyphsASCII }

// String is the name used by `ishakat doctor` and by [ui] glyphs.
func (g GlyphSet) String() string {
	if g == GlyphsASCII {
		return "ascii"
	}
	return "unicode"
}

// DetectGlyphs resolves the glyph set for the running process, honouring the
// [ui] glyphs override ("auto" | "unicode" | "ascii").
func DetectGlyphs(override string) GlyphSet {
	return DetectGlyphsEnv(override, runtime.GOOS, os.Environ())
}

// DetectGlyphsEnv is DetectGlyphs against an explicit OS and environment. It
// exists so the rules are testable: the interesting case is a Windows console,
// which is precisely the platform the test suite does not run on, and a rule
// that can only be exercised by shipping it is not a rule.
func DetectGlyphsEnv(override, goos string, env []string) GlyphSet {
	switch strings.ToLower(strings.TrimSpace(override)) {
	case "ascii", "plain", "off":
		return GlyphsASCII
	case "unicode", "utf8", "utf-8", "on":
		return GlyphsUnicode
	}

	// A dumb terminal gets nothing decorative by definition.
	if term, _ := lookupEnv(env, "TERM"); strings.EqualFold(term, "dumb") {
		return GlyphsASCII
	}

	if strings.EqualFold(goos, "windows") {
		return windowsGlyphs(env)
	}

	// Everywhere else the locale is the honest answer, when it says anything:
	// LANG=C or LC_ALL=POSIX is a promise that the output is single-byte, and
	// writing UTF-8 into it produces mojibake rather than a missing glyph.
	if loc, ok := locale(env); ok && !strings.Contains(strings.ToLower(loc), "utf") {
		return GlyphsASCII
	}
	return GlyphsUnicode
}

// windowsGlyphs decides for a Windows host. The default is ASCII, and that is
// not pessimism: conhost.exe — what you get from powershell.exe or cmd.exe
// opened from the Start menu — starts on the system's OEM code page, so the
// UTF-8 bytes Go writes are decoded as cp437/cp850 and even "·" comes out
// wrong. The exceptions are the hosts that are known to run a UTF-8 pipe with
// a modern font:
//
//   - Windows Terminal (WT_SESSION), which ships Cascadia Mono;
//   - the VS Code and Hyper integrated terminals (TERM_PROGRAM);
//   - ConEmu with ANSI enabled (ConEmuANSI);
//   - anything that sets TERM, which on Windows means an MSYS2/Cygwin pty —
//     Git Bash, mintty, MSYS — all of which are UTF-8 by default.
//
// A user on a console we guessed wrong about has [ui] glyphs to say so, in
// either direction, and `ishakat doctor` prints what was guessed.
func windowsGlyphs(env []string) GlyphSet {
	if v, _ := lookupEnv(env, "WT_SESSION"); v != "" {
		return GlyphsUnicode
	}
	if v, _ := lookupEnv(env, "ConEmuANSI"); strings.EqualFold(v, "on") {
		return GlyphsUnicode
	}
	switch strings.ToLower(first(env, "TERM_PROGRAM")) {
	case "vscode", "hyper", "windows_terminal", "mintty":
		return GlyphsUnicode
	}
	if v, _ := lookupEnv(env, "TERM"); v != "" {
		return GlyphsUnicode
	}
	return GlyphsASCII
}

// locale returns the effective locale following the POSIX precedence, and
// whether any of the three variables was set at all. "Unset" and "set to C"
// are different answers: unset means we know nothing, C means single-byte.
func locale(env []string) (string, bool) {
	for _, key := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if v, ok := lookupEnv(env, key); ok && v != "" {
			return v, true
		}
	}
	return "", false
}

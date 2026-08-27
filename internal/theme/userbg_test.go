package theme_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/MichiTrader/ishakat/internal/theme"
)

// TestLoadEmbebidoUserBG pins the embedded ascua.toml's own `user_bg` value
// (§17 2026-08-19's second half, "user messages should have a different
// background color") the same way TestLoadEmbebido already pins Accent and
// User: a regression that dropped the `user_bg` key from the TOML file, or
// that mistyped its target in parse's own targets map, should fail a build
// test, not a manual look at the running app.
func TestLoadEmbebidoUserBG(t *testing.T) {
	th := theme.Load("ascua")
	if th.UserBG.Hex() != "#182420" {
		t.Errorf("UserBG mal parseado: got %s, want #182420", th.UserBG.Hex())
	}
}

// TestLoadUserThemeMissingUserBGFallsBackToBase is UserBG's own doc comment
// promise made concrete: a theme file written before this field existed (or
// one that simply never sets `user_bg`) must not end up with RGB{}'s pure
// black, it must inherit builtinFallback's own value — exactly the contract
// every other colour in Theme already has, and TestLoadDesdeDirectorioDeUsuario
// already pins for FG.
func TestLoadUserThemeMissingUserBGFallsBackToBase(t *testing.T) {
	dir := t.TempDir()
	body := "name = \"sin_user_bg\"\n[colors]\naccent = \"#00ff00\"\n"
	if err := os.WriteFile(filepath.Join(dir, "sin_user_bg.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	th := theme.Load("sin_user_bg", dir)
	if th.UserBG.Hex() != "#182420" {
		t.Errorf("un tema sin user_bg debería heredar el valor base, got %s", th.UserBG.Hex())
	}
}

// TestLoadUserThemeCanOverrideUserBG confirms the new key is actually wired
// through parse's targets map and not merely declared on the struct: a
// theme author's own `user_bg` must win over the base the same way `accent`
// already does.
func TestLoadUserThemeCanOverrideUserBG(t *testing.T) {
	dir := t.TempDir()
	body := "name = \"bg_custom\"\n[colors]\nuser_bg = \"#101010\"\n"
	if err := os.WriteFile(filepath.Join(dir, "bg_custom.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	th := theme.Load("bg_custom", dir)
	if th.UserBG.Hex() != "#101010" {
		t.Errorf("user_bg del usuario ignorado: got %s, want #101010", th.UserBG.Hex())
	}
}

// TestPaintBackgroundSurvivesNestedReset is the regression test for the
// central risk this feature's implementation had to solve: lipgloss/ansi's
// Style.Styled always appends a *full* SGR reset ("\x1b[m", ansi.ResetStyle),
// not one scoped to a single attribute, so any already-styled substring
// embedded in the block (a Chroma token, a header's own foreground) would
// silently clear PaintBackground's own background the instant that
// substring's reset fires — not just its own foreground. This confirms the
// background escape reappears immediately after every embedded reset, so
// the paint survives to the end of the line.
func TestPaintBackgroundSurvivesNestedReset(t *testing.T) {
	th := theme.Load("")
	styles := theme.NewStyles(th, theme.CapTruecolor, theme.GlyphsUnicode)

	inner := styles.Accent.Render("red") + " plain " + styles.Accent.Render("red2")
	out := styles.PaintBackground(inner, 0)

	bgEsc := fmtBG(th.UserBG)
	// The background escape must appear at least twice: once at the very
	// start, and once more right after the first embedded full reset —
	// otherwise " plain " (and everything after it) would render with no
	// background at all, which is the exact bug this function exists to fix.
	if strings.Count(out, bgEsc) < 2 {
		t.Fatalf("background escape does not reappear after the embedded reset: got %q", out)
	}
	if !strings.Contains(out, "plain") {
		t.Errorf("PaintBackground must not touch the text itself: got %q", out)
	}
}

// TestPaintBackgroundNoColourIsNoOp pins CapNone's long-standing contract
// (NewStyles' own doc comment) for the new UserBG field too: with no colour
// capability, PaintBackground must return the block byte-for-byte unchanged,
// not an empty escape pair around it.
func TestPaintBackgroundNoColourIsNoOp(t *testing.T) {
	th := theme.Load("")
	styles := theme.NewStyles(th, theme.CapNone, theme.GlyphsUnicode)

	block := "hola\nmundo"
	if got := styles.PaintBackground(block, 40); got != block {
		t.Errorf("CapNone debe ser un no-op: got %q, want %q", got, block)
	}
}

// TestPaintBackgroundSkipsBlankLines confirms a blank line inside a
// multi-line block is left empty rather than gaining a background-only
// escape pair around nothing — a cosmetic but real difference: a "coloured"
// empty line still paints a visible strip of background across the
// terminal, which reads as an odd gap in an otherwise-tight bubble.
func TestPaintBackgroundSkipsBlankLines(t *testing.T) {
	th := theme.Load("")
	styles := theme.NewStyles(th, theme.CapTruecolor, theme.GlyphsUnicode)

	out := styles.PaintBackground("uno\n\ndos", 0)
	lines := strings.Split(out, "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 lines, got %d: %q", len(lines), out)
	}
	if lines[1] != "" {
		t.Errorf("blank line should stay blank, got %q", lines[1])
	}
}

// TestPaintBackgroundPadsShortLinesToWidth is the regression test for the
// 2026-08-27 fix: before width existed, a short line's background stopped
// right after its own last visible cell, so on screen the paint read as
// "highlighted letters" rather than a full-width message bubble strip. This
// confirms a line shorter than width is padded with plain spaces *inside*
// the coloured span (prefix ... padding ... suffix), and that the padding
// itself carries no visible character — only the background escape reaches
// further right than the text did.
func TestPaintBackgroundPadsShortLinesToWidth(t *testing.T) {
	th := theme.Load("")
	styles := theme.NewStyles(th, theme.CapTruecolor, theme.GlyphsUnicode)

	out := styles.PaintBackground("hi", 10)
	bgEsc := fmtBG(th.UserBG)
	i := strings.Index(out, bgEsc)
	if i < 0 {
		t.Fatalf("missing background escape entirely: %q", out)
	}
	// Strip the leading prefix escape and the trailing suffix reset, what's
	// left between them is "hi" plus 8 padding spaces.
	suffix := ansiFullResetFor(t, styles)
	rest := strings.TrimSuffix(out[i+len(bgEsc):], suffix)
	// rest may still start with an SGR terminator ("m") from the escape
	// sequence itself; find where "hi" begins instead of assuming byte 0.
	hPos := strings.Index(rest, "hi")
	if hPos < 0 {
		t.Fatalf("text missing from painted output: %q", out)
	}
	afterText := rest[hPos+len("hi"):]
	if afterText != strings.Repeat(" ", 8) {
		t.Errorf("expected 8 padding spaces after a 2-cell line at width 10, got %q (full output %q)", afterText, out)
	}
}

// TestPaintBackgroundZeroWidthDoesNotPad pins width<=0's documented
// "do not pad" behaviour: existing callers (mostly tests) that only care
// about the reset-patching, not a real line width, must keep getting back
// exactly the pre-width-parameter output.
func TestPaintBackgroundZeroWidthDoesNotPad(t *testing.T) {
	th := theme.Load("")
	styles := theme.NewStyles(th, theme.CapTruecolor, theme.GlyphsUnicode)

	withZero := styles.PaintBackground("hi", 0)
	withNegative := styles.PaintBackground("hi", -1)
	if withZero != withNegative {
		t.Errorf("width<=0 should behave identically: width=0 got %q, width=-1 got %q", withZero, withNegative)
	}
	if strings.Contains(withZero, "  ") {
		t.Errorf("width<=0 must not pad: got %q", withZero)
	}
}

// ansiFullResetFor returns the exact suffix PaintBackground appends after
// every painted line (the probe's own suffix half), so the padding test
// above can strip it without hard-coding the escape sequence twice.
func ansiFullResetFor(t *testing.T, s theme.Styles) string {
	t.Helper()
	probe := s.UserBG.Render("\x00")
	i := strings.IndexByte(probe, 0)
	if i < 0 {
		t.Fatal("UserBG probe produced no escape at all")
	}
	return probe[i+1:]
}

// fmtBG mirrors the tui package's own ansiFG test helper (48 is the SGR base
// for a truecolor background, where 38 is the one for foreground), kept
// local to this file since the theme package's own tests have no equivalent
// helper yet.
func fmtBG(c theme.RGB) string {
	return "48;2;" + strconv.Itoa(int(c.R)) + ";" + strconv.Itoa(int(c.G)) + ";" + strconv.Itoa(int(c.B))
}

package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/MichiTrader/ishakat/internal/theme"
)

// TestRenderTranscriptLineColoursHeaderByRole is the regression test for §17
// 2026-08-19's "user/assistant messages are not visually differentiated"
// fix: theme.Styles.User and .Assistant have existed since Step 3, and every
// theme's TOML has always defined distinct colours for them (ascua.toml's
// own user=#7fd1b9, assistant=#ffb454), but renderTranscriptLine never
// called .Render with either one — the header of every bubble drew in the
// same colour regardless of who sent it. This pins that a user bubble's
// header now carries styles.User's truecolor escape and an assistant
// bubble's carries styles.Assistant's, and that the two are not the same
// escape (a copy-paste that pointed both branches at the same field would
// still pass a test that only checked "some colour is present").
func TestRenderTranscriptLineColoursHeaderByRole(t *testing.T) {
	th := theme.Load("")
	styles := theme.NewStyles(th, theme.CapTruecolor, theme.GlyphsUnicode)
	ts := time.Date(2026, 8, 19, 14, 2, 0, 0, time.UTC)

	userOut := renderTranscriptLine(styles, unicodeGlyphs, 40, "user", "tú", "hola", ts, false, false, false, "", "")
	assistantOut := renderTranscriptLine(styles, unicodeGlyphs, 40, "assistant", "openai/gpt-5", "hola", ts, false, false, false, "", "")

	userEsc := ansiFG(th.User)
	assistantEsc := ansiFG(th.Assistant)

	if !strings.Contains(userOut, userEsc) {
		t.Errorf("user bubble header does not carry styles.User's escape (%q); got %q", userEsc, userOut)
	}
	if !strings.Contains(assistantOut, assistantEsc) {
		t.Errorf("assistant bubble header does not carry styles.Assistant's escape (%q); got %q", assistantEsc, assistantOut)
	}
	if userEsc == assistantEsc {
		t.Fatalf("theme's own User/Assistant colours collide (%q); the test cannot distinguish the two branches", userEsc)
	}
	if strings.Contains(userOut, assistantEsc) {
		t.Errorf("user bubble header unexpectedly carries the assistant's colour: %q", userOut)
	}
	if strings.Contains(assistantOut, userEsc) {
		t.Errorf("assistant bubble header unexpectedly carries the user's colour: %q", assistantOut)
	}
}

// TestRenderTranscriptLineHeaderColourDoesNotBleedIntoBody guards the
// deliberate scope of the fix: only the header line (marker, name,
// timestamp) is coloured by role, not the message body — the body is where
// Chroma (code) and Glamour (prose) already apply their own colouring, and
// tinting it by role on top would fight both. This is checked by confirming
// the role colour's escape appears exactly once — on the header — even
// though the body itself contains no markdown/code that would produce its
// own competing escapes here (highlightCode/renderProse both false).
func TestRenderTranscriptLineHeaderColourDoesNotBleedIntoBody(t *testing.T) {
	th := theme.Load("")
	styles := theme.NewStyles(th, theme.CapTruecolor, theme.GlyphsUnicode)
	ts := time.Date(2026, 8, 19, 14, 2, 0, 0, time.UTC)

	out := renderTranscriptLine(styles, unicodeGlyphs, 40, "user", "tú", "un mensaje sin formato especial", ts, false, false, false, "", "")
	lines := strings.SplitN(out, "\n", 2)
	if len(lines) != 2 {
		t.Fatalf("expected a header line and a body line, got %d line(s): %q", len(lines), out)
	}
	userEsc := ansiFG(th.User)
	if !strings.Contains(lines[0], userEsc) {
		t.Errorf("header line missing the role colour: %q", lines[0])
	}
	if strings.Contains(stripANSI(lines[1]), "\x1b") {
		t.Errorf("body line still carries a raw escape byte after stripANSI, test helper itself is broken")
	}
	// TrimRight, not a bare ==: a user bubble's body is now padded with
	// trailing spaces up to width by PaintBackground (2026-08-27, "full line
	// background, not just under the letters") so the background reaches
	// the same right edge on every line — that padding is the fix working
	// as intended, not a change to the message text itself.
	if got := strings.TrimRight(stripANSI(lines[1]), " "); got != "un mensaje sin formato especial" {
		t.Errorf("body text changed by the header colouring: got %q", got)
	}
}

// TestRenderTranscriptLineNoColourEmitsNoEscape is theme.CapNone's own
// long-standing contract (NewStyles' doc comment: "con CapNone todos quedan
// sin color, así el resto del código no tiene que preguntar nunca si hay
// color o no"), pinned here so the new .Render call does not quietly break
// it — a plain-text terminal, or NO_COLOR, must see the exact same bytes
// this function has always produced for a role marker, not a bare escape
// sequence with an empty colour.
func TestRenderTranscriptLineNoColourEmitsNoEscape(t *testing.T) {
	th := theme.Load("")
	styles := theme.NewStyles(th, theme.CapNone, theme.GlyphsUnicode)
	ts := time.Date(2026, 8, 19, 14, 2, 0, 0, time.UTC)

	out := renderTranscriptLine(styles, unicodeGlyphs, 40, "user", "tú", "hola", ts, false, false, false, "", "")
	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("theme.CapNone must never emit an escape sequence, got %q", out)
	}
}

// TestRenderTranscriptLineUserGetsBackground is the regression test for §17
// 2026-08-19's second half, "user messages should have a different
// background color" — a distinct requirement from the header-foreground fix
// pinned by TestRenderTranscriptLineColoursHeaderByRole above. It confirms a
// user bubble's rendered output carries the theme's UserBG background
// escape somewhere in *both* the header line and the body line (the whole
// bubble, not just one or the other), and that an assistant bubble carries
// neither — the background is exclusively a user-message affordance.
func TestRenderTranscriptLineUserGetsBackground(t *testing.T) {
	th := theme.Load("")
	styles := theme.NewStyles(th, theme.CapTruecolor, theme.GlyphsUnicode)
	ts := time.Date(2026, 8, 19, 14, 2, 0, 0, time.UTC)

	userOut := renderTranscriptLine(styles, unicodeGlyphs, 40, "user", "tú", "hola\nmundo", ts, false, false, false, "", "")
	assistantOut := renderTranscriptLine(styles, unicodeGlyphs, 40, "assistant", "openai/gpt-5", "hola\nmundo", ts, false, false, false, "", "")

	bgEsc := ansiBG(th.UserBG)
	lines := strings.SplitN(userOut, "\n", 2)
	if len(lines) != 2 {
		t.Fatalf("expected a header line and a body block, got %d piece(s): %q", len(lines), userOut)
	}
	if !strings.Contains(lines[0], bgEsc) {
		t.Errorf("user bubble header missing the UserBG background escape: %q", lines[0])
	}
	if !strings.Contains(lines[1], bgEsc) {
		t.Errorf("user bubble body missing the UserBG background escape: %q", lines[1])
	}
	if strings.Contains(assistantOut, bgEsc) {
		t.Errorf("assistant bubble must not carry the user background: %q", assistantOut)
	}
}

// TestRenderTranscriptLineUserBackgroundSurvivesCodeBlock guards the
// scenario PaintBackground's own doc comment calls out by name: a user
// message whose body contains a fenced, syntax-highlighted code block is
// full of Chroma's own per-token escapes, each ending in a full SGR reset.
// Without the reset-patching PaintBackground does, the background would
// visibly "leak" back to the terminal's default between tokens. This pins
// that the background escape reappears after the code block's own colouring
// — i.e. that it is present more than once in the rendered body — rather
// than only at the very start of the message.
func TestRenderTranscriptLineUserBackgroundSurvivesCodeBlock(t *testing.T) {
	th := theme.Load("")
	styles := theme.NewStyles(th, theme.CapTruecolor, theme.GlyphsUnicode)
	ts := time.Date(2026, 8, 19, 14, 2, 0, 0, time.UTC)

	text := "mira esto:\n```go\nfunc main() { x := 1 }\n```\ngracias"
	out := renderTranscriptLine(styles, unicodeGlyphs, 60, "user", "tú", text, ts, true, false, false, "", "")

	bgEsc := ansiBG(th.UserBG)
	if got := strings.Count(out, bgEsc); got < 2 {
		t.Fatalf("background escape must reappear after the code block's own resets, got only %d occurrence(s): %q", got, out)
	}
	if !strings.Contains(stripANSI(out), "func main() { x := 1 }") {
		t.Errorf("code text must survive unchanged under the background paint: %q", stripANSI(out))
	}
}

// ansiBG is ansiFG's own sibling: the truecolor *background* escape lipgloss
// emits for c ("48;2;r;g;b" rather than foreground's "38;2;r;g;b").
func ansiBG(c theme.RGB) string {
	return strings.Replace(ansiFG(c), "38;2;", "48;2;", 1)
}

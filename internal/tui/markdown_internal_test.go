package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/theme"
)

// TestRenderMarkdownBoldProducesAnANSIRun confirms **bold** actually gets
// rendered through Glamour rather than left as literal asterisks, and that
// the plain words survive once escapes are stripped.
func TestRenderMarkdownBoldProducesAnANSIRun(t *testing.T) {
	th := theme.Load("ascua")
	styles := theme.NewStyles(th, theme.CapTruecolor, theme.GlyphsUnicode)

	out := renderMarkdown(styles, "this is **bold** text", 40)
	if !strings.Contains(out, "\x1b[") {
		t.Errorf("expected at least one ANSI escape in the rendered output, got %q", out)
	}
	plain := stripANSI(out)
	for _, want := range []string{"this", "is", "bold", "text"} {
		if !strings.Contains(plain, want) {
			t.Errorf("plain text %q missing from rendered output once escapes are stripped: %q", want, plain)
		}
	}
}

// TestRenderMarkdownCapNoneNeverColours confirms a CapNone terminal gets
// zero ANSI escapes even with heavy Markdown syntax, mirroring
// syntaxStyleFor's own CapNone rule in codeblock.go.
func TestRenderMarkdownCapNoneNeverColours(t *testing.T) {
	th := theme.Load("ascua")
	styles := theme.NewStyles(th, theme.CapNone, theme.GlyphsUnicode)

	out := renderMarkdown(styles, "# Heading\n\n**bold** and [a link](http://example.com)\n\n- one\n- two", 40)
	if strings.Contains(out, "\x1b[") {
		t.Errorf("CapNone must never colour markdown output, got %q", out)
	}
}

// TestRenderMarkdownPlainTextSurvives confirms every word of plain
// (no-Markdown) input text survives Glamour's rendering.
func TestRenderMarkdownPlainTextSurvives(t *testing.T) {
	th := theme.Load("ascua")
	styles := theme.NewStyles(th, theme.CapTruecolor, theme.GlyphsUnicode)
	text := "una respuesta normal sin ningun marcado especial de por medio"

	out := renderMarkdown(styles, text, 40)
	plain := stripANSI(out)
	for _, word := range strings.Fields(text) {
		if !strings.Contains(plain, word) {
			t.Errorf("word %q missing from rendered output: %q", word, plain)
		}
	}
}

// TestRenderMessageBodyRenderProseGatesMarkdown confirms the config gate:
// renderProse=false leaves **negrita** markers literal; renderProse=true
// consumes them into a real bold escape.
func TestRenderMessageBodyRenderProseGatesMarkdown(t *testing.T) {
	th := theme.Load("ascua")
	styles := theme.NewStyles(th, theme.CapTruecolor, theme.GlyphsUnicode)
	g := unicodeGlyphs
	text := "esto es **negrita** de verdad"

	off := renderMessageBody(styles, g, text, 40, true, false)
	if !strings.Contains(off, "**negrita**") {
		t.Errorf("with renderProse off, ** markers must stay literal: %q", off)
	}

	on := renderMessageBody(styles, g, text, 40, true, true)
	if strings.Contains(on, "**negrita**") {
		t.Errorf("with renderProse on, ** markers must be consumed by Glamour: %q", on)
	}
	if !strings.Contains(on, "\x1b[") {
		t.Errorf("with renderProse on, expected an ANSI escape for the bold run: %q", on)
	}
}

// TestRenderMessageBodyMarkdownLeavesFencedCodeToChroma confirms mixed
// prose+fence text still draws the rail for the code segment while the
// prose segment's ** markers get consumed by Markdown rendering.
func TestRenderMessageBodyMarkdownLeavesFencedCodeToChroma(t *testing.T) {
	th := theme.Load("ascua")
	styles := theme.NewStyles(th, theme.CapTruecolor, theme.GlyphsUnicode)
	g := unicodeGlyphs
	text := "una nota **importante**:\n```go\nfunc main() {}\n```\nfin"

	out := renderMessageBody(styles, g, text, 40, true, true)
	if !strings.Contains(out, g.barLead) {
		t.Errorf("the rail must still be drawn for the fenced segment: %q", stripANSI(out))
	}
	plain := stripANSI(out)
	if !strings.Contains(plain, "func main() {}") {
		t.Errorf("code content lost: %q", plain)
	}
	if strings.Contains(plain, "**importante**") {
		t.Errorf("prose ** markers must be consumed by Glamour even alongside a fence: %q", plain)
	}
	if strings.Contains(plain, "```") {
		t.Errorf("fence markers must not survive onto the screen: %q", plain)
	}
}

// TestRenderMarkdownBrokenRendererFallsBackToWrapText confirms width<=0
// never panics/hangs (Glamour's WithWordWrap(0) means "one char per line",
// not "unlimited" like wrapText's own convention) and still renders text.
func TestRenderMarkdownBrokenRendererFallsBackToWrapText(t *testing.T) {
	th := theme.Load("ascua")
	styles := theme.NewStyles(th, theme.CapTruecolor, theme.GlyphsUnicode)
	text := "hola mundo"

	got := renderMarkdown(styles, text, 0)
	want := wrapText(text, 0)
	if got != want {
		t.Errorf("width<=0 must fall back to wrapText directly:\ngot  %q\nwant %q", got, want)
	}
}

// TestRenderMarkdownPlainTextSkipsGlamourEntirely pins hasMarkdownSyntax's
// fast path: text with none of the Markdown-triggering bytes must produce
// byte-identical output to wrapText, the same guarantee
// TestRenderMessageBodyWithNoFenceMatchesWrapText already pins one layer up
// in codeblock_internal_test.go — this is the layer where that identity is
// actually decided.
func TestRenderMarkdownPlainTextSkipsGlamourEntirely(t *testing.T) {
	th := theme.Load("ascua")
	styles := theme.NewStyles(th, theme.CapTruecolor, theme.GlyphsUnicode)
	text := strings.Repeat("z", 300)

	got := renderMarkdown(styles, text, 40)
	want := wrapText(text, 40)
	if got != want {
		t.Errorf("text with no markdown syntax must match wrapText verbatim:\ngot  %q\nwant %q", got, want)
	}
}

// TestPlayTurnWithMarkdownRendersBoldEndToEnd is the integration test via
// the real Root/dummy echo engine, mirroring
// TestPlayTurnWithFencedCodeRendersRailEndToEnd's own pattern: confirms
// bold markdown streamed in via chunks renders correctly end-to-end.
func TestPlayTurnWithMarkdownRendersBoldEndToEnd(t *testing.T) {
	text := "esto es **muy importante** de verdad"

	var m tea.Model = newVisibleRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 60, Height: 30})
	m = playTurn(m, text)

	content := m.View().Content
	plain := stripANSI(content)
	for _, want := range []string{"esto", "es", "muy", "importante", "de", "verdad"} {
		if !strings.Contains(plain, want) {
			t.Errorf("frame is missing %q once escapes are stripped:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "**muy importante**") {
		t.Errorf("** markers must not survive onto the screen: %q", plain)
	}
}

package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/theme"
)

// TestRenderReasoningPreviewOffShowsNothing pins ui.reasoning = "off": no
// matter how much reasoning a turn produced, the preview is "" — the same
// "hidden means hidden, regardless of how much there is" contract
// headless.go's own showReasoning already gives that mode on the text sink.
func TestRenderReasoningPreviewOffShowsNothing(t *testing.T) {
	th := theme.Load("")
	styles := theme.NewStyles(th, theme.CapTruecolor, theme.GlyphsUnicode)

	got := renderReasoningPreview(styles, unicodeGlyphs, "the model thinks step by step about this", "off", false, 40)
	if got != "" {
		t.Errorf("mode=off must render nothing, got %q", got)
	}
}

// TestRenderReasoningPreviewOffShowsNothingEvenFolded extends the case
// above to folded=true: F8b's own "off stays authoritative" rule
// (renderReasoningPreview's doc comment) means toggling ctrl+r must never
// reveal a reasoning stream ui.reasoning="off" says to hide.
func TestRenderReasoningPreviewOffShowsNothingEvenFolded(t *testing.T) {
	th := theme.Load("")
	styles := theme.NewStyles(th, theme.CapTruecolor, theme.GlyphsUnicode)

	got := renderReasoningPreview(styles, unicodeGlyphs, "the model thinks step by step about this", "off", true, 40)
	if got != "" {
		t.Errorf("mode=off, folded=true must still render nothing, got %q", got)
	}
}

// TestRenderReasoningPreviewEmptyReasoningShowsNothing guards the "no
// pointless dim row" rule renderReasoningPreview's own doc comment states:
// even under "full", a turn that produced no reasoning at all must not
// leave a blank styled line glued to the answer.
func TestRenderReasoningPreviewEmptyReasoningShowsNothing(t *testing.T) {
	th := theme.Load("")
	styles := theme.NewStyles(th, theme.CapTruecolor, theme.GlyphsUnicode)

	for _, mode := range []string{"off", "collapsed", "full", "", "bogus"} {
		for _, folded := range []bool{false, true} {
			if got := renderReasoningPreview(styles, unicodeGlyphs, "   ", mode, folded, 40); got != "" {
				t.Errorf("mode=%q folded=%v with blank reasoning must render nothing, got %q", mode, folded, got)
			}
		}
	}
}

// TestRenderReasoningPreviewCollapsedTruncatesWithEllipsis pins the literal
// "~2 lines" shape from the report: a reasoning stream that wraps to more
// than reasoningPreviewLines rows is cut to exactly that many, with a
// trailing "…" line marking that more was dropped — mirroring
// foldSummary's/truncateOutput's own "always say something was cut"
// discipline elsewhere in this package.
func TestRenderReasoningPreviewCollapsedTruncatesWithEllipsis(t *testing.T) {
	th := theme.Load("")
	styles := theme.NewStyles(th, theme.CapTruecolor, theme.GlyphsUnicode)

	long := strings.Repeat("word ", 40) // wraps to well over 2 lines at width 20
	got := renderReasoningPreview(styles, unicodeGlyphs, long, "collapsed", false, 20)
	plain := stripANSI(got)
	lines := strings.Split(plain, "\n")

	if len(lines) != reasoningPreviewLines+1 {
		t.Fatalf("expected %d lines (preview + ellipsis), got %d: %q", reasoningPreviewLines+1, len(lines), plain)
	}
	if got := strings.TrimSpace(lines[len(lines)-1]); got != "…" {
		t.Errorf("last line must be the ellipsis marker, got %q", got)
	}
}

// TestRenderReasoningPreviewCollapsedShortStaysWhole confirms the other half
// of "collapsed": reasoning that already fits within reasoningPreviewLines
// lines renders in full, with no ellipsis appended — truncation only ever
// fires when something would actually be cut.
func TestRenderReasoningPreviewCollapsedShortStaysWhole(t *testing.T) {
	th := theme.Load("")
	styles := theme.NewStyles(th, theme.CapTruecolor, theme.GlyphsUnicode)

	short := "thinking briefly"
	got := renderReasoningPreview(styles, unicodeGlyphs, short, "collapsed", false, 40)
	plain := stripANSI(got)
	if strings.Contains(plain, "…") {
		t.Errorf("short reasoning must not gain an ellipsis: %q", plain)
	}
	if !strings.Contains(plain, short) {
		t.Errorf("short reasoning text lost, got %q", plain)
	}
}

// TestRenderReasoningPreviewFullShowsEverything pins "full": no truncation
// at all, however many wrapped lines the reasoning stream needs.
func TestRenderReasoningPreviewFullShowsEverything(t *testing.T) {
	th := theme.Load("")
	styles := theme.NewStyles(th, theme.CapTruecolor, theme.GlyphsUnicode)

	long := strings.Repeat("word ", 40)
	got := renderReasoningPreview(styles, unicodeGlyphs, long, "full", false, 20)
	plain := stripANSI(got)
	if strings.Contains(plain, "…") {
		t.Errorf("mode=full must never truncate, got an ellipsis in %q", plain)
	}
	wantWords := strings.Fields(long)
	gotWords := strings.Fields(plain)
	if len(gotWords) != len(wantWords) {
		t.Errorf("mode=full must preserve every word, want %d got %d", len(wantWords), len(gotWords))
	}
}

// TestRenderReasoningPreviewUsesDimStyle confirms the preview renders
// through styles.Dim (the same grey codeblock.go already uses for a folded
// block's summary), not some other colour — matching the report's own "in
// grey" wording literally rather than inventing a new theme surface.
func TestRenderReasoningPreviewUsesDimStyle(t *testing.T) {
	th := theme.Load("")
	styles := theme.NewStyles(th, theme.CapTruecolor, theme.GlyphsUnicode)

	got := renderReasoningPreview(styles, unicodeGlyphs, "brief thought", "full", false, 40)
	want := styles.Dim.Render("brief thought")
	if got != want {
		t.Errorf("preview does not match styles.Dim.Render output: got %q want %q", got, want)
	}
}

// TestRenderReasoningPreviewFoldedCollapsesToSummary is F8b's own core
// contract (docs/ROADMAP-ux-2026-08-20.md W2 item 5): with folded=true and
// a mode that would otherwise show something ("collapsed" or "full"), the
// preview collapses to reasoningFoldSummary's one-line "thinking, N lines"
// form instead of any of the reasoning text itself — mirroring foldSummary's
// own shape for a folded code block (codeblock.go), so ctrl+r folds both
// the same way.
func TestRenderReasoningPreviewFoldedCollapsesToSummary(t *testing.T) {
	th := theme.Load("")
	styles := theme.NewStyles(th, theme.CapTruecolor, theme.GlyphsUnicode)

	for _, mode := range []string{"collapsed", "full"} {
		reasoning := "line one\nline two\nline three"
		got := renderReasoningPreview(styles, unicodeGlyphs, reasoning, mode, true, 40)
		plain := stripANSI(got)
		if strings.Contains(plain, "line one") || strings.Contains(plain, "line two") || strings.Contains(plain, "line three") {
			t.Errorf("mode=%q folded=true must not leak the raw reasoning text, got %q", mode, plain)
		}
		if !strings.Contains(plain, unicodeGlyphs.foldMark) {
			t.Errorf("mode=%q folded=true must show the fold summary glyph, got %q", mode, plain)
		}
		if !strings.Contains(plain, "3 lines") {
			t.Errorf("mode=%q folded=true must report the line count, got %q", mode, plain)
		}
		if strings.Count(plain, "\n") != 0 {
			t.Errorf("mode=%q folded=true must render as exactly one line, got %q", mode, plain)
		}
	}
}

// TestRenderTranscriptLineSplicesReasoningBetweenHeaderAndBody confirms the
// integration point: renderTranscriptLine, given a non-empty reasoning and
// a mode that shows it, inserts the preview as its own line between the
// role header and the message body — "glued to the response", not
// competing with it or replacing it.
func TestRenderTranscriptLineSplicesReasoningBetweenHeaderAndBody(t *testing.T) {
	th := theme.Load("")
	styles := theme.NewStyles(th, theme.CapTruecolor, theme.GlyphsUnicode)
	ts := time.Date(2026, 8, 19, 14, 2, 0, 0, time.UTC)

	out := renderTranscriptLine(styles, unicodeGlyphs, 40, "assistant", "openai/gpt-5", "la respuesta final", ts, false, false, false, "pensando en la pregunta", "collapsed")
	plain := stripANSI(out)
	lines := strings.Split(plain, "\n")
	if len(lines) != 3 {
		t.Fatalf("expected header + reasoning + body = 3 lines, got %d: %q", len(lines), plain)
	}
	if !strings.Contains(lines[1], "pensando en la pregunta") {
		t.Errorf("second line must be the reasoning preview, got %q", lines[1])
	}
	if lines[2] != "la respuesta final" {
		t.Errorf("third line must be the untouched answer body, got %q", lines[2])
	}
}

// TestRenderTranscriptLineFoldedCollapsesReasoningToo is F8b's own
// integration proof at renderTranscriptLine's own call boundary (chat.go):
// folded=true must reach the reasoning preview exactly the way it already
// reaches renderMessageBody's code blocks, collapsing the reasoning line
// to reasoningFoldSummary's one-line form in the same call that also folds
// any fenced code in the body — "one toggle... together", not two toggles
// that happen to share a keybinding.
func TestRenderTranscriptLineFoldedCollapsesReasoningToo(t *testing.T) {
	th := theme.Load("")
	styles := theme.NewStyles(th, theme.CapTruecolor, theme.GlyphsUnicode)
	ts := time.Date(2026, 8, 19, 14, 2, 0, 0, time.UTC)

	reasoning := "step one\nstep two\nstep three"
	unfolded := renderTranscriptLine(styles, unicodeGlyphs, 40, "assistant", "openai/gpt-5", "la respuesta", ts, false, false, false, reasoning, "collapsed")
	folded := renderTranscriptLine(styles, unicodeGlyphs, 40, "assistant", "openai/gpt-5", "la respuesta", ts, false, false, true, reasoning, "collapsed")

	plainUnfolded := stripANSI(unfolded)
	plainFolded := stripANSI(folded)

	if !strings.Contains(plainUnfolded, "step one") {
		t.Fatalf("unfolded must show the reasoning preview text, got %q", plainUnfolded)
	}
	if strings.Contains(plainFolded, "step one") || strings.Contains(plainFolded, "step two") || strings.Contains(plainFolded, "step three") {
		t.Errorf("folded=true must not leak any reasoning text, got %q", plainFolded)
	}
	if !strings.Contains(plainFolded, unicodeGlyphs.foldMark) {
		t.Errorf("folded=true must show the fold summary glyph for reasoning, got %q", plainFolded)
	}
	if !strings.Contains(plainFolded, "la respuesta") {
		t.Errorf("folded=true must still show the untouched answer body, got %q", plainFolded)
	}
}

// TestRenderTranscriptLineNoReasoningNoExtraLine guards the "add no line at
// all" contract for the common case (empty reasoning, or mode=off): a
// bubble with nothing to preview must render exactly the same
// header+body shape it always has, not a stray blank dim row.
func TestRenderTranscriptLineNoReasoningNoExtraLine(t *testing.T) {
	th := theme.Load("")
	styles := theme.NewStyles(th, theme.CapTruecolor, theme.GlyphsUnicode)
	ts := time.Date(2026, 8, 19, 14, 2, 0, 0, time.UTC)

	withReasoning := renderTranscriptLine(styles, unicodeGlyphs, 40, "assistant", "openai/gpt-5", "hola", ts, false, false, false, "algo", "off")
	withoutReasoning := renderTranscriptLine(styles, unicodeGlyphs, 40, "assistant", "openai/gpt-5", "hola", ts, false, false, false, "", "collapsed")

	if got := strings.Count(stripANSI(withReasoning), "\n"); got != 1 {
		t.Errorf("mode=off must not add a line even with reasoning present, got %d newline(s): %q", got, stripANSI(withReasoning))
	}
	if got := strings.Count(stripANSI(withoutReasoning), "\n"); got != 1 {
		t.Errorf("empty reasoning must not add a line, got %d newline(s): %q", got, stripANSI(withoutReasoning))
	}
}

// TestReasoningModeOrDefaultsToCollapsed pins defaults.toml's own
// "reasoning = \"collapsed\"" for both a nil config (this package's own
// tests, which build a Root without a real *config.Config) and one whose
// UI.Reasoning was never set — mirroring animationsCfg's own "restate the
// default" rule for [ui.animations].
func TestReasoningModeOrDefaultsToCollapsed(t *testing.T) {
	if got := reasoningModeOr(nil); got != "collapsed" {
		t.Errorf("nil config must default to collapsed, got %q", got)
	}
	if got := reasoningModeOr(&config.Config{}); got != "collapsed" {
		t.Errorf("zero-valued UI.Reasoning must default to collapsed, got %q", got)
	}
	cfg := &config.Config{}
	cfg.UI.Reasoning = "full"
	if got := reasoningModeOr(cfg); got != "full" {
		t.Errorf("explicit config value must be honoured, got %q", got)
	}
}

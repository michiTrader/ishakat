// codeblock.go is Fase 3's syntax-highlighting increment (docs/PLAN.md §11,
// "bloques de código resaltados — Chroma entra aquí"), the item chat.go's
// own renderTranscriptLine doc comment has been flagging as deferred since
// Step 3 ("Markdown is still deferred").
//
// This is deliberately narrower than "Markdown rendering": it does not touch
// bold, headers, links or lists — that is markdown.go's job, wired in as a
// separate, later increment (docs/PLAN.md §11, "Markdown/Glamour"). What
// this file adds is the one piece §9.3's own wireframe already draws with a
// fenced code block inside a normal message — the rail (`│`) that has been
// part of the spec since the wireframe was written — and colouring its
// tokens instead of leaving them the plain foreground colour every other
// word in the bubble already had. renderMessageBody is the seam between the
// two: it splits a message into code and prose segments and hands each to
// the renderer that owns it — renderCodeBlock/highlightSource for code,
// renderMarkdown (markdown.go) for prose when enabled.
package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"

	"github.com/MichiTrader/ishakat/internal/theme"
)

// codeSegment is one run of a message's text: either prose to hand to
// wrapText exactly as before, or a fenced code block to hand to
// renderCodeBlock instead.
type codeSegment struct {
	isCode bool
	lang   string
	text   string
}

// splitCodeSegments walks a message body line by line and cuts it wherever a
// fence (a line whose trimmed content starts with three backticks) opens or
// closes a code block. The fence lines themselves are consumed, not kept:
// §9.3's own wireframe never shows the backticks, only the rail and — when
// the fence named one — the language on its own first rail line.
//
// A block left open at the end of the text (no closing fence yet) is still
// returned as a code segment rather than dropped or left as prose. That is
// not a leniency for malformed input: it is what a code fence looks like
// while the model is still streaming it, and renderLiveTurn calls this on
// the same in-progress text every tick — a fence that will close on some
// future delta must already read as code on this one, or every block would
// render as three loose backticks and plain text until the exact character
// that closes it arrives.
func splitCodeSegments(text string) []codeSegment {
	lines := strings.Split(text, "\n")
	var segs []codeSegment
	var buf []string
	inCode := false
	lang := ""

	flush := func() {
		if len(buf) == 0 {
			return
		}
		segs = append(segs, codeSegment{isCode: inCode, lang: lang, text: strings.Join(buf, "\n")})
		buf = nil
	}

	for _, ln := range lines {
		trimmed := strings.TrimSpace(ln)
		if strings.HasPrefix(trimmed, "```") {
			if !inCode {
				flush()
				inCode = true
				lang = strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
			} else {
				flush()
				inCode = false
				lang = ""
			}
			continue
		}
		buf = append(buf, ln)
	}
	flush()
	return segs
}

// renderMessageBody is renderTranscriptLine's own text half, now aware of
// fenced code blocks and, via renderProse, of Markdown prose (markdown.go).
// For a message with no fence at all — the overwhelming majority —
// splitCodeSegments returns exactly one prose segment whose text is the
// original string unchanged (strings.Split then strings.Join with the same
// separator is the identity), so with renderProse false this produces
// byte-identical output to the plain wrapText(text, width) call it
// replaces; the branches below only do anything different once a fence
// actually appears, or renderProse is enabled.
//
// folded is §17's 2026-08-18 "code blocks fill the terminal" fix (part 2):
// with it true, every fenced block found here collapses to renderCodeBlock's
// one-line summary instead of its full body. It is a single flag for the
// whole message rather than a per-block one — see tui.Map.ToggleFold's own
// comment for why the toggle itself (ctrl+r, Root.foldCode) applies to every
// code block still in the live-managed region at once, not to one block
// picked out individually.
func renderMessageBody(styles theme.Styles, g glyphs, text string, width int, highlightCode, renderProse, folded bool) string {
	segs := splitCodeSegments(text)
	if len(segs) == 0 {
		if renderProse {
			return renderMarkdown(styles, text, width)
		}
		return wrapText(text, width)
	}
	parts := make([]string, 0, len(segs))
	for _, seg := range segs {
		if seg.text == "" {
			continue
		}
		if seg.isCode {
			parts = append(parts, renderCodeBlock(styles, g, seg.lang, seg.text, width, highlightCode, folded))
		} else if renderProse {
			parts = append(parts, renderMarkdown(styles, seg.text, width))
		} else {
			parts = append(parts, wrapText(seg.text, width))
		}
	}
	return strings.Join(parts, "\n")
}

// renderCodeBlock draws one fenced block with the left rail §9.3 specifies
// ("Los bloques de código usan riel izquierdo `│` en vez de caja completa: a
// 40 columnas una caja roba 4 columnas útiles... y además el riel deja el
// código copiable de un tirón"). The rail is drawn even when highlightCode is
// false or the theme has no [syntax] colours: it is a formatting choice, not
// a colour one, and a plain-text terminal still benefits from the code being
// visually set off from the prose around it.
//
// The language name, when the fence named one, gets its own rail line ahead
// of the code — exactly the shape §9.3's own example draws ("│ sql" as its
// own row above the query) — rendered dim rather than highlighted, since it
// is metadata about the block and not code inside it.
//
// folded true skips all of the above and draws a single dim summary line
// instead — rail, foldMark, the line count and the language when one was
// named — the fix for the real-session report that pasted code "fills the
// terminal" with nothing to collapse it, the same way other CLI agents fold
// long tool/code output behind a one-line toggle.
func renderCodeBlock(styles theme.Styles, g glyphs, lang, code string, width int, highlightCode, folded bool) string {
	rail := styles.Border.Render(g.barLead) + " "
	inner := width - lipgloss.Width(rail)
	if inner < 1 {
		inner = 1
	}

	if folded {
		return rail + wrapText(styles.Dim.Render(foldSummary(g, lang, code)), inner)
	}

	var out []string
	if lang != "" {
		for _, ln := range strings.Split(wrapText(styles.Dim.Render(lang), inner), "\n") {
			out = append(out, rail+ln)
		}
	}

	var body string
	if highlightCode {
		body = highlightSource(styles, lang, code)
	} else {
		body = styles.Code.Render(code)
	}
	for _, ln := range strings.Split(wrapText(body, inner), "\n") {
		out = append(out, rail+ln)
	}
	return strings.Join(out, "\n")
}

// foldSummary is the plain (unstyled) text of a folded code block's single
// line — split out from renderCodeBlock so the "N lines" pluralisation and
// the "lang, " prefix rule have one place to be tested without also
// exercising the styling/rail machinery around them.
func foldSummary(g glyphs, lang, code string) string {
	n := strings.Count(code, "\n") + 1
	unit := "line"
	if n != 1 {
		unit = "lines"
	}
	if lang != "" {
		return fmt.Sprintf("%s %s, %d %s", g.foldMark, lang, n, unit)
	}
	return fmt.Sprintf("%s %d %s", g.foldMark, n, unit)
}

// highlightSource tokenises code with Chroma's lexer for lang and colours
// each token via syntaxStyleFor. An unrecognised or empty lang (chroma's
// lexers.Get returns nil for both, §6.4's own "no language named" case a
// fence without one always hits) falls back to the plain body colour rather
// than guessing: a wrong guess colours code as if it were a different
// language, which reads as more wrong than no colour at all.
func highlightSource(styles theme.Styles, lang, code string) string {
	lex := lexers.Get(strings.TrimSpace(lang))
	if lex == nil {
		return styles.Code.Render(code)
	}
	it, err := lex.Tokenise(nil, code)
	if err != nil {
		return styles.Code.Render(code)
	}
	var b strings.Builder
	for _, tok := range it.Tokens() {
		if tok.Value == "" {
			continue
		}
		st, ok := syntaxStyleFor(styles, tok.Type)
		if !ok {
			st = styles.Code
		}
		b.WriteString(st.Render(tok.Value))
	}
	return b.String()
}

// syntaxStyleFor maps a Chroma token category onto the theme's own [syntax]
// table (theme.go's Theme.Syntax, parsed since Step 3 but never consumed
// until this file). Four keys only — keyword, string, comment, number — the
// same four ascua.toml already ships, because a theme file that has to name
// every one of Chroma's several hundred token subtypes to look right is a
// theme file nobody will ever write by hand, which is exactly the contract
// §8 promises ("un tema es un archivo de datos"). Everything else, and
// anything under CapNone, falls back to the caller's plain body colour: a
// terminal that gets no colour at all should not get a highlighter that
// tries and fails to find one.
func syntaxStyleFor(styles theme.Styles, t chroma.TokenType) (lipgloss.Style, bool) {
	if styles.Cap == theme.CapNone {
		return lipgloss.Style{}, false
	}
	var key string
	switch {
	case t.InSubCategory(chroma.LiteralString):
		key = "string"
	case t.InSubCategory(chroma.LiteralNumber):
		key = "number"
	case t.InCategory(chroma.Comment):
		key = "comment"
	case t.InCategory(chroma.Keyword):
		key = "keyword"
	default:
		return lipgloss.Style{}, false
	}
	c, ok := styles.Theme.Syntax[key]
	if !ok {
		return lipgloss.Style{}, false
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(c.Hex())), true
}

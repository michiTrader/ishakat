package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/theme"
)

// ansiFG is the truecolor foreground escape lipgloss emits for c — "38;2;r;g;b",
// not the "#rrggbb" RGB.Hex() itself uses — so tests that check a token was
// painted with a given theme colour have to search for this, not for Hex().
func ansiFG(c theme.RGB) string {
	return fmt.Sprintf("38;2;%d;%d;%d", c.R, c.G, c.B)
}

// TestSplitCodeSegmentsNoFenceIsIdentity pins the "the overwhelming
// majority of messages have no fence at all" claim codeblock.go's own
// renderMessageBody comment makes: for plain prose, splitCodeSegments must
// produce exactly one prose segment whose text, once rejoined, is
// byte-identical to the input.
func TestSplitCodeSegmentsNoFenceIsIdentity(t *testing.T) {
	text := "el problema es que el filtro por fecha\nno puede usar el índice existente."
	segs := splitCodeSegments(text)
	if len(segs) != 1 || segs[0].isCode {
		t.Fatalf("want exactly one prose segment, got %+v", segs)
	}
	if segs[0].text != text {
		t.Errorf("prose segment must be byte-identical to the input:\ngot  %q\nwant %q", segs[0].text, text)
	}
}

// TestSplitCodeSegmentsExtractsFencedBlock is §9.3's own worked example: a
// sentence, a fenced ```sql block, and the sentence that follows it.
func TestSplitCodeSegmentsExtractsFencedBlock(t *testing.T) {
	text := "Un índice compuesto:\n```sql\nCREATE INDEX idx_events_user\n  ON events (user_id, created_at);\n```\nCon eso el planner hace index scan"
	segs := splitCodeSegments(text)
	if len(segs) != 3 {
		t.Fatalf("want 3 segments (prose, code, prose), got %d: %+v", len(segs), segs)
	}
	if segs[0].isCode || segs[2].isCode {
		t.Fatalf("segments 0 and 2 must be prose: %+v", segs)
	}
	if !segs[1].isCode {
		t.Fatalf("segment 1 must be code: %+v", segs)
	}
	if segs[1].lang != "sql" {
		t.Errorf("lang = %q, want %q", segs[1].lang, "sql")
	}
	if !strings.Contains(segs[1].text, "CREATE INDEX") {
		t.Errorf("code segment lost its content: %q", segs[1].text)
	}
	if strings.Contains(segs[1].text, "```") {
		t.Errorf("the fence markers themselves must not survive into the segment: %q", segs[1].text)
	}
}

// TestSplitCodeSegmentsUnclosedFenceStillReadsAsCode is renderLiveTurn's own
// requirement, spelled out in splitCodeSegments' doc comment: a fence still
// streaming in — no closing ``` yet — has to render as code on every tick
// before the one that closes it, not as three loose backticks and plain text.
func TestSplitCodeSegmentsUnclosedFenceStillReadsAsCode(t *testing.T) {
	text := "aquí va el código:\n```go\nfunc main() {\n\tfmt.Println(\"hola\")"
	segs := splitCodeSegments(text)
	if len(segs) != 2 {
		t.Fatalf("want 2 segments (prose, still-open code), got %d: %+v", len(segs), segs)
	}
	if !segs[1].isCode {
		t.Fatalf("the still-open block must already read as code: %+v", segs[1])
	}
	if segs[1].lang != "go" {
		t.Errorf("lang = %q, want %q", segs[1].lang, "go")
	}
	if !strings.Contains(segs[1].text, "fmt.Println") {
		t.Errorf("open block lost its content: %q", segs[1].text)
	}
}

// TestSplitCodeSegmentsNoLanguageNamed covers a fence with nothing after the
// backticks — the common case in practice — which must still split cleanly
// with lang == "".
func TestSplitCodeSegmentsNoLanguageNamed(t *testing.T) {
	text := "```\nplain fenced text\n```"
	segs := splitCodeSegments(text)
	if len(segs) != 1 || !segs[0].isCode {
		t.Fatalf("want exactly one code segment, got %+v", segs)
	}
	if segs[0].lang != "" {
		t.Errorf("lang = %q, want empty", segs[0].lang)
	}
}

// TestRenderCodeBlockDrawsTheRail pins §9.3's own layout decision, quoted in
// codeblock.go's doc comment: "Los bloques de código usan riel izquierdo │
// en vez de caja completa". Every non-empty line of the block, including the
// language line when one is named, must start with the rail glyph.
func TestRenderCodeBlockDrawsTheRail(t *testing.T) {
	th := theme.Load("ascua")
	styles := theme.NewStyles(th, theme.CapNone, theme.GlyphsUnicode)
	g := unicodeGlyphs

	out := renderCodeBlock(styles, g, "sql", "SELECT 1;\nSELECT 2;", 40, false)
	lines := strings.Split(out, "\n")
	if len(lines) < 3 {
		t.Fatalf("want at least 3 rail lines (lang + 2 code lines), got %d: %q", len(lines), out)
	}
	for i, ln := range lines {
		if !strings.HasPrefix(ln, g.barLead) {
			t.Errorf("line %d does not start with the rail %q: %q", i, g.barLead, ln)
		}
	}
	if !strings.Contains(lines[0], "sql") {
		t.Errorf("first line should carry the language name, got %q", lines[0])
	}
}

// TestRenderCodeBlockNoLanguageHasNoLangLine is the ASCII/no-language
// counterpart: with lang == "" there must be no leading metadata row, only
// the code's own rail lines.
func TestRenderCodeBlockNoLanguageHasNoLangLine(t *testing.T) {
	th := theme.Load("ascua")
	styles := theme.NewStyles(th, theme.CapNone, theme.GlyphsUnicode)
	g := unicodeGlyphs

	out := renderCodeBlock(styles, g, "", "one line only", 40, false)
	lines := strings.Split(out, "\n")
	if len(lines) != 1 {
		t.Fatalf("want exactly one rail line for one line of code with no language, got %d: %q", len(lines), out)
	}
	if !strings.Contains(lines[0], "one line only") {
		t.Errorf("code content missing: %q", lines[0])
	}
}

// TestHighlightSourceColoursKnownTokenKinds is the actual Chroma wiring: with
// a recognised language and a theme that has [syntax] colours, the four
// keys ascua.toml ships (keyword/string/comment/number) must each produce
// an ANSI-coloured run somewhere in the output — proof the token stream
// reached syntaxStyleFor and did not just fall back to the plain body style
// for everything.
func TestHighlightSourceColoursKnownTokenKinds(t *testing.T) {
	th := theme.Load("ascua")
	styles := theme.NewStyles(th, theme.CapTruecolor, theme.GlyphsUnicode)

	code := "// a comment\nfunc main() {\n\tx := 42\n\ts := \"hola\"\n\t_ = x\n\t_ = s\n}\n"
	out := highlightSource(styles, "go", code)

	if !strings.Contains(out, ansiFG(th.Syntax["comment"])) {
		t.Errorf("comment token was not painted with theme.Syntax[comment] (%s): %q", th.Syntax["comment"].Hex(), out)
	}
	if !strings.Contains(out, ansiFG(th.Syntax["keyword"])) {
		t.Errorf("keyword token was not painted with theme.Syntax[keyword] (%s): %q", th.Syntax["keyword"].Hex(), out)
	}
	if !strings.Contains(out, ansiFG(th.Syntax["string"])) {
		t.Errorf("string token was not painted with theme.Syntax[string] (%s): %q", th.Syntax["string"].Hex(), out)
	}
	if !strings.Contains(out, ansiFG(th.Syntax["number"])) {
		t.Errorf("number token was not painted with theme.Syntax[number] (%s): %q", th.Syntax["number"].Hex(), out)
	}
	if !strings.Contains(stripANSI(out), "func main") {
		t.Errorf("the code's own text must still be present once escapes are stripped: %q", stripANSI(out))
	}
}

// TestHighlightSourceUnknownLanguageFallsBackPlain is highlightSource's own
// documented behaviour for an unrecognised or absent language: no guessing,
// the plain body colour, text unchanged.
func TestHighlightSourceUnknownLanguageFallsBackPlain(t *testing.T) {
	th := theme.Load("ascua")
	styles := theme.NewStyles(th, theme.CapTruecolor, theme.GlyphsUnicode)
	code := "algo que no es ningún lenguaje reconocido"

	for _, lang := range []string{"", "unlenguajequenoexiste12345"} {
		out := highlightSource(styles, lang, code)
		if stripANSI(out) != code {
			t.Errorf("lang %q: text must survive unchanged, got %q", lang, stripANSI(out))
		}
	}
}

// TestSyntaxStyleForCapNoneNeverColours is the terminal-capability guard:
// even with a recognised token kind and a theme that has [syntax] colours,
// CapNone must never emit an escape sequence — the same rule
// TestStylesSinColorNoEmiteEscapes already pins for the rest of the styles
// (internal/theme/theme_test.go), now checked for the syntax table too.
func TestSyntaxStyleForCapNoneNeverColours(t *testing.T) {
	th := theme.Load("ascua")
	styles := theme.NewStyles(th, theme.CapNone, theme.GlyphsUnicode)
	code := "func main() {}"
	out := highlightSource(styles, "go", code)
	if strings.Contains(out, "\x1b[") {
		t.Errorf("CapNone must not emit ANSI escapes: %q", out)
	}
	if out != code {
		t.Errorf("CapNone output should equal the plain input text, got %q", out)
	}
}

// TestRenderMessageBodyWithNoFenceMatchesWrapText is renderMessageBody's own
// documented fast path: for text with no fence at all, it must produce
// exactly what wrapText(text, width) already produced before this file
// existed — the change must be additive, not a behaviour shift for the
// common case.
func TestRenderMessageBodyWithNoFenceMatchesWrapText(t *testing.T) {
	th := theme.Load("ascua")
	styles := theme.NewStyles(th, theme.CapTruecolor, theme.GlyphsUnicode)
	g := unicodeGlyphs
	text := "una respuesta normal sin ningún bloque de código, apenas prosa."

	got := renderMessageBody(styles, g, text, 40, true, false)
	want := wrapText(text, 40)
	if got != want {
		t.Errorf("no-fence path must match wrapText verbatim:\ngot  %q\nwant %q", got, want)
	}
}

// TestRenderMessageBodyHighlightCodeFalseStillDrawsRail checks the config
// gate (ui.syntax = false, Root.cfgSyntax): the rail is a layout decision
// and must still appear, and the code line must carry exactly one colour
// run (the plain body style) rather than the several per-token runs
// highlightSource would produce for the same "func main() {}" — keyword,
// name and punctuation would each get their own escape if the gate had not
// actually turned highlighting off.
func TestRenderMessageBodyHighlightCodeFalseStillDrawsRail(t *testing.T) {
	th := theme.Load("ascua")
	styles := theme.NewStyles(th, theme.CapTruecolor, theme.GlyphsUnicode)
	g := unicodeGlyphs
	text := "código:\n```go\nfunc main() {}\n```\nfin"

	out := renderMessageBody(styles, g, text, 40, false, false)
	if !strings.Contains(out, g.barLead) {
		t.Errorf("the rail must be drawn even with highlighting off: %q", out)
	}
	if got := strings.Count(out, ansiFG(th.Syntax["keyword"])); got != 0 {
		t.Errorf("with highlighting off, the keyword colour must not appear at all (got %d times): %q", got, out)
	}
	if !strings.Contains(stripANSI(out), "func main() {}") {
		t.Errorf("code text must still be present: %q", stripANSI(out))
	}
}

// TestPlayTurnWithFencedCodeRendersRailEndToEnd is the integration check: a
// real Root, through the dummy echo engine (which streams the prompt back
// in echoChunkSize-sized chunks — so a fence necessarily arrives split
// across several ticks, exercising the still-open-fence path along the
// way), ends with the rail on screen and every character sent still
// present once escapes are stripped — the same "not lost, only from the
// screen" property prose_internal_test.go already pins for plain text,
// now checked with a fence in the message.
func TestPlayTurnWithFencedCodeRendersRailEndToEnd(t *testing.T) {
	text := "antes\n```go\nfunc main() {\n\tfmt.Println(\"hola\")\n}\n```\ndespués"

	var m tea.Model = newVisibleRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 60, Height: 30})
	m = playTurn(m, text)

	content := m.View().Content
	if !strings.Contains(content, unicodeGlyphs.barLead) {
		t.Errorf("the rail must appear somewhere in the finished frame: %q", stripANSI(content))
	}
	plain := stripANSI(content)
	for _, want := range []string{"func main()", "fmt.Println", "hola", "antes", "después"} {
		if !strings.Contains(plain, want) {
			t.Errorf("frame is missing %q once escapes are stripped:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "```") {
		t.Errorf("fence markers must not survive onto the screen: %q", plain)
	}
}

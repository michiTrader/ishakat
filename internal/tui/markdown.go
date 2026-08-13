// markdown.go is Phase 3's remaining prose item (docs/PLAN.md §11, "next:
// Markdown/Glamour"): the bold, headers, links and lists codeblock.go's own
// doc comment left as "Glamour's job" when the Chroma increment landed.
//
// The split stays exactly where codeblock.go already drew it: fenced code
// blocks keep the §9.3 rail + Chroma path unchanged (renderCodeBlock,
// highlightSource); this file only touches the prose segments
// splitCodeSegments already isolates. Glamour has its own fenced-code
// renderer, but running it over code too would mean answering, twice, the
// one question §6.4's budget note already answered once — "a duplicate
// lipgloss v1 major version" is the cost of adding Glamour at all, adding a
// second highlighting path on top of Chroma's would be paying it again for
// nothing new on screen.
package tui

import (
	"strconv"
	"strings"
	"sync"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"

	"github.com/MichiTrader/ishakat/internal/theme"
)

func zeroUint() *uint          { z := uint(0); return &z }
func emptyStr() *string        { e := ""; return &e }
func mdColor(s string) *string { return &s }
func mdTrue() *bool            { b := true; return &b }

func noBox() ansi.StyleBlock {
	return ansi.StyleBlock{Margin: zeroUint(), Indent: zeroUint(), IndentToken: emptyStr()}
}

func markdownStyleConfig(t theme.Styles) ansi.StyleConfig {
	fg := t.Theme.FG.Hex()
	accent := t.Theme.Accent.Hex()
	code := t.Theme.FG.Hex()
	if c, ok := t.Theme.Syntax["string"]; ok {
		code = c.Hex()
	}

	return ansi.StyleConfig{
		Document:  noBox(),
		Paragraph: noBox(),
		List:      ansi.StyleList{StyleBlock: noBox()},
		Item: ansi.StylePrimitive{
			Color:  mdColor(fg),
			Prefix: "- ",
		},
		Enumeration: ansi.StylePrimitive{
			Color:  mdColor(fg),
			Suffix: ". ",
		},
		Heading: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{Color: mdColor(accent), Bold: mdTrue()},
			Margin:         zeroUint(),
			Indent:         zeroUint(),
			IndentToken:    emptyStr(),
		},
		Text:   ansi.StylePrimitive{Color: mdColor(fg)},
		Strong: ansi.StylePrimitive{Bold: mdTrue()},
		Emph:   ansi.StylePrimitive{Italic: mdTrue()},
		Link: ansi.StylePrimitive{
			Color:     mdColor(accent),
			Underline: mdTrue(),
		},
		LinkText: ansi.StylePrimitive{Color: mdColor(accent)},
		Code:     ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Color: mdColor(code)}},
	}
}

// mdRendererCache memoises the *glamour.TermRenderer built for a given
// (theme colours, terminal capability, width) combination instead of
// constructing one from scratch on every renderMarkdown call.
//
// This exists because renderLiveTurn (chat.go) calls renderMessageBody, and
// therefore renderMarkdown, once per repaint tick while a message is still
// streaming in — a live turn a few seconds long can mean hundreds of calls
// over the same (styles, width) pair before the message finishes. Building
// a fresh glamour.TermRenderer (which itself builds a goldmark parser and
// compiles the style config into an ANSI renderer) on every one of those
// ticks is wasted work the pre-existing wrapText/Chroma paths never had to
// pay, and it is exactly the kind of per-call cost that a slowdown (the
// race detector's instrumentation, a slow CI runner, a low-powered terminal
// session) can turn into a visible stall — go test -race's own
// TestTheLiveTurnWrapsWhileItStreams timed out this way before this cache
// existed.
//
// Keying: two Root values can share the same theme.Styles by construction
// (NewRoot builds it once per /theme or startup), so caching by the actual
// colour/cap/width tuple rather than by width alone still hits on the
// overwhelmingly common case (repeated ticks of the same live turn, same
// terminal size) without ever serving a stale renderer if the user switches
// theme or resizes mid-session — both of those change the key, which simply
// grows the cache by one entry rather than needing an invalidation path.
//
// mu guards the map because Bubble Tea's own Update loop is single
// threaded, but package-level state must not assume that of every caller
// (tests exercise renderMarkdown directly, and a future caller might not);
// a mutex here is cheap next to the cost it is saving.
var (
	mdRendererCacheMu sync.Mutex
	mdRendererCache   = map[string]*glamour.TermRenderer{}
)

func mdCacheKey(cfg ansi.StyleConfig, width int) string {
	// The colours actually present in cfg are exactly what markdownStyleConfig
	// derived from styles.Theme (fg/accent/code) plus the CapNone empty-config
	// case (cfg is the ansi.StyleConfig{} zero value then, whose Text.Color is
	// nil) — dereferencing each *string defensively rather than assuming a
	// non-nil pointer keeps this safe across both branches in renderMarkdown.
	var b strings.Builder
	b.WriteString(derefStr(cfg.Text.Color))
	b.WriteByte('|')
	b.WriteString(derefStr(cfg.Heading.StylePrimitive.Color))
	b.WriteByte('|')
	b.WriteString(derefStr(cfg.Code.StylePrimitive.Color))
	b.WriteByte('|')
	b.WriteString(derefStr(cfg.Link.Color))
	b.WriteByte('|')
	b.WriteString(strconv.Itoa(width))
	return b.String()
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// mdRendererFor returns the cached *glamour.TermRenderer for cfg/width,
// building and storing one on first use. Errors from
// glamour.NewTermRenderer are returned rather than cached — a construction
// failure is not expected to be a function of (cfg, width) staying broken
// forever, and caching a nil renderer would need its own sentinel handling
// for no benefit (renderMarkdown's caller already falls back to wrapText on
// any error, cached or not).
func mdRendererFor(cfg ansi.StyleConfig, width int) (*glamour.TermRenderer, error) {
	key := mdCacheKey(cfg, width)

	mdRendererCacheMu.Lock()
	if r, ok := mdRendererCache[key]; ok {
		mdRendererCacheMu.Unlock()
		return r, nil
	}
	mdRendererCacheMu.Unlock()

	r, err := glamour.NewTermRenderer(glamour.WithStyles(cfg), glamour.WithWordWrap(width))
	if err != nil {
		return nil, err
	}

	mdRendererCacheMu.Lock()
	mdRendererCache[key] = r
	mdRendererCacheMu.Unlock()
	return r, nil
}

// hasMarkdownSyntax checks for every byte that can open a Markdown
// construct this project's own markdownStyleConfig actually styles
// (bold/italic via *_, headings via #, links/images via [](), unordered
// lists via -*+, inline code via `). A chunk of plain prose containing none
// of these bytes cannot possibly render any differently through Glamour
// than through wrapText — Glamour's goldmark parser would walk the whole
// text, find nothing to mark up, and hand back the same words. Skipping
// the parse entirely for that (overwhelmingly common, for a chat reply)
// case is not a shortcut that risks missing real Markdown; it is a
// precondition that guarantees there is none to find.
//
// Deliberately not scanning for ordered-list digits (e.g. "1. ") here: a
// cheap byte-set check cannot tell "1. first step" at the start of a line
// from "flight 1. departs at noon" in the middle of a sentence, and digits
// are far too common in ordinary prose (times, counts, versions) for this
// fast path to stay worth having if it had to treat every one as "maybe
// Markdown". Ordered lists opened by a bare "N. " therefore always take
// the Glamour path below regardless of this check failing to catch them —
// this function only needs to be conservative in one direction: it must
// never return false for text that does contain "*_#[]`>-|", not catch
// every possible Markdown construct on its own.
//
// This is what actually keeps a live-streaming turn responsive: chat.go's
// renderLiveTurn re-renders the *whole* accumulated answer on every tick
// (liveTurn.text only grows), so without this check a long plain-text
// answer would re-run Glamour's parser over an ever-larger string once per
// tick even though the text never contains a single Markdown character —
// exactly the shape TestTheLiveTurnWrapsWhileItStreams exercises with a
// message that is nothing but "z" repeated. mdRendererFor's cache (below)
// already removed the renderer-construction cost per call; this removes
// the render-call cost itself for the case that dominates in practice.
func hasMarkdownSyntax(text string) bool {
	return strings.ContainsAny(text, "*_#[]`>-|")
}

func renderMarkdown(styles theme.Styles, text string, width int) string {
	// width <= 0 is wrapText's own "no limit" convention (wrap.go). Glamour
	// has no such convention — WithWordWrap(0) does not mean unlimited, it
	// wraps after zero columns, one character per line — so this is not
	// merely deferring to the same rule below; it has to skip Glamour's own
	// wrapping entirely and fall back before ever constructing a renderer.
	if width <= 0 {
		return wrapText(text, width)
	}
	if !hasMarkdownSyntax(text) {
		return wrapText(text, width)
	}
	cfg := ansi.StyleConfig{}
	if styles.Cap != theme.CapNone {
		cfg = markdownStyleConfig(styles)
	}
	r, err := mdRendererFor(cfg, width)
	if err != nil {
		return wrapText(text, width)
	}
	out, err := r.Render(text)
	if err != nil {
		return wrapText(text, width)
	}
	// Glamour always frames a document with a leading and trailing blank
	// line (its own Document.Margin default, still present even with every
	// element's own margin/indent zeroed above) — trimmed here rather than
	// carried into renderMessageBody's strings.Join.
	//
	// Glamour's own word-wrap (muesli/reflow) only breaks on spaces, unlike
	// this project's wrapText (ansi.Wrap), which also breaks inside an
	// unbreakable run of characters when there is nowhere else to go
	// (wrap.go's own doc comment, pinned by
	// TestALongMessageIsWrappedInsteadOfClipped). Re-wrapping Glamour's
	// output through wrapText recovers that guarantee regardless of which
	// renderer touched the text.
	return wrapText(strings.Trim(out, "\n"), width)
}

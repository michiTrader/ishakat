package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/convo"
	"github.com/MichiTrader/ishakat/internal/theme"
)

// liveTurn es el turno en curso: vive en el modelo mientras genera y se
// vuelca al scrollback en cuanto termina (§7.5). Desde el Paso 8 lo llena
// engine.StreamBuf a través de Root.drainStream, un drenado por tick de
// repintado y no uno por token.
//
// The text field is a plain string on purpose. Bubble Tea's Update takes and
// returns the model by value, so every message copies the whole Root — and a
// strings.Builder cannot survive that: it records the address it was first
// written at and panics with "strings: illegal use of non-zero Builder copied
// by value" on the next write from a copy. That is exactly the crash that
// closed the app while streaming a long answer. Anything stored here must be
// safe to copy; concatenation is O(n²) in theory but a turn is a few kilobytes
// and correctness beats a micro-optimisation that takes the process down.
type liveTurn struct {
	active bool
	model  string
	text   string

	// reason is the reasoning stream, kept apart from text because §4 gives
	// it its own block kind: merging the two here would force finishTurn to
	// split them again, and there is no reliable place to cut once they have
	// been concatenated.
	reason string

	startedAt time.Time
	tokens    int

	// usage is the provider's own accounting once it arrives (EventUsage, or
	// EventDone). nil until then, which is why tokens still exists: the footer
	// needs a number that moves from the first delta, long before any
	// provider says anything about tokens.
	usage *convo.Usage

	aborted bool
}

func (t *liveTurn) start(model string) {
	t.active = true
	t.model = model
	t.text = ""
	t.reason = ""
	t.startedAt = time.Now()
	t.tokens = 0
	t.usage = nil
	t.aborted = false
}

func (t *liveTurn) append(delta string) {
	t.text += delta
	// Estimación gruesa nada más para que el footer tenga un número que
	// avance mientras genera; el conteo real llega con convo.Usage y lo
	// corrige liveTurn.tokenCount.
	t.tokens += len(strings.Fields(delta))
}

// appendReasoning is append's sibling for EventReasoning deltas. It does not
// touch tokens: the reasoning stream is not what the user is reading, and
// counting it into the same running total would make the footer's number
// jump for text that is not on screen.
func (t *liveTurn) appendReasoning(delta string) { t.reason += delta }

// reasoning is the reasoning accumulated so far, mirroring body().
func (t liveTurn) reasoning() string { return t.reason }

// tokenCount is what the footer should show: the provider's own number once
// it has sent one, and the rough word count until then. A provider that
// reports usage mid-stream therefore replaces the estimate as soon as it can,
// instead of the two disagreeing for the rest of the turn.
func (t liveTurn) tokenCount() int {
	if t.usage != nil {
		return t.usage.Out
	}
	return t.tokens
}

// body is the text accumulated so far. It exists as a method so callers never
// touch the field directly and the storage can change later (a []byte rope,
// engine.StreamBuf in Step 8) without a single call site moving.
func (t liveTurn) body() string { return t.text }

func (t liveTurn) elapsed() time.Duration {
	if t.startedAt.IsZero() {
		return 0
	}
	return time.Since(t.startedAt)
}

// renderTranscriptLine arma una burbuja de conversación como en §9.3: marcador
// de rol, nombre y hora, seguido del texto.
//
// width is the number of columns the text may use, and passing it is not
// optional. This used to write text verbatim ("el markdown/wrap llega en una
// fase posterior, fuera del alcance del Paso 3") on the theory that wrapping
// was a cosmetic step for later — but Bubble Tea's inline renderer clips an
// overlong row instead of wrapping it, so without this a message longer than
// the terminal showed only its first row, with the rest gone from the screen
// (not the model — from the screen, which reads as a truncated answer rather
// than an error).
//
// Fenced code blocks are no longer deferred either (codeblock.go, Fase 3):
// the body is handed to renderMessageBody, which draws §9.3's own rail for
// any ``` fence it finds and, when highlightCode is true, colours the code
// inside it with Chroma. Prose outside a fence — bold, headers, links,
// lists — goes through Glamour (markdown.go) when renderProse is true.
//
// folded (§17 2026-08-18, part 2) is passed straight through to
// renderMessageBody: every fenced block in this one message collapses to its
// one-line summary when true. See tui.Map.ToggleFold's own comment for why
// this is a whole-message flag rather than a per-block one.
//
// The header is coloured with styles.User/styles.Assistant (§17 2026-08-19
// "user/assistant messages are not visually differentiated" fix): both
// fields have existed on theme.Styles since Step 3 (theme/style.go), each
// theme's TOML has always defined distinct `user`/`assistant` colours
// (ascua.toml's own `#7fd1b9`/`#ffb454`), but nothing in this package ever
// called .Render with either one — every bubble's header drew in the plain
// foreground colour regardless of who sent it, which is the entire reported
// defect: at a glance, a fast-scrolling transcript reads as one undivided
// column of text with no visual anchor for "which of these did I write".
// Colouring only the header line, not the body, is deliberate: the body is
// where code highlighting (Chroma) and prose styling (Glamour) already
// apply their own colours, and re-tinting it on top would fight both
// rather than complement either — the header alone is enough to place each
// bubble's role at a glance, the same way a chat client colours the sender
// name and lets the message text render in the app's normal reading colour.
//
// reasoning/reasoningMode (§17 point 6a, "show at least ~2 lines of
// thinking glued to the response, in grey") add a dim preview between the
// header and the body for an assistant bubble whose turn carried a
// reasoning stream. reasoning is always whatever the caller recorded
// (transcriptEntry.reasoning, never touched by mode); reasoningMode is
// ui.reasoning's three values ("off"/"collapsed"/"full") and is what
// actually decides whether/how much prints — see renderReasoningPreview's
// own doc comment for the exact truncation rule. A user bubble is passed
// an empty reasoning by every call site (only an assistant turn ever
// produces one), so this is a no-op there regardless of mode.
//
// folded also now gates the reasoning preview, not just renderMessageBody's
// code blocks (F8b, docs/ROADMAP-ux-2026-08-20.md W2 item 5, replacing the
// old "ctrl+r folds code only" behaviour with "one toggle that folds/
// unfolds reasoning and code together"). This is deliberately *not* a
// second parameter or a new Root field: F8b's own text in the roadmap's
// §5 "deliberately not in any wave" ("F8b extends *what* it folds, not
// *how much state* it keeps") is read literally here — Root.foldCode
// stays the single global bool it already was, and this function just
// widens what that one bool reaches into. reasoningFoldSummary (below)
// mirrors foldSummary's (codeblock.go) own one-line, dim, "say something
// was folded" shape, so a folded bubble reads consistently whether the
// thing collapsed was code or thinking. mode=="off" stays authoritative
// either way: folding never *reveals* a reasoning stream the config says
// to hide, it only ever collapses one that would otherwise show.
func renderTranscriptLine(styles theme.Styles, g glyphs, width int, role, name, text string, ts time.Time, highlightCode, renderProse, folded bool, reasoning, reasoningMode string) string {
	marker := g.userMark
	roleStyle := styles.User
	if role == "assistant" {
		marker = g.assistantMark
		roleStyle = styles.Assistant
	}
	header := roleStyle.Render(fmt.Sprintf("%s %s %s", marker, name, ts.Format("15:04")))
	body := wrapText(header, width)
	if preview := renderReasoningPreview(styles, g, reasoning, reasoningMode, folded, width); preview != "" {
		body += "\n" + preview
	}
	body += "\n" + renderMessageBody(styles, g, text, width, highlightCode, renderProse, folded)
	if role == "user" {
		// §17 2026-08-19 second half: user messages get a distinct
		// *background*, not just the header foreground fixed above —
		// applied to the whole rendered bubble (header + body) via
		// PaintBackground so it survives the header's own User colour and
		// any code/prose styling in the body without losing the paint at
		// their embedded resets (see PaintBackground's doc comment). width
		// is passed through (2026-08-27 fix) so every line's background
		// reaches the same right edge instead of stopping right after that
		// line's own last visible glyph — a full-width band behind the
		// whole bubble, not a highlight confined to the letters.
		body = styles.PaintBackground(body, width)
	}
	return body
}

// reasoningModeOr resolves ui.reasoning (config/schema.go's UI.Reasoning)
// against defaults.toml's own documented default, the same "restate the
// default rather than trust the zero value" rule animationsCfg (anim.go)
// already follows for [ui.animations] — cfg == nil happens in this
// package's own tests, which build a Root without a real *config.Config at
// all, and a zero-valued string there would silently mean "off" instead of
// the "collapsed" defaults.toml actually promises.
func reasoningModeOr(cfg *config.Config) string {
	if cfg == nil || cfg.UI.Reasoning == "" {
		return "collapsed"
	}
	return cfg.UI.Reasoning
}

// steeringModeOr and followupModeOr mirror reasoningModeOr's own "cfg-or-
// documented-default" shape for the two W2 item 4 config keys (F13,
// docs/ROADMAP-ux-2026-08-20.md, DECISION-2 consequence 3): ui.steering_mode
// and ui.followup_mode, defaults.toml's own "one-at-a-time" for both.
// validateSteering (internal/config/validate.go) already rejects any value
// that is neither "one-at-a-time" nor "batch" before a *config.Config ever
// reaches this package, but cfg == nil (this package's own tests building a
// Root with no real *config.Config, same as reasoningModeOr's own comment)
// and a Config built by hand rather than through Load — bypassing that
// validation — both still need a safe fallback here.
func steeringModeOr(cfg *config.Config) string {
	if cfg == nil || cfg.UI.SteeringMode == "" {
		return "one-at-a-time"
	}
	return cfg.UI.SteeringMode
}

func followupModeOr(cfg *config.Config) string {
	if cfg == nil || cfg.UI.FollowupMode == "" {
		return "one-at-a-time"
	}
	return cfg.UI.FollowupMode
}

// reasoningPreviewLines is "~2 lines" from the report, taken literally: the
// point was a short glance at what the model was doing, not a second reading
// pane competing with the answer for the screen's own limited height (§2).
const reasoningPreviewLines = 2

// renderReasoningPreview turns a turn's raw reasoning text into the "glued to
// the response, in grey" preview §17 point 6a asks for, or "" when there is
// nothing to show — which renderTranscriptLine treats as "add no line at
// all" rather than an empty dim row, the same "no pointless escape pair"
// discipline PaintBackground's own blank-line handling already follows.
//
// mode is ui.reasoning verbatim, not a bool: three real behaviours, not two.
//   - "off" (or unset/anything unrecognised — the safe default matching
//     defaults.toml's own pre-Step-33 behaviour of showing nothing) returns
//     "" unconditionally, regardless of how much reasoning is available, and
//     regardless of folded — see folded's own paragraph below.
//   - "collapsed" (defaults.toml's own default) truncates to
//     reasoningPreviewLines lines and, when more remains, appends an
//     ellipsis line rather than silently cutting a sentence in half — the
//     same "say that something was dropped" discipline truncateOutput
//     (agentloop.go) and foldSummary (codeblock.go) both already follow for
//     their own truncations.
//   - "full" prints the whole stream, unclipped — for a user who explicitly
//     wants to read everything the model was doing, not just a taste of it.
//
// folded is F8b's own addition (docs/ROADMAP-ux-2026-08-20.md W2 item 5,
// Root.foldCode/ctrl+r — see renderTranscriptLine's own doc comment for why
// this reuses that single existing bool rather than adding new state): when
// true and mode is "collapsed" or "full" (i.e. there would be something to
// show), the preview collapses to reasoningFoldSummary's one-line form
// instead — mirroring exactly what folded already does to a fenced code
// block via foldSummary (codeblock.go). mode == "off" is unaffected by
// folded in either direction: a hidden reasoning stream neither gains a
// summary line (there is nothing folded to announce) nor gets revealed by
// toggling fold — ctrl+r changes how much of what would already show is
// visible, never whether ui.reasoning's own "off" is honoured.
//
// The whole preview renders through styles.Dim — the same grey codeblock.go
// already uses for a folded block's one-line summary and a fenced block's
// language tag — wrapped to width first so a long reasoning line does not
// escape the terminal the same way an unwrapped answer used to (wrap.go's
// own doc comment). Wrapping happens before truncation, not after: cutting
// at "N wrapped rows" is what "~2 lines" on screen actually means, whereas
// cutting at "N sentences/paragraphs of raw text" could still overflow the
// terminal on a narrow window.
func renderReasoningPreview(styles theme.Styles, g glyphs, reasoning, mode string, folded bool, width int) string {
	reasoning = strings.TrimSpace(reasoning)
	if reasoning == "" {
		return ""
	}
	switch mode {
	case "full":
		if folded {
			return styles.Dim.Render(reasoningFoldSummary(g, reasoning))
		}
		return styles.Dim.Render(wrapText(reasoning, width))
	case "collapsed":
		if folded {
			return styles.Dim.Render(reasoningFoldSummary(g, reasoning))
		}
		wrapped := wrapText(reasoning, width)
		lines := strings.Split(wrapped, "\n")
		if len(lines) <= reasoningPreviewLines {
			return styles.Dim.Render(wrapped)
		}
		lines = lines[:reasoningPreviewLines]
		lines = append(lines, "…")
		return styles.Dim.Render(strings.Join(lines, "\n"))
	default:
		// "off", empty, or an unrecognised value: show nothing. This is the
		// same "an unrecognised override is not an error, it degrades to
		// the safe default" rule theme.overrideCapability already follows
		// for [ui] color, applied here to [ui] reasoning — unaffected by
		// folded, per this function's own doc comment above.
		return ""
	}
}

// reasoningFoldSummary is renderReasoningPreview's own folded shape,
// mirroring foldSummary's (codeblock.go) "say something was collapsed, and
// how much" rule for a folded fenced block, applied to a folded reasoning
// stream instead. It counts wrapped-free raw lines (strings.Count on "\n"),
// not reasoningPreviewLines' own post-wrap row count, on purpose: the two
// numbers answer different questions — foldSummary's own "N lines" already
// counts a code block's real newlines the same way, and reusing that exact
// convention here is what makes a folded reasoning summary and a folded
// code summary read as the same kind of thing side by side, not two
// different truncation dialects in one transcript.
func reasoningFoldSummary(g glyphs, reasoning string) string {
	n := strings.Count(reasoning, "\n") + 1
	unit := "line"
	if n != 1 {
		unit = "lines"
	}
	return fmt.Sprintf("%s thinking, %d %s", g.foldMark, n, unit)
}

// renderLiveTurn dibuja el turno vivo con el cursor de streaming al final
// (§9.3) y, si está en curso, la línea de animación con tiempo/tokens.
//
// reasoningMode is ui.reasoning (Root.cfgReasoning) threaded through to the
// same renderTranscriptLine call a finished bubble goes through, so a
// reasoning preview appears *while* the turn is still streaming (t.reason,
// fed live by drainStream's appendReasoning call) instead of only popping
// into existence the instant the turn commits — the model is "thinking out
// loud" from the caller's point of view the whole time, not just at the end.
func renderLiveTurn(styles theme.Styles, g glyphs, width int, t liveTurn, crush string, plainCancelHint string, highlightCode, renderProse, folded bool, reasoningMode string) string {
	if !t.active {
		return ""
	}
	var b strings.Builder
	b.WriteString(renderTranscriptLine(styles, g, width, "assistant", t.model, t.body()+g.streamCursor, t.startedAt, highlightCode, renderProse, folded, t.reasoning(), reasoningMode))
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("%s pensando %.1fs %s %d tok\n", crush, t.elapsed().Seconds(), g.dot, t.tokenCount()))
	b.WriteString(plainCancelHint)
	return b.String()
}

// commitEntryCmd hands a finished transcript entry to the real terminal via
// tea.Println (§7.5) instead of leaving it for the live region to keep
// redrawing forever. tea.Println's own output is, per its doc comment,
// "unmanaged by the program and will persist across renders" — exactly the
// permanent-scrollback behaviour a finished message needs once nothing about
// it can change again.
//
// This exists because redrawing the *entire* history inline, every frame, is
// what let the live-managed region grow taller than the terminal. Bubble
// Tea's inline renderer tracks "how many rows did I draw last frame" to move
// the cursor back up before repainting; once that number exceeds the
// terminal's own height the terminal has already scrolled some of those rows
// out from under it, so the bookkeeping and the real cursor position part
// ways and drift a row further apart on every subsequent frame. That is the
// "el cursor se pasa del todo abajo" report: once enough turns accumulated to
// fill the screen, the input box kept sliding down and off it.
//
// The trailing "\n" is not tea.Println's own line break (it supplies that):
// it is the blank separator line the old inline loop in head() used to leave
// between bubbles, kept here so scrollback looks the same as it always did.
//
// folded is whatever Root.foldCode held at the moment this entry left the
// live-managed region — real terminal scrollback (§7.5, this function's own
// comment above) cannot be redrawn afterwards, so a block committed here
// keeps whichever fold state it had at eviction time for good. That is a
// real, documented limitation, not an oversight: ctrl+r only ever reaches
// the last keepInline entries still redrawn by head() (root.go).
func commitEntryCmd(styles theme.Styles, g glyphs, width int, e transcriptEntry, highlightCode, renderProse, folded bool, reasoningMode string) tea.Cmd {
	return tea.Println(renderTranscriptLine(styles, g, width, e.role, e.name, e.text, e.ts, highlightCode, renderProse, folded, e.reasoning, reasoningMode) + "\n")
}

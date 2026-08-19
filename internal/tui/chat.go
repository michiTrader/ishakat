package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

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
func renderTranscriptLine(styles theme.Styles, g glyphs, width int, role, name, text string, ts time.Time, highlightCode, renderProse, folded bool) string {
	marker := g.userMark
	roleStyle := styles.User
	if role == "assistant" {
		marker = g.assistantMark
		roleStyle = styles.Assistant
	}
	header := roleStyle.Render(fmt.Sprintf("%s %s %s", marker, name, ts.Format("15:04")))
	body := wrapText(header, width) + "\n" + renderMessageBody(styles, g, text, width, highlightCode, renderProse, folded)
	if role == "user" {
		// §17 2026-08-19 second half: user messages get a distinct
		// *background*, not just the header foreground fixed above —
		// applied to the whole rendered bubble (header + body) via
		// PaintBackground so it survives the header's own User colour and
		// any code/prose styling in the body without losing the paint at
		// their embedded resets (see PaintBackground's doc comment).
		body = styles.PaintBackground(body)
	}
	return body
}

// renderLiveTurn dibuja el turno vivo con el cursor de streaming al final
// (§9.3) y, si está en curso, la línea de animación con tiempo/tokens.
func renderLiveTurn(styles theme.Styles, g glyphs, width int, t liveTurn, crush string, plainCancelHint string, highlightCode, renderProse, folded bool) string {
	if !t.active {
		return ""
	}
	var b strings.Builder
	b.WriteString(renderTranscriptLine(styles, g, width, "assistant", t.model, t.body()+g.streamCursor, t.startedAt, highlightCode, renderProse, folded))
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
func commitEntryCmd(styles theme.Styles, g glyphs, width int, e transcriptEntry, highlightCode, renderProse, folded bool) tea.Cmd {
	return tea.Println(renderTranscriptLine(styles, g, width, e.role, e.name, e.text, e.ts, highlightCode, renderProse, folded) + "\n")
}

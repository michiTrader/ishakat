package tui

import (
	"fmt"
	"strings"
	"time"
)

// liveTurn es el turno en curso: vive en el modelo mientras genera y se
// vuelca al scrollback en cuanto termina (§7.5). En el Paso 3 no hay
// streaming real: el eco del maniquí llena el texto de a poco, pero la forma
// es la misma que usará el engine en el Paso 8.
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
	active    bool
	model     string
	text      string
	startedAt time.Time
	tokens    int
	aborted   bool
}

func (t *liveTurn) start(model string) {
	t.active = true
	t.model = model
	t.text = ""
	t.startedAt = time.Now()
	t.tokens = 0
	t.aborted = false
}

func (t *liveTurn) append(delta string) {
	t.text += delta
	// Estimación gruesa nada más para que el footer tenga un número que
	// avance durante la demo; el conteo real llega con provider.Usage.
	t.tokens += len(strings.Fields(delta))
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
// than an error). Markdown is still deferred; making every character the
// user sent and the model answered actually visible is not a later step.
func renderTranscriptLine(g glyphs, width int, role, name, text string, ts time.Time) string {
	marker := g.userMark
	if role == "assistant" {
		marker = g.assistantMark
	}
	header := fmt.Sprintf("%s %s %s", marker, name, ts.Format("15:04"))
	return wrapText(header, width) + "\n" + wrapText(text, width)
}

// renderLiveTurn dibuja el turno vivo con el cursor de streaming al final
// (§9.3) y, si está en curso, la línea de animación con tiempo/tokens.
func renderLiveTurn(g glyphs, width int, t liveTurn, crush string, plainCancelHint string) string {
	if !t.active {
		return ""
	}
	var b strings.Builder
	b.WriteString(renderTranscriptLine(g, width, "assistant", t.model, t.body()+g.streamCursor, t.startedAt))
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("%s pensando %.1fs %s %d tok\n", crush, t.elapsed().Seconds(), g.dot, t.tokens))
	b.WriteString(plainCancelHint)
	return b.String()
}

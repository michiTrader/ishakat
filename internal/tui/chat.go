package tui

import (
	"fmt"
	"strings"
	"time"
)

// liveTurn es el turno en curso: vive en el modelo mientras genera y se
// vuelca al scrollback en cuanto termina (§7.5). En el Paso 3 no hay
// streaming real: el eco del maniquí llena Text de a poco, pero la forma es
// la misma que usará el engine en el Paso 8.
type liveTurn struct {
	active    bool
	model     string
	text      strings.Builder
	startedAt time.Time
	tokens    int
	aborted   bool
}

func (t *liveTurn) start(model string) {
	t.active = true
	t.model = model
	t.text.Reset()
	t.startedAt = time.Now()
	t.tokens = 0
	t.aborted = false
}

func (t *liveTurn) append(delta string) {
	t.text.WriteString(delta)
	// Estimación gruesa nada más para que el footer tenga un número que
	// avance durante la demo; el conteo real llega con provider.Usage.
	t.tokens += len(strings.Fields(delta))
}

func (t *liveTurn) elapsed() time.Duration {
	if t.startedAt.IsZero() {
		return 0
	}
	return time.Since(t.startedAt)
}

// renderTranscriptLine arma una burbuja de conversación como en §9.3: marcador
// de rol, nombre y hora, seguido del texto tal cual (el markdown/wrap llega en
// una fase posterior, fuera del alcance del Paso 3).
func renderTranscriptLine(role, name, text string, ts time.Time) string {
	marker := "▌"
	if role == "assistant" {
		marker = "◆"
	}
	header := fmt.Sprintf("%s %s%s%s", marker, name, strings.Repeat(" ", 1), ts.Format("15:04"))
	return header + "\n" + text
}

// renderLiveTurn dibuja el turno vivo con el cursor de streaming "▊" al final
// (§9.3) y, si está en curso, la línea de animación con tiempo/tokens.
func renderLiveTurn(t liveTurn, crush string, plainCancelHint string) string {
	if !t.active {
		return ""
	}
	var b strings.Builder
	b.WriteString(renderTranscriptLine("assistant", t.model, t.text.String()+"▊", t.startedAt))
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("%s pensando %.1fs · %d tok\n", crush, t.elapsed().Seconds(), t.tokens))
	b.WriteString(plainCancelHint)
	return b.String()
}

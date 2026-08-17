package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// FooterState es la información cruda que el footer necesita para dibujarse
// (§9.3, §9.6). No tiene ningún dato de proveedor: en el Paso 3 esto lo
// rellena un maniquí en root.go; el engine real lo reemplaza en el Paso 8.
type FooterState struct {
	Model      string  // ya recortado por quien lo produce si hace falta
	ContextPct float64 // 0..1
	Tokens     int
	CostUSD    float64
	GitBranch  string
	CWD        string

	// Autonomy is §21.4 layer 3's own persistent value ("auto", "agile" or
	// "readonly"), rendered in the same lowercase form
	// permissions.Autonomy.String() already produces — this package never
	// imports internal/permissions itself (§6.1: tui stays ignorant of
	// what a tier or an autonomy *is*, only that it is a string to draw),
	// so Root's own caller (internal/app) is what calls .String() before
	// this field is ever set. Empty is the supported "not wired" value —
	// every test in this package, and any Root built before Step 30's own
	// app.go wiring landed — which is why "autonomy" (below) draws nothing
	// rather than a bare "" the way "git"/"cwd" already treat their own
	// empty string.
	//
	// §21.1's full mockup pairs this with a transient phase word
	// ("auto·exec", "auto·wait 22s") on the same line — see Phase below,
	// Step 32's own closing addition.
	Autonomy string

	// Phase is §21.1's other axis: transient, loop-owned, one of
	// "exec"/"ask"/"wait <duration>" today (see agentturn.go's
	// startAgentTurn/openToolApprove/openAskUser/applyPhaseWait for who
	// sets each). "plan"/"check" are deliberately not produced anywhere
	// yet: §21.14's own Step 31 closing note says explicit `/plan` and the
	// full §21.11 fan-out display remain unbuilt, so there is no real
	// plan/exec split in the running loop for either to name honestly.
	// Inventing one here would be a label with nothing underneath it,
	// worse than omitting it. Empty is the supported "no turn running"
	// value (ModeChat, and every test in this package): "autonomy" below
	// draws the bare Autonomy word with no trailing dot when Phase is
	// empty, the same "empty is invisible" rule Autonomy itself follows
	// for "not wired".
	Phase string
}

// footerItemOrder son las claves válidas de ui.footer.items, en el mismo
// orden que documenta §9.3: el footer recorta de derecha a izquierda según
// esta lista cuando no entra completo. "autonomy" sits right after "model"
// — the two identity-ish items a human glances at together ("which model,
// how much can it decide alone") — rather than at either end, which would
// bury it behind context/tokens/cost in a narrow terminal's right-to-left
// drop order.
var footerItemOrder = []string{"model", "autonomy", "context", "tokens", "cost", "git", "cwd"}

// RenderFooter arma el footer según el breakpoint. En BPMinimo es una sola
// línea recortada al ancho; en el resto, el número de secciones que agregue
// lay.FooterSections() (reservado para la Fase 2 completa: aquí construimos
// la línea única y la truncamos si hace falta).
func RenderFooter(lay Layout, st FooterState, items []string) string {
	if len(items) == 0 {
		items = footerItemOrder
	}
	g := lay.glyphs()
	parts := renderItems(g, st, items)

	line := strings.Join(parts, "  ")
	line = " " + line

	w := lay.ContentWidth()
	// Recorta de derecha a izquierda: se van soltando ítems del final de la
	// lista mientras la línea no entre en el ancho disponible.
	for i := len(items); i > 0 && lipglossWidth(line) > w; i-- {
		parts = renderItems(g, st, items[:i-1])
		line = " " + strings.Join(parts, "  ")
	}
	if lipglossWidth(line) > w && w > 1 {
		r := []rune(line)
		if len(r) > w {
			line = string(r[:w])
		}
	}
	return line
}

func renderItems(g glyphs, st FooterState, items []string) []string {
	var parts []string
	for _, it := range items {
		switch it {
		case "model":
			if st.Model != "" {
				parts = append(parts, g.modelMark+" "+st.Model)
			}
		case "autonomy":
			switch {
			case st.Autonomy != "" && st.Phase != "":
				parts = append(parts, st.Autonomy+g.dot+st.Phase)
			case st.Autonomy != "":
				parts = append(parts, st.Autonomy)
			}
		case "context":
			parts = append(parts, contextBar(g, st.ContextPct))
		case "tokens":
			parts = append(parts, formatTokens(st.Tokens))
		case "cost":
			parts = append(parts, fmt.Sprintf("$%.2f", st.CostUSD))
		case "git":
			if st.GitBranch != "" {
				parts = append(parts, g.gitMark+st.GitBranch)
			}
		case "cwd":
			if st.CWD != "" {
				parts = append(parts, st.CWD)
			}
		}
	}
	return parts
}

// contextBar draws the remaining-context meter, "│▓░ 18%" in the Unicode
// repertoire and "|#. 18%" in ASCII.
//
// The percentage is spelled out next to the bar on purpose: two slots of shading
// can only ever show three states, so the bar is the glance and the number is
// the answer. That is also why the bar losing its shading to ASCII costs
// nothing readable.
func contextBar(g glyphs, pct float64) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 1 {
		pct = 1
	}
	const slots = 2
	filled := int(pct*slots + 0.5)
	bar := strings.Repeat(g.barFull, filled) + strings.Repeat(g.barEmpty, slots-filled)
	return fmt.Sprintf("%s%s %d%%", g.barLead, bar, int(pct*100))
}

func formatTokens(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d tok", n)
	}
	return fmt.Sprintf("%.0fk", float64(n)/1000)
}

// lipglossWidth mide el ancho visible en celdas de terminal, ignorando
// secuencias ANSI: usa la misma medida que lipgloss para que el recorte del
// footer coincida con lo que realmente ocupa en pantalla.
func lipglossWidth(s string) int {
	return lipgloss.Width(s)
}

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
// orden que documenta §9.3. RC-7 usa este mismo orden para dos cosas: es el
// orden en que los ítems se acomodan en las filas que arma RenderFooter, y
// es el orden de "quién se suelta primero" cuando ni envolver en
// lay.FooterSections() filas ni abreviar alcanza — el último ítem de la
// lista es el primero en desaparecer, igual que documentaba la versión
// anterior de este comentario. "autonomy" sits right after "model" — the
// two identity-ish items a human glances at together ("which model, how
// much can it decide alone") — rather than at either end, which would bury
// it behind context/tokens/cost as the drop-from-the-right last resort
// reaches for it.
var footerItemOrder = []string{"model", "autonomy", "context", "tokens", "cost", "git", "cwd"}

// footerMinAbbrevWidth is the narrowest a single footer item is allowed to
// shrink to while other items are still present alongside it. Below this,
// abbreviateItem would be spending its one ellipsis column on a string with
// nothing left to abbreviate ("m…" tells the reader less than dropping
// "model" and giving that column to "cwd" instead), so fitFooterItems
// treats a cap this low as not fitting and lets its caller drop the item
// instead. See fitFooterItems's own minCap for the one exception (the last
// item left has nowhere else to go).
const footerMinAbbrevWidth = 3

// RenderFooter arma el footer con la política de RC-7: envolver primero,
// abreviar después, soltar ítems solo como último recurso — la misma regla
// "el contenido se adapta, la información sobrevive" que aplica el resto de
// §9. Antes de este cambio RenderFooter soltaba ítems de derecha a
// izquierda en el primer ancho angosto que veía, lo cual en una terminal de
// Termux vertical (BPEstrecho, el uso real más común del proyecto, §9.1)
// hacía desaparecer context/tokens/cost/cwd enteros aunque hubiera lugar de
// sobra en una segunda fila.
//
// El algoritmo, en orden de preferencia, para cada subconjunto de items
// (empezando por todos y soltando de derecha a izquierda solo si ningún
// subconjunto mayor entra):
//  1. Renderizar cada ítem tal cual y envolverlos en como máximo
//     lay.FooterSections() filas (1 en BPMinimo, 2 en el resto, §9.1/§9.3).
//  2. Si no entran así, abreviar en conjunto: buscar el tope de ancho por
//     ítem más grande con el que sí entran, y abreviar (con el mismo patrón
//     de path.go: truncateRunes/ShortenPath) solo los ítems que superen ese
//     tope — como un flexbox shrink, todos ceden a la vez y el que sobraba
//     más es el que más cede, en vez de que uno solo absorba todo el
//     recorte o cualquiera lo pierda entero.
//  3. Si ni envuelto ni abreviado entra en las filas disponibles, soltar el
//     último ítem de la lista y repetir desde el paso 1 con el resto.
//
// El resultado es un string con "\n" entre filas cuando hay más de una:
// restRows() (view.go) ya cuenta esos "\n" para el presupuesto de altura de
// RC-3, así que una fila extra aquí no requiere ningún cambio ahí.
func RenderFooter(lay Layout, st FooterState, items []string) string {
	if len(items) == 0 {
		items = footerItemOrder
	}
	g := lay.glyphs()
	w := lay.ContentWidth()
	maxRows := lay.FooterSections()

	for i := len(items); i > 0; i-- {
		keys := items[:i]
		if rows, ok := fitFooterItems(g, st, keys, w, maxRows); ok {
			return strings.Join(rows, "\n")
		}
	}
	// Ni un solo ítem, abreviado al máximo, entra en una fila de este ancho
	// (una terminal de una o dos columnas): el mismo piso de emergencia que
	// ya tenía RenderFooter, un corte duro a exactamente w runas.
	line := " "
	if r := []rune(line); len(r) > w && w > 0 {
		line = string(r[:w])
	}
	return line
}

// footerPart es un ítem del footer ya renderizado junto con la clave que lo
// produjo, para que abbreviateItem sepa qué estrategia de abreviación usar
// (ShortenPath para "cwd", recorte del nombre de rama para "git", genérica
// para el resto) sin tener que volver a parsear el texto ya armado.
type footerPart struct {
	Key  string
	Text string
}

// fitFooterItems renders keys and looks for the widest shared per-item cap
// that still lets the result wrap into at most maxRows rows of at most w
// columns. It starts uncapped (the cap equal to the widest item present, so
// nothing is shrunk) and lowers the cap one column at a time, abbreviating
// (via abbreviateItem) only the items wider than the current cap, until
// either the wrap fits or there is no cap left to try. Because lowering the
// cap can only shrink already-rendered text further, whether the layout
// fits is monotonic in the cap, so the first cap (searched from widest to
// narrowest) that fits is the one that keeps the most information on
// screen — the "abbreviate" step of RC-7's wrap/abbreviate/drop policy.
func fitFooterItems(g glyphs, st FooterState, keys []string, w, maxRows int) ([]string, bool) {
	fps := renderFooterParts(g, st, keys)
	if len(fps) == 0 {
		return []string{" "}, true
	}

	// itemBudget es lo más ancho que puede ser un ítem que ocupe una fila
	// solo, descontando el espacio de margen que footerRowText antepone a
	// cada fila: ningún tope por encima de esto puede servir de nada.
	itemBudget := w - 1
	if itemBudget < 1 {
		itemBudget = 1
	}

	itemCap := 0
	for _, fp := range fps {
		if wd := lipglossWidth(fp.Text); wd > itemCap {
			itemCap = wd
		}
	}
	if itemCap > itemBudget {
		itemCap = itemBudget
	}

	// minCap is how far a single item is allowed to shrink before
	// abbreviateItem stops producing something worth reading (truncateRunes
	// already spends one whole column on the ellipsis, so 3 is the least
	// that can show a hint of the original text plus that ellipsis). Below
	// it, cramming every item down to one illegible character each is a
	// worse outcome than dropping one of them outright, so this subset is
	// treated as not fitting and RenderFooter's own caller drops the last
	// item and tries again — unless this is already the last item left,
	// where there is nothing left to drop and some abbreviation, however
	// tight, still beats the hard cut RenderFooter falls back to.
	minCap := footerMinAbbrevWidth
	if len(fps) == 1 {
		minCap = 1
	}

	for ; itemCap >= minCap; itemCap-- {
		parts := make([]string, len(fps))
		for i, fp := range fps {
			text := fp.Text
			if lipglossWidth(text) > itemCap {
				text = abbreviateItem(g, fp.Key, text, itemCap)
			}
			parts[i] = text
		}

		rows := wrapFooterRows(parts, w)
		if len(rows) > maxRows {
			continue
		}
		fits := true
		for _, r := range rows {
			if lipglossWidth(r) > w {
				fits = false
				break
			}
		}
		if fits {
			return rows, true
		}
	}
	return nil, false
}

// wrapFooterRows empaqueta parts en filas de a lo sumo w columnas, en orden,
// sin reordenar ni partir ningún ítem: agrega ítems a la fila actual
// mientras entren junto con el separador de dos espacios, y abre una fila
// nueva en cuanto el siguiente ítem no entra. Un ítem que por sí solo ya
// exceda w igual abre su propia fila — tryFooterLayout es quien decide si
// esa fila de más se puede tolerar o hace falta abreviar/soltar.
func wrapFooterRows(parts []string, w int) []string {
	if len(parts) == 0 {
		return []string{" "}
	}
	var rows []string
	var cur []string
	for _, p := range parts {
		if len(cur) == 0 {
			cur = []string{p}
			continue
		}
		trial := append(append([]string{}, cur...), p)
		if lipglossWidth(footerRowText(trial)) > w {
			rows = append(rows, footerRowText(cur))
			cur = []string{p}
			continue
		}
		cur = trial
	}
	if len(cur) > 0 {
		rows = append(rows, footerRowText(cur))
	}
	return rows
}

// footerRowText junta los ítems de una fila con el mismo separador y margen
// izquierdo que RenderFooter siempre usó para su única línea.
func footerRowText(items []string) string {
	return " " + strings.Join(items, "  ")
}

// abbreviateItem shrinks a single already-rendered footer item to fit max
// columns, reusing path.go's own truncation patterns instead of inventing a
// second one: ShortenPath's "shrink the least useful part first, only
// truncate the leaf as a last resort" for "cwd" (it is a path already),
// keep the glyph and shrink the name for "git" (the glyph is one column of
// meaning, losing it to an ellipsis first would be a worse trade), and

// truncateRunes's own "spend the last column on an ellipsis" for everything
// else, model included.
func abbreviateItem(g glyphs, key, text string, max int) string {
	if max <= 0 {
		return ""
	}
	switch key {
	case "cwd":
		return ShortenPath(text, max)
	case "git":
		mark := g.gitMark
		budget := max - runeLen(mark)
		if budget < 1 {
			return truncateRunes(text, max)
		}
		branch := strings.TrimPrefix(text, mark)
		return mark + truncateRunes(branch, budget)
	default:
		return truncateRunes(text, max)
	}
}

// renderFooterParts renders each requested item that has something to show,
// keeping the key that produced it so abbreviateItem can special-case it.
// Items with nothing to draw (an empty CWD/GitBranch/Autonomy, the "not
// wired" defaults FooterState documents on each field) are skipped here, the
// same "empty is invisible" rule the previous single-string renderItems
// already followed.
func renderFooterParts(g glyphs, st FooterState, items []string) []footerPart {
	var parts []footerPart
	for _, it := range items {
		switch it {
		case "model":
			if st.Model != "" {
				parts = append(parts, footerPart{it, g.modelMark + " " + st.Model})
			}
		case "autonomy":
			switch {
			case st.Autonomy != "" && st.Phase != "":
				parts = append(parts, footerPart{it, st.Autonomy + g.dot + st.Phase})
			case st.Autonomy != "":
				parts = append(parts, footerPart{it, st.Autonomy})
			}
		case "context":
			parts = append(parts, footerPart{it, contextBar(g, st.ContextPct)})
		case "tokens":
			parts = append(parts, footerPart{it, formatTokens(st.Tokens)})
		case "cost":
			parts = append(parts, footerPart{it, fmt.Sprintf("$%.2f", st.CostUSD)})
		case "git":
			if st.GitBranch != "" {
				parts = append(parts, footerPart{it, g.gitMark + st.GitBranch})
			}
		case "cwd":
			if st.CWD != "" {
				parts = append(parts, footerPart{it, st.CWD})
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

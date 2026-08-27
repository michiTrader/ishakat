package tui_test

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/MichiTrader/ishakat/internal/theme"
	"github.com/MichiTrader/ishakat/internal/tui"
)

func TestRenderFooterIncluyeItemsPedidos(t *testing.T) {
	lay := tui.NewLayout(120, 40, 0, false, false)
	st := tui.FooterState{
		Model:      "sonnet-4-5",
		ContextPct: 0.18,
		Tokens:     36000,
		CostUSD:    0.04,
		GitBranch:  "main",
		CWD:        "~/proyectos/api",
	}
	line := tui.RenderFooter(lay, st, []string{"model", "context", "tokens", "cost", "git", "cwd"})

	for _, want := range []string{"sonnet-4-5", "18%", "36k", "$0.04", "▪main", "~/proyectos/api"} {
		if !strings.Contains(line, want) {
			t.Errorf("RenderFooter() = %q, esperaba que contuviera %q", line, want)
		}
	}
}

// TestRenderFooterNuncaExcedeElAnchoNiElNumeroDeFilas is RC-7's own width
// invariant, kept from the pre-RC-7 test this replaces (the old name,
// "RecortaDeDerechaAIzquierda", documented the drop-first behaviour that RC-7
// retires — see TestRenderFooterEnvuelveEnVezDeSoltar below for the actual
// policy this test now guards). No matter how narrow the layout or how much
// text FooterState carries, every row RenderFooter returns has to fit inside
// lay.ContentWidth() and there can never be more rows than
// lay.FooterSections() allows, or the frame-geometry invariants RC-3/RC-5
// already enforce for every other line would have a footer-shaped hole in
// them.
func TestRenderFooterNuncaExcedeElAnchoNiElNumeroDeFilas(t *testing.T) {
	lay := tui.NewLayout(20, 40, 0, false, false) // ancho muy angosto
	st := tui.FooterState{
		Model:     "un-nombre-de-modelo-bastante-largo",
		CWD:       "~/una/ruta/muy/larga/que/no/entra",
		GitBranch: "feature/algo",
	}
	line := tui.RenderFooter(lay, st, []string{"model", "context", "tokens", "cost", "git", "cwd"})

	rows := strings.Split(line, "\n")
	if got, want := len(rows), lay.FooterSections(); got > want {
		t.Errorf("RenderFooter() devolvió %d filas, lay.FooterSections() permite %d: %q", got, want, line)
	}
	for i, row := range rows {
		if got := len([]rune(row)); got > 20 {
			t.Errorf("RenderFooter() fila %d de %d runas excede el ancho de layout (20): %q", i, got, row)
		}
	}
}

// TestRenderFooterEnvuelveEnVezDeSoltar is RC-7's own regression: a
// BPEstrecho terminal (40-59 columns, "Termux en vertical", §9.1's own real
// most-common case) used to lose context/tokens/cost/cwd entirely because
// RenderFooter dropped items right-to-left the moment the single line did
// not fit. lay.FooterSections() gives BPEstrecho two rows on purpose — this
// pins that every item survives, wrapped across those two rows, instead of
// the tail of footerItemOrder disappearing.
func TestRenderFooterEnvuelveEnVezDeSoltar(t *testing.T) {
	lay := tui.NewLayout(45, 40, 0, false, false) // BPEstrecho
	if lay.Breakpoint != tui.BPEstrecho {
		t.Fatalf("width 45 should classify as BPEstrecho, got breakpoint %v", lay.Breakpoint)
	}
	st := tui.FooterState{
		Model:      "sonnet-4-5",
		ContextPct: 0.18,
		Tokens:     36000,
		CostUSD:    0.04,
		GitBranch:  "algo",
		CWD:        "~/proyectos/api",
	}
	items := []string{"model", "context", "tokens", "cost", "git", "cwd"}
	line := tui.RenderFooter(lay, st, items)

	rows := strings.Split(line, "\n")
	if got, want := len(rows), 2; got != want {
		t.Fatalf("RenderFooter() en BPEstrecho devolvió %d filas, esperaba %d para envolver en vez de soltar: %q", got, want, line)
	}
	for _, want := range []string{"sonnet-4-5", "18%", "36k", "$0.04", "algo", "~/proyectos/api"} {
		if !strings.Contains(line, want) {
			t.Errorf("RenderFooter() = %q, esperaba que el envoltorio conservara %q en vez de soltarlo", line, want)
		}
	}
	for _, row := range rows {
		if got := lipgloss.Width(row); got > lay.ContentWidth() {
			t.Errorf("fila %q de %d columnas excede el ancho de contenido (%d)", row, got, lay.ContentWidth())
		}
	}
}

// TestRenderFooterEnvuelveConAutonomyYEffort is the 2026-08-27 feedback
// batch's own follow-up to RC-7: adding FooterState.Effort (and, before it,
// Autonomy) grew footerItemOrder from six items to eight without anyone
// re-checking that BPEstrecho's own wrap-to-two-rows policy still holds —
// the exact class of regression TestRenderFooterEnvuelveEnVezDeSoltar
// already guards for the original six, just not for the two items added
// since. Same BPEstrecho width and the same six original items, now with
// Autonomy and Effort both set and included in the requested item list:
// every item, old and new, must still survive wrapped across
// lay.FooterSections() rows rather than any of them silently starting to
// get dropped now that the footer carries more to say.
func TestRenderFooterEnvuelveConAutonomyYEffort(t *testing.T) {
	lay := tui.NewLayout(45, 40, 0, false, false) // BPEstrecho
	if lay.Breakpoint != tui.BPEstrecho {
		t.Fatalf("width 45 should classify as BPEstrecho, got breakpoint %v", lay.Breakpoint)
	}
	st := tui.FooterState{
		Model:      "sonnet-4-5",
		Autonomy:   "auto",
		Effort:     "high",
		ContextPct: 0.18,
		Tokens:     36000,
		CostUSD:    0.04,
		GitBranch:  "algo",
		CWD:        "~/proyectos/api",
	}
	items := []string{"model", "autonomy", "effort", "context", "tokens", "cost", "git", "cwd"}
	line := tui.RenderFooter(lay, st, items)

	rows := strings.Split(line, "\n")
	if got, want := len(rows), lay.FooterSections(); got > want {
		t.Fatalf("RenderFooter() en BPEstrecho con autonomy+effort devolvió %d filas, lay.FooterSections() permite %d: %q", got, want, line)
	}
	for _, want := range []string{"sonnet-4-5", "auto", "high", "18%", "36k", "$0.04", "algo", "~/proyectos/api"} {
		if !strings.Contains(line, want) {
			t.Errorf("RenderFooter() = %q, esperaba que el envoltorio conservara %q en vez de soltarlo ahora que hay 8 items", line, want)
		}
	}
	for _, row := range rows {
		if got := lipgloss.Width(row); got > lay.ContentWidth() {
			t.Errorf("fila %q de %d columnas excede el ancho de contenido (%d)", row, got, lay.ContentWidth())
		}
	}
}

// TestRenderFooterAbreviaAntesDeSoltar checks the middle step of RC-7's
// policy directly: an item just a little too wide to sit alongside the
// others gets shortened (path.go's own truncateRunes/ShortenPath patterns —
// an ellipsis, or a shrunk path) rather than being dropped outright the
// moment it does not fit verbatim.
func TestRenderFooterAbreviaAntesDeSoltar(t *testing.T) {
	lay := tui.NewLayout(30, 40, 0, false, false) // BPEstrecho, 2 filas
	st := tui.FooterState{
		Model:     "sonnet-4-5",
		GitBranch: "main",
		CWD:       "~/una/ruta/muy/larga/que/no/entra/nunca",
	}
	line := tui.RenderFooter(lay, st, []string{"model", "git", "cwd"})

	if strings.Contains(line, "una/ruta/muy/larga/que/no/entra/nunca") {
		t.Fatalf("RenderFooter() = %q, esperaba el cwd abreviado, no intacto", line)
	}
	if !strings.Contains(line, "sonnet-4-5") || !strings.Contains(line, "main") {
		t.Errorf("RenderFooter() = %q, abreviar el cwd no debería costar los otros ítems", line)
	}
	if !strings.HasSuffix(line, "nunca") {
		t.Errorf("RenderFooter() = %q, esperaba el cwd abreviado (ShortenPath) terminando en su hoja \"nunca\"", line)
	}
}

// TestRenderFooterSueltaSoloComoUltimoRecurso checks the last step: cuando
// ni envolver en lay.FooterSections() filas ni abreviar (hasta
// footerMinAbbrevWidth) alcanza, RenderFooter suelta ítems de derecha a
// izquierda — pero solo entonces, y solo los que hacen falta, no todos a la
// vez como hacía la versión pre-RC-7. A este ancho (16, BPMinimo, una sola
// fila) ni "cost", "git" ni "cwd" abreviados alcanzan junto al resto, así
// que se sueltan, pero "model", "context" y "tokens" — los primeros de
// footerItemOrder — sobreviven abreviados.
func TestRenderFooterSueltaSoloComoUltimoRecurso(t *testing.T) {
	lay := tui.NewLayout(16, 40, 0, false, false) // BPMinimo, 1 fila, muy angosto
	st := tui.FooterState{
		Model:      "sonnet-4-5",
		ContextPct: 0.18,
		Tokens:     36000,
		CostUSD:    0.04,
		GitBranch:  "main",
		CWD:        "~/proj",
	}
	items := []string{"model", "context", "tokens", "cost", "git", "cwd"}
	line := tui.RenderFooter(lay, st, items)

	if got := lipgloss.Width(line); got > lay.ContentWidth() {
		t.Fatalf("RenderFooter() = %q, de %d columnas, excede el ancho de contenido (%d)", line, got, lay.ContentWidth())
	}
	for _, want := range []string{"36k"} {
		if !strings.Contains(line, want) {
			t.Errorf("RenderFooter() = %q, esperaba que sobreviviera %q antes de soltar los últimos ítems", line, want)
		}
	}
	for _, dropped := range []string{"$0.04", "main", "~/proj"} {
		if strings.Contains(line, dropped) {
			t.Errorf("RenderFooter() = %q, no esperaba encontrar %q intacto a este ancho", line, dropped)
		}
	}
}

// The footer used to draw "◍" for the model and "⎇" for the branch. Neither is
// in WGL4 and the second is missing from most fonts on any platform, so on the
// console that was reported both came out as boxes. In ASCII the whole line has
// to stay under U+0080, and it still has to carry every value: a footer that
// drops the branch to fit its own decoration is not a fix.
func TestRenderFooterEnASCIINoSaleDeASCII(t *testing.T) {
	lay := tui.NewLayout(120, 40, 0, false, false).WithGlyphs(theme.GlyphsASCII)
	st := tui.FooterState{
		Model:      "sonnet-4-5",
		ContextPct: 0.18,
		Tokens:     36000,
		CostUSD:    0.04,
		GitBranch:  "main",
		CWD:        "~/proyectos/api",
	}
	line := tui.RenderFooter(lay, st, nil)

	for _, r := range line {
		if r > 127 {
			t.Fatalf("the ASCII footer draws %q (U+%04X): %q", r, r, line)
		}
	}
	for _, want := range []string{"sonnet-4-5", "18%", "36k", "$0.04", "main", "~/proyectos/api"} {
		if !strings.Contains(line, want) {
			t.Errorf("RenderFooter() = %q, expected it to contain %q", line, want)
		}
	}
}

func TestRenderFooterSinItemsUsaOrdenPorDefecto(t *testing.T) {
	lay := tui.NewLayout(120, 40, 0, false, false)
	st := tui.FooterState{Model: "auto/coding"}
	line := tui.RenderFooter(lay, st, nil)
	if !strings.Contains(line, "auto/coding") {
		t.Errorf("RenderFooter() con items nil debería usar el orden por defecto: %q", line)
	}
}

// TestRenderFooterDrawsAutonomy is Step 30's own closing-adjacent test for
// the status-line half of §21.4 layer 3: the autonomy word appears in the
// default item order, right after the model, matching §21.1's own
// "auto·exec" mockup's left-of-the-dot half with no phase set (see
// TestRenderFooterDrawsAutonomyPhase below for the whole mockup drawn with
// both halves, Step 32's own addition).
func TestRenderFooterDrawsAutonomy(t *testing.T) {
	lay := tui.NewLayout(120, 40, 0, false, false)
	st := tui.FooterState{Model: "sonnet-4-5", Autonomy: "auto"}
	line := tui.RenderFooter(lay, st, nil)
	if !strings.Contains(line, "auto") {
		t.Errorf("RenderFooter() = %q, esperaba que contuviera la autonomía %q", line, "auto")
	}
}

// TestRenderFooterDrawsAutonomyPhase is Step 32's own closing test for the
// status-line's right-of-the-dot half: §21.1's "auto·exec" mockup drawn
// whole, autonomy and phase joined by the same "·" glyph every other
// footer separator uses, once FooterState.Phase is set alongside Autonomy.
func TestRenderFooterDrawsAutonomyPhase(t *testing.T) {
	lay := tui.NewLayout(120, 40, 0, false, false)
	st := tui.FooterState{Model: "sonnet-4-5", Autonomy: "auto", Phase: "exec"}
	line := tui.RenderFooter(lay, st, nil)
	if !strings.Contains(line, "auto\u00b7exec") {
		t.Errorf("RenderFooter() = %q, esperaba que contuviera %q", line, "auto\u00b7exec")
	}
}

// TestRenderFooterOmitePhaseVacia is Phase's own "no turn running" default
// (empty, like Autonomy's "not wired" default above) drawing the bare
// autonomy word with no trailing dot — the mockup never shows "auto·" with
// nothing after it.
func TestRenderFooterOmitePhaseVacia(t *testing.T) {
	lay := tui.NewLayout(120, 40, 0, false, false)
	st := tui.FooterState{Model: "sonnet-4-5", Autonomy: "auto"}
	line := tui.RenderFooter(lay, st, nil)
	if strings.Contains(line, "\u00b7") {
		t.Errorf("RenderFooter() = %q, expected no dot separator with an empty phase", line)
	}
	if !strings.Contains(line, "auto") {
		t.Errorf("RenderFooter() = %q, expected the bare autonomy word", line)
	}
}

// TestRenderFooterOmiteAutonomyVacia is the "not wired" default (every
// Root built before app.go sets FooterState.Autonomy — see its own doc
// comment) drawing nothing, the same empty-is-invisible rule "git"/"cwd"
// already follow for their own zero value.
func TestRenderFooterOmiteAutonomyVacia(t *testing.T) {
	lay := tui.NewLayout(120, 40, 0, false, false)
	st := tui.FooterState{Model: "sonnet-4-5"}
	line := tui.RenderFooter(lay, st, []string{"model", "autonomy"})
	if strings.Contains(line, "  ") {
		t.Errorf("RenderFooter() = %q, expected no double-space gap left by an omitted empty autonomy item", line)
	}
	if !strings.Contains(line, "sonnet-4-5") {
		t.Errorf("RenderFooter() = %q, expected the model to still be drawn", line)
	}
}

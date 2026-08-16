package tui_test

import (
	"strings"
	"testing"

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

func TestRenderFooterRecortaDeDerechaAIzquierdaSiNoEntra(t *testing.T) {
	lay := tui.NewLayout(20, 40, 0, false, false) // ancho muy angosto
	st := tui.FooterState{
		Model:     "un-nombre-de-modelo-bastante-largo",
		CWD:       "~/una/ruta/muy/larga/que/no/entra",
		GitBranch: "feature/algo",
	}
	line := tui.RenderFooter(lay, st, []string{"model", "context", "tokens", "cost", "git", "cwd"})

	if len([]rune(line)) > 20 {
		t.Errorf("RenderFooter() de %d runas excede el ancho de layout (20): %q", len([]rune(line)), line)
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
// "auto·exec" mockup's left-of-the-dot half (the "·exec" phase half is
// Step 32's scope, not drawn here since it does not exist in code yet).
func TestRenderFooterDrawsAutonomy(t *testing.T) {
	lay := tui.NewLayout(120, 40, 0, false, false)
	st := tui.FooterState{Model: "sonnet-4-5", Autonomy: "auto"}
	line := tui.RenderFooter(lay, st, nil)
	if !strings.Contains(line, "auto") {
		t.Errorf("RenderFooter() = %q, esperaba que contuviera la autonomía %q", line, "auto")
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

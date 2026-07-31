package tui_test

import (
	"strings"
	"testing"

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

	for _, want := range []string{"sonnet-4-5", "18%", "36k", "$0.04", "⎇main", "~/proyectos/api"} {
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

func TestRenderFooterSinItemsUsaOrdenPorDefecto(t *testing.T) {
	lay := tui.NewLayout(120, 40, 0, false, false)
	st := tui.FooterState{Model: "auto/coding"}
	line := tui.RenderFooter(lay, st, nil)
	if !strings.Contains(line, "auto/coding") {
		t.Errorf("RenderFooter() con items nil debería usar el orden por defecto: %q", line)
	}
}

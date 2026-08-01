package tui_test

import (
	"strings"
	"testing"

	"github.com/MichiTrader/ishakat/internal/theme"
	"github.com/MichiTrader/ishakat/internal/tui"
)

// fakeStyles es un doble mínimo de theme.Styles para probar banner.go sin
// acoplar el test al paquete theme.
type fakeStyles struct{}

func (fakeStyles) GradientLines(block string, offset int) string { return block }
func (fakeStyles) DimRender(s string) string                     { return s }

func TestBannerVacioSiNoCorresponde(t *testing.T) {
	lay := tui.NewLayout(30, 40, 0, false, false) // BPMinimo: nunca banner
	got := tui.Banner(lay, fakeStyles{}, "0.1.0", "~/api", "auto/coding", true, 0)
	if got != "" {
		t.Errorf("Banner() en BPMinimo debería ser vacío, obtuve %q", got)
	}
}

func TestBannerIncluyeVersionYProveedor(t *testing.T) {
	lay := tui.NewLayout(80, 30, 0, false, false)
	got := tui.Banner(lay, fakeStyles{}, "0.1.0", "~/api", "omniroute · auto/coding · 200k ctx", true, 0)
	for _, want := range []string{"0.1.0", "~/api", "auto/coding", "/help"} {
		if !strings.Contains(got, want) {
			t.Errorf("Banner() = %q, esperaba que contuviera %q", got, want)
		}
	}
}

func TestBannerVacioSinCfgBanner(t *testing.T) {
	lay := tui.NewLayout(80, 30, 0, false, false)
	got := tui.Banner(lay, fakeStyles{}, "0.1.0", "~/api", "", false, 0)
	if got != "" {
		t.Errorf("Banner() con ui.banner=false debería ser vacío, obtuve %q", got)
	}
}

// The reported bug: the logo showed as boxes. It was built from quadrant blocks
// (U+2596..U+259F), which Consolas does not have. This is the rule that replaced
// them — three characters, all of them in WGL4 and in cp437 — written down so it
// cannot quietly erode the next time someone finds a prettier block.
func TestWordmarkUsaSoloBloquesUniversales(t *testing.T) {
	lay := tui.NewLayout(80, 30, 0, false, false)
	allowed := map[rune]bool{'▀': true, '▄': true, '█': true, ' ': true}

	for _, line := range tui.Wordmark(lay) {
		for _, r := range line {
			if !allowed[r] {
				t.Errorf("el logo dibuja %q (U+%04X), fuera del set de tres bloques", r, r)
			}
		}
	}
}

func TestWordmarkASCIINoSaleDeASCII(t *testing.T) {
	lay := tui.NewLayout(80, 30, 0, false, false).WithGlyphs(theme.GlyphsASCII)

	for _, line := range tui.Wordmark(lay) {
		for _, r := range line {
			if r > 127 {
				t.Fatalf("el logo ASCII contiene %q (U+%04X): %q", r, r, line)
			}
		}
	}
	// Y sigue diciendo el nombre: un logo ilegible es el bug que estamos
	// arreglando, no una alternativa aceptable.
	joined := strings.Join(tui.Wordmark(lay), "\n")
	if !strings.Contains(joined, "I S H A K A T") {
		t.Errorf("el logo ASCII debería deletrear el nombre:\n%s", joined)
	}
}

// El banner aparece desde BPEstrecho, que empieza en 40 columnas: si el logo no
// entra ahí, se ve cortado justo en el caso de uso más común del proyecto
// (Termux en vertical).
func TestWordmarkEntraEn40Columnas(t *testing.T) {
	for _, set := range []theme.GlyphSet{theme.GlyphsUnicode, theme.GlyphsASCII} {
		lay := tui.NewLayout(40, 30, 0, false, false).WithGlyphs(set)
		lines := tui.Wordmark(lay)
		width := 0
		for _, ln := range lines {
			if n := len([]rune(ln)); n > width {
				width = n
			}
		}
		if width > 40 {
			t.Errorf("el logo %v mide %d columnas, no entra en 40", set, width)
		}
		// Todas las filas del mismo ancho: una fila corta desalinea el
		// degradado, que se aplica por columna.
		for i, ln := range lines {
			if len([]rune(ln)) != len([]rune(lines[0])) {
				t.Errorf("la fila %d del logo %v mide %d y la primera %d",
					i, set, len([]rune(ln)), len([]rune(lines[0])))
			}
		}
	}
}

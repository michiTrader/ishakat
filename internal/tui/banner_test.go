package tui_test

import (
	"strings"
	"testing"

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

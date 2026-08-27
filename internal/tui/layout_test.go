package tui_test

import (
	"testing"

	"github.com/MichiTrader/ishakat/internal/tui"
)

func TestClassifyBreakpoint(t *testing.T) {
	cases := []struct {
		width int
		want  tui.Breakpoint
	}{
		{20, tui.BPMinimo},
		{39, tui.BPMinimo},
		{40, tui.BPEstrecho},
		{59, tui.BPEstrecho},
		{60, tui.BPNormal},
		{99, tui.BPNormal},
		{100, tui.BPAncho},
		{200, tui.BPAncho},
	}
	for _, c := range cases {
		if got := tui.ClassifyBreakpoint(c.width); got != c.want {
			t.Errorf("ClassifyBreakpoint(%d) = %v, want %v", c.width, got, c.want)
		}
	}
}

func TestLayoutContentWidthLimitaSoloEnAncho(t *testing.T) {
	l := tui.NewLayout(120, 40, 80, false, false)
	if l.Breakpoint != tui.BPAncho {
		t.Fatalf("esperaba BPAncho, tengo %v", l.Breakpoint)
	}
	if got := l.ContentWidth(); got != 80 {
		t.Errorf("ContentWidth() = %d, want 80 (limitado por max_width)", got)
	}

	l2 := tui.NewLayout(70, 40, 80, false, false)
	if got := l2.ContentWidth(); got != 70 {
		t.Errorf("ContentWidth() = %d, want 70 (BPNormal no debe limitarse)", got)
	}
}

// TestLayoutInputWidthIgnoraMaxWidth is the regression test for the
// 2026-08-27 fix ("la barra inferior de input de texto tiene un tamaño
// maximo horizontal, mejor deberia ser del tamaño maximo de la terminal
// horizontalmente"): unlike ContentWidth, InputWidth must always reach the
// terminal's full width even in BPAncho with ui.max_width configured
// smaller than the terminal — the input box is not prose being read back,
// it is the line the user is actively typing into.
func TestLayoutInputWidthIgnoraMaxWidth(t *testing.T) {
	l := tui.NewLayout(120, 40, 80, false, false)
	if l.Breakpoint != tui.BPAncho {
		t.Fatalf("esperaba BPAncho, tengo %v", l.Breakpoint)
	}
	if got := l.ContentWidth(); got != 80 {
		t.Fatalf("ContentWidth() = %d, want 80 (para confirmar que sí se recorta)", got)
	}
	if got := l.InputWidth(); got != 120 {
		t.Errorf("InputWidth() = %d, want 120 (no debe recortarse a max_width)", got)
	}
}

func TestLayoutShowBannerRequiereTTYYAlto(t *testing.T) {
	base := tui.NewLayout(80, 24, 0, false, false)
	if !base.ShowBanner(true) {
		t.Error("con TTY, alto suficiente y cfg.banner=true debería mostrar el banner")
	}
	if base.ShowBanner(false) {
		t.Error("con cfg.banner=false nunca debe mostrarse")
	}

	noTTY := tui.NewLayout(80, 24, 0, false, true)
	if noTTY.ShowBanner(true) {
		t.Error("sin TTY nunca debe mostrarse el banner")
	}

	short := tui.NewLayout(80, 10, 0, false, false)
	if short.ShowBanner(true) {
		t.Error("con menos de 20 líneas de alto no debe mostrarse el banner")
	}

	minimo := tui.NewLayout(30, 40, 0, false, false)
	if minimo.ShowBanner(true) {
		t.Error("en BPMinimo nunca debe mostrarse el banner, sin importar el alto")
	}
}

func TestLayoutInputPrefixSegunBreakpoint(t *testing.T) {
	minimo := tui.NewLayout(30, 40, 0, false, false)
	if minimo.ShowBoxedInput() {
		t.Error("en BPMinimo el input no debe llevar caja")
	}
	if minimo.InputPrefix() != "›" {
		t.Errorf("InputPrefix() en BPMinimo = %q, want un solo carácter", minimo.InputPrefix())
	}

	normal := tui.NewLayout(80, 40, 0, false, false)
	if !normal.ShowBoxedInput() {
		t.Error("fuera de BPMinimo el input debe llevar caja")
	}
}

func TestLayoutFooterSections(t *testing.T) {
	if got := tui.NewLayout(30, 40, 0, false, false).FooterSections(); got != 1 {
		t.Errorf("FooterSections() en BPMinimo = %d, want 1", got)
	}
	if got := tui.NewLayout(80, 40, 0, false, false).FooterSections(); got != 2 {
		t.Errorf("FooterSections() en BPNormal = %d, want 2", got)
	}
}

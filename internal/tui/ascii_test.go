package tui

import (
	"testing"

	"charm.land/lipgloss/v2"
)

func TestFoldASCIIRewritesProse(t *testing.T) {
	cases := []struct{ in, want string }{
		{"catálogo", "catalogo"},
		{"conversación nueva", "conversacion nueva"},
		{"salto de línea", "salto de linea"},
		{"tú", "tu"},
		{"diagnóstico", "diagnostico"},
		{"¿seguro?", "?seguro?"},
		{"~/proyectos/mañana", "~/proyectos/manana"},
		{"already plain", "already plain"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := foldASCII(tc.in); got != tc.want {
			t.Errorf("foldASCII(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The fold must not become a way to stop choosing glyphs: decoration that is
// missing from the ASCII table has to survive untouched so the repertoire test
// still reports it, instead of being quietly turned into a stand-in that looks
// deliberate.
func TestFoldASCIILeavesDecorationAlone(t *testing.T) {
	for _, s := range []string{"▓", "░", "▌", "─", "↑↓", "█"} {
		if got := foldASCII(s); got != s {
			t.Errorf("foldASCII(%q) = %q; block and box drawing must be left for the glyph table", s, got)
		}
	}
}

// Widths are load-bearing: the footer trims to a column budget and the cursor is
// placed by measuring the rendered rows, so a fold that changed a line's width
// would put the cursor next to the text rather than on it.
func TestFoldASCIIPreservesWidth(t *testing.T) {
	for _, s := range []string{
		"catálogo · línea",
		"¿seguro? ¡sí!",
		"“comillas” ‘simples’",
		"Ñandú — año",
	} {
		if got, want := lipgloss.Width(foldASCII(s)), lipgloss.Width(s); got != want {
			t.Errorf("foldASCII(%q) is %d columns, the original is %d", s, got, want)
		}
	}
}

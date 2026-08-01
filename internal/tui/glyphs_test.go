package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/theme"
	"github.com/MichiTrader/ishakat/internal/tui"
)

// rootWithGlyphs builds a root restricted to a repertoire and sized to a normal
// terminal, which is the state every rendering test needs.
func rootWithGlyphs(set theme.GlyphSet) tea.Model {
	r := tui.NewRoot(tui.Options{
		Version: "0.1.0",
		CWD:     "/home/user/api",
		Theme:   theme.Load(""),
		Cap:     theme.CapNone,
		Glyphs:  set,
		NoTTY:   true,
	})
	m, _ := r.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return m
}

// The guillemet is the prompt of the Unicode table and ">" is the prompt of the
// ASCII one, so which of the two the view draws is the shortest honest question
// about whether the repertoire reached the renderer at all. It did not: the
// layout was built without Options.Glyphs, so `[ui] glyphs = "ascii"` changed
// the borders (those come from the styles) and nothing else.
func TestNewRootHonoursTheGlyphOption(t *testing.T) {
	view := rootWithGlyphs(theme.GlyphsASCII).View().Content
	if strings.Contains(view, "›") {
		t.Errorf("with GlyphsASCII the view still draws the Unicode prompt:\n%s", view)
	}
	if !strings.Contains(view, ">") {
		t.Errorf("with GlyphsASCII the view should draw the ASCII prompt:\n%s", view)
	}
}

// A resize rebuilds the layout from the new size. Everything NewLayout does not
// take as a parameter is lost in that rebuild, so the glyph set has to be
// re-applied by hand — otherwise the interface starts correct and turns into
// boxes the first time the window changes, which is the worst of both worlds.
func TestGlyphSetSurvivesAResize(t *testing.T) {
	m := rootWithGlyphs(theme.GlyphsASCII)
	for _, size := range []tea.WindowSizeMsg{{Width: 40, Height: 24}, {Width: 120, Height: 40}} {
		m, _ = m.Update(size)
		if view := m.View().Content; strings.Contains(view, "›") {
			t.Fatalf("after resizing to %dx%d the view went back to Unicode:\n%s",
				size.Width, size.Height, view)
		}
	}
}

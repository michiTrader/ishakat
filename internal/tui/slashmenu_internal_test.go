package tui

import (
	"strings"
	"testing"

	"github.com/MichiTrader/ishakat/internal/slash"
	"github.com/MichiTrader/ishakat/internal/theme"
)

func testRegistry() slash.Registry {
	return slash.NewRegistry([]slash.Command{
		{Name: "help", Describe: "esta pantalla"},
		{Name: "model", ArgHint: "[texto]", Describe: "cambiar modelo"},
		{Name: "models", Describe: "explorar catalogo"},
		{Name: "clear", Describe: "limpiar pantalla"},
		{Name: "exit", Aliases: []string{"quit"}, Describe: "salir"},
	})
}

func TestSlashMenuForOpensOnlyWhileTypingTheCommandName(t *testing.T) {
	r := testRegistry()
	cases := []struct {
		text   string
		active bool
	}{
		{"", false},
		{"hello", false},
		{"/", true},
		{"/m", true},
		{"/model", true},
		{"/model ", false}, // the name is done, an argument starts here
		{"/model son45", false},
		{"/zzz", false}, // no command matches at all
	}
	for _, c := range cases {
		m := slashMenuFor(c.text, r, slashMenu{})
		if got := m.Active(); got != c.active {
			t.Errorf("slashMenuFor(%q).Active() = %v, want %v", c.text, got, c.active)
		}
	}
}

func TestSlashMenuForKeepsSelectionAcrossKeystrokes(t *testing.T) {
	r := testRegistry()
	m := slashMenuFor("/m", r, slashMenu{})
	if len(m.items) != 2 {
		t.Fatalf("expected 2 matches for /m, got %d: %v", len(m.items), m.items)
	}
	m = m.moveDown() // select "models"
	if m.Selected().Name != "models" {
		t.Fatalf("after moveDown selection = %q, want models", m.Selected().Name)
	}

	// Typing another character narrows the match set but should not silently
	// reset the cursor back to row zero if the selection is still in range.
	next := slashMenuFor("/mo", r, m)
	if next.Selected().Name != "models" {
		t.Errorf("selection was not preserved across keystrokes: got %q, want models", next.Selected().Name)
	}

	// Narrowing past the previous selection has to clamp back into range
	// instead of indexing out of bounds.
	narrowed := slashMenuFor("/mod", r, m)
	if narrowed.sel < 0 || narrowed.sel >= len(narrowed.items) {
		t.Fatalf("selection out of range after narrowing: sel=%d items=%d", narrowed.sel, len(narrowed.items))
	}
}

func TestSlashMenuMoveWrapsAtBothEnds(t *testing.T) {
	r := testRegistry()
	m := slashMenuFor("/", r, slashMenu{}) // every command matches
	first := m.Selected().Name

	up := m.moveUp() // wrap from the top to the bottom
	if up.sel != len(up.items)-1 {
		t.Errorf("moveUp from index 0 = %d, want %d", up.sel, len(up.items)-1)
	}

	down := up.moveDown()
	if down.Selected().Name != first {
		t.Errorf("moveDown after wrapping up = %q, want back at %q", down.Selected().Name, first)
	}
}

func TestVisibleSlashRowsKeepsSelectionInWindow(t *testing.T) {
	items := make([]slash.Command, 12)
	for i := range items {
		items[i] = slash.Command{Name: string(rune('a' + i))}
	}

	for sel := 0; sel < len(items); sel++ {
		visible, offset := visibleSlashRows(items, sel, slashMenuRows)
		if len(visible) != slashMenuRows {
			t.Fatalf("sel=%d: got %d visible rows, want %d", sel, len(visible), slashMenuRows)
		}
		if sel < offset || sel >= offset+len(visible) {
			t.Errorf("sel=%d fell outside the visible window [%d,%d)", sel, offset, offset+len(visible))
		}
	}
}

func TestRenderSlashMenuHighlightsTheSelection(t *testing.T) {
	r := testRegistry()
	m := slashMenuFor("/", r, slashMenu{})
	m = m.moveDown() // select the second command

	lay := NewLayout(80, 24, 0, false, false)
	styles := theme.NewStyles(theme.Load(""), theme.CapTruecolor, theme.GlyphsUnicode)

	out := renderSlashMenu(lay, styles, m)
	lines := strings.Split(out, "\n")

	// RenderBox colours every border character regardless of selection, so
	// the signal to look for is the accent colour specifically — present in
	// exactly one content row, the selected one.
	accentOpen := strings.SplitN(styles.Accent.Render("x"), "x", 2)[0]
	matches := 0
	selectedRow := -1
	for i, l := range lines {
		if strings.Contains(l, accentOpen) {
			matches++
			selectedRow = i
		}
	}
	if matches != 1 {
		t.Fatalf("expected exactly one accent-styled row, found %d: %q", matches, lines)
	}
	if !strings.Contains(lines[selectedRow], "/"+m.Selected().Name) {
		t.Errorf("accent styling landed on %q, want the row for %q", lines[selectedRow], m.Selected().Name)
	}
}

func TestRenderSlashMenuIsPlainUnderBPMinimo(t *testing.T) {
	r := testRegistry()
	m := slashMenuFor("/", r, slashMenu{})
	lay := NewLayout(30, 24, 0, false, false) // BPMinimo: no boxed input
	styles := theme.NewStyles(theme.Load(""), theme.CapNone, theme.GlyphsUnicode)

	out := renderSlashMenu(lay, styles, m)
	if strings.ContainsAny(out, "╭╮╰╯│") {
		t.Errorf("BPMinimo dropdown should not draw a border: %q", out)
	}
}

func TestRenderSlashMenuEmptyWhenInactive(t *testing.T) {
	lay := NewLayout(80, 24, 0, false, false)
	styles := theme.NewStyles(theme.Load(""), theme.CapNone, theme.GlyphsUnicode)
	if got := renderSlashMenu(lay, styles, slashMenu{}); got != "" {
		t.Errorf("renderSlashMenu on an inactive menu = %q, want empty", got)
	}
}

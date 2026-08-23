package tui

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/textarea"

	"github.com/MichiTrader/ishakat/internal/theme"
)

// testLister is a fixed, in-memory PathLister for tests, standing in for
// NewPathLister's real os.ReadDir call the same way testRegistry stands in
// for a real slash.Registry.
func testLister(tree map[string][]string) PathLister {
	return func(dir string) []string {
		return tree[dir]
	}
}

func defaultTestTree() map[string][]string {
	return map[string][]string{
		"":             {"README.md", "internal/", "main.go"},
		"internal":     {"app/", "tui/"},
		"internal/tui": {"root.go", "atmenu.go", "atrun.go"},
	}
}

func TestAtMenuForOpensOnlyOnATrailingAtToken(t *testing.T) {
	lister := testLister(defaultTestTree())
	cases := []struct {
		text   string
		active bool
	}{
		{"", false},
		{"hello", false},
		{"@", true},
		{"@R", true},
		{"@README.md", true},
		{"see @internal", true},
		{"see @internal/tui/ro", true},
		{"@zzz", false},       // no entry matches at all
		{"@internal ", false}, // trailing space: no longer a token at all
	}
	for _, c := range cases {
		word := currentWordAtEnd(taWithValue(c.text))
		m := atMenuFor(word, lister, atMenu{})
		if got := m.Active(); got != c.active {
			t.Errorf("atMenuFor(%q).Active() = %v, want %v (entries=%v)", c.text, got, c.active, m.entries)
		}
	}
}

func TestAtMenuForNilListerNeverOpens(t *testing.T) {
	word := currentWordAtEnd(taWithValue("@README"))
	m := atMenuFor(word, nil, atMenu{})
	if m.Active() {
		t.Errorf("atMenuFor with a nil lister should never open, got entries=%v", m.entries)
	}
}

func TestAtMenuForKeepsSelectionAcrossKeystrokes(t *testing.T) {
	lister := testLister(map[string][]string{
		"": {"main.go", "models.go", "modelsdev.go"},
	})
	m := atMenuFor("@mo", lister, atMenu{})
	if len(m.entries) != 2 {
		t.Fatalf("expected 2 matches for @mo, got %d: %v", len(m.entries), m.entries)
	}
	m = m.moveDown() // select the second entry
	second := m.Selected()

	next := atMenuFor("@mod", lister, m)
	if next.Selected() != second {
		t.Errorf("selection was not preserved across keystrokes: got %q, want %q", next.Selected(), second)
	}
}

func TestAtMenuMoveWrapsAtBothEnds(t *testing.T) {
	lister := testLister(defaultTestTree())
	m := atMenuFor("@", lister, atMenu{})
	first := m.Selected()

	up := m.moveUp()
	if up.sel != len(up.entries)-1 {
		t.Errorf("moveUp from index 0 = %d, want %d", up.sel, len(up.entries)-1)
	}

	down := up.moveDown()
	if down.Selected() != first {
		t.Errorf("moveDown after wrapping up = %q, want back at %q", down.Selected(), first)
	}
}

func TestSplitAtTokenSeparatesDirFromPrefix(t *testing.T) {
	cases := []struct {
		token, dir, prefix string
	}{
		{"", "", ""},
		{"ro", "", "ro"},
		{"src/tui/ro", "src/tui", "ro"},
		{"src/", "src", ""},
	}
	for _, c := range cases {
		dir, prefix := splitAtToken(c.token)
		if dir != c.dir || prefix != c.prefix {
			t.Errorf("splitAtToken(%q) = (%q,%q), want (%q,%q)", c.token, dir, prefix, c.dir, c.prefix)
		}
	}
}

func TestCurrentWordAtEndOnlyFiresAtTheTrueEndOfTheBuffer(t *testing.T) {
	// A single-line buffer with the cursor at the end: the trailing token
	// is recognised.
	ta := taWithValue("hola @readme")
	if got := currentWordAtEnd(ta); got != "@readme" {
		t.Errorf("currentWordAtEnd(%q) = %q, want %q", ta.Value(), got, "@readme")
	}

	// Moving the cursor off the end (still on the last line) must not see
	// a token anymore: this is the "cursor at the literal end of the
	// buffer" restriction applyAtCompletion's whole-buffer SetValue relies
	// on.
	ta.SetCursorColumn(ta.Column() - 1)
	if got := currentWordAtEnd(ta); got != "" {
		t.Errorf("currentWordAtEnd with cursor not at the end = %q, want empty", got)
	}
}

func TestCurrentWordAtEndIgnoresEarlierLinesInAMultilineDraft(t *testing.T) {
	// The "@" token sits on the first line, not the last: currentWordAtEnd
	// only ever looks at the last line's trailing word (here "segunda",
	// with no "@"), so atMenuFor built on top of it correctly never
	// resurrects a reference typed earlier in a multi-line draft.
	ta := taWithValue("primera linea @notthis\nsegunda")
	if got := currentWordAtEnd(ta); got != "segunda" {
		t.Errorf("currentWordAtEnd = %q, want %q (the last line's own trailing word, not the earlier @token)", got, "segunda")
	}
}

func TestRenderAtMenuHighlightsTheSelection(t *testing.T) {
	lister := testLister(defaultTestTree())
	m := atMenuFor("@", lister, atMenu{})
	m = m.moveDown() // select the second entry

	lay := NewLayout(80, 24, 0, false, false)
	styles := theme.NewStyles(theme.Load(""), theme.CapTruecolor, theme.GlyphsUnicode)

	out := renderAtMenu(lay, styles, m)
	lines := strings.Split(out, "\n")

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
	if !strings.Contains(lines[selectedRow], m.Selected()) {
		t.Errorf("accent styling landed on %q, want the row for %q", lines[selectedRow], m.Selected())
	}
}

func TestRenderAtMenuIsPlainUnderBPMinimo(t *testing.T) {
	lister := testLister(defaultTestTree())
	m := atMenuFor("@", lister, atMenu{})
	lay := NewLayout(30, 24, 0, false, false) // BPMinimo: no boxed input
	styles := theme.NewStyles(theme.Load(""), theme.CapNone, theme.GlyphsUnicode)

	out := renderAtMenu(lay, styles, m)
	if strings.ContainsAny(out, "╭╮╰╯│") {
		t.Errorf("BPMinimo dropdown should not draw a border: %q", out)
	}
}

func TestRenderAtMenuEmptyWhenInactive(t *testing.T) {
	lay := NewLayout(80, 24, 0, false, false)
	styles := theme.NewStyles(theme.Load(""), theme.CapNone, theme.GlyphsUnicode)
	if got := renderAtMenu(lay, styles, atMenu{}); got != "" {
		t.Errorf("renderAtMenu on an inactive menu = %q, want empty", got)
	}
}

// taWithValue is this test file's own small helper: NewInput builds the
// same flat-styled textarea.Model the rest of the package uses, and
// SetValue leaves the cursor at the tail of what it just set — exactly the
// "cursor at the true end of the buffer" state currentWordAtEnd's own
// contract is testing.
func taWithValue(v string) textarea.Model {
	ta := NewInput("")
	ta.SetValue(v)
	return ta
}

// TestOptionsPathListerIsWiredIntoRoot is the same
// NewRoot(Options{...})-not-the-private-field regression shape
// TestOptionsReloadForIsWiredIntoRoot already establishes for ReloadFor.
func TestOptionsPathListerIsWiredIntoRoot(t *testing.T) {
	called := false
	lister := PathLister(func(dir string) []string {
		called = true
		return nil
	})
	root := NewRoot(Options{PathLister: lister})
	if root.pathLister == nil {
		t.Fatal("NewRoot did not wire Options.PathLister into Root.pathLister")
	}
	root.pathLister("")
	if !called {
		t.Fatal("Root.pathLister is not the factory Options.PathLister supplied")
	}
}

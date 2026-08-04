package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/catalog"
	"github.com/MichiTrader/ishakat/internal/theme"
)

func TestCtrlPOpensThePickerFromModeChat(t *testing.T) {
	var m tea.Model = rootWithCatalog(catalogWithModels("omni/son45", "omni/gpt-5"))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})

	root := m.(Root)
	if root.mode != ModePicker {
		t.Fatalf("mode = %v, want ModePicker", root.mode)
	}
}

func TestPickerEscReturnsToChatWithoutChangingTheModel(t *testing.T) {
	root := rootWithCatalog(catalogWithModels("omni/son45"))
	root.model = "omni/son45"
	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})

	got := m.(Root)
	if got.mode != ModeChat {
		t.Fatalf("mode = %v, want ModeChat after esc", got.mode)
	}
	if got.model != "omni/son45" {
		t.Errorf("esc must not change the model, got %q", got.model)
	}
}

func TestPickerTypingNarrowsTheRowList(t *testing.T) {
	root := rootWithCatalog(catalogWithModels("omni/son45", "omni/gpt-5", "other/completely-different"))
	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})

	all := countModelRows(m.(Root).picker.rows)
	if all != 3 {
		t.Fatalf("expected all 3 models before typing anything, got %d", all)
	}

	for _, r := range "son45" {
		m, _ = m.Update(tea.KeyPressMsg{Text: string(r), Code: r})
	}
	got := m.(Root)
	if got.picker.query != "son45" {
		t.Fatalf("picker.query = %q, want %q", got.picker.query, "son45")
	}
	if n := countModelRows(got.picker.rows); n != 1 {
		t.Fatalf("expected exactly one match for %q, got %d rows", "son45", n)
	}
	if got.picker.rows[len(got.picker.rows)-1].cand.Model.Ref != "omni/son45" {
		t.Errorf("the surviving row should be omni/son45, got %+v", got.picker.rows)
	}
}

func TestPickerBackspaceUndoesTheLastRune(t *testing.T) {
	root := rootWithCatalog(catalogWithModels("omni/son45"))
	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	m, _ = m.Update(tea.KeyPressMsg{Text: "x", Code: 'x'})
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})

	if got := m.(Root).picker.query; got != "" {
		t.Errorf("query after backspace = %q, want empty", got)
	}
}

func TestPickerEnterOnAModelRowSwitchesAndReturnsToChat(t *testing.T) {
	root := rootWithCatalog(catalogWithModels("omni/son45"))
	root.model = "auto/coding"
	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})

	// Narrow down to the one model and pick it, exactly like a user typing
	// into the search box and pressing enter.
	for _, r := range "son45" {
		m, _ = m.Update(tea.KeyPressMsg{Text: string(r), Code: r})
	}
	// Typing resets the selection to row 0, which is the provider header
	// (§9.4 groups by provider even when there is only one) — one "down"
	// reaches the model row underneath it.
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	// Enter on a model row returns modelChosenMsg as a tea.Cmd rather than
	// switching inline (picker.go's updatePicker comment explains why:
	// picker.go never touches Root's fields directly). Running that command
	// and feeding its result back through Update is what a real
	// tea.Program's event loop does for us.
	var cmd tea.Cmd
	m, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter on a model row should return the modelChosenMsg command")
	}
	m, _ = m.Update(cmd())

	got := m.(Root)
	if got.mode != ModeChat {
		t.Fatalf("mode = %v, want ModeChat once a model is chosen", got.mode)
	}
	if got.model != "omni/son45" {
		t.Errorf("model = %q, want %q", got.model, "omni/son45")
	}
	if len(got.transcript) != 1 || !strings.Contains(got.transcript[0].text, "omni/son45") {
		t.Errorf("expected a confirmation notice naming the model, got %v", got.transcript)
	}
}

func TestPickerCtrlFCyclesTheFilterLabel(t *testing.T) {
	root := rootWithCatalog(catalogWithModels("omni/son45"))
	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})

	if got := m.(Root).picker.filter.label(); got != "all" {
		t.Fatalf("filter starts as %q, want %q", got, "all")
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	if got := m.(Root).picker.filter.label(); got != "free" {
		t.Errorf("filter after one ctrl+f = %q, want %q", got, "free")
	}
}

func TestPickerLeftCollapsesTheSelectedGroupAndRightExpandsIt(t *testing.T) {
	root := rootWithCatalog(catalogWithModels("omni/son45"))
	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})

	// Selection starts on the one header row (the only row when there is a
	// single provider group and the search box is empty but unfocused by
	// the cursor's own row).
	before := countModelRows(m.(Root).picker.rows)
	if before != 1 {
		t.Fatalf("expected one model row before collapsing, got %d", before)
	}

	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	afterCollapse := m.(Root)
	if n := countModelRows(afterCollapse.picker.rows); n != 0 {
		t.Fatalf("collapsing the group should hide its model rows, got %d", n)
	}
	if !afterCollapse.picker.rows[afterCollapse.picker.sel].collapsed {
		t.Error("the selected header row should report collapsed = true")
	}

	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	afterExpand := m.(Root)
	if n := countModelRows(afterExpand.picker.rows); n != 1 {
		t.Errorf("expanding the group again should restore its model row, got %d", n)
	}
}

func TestPickerActiveIsFalseWithoutACatalog(t *testing.T) {
	var p Picker
	if p.Active() {
		t.Error("a zero-value Picker (no catalog) must report Active() == false")
	}
}

func TestPickerRebuildClampsSelectionAfterTheListShrinks(t *testing.T) {
	cat := catalogWithModels("omni/son45", "omni/gpt-5")
	p := newPicker(cat, catalog.ResolveOptions{}, nil, "", "")
	p.sel = len(p.rows) - 1 // last row, whatever it currently is

	p = p.typeText("nosuchmodelatall")
	if p.sel != 0 {
		t.Errorf("sel = %d, want 0 once the filtered list is empty", p.sel)
	}
	if len(p.rows) != 0 {
		t.Errorf("expected no rows for a query matching nothing, got %v", p.rows)
	}
}

// --- Regression: picker row layout must not waste rows on a mostly-blank
// metadata line, and its window must never grow past the terminal's own
// scroll — both reported directly against a live OmniRoute catalog of ~300
// models, where they compounded into the same symptom: the cursor scrolled
// off the top of the visible area with no way back into view. See
// pickerMaxVisibleRows and renderPickerRow's own comments for the
// mechanics; these tests exist so neither regresses silently.

// TestRenderPickerRowFitsIDAndMetaOnOneLineWhenThereIsRoom is the fix for
// "no aprovecha el espacio": at a normal terminal width, "provider/model"
// plus its "200k · — · TV" metadata comfortably share one line, so they
// must be drawn on one — not stacked across two, the wireframe's 40-column
// layout that was being applied unconditionally at every width.
func TestRenderPickerRowFitsIDAndMetaOnOneLineWhenThereIsRoom(t *testing.T) {
	g := unicodeGlyphs
	st := theme.NewStyles(theme.Load(""), theme.CapTruecolor, theme.GlyphsUnicode)
	row := pickerRow{
		provider: "tllm",
		cand: catalog.Candidate{Model: catalog.Model{
			Ref:      "tllm/openrouter_grok_4",
			Provider: "tllm",
			Context:  200_000,
			Caps:     catalog.Caps{Tools: true, Vision: true},
		}},
	}

	lines := renderPickerRow(g, st, 80, row, false, false, false)
	if len(lines) != 1 {
		t.Fatalf("got %d lines at width 80, want exactly 1 (id and meta should share a line): %q", len(lines), lines)
	}
	if !strings.Contains(lines[0], "openrouter_grok_4") {
		t.Errorf("the one line drawn must still contain the model id, got %q", lines[0])
	}
	if !strings.Contains(lines[0], "200k") {
		t.Errorf("the one line drawn must still contain the metadata, got %q", lines[0])
	}
}

// TestRenderPickerRowFallsBackToTwoLinesWhenTooNarrow keeps §9.4's own
// reason for the two-line layout intact: at a width too narrow to fit both
// without truncating the id — the datum that actually has to be readable —
// it must still fall back to stacking id above metadata, exactly as the
// 40-column wireframe shows.
func TestRenderPickerRowFallsBackToTwoLinesWhenTooNarrow(t *testing.T) {
	g := unicodeGlyphs
	st := theme.NewStyles(theme.Load(""), theme.CapTruecolor, theme.GlyphsUnicode)
	row := pickerRow{
		provider: "tllm",
		cand: catalog.Candidate{Model: catalog.Model{
			Ref:      "tllm/openrouter_deepseek_r1_a_rather_long_wire_id",
			Provider: "tllm",
			Context:  200_000,
			Caps:     catalog.Caps{Tools: true},
		}},
	}

	lines := renderPickerRow(g, st, 40, row, false, false, false)
	if len(lines) != 2 {
		t.Fatalf("got %d lines at width 40, want 2 (id must not be truncated to make room for meta): %q", len(lines), lines)
	}
}

// TestVisiblePickerRowsCapsAtTenAndFollowsTheCursor is the fix for "solo
// veo los ultimos modelos": with a catalog of a few hundred models every
// row used to be drawn unconditionally, so the rendered frame grew far
// taller than any real terminal — moving the cursor into the first rows of
// that frame scrolled them past the top of the *terminal's own* backscroll
// before Bubble Tea ever got to redraw them back into view. Capping the
// window at pickerMaxVisibleRows and keeping sel inside it (the same
// windowing slashmenu.go's own dropdown already relies on) is what makes
// the cursor visible at every position, not just near the end of the list.
func TestVisiblePickerRowsCapsAtTenAndFollowsTheCursor(t *testing.T) {
	rows := make([]pickerRow, 300)
	for i := range rows {
		rows[i] = pickerRow{provider: "tllm", cand: catalog.Candidate{}}
	}

	for _, sel := range []int{0, 1, 50, 149, 150, 298, 299} {
		visible, offset := visiblePickerRows(rows, sel, pickerMaxVisibleRows)
		if len(visible) != pickerMaxVisibleRows {
			t.Fatalf("sel=%d: got %d visible rows, want %d", sel, len(visible), pickerMaxVisibleRows)
		}
		if sel < offset || sel >= offset+len(visible) {
			t.Errorf("sel=%d fell outside the visible window [%d,%d) — the cursor would have scrolled off screen", sel, offset, offset+len(visible))
		}
	}
}

// TestRenderPickerNeverDrawsMoreThanTenRows is the same fix exercised
// through the whole renderPicker path, on a catalog sized like the one
// reported live (hundreds of OmniRoute models): the number of picker rows
// actually written to the frame must stay capped regardless of how many
// rows the catalog matched, or a terminal shorter than the frame is back to
// losing the cursor off the top.
func TestRenderPickerNeverDrawsMoreThanTenRows(t *testing.T) {
	refs := make([]string, 300)
	for i := range refs {
		refs[i] = fmt.Sprintf("tllm/model_%03d", i)
	}
	root := rootWithCatalog(catalogWithModels(refs...))
	root.lay = NewLayout(80, 24, 0, false, false)
	root.styles = theme.NewStyles(theme.Load(""), theme.CapTruecolor, theme.GlyphsUnicode)
	root.picker = newPicker(root.cat, root.resolveOptions(), nil, "", "")
	root.mode = ModePicker
	root.picker.sel = len(root.picker.rows) - 1 // jump to the very last row

	out := root.renderPicker()
	drawn := countDrawnModelRows(out, refs)
	if drawn > pickerMaxVisibleRows {
		t.Errorf("renderPicker drew %d model rows, want at most %d", drawn, pickerMaxVisibleRows)
	}
	// renderPickerRow draws the wireID (the part of Ref after the provider's
	// own slash, per catalog.SplitRef), never the full "provider/wireID"
	// Ref — so the string to look for is "model_299", not "tllm/model_299".
	_, lastWireID, _ := catalog.SplitRef(refs[len(refs)-1])
	if !strings.Contains(out, lastWireID) {
		t.Error("the selected (last) row must still be visible in the drawn window")
	}
}

// countDrawnModelRows counts how many distinct model refs appear in out —
// a simpler and more robust signal than counting newlines, since a model
// row can legitimately be drawn as either one line (fix for the wasted
// two-line layout) or two, depending on width.
func countDrawnModelRows(out string, refs []string) int {
	n := 0
	for _, ref := range refs {
		_, wireID, ok := catalog.SplitRef(ref)
		if !ok {
			wireID = ref
		}
		if strings.Contains(out, wireID) {
			n++
		}
	}
	return n
}

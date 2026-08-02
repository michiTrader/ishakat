package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/catalog"
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

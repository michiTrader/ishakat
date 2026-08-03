package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/catalog"
)

// TestCatalogRefreshedSwapsTheCatalog is CatalogRefreshedMsg's ordinary
// path: the background refresh app.Run kicked off at startup came back with
// something, and Root's own catalog pointer must now point at it — the
// whole reason the message exists is so that a picker opened after the
// refresh (or /model's alias/favorite resolution) sees the fresh list
// instead of whatever LoadCatalog read off disk before the network answered.
func TestCatalogRefreshedSwapsTheCatalog(t *testing.T) {
	root := newHeadlessRoot()
	root.cat = &catalog.Catalog{Models: []catalog.Model{{Ref: "old/model"}}}

	next := &catalog.Catalog{Models: []catalog.Model{{Ref: "new/model"}}}
	var m tea.Model = root
	m, _ = m.Update(CatalogRefreshedMsg{Catalog: next})

	got := m.(Root).cat
	if got != next {
		t.Fatalf("Root.cat = %p, want the refreshed catalog %p", got, next)
	}
	if _, ok := got.Get("new/model"); !ok {
		t.Fatalf("refreshed catalog missing new/model; refs = %v", got.Refs())
	}
}

// TestCatalogRefreshedRebuildsAnOpenPickerWithoutClosingIt covers the
// trickier half: the user has /model open (ModePicker) at the exact moment
// the background refresh lands. Closing the overlay out from under them
// would be a worse surprise than the row list changing, so the picker must
// stay open, on the same mode, with rows rebuilt from the new catalog.
func TestCatalogRefreshedRebuildsAnOpenPickerWithoutClosingIt(t *testing.T) {
	root := newHeadlessRoot()
	root.cat = &catalog.Catalog{Models: []catalog.Model{{Ref: "old/model"}}}
	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	// /model with no query opens the picker directly (openPicker's own
	// zero-candidate exemption in slashrun.go's runModelCommand) — driven
	// through the same public Update surface a real submit would take.
	m = typeAndEnter(m, "/model")
	if m.(Root).mode != ModePicker {
		t.Fatalf("mode = %v after /model, want ModePicker", m.(Root).mode)
	}

	next := &catalog.Catalog{Models: []catalog.Model{{Ref: "new/model"}}}
	m, _ = m.Update(CatalogRefreshedMsg{Catalog: next})

	got := m.(Root)
	if got.mode != ModePicker {
		t.Fatalf("mode = %v after the refresh, want it to stay ModePicker", got.mode)
	}
	if got.picker.cat != next {
		t.Fatalf("picker.cat = %p, want the refreshed catalog %p", got.picker.cat, next)
	}
	foundNew := false
	for _, row := range got.picker.rows {
		if row.header {
			continue
		}
		if row.cand.Model.Ref == "new/model" {
			foundNew = true
		}
		if row.cand.Model.Ref == "old/model" {
			t.Errorf("picker rows still contain old/model after the refresh: %+v", got.picker.rows)
		}
	}
	if !foundNew {
		t.Errorf("picker rows never picked up new/model after rebuild: %+v", got.picker.rows)
	}
}

// TestCatalogRefreshedWithNilCatalogIsANoOp covers app.BackgroundRefresh's
// documented failure answer: nil means the refresh could not improve on
// what was already there, and Root must leave its own catalog untouched
// rather than blank a picker over a network hiccup that was not the user's
// fault.
func TestCatalogRefreshedWithNilCatalogIsANoOp(t *testing.T) {
	root := newHeadlessRoot()
	original := &catalog.Catalog{Models: []catalog.Model{{Ref: "old/model"}}}
	root.cat = original

	var m tea.Model = root
	m, _ = m.Update(CatalogRefreshedMsg{Catalog: nil})

	got := m.(Root).cat
	if got != original {
		t.Fatalf("Root.cat changed on a nil refresh: got %p, want the original %p", got, original)
	}
}

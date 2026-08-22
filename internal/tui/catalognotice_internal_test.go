package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/catalog"
)

// TestPickerShowsTheSeededNotice covers exactly the confusion the user hit:
// with no cache and no reachable OmniRoute, the picker draws the embedded
// seed's 13 entries with nothing to tell them apart from a real, discovered
// catalog — the count alone ("models · 13") looks identical either way.
// catalogNotice's Seeded branch is the fix; this proves it actually reaches
// the picker's own View(), not just the helper in isolation.
func TestPickerShowsTheSeededNotice(t *testing.T) {
	cat := catalogWithModels("omniroute/auto/coding")
	cat.Seeded = true

	root := rootWithCatalog(cat)
	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/model")

	view := m.View().Content
	if !strings.Contains(view, "embedded seed") {
		t.Errorf("picker view should mention the embedded seed when Seeded=true, got:\n%s", view)
	}
}

// TestPickerShowsTheStaleNotice covers the other honesty gap §4.4 already
// promises for `ishakat models` on the command line but never reached the
// interactive picker before this fix: a catalog built from an expired cache
// (Stale, not Seeded) should say so, with the same age wording
// resumeAge/humanAge already use elsewhere in the interface.
func TestPickerShowsTheStaleNotice(t *testing.T) {
	cat := catalogWithModels("omniroute/auto/coding")
	cat.Stale = true
	cat.FetchedAt = time.Now().Add(-3 * time.Hour)

	root := rootWithCatalog(cat)
	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/model")

	view := m.View().Content
	if !strings.Contains(view, "stale cache") {
		t.Errorf("picker view should mention the stale cache when Stale=true, got:\n%s", view)
	}
	if !strings.Contains(view, "3 hours") {
		t.Errorf("picker view should report the cache's age, got:\n%s", view)
	}
}

// TestPickerHasNoNoticeForALiveCatalog is the negative case: a catalog that
// is neither Seeded nor Stale (the ordinary post-refresh state) must not
// print anything extra — the whole point of only two branches in
// catalogNotice is that a healthy catalog stays exactly as quiet as before
// this fix.
func TestPickerHasNoNoticeForALiveCatalog(t *testing.T) {
	root := rootWithCatalog(catalogWithModels("omniroute/auto/coding"))
	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/model")

	view := m.View().Content
	for _, bad := range []string{"embedded seed", "stale cache"} {
		if strings.Contains(view, bad) {
			t.Errorf("picker view should say nothing about staleness for a live catalog, found %q in:\n%s", bad, view)
		}
	}
}

// TestCatalogNoticePrefersSeededOverStale covers Catalog states that are
// technically both at once (a seed is definitionally stale too — see
// catalog.SeedCatalog's own BuildInput{Seeded: true, Stale: true}): Seeded
// is the stronger, more specific claim ("this was never verified"), so its
// message must win outright rather than being silently overwritten by the
// generic stale-cache line.
func TestCatalogNoticePrefersSeededOverStale(t *testing.T) {
	cat := &catalog.Catalog{Seeded: true, Stale: true}
	got := catalogNotice(cat)
	if !strings.Contains(got, "embedded seed") {
		t.Fatalf("catalogNotice(Seeded && Stale) = %q, want the seed wording to win", got)
	}
}

// TestCatalogNoticeNilCatalogIsSilent is the guard Picker.Active() already
// relies on elsewhere: a Root built before any catalog ever loaded (most of
// this package's own tests, and any session started with Options.Catalog
// == nil) must not panic reading Seeded/Stale off a nil receiver.
func TestCatalogNoticeNilCatalogIsSilent(t *testing.T) {
	if got := catalogNotice(nil); got != "" {
		t.Fatalf("catalogNotice(nil) = %q, want empty", got)
	}
}

// TestCatalogNoticeReportsPendingProviders is F11's "catalogs refreshed /
// N pending" notice: a catalog that is neither Seeded nor Stale (an
// ordinary, freshly-built snapshot — see catalog.Build's own doc comment
// on PendingProviders) but still carries PendingProviders > 0 must say so,
// through the picker's own View() and not just the helper in isolation.
func TestCatalogNoticeReportsPendingProviders(t *testing.T) {
	cat := catalogWithModels("omniroute/auto/coding")
	cat.PendingProviders = 2

	root := rootWithCatalog(cat)
	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/model")

	view := m.View().Content
	if !strings.Contains(view, "catalogs refreshed") {
		t.Errorf("picker view should mention the refresh when PendingProviders > 0, got:\n%s", view)
	}
	if !strings.Contains(view, "2 providers pending") {
		t.Errorf("picker view should report the pending count, got:\n%s", view)
	}
}

// TestCatalogNoticePendingProvidersSingular pins pendingProvidersLabel's
// singular wording: "1 provider pending", never "1 providers pending".
func TestCatalogNoticePendingProvidersSingular(t *testing.T) {
	cat := &catalog.Catalog{PendingProviders: 1}
	got := catalogNotice(cat)
	if !strings.Contains(got, "1 provider pending") {
		t.Fatalf("catalogNotice(PendingProviders=1) = %q, want singular \"1 provider pending\"", got)
	}
	if strings.Contains(got, "1 providers") {
		t.Fatalf("catalogNotice(PendingProviders=1) = %q, must not pluralize a singular count", got)
	}
}

// TestCatalogNoticeStaleOutranksPendingProviders: a catalog can be both
// Stale (the whole cache is old) and carry a stale PendingProviders count
// left over from before the cache expired — Stale is the stronger, more
// urgent claim ("this whole snapshot might be wrong"), so it must win.
func TestCatalogNoticeStaleOutranksPendingProviders(t *testing.T) {
	cat := &catalog.Catalog{Stale: true, PendingProviders: 3}
	got := catalogNotice(cat)
	if !strings.Contains(got, "stale cache") {
		t.Fatalf("catalogNotice(Stale && PendingProviders) = %q, want the stale wording to win", got)
	}
	if strings.Contains(got, "pending") {
		t.Fatalf("catalogNotice(Stale && PendingProviders) = %q, must not also mention pending providers", got)
	}
}

// TestCatalogNoticeHasNoPendingLineWhenEveryoneAnswered is the negative
// case symmetric to TestPickerHasNoNoticeForALiveCatalog above: a live,
// fully-answered catalog (PendingProviders == 0) must stay exactly as
// quiet as before this change.
func TestCatalogNoticeHasNoPendingLineWhenEveryoneAnswered(t *testing.T) {
	if got := catalogNotice(&catalog.Catalog{}); got != "" {
		t.Fatalf("catalogNotice(zero value) = %q, want empty", got)
	}
}

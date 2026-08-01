package catalog

import (
	"strings"
	"testing"
)

// The seed is what a first run with no cache and no network shows (§4.4).
// It is embedded, so these tests double as a check that seed.json is valid
// and that nothing in it claims to be verified data.

func TestSeedParsesAndKnowsOmniroute(t *testing.T) {
	if _, err := loadSeed(); err != nil {
		t.Fatalf("seed.json does not parse: %v", err)
	}
	providers := SeedProviders()
	if len(providers) == 0 {
		t.Fatal("SeedProviders() is empty: the first run with no network would show nothing")
	}
	found := false
	for _, p := range providers {
		if p == "omniroute" {
			found = true
		}
	}
	if !found {
		t.Errorf("SeedProviders() = %v, want the default configuration's provider", providers)
	}
}

func TestSeedModelsAreWellFormed(t *testing.T) {
	models := SeedModels("omniroute")
	if len(models) < 10 {
		t.Fatalf("SeedModels(omniroute) has %d entries, §4.4 asks for the virtual models plus the ten most common", len(models))
	}

	seen := map[string]bool{}
	virtual := 0
	for _, m := range models {
		if m.Ref != JoinRef("omniroute", m.WireID) {
			t.Errorf("%q: Ref does not match provider/wire_id", m.Ref)
		}
		if seen[m.Ref] {
			t.Errorf("%q: duplicated in the seed", m.Ref)
		}
		seen[m.Ref] = true
		if m.Provider != "omniroute" {
			t.Errorf("%q: Provider = %q", m.Ref, m.Provider)
		}
		if m.Source != SourceSeed {
			t.Errorf("%q: Source = %s, want seed only — nothing here is verified", m.Ref, m.Source)
		}
		if m.Family == "" {
			t.Errorf("%q: no family, so it cannot be grouped in the picker", m.Ref)
		}
		if m.Name == "" {
			t.Errorf("%q: no display name", m.Ref)
		}
		if m.HasTag(TagVirtual) {
			virtual++
		}
	}
	if virtual == 0 {
		t.Error("the seed has no virtual models, which are the ones discovery can never report")
	}
}

func TestSeedModelsUnknownProviderInventsNothing(t *testing.T) {
	if got := SeedModels("a-provider-that-does-not-exist"); len(got) != 0 {
		t.Errorf("SeedModels(unknown) = %d entries, want none", len(got))
	}
	if got := SeedModels(""); len(got) != 0 {
		t.Errorf("SeedModels(\"\") = %d entries, want none", len(got))
	}
}

func TestSeedModelsIsCaseAndSpaceInsensitive(t *testing.T) {
	if len(SeedModels("  OmniRoute ")) == 0 {
		t.Error("the provider id lookup should tolerate case and spaces")
	}
}

// TestSeedCatalogMarksItselfAsUnverified is the honesty requirement: a
// seeded catalog says it is seeded and stale, so the interface can show the
// strip instead of pretending the data was checked.
func TestSeedCatalogMarksItselfAsUnverified(t *testing.T) {
	cat := SeedCatalog([]ProviderInput{{ID: "omniroute", Enabled: true, AuthOK: true}})

	if cat.Len() == 0 {
		t.Fatal("SeedCatalog produced nothing for omniroute")
	}
	if !cat.Seeded || !cat.Stale {
		t.Errorf("Seeded = %v, Stale = %v, want both true", cat.Seeded, cat.Stale)
	}
	if len(cat.Notes) == 0 || !strings.Contains(strings.Join(cat.Notes, "\n"), "seed") {
		t.Errorf("Notes = %v, want one saying nothing is verified yet", cat.Notes)
	}
	for _, m := range cat.Models {
		if m.Source.Has(SourceDiscover) {
			t.Errorf("%q: Source = %s, the seed must never claim discovery", m.Ref, m.Source)
		}
		if !m.Source.Has(SourceSeed) {
			t.Errorf("%q: Source = %s, want the seed bit", m.Ref, m.Source)
		}
		if m.HasTag(TagUnlisted) {
			t.Errorf("%q: tagged unlisted, but discovery never ran", m.Ref)
		}
	}
}

// TestSeedCatalogLetsTheUserWin: even with no network at all, a
// [[provider.model]] entry is still the authority on its own fields.
func TestSeedCatalogLetsTheUserWin(t *testing.T) {
	cat := SeedCatalog([]ProviderInput{{
		ID: "omniroute", Enabled: true, AuthOK: true,
		Declared: []DeclaredModel{
			{WireID: "auto/coding", Name: "My router", Context: 999_000},
			{WireID: "private/model-not-in-the-seed", Name: "Private"},
		},
	}})

	m := find(t, cat, "omniroute/auto/coding")
	if m.Name != "My router" || m.Context != 999_000 {
		t.Errorf("declared fields lost: name=%q context=%d", m.Name, m.Context)
	}
	if !m.Source.Has(SourceConfig) {
		t.Errorf("Source = %s, want the config bit", m.Source)
	}

	p := find(t, cat, "omniroute/private/model-not-in-the-seed")
	if p.Name != "Private" {
		t.Errorf("Name = %q, want the declared one", p.Name)
	}
}

// TestSeedCatalogSkipsProvidersItDoesNotKnow: seeding a provider we know
// nothing about would invent models that do not exist.
func TestSeedCatalogSkipsProvidersItDoesNotKnow(t *testing.T) {
	cat := SeedCatalog([]ProviderInput{{ID: "some-local-llama", Enabled: true, AuthOK: true}})
	if cat.Len() != 0 {
		t.Errorf("Len = %d, want 0; refs = %v", cat.Len(), cat.Refs())
	}
	if len(cat.Notes) != 0 {
		t.Errorf("Notes = %v, want none when there is nothing to report", cat.Notes)
	}
}

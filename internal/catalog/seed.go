package catalog

import (
	_ "embed"
	"encoding/json"
	"strings"
	"sync"
)

// The seed catalog of §4.4: what the program shows on a first run with no
// cache and no network. Without it the first launch on a phone with no
// signal is an empty list and a model picker that cannot pick anything,
// which is precisely the moment the tool has to look solid.
//
// It is embedded in the binary, not downloaded. Its entries carry
// SourceSeed and are replaced by real data on the first successful refresh:
// nothing here is authoritative, and the interface says so.

//go:embed seed.json
var seedJSON []byte

type seedFile struct {
	V         int                   `json:"v"`
	Note      string                `json:"note"`
	Providers map[string][]seedItem `json:"providers"`
}

type seedItem struct {
	WireID  string   `json:"wire_id"`
	Name    string   `json:"name"`
	Family  string   `json:"family"`
	Context int      `json:"context"`
	Output  int      `json:"output"`
	Tags    []string `json:"tags"`
	Caps    Caps     `json:"caps"`
	Cost    *Cost    `json:"cost"`
}

var (
	seedOnce   sync.Once
	seedParsed seedFile
	seedErr    error
)

func loadSeed() (seedFile, error) {
	seedOnce.Do(func() {
		seedErr = json.Unmarshal(seedJSON, &seedParsed)
	})
	return seedParsed, seedErr
}

// SeedModels returns the seed entries for one provider id, already in
// catalog form. An unknown provider returns nothing rather than pretending:
// seeding a provider we know nothing about would invent models that do not
// exist.
//
// The special case is the id "omniroute", which is the one the default
// configuration ships and the one whose virtual models cannot be discovered
// (§4.3). For any other provider the seed contributes nothing and the list
// is simply empty until the first refresh.
func SeedModels(providerID string) []Model {
	sf, err := loadSeed()
	if err != nil {
		return nil
	}
	items, ok := sf.Providers[strings.ToLower(strings.TrimSpace(providerID))]
	if !ok {
		return nil
	}
	out := make([]Model, 0, len(items))
	for _, it := range items {
		if strings.TrimSpace(it.WireID) == "" {
			continue
		}
		m := Model{
			Ref:       JoinRef(providerID, it.WireID),
			Provider:  providerID,
			WireID:    it.WireID,
			Name:      it.Name,
			Family:    it.Family,
			Context:   it.Context,
			MaxOutput: it.Output,
			Cost:      it.Cost,
			Caps:      it.Caps,
			Source:    SourceSeed,
			Health:    HealthOK,
		}
		for _, t := range it.Tags {
			m.addTag(t)
		}
		if m.Family == "" {
			m.Family = familyOf(it.WireID)
		}
		out = append(out, m)
	}
	return out
}

// SeedProviders lists the provider ids the seed knows about.
func SeedProviders() []string {
	sf, err := loadSeed()
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(sf.Providers))
	for id := range sf.Providers {
		out = append(out, id)
	}
	return out
}

// SeedCatalog builds a snapshot from the seed for the given providers, in
// the order they were passed (configuration order).
//
// Declared models still win: a user who wrote [[provider.model]] entries
// gets them merged on top, because their configuration is the one source
// that is always right about what they can call.
func SeedCatalog(providers []ProviderInput) Catalog {
	in := BuildInput{Seeded: true, Stale: true}
	for _, p := range providers {
		seeded := SeedModels(p.ID)
		p.Discovered = make([]DiscoveredModel, 0, len(seeded))
		for _, m := range seeded {
			p.Discovered = append(p.Discovered, DiscoveredModel{
				WireID:  m.WireID,
				Name:    m.Name,
				Context: m.Context,
				Output:  m.MaxOutput,
				Tags:    m.Tags,
			})
		}
		// Discovery never ran, so nothing may be marked "unlisted".
		p.DiscoverOK = false
		p.Discover = false
		in.Providers = append(in.Providers, p)
	}
	cat := Build(in)

	// Build stamps SourceDiscover on anything that came in as discovered;
	// for the seed that would be a lie, so the mark is corrected here.
	for i := range cat.Models {
		cat.Models[i].Source &^= SourceDiscover
		cat.Models[i].Source |= SourceSeed
	}
	if len(cat.Models) > 0 {
		cat.Note("seed catalog: nothing verified against the provider yet")
	}
	return cat
}

package catalog

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// The fusion of §4.3, and the rules it follows, in one place:
//
//   - EXISTENCE is defined by discovery. If the provider does not list it,
//     it cannot be called.
//   - METADATA comes from models.dev when discovery does not bring it.
//   - THE USER ALWAYS WINS. A model declared by hand that discovery does not
//     report stays visible but marked (TagUnlisted) — exactly the case of
//     OmniRoute's virtual models.
//
// The merge is field by field, not record by record: a provider that reports
// a context window but no price, plus a models.dev entry with the price,
// must produce one complete record and not two halves.

// DeclaredModel is a [[provider.model]] entry from the configuration.
// Like DiscoveredModel it is catalog's own type so this package does not
// have to import config (§6.1); internal/app does the conversion.
type DeclaredModel struct {
	WireID  string
	Name    string
	Context int
	Output  int
	Tags    []string
}

// ProviderInput is everything known about one provider at merge time.
type ProviderInput struct {
	ID   string
	Name string

	// Enabled and AuthOK come from the configuration; they decide Health,
	// not visibility. A provider without a key still shows its models, with
	// HealthUnauthenticated, because hiding them makes the fix impossible
	// to discover.
	Enabled bool
	AuthOK  bool

	// Discover is the config flag. When false the provider contributes only
	// its declared models, and its absence from discovery is not a failure.
	Discover bool

	// Discovered is the provider's list, fresh or from cache.
	Discovered []DiscoveredModel

	// DiscoverOK is false when the last attempt failed. The models are
	// still used —they come from the cache— but marked unreachable.
	DiscoverOK bool

	// DiscoverErr is the one-line reason, for the notes.
	DiscoverErr string

	// Declared are the [[provider.model]] entries.
	Declared []DeclaredModel

	FetchedAt time.Time
}

// BuildInput is the whole merge in one struct, which keeps Build a pure
// function of its arguments: no clock, no filesystem, no network. That is
// what makes the closing test of Step 6 a table test.
type BuildInput struct {
	Providers []ProviderInput
	ModelsDev *Index
	Stats     map[string]Stat

	// HideDeprecated drops the models the provider marked as going away.
	// A model the user has actually used is never hidden, no matter what
	// the flag says: it is in their history and it must remain findable.
	HideDeprecated bool

	// Stale and Seeded are propagated to the snapshot so the interface can
	// show the staleness strip of §4.4.
	Stale  bool
	Seeded bool
}

// Build fuses the three sources into a snapshot.
func Build(in BuildInput) Catalog {
	var cat Catalog
	order := make([]string, 0, len(in.Providers))

	for _, p := range in.Providers {
		order = append(order, p.ID)

		// byWire keeps the position of each model in the output, so the
		// declared entries can amend the discovered ones in place instead
		// of appending a duplicate.
		byWire := make(map[string]int, len(p.Discovered)+len(p.Declared))

		for _, d := range p.Discovered {
			wire := strings.TrimSpace(d.WireID)
			if wire == "" {
				continue // an entry with no id is not callable
			}
			m := modelFromDiscovery(p, d)
			applyModelsDev(&m, in.ModelsDev)
			byWire[strings.ToLower(wire)] = len(cat.Models)
			cat.Models = append(cat.Models, m)
		}

		for _, decl := range p.Declared {
			wire := strings.TrimSpace(decl.WireID)
			if wire == "" {
				continue
			}
			key := strings.ToLower(wire)
			if i, ok := byWire[key]; ok {
				applyDeclared(&cat.Models[i], decl)
				continue
			}
			// Declared but not discovered: visible and marked. This is the
			// OmniRoute virtual-model case, and dropping it would remove
			// the models the user most wants (§4.3).
			m := Model{
				Ref:      JoinRef(p.ID, wire),
				Provider: p.ID,
				WireID:   wire,
				Source:   SourceConfig,
				Health:   healthFor(p),
			}
			applyDeclared(&m, decl)
			applyModelsDev(&m, in.ModelsDev)
			if p.Discover && p.DiscoverOK {
				// Only claim "the provider does not list it" when the
				// provider actually answered.
				m.addTag(TagUnlisted)
			}
			byWire[key] = len(cat.Models)
			cat.Models = append(cat.Models, m)
		}

		if p.Discover && !p.DiscoverOK {
			msg := "could not list the models of " + p.ID
			if p.DiscoverErr != "" {
				msg += ": " + p.DiscoverErr
			}
			cat.Note(msg)
		}
		if !p.AuthOK {
			cat.Note("provider " + p.ID + " has no resolved credential")
		}
		if !p.FetchedAt.IsZero() && (cat.FetchedAt.IsZero() || p.FetchedAt.Before(cat.FetchedAt)) {
			cat.FetchedAt = p.FetchedAt
		}
	}

	applyStats(cat.Models, in.Stats)

	if in.HideDeprecated {
		kept := cat.Models[:0]
		hidden := 0
		for _, m := range cat.Models {
			if m.Deprecated() && m.UseCount == 0 && !m.Source.Has(SourceConfig) {
				hidden++
				continue
			}
			kept = append(kept, m)
		}
		cat.Models = kept
		if hidden > 0 {
			cat.Note(strconv.Itoa(hidden) + " deprecated model(s) hidden (catalog.hide_deprecated)")
		}
	}

	sortModels(cat.Models, order)
	cat.Stale = in.Stale
	cat.Seeded = in.Seeded
	cat.ensureIndex()
	return cat
}

func healthFor(p ProviderInput) Health {
	switch {
	case !p.AuthOK:
		return HealthUnauthenticated
	case p.Discover && !p.DiscoverOK:
		return HealthUnreachable
	default:
		return HealthOK
	}
}

// modelFromDiscovery turns a provider entry into a record, reading the extra
// fields gateways put in the raw JSON.
func modelFromDiscovery(p ProviderInput, d DiscoveredModel) Model {
	m := Model{
		Ref:       JoinRef(p.ID, d.WireID),
		Provider:  p.ID,
		WireID:    d.WireID,
		Name:      d.Name,
		Context:   d.Context,
		MaxOutput: d.Output,
		Source:    SourceDiscover,
		Health:    healthFor(p),
	}
	for _, t := range d.Tags {
		m.addTag(t)
	}
	applyRaw(&m, d.Raw)
	if m.Family == "" {
		m.Family = familyOf(d.WireID)
	}
	return m
}

// wireExtra are the fields gateways add to their /models entries. Only the
// widespread ones are read; anything else stays in Raw for a future step.
type wireExtra struct {
	OwnedBy     string `json:"owned_by"`
	Family      string `json:"family"`
	Description string `json:"description"`
	Deprecated  bool   `json:"deprecated"`
	Pricing     *struct {
		// OpenRouter-style gateways send USD per token as strings.
		Prompt     json.Number `json:"prompt"`
		Completion json.Number `json:"completion"`
		Request    json.Number `json:"request"`
		Image      json.Number `json:"image"`
	} `json:"pricing"`
	Architecture *struct {
		Modality         string   `json:"modality"`
		InputModalities  []string `json:"input_modalities"`
		OutputModalities []string `json:"output_modalities"`
	} `json:"architecture"`
	SupportedParameters []string `json:"supported_parameters"`
}

func applyRaw(m *Model, raw json.RawMessage) {
	if len(raw) == 0 {
		return
	}
	var x wireExtra
	if err := json.Unmarshal(raw, &x); err != nil {
		return
	}
	if m.Family == "" {
		switch {
		case x.Family != "":
			m.Family = strings.ToLower(x.Family)
		case x.OwnedBy != "":
			m.Family = strings.ToLower(x.OwnedBy)
		}
	}
	if x.Deprecated {
		m.addTag(TagDeprecated)
	}
	if x.Pricing != nil {
		// Per-token USD in strings → per-million USD. Zero prices are
		// meaningful here (the gateway is claiming the model is free), so a
		// present-but-zero pricing block becomes a Cost of zero, not nil.
		in := perMillion(x.Pricing.Prompt)
		out := perMillion(x.Pricing.Completion)
		if in >= 0 && out >= 0 {
			m.Cost = &Cost{In: in, Out: out}
			if m.Cost.Zero() {
				m.addTag(TagFree)
			}
		}
	}
	if x.Architecture != nil {
		for _, in := range x.Architecture.InputModalities {
			if strings.EqualFold(in, "image") {
				m.Caps.Vision = true
			}
		}
		if strings.Contains(strings.ToLower(x.Architecture.Modality), "image") {
			m.Caps.Vision = true
		}
	}
	for _, p := range x.SupportedParameters {
		switch strings.ToLower(p) {
		case "tools", "tool_choice":
			m.Caps.Tools = true
		case "reasoning", "include_reasoning":
			m.Caps.Reasoning = true
		case "response_format", "structured_outputs":
			m.Caps.JSONSchema = true
		}
	}
}

// perMillion converts a USD-per-token string into USD per million tokens.
// Returns -1 when the value is absent or unparseable, which is how the
// caller tells "no price" from "price zero".
func perMillion(n json.Number) float64 {
	s := strings.TrimSpace(n.String())
	if s == "" {
		return -1
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v < 0 {
		return -1
	}
	return v * 1_000_000
}

// applyModelsDev fills the gaps with the metadata source. It never
// overwrites: discovery and the user are both more authoritative about what
// this particular gateway serves.
func applyModelsDev(m *Model, ix *Index) {
	if ix.Empty() {
		return
	}
	md, stage := ix.Lookup(m.Provider, m.WireID)
	if stage == MatchNone {
		return
	}
	m.Source |= SourceModelsDev

	if m.Name == "" || m.Name == m.WireID {
		if md.Name != "" {
			m.Name = md.Name
		}
	}
	if m.Family == "" && md.Family != "" {
		m.Family = strings.ToLower(md.Family)
	}
	if m.Context == 0 && md.Context > 0 {
		m.Context = md.Context
	}
	if m.MaxOutput == 0 && md.Output > 0 {
		m.MaxOutput = md.Output
	}
	if m.Cost == nil {
		if c := md.CatalogCost(); c != nil {
			m.Cost = c
			if c.Zero() {
				m.addTag(TagFree)
			}
		}
	}
	m.Caps = m.Caps.Merge(md.Caps())

	// Modalities/Temperature feed Layer 1's curation (curate.go's
	// nonChat): both are copy-only-if-unset, the same "discovery and the
	// user both outrank this source" rule as everything else in this
	// function, and both stay nil/empty rather than false/zero when
	// models.dev never said — see the field comments on Model and
	// MDModel for why that distinction has to survive the copy.
	if len(m.Modalities) == 0 && len(md.Modalities) > 0 {
		m.Modalities = md.Modalities
	}
	if m.Temperature == nil && md.Temperature != nil {
		m.Temperature = md.Temperature
	}

	// models.dev's own lifecycle field. This is what makes hide_deprecated
	// (merge.go's HideDeprecated below) actually hide something: providers
	// almost never send "deprecated": true on their own /models response
	// (see applyRaw's TagDeprecated, which fires from the gateway payload
	// instead), so without this models.dev is the only source that has the
	// data at all. See docs/DESIGN-model-curation.md §1.1.
	switch strings.ToLower(md.Status) {
	case "deprecated":
		m.addTag(TagDeprecated)
	case "beta", "alpha":
		m.addTag(TagBeta)
	}
}

// applyDeclared is the last word: whatever the user wrote wins over both
// other sources, field by field, and only for the fields they actually set.
func applyDeclared(m *Model, d DeclaredModel) {
	m.Source |= SourceConfig
	if d.Name != "" {
		m.Name = d.Name
	}
	if d.Context > 0 {
		m.Context = d.Context
	}
	if d.Output > 0 {
		m.MaxOutput = d.Output
	}
	for _, t := range d.Tags {
		m.addTag(t)
	}
	if m.Family == "" {
		m.Family = familyOf(m.WireID)
	}
}

func applyStats(models []Model, stats map[string]Stat) {
	if len(stats) == 0 {
		return
	}
	for i := range models {
		s, ok := stats[models[i].Ref]
		if !ok {
			continue
		}
		models[i].UseCount = s.UseCount
		models[i].LastUsed = s.LastUsed
		models[i].P50Latency = time.Duration(s.P50ms) * time.Millisecond
		models[i].FailStreak = s.FailStreak
		if s.FailStreak >= 3 && models[i].Health == HealthOK {
			models[i].Health = HealthCooling
		}
	}
}

// familyOf is the last-resort family guess: the first meaningful word of the
// identifier. It is only used for grouping and for the metadata fallback, so
// being wrong is cheap — being absent is not, because then nothing groups.
func familyOf(wireID string) string {
	s := NormalizeID(wireID)
	if s == "" {
		return ""
	}
	s = strings.ReplaceAll(s, "_", "-")
	s = strings.ReplaceAll(s, ":", "-")
	parts := strings.Split(s, "-")
	for _, p := range parts {
		if p == "" {
			continue
		}
		if _, err := strconv.Atoi(p); err == nil {
			continue // "4o" survives, "4" alone does not
		}
		return p
	}
	return parts[0]
}

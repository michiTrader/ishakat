// Package catalog implements contract 2 of the PLAN (§4bis): the single
// normalized model registry that fuses the three sources —provider
// discovery, models.dev metadata and the user's configuration— into one list
// the rest of the program can trust.
//
// ARCHITECTURAL NOTE (deviation from the §6.2 tree, deliberate and
// documented). §6.2 places modelsdev.go —an HTTP client— inside this
// package, but §6.1 states that catalog is one of the two pure type packages
// that cross every boundary, and the CI test of §6.1 fails if net/http shows
// up in the transitive closure of internal/tui. Both cannot hold at once:
// the model picker (Step 10) imports catalog. The split chosen here keeps
// both promises:
//
//   - internal/catalog: pure. Types, cache file, merge, resolution. No
//     net/http, no provider import, no lipgloss.
//   - internal/catalog/fetch: everything that touches the network —provider
//     discovery and the models.dev client with If-None-Match.
//
// Parsing of the models.dev payloads stays here (modelsdev.go) because
// decoding bytes is pure; only the transport moved.
package catalog

import (
	"sort"
	"strings"
	"time"
)

// Source is a bitmask recording which of the sources contributed to a model
// record. It is not decoration: the interface has to be able to say "this
// one is declared by you but the provider does not list it" instead of
// silently hiding it (§4.3).
type Source uint8

const (
	// SourceDiscover means the provider listed the model in GET /models.
	// That is what makes a model callable *right now*.
	SourceDiscover Source = 1 << iota

	// SourceModelsDev means models.dev contributed metadata (context, cost,
	// capabilities).
	SourceModelsDev

	// SourceConfig means the user declared it in [[provider.model]]. The
	// user always wins on the fields they set.
	SourceConfig

	// SourceSeed is an extension over §4.2: the embedded seed catalog that
	// makes a first run with no cache and no network usable (§4.4). It is
	// tracked separately so the UI can be honest about the fact that
	// nothing has been verified against the provider yet.
	SourceSeed
)

// Has reports whether s contains every bit of o.
func (s Source) Has(o Source) bool { return s&o == o }

// String renders the mask as "discover+modelsdev", for `ishakat models` and
// for test failure messages.
func (s Source) String() string {
	var parts []string
	if s.Has(SourceDiscover) {
		parts = append(parts, "discover")
	}
	if s.Has(SourceModelsDev) {
		parts = append(parts, "modelsdev")
	}
	if s.Has(SourceConfig) {
		parts = append(parts, "config")
	}
	if s.Has(SourceSeed) {
		parts = append(parts, "seed")
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, "+")
}

// Health is what the last contact with the provider said about this model.
type Health uint8

const (
	// HealthOK is the provider answering and listing the model.
	HealthOK Health = iota

	// HealthCooling is a model that just failed and is being backed off.
	// FailStreak drives it; the engine (Step 8) sets it.
	HealthCooling

	// HealthUnauthenticated is a provider with no resolved credential. The
	// model is shown —hiding it would be baffling— but it cannot be used
	// until the key exists, and §4.6 checks this *before* the swap.
	HealthUnauthenticated

	// HealthUnreachable is a provider whose discovery failed. Its models
	// come from the cache, so they are shown as known-but-unverified.
	HealthUnreachable
)

var healthNames = map[Health]string{
	HealthOK:              "ok",
	HealthCooling:         "cooling",
	HealthUnauthenticated: "unauthenticated",
	HealthUnreachable:     "unreachable",
}

func (h Health) String() string {
	if s, ok := healthNames[h]; ok {
		return s
	}
	return "unknown"
}

// Usable reports whether a turn can be attempted against this model right
// now. Unreachable is usable on purpose: the provider may have been down for
// discovery and fine for inference, and refusing to try would be worse than
// trying and failing with a real error.
func (h Health) Usable() bool { return h != HealthUnauthenticated }

// Cost is USD per million tokens.
//
// A nil *Cost means UNKNOWN, which is not the same as free. The selector
// renders "—" instead of "$0", because marking as free something that
// charges is the worst lie that screen can tell (§4.2).
type Cost struct {
	In         float64 `json:"in,omitempty"`
	Out        float64 `json:"out,omitempty"`
	CacheRead  float64 `json:"cache_read,omitempty"`
	CacheWrite float64 `json:"cache_write,omitempty"`
}

// Zero reports whether every price is zero, which is how models.dev encodes
// a genuinely free model.
func (c *Cost) Zero() bool {
	return c != nil && c.In == 0 && c.Out == 0 && c.CacheRead == 0 && c.CacheWrite == 0
}

// Caps are the capabilities of §4.2. The zero value means "text only", the
// lowest common denominator, which always works.
type Caps struct {
	Tools       bool `json:"tools,omitempty"`
	Vision      bool `json:"vision,omitempty"`
	Reasoning   bool `json:"reasoning,omitempty"`
	Streaming   bool `json:"streaming,omitempty"`
	JSONSchema  bool `json:"json_schema,omitempty"`
	Attachments bool `json:"attachments,omitempty"`
}

// Any reports whether anything at all is known about the capabilities.
func (c Caps) Any() bool {
	return c.Tools || c.Vision || c.Reasoning || c.Streaming || c.JSONSchema || c.Attachments
}

// Merge fills in the gaps of c with the bits set in o, never clearing what c
// already claims. Capabilities are merged optimistically because the sources
// disagree by omission, not by contradiction: discovery rarely reports them
// and models.dev usually does.
func (c Caps) Merge(o Caps) Caps {
	return Caps{
		Tools:       c.Tools || o.Tools,
		Vision:      c.Vision || o.Vision,
		Reasoning:   c.Reasoning || o.Reasoning,
		Streaming:   c.Streaming || o.Streaming,
		JSONSchema:  c.JSONSchema || o.JSONSchema,
		Attachments: c.Attachments || o.Attachments,
	}
}

// Well-known tags. They are strings and not an enum because providers invent
// their own and dropping the unknown ones would lose information.
const (
	TagFree       = "free"
	TagVirtual    = "virtual"
	TagLocal      = "local"
	TagDeprecated = "deprecated"
	TagBeta       = "beta"

	// TagUnlisted is an extension: the user declared the model in
	// [[provider.model]] but discovery did not report it. Exactly the case
	// of OmniRoute's virtual models, which are real and callable but never
	// appear in GET /models (§4.3).
	TagUnlisted = "unlisted"
)

// ContextFloor is the conservative window assumed for compaction warnings
// when the real one is unknown. §4.3 is explicit: do not guess 128k. Assume
// a floor, warn early, and let the first real usage report fix the number.
const ContextFloor = 32_000

// Model is the normalized record of §4.2.
type Model struct {
	Ref      string `json:"ref"`      // "omniroute/anthropic/claude-sonnet-4-5" — unique key, what the user sees
	Provider string `json:"provider"` // "omniroute"
	WireID   string `json:"wire_id"`  // "anthropic/claude-sonnet-4-5" — what goes in the request JSON
	Name     string `json:"name"`     // "Claude Sonnet 4.5"
	Family   string `json:"family"`   // "claude" — for grouping and metadata fallback

	Context   int `json:"context,omitempty"` // 0 = unknown
	MaxOutput int `json:"max_output,omitempty"`

	Cost *Cost    `json:"cost,omitempty"` // nil = UNKNOWN, which is not the same as free
	Caps Caps     `json:"caps,omitzero"`
	Tags []string `json:"tags,omitempty"`

	Source Source `json:"source"`
	Health Health `json:"health"`

	// Local statistics; they live in the cache and feed the fuzzy ranking
	// of §4.5.
	UseCount   int           `json:"use_count,omitempty"`
	LastUsed   time.Time     `json:"last_used,omitzero"`
	P50Latency time.Duration `json:"p50_latency,omitempty"`
	FailStreak int           `json:"fail_streak,omitempty"`
}

// HasTag reports whether the model carries a tag, case-insensitively.
func (m Model) HasTag(tag string) bool {
	for _, t := range m.Tags {
		if strings.EqualFold(t, tag) {
			return true
		}
	}
	return false
}

// Free reports whether the model is known to be free. Unknown cost is not
// free: that is the whole point of Cost being a pointer.
func (m Model) Free() bool { return m.HasTag(TagFree) || m.Cost.Zero() }

// Deprecated reports whether the provider marked the model as going away.
func (m Model) Deprecated() bool { return m.HasTag(TagDeprecated) }

// EffectiveContext is the window to use for compaction math: the real one
// when known, the conservative floor when not (§4.3).
func (m Model) EffectiveContext() int {
	if m.Context > 0 {
		return m.Context
	}
	return ContextFloor
}

// ContextKnown separates "no window reported" from "a small window", which
// the footer renders differently: "32k?" is not "32k".
func (m Model) ContextKnown() bool { return m.Context > 0 }

// Display is the label for lists: the human name when there is one, the wire
// id when there is not.
func (m Model) Display() string {
	if strings.TrimSpace(m.Name) != "" {
		return m.Name
	}
	return m.WireID
}

// addTag appends a tag if it is not already there, preserving order.
func (m *Model) addTag(tag string) {
	if tag == "" || m.HasTag(tag) {
		return
	}
	m.Tags = append(m.Tags, tag)
}

// SplitRef cuts a reference into provider and wire id.
//
// The cut happens on the FIRST slash only. OmniRoute serves models whose own
// identifier already contains slashes, so strings.Split(ref, "/") is a
// guaranteed bug and §4.2 says so in as many words.
func SplitRef(ref string) (providerID, wireID string, ok bool) {
	head, tail, found := strings.Cut(strings.TrimSpace(ref), "/")
	if !found || head == "" || tail == "" {
		return "", strings.TrimSpace(ref), false
	}
	return head, tail, true
}

// JoinRef builds a reference from its two halves.
func JoinRef(providerID, wireID string) string {
	return providerID + "/" + wireID
}

// Catalog is an immutable snapshot. The picker (Step 10) receives one of
// these and never touches the network; a background refresh replaces the
// whole snapshot instead of mutating it in place, which is what makes the
// hot swap of §4.4 safe without a mutex in the UI.
type Catalog struct {
	Models []Model `json:"models"`

	// FetchedAt is the oldest successful fetch among the sources: the
	// number behind the "catalog from 3 days ago" strip.
	FetchedAt time.Time `json:"fetched_at,omitzero"`

	// Stale means the data comes from an expired cache, and Seeded means it
	// comes from the embedded seed. Both are shown, never hidden.
	Stale  bool `json:"stale,omitempty"`
	Seeded bool `json:"seeded,omitempty"`

	// Notes are honest one-liners for the interface: which provider could
	// not be reached, which metadata is missing. Never errors that abort.
	Notes []string `json:"notes,omitempty"`

	index map[string]int
}

// Len is the number of models in the snapshot.
func (c *Catalog) Len() int {
	if c == nil {
		return 0
	}
	return len(c.Models)
}

// Get looks a model up by exact reference, case-insensitively.
func (c *Catalog) Get(ref string) (Model, bool) {
	if c == nil || len(c.Models) == 0 {
		return Model{}, false
	}
	c.ensureIndex()
	if i, ok := c.index[strings.ToLower(strings.TrimSpace(ref))]; ok {
		return c.Models[i], true
	}
	return Model{}, false
}

// Has reports whether a reference exists in the snapshot.
func (c *Catalog) Has(ref string) bool {
	_, ok := c.Get(ref)
	return ok
}

// Refs lists every reference in snapshot order.
func (c *Catalog) Refs() []string {
	if c == nil {
		return nil
	}
	out := make([]string, 0, len(c.Models))
	for _, m := range c.Models {
		out = append(out, m.Ref)
	}
	return out
}

// ByProvider groups the models keeping the snapshot order inside each group.
// The picker of §9.4 groups by provider, and doing it here keeps that view
// from having to sort anything.
func (c *Catalog) ByProvider() map[string][]Model {
	if c == nil {
		return nil
	}
	out := make(map[string][]Model)
	for _, m := range c.Models {
		out[m.Provider] = append(out[m.Provider], m)
	}
	return out
}

// Providers lists the provider ids present, in first-appearance order, which
// is the configuration order that Build preserves.
func (c *Catalog) Providers() []string {
	if c == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, m := range c.Models {
		if !seen[m.Provider] {
			seen[m.Provider] = true
			out = append(out, m.Provider)
		}
	}
	return out
}

// Note appends an honest one-liner, ignoring duplicates.
func (c *Catalog) Note(msg string) {
	if msg == "" {
		return
	}
	for _, n := range c.Notes {
		if n == msg {
			return
		}
	}
	c.Notes = append(c.Notes, msg)
}

// Prepend is Note, but at the front. It exists for the one caller that has
// to graft the notes of a discarded build onto a replacement catalog and
// wants them to keep reading in their original order.
func (c *Catalog) Prepend(msg string) {
	if msg == "" {
		return
	}
	for _, n := range c.Notes {
		if n == msg {
			return
		}
	}
	c.Notes = append([]string{msg}, c.Notes...)
}

func (c *Catalog) ensureIndex() {
	if c.index != nil && len(c.index) == len(c.Models) {
		return
	}
	c.index = make(map[string]int, len(c.Models))
	for i, m := range c.Models {
		c.index[strings.ToLower(m.Ref)] = i
	}
}

// sortModels orders a slice for display: providers in the given order, and
// inside a provider by use count, then by name. Determinism matters here —
// a list that reshuffles between runs is unusable from muscle memory.
func sortModels(models []Model, providerOrder []string) {
	rank := make(map[string]int, len(providerOrder))
	for i, p := range providerOrder {
		rank[p] = i
	}
	pos := func(p string) int {
		if i, ok := rank[p]; ok {
			return i
		}
		return len(providerOrder) + 1
	}
	sort.SliceStable(models, func(i, j int) bool {
		a, b := models[i], models[j]
		if pa, pb := pos(a.Provider), pos(b.Provider); pa != pb {
			return pa < pb
		}
		if a.UseCount != b.UseCount {
			return a.UseCount > b.UseCount
		}
		if a.Deprecated() != b.Deprecated() {
			return b.Deprecated()
		}
		return a.Ref < b.Ref
	})
}

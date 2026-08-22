package catalog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// This file holds the models.dev side of the merge: the shape of its two
// payloads, the digest that gets cached, and the cascade lookup of §4.3.
// Only parsing lives here — the conditional HTTP request is in
// internal/catalog/fetch, so this package stays free of net/http (see the
// package comment in model.go).
//
// The two payloads:
//
//   - api.json:    { "<provider>": { id, name, models: { "<model>": {…} } } }
//   - models.json: { "<vendor>/<model>": {…} }   ← provider-agnostic base
//
// The second one exists precisely for the gateway case: when OmniRoute
// serves Claude under a name that matches no models.dev *provider*, the
// agnostic base still knows what that model is.

// DigestVersion guards the sibling cache file the same way CacheVersion
// guards catalog.json.
const DigestVersion = 1

// MDCost is the models.dev cost block, in USD per million tokens.
type MDCost struct {
	Input      float64 `json:"input,omitempty"`
	Output     float64 `json:"output,omitempty"`
	CacheRead  float64 `json:"cache_read,omitempty"`
	CacheWrite float64 `json:"cache_write,omitempty"`
}

// MDModel is the subset of a models.dev record that ishakat actually uses.
// Everything else (npm package, docs URL, weights, release dates beyond the
// deprecation hint) is dropped at parse time so the cached digest stays
// small: the full api.json is over 3 MB and this brings it down by an order
// of magnitude.
type MDModel struct {
	ID          string  `json:"id"`
	Name        string  `json:"name,omitempty"`
	Family      string  `json:"family,omitempty"`
	Context     int     `json:"context,omitempty"`
	Output      int     `json:"output,omitempty"`
	Cost        *MDCost `json:"cost,omitempty"`
	Tools       bool    `json:"tools,omitempty"`
	Reasoning   bool    `json:"reasoning,omitempty"`
	Vision      bool    `json:"vision,omitempty"`
	Attachments bool    `json:"attachments,omitempty"`
	JSONSchema  bool    `json:"json_schema,omitempty"`

	// Modalities is the declared OUTPUT modalities, not input: what a
	// model can accept and what it can emit are independent, and
	// docs/DESIGN-model-curation.md §1.2's first curation signal ("output
	// non-empty and lacks text") only makes sense against what comes
	// out. A model that accepts image/audio/video and answers in text
	// (gemini-3.5-flash) is a chat model; one that accepts text and
	// emits only audio (a TTS endpoint) is not, regardless of what it
	// accepts. Vision (below) is the input-side signal and stays
	// separate on purpose.
	Modalities []string `json:"modalities,omitempty"`

	OpenWeights bool   `json:"open_weights,omitempty"`
	ReleaseDate string `json:"release_date,omitempty"`

	// Status is models.dev's own lifecycle field: "deprecated", "beta",
	// "alpha", or absent. This is what lets `hide_deprecated` do anything at
	// all — see applyModelsDev in merge.go, and the design note in
	// docs/DESIGN-model-curation.md §1.1.
	Status string `json:"status,omitempty"`

	// Temperature is a pointer on purpose: absent must not collapse into
	// false. A model that omits the field is simply undocumented on this
	// axis, not evidence that it takes no temperature — conflating the two
	// would violate the "unknown is never a reason to hide" rule
	// (docs/DESIGN-model-curation.md §2, principle 10). Nothing reads this
	// yet; it exists so a future non-conversational filter (§1.2 of that
	// same design) has the field parsed and ready.
	Temperature *bool `json:"temperature,omitempty"`
}

// Caps turns the models.dev flags into the catalog's own capability set.
// Streaming is asserted because every service behind the OpenAI dialect
// streams; the day one does not, it will say so in its own discovery.
func (m MDModel) Caps() Caps {
	return Caps{
		Tools:       m.Tools,
		Vision:      m.Vision,
		Reasoning:   m.Reasoning,
		Streaming:   true,
		JSONSchema:  m.JSONSchema,
		Attachments: m.Attachments,
	}
}

// CatalogCost converts the models.dev prices. A record with no cost block
// returns nil, which means UNKNOWN and not free (§4.2).
func (m MDModel) CatalogCost() *Cost {
	if m.Cost == nil {
		return nil
	}
	return &Cost{
		In:         m.Cost.Input,
		Out:        m.Cost.Output,
		CacheRead:  m.Cost.CacheRead,
		CacheWrite: m.Cost.CacheWrite,
	}
}

// Index is the digested, cacheable form of both payloads plus the lookup
// tables the cascade needs.
type Index struct {
	V         int       `json:"v"`
	ETag      string    `json:"etag,omitempty"`
	MetaETag  string    `json:"meta_etag,omitempty"`
	FetchedAt time.Time `json:"fetched_at,omitzero"`

	// ByProvider is api.json: provider id → model id → record.
	ByProvider map[string]map[string]MDModel `json:"by_provider,omitempty"`

	// Agnostic is models.json: "vendor/model" → record.
	Agnostic map[string]MDModel `json:"agnostic,omitempty"`

	// Lazily built lookup tables; not serialized.
	normalized map[string]MDModel
	byFamily   map[string][]MDModel
}

// NewIndex returns an empty index.
func NewIndex() *Index {
	return &Index{V: DigestVersion, ByProvider: map[string]map[string]MDModel{}, Agnostic: map[string]MDModel{}}
}

// Count is the number of provider-scoped records, which is the "models"
// number reported in the §4.4 cache receipt.
func (ix *Index) Count() int {
	if ix == nil {
		return 0
	}
	n := 0
	for _, models := range ix.ByProvider {
		n += len(models)
	}
	return n
}

// Empty reports whether there is nothing to match against.
func (ix *Index) Empty() bool {
	return ix == nil || (ix.Count() == 0 && len(ix.Agnostic) == 0)
}

// wireAPIProvider is one entry of api.json.
type wireAPIProvider struct {
	ID     string                    `json:"id"`
	Name   string                    `json:"name"`
	Models map[string]wireMDModelRaw `json:"models"`
}

// wireMDModelRaw is the on-the-wire model record, before digesting.
type wireMDModelRaw struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Family           string `json:"family"`
	Attachment       bool   `json:"attachment"`
	Reasoning        bool   `json:"reasoning"`
	ToolCall         bool   `json:"tool_call"`
	StructuredOutput bool   `json:"structured_output"`
	OpenWeights      bool   `json:"open_weights"`
	ReleaseDate      string `json:"release_date"`

	// Status is the lifecycle field this whole change exists to read:
	// "deprecated" | "beta" | "alpha" | "" (absent, the vast majority).
	Status string `json:"status"`

	// Temperature stays a pointer through digest() too — see the field
	// comment on MDModel for why absent must not become false.
	Temperature *bool `json:"temperature"`

	Modalities struct {
		Input  []string `json:"input"`
		Output []string `json:"output"`
	} `json:"modalities"`
	Limit struct {
		Context int `json:"context"`
		Output  int `json:"output"`
	} `json:"limit"`
	Cost *MDCost `json:"cost"`
}

func (w wireMDModelRaw) digest(id string) MDModel {
	if w.ID != "" {
		id = w.ID
	}
	vision := false
	for _, in := range w.Modalities.Input {
		switch strings.ToLower(in) {
		case "image", "video":
			vision = true
		}
	}
	return MDModel{
		ID:          id,
		Name:        w.Name,
		Family:      w.Family,
		Context:     w.Limit.Context,
		Output:      w.Limit.Output,
		Cost:        w.Cost,
		Tools:       w.ToolCall,
		Reasoning:   w.Reasoning,
		Vision:      vision,
		Attachments: w.Attachment,
		JSONSchema:  w.StructuredOutput,
		Modalities:  w.Modalities.Output,
		OpenWeights: w.OpenWeights,
		ReleaseDate: w.ReleaseDate,
		Status:      w.Status,
		Temperature: w.Temperature,
	}
}

// ParseAPI digests api.json into the index. A broken individual record is
// skipped instead of failing the whole file: one bad entry in a catalog of
// two thousand must not leave the user with no metadata at all.
func (ix *Index) ParseAPI(raw []byte) error {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("catalog: unreadable api.json: %w", err)
	}
	if ix.ByProvider == nil {
		ix.ByProvider = map[string]map[string]MDModel{}
	}
	for provID, blob := range doc {
		var p wireAPIProvider
		if err := json.Unmarshal(blob, &p); err != nil {
			continue
		}
		if len(p.Models) == 0 {
			continue
		}
		id := p.ID
		if id == "" {
			id = provID
		}
		models := make(map[string]MDModel, len(p.Models))
		for modelID, m := range p.Models {
			d := m.digest(modelID)
			models[strings.ToLower(d.ID)] = d
		}
		ix.ByProvider[strings.ToLower(id)] = models
	}
	ix.invalidate()
	return nil
}

// ParseMeta digests models.json, the provider-agnostic base.
func (ix *Index) ParseMeta(raw []byte) error {
	var doc map[string]wireMDModelRaw
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("catalog: unreadable models.json: %w", err)
	}
	if ix.Agnostic == nil {
		ix.Agnostic = map[string]MDModel{}
	}
	for key, m := range doc {
		d := m.digest(key)
		ix.Agnostic[strings.ToLower(key)] = d
	}
	ix.invalidate()
	return nil
}

func (ix *Index) invalidate() {
	ix.normalized = nil
	ix.byFamily = nil
}

// MatchStage records which rung of the §4.3 cascade produced a match. It is
// exported because the test table asserts on it: "it matched" is not enough,
// the interesting part is *how*.
type MatchStage int

const (
	MatchNone MatchStage = iota

	// MatchExact is provider/wire_id straight against api.json.
	MatchExact

	// MatchVendor is the gateway case: the wire id itself carries the
	// vendor ("anthropic/claude-sonnet-4-5" served by omniroute), so the
	// first segment is tried as a models.dev provider.
	MatchVendor

	// MatchNormalized is after lowercasing and stripping "-latest", date
	// suffixes and duplicated vendor prefixes.
	MatchNormalized

	// MatchFamily is the last rung: the agnostic base, by family.
	MatchFamily
)

var matchStageNames = map[MatchStage]string{
	MatchNone:       "none",
	MatchExact:      "exact",
	MatchVendor:     "vendor",
	MatchNormalized: "normalized",
	MatchFamily:     "family",
}

func (s MatchStage) String() string {
	if n, ok := matchStageNames[s]; ok {
		return n
	}
	return "unknown"
}

// dateSuffix matches the version stamps providers append: -20250219,
// -2025-02-19, -0125.
var dateSuffix = regexp.MustCompile(`-(\d{8}|\d{4}-\d{2}-\d{2}|\d{4})$`)

// NormalizeID puts a model identifier in the shape used for the third rung
// of the cascade: lowercase, no "-latest", no date stamp, no duplicated
// vendor prefix ("anthropic/anthropic.claude-3" → "claude-3").
func NormalizeID(id string) string {
	s := strings.ToLower(strings.TrimSpace(id))
	s = strings.TrimSuffix(s, ":latest")
	s = strings.TrimSuffix(s, "-latest")
	s = strings.TrimSuffix(s, "@latest")

	// Some gateways separate the vendor with a dot instead of a slash.
	if _, tail, ok := strings.Cut(s, "/"); ok {
		s = tail
	}
	if i := strings.LastIndex(s, "."); i > 0 && i < len(s)-1 {
		head, tail := s[:i], s[i+1:]
		// Only treat a dot as a vendor separator when the head looks like a
		// vendor name and not like a version ("gpt-4.1" must survive).
		if !strings.ContainsAny(head, "0123456789") && len(tail) > 2 {
			s = tail
		}
	}
	s = dateSuffix.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

// normalizedIndex builds, once, the lookup by normalized id over both
// payloads. api.json wins over models.json when both have the same key,
// because a provider-scoped record is more specific.
func (ix *Index) normalizedIndex() map[string]MDModel {
	if ix.normalized != nil {
		return ix.normalized
	}
	out := make(map[string]MDModel, ix.Count()+len(ix.Agnostic))
	for key, m := range ix.Agnostic {
		out[NormalizeID(key)] = m
	}
	// Deterministic order so a duplicate key always resolves the same way.
	provIDs := make([]string, 0, len(ix.ByProvider))
	for p := range ix.ByProvider {
		provIDs = append(provIDs, p)
	}
	sort.Strings(provIDs)
	for _, p := range provIDs {
		for id, m := range ix.ByProvider[p] {
			out[NormalizeID(id)] = m
		}
	}
	ix.normalized = out
	return out
}

// familyIndex groups the agnostic base by family, for the last rung.
func (ix *Index) familyIndex() map[string][]MDModel {
	if ix.byFamily != nil {
		return ix.byFamily
	}
	out := map[string][]MDModel{}
	keys := make([]string, 0, len(ix.Agnostic))
	for k := range ix.Agnostic {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		m := ix.Agnostic[k]
		if m.Family == "" {
			continue
		}
		f := strings.ToLower(m.Family)
		out[f] = append(out[f], m)
	}
	ix.byFamily = out
	return out
}

// Lookup runs the cascade of §4.3 and stops at the first rung that answers.
//
// The order is deliberate — most specific first — because a wrong metadata
// match is worse than none: it would show a price and a context window that
// belong to a different model.
func (ix *Index) Lookup(providerID, wireID string) (MDModel, MatchStage) {
	if ix.Empty() || strings.TrimSpace(wireID) == "" {
		return MDModel{}, MatchNone
	}
	lowWire := strings.ToLower(strings.TrimSpace(wireID))

	// 1. provider/wire_id exactly as configured.
	if models, ok := ix.ByProvider[strings.ToLower(providerID)]; ok {
		if m, ok := models[lowWire]; ok {
			return m, MatchExact
		}
	}

	// 2. The gateway case: the wire id itself carries the vendor.
	if vendor, rest, ok := strings.Cut(lowWire, "/"); ok {
		if models, ok := ix.ByProvider[vendor]; ok {
			if m, ok := models[rest]; ok {
				return m, MatchVendor
			}
		}
		if m, ok := ix.Agnostic[lowWire]; ok {
			return m, MatchVendor
		}
	}

	// 3. Normalized on both sides.
	norm := ix.normalizedIndex()
	if m, ok := norm[NormalizeID(wireID)]; ok {
		return m, MatchNormalized
	}

	// 4. By family over the agnostic base. Only accepted when the family
	// name actually appears in the identifier, and the record picked is the
	// one with the longest common prefix, so "claude-sonnet-4-5-fast"
	// lands on a sonnet and not on the first claude in the map.
	if best, ok := ix.lookupFamily(NormalizeID(wireID)); ok {
		return best, MatchFamily
	}

	return MDModel{}, MatchNone
}

func (ix *Index) lookupFamily(norm string) (MDModel, bool) {
	if norm == "" {
		return MDModel{}, false
	}
	fams := ix.familyIndex()
	var bestFam string
	for fam := range fams {
		if !strings.Contains(norm, fam) {
			continue
		}
		if len(fam) > len(bestFam) {
			bestFam = fam
		}
	}
	if bestFam == "" {
		return MDModel{}, false
	}
	candidates := fams[bestFam]
	var best MDModel
	bestScore := -1
	for _, m := range candidates {
		score := commonPrefixLen(norm, NormalizeID(m.ID))
		if score > bestScore {
			best, bestScore = m, score
		}
	}
	return best, bestScore >= 0
}

func commonPrefixLen(a, b string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return i
}

// LoadDigest reads the sibling digest file. Like LoadCache it never fails
// loudly: a missing or corrupt digest just means the metadata will be
// fetched again.
func LoadDigest(path string) *Index {
	ix := NewIndex()
	if strings.TrimSpace(path) == "" {
		return ix
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ix
	}
	var got Index
	if err := json.Unmarshal(raw, &got); err != nil {
		return ix
	}
	if got.V != DigestVersion {
		return ix
	}
	if got.ByProvider == nil {
		got.ByProvider = map[string]map[string]MDModel{}
	}
	if got.Agnostic == nil {
		got.Agnostic = map[string]MDModel{}
	}
	return &got
}

// SaveDigest writes the digest atomically, next to catalog.json.
func (ix *Index) SaveDigest(path string) error {
	if ix == nil || strings.TrimSpace(path) == "" {
		return fmt.Errorf("catalog: no path to save the models.dev digest to")
	}
	ix.V = DigestVersion
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("catalog: could not create %s: %w", dir, err)
	}
	body, err := json.Marshal(ix)
	if err != nil {
		return fmt.Errorf("catalog: could not serialize the models.dev digest: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".modelsdev-*.tmp")
	if err != nil {
		return fmt.Errorf("catalog: could not create a temporary file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// DigestPath is the digest file that goes with a catalog.json path.
func DigestPath(cachePath string) string {
	if strings.TrimSpace(cachePath) == "" {
		return ""
	}
	dir := filepath.Dir(cachePath)
	base := filepath.Base(cachePath)
	ext := filepath.Ext(base)
	return filepath.Join(dir, strings.TrimSuffix(base, ext)+"-modelsdev"+ext)
}

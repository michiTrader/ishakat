package catalog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// CacheVersion is the "v" field of §4.4. A cache written by an older binary
// with a different shape is discarded rather than migrated: it costs one
// network refresh and avoids a whole class of decoding bugs.
const CacheVersion = 1

// DiscoveredModel is what a provider said about a model, already stripped of
// the transport but not yet normalized. It is catalog's own type on purpose:
// provider.RawModel lives in a package that imports net/http, and importing
// it here would drag HTTP into the transitive closure of internal/tui,
// breaking the §6.1 boundary test. internal/catalog/fetch does the (trivial)
// conversion.
type DiscoveredModel struct {
	WireID  string   `json:"wire_id"`
	Name    string   `json:"name,omitempty"`
	Context int      `json:"context,omitempty"`
	Output  int      `json:"output,omitempty"`
	Tags    []string `json:"tags,omitempty"`

	// Raw is the provider's original JSON entry. Gateways put their own
	// fields in there (pricing, family, limits) and merge.go knows how to
	// read the common ones. It is kept in the cache because re-deriving it
	// would need the network.
	Raw json.RawMessage `json:"raw,omitempty"`
}

// ProviderCache is the discovery result of one provider, as stored.
type ProviderCache struct {
	FetchedAt time.Time         `json:"fetched_at,omitzero"`
	OK        bool              `json:"ok"`
	Error     string            `json:"error,omitempty"`
	Models    []DiscoveredModel `json:"models,omitempty"`
}

// ModelsDevCache is the bookkeeping of §4.4 for the metadata source: the
// validator for the conditional request, when it was fetched and how many
// records it had.
//
// The payload itself is NOT here. Keeping 3 MB of api.json inside the file
// that has to be read on every start would blow the startup budget on
// Termux, so the digest lives in a sibling file (modelsdev.json) written by
// the same atomic dance. This struct is the receipt; Digest is the goods.
type ModelsDevCache struct {
	ETag      string    `json:"etag,omitempty"`
	FetchedAt time.Time `json:"fetched_at,omitzero"`
	Models    int       `json:"models,omitempty"`
	Error     string    `json:"error,omitempty"`
}

// Stat is the local usage record that feeds the fuzzy ranking of §4.5.
type Stat struct {
	UseCount   int       `json:"use_count,omitempty"`
	LastUsed   time.Time `json:"last_used,omitzero"`
	P50ms      int       `json:"p50_ms,omitempty"`
	FailStreak int       `json:"fail_streak,omitempty"`
}

// Cache is the on-disk file of §4.4: one JSON document under
// $XDG_CACHE_HOME/ishakat/catalog.json.
type Cache struct {
	V         int                      `json:"v"`
	FetchedAt time.Time                `json:"fetched_at,omitzero"`
	ModelsDev ModelsDevCache           `json:"modelsdev,omitzero"`
	Providers map[string]ProviderCache `json:"providers,omitempty"`
	Stats     map[string]Stat          `json:"stats,omitempty"`

	// Loaded records whether this came from an actual file. A cache that
	// was never written is not an error —first run is normal— but the
	// caller decides differently between "empty" and "missing".
	Loaded bool `json:"-"`

	// Path is where it was read from, so Save has a default.
	Path string `json:"-"`

	// Note explains why an existing file was ignored (unreadable, corrupt,
	// wrong version). It is surfaced, never swallowed.
	Note string `json:"-"`
}

// NewCache returns an empty, usable cache.
func NewCache(path string) *Cache {
	return &Cache{
		V:         CacheVersion,
		Providers: map[string]ProviderCache{},
		Stats:     map[string]Stat{},
		Path:      path,
	}
}

// LoadCache reads the cache file.
//
// It NEVER returns an error for a missing, unreadable or corrupt file: the
// startup path must not depend on the cache being intact, so the worst case
// degrades to an empty cache with a Note explaining it. An error is only
// returned for programmer mistakes (an empty path).
func LoadCache(path string) *Cache {
	c := NewCache(path)
	if strings.TrimSpace(path) == "" {
		c.Note = "no cache path configured"
		return c
	}

	raw, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		return c // first run; not a problem and not worth a note
	case err != nil:
		c.Note = fmt.Sprintf("cache unreadable (%v); starting from an empty one", err)
		return c
	}

	var got Cache
	if err := json.Unmarshal(raw, &got); err != nil {
		c.Note = fmt.Sprintf("corrupt cache (%v); it will be rewritten on the next refresh", err)
		return c
	}
	if got.V != CacheVersion {
		c.Note = fmt.Sprintf("cache with version %d, this build writes %d; discarded", got.V, CacheVersion)
		return c
	}

	got.Loaded = true
	got.Path = path
	if got.Providers == nil {
		got.Providers = map[string]ProviderCache{}
	}
	if got.Stats == nil {
		got.Stats = map[string]Stat{}
	}
	return &got
}

// Save writes the cache atomically: temporary file in the same directory,
// fsync, rename. §4.4 asks for exactly this so a Ctrl+C mid-write cannot
// leave a truncated JSON behind — rename is atomic within a filesystem, a
// partial write is not.
func (c *Cache) Save(path string) error {
	if strings.TrimSpace(path) == "" {
		path = c.Path
	}
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("catalog: no path to save the cache to")
	}
	c.V = CacheVersion
	c.Path = path

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("catalog: could not create %s: %w", dir, err)
	}

	body, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("catalog: could not serialize the cache: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".catalog-*.tmp")
	if err != nil {
		return fmt.Errorf("catalog: could not create a temporary file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeded

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("catalog: could not set permissions on %s: %w", tmpName, err)
	}
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return fmt.Errorf("catalog: could not write %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("catalog: could not flush %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("catalog: could not close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("catalog: could not replace %s: %w", path, err)
	}
	return nil
}

// Age is how long ago the cache was refreshed. A never-fetched cache reports
// a huge age, which makes every "is it expired?" check answer yes without a
// special case.
func (c *Cache) Age(now time.Time) time.Duration {
	if c == nil || c.FetchedAt.IsZero() {
		return time.Duration(1<<62 - 1)
	}
	if d := now.Sub(c.FetchedAt); d > 0 {
		return d
	}
	return 0
}

// Expired reports whether the TTL elapsed. A non-positive TTL means "always
// expired", which is how refresh = "startup" is expressed without a second
// flag.
func (c *Cache) Expired(ttl time.Duration, now time.Time) bool {
	if c == nil || !c.Loaded || c.FetchedAt.IsZero() {
		return true
	}
	if ttl <= 0 {
		return true
	}
	return c.Age(now) > ttl
}

// Provider returns the stored discovery of one provider.
func (c *Cache) Provider(id string) (ProviderCache, bool) {
	if c == nil || c.Providers == nil {
		return ProviderCache{}, false
	}
	pc, ok := c.Providers[id]
	return pc, ok
}

// SetProvider records a successful discovery.
func (c *Cache) SetProvider(id string, models []DiscoveredModel, at time.Time) {
	if c.Providers == nil {
		c.Providers = map[string]ProviderCache{}
	}
	c.Providers[id] = ProviderCache{FetchedAt: at, OK: true, Models: models}
}

// SetProviderError records a failed discovery WITHOUT dropping the models
// already known. §4.4 is explicit: with no network nothing visible happens
// beyond a staleness strip, and that only works if a failure preserves the
// previous list.
func (c *Cache) SetProviderError(id string, err error, at time.Time) {
	if c.Providers == nil {
		c.Providers = map[string]ProviderCache{}
	}
	prev := c.Providers[id]
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	c.Providers[id] = ProviderCache{
		FetchedAt: prev.FetchedAt,
		OK:        false,
		Error:     msg,
		Models:    prev.Models,
	}
	_ = at
}

// Stat returns the local statistics of a reference.
func (c *Cache) Stat(ref string) Stat {
	if c == nil || c.Stats == nil {
		return Stat{}
	}
	return c.Stats[ref]
}

// RecordUse bumps the counters after a turn. The latency is folded in with a
// cheap running approximation instead of keeping a sample window: this is a
// tiebreaker in a ranking, not telemetry, and the memory it would cost is
// not worth the extra precision.
func (c *Cache) RecordUse(ref string, latency time.Duration, at time.Time) {
	if strings.TrimSpace(ref) == "" {
		return
	}
	if c.Stats == nil {
		c.Stats = map[string]Stat{}
	}
	s := c.Stats[ref]
	s.UseCount++
	s.LastUsed = at
	s.FailStreak = 0
	if ms := int(latency / time.Millisecond); ms > 0 {
		if s.P50ms == 0 {
			s.P50ms = ms
		} else {
			s.P50ms = (s.P50ms*3 + ms) / 4
		}
	}
	c.Stats[ref] = s
}

// RecordFailure lengthens the failure streak, which is what puts a model in
// HealthCooling.
func (c *Cache) RecordFailure(ref string) {
	if strings.TrimSpace(ref) == "" {
		return
	}
	if c.Stats == nil {
		c.Stats = map[string]Stat{}
	}
	s := c.Stats[ref]
	s.FailStreak++
	c.Stats[ref] = s
}

// PruneStats keeps the file from growing forever: statistics for references
// that no longer exist are dropped, keeping at most max entries by recency.
func (c *Cache) PruneStats(alive map[string]bool, max int) {
	if c == nil || len(c.Stats) == 0 {
		return
	}
	for ref := range c.Stats {
		if len(alive) > 0 && !alive[ref] {
			delete(c.Stats, ref)
		}
	}
	if max <= 0 || len(c.Stats) <= max {
		return
	}
	type entry struct {
		ref string
		at  time.Time
	}
	all := make([]entry, 0, len(c.Stats))
	for ref, s := range c.Stats {
		all = append(all, entry{ref, s.LastUsed})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].at.After(all[j].at) })
	for _, e := range all[max:] {
		delete(c.Stats, e.ref)
	}
}

// OldestFetch is the oldest successful timestamp among the providers, which
// is the honest number behind "catalog from 3 days ago".
func (c *Cache) OldestFetch() time.Time {
	if c == nil {
		return time.Time{}
	}
	var oldest time.Time
	for _, pc := range c.Providers {
		if pc.FetchedAt.IsZero() {
			continue
		}
		if oldest.IsZero() || pc.FetchedAt.Before(oldest) {
			oldest = pc.FetchedAt
		}
	}
	if oldest.IsZero() {
		return c.FetchedAt
	}
	return oldest
}

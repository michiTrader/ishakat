package catalog

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadCacheMissingFileIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	c := LoadCache(filepath.Join(dir, "does-not-exist.json"))
	if c.Loaded {
		t.Error("a missing cache file must report Loaded == false")
	}
	if c.Note != "" {
		t.Errorf("a missing file on first run is not worth a Note, got %q", c.Note)
	}
	if len(c.Providers) != 0 || len(c.Stats) != 0 {
		t.Error("an empty cache must still have usable (non-nil) maps")
	}
}

func TestLoadCacheCorruptFileDegradesGracefully(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := LoadCache(path)
	if c.Loaded {
		t.Error("a corrupt cache must report Loaded == false")
	}
	if c.Note == "" {
		t.Error("a corrupt cache must explain itself in Note")
	}
}

func TestLoadCacheWrongVersionIsDiscarded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.json")
	if err := os.WriteFile(path, []byte(`{"v":999}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c := LoadCache(path)
	if c.Loaded {
		t.Error("a cache written by a future/incompatible version must be discarded, not migrated")
	}
}

func TestCacheSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.json")

	c := NewCache(path)
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	c.SetProvider("omniroute", []DiscoveredModel{
		{WireID: "anthropic/claude-sonnet-4-5", Name: "Claude Sonnet 4.5", Context: 200000},
	}, now)
	c.RecordUse("omniroute/anthropic/claude-sonnet-4-5", 500*time.Millisecond, now)
	c.FetchedAt = now

	if err := c.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// No stray temp files must be left behind.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly one file after Save, found %d: %v", len(entries), entries)
	}

	got := LoadCache(path)
	if !got.Loaded {
		t.Fatal("round-tripped cache must load with Loaded == true")
	}
	pc, ok := got.Provider("omniroute")
	if !ok || !pc.OK || len(pc.Models) != 1 {
		t.Fatalf("provider round trip broke: %+v (ok=%v)", pc, ok)
	}
	if pc.Models[0].WireID != "anthropic/claude-sonnet-4-5" {
		t.Errorf("wire id round trip broke: %q", pc.Models[0].WireID)
	}
	stat := got.Stat("omniroute/anthropic/claude-sonnet-4-5")
	if stat.UseCount != 1 || stat.P50ms == 0 {
		t.Errorf("stat round trip broke: %+v", stat)
	}
}

func TestCacheSavePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.json")
	c := NewCache(path)
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("cache file permissions = %o, want 0600 (it may contain provider metadata)", perm)
	}
}

func TestSetProviderErrorKeepsPreviousModels(t *testing.T) {
	// §4.4: a failed refresh must be invisible beyond a staleness strip —
	// the previously known models stay in the cache.
	c := NewCache("")
	now := time.Now()
	c.SetProvider("omniroute", []DiscoveredModel{{WireID: "gpt-5"}}, now)

	c.SetProviderError("omniroute", errors.New("network unreachable"), now.Add(time.Hour))

	pc, ok := c.Provider("omniroute")
	if !ok {
		t.Fatal("provider entry disappeared entirely after a failed refresh")
	}
	if pc.OK {
		t.Error("OK must be false after SetProviderError")
	}
	if pc.Error == "" {
		t.Error("Error must be recorded")
	}
	if len(pc.Models) != 1 || pc.Models[0].WireID != "gpt-5" {
		t.Errorf("the previous model list must survive a failed refresh, got %+v", pc.Models)
	}
}

func TestExpiredRules(t *testing.T) {
	now := time.Now()
	fresh := NewCache("")
	fresh.Loaded = true
	fresh.FetchedAt = now.Add(-1 * time.Hour)

	if fresh.Expired(24*time.Hour, now) {
		t.Error("a 1h-old cache with a 24h TTL must not be expired")
	}
	if !fresh.Expired(30*time.Minute, now) {
		t.Error("a 1h-old cache with a 30m TTL must be expired")
	}
	// refresh = "startup" is expressed as a non-positive TTL: always expired.
	if !fresh.Expired(0, now) {
		t.Error("a non-positive TTL must always report expired (refresh = \"startup\")")
	}

	var never *Cache
	if !never.Expired(24*time.Hour, now) {
		t.Error("a nil cache must report expired")
	}

	unloaded := NewCache("")
	if !unloaded.Expired(24*time.Hour, now) {
		t.Error("a cache that was never loaded from disk must report expired")
	}
}

func TestRecordFailureAndPruneStats(t *testing.T) {
	c := NewCache("")
	c.RecordUse("a/1", time.Millisecond, time.Now())
	c.RecordUse("a/2", time.Millisecond, time.Now())
	c.RecordFailure("a/1")
	c.RecordFailure("a/1")

	if c.Stat("a/1").FailStreak != 2 {
		t.Errorf("FailStreak = %d, want 2", c.Stat("a/1").FailStreak)
	}

	// a/2 no longer exists in the catalog: PruneStats must drop it.
	c.PruneStats(map[string]bool{"a/1": true}, 0)
	if _, ok := c.Stats["a/2"]; ok {
		t.Error("PruneStats must drop stats for references no longer alive")
	}
	if _, ok := c.Stats["a/1"]; !ok {
		t.Error("PruneStats must keep stats for references still alive")
	}
}

func TestPruneStatsCapsByRecency(t *testing.T) {
	c := NewCache("")
	base := time.Now()
	alive := map[string]bool{}
	for i := 0; i < 5; i++ {
		ref := string(rune('a' + i))
		alive[ref] = true
		c.RecordUse(ref, time.Millisecond, base.Add(time.Duration(i)*time.Minute))
	}
	c.PruneStats(alive, 2)
	if len(c.Stats) != 2 {
		t.Fatalf("expected exactly 2 stats to survive the cap, got %d", len(c.Stats))
	}
	// The two most recent (largest offset) must be the ones kept.
	if _, ok := c.Stats["e"]; !ok {
		t.Error("the most recently used reference must survive PruneStats")
	}
	if _, ok := c.Stats["d"]; !ok {
		t.Error("the second most recently used reference must survive PruneStats")
	}
}

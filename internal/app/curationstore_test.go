package app

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichiTrader/ishakat/internal/curation"
	"github.com/MichiTrader/ishakat/internal/xdg"
)

func TestFileCurationStoreHidePersistsAndReloads(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "curation.json")

	store := newFileCurationStore(path)
	if store.IsHidden("omni/son45") {
		t.Fatal("a fresh store should not report anything hidden")
	}
	if err := store.Hide("omni/son45"); err != nil {
		t.Fatalf("Hide: %v", err)
	}
	if !store.IsHidden("omni/son45") {
		t.Error("IsHidden should be true immediately after Hide")
	}
	if got := store.Reason("omni/son45"); got != "hidden by you" {
		t.Errorf("Reason = %q, want %q", got, "hidden by you")
	}

	// Reload from disk into a fresh store and confirm the hide survived.
	reloaded := newFileCurationStore(path)
	if !reloaded.IsHidden("omni/son45") {
		t.Error("a reloaded store should still report the ref hidden")
	}

	onDisk, err := curation.Load(path)
	if err != nil {
		t.Fatalf("curation.Load: %v", err)
	}
	if !onDisk.IsHidden("omni/son45") {
		t.Error("curation.json on disk should record the hide")
	}
}

func TestFileCurationStoreUnhidePersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "curation.json")

	store := newFileCurationStore(path)
	if err := store.Hide("omni/son45"); err != nil {
		t.Fatalf("Hide: %v", err)
	}
	if err := store.Unhide("omni/son45"); err != nil {
		t.Fatalf("Unhide: %v", err)
	}
	if store.IsHidden("omni/son45") {
		t.Error("IsHidden should be false immediately after Unhide")
	}
	if got := store.Reason("omni/son45"); got != "" {
		t.Errorf("Reason after Unhide = %q, want empty", got)
	}

	reloaded := newFileCurationStore(path)
	if reloaded.IsHidden("omni/son45") {
		t.Error("a reloaded store should not report the un-hidden ref as hidden")
	}
}

// TestFileCurationStoreKeepUnhidesAndPersists pins /model keep's own verb:
// Keep pulls a ref OUT of the hide list (unlike Unhide, it also has to work
// for a ref that was never hidden through this store at all — the common
// case, a model [catalog.curate]'s own automatic rules hid), and the pin
// survives a reload.
func TestFileCurationStoreKeepUnhidesAndPersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "curation.json")

	store := newFileCurationStore(path)
	if err := store.Hide("omni/son45"); err != nil {
		t.Fatalf("Hide: %v", err)
	}
	if err := store.Keep("omni/son45"); err != nil {
		t.Fatalf("Keep: %v", err)
	}
	if store.IsHidden("omni/son45") {
		t.Error("Keep should remove the ref from the hide list")
	}

	onDisk, err := curation.Load(path)
	if err != nil {
		t.Fatalf("curation.Load: %v", err)
	}
	if !onDisk.IsKept("omni/son45") {
		t.Error("curation.json on disk should record the keep")
	}
}

// TestFileCurationStoreHiddenListsEveryHiddenRefSorted pins /models
// hidden's own data source: Hidden() has to enumerate every ref this
// store's hide list holds, sorted, regardless of insertion order.
func TestFileCurationStoreHiddenListsEveryHiddenRefSorted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "curation.json")

	store := newFileCurationStore(path)
	if got := store.Hidden(); len(got) != 0 {
		t.Fatalf("a fresh store's Hidden() = %v, want empty", got)
	}
	if err := store.Hide("b/model-b"); err != nil {
		t.Fatalf("Hide: %v", err)
	}
	if err := store.Hide("a/model-a"); err != nil {
		t.Fatalf("Hide: %v", err)
	}

	got := store.Hidden()
	want := []string{"a/model-a", "b/model-b"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("Hidden() = %v, want %v", got, want)
	}
}

// TestFileCurationStoreResetDropsHidesKeepsKeptAndPersists pins /models
// reset's own definition (design doc §2.1): every hide is dropped, but a
// Keep is left alone — a reset undoes accidental noise, not a deliberate
// pin.
func TestFileCurationStoreResetDropsHidesKeepsKeptAndPersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "curation.json")

	store := newFileCurationStore(path)
	if err := store.Hide("omni/hidden-one"); err != nil {
		t.Fatalf("Hide: %v", err)
	}
	if err := store.Keep("omni/kept-one"); err != nil {
		t.Fatalf("Keep: %v", err)
	}

	if err := store.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if store.IsHidden("omni/hidden-one") {
		t.Error("Reset should have dropped the hide")
	}
	if got := store.Hidden(); len(got) != 0 {
		t.Errorf("Hidden() after Reset = %v, want empty", got)
	}

	reloaded := newFileCurationStore(path)
	if reloaded.IsHidden("omni/hidden-one") {
		t.Error("a reloaded store should not see the reset hide come back")
	}
	onDisk, err := curation.Load(path)
	if err != nil {
		t.Fatalf("curation.Load: %v", err)
	}
	if !onDisk.IsKept("omni/kept-one") {
		t.Error("Reset must not touch Kept")
	}
}

// TestFileCurationStoreNeverTouchesConfigTOML is design doc §2.3's first
// closing criterion, asserted byte-for-byte: a full Layer 2 cycle — Hide,
// Keep (which also un-hides), Unhide, and Reset, on both fileCurationStore
// directly and through curationRules' own config.toml-adjacent read path —
// must leave config.toml's bytes, comments included, completely
// unchanged. This is principle 7's own "the config file the user wrote
// stays byte-identical" (§2), and the mechanism that makes it true is
// structural: fileCurationStore only ever calls curation.Save against its
// own path (xdg.CurationFile(), a different file in $XDG_STATE_HOME, never
// $XDG_CONFIG_HOME/ishakat/config.toml) — this test pins that fact against
// a real hand-written fixture with comments, rather than only trusting the
// source read.
func TestFileCurationStoreNeverTouchesConfigTOML(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configDir)
	stateDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateDir)

	// A realistic hand-written config.toml, comments and all — exactly the
	// kind of file principle 7 says must survive untouched. Curation has
	// no reason to ever open this path, but the fixture exists so a
	// regression that DID open it (a future refactor that threads cfg
	// into the wrong writer, say) would be caught here rather than only
	// showing up as a bug report about vanished comments.
	fixture := "# personal config, hand-tuned -- do not clobber my comments\n" +
		"[app]\n" +
		"default_model = \"omni/son45\" # my daily driver\n" +
		"\n" +
		"[catalog.curate]\n" +
		"chat_only = true\n"
	if err := os.MkdirAll(filepath.Dir(xdg.ConfigFile()), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(xdg.ConfigFile(), []byte(fixture), 0o644); err != nil {
		t.Fatalf("WriteFile(config.toml): %v", err)
	}

	before, err := os.ReadFile(xdg.ConfigFile())
	if err != nil {
		t.Fatalf("ReadFile(config.toml) before: %v", err)
	}

	// A full Layer 2 cycle through the real, on-disk-backed store --
	// exactly what ctrl+x/ctrl+h, /model hide|keep, and /models reset all
	// eventually call.
	store := newFileCurationStore(xdg.CurationFile())
	if err := store.Hide("google/gemini-embedding-2"); err != nil {
		t.Fatalf("Hide: %v", err)
	}
	if err := store.Keep("other/kept-model"); err != nil {
		t.Fatalf("Keep: %v", err)
	}
	if err := store.Unhide("google/gemini-embedding-2"); err != nil {
		t.Fatalf("Unhide: %v", err)
	}
	if err := store.Hide("omni/hidden-again"); err != nil {
		t.Fatalf("Hide (again): %v", err)
	}
	if err := store.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}

	// curationRules is the OTHER read path near config.toml's own -- it
	// loads curation.json (via xdg.CurationFile()) inside the same
	// function that also reads cfg.Catalog.Curate off an already-loaded
	// *config.Config, so this exercises that neighbor too, not only the
	// picker/slash-command write path above.
	_ = curationRules(nil)

	after, err := os.ReadFile(xdg.ConfigFile())
	if err != nil {
		t.Fatalf("ReadFile(config.toml) after: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("config.toml changed by Layer 2 curation:\nbefore:\n%s\nafter:\n%s", before, after)
	}

	// curation.json itself DID change (sanity check that the cycle above
	// actually exercised the real write path, rather than this test
	// passing vacuously because nothing was written anywhere).
	if _, err := os.Stat(xdg.CurationFile()); err != nil {
		t.Fatalf("expected curation.json to exist after a real Hide/Keep/Reset cycle: %v", err)
	}
}

package config_test

import (
	"os"
	"testing"

	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/xdg"
)

// --- P3c: SetAppModel ----------------------------------------------------

func TestSetAppModelDefault(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := config.SetAppModel(config.AppModelDefault, "gemini-direct/gemini-2.5-flash"); err != nil {
		t.Fatalf("SetAppModel() error = %v", err)
	}
	cfg, err := config.Load(config.Options{SkipProject: true})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.App.DefaultModel != "gemini-direct/gemini-2.5-flash" {
		t.Errorf("DefaultModel = %q, want %q", cfg.App.DefaultModel, "gemini-direct/gemini-2.5-flash")
	}
}

func TestSetAppModelCompactAndFallback(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := config.SetAppModel(config.AppModelCompact, "gemini-direct/gemini-2.5-flash-lite"); err != nil {
		t.Fatalf("SetAppModel(compact) error = %v", err)
	}
	if err := config.SetAppModel(config.AppModelFallback, "gemini-direct/gemini-2.5-flash"); err != nil {
		t.Fatalf("SetAppModel(fallback) error = %v", err)
	}
	cfg, err := config.Load(config.Options{SkipProject: true})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.App.CompactModel != "gemini-direct/gemini-2.5-flash-lite" {
		t.Errorf("CompactModel = %q, want the lite model", cfg.App.CompactModel)
	}
	if cfg.App.FallbackModel != "gemini-direct/gemini-2.5-flash" {
		t.Errorf("FallbackModel = %q, want the flash model", cfg.App.FallbackModel)
	}
}

// TestSetAppModelEmptyCompactResetsToFollowDefault: an empty ref is valid
// for compact_model/fallback_model — ResolveModel's own empty-string rule
// treats "" as "same as default_model" — but must still be rejected for
// default_model, since there is nothing else default_model could fall
// back to.
func TestSetAppModelEmptyCompactResetsToFollowDefault(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := config.SetAppModel(config.AppModelCompact, ""); err != nil {
		t.Fatalf("SetAppModel(compact, \"\") error = %v, want nil", err)
	}
	if err := config.SetAppModel(config.AppModelDefault, ""); err == nil {
		t.Fatal("SetAppModel(default, \"\") error = nil, want an error")
	}
}

func TestSetAppModelPreservesOtherAppSettings(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := config.SetAppModel(config.AppModelDefault, "openai/gpt-4o-mini"); err != nil {
		t.Fatalf("first SetAppModel() error = %v", err)
	}
	if err := config.SetAppModel(config.AppModelCompact, "openai/gpt-4o-mini"); err != nil {
		t.Fatalf("second SetAppModel() error = %v", err)
	}
	cfg, err := config.Load(config.Options{SkipProject: true})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.App.DefaultModel != "openai/gpt-4o-mini" {
		t.Errorf("the second SetAppModel call clobbered the first: DefaultModel = %q", cfg.App.DefaultModel)
	}
}

// --- P3c: SetAlias / RemoveAlias ------------------------------------------

func TestSetAliasCreatesNewEntry(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := config.SetAlias("smart", "gemini-direct/gemini-2.5-pro"); err != nil {
		t.Fatalf("SetAlias() error = %v", err)
	}
	cfg, err := config.Load(config.Options{SkipProject: true})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Alias["smart"] != "gemini-direct/gemini-2.5-pro" {
		t.Errorf("alias[smart] = %q, want the pro model. Alias = %+v", cfg.Alias["smart"], cfg.Alias)
	}
}

// TestSetAliasOverwritesCaseInsensitively mirrors lookupAlias's own
// case-insensitive lookup (internal/app/modelref.go): setting "Smart" after
// "smart" already exists must update the existing key, not create a
// second, differently-cased one.
func TestSetAliasOverwritesCaseInsensitively(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := config.SetAlias("smart", "openai/gpt-4o"); err != nil {
		t.Fatalf("first SetAlias() error = %v", err)
	}
	if err := config.SetAlias("Smart", "openai/gpt-4.1"); err != nil {
		t.Fatalf("second SetAlias() error = %v", err)
	}

	cfg, err := config.Load(config.Options{SkipProject: true})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	found := 0
	for k, v := range cfg.Alias {
		if k == "smart" || k == "Smart" {
			found++
			if v != "openai/gpt-4.1" {
				t.Errorf("alias[%s] = %q, want the second value to have won", k, v)
			}
		}
	}
	if found != 1 {
		t.Fatalf("want exactly one smart/Smart entry after the overwrite, found %d", found)
	}
}

func TestRemoveAlias(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := config.SetAlias("smart", "openai/gpt-4o"); err != nil {
		t.Fatalf("SetAlias() error = %v", err)
	}
	if err := config.RemoveAlias("SMART"); err != nil { // case-insensitive removal
		t.Fatalf("RemoveAlias() error = %v", err)
	}

	cfg, err := config.Load(config.Options{SkipProject: true})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if _, ok := cfg.Alias["smart"]; ok {
		t.Error("alias[smart] still present after RemoveAlias")
	}
}

// TestRemoveAliasOfMissingNameIsNotAnError: removing something that was
// never there leaves the caller's desired end state ("this alias name
// doesn't resolve to anything") already true.
func TestRemoveAliasOfMissingNameIsNotAnError(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := config.RemoveAlias("does-not-exist"); err != nil {
		t.Fatalf("RemoveAlias() of a missing name: error = %v, want nil", err)
	}
}

// --- P3c: AddFavorite / RemoveFavorite ------------------------------------

func TestAddFavorite(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := config.AddFavorite("gemini-direct/gemini-2.5-flash"); err != nil {
		t.Fatalf("AddFavorite() error = %v", err)
	}
	cfg, err := config.Load(config.Options{SkipProject: true})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.Favorites.List) != 1 || cfg.Favorites.List[0] != "gemini-direct/gemini-2.5-flash" {
		t.Errorf("Favorites.List = %v, want exactly one entry", cfg.Favorites.List)
	}
}

// TestAddFavoriteIsIdempotent: adding the same ref twice must not create a
// duplicate entry.
func TestAddFavoriteIsIdempotent(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := config.AddFavorite("openai/gpt-4o-mini"); err != nil {
		t.Fatalf("first AddFavorite() error = %v", err)
	}
	if err := config.AddFavorite("openai/gpt-4o-mini"); err != nil {
		t.Fatalf("second AddFavorite() error = %v", err)
	}
	cfg, err := config.Load(config.Options{SkipProject: true})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.Favorites.List) != 1 {
		t.Errorf("Favorites.List = %v, want exactly one entry (no duplicate)", cfg.Favorites.List)
	}
}

func TestAddFavoriteMultipleEntries(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := config.AddFavorite("openai/gpt-4o-mini"); err != nil {
		t.Fatalf("AddFavorite() error = %v", err)
	}
	if err := config.AddFavorite("gemini-direct/gemini-2.5-flash"); err != nil {
		t.Fatalf("AddFavorite() error = %v", err)
	}
	cfg, err := config.Load(config.Options{SkipProject: true})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.Favorites.List) != 2 {
		t.Fatalf("Favorites.List = %v, want 2 entries", cfg.Favorites.List)
	}
}

func TestRemoveFavorite(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := config.AddFavorite("openai/gpt-4o-mini"); err != nil {
		t.Fatalf("AddFavorite() error = %v", err)
	}
	if err := config.AddFavorite("gemini-direct/gemini-2.5-flash"); err != nil {
		t.Fatalf("AddFavorite() error = %v", err)
	}
	if err := config.RemoveFavorite("openai/gpt-4o-mini"); err != nil {
		t.Fatalf("RemoveFavorite() error = %v", err)
	}

	cfg, err := config.Load(config.Options{SkipProject: true})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.Favorites.List) != 1 || cfg.Favorites.List[0] != "gemini-direct/gemini-2.5-flash" {
		t.Errorf("Favorites.List = %v, want only gemini-direct left", cfg.Favorites.List)
	}
}

func TestRemoveFavoriteOfMissingRefIsNotAnError(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := config.RemoveFavorite("does-not-exist/anywhere"); err != nil {
		t.Fatalf("RemoveFavorite() of a missing ref: error = %v, want nil", err)
	}
}

// TestSetAppModelWritesReadableConfigFile is a light guard mirroring
// SaveProviderConnection's own comment: config.toml must stay 0644
// (shareable), not 0600, after any of these mutators write it.
func TestSetAppModelWritesReadableConfigFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := config.SetAppModel(config.AppModelDefault, "openai/gpt-4o-mini"); err != nil {
		t.Fatalf("SetAppModel() error = %v", err)
	}
	info, err := os.Stat(xdg.ConfigFile())
	if err != nil {
		t.Fatalf("stat config file: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o644 {
		t.Errorf("config.toml mode = %o, want 0644", mode)
	}
}

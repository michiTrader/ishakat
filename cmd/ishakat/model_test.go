package main

import (
	"strings"
	"testing"

	"github.com/MichiTrader/ishakat/internal/config"
)

// --- ishakat model set -----------------------------------------------------

// TestCmdModelSetNoFlagSetsDefault covers the documented default role:
// `ishakat model set <ref>` with no flag at all sets app.default_model.
func TestCmdModelSetNoFlagSetsDefault(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	if code := cmdModel([]string{"set", "google/gemini-2.5-flash"}); code != 0 {
		t.Fatalf("cmdModel([set ref]) = %d, want 0", code)
	}
	cfg, err := config.Load(config.Options{SkipProject: true})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.App.DefaultModel != "google/gemini-2.5-flash" {
		t.Errorf("DefaultModel = %q, want the gemini ref", cfg.App.DefaultModel)
	}
}

// TestCmdModelSetRefFirstThenCompactFlag is THE regression test for the
// flag.Parse "stop at first positional" bug: every documented usage
// example puts <ref> before the role flag ("model set <ref> --compact"),
// which is exactly the ordering flag.Parse mishandles unless args are
// partitioned by hand first (see splitFlagsFromPositionals). Before that
// fix this invocation failed with "got extra arguments: --compact" instead
// of setting compact_model.
func TestCmdModelSetRefFirstThenCompactFlag(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	if code := cmdModel([]string{"set", "google/gemini-2.5-flash-lite", "--compact"}); code != 0 {
		t.Fatalf("cmdModel([set ref --compact]) = %d, want 0", code)
	}
	cfg, err := config.Load(config.Options{SkipProject: true})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.App.CompactModel != "google/gemini-2.5-flash-lite" {
		t.Errorf("CompactModel = %q, want the lite ref (ref-before-flag ordering must work)", cfg.App.CompactModel)
	}
	if cfg.App.DefaultModel != "" {
		t.Errorf("DefaultModel = %q, want unset (--compact must not also set default_model)", cfg.App.DefaultModel)
	}
}

// TestCmdModelSetRefFirstThenFallbackShortFlag covers the short-flag form
// of the same ref-before-flag ordering, with -f instead of --fallback.
func TestCmdModelSetRefFirstThenFallbackShortFlag(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	if code := cmdModel([]string{"set", "openai/gpt-4o-mini", "-f"}); code != 0 {
		t.Fatalf("cmdModel([set ref -f]) = %d, want 0", code)
	}
	cfg, err := config.Load(config.Options{SkipProject: true})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.App.FallbackModel != "openai/gpt-4o-mini" {
		t.Errorf("FallbackModel = %q, want the gpt-4o-mini ref", cfg.App.FallbackModel)
	}
}

// TestCmdModelSetAllSetsAllThreeKeys covers --all: default_model,
// compact_model and fallback_model must all be set to the same ref.
func TestCmdModelSetAllSetsAllThreeKeys(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	if code := cmdModel([]string{"set", "google/gemini-2.5-pro", "--all"}); code != 0 {
		t.Fatalf("cmdModel([set ref --all]) = %d, want 0", code)
	}
	cfg, err := config.Load(config.Options{SkipProject: true})
	if err != nil {
		t.Fatal(err)
	}
	want := "google/gemini-2.5-pro"
	if cfg.App.DefaultModel != want || cfg.App.CompactModel != want || cfg.App.FallbackModel != want {
		t.Errorf("after --all: default=%q compact=%q fallback=%q, want all %q",
			cfg.App.DefaultModel, cfg.App.CompactModel, cfg.App.FallbackModel, want)
	}
}

// TestCmdModelSetEmptyRefResetsCompact covers `ishakat model set "" --compact`,
// the documented way to reset compact_model back to "follow default_model".
func TestCmdModelSetEmptyRefResetsCompact(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	if code := cmdModel([]string{"set", "openai/gpt-4o", "--compact"}); code != 0 {
		t.Fatalf("first cmdModel([set ref --compact]) = %d, want 0", code)
	}
	if code := cmdModel([]string{"set", "", "--compact"}); code != 0 {
		t.Fatalf("cmdModel([set \"\" --compact]) = %d, want 0", code)
	}
	cfg, err := config.Load(config.Options{SkipProject: true})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.App.CompactModel != "" {
		t.Errorf("CompactModel = %q, want reset to \"\" (follows default_model)", cfg.App.CompactModel)
	}
}

// TestCmdModelSetEmptyRefRejectedForDefault: default_model has nothing to
// fall back to, so an empty ref must be rejected there — mirrors
// TestSetAppModelEmptyCompactResetsToFollowDefault (internal/config).
func TestCmdModelSetEmptyRefRejectedForDefault(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	if code := cmdModel([]string{"set", ""}); code == 0 {
		t.Error("cmdModel([set \"\"]) = 0, want a non-zero exit (empty ref invalid for default_model)")
	}
}

// TestCmdModelSetMultipleRoleFlagsRejected: passing more than one role
// flag at once is ambiguous and must be rejected with a usage error, not
// silently pick one.
func TestCmdModelSetMultipleRoleFlagsRejected(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	if code := cmdModel([]string{"set", "openai/gpt-4o", "--compact", "--fallback"}); code != 2 {
		t.Errorf("cmdModel([set ref --compact --fallback]) = %d, want 2 (usage error)", code)
	}
}

// TestCmdModelSetNoArgsIsUsageError: no ref at all must be a usage error,
// not a crash or a silent no-op.
func TestCmdModelSetNoArgsIsUsageError(t *testing.T) {
	if code := cmdModel([]string{"set"}); code != 2 {
		t.Errorf("cmdModel([set]) = %d, want 2", code)
	}
}

// TestCmdModelSetExtraPositionalArgsRejected: a second positional argument
// after <ref> (not a flag) must be reported as an extra-argument usage
// error rather than silently ignored.
func TestCmdModelSetExtraPositionalArgsRejected(t *testing.T) {
	if code := cmdModel([]string{"set", "openai/gpt-4o", "extra-word"}); code != 2 {
		t.Errorf("cmdModel([set ref extra-word]) = %d, want 2", code)
	}
}

// --- ishakat model alias ----------------------------------------------------

func TestCmdModelAliasSet(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	if code := cmdModel([]string{"alias", "set", "smart", "google/gemini-2.5-pro"}); code != 0 {
		t.Fatalf("cmdModel([alias set smart ref]) = %d, want 0", code)
	}
	cfg, err := config.Load(config.Options{SkipProject: true})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Alias["smart"] != "google/gemini-2.5-pro" {
		t.Errorf("alias[smart] = %q, want the pro ref", cfg.Alias["smart"])
	}
}

func TestCmdModelAliasSetWrongArgCountIsUsageError(t *testing.T) {
	if code := cmdModel([]string{"alias", "set", "smart"}); code != 2 {
		t.Errorf("cmdModel([alias set smart]) = %d, want 2 (missing ref)", code)
	}
}

func TestCmdModelAliasRemove(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	if code := cmdModel([]string{"alias", "set", "smart", "openai/gpt-4o"}); code != 0 {
		t.Fatalf("cmdModel([alias set]) = %d, want 0", code)
	}
	if code := cmdModel([]string{"alias", "remove", "smart"}); code != 0 {
		t.Fatalf("cmdModel([alias remove smart]) = %d, want 0", code)
	}
	cfg, err := config.Load(config.Options{SkipProject: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Alias["smart"]; ok {
		t.Error("alias[smart] still present after remove")
	}
}

func TestCmdModelAliasUnknownSubcommandIsUsageError(t *testing.T) {
	if code := cmdModel([]string{"alias", "frobnicate"}); code != 2 {
		t.Errorf("cmdModel([alias frobnicate]) = %d, want 2", code)
	}
}

// --- ishakat model favorite --------------------------------------------------

func TestCmdModelFavoriteAdd(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	if code := cmdModel([]string{"favorite", "add", "google/gemini-2.5-flash"}); code != 0 {
		t.Fatalf("cmdModel([favorite add ref]) = %d, want 0", code)
	}
	cfg, err := config.Load(config.Options{SkipProject: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Favorites.List) != 1 || cfg.Favorites.List[0] != "google/gemini-2.5-flash" {
		t.Errorf("Favorites.List = %v, want exactly one entry", cfg.Favorites.List)
	}
}

// TestCmdModelFavoriteAliases: "favorites" and "fav" must both dispatch to
// the same favorite subcommand (see cmdModel's switch).
func TestCmdModelFavoriteAliases(t *testing.T) {
	for _, alias := range []string{"favorite", "favorites", "fav"} {
		dir := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", dir)

		if code := cmdModel([]string{alias, "add", "openai/gpt-4o-mini"}); code != 0 {
			t.Errorf("cmdModel([%s add ref]) = %d, want 0", alias, code)
		}
	}
}

func TestCmdModelFavoriteRemove(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	if code := cmdModel([]string{"favorite", "add", "openai/gpt-4o-mini"}); code != 0 {
		t.Fatalf("cmdModel([favorite add]) = %d, want 0", code)
	}
	if code := cmdModel([]string{"favorite", "remove", "openai/gpt-4o-mini"}); code != 0 {
		t.Fatalf("cmdModel([favorite remove ref]) = %d, want 0", code)
	}
	cfg, err := config.Load(config.Options{SkipProject: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Favorites.List) != 0 {
		t.Errorf("Favorites.List = %v, want empty after remove", cfg.Favorites.List)
	}
}

func TestCmdModelFavoriteWrongArgCountIsUsageError(t *testing.T) {
	if code := cmdModel([]string{"favorite", "add"}); code != 2 {
		t.Errorf("cmdModel([favorite add]) = %d, want 2 (missing ref)", code)
	}
}

func TestCmdModelFavoriteUnknownSubcommandIsUsageError(t *testing.T) {
	if code := cmdModel([]string{"favorite", "frobnicate", "ref"}); code != 2 {
		t.Errorf("cmdModel([favorite frobnicate ref]) = %d, want 2", code)
	}
}

// --- ishakat model (top-level dispatch) -------------------------------------

func TestCmdModelNoArgsIsUsageError(t *testing.T) {
	if code := cmdModel(nil); code != 2 {
		t.Errorf("cmdModel(nil) = %d, want 2", code)
	}
}

func TestCmdModelUnknownSubcommandIsUsageError(t *testing.T) {
	if code := cmdModel([]string{"frobnicate"}); code != 2 {
		t.Errorf("cmdModel([frobnicate]) = %d, want 2", code)
	}
}

func TestCmdModelHelp(t *testing.T) {
	for _, h := range []string{"help", "--help", "-h"} {
		if code := cmdModel([]string{h}); code != 0 {
			t.Errorf("cmdModel([%s]) = %d, want 0", h, code)
		}
	}
}

// TestUsageMentionsModelSubcommand is a light guard that the top-level
// help text documents `model` alongside `provider`/`purge`, the same style
// TestCmdConfigInitUsageMentionsFull (config_test.go) already checks for
// --full.
func TestUsageMentionsModelSubcommand(t *testing.T) {
	if !strings.Contains(usage, "model set|alias|favorite") {
		t.Error("usage text does not mention `model set|alias|favorite`; the new subcommand should be discoverable from `ishakat --help`")
	}
}

// TestKnownSubcommandsIncludesModel guards main()'s own dispatch table
// (knownSubcommands) so `closestSubcommand`'s "did you mean" suggestion
// can point at `model` for a typo of it.
func TestKnownSubcommandsIncludesModel(t *testing.T) {
	found := false
	for _, s := range knownSubcommands {
		if s == "model" {
			found = true
		}
	}
	if !found {
		t.Errorf("knownSubcommands = %v, missing \"model\"", knownSubcommands)
	}
}

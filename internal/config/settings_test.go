package config_test

import (
	"os"
	"testing"

	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/xdg"
)

// --- config.Setting / config.Settings metadata table -----------------------

func TestSettingValidAcceptsOnlyTrueOrFalseForBool(t *testing.T) {
	s, ok := config.FindSetting("ui.banner")
	if !ok {
		t.Fatal("FindSetting(\"ui.banner\") ok = false, want true")
	}
	for _, v := range []string{"true", "false"} {
		if !s.Valid(v) {
			t.Errorf("Setting(ui.banner).Valid(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"", "yes", "1", "TRUE"} {
		if s.Valid(v) {
			t.Errorf("Setting(ui.banner).Valid(%q) = true, want false", v)
		}
	}
}

func TestSettingValidAcceptsOnlyDeclaredEnumValues(t *testing.T) {
	s, ok := config.FindSetting("ui.reasoning")
	if !ok {
		t.Fatal("FindSetting(\"ui.reasoning\") ok = false, want true")
	}
	for _, v := range []string{"off", "collapsed", "full"} {
		if !s.Valid(v) {
			t.Errorf("Setting(ui.reasoning).Valid(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"", "on", "Full", "true"} {
		if s.Valid(v) {
			t.Errorf("Setting(ui.reasoning).Valid(%q) = true, want false", v)
		}
	}
}

func TestSettingAllowedDisplayReflectsKind(t *testing.T) {
	boolSetting, _ := config.FindSetting("ui.markdown")
	if got := boolSetting.AllowedDisplay(); len(got) != 2 || got[0] != "true" || got[1] != "false" {
		t.Errorf("AllowedDisplay() for a bool setting = %v, want [true false]", got)
	}

	enumSetting, _ := config.FindSetting("ui.reasoning")
	got := enumSetting.AllowedDisplay()
	want := []string{"off", "collapsed", "full"}
	if len(got) != len(want) {
		t.Fatalf("AllowedDisplay() for ui.reasoning = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("AllowedDisplay()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestFindSettingUnknownKeyIsNotFound(t *testing.T) {
	if _, ok := config.FindSetting("ui.does-not-exist"); ok {
		t.Error("FindSetting(\"ui.does-not-exist\") ok = true, want false")
	}
}

func TestSettingsTableCoversExactlyThisSlicesFourKeys(t *testing.T) {
	// This slice's own deliberately narrow scope (settings.go's package
	// comment): only these four [ui] keys, each with an already-wired,
	// restart-free live-apply path in internal/tui.Root. Widening this
	// table is a later slice's job — this test pins the boundary so that
	// widening it is a conscious edit, not an accident.
	want := []string{"ui.banner", "ui.markdown", "ui.syntax", "ui.reasoning"}
	if len(config.Settings) != len(want) {
		t.Fatalf("len(Settings) = %d, want %d: %v", len(config.Settings), len(want), config.Settings)
	}
	for _, key := range want {
		if _, ok := config.FindSetting(key); !ok {
			t.Errorf("Settings is missing %q", key)
		}
	}
}

// --- config.SetSetting ------------------------------------------------------

func TestSetSettingWritesBoolValue(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := config.SetSetting("ui.banner", "false"); err != nil {
		t.Fatalf("SetSetting() error = %v", err)
	}
	cfg, err := config.Load(config.Options{SkipProject: true})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.UI.Banner {
		t.Errorf("UI.Banner = true, want false after SetSetting")
	}
}

func TestSetSettingWritesEnumValue(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := config.SetSetting("ui.reasoning", "full"); err != nil {
		t.Fatalf("SetSetting() error = %v", err)
	}
	cfg, err := config.Load(config.Options{SkipProject: true})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.UI.Reasoning != "full" {
		t.Errorf("UI.Reasoning = %q, want %q", cfg.UI.Reasoning, "full")
	}
}

func TestSetSettingRejectsUnknownKey(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := config.SetSetting("ui.does-not-exist", "true"); err == nil {
		t.Fatal("SetSetting() error = nil, want an error for an unknown key")
	}
}

func TestSetSettingRejectsInvalidValue(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := config.SetSetting("ui.banner", "yes"); err == nil {
		t.Fatal("SetSetting() error = nil, want an error for an invalid bool value")
	}
	if err := config.SetSetting("ui.reasoning", "loud"); err == nil {
		t.Fatal("SetSetting() error = nil, want an error for an invalid enum value")
	}
}

// TestSetSettingPreservesOtherUISettings mirrors
// TestSetThemePreservesOtherUISettings (connection_test.go): [ui] already
// carries several other keys by the time a user ever types /settings, and a
// naive write must not clobber them.
func TestSetSettingPreservesOtherUISettings(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	if err := os.MkdirAll(dir+"/ishakat", 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	seed := "[ui]\ntheme = \"dracula\"\nmarkdown = false\n"
	if err := os.WriteFile(xdg.ConfigFile(), []byte(seed), 0o644); err != nil {
		t.Fatalf("seed config file: %v", err)
	}

	if err := config.SetSetting("ui.banner", "false"); err != nil {
		t.Fatalf("SetSetting() error = %v", err)
	}
	cfg, err := config.Load(config.Options{SkipProject: true})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.UI.Theme != "dracula" {
		t.Errorf("SetSetting clobbered UI.Theme: got %q, want the seeded %q", cfg.UI.Theme, "dracula")
	}
	if cfg.UI.Markdown {
		t.Errorf("SetSetting clobbered UI.Markdown: got true, want the seeded false")
	}
	if cfg.UI.Banner {
		t.Errorf("UI.Banner = true, want false after SetSetting")
	}
}

func TestSetSettingOverwritesPreviousValue(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := config.SetSetting("ui.syntax", "false"); err != nil {
		t.Fatalf("first SetSetting() error = %v", err)
	}
	if err := config.SetSetting("ui.syntax", "true"); err != nil {
		t.Fatalf("second SetSetting() error = %v", err)
	}
	cfg, err := config.Load(config.Options{SkipProject: true})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.UI.Syntax {
		t.Errorf("UI.Syntax = false, want true after overwrite")
	}
}

// TestSetSettingWritesReadableConfigFile mirrors
// TestSetAppModelWritesReadableConfigFile: config.toml must stay 0644
// (shareable), not 0600, after SetSetting writes it.
func TestSetSettingWritesReadableConfigFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := config.SetSetting("ui.banner", "true"); err != nil {
		t.Fatalf("SetSetting() error = %v", err)
	}
	info, err := os.Stat(xdg.ConfigFile())
	if err != nil {
		t.Fatalf("stat config file: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o644 {
		t.Errorf("config.toml mode = %o, want 0644", mode)
	}
}

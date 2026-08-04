package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/xdg"
)

func TestSaveCredentialActivatesProviderWithoutEditingConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := config.SaveCredential("nvidia", "nv-secret"); err != nil {
		t.Fatalf("SaveCredential() error = %v", err)
	}

	info, err := os.Stat(xdg.CredentialsFile())
	if err != nil {
		t.Fatalf("credentials file missing: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("credentials mode = %o, want 600", info.Mode().Perm())
	}

	cfg, err := config.Load(config.Options{UserPath: filepath.Join(t.TempDir(), "missing.toml"), SkipProject: true})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	var found bool
	for _, p := range cfg.Providers {
		if p.ID == "nvidia" {
			found = true
			if !p.Enabled || !p.AuthOK || p.APIKey != "nv-secret" {
				t.Fatalf("configured provider = %+v, want enabled and authenticated", p)
			}
		}
	}
	if !found {
		t.Fatal("configured provider was not loaded")
	}

	contents, err := os.ReadFile(xdg.CredentialsFile())
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(contents), "nv-secret") {
		t.Fatal("credentials file did not contain the saved key")
	}
}

func TestRemoveCredentialDeletesPrivateFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := config.SaveCredential("gemini-direct", "gem-secret"); err != nil {
		t.Fatalf("SaveCredential() error = %v", err)
	}
	if err := config.RemoveCredential("gemini-direct"); err != nil {
		t.Fatalf("RemoveCredential() error = %v", err)
	}
	if _, err := os.Stat(xdg.CredentialsFile()); !os.IsNotExist(err) {
		t.Fatalf("credentials file still exists, stat error = %v", err)
	}
}

func TestResolveProviderPresetAliases(t *testing.T) {
	for _, name := range []string{"gemini", "google"} {
		p, err := config.ResolveProviderPreset(name)
		if err != nil {
			t.Fatalf("ResolveProviderPreset(%q) error = %v", name, err)
		}
		if p.ID != "gemini-direct" {
			t.Errorf("ResolveProviderPreset(%q).ID = %q, want gemini-direct", name, p.ID)
		}
	}
}

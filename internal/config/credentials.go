package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/MichiTrader/ishakat/internal/xdg"
)

// ProviderPreset describes a provider that can be configured without editing TOML.
type ProviderPreset struct {
	ID          string
	Name        string
	Environment string
}

var providerPresets = map[string]ProviderPreset{
	"omniroute": {ID: "omniroute", Name: "OmniRoute", Environment: "OMNIROUTE_API_KEY"},
	"openai":    {ID: "openai", Name: "OpenAI", Environment: "OPENAI_API_KEY"},
	"anthropic": {ID: "anthropic", Name: "Anthropic", Environment: "ANTHROPIC_API_KEY"},
	"nvidia":    {ID: "nvidia", Name: "NVIDIA NIM", Environment: "NVIDIA_API_KEY"},
	"gemini":    {ID: "gemini-direct", Name: "Google Gemini", Environment: "GEMINI_API_KEY"},
	"google":    {ID: "gemini-direct", Name: "Google Gemini", Environment: "GEMINI_API_KEY"},
}

// ProviderPresets returns the supported setup names in stable display order.
func ProviderPresets() []ProviderPreset {
	return []ProviderPreset{
		providerPresets["omniroute"],
		providerPresets["openai"],
		providerPresets["anthropic"],
		providerPresets["nvidia"],
		providerPresets["gemini"],
	}
}

// ResolveProviderPreset accepts a friendly provider name or its canonical ID.
func ResolveProviderPreset(name string) (ProviderPreset, error) {
	key := strings.ToLower(strings.TrimSpace(name))
	if p, ok := providerPresets[key]; ok {
		return p, nil
	}
	return ProviderPreset{}, fmt.Errorf("unknown provider %q (use omniroute, openai, anthropic, nvidia or gemini)", name)
}

// SaveCredential atomically stores a provider key in the private credentials file.
// The file is separate from config.toml so configuration can be shared without
// accidentally sharing secrets. It is always created with owner-only permissions.
func SaveCredential(providerID, apiKey string) error {
	providerID = strings.TrimSpace(providerID)
	apiKey = strings.TrimSpace(apiKey)
	if providerID == "" || apiKey == "" {
		return errors.New("provider and API key are required")
	}

	path := xdg.CredentialsFile()
	if err := xdg.EnsureDir(filepath.Dir(path)); err != nil {
		return fmt.Errorf("create credentials directory: %w", err)
	}

	raw := map[string]any{"schema": 1}
	if existing, err := os.ReadFile(path); err == nil {
		if _, err := toml.Decode(string(existing), &raw); err != nil {
			return fmt.Errorf("read credentials file: %w", err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read credentials file: %w", err)
	}

	providers := toTables(raw["provider"])
	updated := false
	for i := range providers {
		id, _ := providers[i]["id"].(string)
		if id == providerID {
			providers[i]["api_key"] = apiKey
			providers[i]["enabled"] = true
			updated = true
			break
		}
	}
	if !updated {
		providers = append(providers, map[string]any{
			"id":      providerID,
			"api_key": apiKey,
			"enabled": true,
		})
	}
	raw["provider"] = providers

	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(raw); err != nil {
		return fmt.Errorf("encode credentials: %w", err)
	}
	return atomicWritePrivate(path, buf.Bytes())
}

// RemoveCredential deletes one provider key and removes the credentials file
// when it becomes empty. The provider remains available but disabled by default.
func RemoveCredential(providerID string) error {
	path := xdg.CredentialsFile()
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read credentials file: %w", err)
	}

	raw := map[string]any{}
	if _, err := toml.Decode(string(data), &raw); err != nil {
		return fmt.Errorf("read credentials file: %w", err)
	}
	providers := toTables(raw["provider"])
	filtered := providers[:0]
	for _, p := range providers {
		id, _ := p["id"].(string)
		if id != providerID {
			filtered = append(filtered, p)
		}
	}
	if len(filtered) == len(providers) {
		return nil
	}
	if len(filtered) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove credentials file: %w", err)
		}
		return nil
	}
	raw["provider"] = filtered
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(raw); err != nil {
		return fmt.Errorf("encode credentials: %w", err)
	}
	return atomicWritePrivate(path, buf.Bytes())
}

func atomicWritePrivate(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".credentials-*")
	if err != nil {
		return fmt.Errorf("create temporary credentials file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("protect temporary credentials file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write credentials file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync credentials file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close credentials file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("install credentials file: %w", err)
	}
	return os.Chmod(path, 0o600)
}

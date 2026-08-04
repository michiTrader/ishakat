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

// SaveProviderConnection writes a provider's shareable connection metadata —
// name, kind, base_url, discover — into config.toml (xdg.ConfigFile()).
// It never touches api_key: that belongs only to credentials.toml, written
// separately by SaveCredential.
//
// This split exists because credentials.toml is loaded as the final layer
// (see load.go) precisely so a rotated key always wins. Before this function
// existed, SaveCredential wrote base_url/kind/discover/name *there too*, which
// meant that same final-layer precedence also clobbered any base_url a user
// had pointed at a proxy or a pinned API version in config.toml — silently,
// on every key rotation, with no error. A secrets file should contain only
// secrets; connection metadata is public, shareable, and belongs in the layer
// users are expected to read and version.
//
// If the provider id already has a connection block in config.toml with a
// different base_url, this refuses to overwrite it unless force is true —
// see also mergeExistingConnection below for the exact rule.
func SaveProviderConnection(preset ProviderPreset, force bool) (overwrote bool, err error) {
	path := xdg.ConfigFile()
	if err := xdg.EnsureDir(filepath.Dir(path)); err != nil {
		return false, fmt.Errorf("create config directory: %w", err)
	}

	raw := map[string]any{"schema": 1}
	if existing, err := os.ReadFile(path); err == nil {
		if _, err := toml.Decode(string(existing), &raw); err != nil {
			return false, fmt.Errorf("read config file: %w", err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("read config file: %w", err)
	}

	providers := toTables(raw["provider"])
	updated := false
	for i := range providers {
		id, _ := providers[i]["id"].(string)
		if id != preset.ID {
			continue
		}
		existingBaseURL, _ := providers[i]["base_url"].(string)
		if existingBaseURL != "" && existingBaseURL != preset.BaseURL && !force {
			// The user already pointed this provider somewhere specific
			// (a corporate proxy, a pinned API version, a self-hosted
			// gateway). Silently overwriting that on every `provider add`
			// is exactly the bug this file exists to prevent — see the
			// package comment. Leave it alone unless explicitly forced.
			return false, nil
		}
		providers[i]["name"] = preset.Name
		providers[i]["kind"] = preset.Kind
		providers[i]["base_url"] = preset.BaseURL
		providers[i]["discover"] = preset.Discover
		providers[i]["enabled"] = true
		updated = true
		break
	}
	if !updated {
		providers = append(providers, map[string]any{
			"id":       preset.ID,
			"name":     preset.Name,
			"kind":     preset.Kind,
			"base_url": preset.BaseURL,
			"discover": preset.Discover,
			"enabled":  true,
		})
	}
	raw["provider"] = providers

	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(raw); err != nil {
		return false, fmt.Errorf("encode config: %w", err)
	}
	// config.toml is not a secrets file: it does not need owner-only
	// permissions, and forcing 0600 on it would fight a user who wants to
	// share or version it. atomicWritePrivate is reused for its
	// write-then-rename safety, and its mode is loosened right after.
	if err := atomicWritePrivate(path, buf.Bytes()); err != nil {
		return false, err
	}
	if err := os.Chmod(path, 0o644); err != nil {
		return false, fmt.Errorf("set config file permissions: %w", err)
	}
	return true, nil
}

// disableProviderConnection flips enabled = false for a provider id in
// config.toml. Used by RemoveCredential: once a key is gone, config.toml
// should stop claiming the provider is enabled — otherwise the very next
// config.Load leaves a provider with enabled = true and no api_key, which
// surfaces only as ErrNoAPIKey the next time something tries to use it,
// instead of the provider simply not being offered.
//
// If the provider has no entry of its own in config.toml, an explicit
// override ({id, enabled = false}) is appended rather than treating "no
// entry to flip" as "nothing to do". This matters for providers declared
// only in the embedded defaults.toml — omniroute is the one that ships that
// way — because mergeProviders (merge.go) merges layers by id: a later
// layer's {id, enabled = false} wins over the embedded default's
// enabled = true for that same id while leaving every other field
// (base_url, kind, timeout_s) untouched. Without this append, `provider
// remove omniroute` silently did nothing on a fresh install — there was no
// config.toml entry for omniroute to flip, so the embedded default kept
// applying enabled = true unopposed on every subsequent config.Load, and
// the provider kept showing up (with a "no resolved credential" warning)
// even after being explicitly removed.
func disableProviderConnection(providerID string) error {
	path := xdg.ConfigFile()
	if err := xdg.EnsureDir(filepath.Dir(path)); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}

	raw := map[string]any{"schema": 1}
	if existing, err := os.ReadFile(path); err == nil {
		if _, err := toml.Decode(string(existing), &raw); err != nil {
			return fmt.Errorf("read config file: %w", err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read config file: %w", err)
	}

	providers := toTables(raw["provider"])
	found := false
	for i := range providers {
		id, _ := providers[i]["id"].(string)
		if id == providerID {
			providers[i]["enabled"] = false
			found = true
			break
		}
	}
	if !found {
		providers = append(providers, map[string]any{
			"id":      providerID,
			"enabled": false,
		})
	}
	raw["provider"] = providers

	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(raw); err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	if err := atomicWritePrivate(path, buf.Bytes()); err != nil {
		return err
	}
	return os.Chmod(path, 0o644)
}

// SetDefaultModel writes app.default_model into config.toml. `provider add`
// offers this once discovery finds models, because leaving the stock
// "omniroute/auto/coding" default in place — the single most common failure
// mode this audit found — means a correctly configured provider is never
// actually used: every turn still dials the OmniRoute gateway on
// 127.0.0.1:20128 and connection-refuses, and the user has no reason to
// suspect the key they just entered has nothing to do with it.
func SetDefaultModel(ref string) error {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return errors.New("model reference is required")
	}
	path := xdg.ConfigFile()
	if err := xdg.EnsureDir(filepath.Dir(path)); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}

	raw := map[string]any{"schema": 1}
	if existing, err := os.ReadFile(path); err == nil {
		if _, err := toml.Decode(string(existing), &raw); err != nil {
			return fmt.Errorf("read config file: %w", err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read config file: %w", err)
	}

	app, _ := raw["app"].(map[string]any)
	if app == nil {
		app = map[string]any{}
	}
	app["default_model"] = ref
	raw["app"] = app

	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(raw); err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	if err := atomicWritePrivate(path, buf.Bytes()); err != nil {
		return err
	}
	return os.Chmod(path, 0o644)
}

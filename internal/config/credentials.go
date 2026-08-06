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
	Kind        string
	BaseURL     string
	Discover    bool
	Environment string

	// VerifyModel is the model id sent on the one-token authenticated probe
	// that `provider add` performs before writing anything (see verify.go).
	// It has to be a real model the service currently serves: an invalid
	// model on some gateways produces the same 4xx family as an invalid
	// key, which would make the probe unable to tell "bad key" from "bad
	// model" apart — the opposite of what verification is for. NVIDIA in
	// particular returns 200 with the full catalog for GET /models with no
	// credential at all, so getting this right is the only thing standing
	// between `provider add` and a false "configured" success.
	VerifyModel string

	// Notes is a short, honest caveat about what this preset's chosen kind
	// cannot do for the underlying service, printed once after a successful
	// `provider add`. Empty when the dialect covers the service without
	// compromise.
	Notes string
}

var providerPresets = map[string]ProviderPreset{
	"omniroute": {
		ID: "omniroute", Name: "OmniRoute", Kind: "openai",
		BaseURL: "http://localhost:20128/v1", Discover: true,
		Environment: "OMNIROUTE_API_KEY", VerifyModel: "auto",
	},
	"openai": {
		ID: "openai", Name: "OpenAI", Kind: "openai",
		BaseURL: "https://api.openai.com/v1", Discover: true,
		Environment: "OPENAI_API_KEY", VerifyModel: "gpt-4o-mini",
	},
	// Anthropic ships an OpenAI-compatible chat/completions endpoint,
	// verified directly against the live API: POST /v1/chat/completions
	// with a bad key returns an OpenAI-shaped 401 error body, and GET
	// /v1/models accepts a plain Bearer token. kind = "anthropic" was
	// never registered by any adapter in this build — the only kinds any
	// init() registers are "openai" and "responses" (see
	// internal/provider/openai/openai.go) — so `validKind` accepted the
	// string anyway, and `provider add anthropic` used to collect the
	// user's real API key, write it to disk, print "Configured
	// Anthropic", and leave a provider that failed on its very first turn
	// with "kind = \"anthropic\", which this build cannot speak yet".
	// Routing through the real OpenAI-compatible shim actually works, at
	// the cost documented in Notes below. `presetByID`/`ResolveProviderPreset`
	// keep exposing this id as "anthropic" for the user-facing name; only
	// the wire dialect changed.
	"anthropic": {
		ID: "anthropic", Name: "Anthropic", Kind: "openai",
		BaseURL: "https://api.anthropic.com/v1", Discover: true,
		Environment: "ANTHROPIC_API_KEY", VerifyModel: "claude-3-5-haiku-20241022",
		Notes: "Anthropic is reached through its OpenAI-compatible shim, not " +
			"the native API: no prompt caching, no extended thinking, and " +
			"tool-use may not carry every field the native API supports.",
	},
	"nvidia": {
		ID: "nvidia", Name: "NVIDIA NIM", Kind: "openai",
		BaseURL: "https://integrate.api.nvidia.com/v1", Discover: true,
		Environment: "NVIDIA_API_KEY", VerifyModel: "meta/llama-3.1-8b-instruct",
		Notes: "NVIDIA's model catalog (GET /models) is public and answers " +
			"without any credential, so it can never be used to check a key " +
			"— `provider add` verifies with a real chat completion instead. " +
			"The catalog also lists embedding, reranker and vision-only " +
			"models alongside chat models; not everything discovery finds " +
			"here can hold a conversation.",
	},
	"gemini": {
		ID: "gemini-direct", Name: "Google Gemini", Kind: "openai",
		BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai", Discover: true,
		Environment: "GEMINI_API_KEY", VerifyModel: "gemini-2.0-flash",
		Notes: "Google's OpenAI-compatible layer has historically rejected " +
			"fields it doesn't recognise with a plain 400 in streaming mode; " +
			"if a turn fails that way, add [provider.params] stream_options " +
			"= {} for this provider in config.toml. Model ids also come back " +
			"prefixed (\"models/gemini-...\"): discovery normalizes that, but " +
			"a hand-written model ref should use the bare id.",
	},
}

func init() {
	providerPresets["google"] = providerPresets["gemini"]
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

func presetByID(id string) (ProviderPreset, bool) {
	for _, preset := range providerPresets {
		if preset.ID == id {
			return preset, true
		}
	}
	return ProviderPreset{}, false
}

// VerifyModelFor returns the VerifyModel of the preset whose ID matches
// providerID (see ProviderPreset.VerifyModel above) — the exact wire id
// `provider add`'s own verification probe already proved answers for that
// preset's credential (see offerDefaultModel in cmd/ishakat/provider.go,
// the first caller to rely on this being a safe, working model id rather
// than a guess).
//
// This is P2's boot-time fallback's own lookup (internal/app.ResolveModelForBoot):
// when app.default_model points at a provider that turns out to be
// disabled or missing its credential, and some *other* configured provider
// is usable, the fallback needs a model id known to work for that other
// provider without touching the network or the catalog (both out of the
// critical path per §4.4). It only recognizes ids that came from a preset
// (ProviderPresets()); a provider the user declared entirely by hand under
// a different id/base_url has no entry here — on purpose: guessing a model
// id for a service this package has never talked to would be worse than
// admitting it doesn't know one.
func VerifyModelFor(providerID string) (string, bool) {
	p, ok := presetByID(providerID)
	if !ok || strings.TrimSpace(p.VerifyModel) == "" {
		return "", false
	}
	return p.VerifyModel, true
}

// SaveCredential atomically stores a provider's API key in the private
// credentials file, and nothing else.
//
// This file is the last configuration layer loaded (see load.go), and that
// used to be the reason it also carried base_url/kind/discover/name/enabled:
// whichever provider a user had a key for would "just work" without editing
// config.toml. The problem is that credentials.toml being the *last* layer
// means those fields also win over anything the *same* user wrote in
// config.toml — a base_url pointed at a proxy or a pinned API version would
// silently revert to the preset default the next time the key was rotated,
// with no error and no visible trace, because config.toml still shows what
// the user typed while credentials.toml quietly overrides it.
//
// The fix is the one this function embodies: a secrets file contains only
// secrets. Connection metadata belongs in config.toml, written separately by
// SaveProviderConnection (see cmd/ishakat/provider.go, which calls both).
// Rotating a key here can never again change where the request goes.
func SaveCredential(providerID, apiKey string) error {
	providerID = strings.TrimSpace(providerID)
	apiKey = strings.TrimSpace(apiKey)
	if providerID == "" || apiKey == "" {
		return errors.New("provider and API key are required")
	}
	if _, ok := presetByID(providerID); !ok {
		return fmt.Errorf("unknown provider id %q", providerID)
	}
	if !xdg.HomeResolved() {
		return errors.New("cannot determine the user's home directory; " +
			"refusing to write a credential to the current directory, which " +
			"could be a git checkout. Set $HOME (or $XDG_CONFIG_HOME) and try again")
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
			updated = true
			break
		}
	}
	if !updated {
		providers = append(providers, map[string]any{
			"id":      providerID,
			"api_key": apiKey,
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
// when it becomes empty. It also flips `enabled = false` for that provider in
// config.toml (disableProviderConnection): without this, a removed key leaves
// a provider that config.toml still marks enabled = true but with no
// api_key — a worse failure mode than the provider simply disappearing from
// `provider list` and model resolution the moment its key is gone.
func RemoveCredential(providerID string) error {
	if err := disableProviderConnection(providerID); err != nil {
		return fmt.Errorf("disable provider in config: %w", err)
	}

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
	filtered := make([]map[string]any, 0, len(providers))
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

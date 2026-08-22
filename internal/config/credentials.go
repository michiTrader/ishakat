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

	// Label is the short, human-facing provider name the picker renders in
	// `model-id [label] TVR ✓` (docs/ROADMAP-ux-2026-08-20.md's DECISION-3),
	// dimmed, next to every row. It exists purely for display: `id` remains
	// the only thing configs, refs and session files ever store or persist
	// (DECISION-3's own words), so this field carries no config.toml
	// counterpart and no user-override mechanism — it is a compile-time
	// constant of the preset, looked up at render time via LabelFor below.
	// Almost every preset's Label equals its own ID (omniroute, openai,
	// anthropic, nvidia); "gemini-direct" is the one exception, labelled
	// "google" because that's the name every user actually recognizes —
	// DECISION-3 overruled making "google" the real id for W4 (that full
	// rename is deliberately deferred to W5, after the rendering and loop
	// waves), so for now the mismatch between id and Label is the whole
	// point: it's what lets the picker show "google" today without
	// touching anything a provider ref, config file or session transcript
	// stores.
	Label string

	// Notes is a short, honest caveat about what this preset's chosen kind
	// cannot do for the underlying service, printed once after a successful
	// `provider add`. Empty when the dialect covers the service without
	// compromise.
	Notes string

	// The four fields below are Step 24's OAuth device-flow half
	// (docs/PLAN.md §11, `ishakat login`): when DeviceCodeURL and TokenURL
	// are both set, `ishakat login <provider>` drives internal/oauth's RFC
	// 8628 client instead of prompting for a pasted API key. All four are
	// empty for every preset below on purpose — see login.go's own package
	// comment for why none of the five presets in ProviderPresets() opts
	// into this today, and why `ishakat login` still works for a
	// self-hosted or third-party gateway that documents its own device-flow
	// endpoints, via --client-id/--device-code-url/--token-url rather than
	// a preset.
	OAuthClientID      string
	OAuthScope         string
	OAuthDeviceCodeURL string
	OAuthTokenURL      string
}

// SupportsDeviceFlow reports whether p declares enough of the four OAuth
// fields above for RequestDeviceCode/PollForToken to be usable. ClientID
// can legitimately be empty for a provider whose device-flow endpoint
// does not require one; DeviceCodeURL and TokenURL cannot, since
// internal/oauth has nowhere to POST without them.
func (p ProviderPreset) SupportsDeviceFlow() bool {
	return strings.TrimSpace(p.OAuthDeviceCodeURL) != "" && strings.TrimSpace(p.OAuthTokenURL) != ""
}

var providerPresets = map[string]ProviderPreset{
	"omniroute": {
		ID: "omniroute", Name: "OmniRoute", Kind: "openai", Label: "omniroute",
		BaseURL: "http://localhost:20128/v1", Discover: true,
		Environment: "OMNIROUTE_API_KEY", VerifyModel: "auto",
	},
	"openai": {
		ID: "openai", Name: "OpenAI", Kind: "openai", Label: "openai",
		BaseURL: "https://api.openai.com/v1", Discover: true,
		Environment: "OPENAI_API_KEY", VerifyModel: "gpt-4o-mini",
	},
	// Anthropic ships an OpenAI-compatible chat/completions endpoint,
	// verified directly against the live API: POST /v1/chat/completions
	// with a bad key returns an OpenAI-shaped 401 error body, and GET
	// /v1/models accepts a plain Bearer token. kind = "anthropic" used to
	// be accepted by validKind with zero registered adapter — the only
	// kinds any init() registered were "openai" and "responses" (see
	// internal/provider/openai/openai.go) — so `provider add anthropic`
	// used to collect the user's real API key, write it to disk, print
	// "Configured Anthropic", and leave a provider that failed on its
	// very first turn with "kind = \"anthropic\", which this build cannot
	// speak yet". Routing through the OpenAI-compatible shim fixed that,
	// at the cost documented in Notes below.
	//
	// internal/provider/anthropic now exists and registers the native
	// "anthropic" kind (Fase 4), but this preset's Kind deliberately
	// stays "openai": this preset is the one every new user hits first
	// (`provider add anthropic`), and the shim is the path this codebase
	// has actually exercised end-to-end against the live API, while the
	// native adapter has only run against httptest fakes built from
	// public docs (no live Anthropic key was available to verify it — see
	// docs/PLAN.md §17). Someone who wants the native dialect's extra
	// capabilities (prompt caching, extended thinking) can write
	// kind = "anthropic" in config.toml by hand today; switching this
	// preset's default is a separate decision for once the native
	// adapter has real-traffic mileage behind it, not a byproduct of
	// merely having written the code. `presetByID`/`ResolveProviderPreset`
	// keep exposing this id as "anthropic" for the user-facing name either
	// way; only the wire dialect would change.
	"anthropic": {
		ID: "anthropic", Name: "Anthropic", Kind: "openai", Label: "anthropic",
		BaseURL: "https://api.anthropic.com/v1", Discover: true,
		Environment: "ANTHROPIC_API_KEY", VerifyModel: "claude-3-5-haiku-20241022",
		Notes: "Anthropic is reached through its OpenAI-compatible shim, not " +
			"the native API: no prompt caching, no extended thinking, and " +
			"tool-use may not carry every field the native API supports. " +
			"A native adapter exists (kind = \"anthropic\" in config.toml) " +
			"for anyone who wants those capabilities before this preset " +
			"switches its default.",
	},
	"nvidia": {
		ID: "nvidia", Name: "NVIDIA NIM", Kind: "openai", Label: "nvidia",
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
		ID: "gemini-direct", Name: "Google Gemini", Kind: "openai", Label: "google",
		BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai", Discover: true,
		Environment: "GEMINI_API_KEY", VerifyModel: "gemini-2.0-flash",
		Notes: "Google's OpenAI-compatible layer has historically rejected " +
			"fields it doesn't recognise with a plain 400 in streaming mode; " +
			"if a turn fails that way, add [provider.params] stream_options " +
			"= {} for this provider in config.toml. Model ids also come back " +
			"prefixed (\"models/gemini-...\"): discovery normalizes that, but " +
			"a hand-written model ref should use the bare id. A native " +
			"adapter also exists (kind = \"gemini\" in config.toml) for " +
			"anyone who wants generateContent's own quirks (thoughtSignature " +
			"round-trip, native tool schema) instead of the shim; switching " +
			"this preset's default is a separate decision for once the " +
			"native adapter has real-traffic mileage behind it.",
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

// LabelFor returns the short, human-facing display name (ProviderPreset.Label
// above) of the preset whose ID matches providerID — the picker's own lookup
// for F11's `model-id [label] TVR ✓` row format
// (docs/ROADMAP-ux-2026-08-20.md's DECISION-3). It mirrors VerifyModelFor's
// exact contract on purpose: only providers that came from a preset
// (ProviderPresets()) have a known Label; a provider the user declared
// entirely by hand under a different id has none, and callers fall back to
// rendering the bare id instead of guessing a display name for a service
// this package has never described.
func LabelFor(providerID string) (string, bool) {
	p, ok := presetByID(providerID)
	if !ok || strings.TrimSpace(p.Label) == "" {
		return "", false
	}
	return p.Label, true
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

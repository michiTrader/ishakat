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
	raw, err := readRawConfigTOML()
	if err != nil {
		return false, err
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
	// config.toml is not a secrets file: it does not need owner-only
	// permissions, and forcing 0600 on it would fight a user who wants to
	// share or version it. writeRawConfigTOML already loosens the mode to
	// 0644 after its own atomic write-then-rename.
	if err := writeRawConfigTOML(raw); err != nil {
		return false, err
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
	raw, err := readRawConfigTOML()
	if err != nil {
		return err
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
	return writeRawConfigTOML(raw)
}

// readRawConfigTOML loads config.toml (xdg.ConfigFile()) as a raw
// map[string]any, or {"schema": 1} if the file doesn't exist yet — the same
// "start from a minimal skeleton" rule every mutator in this file already
// followed on its own before this was extracted. Shared by every function
// below that edits config.toml in place: SetDefaultModel/SetAppModel,
// SaveProviderConnection, disableProviderConnection, SetAlias, RemoveAlias,
// AddFavorite and RemoveFavorite all need the exact same read-decode-or-else
// step, previously duplicated four times over.
func readRawConfigTOML() (map[string]any, error) {
	raw := map[string]any{"schema": 1}
	path := xdg.ConfigFile()
	existing, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return raw, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}
	if _, err := toml.Decode(string(existing), &raw); err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}
	return raw, nil
}

// writeRawConfigTOML encodes raw and atomically installs it as config.toml,
// world-readable (0644) like every other writer in this file: config.toml
// is not a secrets file (see SaveProviderConnection's own comment) and a
// user who wants to share or version it should not have to chmod it back
// every time this program touches it.
func writeRawConfigTOML(raw map[string]any) error {
	path := xdg.ConfigFile()
	if err := xdg.EnsureDir(filepath.Dir(path)); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(raw); err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	if err := atomicWritePrivate(path, buf.Bytes()); err != nil {
		return err
	}
	return os.Chmod(path, 0o644)
}

// AppModelKey names one of the three model-selection fields in [app] that
// `ishakat model set` (cmd/ishakat/model.go, P3c) can point at. These are
// exactly config.App's own DefaultModel/CompactModel/FallbackModel fields —
// declared here, not derived from reflection, so a typo in a call site is a
// compile error instead of a silently-ignored string.
type AppModelKey string

const (
	AppModelDefault  AppModelKey = "default_model"
	AppModelCompact  AppModelKey = "compact_model"
	AppModelFallback AppModelKey = "fallback_model"
)

// SetDefaultModel writes app.default_model into config.toml. `provider add`
// offers this once discovery finds models, because leaving the stock
// "omniroute/auto/coding" default in place — the single most common failure
// mode this audit found — means a correctly configured provider is never
// actually used: every turn still dials the OmniRoute gateway on
// 127.0.0.1:20128 and connection-refuses, and the user has no reason to
// suspect the key they just entered has nothing to do with it.
//
// This is now a thin wrapper over SetAppModel(AppModelDefault, ref) —
// kept as its own exported name because offerDefaultModel
// (cmd/ishakat/provider.go) and its tests already call it by this name,
// and "the default model" reads better there than the more general
// three-key API SetAppModel exists for.
func SetDefaultModel(ref string) error {
	return SetAppModel(AppModelDefault, ref)
}

// SetAppModel writes one of [app]'s three model-selection keys
// (default_model/compact_model/fallback_model) into config.toml. This is
// P3c's own primitive: `ishakat model set <ref> [--default|--compact|
// --fallback|--all]` (cmd/ishakat/model.go) is what turns the ergonomics
// the original bug report asked for — "a command instead of hand-editing
// TOML" — into config.toml writes, one key at a time.
//
// ref == "" is accepted on purpose for compact_model/fallback_model only
// (SetDefaultModel above already rejects "" for default_model, and this
// keeps that behaviour): ResolveModel's own empty-string rule treats an
// empty compact_model as "same as default_model" (see modelref.go), so
// `ishakat model set "" --compact` is the supported way to reset it back
// to following the default rather than pointing at something specific.
func SetAppModel(key AppModelKey, ref string) error {
	ref = strings.TrimSpace(ref)
	if key == AppModelDefault && ref == "" {
		return errors.New("model reference is required")
	}
	raw, err := readRawConfigTOML()
	if err != nil {
		return err
	}
	app, _ := raw["app"].(map[string]any)
	if app == nil {
		app = map[string]any{}
	}
	app[string(key)] = ref
	raw["app"] = app
	return writeRawConfigTOML(raw)
}

// SetEvolveMode writes tools.evolve.mode into config.toml. It is §19.7's
// decay-writeback path: when a user has rejected `decay_after_rejects`
// suggestions in a row, the suggest-mode overlay calls this to drop Mode
// down to "on_request" and say so, rather than keep asking.
//
// Unlike SetAppModel's flat raw["app"], [tools.evolve] is a *nested* table:
// BurntSushi/toml decodes it as raw["tools"].(map[string]any)["evolve"], one
// level under the umbrella [tools] table that also (today) holds nothing
// else, but might one day hold sibling settings for other tool-related
// features. Flattening it into raw["evolve"] the way SetAppModel flattens
// into raw["app"] would silently produce a *second*, wrong top-level table
// in config.toml, next to a real, still-nested [tools.evolve] a user could
// have hand-written — so both levels of the map must be read, created if
// absent, and written back explicitly.
func SetEvolveMode(mode string) error {
	mode = strings.TrimSpace(mode)
	if !validEvolveMode(mode) {
		return fmt.Errorf("invalid evolve mode %q: must be one of off, on_request, suggest, auto", mode)
	}
	raw, err := readRawConfigTOML()
	if err != nil {
		return err
	}
	tools, _ := raw["tools"].(map[string]any)
	if tools == nil {
		tools = map[string]any{}
	}
	evolve, _ := tools["evolve"].(map[string]any)
	if evolve == nil {
		evolve = map[string]any{}
	}
	evolve["mode"] = mode
	tools["evolve"] = evolve
	raw["tools"] = tools
	return writeRawConfigTOML(raw)
}

// SetTheme writes [ui].theme into config.toml. It is /theme's own
// persistence half (§8/§11 Fase 3): the in-session switch itself just
// rebuilds internal/tui's styles in memory (theme.Load never errors, so
// there is nothing for this function to validate against — an unknown
// name is exactly as legitimate to write here as a real one, the same
// way theme.Load itself degrades to the embedded default plus a warning
// rather than refusing outright), this is only what makes that choice
// survive the next `ishakat` launch.
//
// [ui] is a flat table like [app] (SetAppModel's own comment on why that
// matters, contrasted with [tools.evolve]'s nested shape SetEvolveMode
// has to read one level deeper): a single raw["ui"] map, read once,
// mutated in place, written back whole.
func SetTheme(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("theme name is required")
	}
	raw, err := readRawConfigTOML()
	if err != nil {
		return err
	}
	ui, _ := raw["ui"].(map[string]any)
	if ui == nil {
		ui = map[string]any{}
	}
	ui["theme"] = name
	raw["ui"] = ui
	return writeRawConfigTOML(raw)
}

// SetAlias writes (or overwrites) one [alias] entry in config.toml. name is
// matched case-insensitively against every OTHER key already in the table
// (mirroring lookupAlias's own case-insensitive lookup in
// internal/app/modelref.go) so `ishakat model alias set Smart …` updates an
// existing "smart" key instead of creating a second, differently-cased one
// that would only ever shadow the first depending on map iteration order.
// A brand new alias is stored exactly as the caller typed it.
func SetAlias(name, ref string) error {
	name = strings.TrimSpace(name)
	ref = strings.TrimSpace(ref)
	if name == "" {
		return errors.New("alias name is required")
	}
	if ref == "" {
		return errors.New("alias target (model reference) is required")
	}
	raw, err := readRawConfigTOML()
	if err != nil {
		return err
	}
	alias, _ := raw["alias"].(map[string]any)
	if alias == nil {
		alias = map[string]any{}
	}
	key := name
	for k := range alias {
		if strings.EqualFold(k, name) {
			key = k
			break
		}
	}
	alias[key] = ref
	raw["alias"] = alias
	return writeRawConfigTOML(raw)
}

// RemoveAlias deletes one [alias] entry from config.toml, matched
// case-insensitively (see SetAlias's own comment on why). Removing an
// alias that isn't there is not an error — same "idempotent, no-op on
// absence" rule as disableProviderConnection's own removal path — since
// the end state the caller wants ("this alias name no longer resolves to
// anything") is already true.
func RemoveAlias(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("alias name is required")
	}
	raw, err := readRawConfigTOML()
	if err != nil {
		return err
	}
	alias, _ := raw["alias"].(map[string]any)
	for k := range alias {
		if strings.EqualFold(k, name) {
			delete(alias, k)
		}
	}
	raw["alias"] = alias
	return writeRawConfigTOML(raw)
}

// AddFavorite appends ref to [favorites].list in config.toml, unless it is
// already there — favorites.List has no ordering guarantee worth
// preserving duplicates for, and the picker (internal/tui) only ever needs
// to know "is this ref a favorite", not "how many times was it added".
func AddFavorite(ref string) error {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return errors.New("model reference is required")
	}
	raw, err := readRawConfigTOML()
	if err != nil {
		return err
	}
	list := stringList(raw["favorites"])
	for _, existing := range list {
		if existing == ref {
			return writeRawConfigTOML(raw) // already a favorite; still normalize the file.
		}
	}
	list = append(list, ref)
	raw["favorites"] = map[string]any{"list": list}
	return writeRawConfigTOML(raw)
}

// RemoveFavorite removes ref from [favorites].list in config.toml. Same
// "no-op on absence" rule as RemoveAlias: removing something that was
// never a favorite still leaves the caller's desired end state true.
func RemoveFavorite(ref string) error {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return errors.New("model reference is required")
	}
	raw, err := readRawConfigTOML()
	if err != nil {
		return err
	}
	list := stringList(raw["favorites"])
	out := make([]string, 0, len(list))
	for _, existing := range list {
		if existing != ref {
			out = append(out, existing)
		}
	}
	raw["favorites"] = map[string]any{"list": out}
	return writeRawConfigTOML(raw)
}

// stringList reads a [favorites]-shaped raw value (a table with a "list"
// key holding a TOML array) back into a []string. TOML decodes an array of
// strings as []any under this package's raw-map representation (the same
// shape mergeRoot/toTables already navigate elsewhere in this package), so
// each element is asserted individually rather than type-switched as a
// whole slice.
func stringList(v any) []string {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	raw, ok := m["list"].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, e := range raw {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

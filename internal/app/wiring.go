package app

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/provider"

	// The §5.4 registry only knows the dialects someone imports. This blank
	// import is the switch that turns on kind = "openai", and it lives here
	// —in the wiring package— because neither provider nor tui get to decide
	// which dialects exist.
	_ "github.com/MichiTrader/ishakat/internal/provider/openai"
)

// Settings translates a [[provider]] TOML entry into provider.Settings.
//
// This is the only place in the program where config and provider look at
// each other: thanks to this function the adapter never imports config
// (§6.1), and it can be instantiated in a test with three lines.
func Settings(cfg *config.Config, p config.Provider, version string) provider.Settings {
	timeout := time.Duration(p.TimeoutS) * time.Second
	if p.TimeoutS <= 0 {
		timeout = time.Duration(cfg.App.TimeoutS) * time.Second
	}
	connect := time.Duration(cfg.App.ConnectTimeoutS) * time.Second

	ua := "ishakat"
	if version != "" && version != "dev" {
		ua = "ishakat/" + version
	}

	return provider.Settings{
		ID:             p.ID,
		Name:           p.Name,
		Kind:           strings.ToLower(p.Kind),
		BaseURL:        p.BaseURL,
		WireAPI:        p.WireAPI,
		APIKey:         p.APIKey,
		Headers:        p.Headers,
		Params:         p.Params,
		Timeout:        timeout,
		ConnectTimeout: connect,
		UserAgent:      ua,
	}
}

// FindProvider looks up a provider by id. Returns a copy: nothing outside
// config mutates the loaded configuration.
func FindProvider(cfg *config.Config, id string) (config.Provider, bool) {
	for _, p := range cfg.Providers {
		if strings.EqualFold(p.ID, id) {
			return p, true
		}
	}
	return config.Provider{}, false
}

// EnabledProviders are the usable providers, in the order they appear in the
// configuration. That order matters: the first one is used when a model
// reference has no provider prefix.
func EnabledProviders(cfg *config.Config) []config.Provider {
	out := make([]config.Provider, 0, len(cfg.Providers))
	for _, p := range cfg.Providers {
		if p.Enabled {
			out = append(out, p)
		}
	}
	return out
}

// NewProvider builds the adapter for a provider from the configuration.
//
// The two errors caught here are the two the user hits on first run, and
// both deserve a message that says what to do about it: an unknown kind and
// an API key that failed to expand.
func NewProvider(cfg *config.Config, p config.Provider, version string) (provider.Provider, error) {
	if !p.Enabled {
		return nil, fmt.Errorf("provider %q is disabled (enabled = false in %s)",
			p.ID, configOrigin(cfg))
	}
	if !provider.Registered(strings.ToLower(p.Kind)) {
		return nil, fmt.Errorf("provider %q uses kind = %q, which this build cannot speak yet "+
			"(available dialects: %s)", p.ID, p.Kind, strings.Join(provider.Kinds(), ", "))
	}
	if !p.AuthOK {
		hint := "set the environment variable and try again"
		if p.MissingEnv != "" {
			hint = fmt.Sprintf("export %s=… and try again", p.MissingEnv)
		}
		return nil, fmt.Errorf("%w for %q: %s", provider.ErrNoAPIKey, p.ID, hint)
	}
	return provider.New(Settings(cfg, p, version))
}

// configOrigin names the file the user would need to edit. With several
// layers loaded, the last one is mentioned, since that's the one that wins.
func configOrigin(cfg *config.Config) string {
	if n := len(cfg.Files); n > 0 {
		return cfg.Files[n-1]
	}
	return "config.toml"
}

// SystemPrompt resolves the effective system prompt.
//
// §5.2 is explicit: if both system_prompt and system_prompt_file are set,
// the file wins. An unreadable file is not a reason to abort startup; the
// warning is returned and execution continues with whatever the TOML had.
func SystemPrompt(cfg *config.Config) (string, string) {
	if f := strings.TrimSpace(cfg.App.SystemPromptFile); f != "" {
		raw, err := os.ReadFile(f)
		if err != nil {
			return cfg.App.SystemPrompt, fmt.Sprintf("could not read system_prompt_file (%s): %v", f, err)
		}
		return strings.TrimSpace(string(raw)), ""
	}
	return cfg.App.SystemPrompt, ""
}

// Dialects are the dialects this binary can speak. `ishakat doctor` reports
// this instead of a hand-written list, because what matters is which init()
// actually got compiled in (§5.4).
func Dialects() []string { return provider.Kinds() }

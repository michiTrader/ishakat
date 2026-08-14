package app

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/MichiTrader/ishakat/internal/agentsmd"
	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/provider"
	"github.com/MichiTrader/ishakat/internal/skills"
	"github.com/MichiTrader/ishakat/internal/xdg"

	// The §5.4 registry only knows the dialects someone imports. This blank
	// import is the switch that turns on kind = "openai", and it lives here
	// —in the wiring package— because neither provider nor tui get to decide
	// which dialects exist.
	_ "github.com/MichiTrader/ishakat/internal/provider/openai"

	// Same switch, for kind = "anthropic" (Fase 4's native Messages API
	// adapter). The built-in "anthropic" preset in credentials.go still
	// defaults to the openai dialect against Anthropic's own
	// OpenAI-compatible shim; this import is what makes a hand-written
	// kind = "anthropic" in config.toml actually resolve instead of
	// failing validation with "unknown kind".
	_ "github.com/MichiTrader/ishakat/internal/provider/anthropic"
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
		// P3: a provider reaching this branch was, by construction,
		// explicitly declared by the user (P1's expandVars already
		// auto-disables an embedded-only provider with an unresolved
		// credential before Enabled/AuthOK ever get here — see
		// internal/config/expand.go's own doc comment). So "set the
		// variable" is genuinely the right first suggestion; what the
		// original bug report actually needed was the second half: an
		// escape hatch for "I don't want this provider at all", since
		// `export VAR=… and try again` alone reads as mandatory.
		hint := "set the environment variable and try again"
		if p.MissingEnv != "" {
			hint = fmt.Sprintf("export %s=… and try again", p.MissingEnv)
		}
		return nil, fmt.Errorf("%w for %q: %s (or `ishakat provider remove %s` if you don't want this provider)",
			provider.ErrNoAPIKey, p.ID, hint, p.ID)
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
//
// Step 18 (§11) then appends the merged AGENTS.md rules, when
// cfg.App.AgentsMD is on: a project's standing instructions are additional
// context on top of whatever system_prompt/system_prompt_file already said,
// never a replacement for it — the two are orthogonal knobs answering
// different questions ("who is the assistant" vs. "what does this project
// need it to know"). This is the one place both BuildEngine and Headless
// resolve the system prompt (see their own comments), so a caller never has
// to remember to ask for AGENTS.md separately.
//
// Step 19 (§19.4) then appends the rung-0 skills listing, gated behind
// cfg.Tools.Enabled: a skill is discovered content the model can act on by
// calling read_file on skills.Skill.File (§19.1's own reasoning against a
// second "load skill" tool), so offering the listing to a model that has no
// tools at all would name a capability nothing can reach. Only Name +
// Description of each skill enters the prompt (skills.Summary), never a
// body — §19.4's progressive-disclosure rule is what keeps forty skills
// costing ~600 tokens instead of forty times a SKILL.md's own 2.000-8.000.
func SystemPrompt(cfg *config.Config) (string, string) {
	system := cfg.App.SystemPrompt
	var warn string

	if f := strings.TrimSpace(cfg.App.SystemPromptFile); f != "" {
		raw, err := os.ReadFile(f)
		if err != nil {
			warn = fmt.Sprintf("could not read system_prompt_file (%s): %v", f, err)
		} else {
			system = strings.TrimSpace(string(raw))
		}
	}

	if cfg.App.AgentsMD {
		res := agentsmd.Resolve(xdg.AgentsFile(), ".")
		if res.Text != "" {
			system = appendSystemBlock(system, res.Text)
		}
		if res.Warn != "" {
			warn = joinWarn(warn, res.Warn)
		}
	}

	if cfg.Tools.Enabled {
		sk := DiscoverSkills(cfg)
		if summary := skills.Summary(sk.Skills); summary != "" {
			system = appendSystemBlock(system, "Available skills (call read_file on their path for the full content):\n"+summary)
		}
		if sk.Warn != "" {
			warn = joinWarn(warn, sk.Warn)
		}
	}

	return system, warn
}

// DiscoverSkills resolves the rung-0 skills listing (§19.2/§19.4, Step 19),
// gated behind cfg.Tools.Enabled exactly like SystemPrompt's own fold above —
// a skill points the model at read_file to load its body, so discovering one
// for a session with no tools at all would name a capability nothing can
// reach. This is its own function, not inlined into SystemPrompt, because
// app.go's tui.Options.Skills (internal/tui's /skills command, skills.go)
// needs the same snapshot SystemPrompt already computed for the prompt
// without a second, slightly different copy of the gate drifting from this
// one — see Root.skills' own comment for why internal/tui never calls
// skills.Discover itself.
func DiscoverSkills(cfg *config.Config) skills.Result {
	if !cfg.Tools.Enabled {
		return skills.Result{}
	}
	return skills.Discover(cfg.Tools.SkillsDir)
}

// appendSystemBlock adds a block of rules to the end of the base system
// prompt, blank-line separated. Kept as its own function because an empty
// base prompt (the common case: most users never set system_prompt) must not
// grow a leading blank line — AGENTS.md content becomes the entire prompt in
// that case, not an addendum to nothing.
func appendSystemBlock(base, block string) string {
	if base == "" {
		return block
	}
	return base + "\n\n" + block
}

// joinWarn combines two warning strings the way BuildEngine's own fbLine/warn
// combination already does (engine.go), so a caller printing warn verbatim
// sees every problem instead of only the first or the last.
func joinWarn(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	return a + "; " + b
}

// Dialects are the dialects this binary can speak. `ishakat doctor` reports
// this instead of a hand-written list, because what matters is which init()
// actually got compiled in (§5.4).
func Dialects() []string { return provider.Kinds() }

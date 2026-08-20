package config

import (
	"fmt"
	"strings"
)

func Validate(c *Config) error {
	if c.Schema != Schema {
		return fmt.Errorf("schema = %d no soportado (esta versión entiende %d); "+
			"actualiza ishakat o corrige la primera línea de config.toml", c.Schema, Schema)
	}
	seen := map[string]bool{}
	for i := range c.Providers {
		p := &c.Providers[i]
		where := fmt.Sprintf("provider[%d]", i)
		if p.ID == "" {
			return fmt.Errorf("%s: falta id. Cada [[provider]] necesita un id único", where)
		}
		if seen[p.ID] {
			return fmt.Errorf("provider %q está declarado dos veces", p.ID)
		}
		seen[p.ID] = true
		if p.Kind == "" {
			return fmt.Errorf("provider %q: falta kind. Usa openai, anthropic o gemini", p.ID)
		}
		if p.BaseURL == "" {
			return fmt.Errorf("provider %q: falta base_url.\n  Ejemplo: base_url = \"https://api.openai.com/v1\"", p.ID)
		}
		if !validKind(p.Kind) {
			p.Enabled = false
			c.Warnings = append(c.Warnings, Warning{where,
				fmt.Sprintf("kind %q no soportado; el proveedor queda desactivado", p.Kind)})
		}
	}
	if err := validateTools(c); err != nil {
		return err
	}
	if err := validateKeys(c); err != nil {
		return err
	}
	validateServe(c)
	return nil
}

// validateServe checks docs/PLAN.md §11 Step 23's [serve] section. Unlike
// validateTools, none of these are fatal: a bad value here can be repaired by
// clamping it to a safe default and warning, because there is no unsafe
// interpretation the way there is for an unrecognised permission mode — the
// worst a bad serve setting can do is refuse connections or accept them
// without a limit, both recoverable at runtime.
func validateServe(c *Config) {
	s := &c.Serve
	if s.MaxSessions < 0 {
		c.Warnings = append(c.Warnings, Warning{"serve",
			"max_sessions cannot be negative; treated as 0 (no limit)"})
		s.MaxSessions = 0
	}
	if s.IdleTimeoutS < 0 {
		c.Warnings = append(c.Warnings, Warning{"serve",
			"idle_timeout_s cannot be negative; treated as 0 (no timeout)"})
		s.IdleTimeoutS = 0
	}
	if s.AllowToolCreate {
		c.Warnings = append(c.Warnings, Warning{"serve",
			"allow_tool_create = true: a serve session may propose tool_create. " +
				"Creation still requires an explicit permission_request answer over the socket (§19.7)"})
	}
	if !isLoopback(s.Addr) && s.Token == "" {
		c.Warnings = append(c.Warnings, Warning{"serve",
			fmt.Sprintf("addr = %q is not loopback and token is empty: "+
				"any host that can reach this address can open a session with no credential at all", s.Addr)})
	}
}

// isLoopback reports whether addr's host part is a loopback address, so
// validateServe can tell a local-only listener from one that might be
// reachable off-machine. It errs toward treating an unparseable or empty host
// as loopback (the shipped default has no host part before the colon does
// not apply here — "127.0.0.1:20129" always has one), since the alternative
// is warning on every malformed address on top of whatever already reported
// the malformation.
func isLoopback(addr string) bool {
	host := addr
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		host = addr[:i]
	}
	host = strings.Trim(host, "[]")
	switch host {
	case "127.0.0.1", "::1", "localhost", "":
		return true
	default:
		return false
	}
}

// validateTools checks the §19 agent layer. These are hard errors rather than
// warnings, unlike an unsupported provider kind: a misspelled permission mode
// has no safe interpretation. Silently treating an unrecognised `write = "alow"`
// as anything at all would either block every write or authorise every write,
// and the user would find out which one at the worst possible moment. Refusing
// to start is the only honest option.
func validateTools(c *Config) error {
	t := &c.Tools

	for _, f := range []struct {
		name  string
		value string
	}{
		{"read", t.Permissions.Read},
		{"write", t.Permissions.Write},
		{"shell", t.Permissions.Shell},
	} {
		if !validPermission(f.value) {
			return fmt.Errorf("[tools.permissions] %s = %q no reconocido; usa \"ask\", \"allow\" o \"deny\"",
				f.name, f.value)
		}
	}

	if !validEvolveMode(t.Evolve.Mode) {
		return fmt.Errorf("[tools.evolve] mode = %q no reconocido; usa \"off\", \"on_request\", \"suggest\" o \"auto\"",
			t.Evolve.Mode)
	}

	// A threshold outside (0,1] can never match, or matches everything: either
	// way the dedup gate of §19.6 silently stops doing its job, which is how a
	// catalogue of near-identical tools gets built.
	if t.Evolve.DedupThreshold <= 0 || t.Evolve.DedupThreshold > 1 {
		return fmt.Errorf("[tools.evolve] dedup_threshold = %g fuera de rango; debe estar en (0, 1]",
			t.Evolve.DedupThreshold)
	}

	for _, f := range []struct {
		name  string
		value int
	}{
		{"max_tools", t.MaxTools},
		{"archive_days", t.ArchiveDays},
		{"max_calls_per_turn", t.MaxCallsPerTurn},
		{"max_output_bytes", t.MaxOutputBytes},
		{"timeout_s", t.TimeoutS},
	} {
		if f.value < 0 {
			return fmt.Errorf("[tools] %s = %d no puede ser negativo", f.name, f.value)
		}
	}
	if t.BudgetUSD < 0 {
		return fmt.Errorf("[tools] budget_usd = %g no puede ser negativo (usa 0 para sin límite)", t.BudgetUSD)
	}

	// A cap of zero would mean the model can announce a tool call and never be
	// allowed to run it: every agentic turn would dead-end. Warn rather than
	// fail, since `enabled = false` is the supported way to turn tools off.
	if t.Enabled && t.MaxCallsPerTurn == 0 {
		c.Warnings = append(c.Warnings, Warning{"tools",
			"max_calls_per_turn = 0 con enabled = true: ninguna herramienta podrá ejecutarse. " +
				"Usa enabled = false para desactivar la capa de herramientas"})
	}

	// §19.7: self-extension needs a human at gate 2, and the loudest way to
	// say so is at start-up rather than at the moment a tool gets written.
	if t.Evolve.AllowWithoutTTY {
		c.Warnings = append(c.Warnings, Warning{"tools.evolve",
			"allow_without_tty = true: ishakat podrá crear herramientas sin que nadie autorice. " +
				"Preferí el flag --allow-tool-create en el script concreto que lo necesite"})
	}
	if t.Evolve.Mode != "off" && !t.Evolve.RequireSelftest {
		c.Warnings = append(c.Warnings, Warning{"tools.evolve",
			"require_selftest = false: una herramienta recién creada podrá usarse sin haberse probado"})
	}
	if t.Egress.AllowAll {
		c.Warnings = append(c.Warnings, Warning{"tools.egress",
			"allow_all = true: las herramientas podrán alcanzar cualquier host"})
	}

	return nil
}

func validPermission(p string) bool {
	switch p {
	case "ask", "allow", "deny":
		return true
	default:
		return false
	}
}

func validEvolveMode(m string) bool {
	switch m {
	case "off", "on_request", "suggest", "auto":
		return true
	default:
		return false
	}
}

// validKind lists the wire dialects an adapter actually exists for.
//
// "anthropic" and "gemini" are accepted since internal/provider/anthropic's
// and internal/provider/gemini's init()s register them (Fase 4). The
// practical effect of accepting an unregistered kind was that a provider
// preset or a hand-written config.toml loaded successfully, looked enabled
// in `provider list`, and only failed — with a confusing message naming a
// kind the user never typed — on its very first turn. Both providers are
// still reachable through the "openai" dialect too (see the provider
// presets in credentials.go); the day a native adapter for some other
// service exists, its kind string belongs back in this list next to its
// init()'s provider.Register call, not before.
func validKind(k string) bool {
	switch strings.ToLower(k) {
	case "openai", "responses", "anthropic", "gemini", "fake":
		return true
	default:
		return false
	}
}

func Mask(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return "…"
	}
	return "…" + s[len(s)-4:]
}

// fromEnv reports whether val is exactly the value expandVars substituted
// in for some "$VAR" it found somewhere in c — the same evidence
// expandVars itself records in Config.EnvUsed (expand.go), rather than
// guessing a value is a secret from its map key's name or its shape. A
// custom provider.headers entry can be called anything ("X-Api-Key",
// "X-Auth-Token", a gateway's own header name); the one thing every
// value that actually needs redacting has in common is that it came from
// the environment, exactly like provider.api_key does.
func fromEnv(envUsed map[string]string, val string) bool {
	if val == "" {
		return false
	}
	for _, v := range envUsed {
		if v == val {
			return true
		}
	}
	return false
}

// redactedHeaders masks every provider.headers value that fromEnv flags,
// leaving literal constants (an API version string, a fixed "X-Title")
// untouched — those are configuration, not credentials, and masking them
// would make a redacted /config view harder to read for no safety gain.
func redactedHeaders(h map[string]string, envUsed map[string]string) map[string]string {
	if h == nil {
		return nil
	}
	out := make(map[string]string, len(h))
	for k, v := range h {
		if fromEnv(envUsed, v) {
			v = Mask(v)
		}
		out[k] = v
	}
	return out
}

// redactedParams is redactedHeaders' counterpart for provider.params,
// whose values are toml's map[string]any rather than map[string]string:
// only a string value can possibly be something expandVars substituted,
// so a non-string (temperature = 0.7's float64, say) passes through
// unexamined.
func redactedParams(p map[string]any, envUsed map[string]string) map[string]any {
	if p == nil {
		return nil
	}
	out := make(map[string]any, len(p))
	for k, v := range p {
		if s, ok := v.(string); ok && fromEnv(envUsed, s) {
			v = Mask(s)
		}
		out[k] = v
	}
	return out
}

// Redacted returns a copy of c with every provider's api_key masked, plus
// any provider.headers/provider.params value that came from an expanded
// environment variable (redactedHeaders/redactedParams above) — a custom
// header carrying a secret for a nonstandard gateway (e.g. a header named
// "X-Api-Key" instead of the Authorization api_key field) is exactly as
// real a leak as api_key itself, and §1.7's original design spec for this
// file ("No path that logs to disk should use the Config without passing
// through here") does not carve out an exception for it. No path that
// shows a Config to a screen or a log should use c directly; this is the
// one it should use instead — the same rule /config's runner (Step 18)
// and any future /debug follow.
func (c *Config) Redacted() *Config {
	cp := *c
	cp.Providers = make([]Provider, len(c.Providers))
	for i, p := range c.Providers {
		pCp := p
		pCp.APIKey = Mask(p.APIKey)
		pCp.Headers = redactedHeaders(p.Headers, c.EnvUsed)
		pCp.Params = redactedParams(p.Params, c.EnvUsed)
		cp.Providers[i] = pCp
	}
	return &cp
}

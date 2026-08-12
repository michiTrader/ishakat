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
// "anthropic" and "gemini" used to be accepted here even though no init() in
// the tree ever registers either string with internal/provider (grep
// provider.Register across the codebase: only "openai", "responses" and,
// in tests, "fake"). The practical effect was that a provider preset or a
// hand-written config.toml with kind = "anthropic" loaded successfully,
// looked enabled in `provider list`, and only failed — with a confusing
// message naming a kind the user never typed — on its very first turn.
// Anthropic and Gemini are both reachable through the "openai" dialect (see
// the provider presets in credentials.go); the day a native adapter for
// either exists, its kind string belongs back in this list next to its
// init()'s provider.Register call, not before.
func validKind(k string) bool {
	switch strings.ToLower(k) {
	case "openai", "responses", "fake":
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

func (c *Config) Redacted() *Config {
	cp := *c
	cp.Providers = make([]Provider, len(c.Providers))
	for i, p := range c.Providers {
		pCp := p
		pCp.APIKey = Mask(p.APIKey)
		cp.Providers[i] = pCp
	}
	return &cp
}

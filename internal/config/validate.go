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
	return nil
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

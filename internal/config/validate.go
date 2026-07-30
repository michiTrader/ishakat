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
	return nil
}

func validKind(k string) bool {
	switch strings.ToLower(k) {
	case "openai", "anthropic", "gemini", "fake":
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

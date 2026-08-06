package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"

	"github.com/MichiTrader/ishakat/internal/xdg"
)

var varRe = regexp.MustCompile(`\$\{?([A-Za-z_][A-Za-z0-9_]*)\}?`)

// expandVars expands every "$VAR"/"${VAR}" in c, including each provider's
// api_key, and reports warnings for the ones a user would actually want to
// hear about.
//
// embeddedOnly is the P1 set from load.go: provider ids declared ONLY by
// the compiled-in defaults.toml, with no mention at all in anything the
// user wrote to disk (config.toml, a project's .ishakat.toml, or
// credentials.toml). For those — and only those — a missing credential
// disables the provider outright (Enabled = false) instead of leaving it
// enabled with a warning.
//
// The distinction matters because "enabled = true" in the *embedded*
// defaults.toml is not a decision the user made; it is this binary's own
// factory setting, offered so `provider list`/`provider add <id>` can see
// the preset without the user having typed a single line of TOML. Treating
// that shipped default as equivalent to a user's own `enabled = true` is
// what produced the original bug report this fix responds to: a fresh
// install warned on every single launch about $OMNIROUTE_API_KEY missing,
// for a provider the user never asked to activate and had no config.toml
// line to point at. A provider the user's own files *do* mention — even a
// bare `[[provider]] id = "omniroute"` with no other field — is presumed
// intentional and keeps the ordinary warn-don't-disable behaviour below.
func expandVars(c *Config, embeddedOnly map[string]bool) []Warning {
	var warns []Warning
	for i := range c.Providers {
		p := &c.Providers[i]
		raw := p.APIKey
		val, missing := expandString(raw, c.EnvUsed)
		p.APIKey = val
		switch {
		case raw == "":
			p.AuthOK = true
		case missing != "":
			p.AuthOK, p.MissingEnv = false, missing
			switch {
			case embeddedOnly[p.ID] && p.Enabled:
				// P1: nothing the user wrote asked for this provider, and
				// it has no working credential — there is nothing to warn
				// about because there was never a user decision to
				// second-guess. Silently disabling is the honest state:
				// `provider list` still shows it (as declared, disabled),
				// and `provider add <id>` still works exactly as before.
				p.Enabled = false
			case p.Enabled:
				// El warning visible solo tiene sentido para un proveedor que el
				// usuario efectivamente quiere usar. Con `enabled = false` (el
				// valor por defecto de openai/anthropic en config.example.toml,
				// pensado para quien solo usa OmniRoute) AuthOK/MissingEnv se
				// siguen registrando igual —por si algo más adelante consulta
				// ese estado— pero no se imprime ruido de arranque por una
				// variable que el usuario nunca pidió configurar.
				warns = append(warns, Warning{
					Where: "provider[" + p.ID + "]",
					Msg:   "missing $" + missing + "; the provider is left unauthenticated",
				})
			}
		default:
			p.AuthOK = true
		}
	}
	walkStrings(c, func(s string) string { v, _ := expandString(s, c.EnvUsed); return v })
	return warns
}

func expandString(s string, envUsed map[string]string) (string, string) {
	if s == "" {
		return "", ""
	}
	var firstMissing string
	out := varRe.ReplaceAllStringFunc(s, func(m string) string {
		match := varRe.FindStringSubmatch(m)
		if len(match) < 2 {
			return m
		}
		varName := match[1]

		var val string
		var found bool

		// XDG variables expand to the BASE directory, without the "ishakat"
		// suffix: the §5.2 TOML already writes that suffix itself
		// ("$XDG_DATA_HOME/ishakat/sessions"). Expanding to xdg.DataDir()
		// produced ~/.local/share/ishakat/ishakat/sessions.
		switch varName {
		case "XDG_CONFIG_HOME":
			val = xdg.ConfigHome()
			found = true
		case "XDG_CACHE_HOME":
			val = xdg.CacheHome()
			found = true
		case "XDG_DATA_HOME":
			val = xdg.DataHome()
			found = true
		case "XDG_STATE_HOME":
			val = xdg.StateHome()
			found = true
		default:
			val, found = os.LookupEnv(varName)
		}

		if !found || val == "" {
			if firstMissing == "" && !strings.HasPrefix(varName, "XDG_") {
				firstMissing = varName
			}
			return m
		}

		if envUsed != nil {
			envUsed["$"+varName] = val
		}
		return val
	})
	return out, firstMissing
}

func walkStrings(v any, fn func(string) string) {
	val := reflect.ValueOf(v)
	walkValue(val, fn)
}

func walkValue(val reflect.Value, fn func(string) string) {
	if !val.IsValid() {
		return
	}
	switch val.Kind() {
	case reflect.Ptr, reflect.Interface:
		if !val.IsNil() {
			walkValue(val.Elem(), fn)
		}
	case reflect.Struct:
		for i := 0; i < val.NumField(); i++ {
			f := val.Field(i)
			if f.CanSet() {
				if f.Kind() == reflect.String {
					f.SetString(fn(f.String()))
				} else {
					walkValue(f, fn)
				}
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < val.Len(); i++ {
			e := val.Index(i)
			if e.Kind() == reflect.String && e.CanSet() {
				e.SetString(fn(e.String()))
			} else {
				walkValue(e, fn)
			}
		}
	case reflect.Map:
		for _, k := range val.MapKeys() {
			elem := val.MapIndex(k)
			if elem.Kind() == reflect.String {
				newStr := fn(elem.String())
				val.SetMapIndex(k, reflect.ValueOf(newStr))
			}
		}
	}
}

// checkPerms warns if path is group/world accessible. Call only for the
// credentials layer (xdg.CredentialsFile()); config.toml and .ishakat.toml
// are shareable-by-design and always written at 0644, so running this
// against them would contradict SaveProviderConnection's own choice — see
// the call site comment in load.go.
func checkPerms(path string) []Warning {
	var warns []Warning
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	mode := info.Mode().Perm()
	if mode&0077 != 0 {
		warns = append(warns, Warning{
			Where: filepath.Base(path),
			Msg:   fmt.Sprintf("insecure permissions %#o (0600 recommended)", mode),
		})
	}
	return warns
}

var envMap = map[string]string{
	"ISHAKAT_MODEL":   "app.default_model",
	"ISHAKAT_THEME":   "ui.theme",
	"ISHAKAT_COLOR":   "ui.color",
	"ISHAKAT_NO_ANIM": "ui.animations.mode",
}

func applyEnv(raw map[string]any) {
	for envVar, targetPath := range envMap {
		if val, ok := os.LookupEnv(envVar); ok && val != "" {
			if envVar == "ISHAKAT_NO_ANIM" && val == "1" {
				val = "off"
			}
			setPath(raw, targetPath, val)
		}
	}
}

func setPath(raw map[string]any, path string, val any) {
	parts := strings.Split(path, ".")
	curr := raw
	for i, p := range parts {
		if i == len(parts)-1 {
			curr[p] = val
			return
		}
		next, ok := curr[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			curr[p] = next
		}
		curr = next
	}
}

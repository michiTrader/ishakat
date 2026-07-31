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

func expandVars(c *Config) []Warning {
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
			warns = append(warns, Warning{
				Where: "provider[" + p.ID + "]",
				Msg:   "falta $" + missing + "; el proveedor queda sin autenticar",
			})
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
			Msg:   fmt.Sprintf("permisos inseguros %#o (se recomienda 0600)", mode),
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

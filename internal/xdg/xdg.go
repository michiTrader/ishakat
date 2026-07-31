package xdg

import (
	"os"
	"path/filepath"
	"strings"
)

const App = "ishakat"

func home() string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h
	}
	return "."
}

func base(env string, def ...string) string {
	if v := os.Getenv(env); v != "" {
		return v
	}
	return filepath.Join(append([]string{home()}, def...)...)
}

// The *Home functions are the XDG base directories, without the app name.
// They exist because the configuration writes paths like
// "$XDG_DATA_HOME/ishakat/sessions" (§5.2): if $XDG_DATA_HOME expanded to
// DataDir(), which already carries the ishakat suffix, the result would be
// ~/.local/share/ishakat/ishakat/sessions. That bug existed and this pair of
// functions is what closes it.
func ConfigHome() string { return base("XDG_CONFIG_HOME", ".config") }
func CacheHome() string  { return base("XDG_CACHE_HOME", ".cache") }
func DataHome() string   { return base("XDG_DATA_HOME", ".local", "share") }
func StateHome() string  { return base("XDG_STATE_HOME", ".local", "state") }

func ConfigDir() string { return filepath.Join(ConfigHome(), App) }
func CacheDir() string  { return filepath.Join(CacheHome(), App) }
func DataDir() string   { return filepath.Join(DataHome(), App) }
func StateDir() string  { return filepath.Join(StateHome(), App) }

func ConfigFile() string  { return filepath.Join(ConfigDir(), "config.toml") }
func ThemesDir() string   { return filepath.Join(ConfigDir(), "themes") }
func CatalogFile() string { return filepath.Join(CacheDir(), "catalog.json") }
func SessionsDir() string { return filepath.Join(DataDir(), "sessions") }
func ErrorFile() string   { return filepath.Join(StateDir(), "last-error.json") }

// EnsureDir crea un directorio con permisos 0700 (§8.1).
func EnsureDir(p string) error { return os.MkdirAll(p, 0o700) }

// IsTermux determina si estamos ejecutando dentro de Termux.
func IsTermux() bool {
	if strings.Contains(os.Getenv("PREFIX"), "com.termux") {
		return true
	}
	_, err := os.Stat("/data/data/com.termux/files/usr")
	return err == nil
}

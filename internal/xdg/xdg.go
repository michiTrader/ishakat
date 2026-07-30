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

func ConfigDir() string  { return filepath.Join(base("XDG_CONFIG_HOME", ".config"), App) }
func CacheDir() string   { return filepath.Join(base("XDG_CACHE_HOME", ".cache"), App) }
func DataDir() string    { return filepath.Join(base("XDG_DATA_HOME", ".local", "share"), App) }
func StateDir() string   { return filepath.Join(base("XDG_STATE_HOME", ".local", "state"), App) }

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

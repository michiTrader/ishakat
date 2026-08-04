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

// HomeResolved reports whether the OS could tell us the user's home
// directory. When it can't (a stripped-down container, a CI runner with no
// $HOME, a broken environment), home() above silently falls back to the
// current working directory — harmless for anything that only *reads*
// config, since the embedded defaults still apply, but never safe for code
// about to *write* a secret: "." can be a cloned git repository, and a
// credentials file dropped there has nothing stopping it from being
// committed and pushed. Callers about to persist something sensitive must
// check this first and refuse instead of silently writing into whatever
// directory the process happened to start in.
func HomeResolved() bool {
	h, err := os.UserHomeDir()
	return err == nil && h != ""
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

func ConfigFile() string      { return filepath.Join(ConfigDir(), "config.toml") }
func CredentialsFile() string { return filepath.Join(ConfigDir(), "credentials.toml") }
func ThemesDir() string       { return filepath.Join(ConfigDir(), "themes") }
func CatalogFile() string     { return filepath.Join(CacheDir(), "catalog.json") }
func SessionsDir() string     { return filepath.Join(DataDir(), "sessions") }
func ErrorFile() string       { return filepath.Join(StateDir(), "last-error.json") }

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

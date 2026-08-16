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

// AgentsFile is the global layer of Step 18's AGENTS.md precedence (docs/PLAN.md
// §11): standing rules the user wants applied to every project, the same way
// config.toml holds settings that apply everywhere until a project overrides
// them. It lives beside config.toml rather than under DataDir/CacheDir because
// it is meant to be hand-edited, exactly like config.toml itself.
func AgentsFile() string  { return filepath.Join(ConfigDir(), "AGENTS.md") }
func CatalogFile() string { return filepath.Join(CacheDir(), "catalog.json") }
func SessionsDir() string { return filepath.Join(DataDir(), "sessions") }
func ErrorFile() string   { return filepath.Join(StateDir(), "last-error.json") }

// UsageFile is §19.7's crystallization-by-observation ledger: one JSON
// line per normalized bash/fetch invocation pattern, with a count and a
// last-seen date. It lives under StateDir, not DataDir or CacheDir,
// because it is neither user data worth backing up (SessionsDir) nor a
// disposable cache that is safe to delete and silently redownload
// (CatalogFile) — losing it only degrades suggest mode's memory of what
// has repeated before, exactly the kind of "frequently changed state
// that is not quite a cache" $XDG_STATE_HOME exists for. Path matches
// §19.7's own worked example verbatim:
// "$XDG_STATE_HOME/ishakat/usage.jsonl".
func UsageFile() string { return filepath.Join(StateDir(), "usage.jsonl") }

// SuggestStateFile is §19.7's suggestion budget/decay bookkeeping: how many
// suggestions have been shown this week, and how many consecutive rejections
// have piled up. It is deliberately a separate file from UsageFile, not a
// field folded into it: usage.jsonl is an append-only observation ledger
// that a user might reasonably hand-edit or truncate, while this file is
// small, mutable counter state that must round-trip exactly for the weekly
// budget and the decay-to-on_request rule to behave correctly. Keeping them
// apart means a corrupted or hand-edited ledger can never desynchronize the
// budget/decay counters, and vice versa. Lives under StateDir for the same
// reason UsageFile does: frequently changed, not worth backing up, not a
// disposable cache.
func SuggestStateFile() string { return filepath.Join(StateDir(), "suggest-state.json") }

// TrustFile is §21.4 layer 2's own persisted answer: one JSON record per
// project path the human has already been asked "how should I work here?",
// so the question is not asked again on the next run — Pi's own
// ~/.pi/agent/trust.json, adapted (docs/PLAN.md §21.2/§21.4). It lives
// under StateDir, not DataDir or CacheDir, for the same reasoning
// UsageFile's own comment already gives: this is neither backed-up user
// data nor a disposable cache safe to silently redownload — losing it only
// means the trust question is asked again, exactly the "frequently
// changed, small, not worth backing up" shape $XDG_STATE_HOME exists for.
func TrustFile() string { return filepath.Join(StateDir(), "trust.json") }

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

// Package agentsmd implements Step 18 of docs/PLAN.md §11: a project's
// AGENTS.md rules, read at three precedence levels — global, project, local —
// and merged into text a caller appends to the system prompt, so the user
// never has to repeat standing instructions on every message.
//
// This mirrors §5.1's own config precedence in shape ("un archivo de usuario
// ..., opcionalmente ./.ishakat.toml por proyecto, que se fusiona encima")
// but not in merge semantics: config.toml layers *replace* a value when a
// higher layer sets it, while prose rules do not have a natural "replace"
// operation — a project rule does not erase whatever the user's global rules
// said, it adds to them. So the three layers here are concatenated, low
// precedence first, rather than merged key by key.
//
// The package is deliberately pure: it takes paths in and returns text out,
// with no dependency on internal/config or the XDG layout, so it can be
// tested without touching $HOME and so internal/tools (§19, not built yet)
// can read the same files without an import cycle back into internal/app.
package agentsmd

import (
	"os"
	"path/filepath"
	"strings"
)

// ProjectName and LocalName are the two per-project files, read from
// projectDir. ProjectName is meant to be committed and shared with the team,
// the same way §5.1's ".ishakat.toml" is; LocalName is meant to be
// gitignored, for machine- or person-specific rules that should not leak into
// a shared repository (§0.2's own instruction to keep credentials and
// per-developer noise out of what gets committed).
const (
	ProjectName = "AGENTS.md"
	LocalName   = "AGENTS.local.md"
)

// Layer names one of the three precedence levels, used only for diagnostics
// (Result.Files says which path won; a caller wanting to say *why* uses this).
type Layer int

const (
	Global Layer = iota
	Project
	Local
)

func (l Layer) String() string {
	switch l {
	case Global:
		return "global"
	case Project:
		return "project"
	case Local:
		return "local"
	default:
		return "unknown"
	}
}

// Source is one layer's resolved path, whether or not the file exists.
type Source struct {
	Layer Layer
	Path  string
}

// Sources returns the three paths Resolve reads from, in low-to-high
// precedence order, without touching the filesystem. Exposed separately from
// Resolve so `doctor` (Step 18's own closing criterion includes being able to
// see which files are in play) can report all three paths even when some of
// them do not exist yet.
func Sources(globalPath, projectDir string) []Source {
	return []Source{
		{Layer: Global, Path: globalPath},
		{Layer: Project, Path: filepath.Join(projectDir, ProjectName)},
		{Layer: Local, Path: filepath.Join(projectDir, LocalName)},
	}
}

// Result is what Resolve found.
type Result struct {
	// Text is every layer that was found and read successfully, concatenated
	// low precedence first (global, then project, then local) and separated
	// by a blank line. Empty when no layer exists.
	Text string

	// Files are the paths that actually contributed to Text, in the same
	// precedence order. A path missing from this slice either does not exist
	// (the ordinary case — most projects have none of the three) or failed to
	// read, which Warn explains.
	Files []string

	// Warn names the first layer that exists but could not be read (a
	// permission error, a directory where a file was expected, ...). A
	// missing file is not a warning — most projects have none of the three,
	// and §5.2's own system_prompt_file precedent treats "not set" as the
	// ordinary case, not an error. Empty when there is nothing to report.
	Warn string
}

// Resolve reads the three AGENTS.md layers and merges them.
//
// globalPath is normally xdg.AgentsFile(); passed in rather than resolved
// here so a caller — and every test in this package — can point it at a
// temporary directory instead of the real $HOME, the same reason
// config.Options.UserPath is a parameter rather than a hardcoded call to
// xdg.ConfigFile() inside config.Load.
//
// projectDir is normally "." (the current working directory), matching
// config.Options.ProjectPath's own default of "./.ishakat.toml" — both read
// project-local files relative to wherever ishakat was started, not relative
// to some resolved absolute root, because there is no repository-root
// detection in this codebase yet and inventing one is out of scope here.
func Resolve(globalPath, projectDir string) Result {
	var res Result
	var parts []string

	for _, src := range Sources(globalPath, projectDir) {
		body, err := os.ReadFile(src.Path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			if res.Warn == "" {
				res.Warn = "could not read " + src.Layer.String() + " AGENTS.md (" + src.Path + "): " + err.Error()
			}
			continue
		}
		trimmed := strings.TrimSpace(string(body))
		if trimmed == "" {
			// An empty file is not an error and not a warning: it is
			// indistinguishable in intent from "no rules yet", and a
			// warning here would fire on every run of a project that
			// created the file with `touch` before writing anything into
			// it.
			continue
		}
		parts = append(parts, trimmed)
		res.Files = append(res.Files, src.Path)
	}

	res.Text = strings.Join(parts, "\n\n")
	return res
}

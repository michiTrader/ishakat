// Package skills implements rung 0 of §19.2's crystallization ladder:
// knowledge in prose, one `SKILL.md` per subdirectory of a skills directory
// (normally cfg.Tools.SkillsDir, `$XDG_DATA_HOME/ishakat/skills`), discovered
// at startup and offered to the model as an addition to the system prompt.
//
// §19.4 makes progressive disclosure mandatory: only a skill's name and
// description — not its body — may enter the system prompt, at roughly 15
// tokens each. A skill's actual content (the "~2.000-8.000 tok" §19.2's own
// ladder prices it at) loads only when the model decides it needs that
// skill, by calling the read_file core tool on the path this package
// reports — there is no separate "load skill" tool, because read_file
// already does exactly that job and a second tool for the same operation
// would just be one more name competing for the model's attention (§19.6's
// own reasoning against near-duplicate tools, applied here to skills
// instead of tools).
//
// This package is deliberately pure, mirroring internal/agentsmd's own
// stated reason for being so: it takes a directory path in and returns
// structured data out, with no dependency on internal/config or the XDG
// layout, so it is testable without touching $HOME and so a caller in
// internal/tools or internal/app can use it without an import cycle. It also
// parses only the two frontmatter fields SKILL.md's own convention actually
// needs (name, description) with a deliberately minimal line-based reader,
// not a general YAML parser — see parseFrontmatter's own doc comment for
// why a full YAML dependency would violate §6.4's zero-new-dependency
// budget for a feature this narrow.
package skills

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// FileName is the file every skill directory must contain. A directory
// without one is silently skipped (not every subdirectory of a skills
// directory need be a skill — a user may keep notes or drafts alongside),
// exactly as agentsmd.Resolve treats a missing layer as the ordinary case
// rather than an error.
const FileName = "SKILL.md"

// Skill is one discovered capability: enough to both list it in the system
// prompt (Name, Description) and to point the model at its full body
// (File) without this package ever reading that body itself unless asked.
type Skill struct {
	// Name identifies the skill. From the frontmatter's name field when
	// present and non-empty; falls back to the directory's own base name
	// otherwise, so a skill author who forgets the field still gets a
	// usable, stable identifier rather than an empty string breaking
	// Summary's output.
	Name string

	// Description is the one-line, model-facing summary of what the skill
	// is for — the only prose besides Name that Summary ever emits, per
	// §19.4's progressive-disclosure rule. Empty when the frontmatter did
	// not declare one; Discover still reports the skill (an empty
	// description is a quality problem for the author to fix, not a reason
	// to hide the skill entirely).
	Description string

	// Dir is the skill's own directory, absolute or relative exactly as
	// the directory Discover walked was.
	Dir string

	// File is the path to Dir's SKILL.md, what a model calls read_file on
	// to load the skill's full body.
	File string
}

// Result is what Discover found.
type Result struct {
	// Skills are every subdirectory of the scanned directory that contained
	// a readable, parseable SKILL.md, sorted by Name for a stable listing
	// run to run — directory iteration order is not guaranteed and a
	// system prompt that reshuffled skills between runs would be as
	// unhelpful as registry.go's own doc comment says an unordered tool
	// list would be.
	Skills []Skill

	// Warn names the first subdirectory whose SKILL.md exists but could not
	// be read or parsed (a permission error, a frontmatter with no closing
	// "---", ...). A missing SKILL.md is not a warning — most subdirectories
	// of a skills directory, and the skills directory not existing at all
	// (the ordinary case for a fresh install with none configured yet), are
	// both unremarkable rather than an error.
	Warn string
}

// Discover reads every immediate subdirectory of dir for a SKILL.md and
// parses its frontmatter. dir not existing at all is not an error — it
// returns a zero Result, the same way a fresh ishakat install has never
// created $XDG_DATA_HOME/ishakat/skills until a skill is actually placed
// there.
func Discover(dir string) Result {
	var res Result
	if dir == "" {
		return res
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return res
		}
		res.Warn = "could not read skills directory (" + dir + "): " + err.Error()
		return res
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillDir := filepath.Join(dir, entry.Name())
		file := filepath.Join(skillDir, FileName)

		body, err := os.ReadFile(file)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			if res.Warn == "" {
				res.Warn = "could not read " + file + ": " + err.Error()
			}
			continue
		}

		front, _, err := splitFrontmatter(string(body))
		if err != nil {
			if res.Warn == "" {
				res.Warn = "could not parse frontmatter in " + file + ": " + err.Error()
			}
			continue
		}
		fields := parseFrontmatter(front)

		name := fields["name"]
		if name == "" {
			name = entry.Name()
		}

		res.Skills = append(res.Skills, Skill{
			Name:        name,
			Description: fields["description"],
			Dir:         skillDir,
			File:        file,
		})
	}

	sort.Slice(res.Skills, func(i, j int) bool { return res.Skills[i].Name < res.Skills[j].Name })
	return res
}

// Body returns SKILL.md's content with the frontmatter block stripped —
// what the model actually reads once it has decided to use s, and what
// /skills' detail view (internal/tui) shows for one skill. A caller wanting
// the raw file including frontmatter should call the read_file tool
// directly instead; this method exists for the cases (progressive
// disclosure's own "body loads only when selected" and the TUI's inspect
// view) that want the prose without the metadata block repeated in front of
// it.
func (s Skill) Body() (string, error) {
	raw, err := os.ReadFile(s.File)
	if err != nil {
		return "", err
	}
	_, body, err := splitFrontmatter(string(raw))
	if err != nil {
		// A body that failed frontmatter parsing at Discover time would
		// already be absent from a Result's Skills slice, so reaching this
		// branch means the file changed on disk between Discover and Body.
		// Returning the raw content is more useful than an error the
		// caller cannot act on.
		return strings.TrimSpace(string(raw)), nil
	}
	return body, nil
}

// Summary renders the §19.4 progressive-disclosure listing: one
// "name: description" line per skill, in Discover's already-sorted order,
// and nothing else — no body, no path, no frontmatter noise. This is the
// literal text a caller appends to the system prompt (internal/app/wiring.go,
// following the same appendSystemBlock pattern Step 18's AGENTS.md
// integration already established) and the text /skills prints verbatim
// when at least one skill is loaded.
func Summary(list []Skill) string {
	if len(list) == 0 {
		return ""
	}
	var b strings.Builder
	for i, sk := range list {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(sk.Name)
		b.WriteString(": ")
		if sk.Description == "" {
			b.WriteString("(sin descripcion)")
		} else {
			b.WriteString(sk.Description)
		}
	}
	return b.String()
}

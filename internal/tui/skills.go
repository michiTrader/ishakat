// skills.go implements /skills (§13, Step 19): a read-only listing of the
// rung-0 prose capabilities (§19.2's crystallization ladder) already
// discovered at startup by internal/app.SystemPrompt and handed to this
// package once as m.skills (see Root.skills' own comment on why this
// package never touches the filesystem to find a SKILL.md itself, the same
// "read once, hand over" rule the catalog and system prompt already
// follow).
//
// This mirrors models.go's own shape deliberately: both are a single
// slashNotice built from a snapshot Root already holds, never a network or
// disk call from inside Update. Unlike /models there is no "active" row to
// mark — a skill is either loaded into the system prompt or it is not, and
// every entry in m.skills.Skills already is.
package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// runSkillsCommand renders every skill Discover found, name and description
// only — the exact §19.4 progressive-disclosure listing already sent to the
// model, so /skills can never claim a capability the prompt itself does not
// also name. An empty list is reported instead of a blank notice with no
// explanation, the same "no hay catalogo" rule runModelsCommand already
// applies to a nil catalog; sk.Warn (a SKILL.md that failed to parse) is
// surfaced alongside the list rather than silently swallowed, since a user
// wondering "why isn't my skill showing up" is exactly who needs to see it.
func (m Root) runSkillsCommand() (tea.Model, tea.Cmd) {
	g := m.lay.glyphs()
	sk := m.skills

	if len(sk.Skills) == 0 {
		msg := g.warnMark + " no hay skills cargadas"
		if sk.Warn != "" {
			msg += "\n  " + g.warnMark + " " + sk.Warn
		}
		return m.slashNotice(msg)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s skills %s %d", g.assistantMark, g.dot, len(sk.Skills))
	if sk.Warn != "" {
		fmt.Fprintf(&b, "\n  %s %s", g.warnMark, sk.Warn)
	}
	for _, s := range sk.Skills {
		desc := s.Description
		if desc == "" {
			desc = "(sin descripcion)"
		}
		fmt.Fprintf(&b, "\n  %s %-24s  %s", g.modelMark, s.Name, desc)
	}

	return m.slashNotice(b.String())
}

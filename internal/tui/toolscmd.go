// toolscmd.go implements /tools (§13, Step 20's own left-over UI half):
// see tools.go's doc comment for the §6.1 boundary that shaped this as an
// interface (ToolsLister) rather than a direct internal/tools import, and
// for why nil is a supported Root.toolsLister value.
//
// Two shapes, mirroring §13's own two rows for this step: bare "/tools"
// renders every layer-2 tool's status/danger/usage as a row each (the
// in-session counterpart to tool_list's LLM-facing text blob); "/tools
// code <name>" renders one tool's manifest in full. Both are read-only —
// there is no write/mutate path here (that is Step 21's larger,
// governance-gated increment: audit/create/edit/delete/revive).
package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// runToolsCommand dispatches on args: no args (or anything that isn't the
// "code <name>" shape) lists every tool; "code <name>" shows one
// manifest. m.toolsLister == nil (every test in this package that never
// sets Options.ToolsLister, plus any real session with [tools].enabled =
// false or an empty tools.dir) reports "nothing configured" instead of
// panicking, the same nil-is-safe rule runResumeCommand already follows
// for sessionLister.
func (m Root) runToolsCommand(args string) (tea.Model, tea.Cmd) {
	g := m.lay.glyphs()
	if m.toolsLister == nil {
		return m.slashNotice(g.warnMark + " no hay herramientas de capa 2 configuradas todavia")
	}

	if name, ok := parseToolsCodeArg(args); ok {
		manifest, err := m.toolsLister.ToolManifest(name)
		if err != nil {
			return m.slashNotice(g.warnMark + " " + err.Error())
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%s tools code %s %s", g.assistantMark, g.dot, name)
		fmt.Fprintf(&b, "\n\n%s", manifest)
		return m.slashNotice(b.String())
	}

	res := m.toolsLister.ListTools()
	if len(res.Tools) == 0 {
		msg := g.warnMark + " no se ha creado ninguna herramienta de capa 2 todavia"
		if res.Warn != "" {
			msg += "\n  " + g.warnMark + " " + res.Warn
		}
		return m.slashNotice(msg)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s tools %s %d", g.assistantMark, g.dot, len(res.Tools))
	if res.Warn != "" {
		fmt.Fprintf(&b, "\n  %s %s", g.warnMark, res.Warn)
	}
	for _, t := range res.Tools {
		lastUsed := t.LastUsed
		if lastUsed == "" {
			lastUsed = "never"
		}
		fmt.Fprintf(&b, "\n  %s %-24s  danger=%-6s state=%-10s use_count=%-4d last_used=%s",
			g.modelMark, t.Name, t.Danger, t.State, t.UseCount, lastUsed)
		if t.State == "broken" && t.LastError != "" {
			fmt.Fprintf(&b, "\n    %s %s", g.warnMark, t.LastError)
		}
	}

	return m.slashNotice(b.String())
}

// parseToolsCodeArg recognizes the "code <name>" shape of /tools' own
// args string. Anything else (empty, a different first word, "code"
// alone with no name) is not the code subcommand — the caller falls back
// to the bare listing.
func parseToolsCodeArg(args string) (string, bool) {
	args = strings.TrimSpace(args)
	rest, ok := cutPrefixWord(args, "code")
	if !ok {
		return "", false
	}
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return "", false
	}
	return rest, true
}

// cutPrefixWord reports whether s begins with word as a whole word (word
// itself, or word followed by whitespace) and returns whatever follows.
// "codex foo" must not match "code" — only "code" or "code foo" should.
func cutPrefixWord(s, word string) (string, bool) {
	if !strings.HasPrefix(s, word) {
		return "", false
	}
	rest := s[len(word):]
	if rest != "" && !strings.HasPrefix(rest, " ") && !strings.HasPrefix(rest, "\t") {
		return "", false
	}
	return rest, true
}

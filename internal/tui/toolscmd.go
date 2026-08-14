// toolscmd.go implements /tools (§13, Step 20's own listing plus Step
// 21's own audit/revive/delete rows): see tools.go's doc comment for the
// §6.1 boundary that shaped this as an interface (ToolsLister) rather
// than a direct internal/tools import, and for why nil is a supported
// Root.toolsLister value.
//
// Five shapes, mirroring §13's own rows: bare "/tools" renders every
// layer-2 tool's status/danger/usage as a row each (the in-session
// counterpart to tool_list's LLM-facing text blob); "/tools code <name>"
// renders one tool's manifest in full; "/tools audit" renders every
// tool's provenance (created_by/reason/repetitions/session_id/sources)
// plus its current SHA-256 and a tamper flag (§19.8 mitigations 2 and 6);
// "/tools revive <name>" calls ToolsLister.ReviveTool and reports its
// status line; "/tools delete <name> [confirm]" calls ToolsLister.
// DeleteTool and reports its status line. The first three are read-only.
// revive needs no confirmation step (§19.5's Archive/Revive pair is
// DangerLow and idempotent by construction); delete does — the trailing
// literal word "confirm" is this command's own explicit, typed gate,
// the slash-command counterpart to tool_delete's own required boolean
// argument (§19.5: "removes it, with confirmation"). create/edit remain
// Step 21's still-open rows.
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

	if isToolsAuditArg(args) {
		return m.renderToolsAudit()
	}

	if name, ok := parseToolsReviveArg(args); ok {
		status, err := m.toolsLister.ReviveTool(name)
		if err != nil {
			return m.slashNotice(g.warnMark + " " + err.Error())
		}
		return m.slashNotice(g.assistantMark + " tools revive " + g.dot + " " + status)
	}

	if name, confirm, ok := parseToolsDeleteArg(args); ok {
		status, err := m.toolsLister.DeleteTool(name, confirm)
		if err != nil {
			return m.slashNotice(g.warnMark + " " + err.Error())
		}
		return m.slashNotice(g.assistantMark + " tools delete " + g.dot + " " + status)
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

// renderToolsAudit implements "/tools audit" (§19.8 mitigation 2,
// verbatim: "Every tool records sources ... and session_id. /tools audit
// lists everything with origin and SHA-256.") plus mitigation 6's tamper
// signal, one line per field per tool (rather than tools' single dense
// line) since a provenance report is read closely, not skimmed the way
// the bare listing's status line is.
func (m Root) renderToolsAudit() (tea.Model, tea.Cmd) {
	g := m.lay.glyphs()
	res := m.toolsLister.AuditTools()
	if len(res.Tools) == 0 {
		msg := g.warnMark + " no se ha creado ninguna herramienta de capa 2 todavia"
		if res.Warn != "" {
			msg += "\n  " + g.warnMark + " " + res.Warn
		}
		return m.slashNotice(msg)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s tools audit %s %d", g.assistantMark, g.dot, len(res.Tools))
	if res.Warn != "" {
		fmt.Fprintf(&b, "\n  %s %s", g.warnMark, res.Warn)
	}
	for _, t := range res.Tools {
		createdBy := t.CreatedBy
		if createdBy == "" {
			createdBy = "unknown"
		}
		fmt.Fprintf(&b, "\n  %s %s", g.modelMark, t.Name)
		fmt.Fprintf(&b, "\n    created_by=%s repetitions=%d session_id=%s", createdBy, t.Repetitions, orNever(t.SessionID))
		if t.Reason != "" {
			fmt.Fprintf(&b, "\n    reason=%q", t.Reason)
		}
		if len(t.Sources) > 0 {
			fmt.Fprintf(&b, "\n    sources=%s", strings.Join(t.Sources, ", "))
		} else {
			fmt.Fprintf(&b, "\n    sources=none")
		}
		if t.HashError != "" {
			fmt.Fprintf(&b, "\n    %s sha256 unavailable: %s", g.warnMark, t.HashError)
			continue
		}
		fmt.Fprintf(&b, "\n    sha256=%s", t.Hash)
		if t.Tampered {
			fmt.Fprintf(&b, "\n    %s tampered: on-disk content changed since the last successful probe", g.warnMark)
		}
	}

	return m.slashNotice(b.String())
}

// orNever returns s, or the literal "never" when s is empty — the same
// convention runToolsCommand already uses for an unset LastUsed, reused
// here for an unset SessionID (a tool created outside any recorded
// session, or hand-written before this bookkeeping existed).
func orNever(s string) string {
	if s == "" {
		return "never"
	}
	return s
}

// isToolsAuditArg reports whether args (already known not to be the
// "code <name>" shape) is the bare "audit" subcommand — the same
// whole-word matching cutPrefixWord already applies to "code", so
// "auditx" cannot false-match. Unlike "code", "audit" takes no further
// argument: anything after it is not recognized as this subcommand and
// falls through to the bare listing instead of silently being ignored.
func isToolsAuditArg(args string) bool {
	return strings.TrimSpace(args) == "audit"
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

// parseToolsReviveArg recognizes the "revive <name>" shape of /tools' own
// args string, the exact mirror of parseToolsCodeArg for the other
// single-argument subcommand. Anything else (empty, a different first
// word, "revive" alone with no name) is not the revive subcommand — the
// caller falls back to the bare listing.
func parseToolsReviveArg(args string) (string, bool) {
	args = strings.TrimSpace(args)
	rest, ok := cutPrefixWord(args, "revive")
	if !ok {
		return "", false
	}
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return "", false
	}
	return rest, true
}

// parseToolsDeleteArg recognizes the "delete <name>" and "delete <name>
// confirm" shapes of /tools' own args string. The trailing literal word
// "confirm" is this command's own explicit, typed gate — matching
// tool_delete's own required boolean argument (§19.5: "removes it, with
// confirmation") — so confirm is reported true only when that exact
// trailing word is present; "delete weather" (no confirm) reports
// confirm=false, the safe default, the same "no safe reading, only a
// coin flip" logic tool_delete.go's own toolDeleteArgs.Confirm doc
// comment already applies to a fatal action. Anything past the name and
// an optional "confirm" (e.g. "delete weather confirm now") is not
// recognized as this subcommand at all — the caller falls back to the
// bare listing rather than silently ignoring trailing garbage after a
// destructive command's own confirmation word.
func parseToolsDeleteArg(args string) (name string, confirm bool, ok bool) {
	args = strings.TrimSpace(args)
	rest, matched := cutPrefixWord(args, "delete")
	if !matched {
		return "", false, false
	}
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return "", false, false
	}
	if withoutConfirm, hasConfirm := cutSuffixWord(rest, "confirm"); hasConfirm {
		name = strings.TrimSpace(withoutConfirm)
		if name == "" {
			return "", false, false
		}
		return name, true, true
	}
	if strings.ContainsAny(rest, " \t") {
		// A second word that is not exactly "confirm" (e.g. "weather now")
		// is not this subcommand's shape at all.
		return "", false, false
	}
	return rest, false, true
}

// cutSuffixWord is cutPrefixWord's mirror: reports whether s ends with
// word as a whole word (word itself, or preceded by whitespace) and
// returns whatever precedes it. "unconfirm" must not match "confirm" —
// only "confirm" or "<name> confirm" should.
func cutSuffixWord(s, word string) (string, bool) {
	if !strings.HasSuffix(s, word) {
		return "", false
	}
	prefix := s[:len(s)-len(word)]
	if prefix != "" && !strings.HasSuffix(prefix, " ") && !strings.HasSuffix(prefix, "\t") {
		return "", false
	}
	return prefix, true
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

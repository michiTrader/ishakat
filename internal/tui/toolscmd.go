// toolscmd.go implements /tools (§13, Step 20's own listing plus Step
// 21's own audit/revive/delete/edit rows): see tools.go's doc comment for
// the §6.1 boundary that shaped this as an interface (ToolsLister) rather
// than a direct internal/tools import, and for why nil is a supported
// Root.toolsLister value.
//
// Seven shapes, mirroring §13's own rows: bare "/tools" renders every
// layer-2 tool's status/danger/usage as a row each (the in-session
// counterpart to tool_list's LLM-facing text blob); "/tools code <name>"
// renders one tool's manifest in full; "/tools audit" renders every
// tool's provenance (created_by/reason/repetitions/session_id/sources)
// plus its current SHA-256 and a tamper flag (§19.8 mitigations 2 and 6);
// "/tools revive <name>" calls ToolsLister.ReviveTool and reports its
// status line; "/tools delete <name> [confirm]" calls ToolsLister.
// DeleteTool and reports its status line; "/tools edit <name>" (a
// multi-line shape, see parseToolsEditArg's own doc comment) calls
// ToolsLister.EditTool and reports its status line; "/tools create
// <name> [--force]" (a different multi-line, key:value shape, see
// parseToolsCreateArg's own doc comment) calls ToolsLister.CreateTool
// and reports its status line. The first three are read-only. revive
// needs no confirmation step (§19.5's Archive/Revive pair is DangerLow
// and idempotent by construction); delete does — the trailing literal
// word "confirm" is this command's own explicit, typed gate, the
// slash-command counterpart to tool_delete's own required boolean
// argument (§19.5: "removes it, with confirmation"). edit and create
// take no separate confirmation step either: like revive, tool_edit.go's
// own hard blocks (checkManifestSafety, the re-parse check) and
// tool_create.go's own hard blocks (evolve.Evaluate unless --force,
// checkManifestSafety always) already refuse anything dangerous before a
// single byte reaches disk, and the resulting status line (a demotion to
// unverified for edit, a fresh "state: unverified" for create — never a
// silent success) is itself the safety net a confirmation step would
// otherwise exist to provide. Step 21's backlog is now fully closed for
// rung 1.
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

	if name, oldString, newString, replaceAll, ok := parseToolsEditArg(args); ok {
		status, err := m.toolsLister.EditTool(name, oldString, newString, replaceAll)
		if err != nil {
			return m.slashNotice(g.warnMark + " " + err.Error())
		}
		return m.slashNotice(g.assistantMark + " tools edit " + g.dot + " " + status)
	}

	if fields, force, ok := parseToolsCreateArg(args); ok {
		status, err := m.toolsLister.CreateTool(fields.name, fields.description, fields.url, fields.method, fields.reason, fields.sources, force)
		if err != nil {
			return m.slashNotice(g.warnMark + " " + err.Error())
		}
		return m.slashNotice(g.assistantMark + " tools create " + g.dot + " " + status)
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

// parseToolsEditArg recognizes /tools edit's own multi-line shape:
//
//	edit <name>
//	<old_string, one or more lines>
//	---
//	<new_string, zero or more lines>
//	[replace_all]
//
// old_string/new_string are typed across multiple literal lines (the
// TUI's own ctrl+j binding inserts a real "\n" into the input without
// submitting — see root.go's own keys.Newline doc comment; slash.Parse
// only ever cuts on the first space, so this whole multi-line string
// survives intact into args) because tool_edit's own old_string/
// new_string contract is exact-text, verbatim, arbitrary-length TOML —
// the same shape edit_file's own arguments have, and just as unsuited to
// a single space-separated command line as edit_file's own arguments
// would be. The literal separator line "---" (its own line, nothing
// else on it once trimmed) is this command's own invented convention —
// no existing delimiter convention for a multi-field slash-command
// argument exists elsewhere in this package to reuse, and "---" was
// chosen because it cannot occur as a bare line inside valid TOML
// (TOML's own table/array-of-tables headers always begin with "[", never
// "---", and a "---" is not a value bareword any parser would accept
// unquoted). An optional trailing "replace_all" line (its own line,
// after new_string) mirrors toolEditArgs.ReplaceAll, the same trailing-
// literal-word convention parseToolsDeleteArg already uses for "confirm"
// — except here it is a separate line, not a trailing word on the same
// line, since new_string's own last line must be free to end in
// anything (including whitespace) without a same-line suffix silently
// eating part of it.
//
// Anything not matching this shape (no name, no old_string body at all,
// no "---" separator line found) is not recognized as this subcommand —
// the caller falls back to the bare listing, the same "fall through,
// don't error" rule every other parseTools*Arg function here already
// follows for its own unmatched shape.
func parseToolsEditArg(args string) (name, oldString, newString string, replaceAll bool, ok bool) {
	args = strings.TrimSpace(args)
	rest, matched := cutPrefixWord(args, "edit")
	if !matched {
		return "", "", "", false, false
	}
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return "", "", "", false, false
	}

	nameAndBody := strings.SplitN(rest, "\n", 2)
	if len(nameAndBody) < 2 {
		// "edit <name>" alone, with no old_string/new_string body at all,
		// is not this subcommand's shape -- there is nothing to edit.
		return "", "", "", false, false
	}
	name = strings.TrimSpace(nameAndBody[0])
	if name == "" || strings.ContainsAny(name, " \t") {
		// A name with embedded whitespace ("edit foo bar\n...") is not a
		// single tool name -- reject rather than guess which word is the
		// real name.
		return "", "", "", false, false
	}

	lines := strings.Split(nameAndBody[1], "\n")
	sepIdx := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "---" {
			sepIdx = i
			break
		}
	}
	if sepIdx == -1 {
		// No separator line found -- not this subcommand's shape.
		return "", "", "", false, false
	}
	oldString = strings.Join(lines[:sepIdx], "\n")
	if oldString == "" {
		return "", "", "", false, false
	}

	after := lines[sepIdx+1:]
	if len(after) > 0 && strings.TrimSpace(after[len(after)-1]) == "replace_all" {
		replaceAll = true
		after = after[:len(after)-1]
	}
	newString = strings.Join(after, "\n")

	return name, oldString, newString, replaceAll, true
}

// toolsCreateFields is parseToolsCreateArg's own return shape: the subset
// of tools.ToolCreate's full argument set this slash command exposes
// directly (see ToolsLister.CreateTool's own doc comment on why this is
// deliberately reduced, and on what a richer tool still needs /tools
// edit for afterward).
type toolsCreateFields struct {
	name        string
	description string
	url         string
	method      string
	reason      string
	sources     []string
}

// parseToolsCreateArg recognizes /tools create's own multi-line shape:
//
//	create <name>
//	description: <one-sentence description>
//	url: <request url>
//	method: <HTTP method>        (optional, defaults to GET)
//	reason: <mandatory provenance -- why this tool is being created>
//	sources: <comma-separated urls>   (optional, absent means none)
//	--force                      (optional, its own trailing line)
//
// Order-independent key:value lines, not "---"-delimited text blobs like
// /tools edit's own old_string/new_string: every field here is a single
// short value (a name, a url, a one-sentence description), never
// arbitrary multi-line TOML content, so a "---" separator convention
// invented for a very different shape (two long, verbatim text blobs)
// would only add ceremony this shape does not need. "key:" (colon,
// optionally followed by whitespace) is this command's own delimiter,
// chosen because a colon cannot appear in any of these fields' own
// syntax (an http:// url's colon is still unambiguous, since only the
// characters up to first colon are ever treated as the key candidate,
// and "url" is not itself a value containing a colon before its own).
// description/url/reason are mandatory (mirroring tool_create.go's own
// Run validation: name/description/url/reason are all Go errors when
// empty) -- their absence here is caught downstream by
// ToolsLister.CreateTool's own preconditions, not by this parser, since
// a name-only "create weather" with no body at all is not this
// subcommand's shape in the first place (mirrors parseToolsEditArg's own
// "no body at all -> not this shape" rule) and falls through to the bare
// listing.
//
// --force is recognized as its own trailing literal line (not a
// same-line suffix the way parseToolsDeleteArg's "confirm" is): every
// other line here is itself a "key: value" pair whose value may
// legitimately contain trailing whitespace or even end in the literal
// word "force" as part of a real description, so a same-line suffix
// convention would risk swallowing part of a genuine value the way
// parseToolsEditArg's own doc comment already explains for new_string's
// last line. A leading "--" makes this line visually distinct from
// every key:value line and from tool_delete's own bare "confirm" word,
// signalling explicitly that this is a flag, not a field.
//
// Anything not matching this shape (no name, no body at all) is not
// recognized as this subcommand -- the caller falls back to the bare
// listing, the same "fall through, don't error" rule every other
// parseTools*Arg function here already follows for its own unmatched
// shape.
func parseToolsCreateArg(args string) (fields toolsCreateFields, force bool, ok bool) {
	args = strings.TrimSpace(args)
	rest, matched := cutPrefixWord(args, "create")
	if !matched {
		return toolsCreateFields{}, false, false
	}
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return toolsCreateFields{}, false, false
	}

	nameAndBody := strings.SplitN(rest, "\n", 2)
	name := strings.TrimSpace(nameAndBody[0])
	if name == "" || strings.ContainsAny(name, " \t") {
		// A name with embedded whitespace ("create foo bar\n...") is not
		// a single tool name -- reject rather than guess which word is
		// the real name.
		return toolsCreateFields{}, false, false
	}
	if len(nameAndBody) < 2 {
		// "create <name>" alone, with no field lines at all, is not this
		// subcommand's shape -- there is nothing to create from.
		return toolsCreateFields{}, false, false
	}

	fields.name = name
	for _, line := range strings.Split(nameAndBody[1], "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "--force" {
			force = true
			continue
		}
		key, value, hasColon := strings.Cut(trimmed, ":")
		if !hasColon {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "description":
			fields.description = value
		case "url":
			fields.url = value
		case "method":
			fields.method = value
		case "reason":
			fields.reason = value
		case "sources":
			if value == "" {
				fields.sources = []string{}
				continue
			}
			var sources []string
			for _, s := range strings.Split(value, ",") {
				s = strings.TrimSpace(s)
				if s != "" {
					sources = append(sources, s)
				}
			}
			fields.sources = sources
		}
	}

	return fields, force, true
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

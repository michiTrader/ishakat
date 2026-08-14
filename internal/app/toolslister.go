// toolslister.go implements internal/tui.ToolsLister (§13, Step 20's own
// left-over UI half) — the concrete adapter that bridges /tools to
// internal/tools' own DiscoverDeclarative/LoadState, the exact role
// session.go's sessionLister/NewSessionLister already play for /resume's
// own read side. internal/app is the one place that already imports both
// internal/tools and internal/tui, so it is the one place allowed to
// import both at once (§6.1 forbids internal/tui from ever importing
// internal/tools directly — see internal/tui/tools.go's own doc comment).
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MichiTrader/ishakat/internal/tools"
	"github.com/MichiTrader/ishakat/internal/tui"
)

// describeToolState renders a tools.ToolState as the one-sentence status
// line DeleteTool's own refusal and success paths both quote verbatim —
// a deliberate duplicate of tool_delete.go's own unexported describeState
// (same wording, same fields) rather than an exported shared helper: this
// package already independently reproduces internal/tools' own field
// shapes wherever a slash command needs to render them (see
// writeToolManifestWithOrigin's own test-fixture precedent), and
// exporting one unexported helper from internal/tools purely for one
// caller here would widen that package's own surface for no other
// benefit.
func describeToolState(name string, s tools.ToolState) string {
	used := "nunca usada"
	if s.UseCount > 0 {
		used = fmt.Sprintf("usada %d vez/veces, la ultima el %s", s.UseCount, s.LastUsed)
	}
	return fmt.Sprintf("%q esta actualmente %s (%s).", name, s.State, used)
}

// toolsLister is the real, filesystem-backed tui.ToolsLister. It
// deliberately re-runs DiscoverDeclarative/LoadState on every call rather
// than caching a snapshot at construction time — see tui.ToolsLister's own
// doc comment for why (§19.7's tool_create can add a layer-2 tool
// mid-session).
//
// allow/allowAll mirror config.Egress's own two fields ([tools.egress]'s
// allow/allow_all) — EditTool needs them to construct a real
// tools.ToolEdit value (see tui.ToolsLister's own doc comment for why
// EditTool, unlike every other method here, delegates to that type's own
// Run rather than being built from exported internal/tools functions
// directly). Every other method on this type ignores both fields
// entirely, matching the fact that ListTools/AuditTools/ToolManifest/
// ReviveTool/DeleteTool never touch egress at all.
type toolsLister struct {
	dir      string
	allow    []string
	allowAll bool
}

var _ tui.ToolsLister = toolsLister{}

// NewToolsLister returns nil when tools are disabled or no directory is
// configured — [tools].enabled = false or an empty tools.dir — matching
// tui.ToolsLister's own documented nil-is-safe contract, the same rule
// NewSessionLister already follows for its own concern.
//
// This constructor's own signature is deliberately left unchanged (no
// allow/allowAll parameters) even though EditTool needs them: it has one
// production call site and dozens of existing test call sites that all
// predate EditTool, and growing this specific signature would be a
// breaking change to every one of them for a value most callers (every
// method except EditTool) never use. NewToolsListerWithEgress is the
// additive alternative — see its own doc comment.
func NewToolsLister(dir string, enabled bool) tui.ToolsLister {
	return NewToolsListerWithEgress(dir, enabled, nil, false)
}

// NewToolsListerWithEgress is NewToolsLister plus the egress allowlist
// EditTool needs to construct its own tools.ToolEdit value (allow/allowAll
// mirror config.Egress's own allow/allow_all fields exactly — the real
// production call site passes cfg.Tools.Egress.Allow/AllowAll straight
// through). NewToolsLister itself is kept as a thin wrapper over this
// function (allow=nil, allowAll=false) rather than replaced by it, so
// every pre-existing call site — the one production call and the dozens
// of test call sites that only ever exercise ListTools/AuditTools/
// ToolManifest/ReviveTool/DeleteTool — keeps compiling unchanged.
func NewToolsListerWithEgress(dir string, enabled bool, allow []string, allowAll bool) tui.ToolsLister {
	if !enabled || dir == "" {
		return nil
	}
	return toolsLister{dir: dir, allow: allow, allowAll: allowAll}
}

// ListTools mirrors tool_list.go's own two calls (DiscoverDeclarative then
// LoadState per tool) but renders tui.ToolSummary rows instead of a single
// preformatted text blob — the LLM-facing meta-tool and this UI-facing
// adapter independently call the same two lower-level functions rather
// than sharing code, since the two shapes (blob vs. struct slice) do not
// otherwise overlap.
func (l toolsLister) ListTools() tui.ToolsListResult {
	disc := tools.DiscoverDeclarative(l.dir)
	res := tui.ToolsListResult{Warn: disc.Warn}
	for _, m := range disc.Tools {
		summary := tui.ToolSummary{
			Name:        m.Name,
			Description: m.Description,
			Danger:      tools.DeclarativeTool{Manifest: m}.Danger().String(),
		}
		state, err := tools.LoadState(m.Dir)
		if err != nil {
			// LoadState only errors on a corrupt/unreadable state.json —
			// a missing one is not an error (zero-value StateUnverified).
			// Surface it on the row rather than dropping the tool from
			// the listing entirely.
			summary.State = "unknown"
			summary.LastError = err.Error()
			res.Tools = append(res.Tools, summary)
			continue
		}
		summary.State = string(state.State)
		summary.UseCount = state.UseCount
		summary.LastUsed = state.LastUsed
		summary.LastError = state.LastError
		res.Tools = append(res.Tools, summary)
	}
	return res
}

// AuditTools mirrors tool_probe.go's own ComputeHash/DetectTamper call
// pair (§19.8 mitigations 2 and 6) but, unlike a probe, never mutates
// state.json — this is a read-only report, not a re-verification: a
// currently-unverified tool's tamper flag is still worth showing (it
// answers "did the content change since it was last touched", which is
// meaningful even for a tool that has never passed a probe), so this
// does not skip tools by state the way CanUse's own gate would.
func (l toolsLister) AuditTools() tui.ToolsAuditResult {
	disc := tools.DiscoverDeclarative(l.dir)
	res := tui.ToolsAuditResult{Warn: disc.Warn}
	for _, m := range disc.Tools {
		entry := tui.ToolAuditEntry{
			Name:        m.Name,
			CreatedBy:   m.Origin.CreatedBy,
			Reason:      m.Origin.Reason,
			Repetitions: m.Origin.Repetitions,
			SessionID:   m.Origin.SessionID,
			Sources:     m.Origin.Sources,
		}

		hash, err := tools.ComputeHash(m.Dir, tools.ManifestFileName)
		if err != nil {
			// The manifest existed a moment ago (DiscoverDeclarative just
			// read it) but could not be hashed now — report it on the row
			// rather than dropping the tool from the audit entirely,
			// matching ListTools' own "surface, don't hide" leniency for
			// a LoadState failure.
			entry.HashError = err.Error()
			res.Tools = append(res.Tools, entry)
			continue
		}
		entry.Hash = hash

		state, err := tools.LoadState(m.Dir)
		if err != nil {
			// A corrupt state.json means "tamper status unknown", not
			// "assume tampered" — the hash itself is still valid and
			// shown; only the comparison against a last-probed hash is
			// unavailable.
			entry.HashError = err.Error()
			res.Tools = append(res.Tools, entry)
			continue
		}
		_, tampered := tools.DetectTamper(state, hash)
		entry.Tampered = tampered
		res.Tools = append(res.Tools, entry)
	}
	return res
}

// ToolManifest returns one tool's manifest file verbatim, or an error if
// no declarative tool by that name exists under l.dir.
func (l toolsLister) ToolManifest(name string) (string, error) {
	disc := tools.DiscoverDeclarative(l.dir)
	for _, m := range disc.Tools {
		if m.Name == name {
			body, err := os.ReadFile(filepath.Join(m.Dir, tools.ManifestFileName))
			if err != nil {
				return "", fmt.Errorf("no se pudo leer el manifiesto de %q: %w", name, err)
			}
			return string(body), nil
		}
	}
	return "", fmt.Errorf("no existe ninguna herramienta llamada %q", name)
}

// ReviveTool implements /tools revive <name> (§13, §19.5) by calling the
// exact same LoadState -> ToolState.Revive -> SaveState sequence
// tools.ToolRevive.Run already runs for the model-initiated tool_revive
// meta-tool — this is a second, human-initiated caller of the same pure
// state transition, not a reimplementation of it. Unlike ToolRevive.Run
// (which reports a no-op as a successful Result so the model sees it as
// data, not a failure), a slash command's caller distinguishes "could not
// even find the tool" from "found it, nothing to do" via the (string,
// error) split ToolManifest already established for this interface: an
// unknown name is an error here, a no-op is a normal string.
func (l toolsLister) ReviveTool(name string) (string, error) {
	disc := tools.DiscoverDeclarative(l.dir)
	var found bool
	var dir string
	for _, m := range disc.Tools {
		if m.Name == name {
			found = true
			dir = m.Dir
			break
		}
	}
	if !found {
		return "", fmt.Errorf("no existe ninguna herramienta llamada %q", name)
	}

	state, err := tools.LoadState(dir)
	if err != nil {
		return "", fmt.Errorf("no se pudo leer el estado de %q: %w", name, err)
	}
	if state.State != tools.StateArchived {
		return fmt.Sprintf("%q no esta archivada (estado actual: %s); no se cambio nada.", name, state.State), nil
	}

	next := state.Revive()
	if err := tools.SaveState(dir, next); err != nil {
		return "", fmt.Errorf("no se pudo guardar el estado de %q: %w", name, err)
	}
	return fmt.Sprintf("%q revivida; su estado ahora es %s.", name, next.State), nil
}

// DeleteTool implements /tools delete <name> [confirm] (§13, §19.5's own
// "removes it, with confirmation"), a second, human-initiated caller of
// tool_delete.go's own two-path contract: without confirm, nothing on
// disk is touched and the returned string reports the tool's current
// state, use_count and last_used (describeToolState, tool_delete.go's
// own describeState reproduced here for the reason that function's own
// doc comment gives) so the decision to confirm is made with that
// information in hand; with confirm=true, the tool's entire directory is
// removed via os.RemoveAll. Only an unknown name or a failed removal are
// a Go error — refusing without confirmation is an attempted, reported
// outcome, matching tool_delete.go's own ErrorResult (not a Go error) for
// the identical case.
func (l toolsLister) DeleteTool(name string, confirm bool) (string, error) {
	disc := tools.DiscoverDeclarative(l.dir)
	var found bool
	var dir string
	for _, m := range disc.Tools {
		if m.Name == name {
			found = true
			dir = m.Dir
			break
		}
	}
	if !found {
		return "", fmt.Errorf("no existe ninguna herramienta llamada %q", name)
	}

	state, err := tools.LoadState(dir)
	if err != nil {
		return "", fmt.Errorf("no se pudo leer el estado de %q: %w", name, err)
	}
	statusLine := describeToolState(name, state)

	if !confirm {
		return fmt.Sprintf(
			"se rehuso a borrar %q sin confirmacion. %s Repeti el comando como \"/tools delete %s confirm\" para borrarla de forma permanente -- esto no se puede revertir con tool_edit.",
			name, statusLine, name), nil
	}

	if err := os.RemoveAll(dir); err != nil {
		return "", fmt.Errorf("no se pudo borrar %q: %w", dir, err)
	}
	return fmt.Sprintf("%q borrada de forma permanente. %s", name, statusLine), nil
}

// EditTool implements /tools edit <name> (§13, §19.5's fourth meta-tool)
// by constructing a real tools.ToolEdit and calling its Run method
// directly through the generic tools.Tool interface — see
// tui.ToolsLister's own doc comment for why this is the one method on
// this type that delegates to a tools.Tool's Run rather than being built
// from exported internal/tools functions directly (tools.ToolEdit.Run's
// own flow depends on two unexported helpers, parseManifest and
// checkManifestSafety, that enforce §19.8's egress and structural-
// exfiltration checks on the edited result).
//
// context.Background() is used here, not a context threaded in from the
// caller: a human-typed slash command has no existing per-turn context
// the way an agent-turn tool call does (runToolsCommand's own call chain
// carries no context.Context today, matching ReviveTool/DeleteTool's own
// context-free signatures on this interface), and tools.ToolEdit.Run's
// own filesystem work (one read, one string replace, one write) is fast
// enough that an uncancellable context poses no real risk here — the
// same reasoning that already applies to ReviveTool/DeleteTool never
// taking one either.
//
// Only "could not even attempt it" outcomes are a Go error here,
// matching this interface's own documented convention: a JSON marshal
// failure of the arguments themselves (never expected in practice, since
// every field is caller-supplied Go data, not attacker-controlled bytes)
// or a Go error from Run itself (bad arguments per tool_edit.go's own
// validation — empty name, empty old_string, old_string == new_string —
// all already rejected by this method's own preconditions below, or a
// cancelled context, which context.Background() never is). Every other
// outcome tool_edit.go's own Run documents as an ErrorResult (unknown
// name, old_string not found, ambiguous match without replace_all, a
// result that no longer parses, a result that fails the safety check) is
// surfaced here as res.Text, not a Go error — the caller sees exactly the
// same wording a model would see from tool_edit itself.
func (l toolsLister) EditTool(name, oldString, newString string, replaceAll bool) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("el nombre de la herramienta no puede estar vacio")
	}
	if oldString == "" {
		return "", fmt.Errorf("old_string no puede estar vacio (usa tool_create para hacer una herramienta nueva)")
	}
	if oldString == newString {
		return "", fmt.Errorf("old_string y new_string son identicos, no hay nada que hacer")
	}

	te := tools.ToolEdit{Dir: l.dir, Allow: l.allow, AllowAll: l.allowAll}
	rawArgs, err := json.Marshal(map[string]any{
		"name":        name,
		"old_string":  oldString,
		"new_string":  newString,
		"replace_all": replaceAll,
	})
	if err != nil {
		return "", fmt.Errorf("no se pudieron preparar los argumentos de edicion: %w", err)
	}

	res, err := te.Run(context.Background(), rawArgs)
	if err != nil {
		return "", fmt.Errorf("no se pudo editar %q: %w", name, err)
	}
	return res.Text, nil
}

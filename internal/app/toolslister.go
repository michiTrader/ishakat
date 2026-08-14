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
	"fmt"
	"os"
	"path/filepath"

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
type toolsLister struct {
	dir string
}

var _ tui.ToolsLister = toolsLister{}

// NewToolsLister returns nil when tools are disabled or no directory is
// configured — [tools].enabled = false or an empty tools.dir — matching
// tui.ToolsLister's own documented nil-is-safe contract, the same rule
// NewSessionLister already follows for its own concern.
func NewToolsLister(dir string, enabled bool) tui.ToolsLister {
	if !enabled || dir == "" {
		return nil
	}
	return toolsLister{dir: dir}
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

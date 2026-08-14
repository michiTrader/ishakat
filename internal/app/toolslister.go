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

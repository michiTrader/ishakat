// tool_list.go implements the first of §19.5's meta-tools table: "what
// exists, with state and usage stats". It is deliberately the smallest and
// lowest-risk of the five (tool_list/tool_create/tool_probe/tool_edit/
// tool_delete) — read-only, no gate of any kind needed (§19.6's three gates
// all exist to guard something being *written* or *executed*; listing what
// is already on disk changes nothing) — so it lands first, on its own, as
// Step 21's own opening slice for the meta-tools work.
//
// This only reports on layer 2 (declarative/script) tools: layer 1's seven
// native tools have no ToolState of their own (lifecycle.go's whole state
// machine exists for "a tool that was written after the binary shipped", a
// category native tools do not belong to) and are already fully visible in
// the system prompt at all times, so a listing of them here would be
// redundant. A future rung-2 script-tool executor lands under the same
// Dir DiscoverDeclarative already walks (§20.11 item 5's "one tool, one
// directory" contract), so this tool needs no changes to pick those up too
// once they exist.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ToolList is the tool_list meta-tool. Dir is the same layer-2 tools
// directory DeclarativeTools/DiscoverDeclarative already take — this
// package's own "minimal, purpose-built argument" pattern (see
// registry.go's doc comment on Core's egressAllow/egressAllowAll) rather
// than accepting a config.Tools value, so internal/tools still never
// imports internal/config.
type ToolList struct {
	Dir string
}

var _ Tool = ToolList{}

func (ToolList) Name() string   { return "tool_list" }
func (ToolList) Danger() Danger { return DangerLow }
func (ToolList) Description() string {
	return "List every layer-2 tool (declarative or script) that has been created, with its lifecycle state, danger tier, and usage stats. Takes no arguments."
}

// Parameters is an empty object schema: tool_list needs no arguments at
// all, but still returns a well-formed JSON-schema object (matching every
// other tool's Parameters() contract) rather than a nil/omitted schema a
// caller would have to special-case.
func (ToolList) Parameters() json.RawMessage {
	return objectSchema(map[string]prop{})
}

// Run never returns a Go error for a normal listing outcome — an empty or
// missing Dir, or a directory with zero tools in it, is not a failure, it
// is simply "nothing to list yet" (mirroring DiscoverDeclarative's own
// "missing directory is not an error" contract). rawArgs is intentionally
// never unmarshalled: there is nothing in it to read, and a model calling
// tool_list with "{}" (the schema's own minimal valid instance) must not
// be rejected for it.
func (t ToolList) Run(ctx context.Context, rawArgs json.RawMessage) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	res := DiscoverDeclarative(t.Dir)
	if len(res.Tools) == 0 {
		text := "no layer-2 tools have been created yet"
		if res.Warn != "" {
			text += "\nwarning: " + res.Warn
		}
		return OKResult(text), nil
	}

	// DiscoverDeclarative already sorts res.Tools by Name; re-sorting here
	// would be redundant, but the sort is cheap enough and this function's
	// own contract should not silently depend on that upstream detail
	// staying true forever.
	sort.Slice(res.Tools, func(i, j int) bool { return res.Tools[i].Name < res.Tools[j].Name })

	var sb strings.Builder
	for i, m := range res.Tools {
		state, err := LoadState(m.Dir)
		if err != nil {
			// A tool directory whose state.json exists but fails to parse
			// is reported inline rather than aborting the whole listing —
			// one damaged sidecar file must not hide every other tool's
			// entry, the same leniency DiscoverDeclarative itself applies
			// to a single broken tool.toml.
			fmt.Fprintf(&sb, "%s: description=%q danger=%s state=<could not read state: %v>\n",
				m.Name, m.Description, inferDanger(m), err)
			continue
		}
		danger := inferDanger(m)
		lastUsed := state.LastUsed
		if lastUsed == "" {
			lastUsed = "never"
		}
		fmt.Fprintf(&sb, "%s: description=%q danger=%s state=%s use_count=%d last_used=%s",
			m.Name, m.Description, danger, state.State, state.UseCount, lastUsed)
		if state.State == StateBroken && state.LastError != "" {
			fmt.Fprintf(&sb, " last_error=%q", state.LastError)
		}
		if i < len(res.Tools)-1 {
			sb.WriteByte('\n')
		}
	}
	if res.Warn != "" {
		fmt.Fprintf(&sb, "\nwarning: %s", res.Warn)
	}
	return OKResult(sb.String()), nil
}

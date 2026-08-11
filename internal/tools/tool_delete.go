// tool_delete.go implements §19.5's fifth and final meta-tool: "removes it,
// with confirmation." Unlike tool_create/tool_probe/tool_edit, there is no
// lifecycle.go state transition this drives — deletion is not a node on
// §19.5's own diagram, it is leaving the diagram altogether, so this file's
// only jobs are (1) never removing anything on the first call, and (2)
// never being silent about what it removed or why it refused to.
//
// §20.9's proposed (not-yet-implemented, ⬜ Phase 6) `ishakat uninstall`
// command describes its own removal behaviour as "refuses silently-in-use,
// same as tool_delete" — read literally, that is two separate promises this
// file keeps: it refuses to act without an explicit, unambiguous signal
// (Confirm, a required boolean, not something inferable from a free-text
// argument a model could phrase past accidentally), and it never resolves
// either the refusal or the deletion itself with a bare "ok"/"denied" —
// both paths report the tool's real lifecycle state, use_count and
// last_used, so a model (and the human reading its transcript) can see
// exactly what was — or would have been — thrown away.
//
// A harder rule — refusing outright to delete a tool that is *currently*
// "in use" (§19.5's diagram places "in use" as its own stage between
// verified and promoted, not one of lifecycle.go's four LifecycleState
// constants) — is deliberately NOT enforced as an unconditional block here.
// lifecycle.go already has Archive/Revive, the strictly less destructive
// alternative §19.5's own diagram names for a tool no longer wanted day to
// day ("unused N days -> archived ... out of the prompt, still on disk,
// revivable"), but no meta-tool exposes either method yet — blocking
// deletion of a verified, actively-used tool with no way to archive it
// first would leave a model with no path to ever remove one, which is a
// worse outcome than the one being guarded against. Confirm is this file's
// one and only gate until a future tool_archive/tool_revive slice lands;
// the pre-deletion report below still names use_count/last_used plainly
// so the decision to confirm is an informed one even without a hard block.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// toolDeleteArgs is tool_delete's argument shape. Confirm has no
// `omitempty`/default-true reading: a caller that does not think to set it
// gets the safe outcome (refused, nothing removed), never the destructive
// one — the same "no safe reading, only a coin flip" logic
// config/schema.go's own doc comments already apply to a fatal
// misconfiguration applies here to a fatal *action*: guessing wrong on a
// deletion cannot be undone by tool_edit the way a bad manifest field can.
type toolDeleteArgs struct {
	Name    string `json:"name"`
	Confirm bool   `json:"confirm"`
}

// ToolDelete is the tool_delete meta-tool. Dir is the same layer-2 tools
// directory ToolList/ToolProbe/ToolCreate/ToolEdit all take (this
// package's own "minimal, purpose-built argument" pattern).
type ToolDelete struct {
	Dir string
}

var _ Tool = ToolDelete{}

func (ToolDelete) Name() string { return "tool_delete" }

// Danger is DangerHigh, unconditionally: removing a tool's directory is an
// irreversible change to disk (no lifecycle.go transition, no tool_edit
// path, restores it) with no undo — the same risk class bash.go's own
// DangerHigh docstring names ("irreversible changes"), applied here to a
// capability instead of an arbitrary shell command.
func (ToolDelete) Danger() Danger { return DangerHigh }

func (ToolDelete) Description() string {
	return "Permanently delete a layer-2 tool's entire directory (its tool.toml, state.json, and any script sidecar). Requires confirm=true; a call without it is refused and reports the tool's current state, use_count and last_used instead of deleting anything, so the decision to confirm is made with that information in hand. This cannot be undone by tool_edit -- recreate the tool with tool_create if it turns out to still be needed."
}

func (ToolDelete) Parameters() json.RawMessage {
	return objectSchema(map[string]prop{
		"name": {
			Type:        "string",
			Description: "The tool's name, exactly as it appears in tool_list's output or its tool.toml's [name].",
		},
		"confirm": {
			Type:        "boolean",
			Description: "Must be explicitly true to actually delete. A call with confirm omitted or false is always refused and reports the tool's state instead, without deleting anything.",
		},
	}, "name", "confirm")
}

// Run finds name's tool directory under t.Dir and, only when args.Confirm
// is true, removes it in full via os.RemoveAll. Every other path — missing
// name, missing directory, Confirm left false — is reported without
// touching disk.
//
// A Go error means the call could not even be attempted (bad arguments
// JSON, an empty name, a cancelled context). An unknown tool name or
// Confirm being false are both ErrorResult — the model asked for something
// that did not happen and can see exactly why, matching every other
// "attempted and refused" outcome elsewhere in this package. A confirmed
// deletion that fails at the OS level (permissions, a concurrent removal)
// is a Go error: unlike a refusal, this was an operation that should have
// succeeded and did not for a reason no argument change can fix.
func (t ToolDelete) Run(ctx context.Context, rawArgs json.RawMessage) (Result, error) {
	var args toolDeleteArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return Result{}, fmt.Errorf("tool_delete: invalid arguments: %w", err)
	}
	if strings.TrimSpace(args.Name) == "" {
		return Result{}, fmt.Errorf("tool_delete: name is required")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(t.Dir) == "" {
		return ErrorResult("tool_delete: no tools directory is configured"), nil
	}

	toolDir := filepath.Join(t.Dir, args.Name)
	manifestPath := filepath.Join(toolDir, ManifestFileName)
	if _, err := os.Stat(manifestPath); err != nil {
		if os.IsNotExist(err) {
			return ErrorResult(fmt.Sprintf("no tool named %q found under %s", args.Name, t.Dir)), nil
		}
		return Result{}, fmt.Errorf("tool_delete: could not check %s: %w", manifestPath, err)
	}

	state, err := LoadState(toolDir)
	if err != nil {
		return Result{}, fmt.Errorf("tool_delete: could not load state for %q: %w", args.Name, err)
	}
	statusLine := describeState(args.Name, state)

	if !args.Confirm {
		return ErrorResult(fmt.Sprintf(
			"refused to delete %q without confirmation. %s Call again with confirm=true to permanently delete it -- this cannot be undone by tool_edit.",
			args.Name, statusLine)), nil
	}

	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if err := os.RemoveAll(toolDir); err != nil {
		return Result{}, fmt.Errorf("tool_delete: could not remove %s: %w", toolDir, err)
	}

	return OKResult(fmt.Sprintf("deleted %q permanently. %s", args.Name, statusLine)), nil
}

// describeState renders a ToolState as the one-sentence status line both
// tool_delete's refusal and its success message quote verbatim — the same
// wording either way, so a model that reads the refusal and then confirms
// sees its own earlier warning echoed back as confirmation of what it just
// agreed to remove, rather than two differently-worded reports of the same
// fact.
func describeState(name string, s ToolState) string {
	used := "never used"
	if s.UseCount > 0 {
		used = fmt.Sprintf("used %d time(s), last on %s", s.UseCount, s.LastUsed)
	}
	return fmt.Sprintf("%q is currently %s (%s).", name, s.State, used)
}

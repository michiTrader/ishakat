// tool_archive.go implements the two meta-tools §19.5's own entropy-control
// text names but the original five-meta-tool table (tool_list/create/probe/
// edit/delete) never exposed a caller for: "Archive on disuse. Unused for 90
// days -> out of the system prompt (stops costing tokens), not deleted.
// `/tools revive <name>` brings it back." lifecycle.go's own Archive/Revive
// methods have implemented and tested that pair of pure state transitions
// since Step 21's first PR (#101); nothing outside internal/tools/
// lifecycle_test.go itself has ever called either one. This file is their
// first real caller, the same relationship tool_probe.go has to Probe and
// tool_edit.go has to Edit.
//
// Both tools live in one file, unlike every other meta-tool having its own,
// because they are one operation's two directions -- Archive and Revive are
// declared next to each other in lifecycle.go for the identical reason, and
// splitting their callers across two files would make the pair harder to
// read together, not easier.
//
// Danger is DangerLow for both, not DangerHigh: neither touches a tool's
// tool.toml or state.json's Hash/UseCount/LastUsed/FailCount/LastError
// fields, and neither runs a request or writes executable content -- the
// only thing either one changes is which LifecycleState a tool sits in,
// exactly the same "acting on what is already there changes nothing new"
// reasoning tool_list.go's own doc comment gives for its own DangerLow,
// extended here to a state flip instead of a read. This also matches
// lifecycle.go's own framing of archiving as reversible-by-construction
// ("out of the prompt, still on disk, revivable") -- the opposite of
// tool_delete's unconditional DangerHigh, which is irreversible by design.
//
// Archiving (unlike deletion) is deliberately NOT gated on any usage
// threshold or confirmation: a human or the agent may always choose to
// quiet a tool down early, and a wrong call costs nothing a second
// tool_revive call cannot undo. This is the missing half tool_delete.go's
// own doc comment names as the reason it does not hard-block deleting an
// actively-used tool -- with ToolArchive/ToolRevive now landed, a future
// change could make that block real, but this file itself does not attempt
// that; it only supplies the two missing state transitions.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// toolArchiveRevive is the shared argument shape for both tool_archive and
// tool_revive -- each takes exactly one thing, the tool's name, the same
// "necessarily about one specific tool" reasoning toolProbeArgs' own doc
// comment gives for tool_probe.
type toolArchiveRevive struct {
	Name string `json:"name"`
}

// ToolArchive is the tool_archive meta-tool. Dir is the same layer-2 tools
// directory every other meta-tool in this package takes (the "minimal,
// purpose-built argument" pattern registry.go's own doc comment states).
type ToolArchive struct {
	Dir string
}

var _ Tool = ToolArchive{}

func (ToolArchive) Name() string   { return "tool_archive" }
func (ToolArchive) Danger() Danger { return DangerLow }
func (ToolArchive) Description() string {
	return "Move a layer-2 tool out of the system prompt without deleting it, remembering its current lifecycle state so tool_revive can restore it later. Use this for a tool that is not currently needed but might be again -- the on-disk tool.toml and state.json are untouched. Archiving an already-archived tool is a no-op."
}

func (ToolArchive) Parameters() json.RawMessage {
	return objectSchema(map[string]prop{
		"name": {
			Type:        "string",
			Description: "The tool's name, exactly as it appears in tool_list's output or its tool.toml's [name].",
		},
	}, "name")
}

// Run finds name's tool directory under t.Dir and moves its recorded state
// to StateArchived via ToolState.Archive, which itself remembers
// PreviousState for tool_revive to restore later. Archiving a tool that is
// already archived is reported as a no-op, not an error -- Archive's own
// idempotent contract, surfaced honestly rather than papered over with a
// generic success message that would suggest something changed when it did
// not.
//
// A Go error means the call could not even be attempted (bad arguments
// JSON, an empty name, a cancelled context). An unknown tool name is
// ErrorResult, matching every sibling meta-tool's identical "the model
// named something that is not there" outcome.
func (t ToolArchive) Run(ctx context.Context, rawArgs json.RawMessage) (Result, error) {
	var args toolArchiveRevive
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return Result{}, fmt.Errorf("tool_archive: invalid arguments: %w", err)
	}
	if strings.TrimSpace(args.Name) == "" {
		return Result{}, fmt.Errorf("tool_archive: name is required")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(t.Dir) == "" {
		return ErrorResult("tool_archive: no tools directory is configured"), nil
	}

	toolDir := filepath.Join(t.Dir, args.Name)
	manifestPath := filepath.Join(toolDir, ManifestFileName)
	if _, err := os.Stat(manifestPath); err != nil {
		if os.IsNotExist(err) {
			return ErrorResult(fmt.Sprintf("no tool named %q found under %s", args.Name, t.Dir)), nil
		}
		return Result{}, fmt.Errorf("tool_archive: could not check %s: %w", manifestPath, err)
	}

	state, err := LoadState(toolDir)
	if err != nil {
		return Result{}, fmt.Errorf("tool_archive: could not load state for %q: %w", args.Name, err)
	}

	if state.State == StateArchived {
		return OKResult(fmt.Sprintf("%q is already archived; nothing changed.", args.Name)), nil
	}

	wasState := state.State
	next := state.Archive()
	if err := SaveState(toolDir, next); err != nil {
		return Result{}, fmt.Errorf("tool_archive: could not save state for %q: %w", args.Name, err)
	}

	return OKResult(fmt.Sprintf("%q archived (was %s). It is out of the system prompt but still on disk -- call tool_revive to bring it back.", args.Name, wasState)), nil
}

// ToolRevive is the tool_revive meta-tool, Archive's exact inverse. Dir is
// the same layer-2 tools directory ToolArchive (and every other meta-tool)
// takes.
type ToolRevive struct {
	Dir string
}

var _ Tool = ToolRevive{}

func (ToolRevive) Name() string   { return "tool_revive" }
func (ToolRevive) Danger() Danger { return DangerLow }
func (ToolRevive) Description() string {
	return "Restore a tool_archive'd tool back to the system prompt, in whatever lifecycle state it was in before it was archived (typically verified). Reviving a tool that is not currently archived is a no-op."
}

func (ToolRevive) Parameters() json.RawMessage {
	return objectSchema(map[string]prop{
		"name": {
			Type:        "string",
			Description: "The tool's name, exactly as it appears in tool_list's output or its tool.toml's [name].",
		},
	}, "name")
}

// Run finds name's tool directory under t.Dir and restores its recorded
// state from StateArchived back to PreviousState via ToolState.Revive.
// Reviving a tool that is not currently archived is reported as a no-op,
// matching Revive's own idempotent contract and ToolArchive.Run's identical
// treatment of the symmetric case.
//
// A Go error means the call could not even be attempted (bad arguments
// JSON, an empty name, a cancelled context). An unknown tool name is
// ErrorResult, matching every sibling meta-tool.
func (t ToolRevive) Run(ctx context.Context, rawArgs json.RawMessage) (Result, error) {
	var args toolArchiveRevive
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return Result{}, fmt.Errorf("tool_revive: invalid arguments: %w", err)
	}
	if strings.TrimSpace(args.Name) == "" {
		return Result{}, fmt.Errorf("tool_revive: name is required")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(t.Dir) == "" {
		return ErrorResult("tool_revive: no tools directory is configured"), nil
	}

	toolDir := filepath.Join(t.Dir, args.Name)
	manifestPath := filepath.Join(toolDir, ManifestFileName)
	if _, err := os.Stat(manifestPath); err != nil {
		if os.IsNotExist(err) {
			return ErrorResult(fmt.Sprintf("no tool named %q found under %s", args.Name, t.Dir)), nil
		}
		return Result{}, fmt.Errorf("tool_revive: could not check %s: %w", manifestPath, err)
	}

	state, err := LoadState(toolDir)
	if err != nil {
		return Result{}, fmt.Errorf("tool_revive: could not load state for %q: %w", args.Name, err)
	}

	if state.State != StateArchived {
		return OKResult(fmt.Sprintf("%q is not archived (currently %s); nothing changed.", args.Name, state.State)), nil
	}

	next := state.Revive()
	if err := SaveState(toolDir, next); err != nil {
		return Result{}, fmt.Errorf("tool_revive: could not save state for %q: %w", args.Name, err)
	}

	return OKResult(fmt.Sprintf("%q revived; state is now %s.", args.Name, next.State)), nil
}

// tool_edit.go implements §19.5's fourth meta-tool: "fixes a tool; demotes
// it to unverified until re-probed." lifecycle.go's own Edit method already
// encodes that demotion (state -> StateUnverified, FailCount/LastError
// cleared); this file is its first real caller, the same relationship
// tool_probe.go has to Probe.
//
// The edit itself reuses edit_file's own exact-string old_string/new_string
// contract (edit_file.go), applied to the manifest's raw tool.toml text
// rather than to a full re-specification of every field the way
// tool_create's argument shape works. Three reasons, not just one:
//
//   - A model that has just called tool_list/tool_probe and seen a
//     LastError already knows the *shape* of the fix (one wrong header
//     name, one wrong query key) far better than it knows every other
//     field's current value -- re-typing the whole manifest to change one
//     line risks silently reverting an unrelated field the model does not
//     remember correctly.
//   - It is the same tool-use convention this project's own Edit tool (and
//     edit_file, its native-tool mirror) already established, so a model
//     that has learned "exact match, unique or replace_all" once reuses
//     that knowledge here for free rather than learning a second,
//     structurally different update mechanism for the one file type that
//     happens to be TOML instead of arbitrary text.
//   - It keeps this file from needing its own copy of every toolCreateArgs
//     field just to let a caller "not change" the fields it is not editing
//     -- the alternative (all-fields-optional, "nil/omitted means
//     unchanged") both duplicates tool_create.go's whole argument surface
//     and reintroduces the sentinel-value ambiguity (see toolCreateArgs.
//     Sources's own doc comment) for every single field, not just one.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// toolEditArgs mirrors editFileArgs field for field -- see this file's own
// doc comment for why an exact-string replacement, not a re-specification,
// is the right shape for editing an existing manifest.
type toolEditArgs struct {
	Name       string `json:"name"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
}

// ToolEdit is the tool_edit meta-tool. Dir/Allow/AllowAll are the same
// layer-2 tools directory and egress allowlist ToolCreate/ToolProbe/
// DeclarativeTools already take -- an edit can introduce a new host or a
// credential-shaped path exactly as easily as a creation can (§19.8
// mitigations 4 and 5 apply to any manifest content reaching disk, not
// only to the moment it is first written), so both are checked here too,
// via the same checkManifestSafety helper tool_create.go uses.
type ToolEdit struct {
	Dir      string
	Allow    []string
	AllowAll bool
}

var _ Tool = ToolEdit{}

func (ToolEdit) Name() string { return "tool_edit" }

// Danger is DangerHigh, unconditionally, for the same reason tool_create's
// own Danger() is: this file's write path can change what request an
// already-installed tool makes -- a new host, a new auth scheme, a
// different body -- which is exactly the same "acquire a new capability
// on disk" risk class §19.8 mitigation 1 names for creation, just applied
// to a tool that already existed a moment ago instead of one that did
// not. checkManifestSafety (egress allowlist, structural exfiltration) is
// re-run on every edit's result for the identical reason.
func (ToolEdit) Danger() Danger { return DangerHigh }
func (ToolEdit) Description() string {
	return "Fix an existing layer-2 tool by replacing an exact, verbatim occurrence of old_string with new_string in its tool.toml (same convention as edit_file). The result must still be a valid manifest and pass the same egress/exfiltration checks tool_create applies. The tool is demoted to unverified until tool_probe passes again."
}

func (ToolEdit) Parameters() json.RawMessage {
	return objectSchema(map[string]prop{
		"name": {
			Type:        "string",
			Description: "The tool's name, exactly as it appears in tool_list's output or its tool.toml's [name].",
		},
		"old_string": {
			Type:        "string",
			Description: "The exact text to find and replace in the tool's tool.toml, including whitespace, exactly as it appears on disk.",
		},
		"new_string": {
			Type:        "string",
			Description: "The text to replace old_string with.",
		},
		"replace_all": {
			Type:        "boolean",
			Description: "Replace every occurrence of old_string instead of requiring exactly one. Defaults to false.",
		},
	}, "name", "old_string", "new_string")
}

// Run reads name's tool.toml, applies the exact-string replacement (same
// unique-match-or-replace_all rule edit_file.go's own doc comment
// describes), re-parses the result, re-runs both §19.8 structural checks
// (checkManifestSafety), and only then writes it back and demotes the
// tool's lifecycle state via ToolState.Edit -- in that order, so a bad
// edit never reaches disk and never demotes a tool that was, until this
// call, verified and working.
//
// A Go error means the edit could not even be attempted (bad arguments
// JSON, a missing field, old_string == new_string). An unknown tool name,
// an old_string not found (or ambiguous without replace_all), a result
// that no longer parses as a valid manifest, or a result that fails
// checkManifestSafety are all ErrorResult -- the model attempted a fix
// and it did not take, which it can see and try again, matching every
// other "attempted and failed" outcome elsewhere in this package.
func (t ToolEdit) Run(ctx context.Context, rawArgs json.RawMessage) (Result, error) {
	var args toolEditArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return Result{}, fmt.Errorf("tool_edit: invalid arguments: %w", err)
	}
	if strings.TrimSpace(args.Name) == "" {
		return Result{}, fmt.Errorf("tool_edit: name is required")
	}
	if args.OldString == "" {
		return Result{}, fmt.Errorf("tool_edit: old_string is required (use tool_create to make a new tool)")
	}
	if args.OldString == args.NewString {
		return Result{}, fmt.Errorf("tool_edit: old_string and new_string are identical, nothing to do")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(t.Dir) == "" {
		return ErrorResult("tool_edit: no tools directory is configured"), nil
	}

	toolDir := filepath.Join(t.Dir, args.Name)
	manifestPath := filepath.Join(toolDir, ManifestFileName)
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrorResult(fmt.Sprintf("no tool named %q found under %s", args.Name, t.Dir)), nil
		}
		return Result{}, fmt.Errorf("tool_edit: could not read %s: %w", manifestPath, err)
	}
	content := string(raw)

	count := strings.Count(content, args.OldString)
	switch {
	case count == 0:
		return ErrorResult(fmt.Sprintf(
			"old_string was not found in %q's tool.toml -- it must match the file's content exactly, including whitespace", args.Name)), nil
	case count > 1 && !args.ReplaceAll:
		return ErrorResult(fmt.Sprintf(
			"old_string matches %d locations in %q's tool.toml, not 1 -- make it more specific (add surrounding context) or set replace_all", count, args.Name)), nil
	}

	var updated string
	if args.ReplaceAll {
		updated = strings.ReplaceAll(content, args.OldString, args.NewString)
	} else {
		updated = strings.Replace(content, args.OldString, args.NewString, 1)
	}

	m, err := parseManifest([]byte(updated))
	if err != nil {
		return ErrorResult(fmt.Sprintf("the edited tool.toml for %q no longer parses: %v -- nothing was written", args.Name, err)), nil
	}
	if m.Name == "" {
		m.Name = args.Name
	}

	if res, blocked := checkManifestSafety(m, t.Allow, t.AllowAll, "edit"); blocked {
		return res, nil
	}

	// toml.Marshal(m) is deliberately not used here to re-serialize the
	// edit's result: updated is already valid TOML (parseManifest just
	// confirmed it) and is exactly what the model asked to change --
	// round-tripping it through Marshal would silently reformat every
	// untouched line (key ordering, quoting style) the same way
	// tool_create's own encode-from-scratch approach is expected to for a
	// brand-new manifest, but here that would turn a surgical one-line
	// fix into a full-file diff a human reviewer did not ask for.
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if err := writeStringAtomic(ctx, manifestPath, updated, 0o644); err != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		return Result{}, fmt.Errorf("tool_edit: could not save %s: %w", manifestPath, err)
	}

	state, err := LoadState(toolDir)
	if err != nil {
		return Result{}, fmt.Errorf("tool_edit: could not load state for %q: %w", args.Name, err)
	}
	if err := SaveState(toolDir, state.Edit()); err != nil {
		return Result{}, fmt.Errorf("tool_edit: could not save state for %q: %w", args.Name, err)
	}

	verb := "replaced 1 occurrence"
	if args.ReplaceAll {
		verb = fmt.Sprintf("replaced %d occurrence(s)", count)
	}
	return OKResult(fmt.Sprintf("%s in %q's tool.toml. State: unverified -- run tool_probe before using it again.", verb, args.Name)), nil
}

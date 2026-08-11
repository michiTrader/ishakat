// tool_probe.go implements §19.5/§19.6's gate 3: "the self-test; pass →
// verified; fail → returns the error to iterate on". This is the piece
// that actually enforces rule 1 ("an unverified tool cannot be used for
// anything") by being the *only* path that may move a tool from
// StateUnverified (or StateBroken) to StateVerified — lifecycle.go's own
// Probe method already encodes that transition; this file is simply its
// first real caller, driving a genuine invocation of the tool rather than
// a hand-constructed pass/fail bool.
//
// Only rung 1 (declarative tool.toml, no run.py sidecar) is probed here.
// A future rung-2 script-tool executor will need its own hash-inputs list
// (ManifestFileName plus that sidecar's own name, per ComputeHash's doc
// comment on caller-owned ordering) but the state-machine calls below —
// LoadState/Probe/SaveState — are already rung-agnostic and need no change
// when that lands.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// toolProbeArgs is tool_probe's argument shape: which tool, by name, to
// probe. Unlike tool_list (which needs no arguments at all), a probe is
// necessarily about one specific tool — probing every tool in one call
// would silently hide which one actually failed, exactly the ambiguity
// tool_list.go's own per-tool line format is designed to avoid on the read
// side.
type toolProbeArgs struct {
	Name string `json:"name"`
}

// ToolProbe is the tool_probe meta-tool. Dir is the same layer-2 tools
// directory ToolList/DeclarativeTools take (this package's own "minimal,
// purpose-built argument" pattern, never a config.Tools value). Allow and
// AllowAll are the same egress allowlist DeclarativeTools passes to every
// discovered DeclarativeTool — a probe's own real HTTP call goes through
// the identical boundary check a normal invocation would, so the two must
// agree on what "allowed" means for a given install.
type ToolProbe struct {
	Dir      string
	Allow    []string
	AllowAll bool
}

var _ Tool = ToolProbe{}

func (ToolProbe) Name() string   { return "tool_probe" }
func (ToolProbe) Danger() Danger { return DangerLow }
func (ToolProbe) Description() string {
	return "Run a layer-2 tool's own self-test ([selftest] in its tool.toml). Passing moves it from unverified to verified, making it usable; failing returns the error to iterate on. A tool cannot be used at all until this has passed once."
}

func (ToolProbe) Parameters() json.RawMessage {
	return objectSchema(map[string]prop{
		"name": {
			Type:        "string",
			Description: "The tool's name, exactly as it appears in tool_list's output or its tool.toml's [name].",
		},
	}, "name")
}

// Run finds name's manifest directory under t.Dir, runs its declarative
// request once (with [selftest].env applied for the call's duration and
// [selftest].args as the call's own arguments), checks [selftest].expect
// as a substring of the output when set, and records the outcome via
// lifecycle.Probe/SaveState.
//
// A Go error means the probe could not even be attempted: bad arguments
// JSON, a missing name, or an unreadable/unparseable manifest — all
// preconditions Run itself checks before doing any probing work, matching
// tool.go's own Result-vs-error split. An unknown tool name is deliberately
// an ErrorResult, not a Go error — unlike Registry.Run's own "unregistered
// name" case (a caller mistake before dispatch even starts), the model
// itself is expected to name tools by exactly what tool_list showed it,
// so a typo here is squarely the same "attempted and failed, model should
// see it and can react" shape every other manifest/argument problem in
// this tool gets. A probe that ran but failed (the declarative call itself
// errored, or the response did not contain [selftest].expect) is also a
// Result with IsError set — the model sees exactly why, which is the
// "returns the error to iterate on" §19.5 promises.
func (t ToolProbe) Run(ctx context.Context, rawArgs json.RawMessage) (Result, error) {
	var args toolProbeArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return Result{}, fmt.Errorf("tool_probe: invalid arguments: %w", err)
	}
	if strings.TrimSpace(args.Name) == "" {
		return Result{}, fmt.Errorf("tool_probe: name is required")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if t.Dir == "" {
		return ErrorResult("tool_probe: no tools directory is configured"), nil
	}

	toolDir := filepath.Join(t.Dir, args.Name)
	manifestPath := filepath.Join(toolDir, ManifestFileName)
	body, err := os.ReadFile(manifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrorResult(fmt.Sprintf("no tool named %q found under %s", args.Name, t.Dir)), nil
		}
		return Result{}, fmt.Errorf("tool_probe: could not read %s: %w", manifestPath, err)
	}
	m, err := parseManifest(body)
	if err != nil {
		return Result{}, fmt.Errorf("tool_probe: could not parse %s: %w", manifestPath, err)
	}
	if m.Name == "" {
		m.Name = args.Name
	}
	m.Dir = toolDir

	state, err := LoadState(toolDir)
	if err != nil {
		return Result{}, fmt.Errorf("tool_probe: could not load state for %q: %w", args.Name, err)
	}

	hash, err := ComputeHash(toolDir, ManifestFileName)
	if err != nil {
		return Result{}, fmt.Errorf("tool_probe: could not hash %q: %w", args.Name, err)
	}

	// §19.8 mitigation 6: content that changed since the last successful
	// probe without going through tool_edit is a tamper signal. This
	// re-probe is exactly the event that should clear it — DetectTamper
	// only demotes an already-StateVerified tool, and the fresh Probe
	// call a few lines below overwrites both State and Hash unconditionally
	// on this run's own outcome anyway, so there is nothing to fold in
	// here beyond letting the demotion happen before that overwrite for
	// a caller that inspects the intermediate state.
	if demoted, wasTampered := DetectTamper(state, hash); wasTampered {
		state = demoted
	}

	callArgs, err := json.Marshal(m.Selftest.Args)
	if err != nil {
		return Result{}, fmt.Errorf("tool_probe: could not encode selftest args for %q: %w", args.Name, err)
	}

	restoreEnv := setEnvTemporarily(m.Selftest.Env)
	res, runErr := DeclarativeTool{Manifest: m, Allow: t.Allow, AllowAll: t.AllowAll}.Run(ctx, callArgs)
	restoreEnv()

	var errMsg string
	passed := true
	switch {
	case runErr != nil:
		passed = false
		errMsg = runErr.Error()
	case res.IsError:
		passed = false
		errMsg = res.Text
	case m.Selftest.Expect != "" && !strings.Contains(res.Text, m.Selftest.Expect):
		passed = false
		errMsg = fmt.Sprintf("selftest expected output to contain %q, got: %s", m.Selftest.Expect, truncateForError([]byte(res.Text)))
	}

	nextState := state.Probe(passed, hash, errMsg)
	if err := SaveState(toolDir, nextState); err != nil {
		return Result{}, fmt.Errorf("tool_probe: could not save state for %q: %w", args.Name, err)
	}

	if !passed {
		return ErrorResult(fmt.Sprintf("probe failed for %q: %s (state: unverified)", args.Name, errMsg)), nil
	}
	return OKResult(fmt.Sprintf("probe passed for %q: state is now verified", args.Name)), nil
}

// setEnvTemporarily applies env (may be nil/empty) to the current process
// via os.Setenv, and returns a func that restores every named variable to
// its previous value (or unsets it, if it was previously unset). This is
// process-wide, not per-request, because DeclarativeTool.Run's own auth/
// request machinery reads credentials via os.LookupEnv (see lookupEnv in
// declarative.go) with no per-call override hook — §19.2's own worked
// example (BYBIT_TESTNET=1) is exactly this shape, a variable the real
// request-building code already consults by name. A probe is expected to
// run synchronously and briefly, so the window where this process's
// environment differs from its steady state is small, and this package's
// own Tool interface has no concurrent-Run requirement this would violate.
func setEnvTemporarily(env map[string]string) func() {
	if len(env) == 0 {
		return func() {}
	}
	type saved struct {
		value string
		wasOK bool
	}
	prev := make(map[string]saved, len(env))
	for k, v := range env {
		old, ok := os.LookupEnv(k)
		prev[k] = saved{value: old, wasOK: ok}
		os.Setenv(k, v)
	}
	return func() {
		for k, s := range prev {
			if s.wasOK {
				os.Setenv(k, s.value)
			} else {
				os.Unsetenv(k)
			}
		}
	}
}

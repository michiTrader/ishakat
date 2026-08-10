// lifecycle.go implements §19.5's state machine for a layer-2 tool
// (declarative or script): the sidecar bookkeeping that lets Step 21's
// meta-tools (tool_create/tool_probe/tool_edit/tool_delete, plus a future
// `/tools` audit view) answer "what state is this tool in" without
// re-deriving it from scratch on every call.
//
//	proposal --> unverified --probe--> verified --> in use --> promoted
//	                 ^                     |             (rung 2->3, by PR)
//	     probe fails |         fails twice in real use
//	                 |                     v
//	           tool_edit (iterate)      broken --> agent reports it
//	                                               and offers to fix
//	                                    |
//	          unused ArchiveDays days --+--> archived
//	                                        (out of the prompt, still on disk)
//
// This file only encodes the state machine and its on-disk persistence; it
// deliberately does not decide *when* a transition happens in response to a
// real tool call (that belongs to the caller running the actual probe or
// invocation — Step 21's meta-tools, still to be written) and does not wire
// CanUse into Registry.Run's own dispatch yet (also Step 21's job, once
// there is a real script-tool executor whose result this can gate).
//
// State is persisted as one JSON file per tool directory (StateFileName,
// a sibling of ManifestFileName — declarative.go's own Discover walks the
// same directories, so a future caller finds both with one os.ReadDir), at
// 0644 like tool.toml itself: neither file holds a secret (declarative.go's
// AuthSpec already keeps credential material in named environment
// variables, never in the manifest), so there is no reason to restrict
// this one more tightly than its own manifest.
package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// LifecycleState is one node of §19.5's state diagram.
type LifecycleState string

const (
	// StateUnverified is where every newly written tool starts (§19.5 rule
	// 1: "an unverified tool cannot be used for anything"), and where a
	// tool returns to after a failed probe or after tool_edit changes it.
	StateUnverified LifecycleState = "unverified"
	// StateVerified means the tool's self-test has passed against its
	// current on-disk content (ToolState.Hash pins exactly which content).
	StateVerified LifecycleState = "verified"
	// StateBroken means a verified tool failed twice in real use (§19.5's
	// own stated threshold) — usable state, but the agent is expected to
	// report it and offer to fix it rather than keep calling it silently.
	StateBroken LifecycleState = "broken"
	// StateArchived means the tool has gone unused past the configured
	// ArchiveDays: out of the system prompt (no longer costing tokens),
	// still on disk, revivable.
	StateArchived LifecycleState = "archived"
)

// StateFileName is the sidecar file recording a tool's ToolState, a
// sibling of ManifestFileName in the same tool directory.
const StateFileName = "state.json"

// ToolState is one tool's full lifecycle bookkeeping.
type ToolState struct {
	// State is the current node in §19.5's diagram.
	State LifecycleState `json:"state"`
	// Hash pins the exact on-disk content (manifest, plus a run.py
	// sidecar for a rung-2 script tool once that lands) the last
	// successful probe verified — see ComputeHash and DetectTamper.
	// Empty means "never probed" (the state a brand new proposal starts
	// in, before tool_probe has run even once).
	Hash string `json:"hash,omitempty"`
	// UseCount and LastUsed (YYYY-MM-DD) track real invocations, feeding
	// both a `/tools` audit view ("use count, last used" — §19.8
	// mitigation 7) and IsStale's own archive-on-disuse check.
	UseCount int    `json:"use_count"`
	LastUsed string `json:"last_used,omitempty"`
	// FailCount counts *consecutive* real-use failures since the last
	// success; two from StateVerified is what RecordUse demotes to
	// StateBroken (§19.5's own stated threshold). A success resets it to
	// zero, matching "fails twice in a row", not "twice ever".
	FailCount int `json:"fail_count"`
	// LastError is the most recent probe or real-use failure's message,
	// kept for tool_edit's own "here is what to fix" starting point.
	LastError string `json:"last_error,omitempty"`
	// PreviousState remembers what State was immediately before Archive
	// moved it to StateArchived, so Revive restores the tool to where it
	// actually was (StateVerified, ordinarily — an unverified or broken
	// tool going unused for ArchiveDays is a less common but not
	// impossible path) rather than guessing.
	PreviousState LifecycleState `json:"previous_state,omitempty"`
}

// LoadState reads dir's StateFileName. A missing file is not an error —
// every layer-2 tool that predates this lifecycle machinery (every
// hand-written tool.toml from Step 20's own tests, and any manifest a
// human wrote directly without going through tool_create) has none —
// and returns the zero-value proposal state, StateUnverified with an
// empty Hash, matching rule 1's own default: a tool with no recorded
// state has necessarily never passed a probe under this mechanism, so it
// cannot be used until one runs.
func LoadState(dir string) (ToolState, error) {
	body, err := os.ReadFile(filepath.Join(dir, StateFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return ToolState{State: StateUnverified}, nil
		}
		return ToolState{}, fmt.Errorf("could not read %s: %w", filepath.Join(dir, StateFileName), err)
	}
	var s ToolState
	if err := json.Unmarshal(body, &s); err != nil {
		return ToolState{}, fmt.Errorf("could not parse %s: %w", filepath.Join(dir, StateFileName), err)
	}
	return s, nil
}

// SaveState writes s to dir's StateFileName atomically (a sibling temp
// file plus rename, the same pattern writeStringAtomic already uses for
// write_file/edit_file — see that function's own doc comment for why the
// temp file has to live in the same directory as its target).
func SaveState(dir string, s ToolState) error {
	body, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("could not encode tool state: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".ishakat-state-*")
	if err != nil {
		return fmt.Errorf("could not create a temp file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	_, writeErr := tmp.Write(append(body, '\n'))
	syncErr := tmp.Sync()
	closeErr := tmp.Close()
	if writeErr != nil {
		return writeErr
	}
	if syncErr != nil {
		return syncErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpPath, filepath.Join(dir, StateFileName))
}

// ComputeHash is the SHA-256, hex-encoded digest of paths' contents,
// prefixed by each file's own base name so that swapping two
// same-content files (or a file going missing entirely) changes the
// result — §19.8 mitigation 6's "hash pinning" needs to notice *which*
// file changed shape, not just whether the concatenated bytes happen to
// still match. paths is hashed in the order given by the caller (Probe's
// own caller is expected to pass ManifestFileName first, then any
// run.py sidecar, a fixed and therefore reproducible order) rather than
// sorted here, so two different orderings are deliberately two different
// hashes — the caller owns that decision, this function does not second-
// guess it.
func ComputeHash(dir string, paths ...string) (string, error) {
	h := sha256.New()
	for _, p := range paths {
		body, err := os.ReadFile(filepath.Join(dir, p))
		if err != nil {
			return "", fmt.Errorf("could not read %s for hashing: %w", filepath.Join(dir, p), err)
		}
		fmt.Fprintf(h, "%s\x00%d\x00", p, len(body))
		h.Write(body)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// DetectTamper compares s's own recorded Hash against currentHash (a
// fresh ComputeHash over the tool's real files right now). A mismatch
// means the on-disk content changed since the last successful probe
// without going through tool_edit — §19.8 mitigation 6, verbatim: "If a
// run.py changed on disk without going through tool_edit, the tool is
// demoted to unverified and reported." tampered=true is what triggers
// that report; the returned ToolState is only different from s when a
// demotion actually happened (StateVerified -> StateUnverified). An
// empty s.Hash (never probed) never counts as tampering — there is
// nothing yet to have drifted from.
func DetectTamper(s ToolState, currentHash string) (ToolState, bool) {
	if s.Hash == "" || s.Hash == currentHash {
		return s, false
	}
	next := s
	if s.State == StateVerified {
		next.State = StateUnverified
	}
	return next, true
}

// Probe records the outcome of running a tool's self-test (tool_probe):
// passed transitions to StateVerified and pins hash as the content that
// passed (clearing FailCount and LastError — a fresh pass is a clean
// slate); a failure transitions to StateUnverified (whatever state the
// tool was in before, including StateBroken — a probe re-run is always
// available as the "iterate" path §19.5's diagram draws from
// tool_edit) and records errMsg for tool_edit's own use.
func (s ToolState) Probe(passed bool, hash, errMsg string) ToolState {
	next := s
	if passed {
		next.State = StateVerified
		next.Hash = hash
		next.FailCount = 0
		next.LastError = ""
		return next
	}
	next.State = StateUnverified
	next.LastError = errMsg
	return next
}

// Edit unconditionally demotes s to StateUnverified — §19.5's own text:
// "tool_edit: fixes a tool; demotes it to unverified until re-probed."
// FailCount and LastError are cleared: an edit is meant to have addressed
// whatever was failing, so the next real use (or probe) starts counting
// fresh rather than carrying over a grievance about content that no
// longer exists. Hash is deliberately left untouched here — it still
// names the *previous* passing content until the next successful Probe
// records the edited content's own hash, which is exactly the window
// DetectTamper needs to stay meaningful for a tool that was edited but
// not yet re-probed.
func (s ToolState) Edit() ToolState {
	next := s
	next.State = StateUnverified
	next.FailCount = 0
	next.LastError = ""
	return next
}

// RecordUse updates s for one real invocation on date today (YYYY-MM-DD).
// A successful use always resets FailCount to zero — "fails twice", per
// §19.5, means twice *in a row*, not twice ever. A failure increments
// FailCount and, only when s.State is StateVerified and the new count
// reaches 2, demotes to StateBroken; a tool that was already
// StateUnverified or StateBroken failing again stays exactly where it
// was — there is no lower state to fall into, and an unverified tool
// should not even have been callable in the first place (rule 1), so
// this path exists mainly for a caller that chooses to record the
// attempt anyway for audit purposes.
func (s ToolState) RecordUse(today string, ok bool, errMsg string) ToolState {
	next := s
	next.UseCount++
	next.LastUsed = today
	if ok {
		next.FailCount = 0
		next.LastError = ""
		return next
	}
	next.FailCount++
	next.LastError = errMsg
	if next.State == StateVerified && next.FailCount >= 2 {
		next.State = StateBroken
	}
	return next
}

// Archive moves s to StateArchived, remembering PreviousState so Revive
// can restore it later. Archiving an already-archived state is a no-op
// (idempotent, matching config's own "end state already true" rule
// already established elsewhere in this codebase for a removal-shaped
// operation).
func (s ToolState) Archive() ToolState {
	if s.State == StateArchived {
		return s
	}
	next := s
	next.PreviousState = s.State
	next.State = StateArchived
	return next
}

// Revive restores s from StateArchived back to PreviousState — "/tools
// revive <name> brings it back", §19.5. Reviving a state that is not
// archived is a no-op; PreviousState being empty (should not happen via
// Archive, but defensive against a hand-edited state.json) falls back to
// StateVerified rather than an empty LifecycleState that CanUse would
// then simply treat as "not verified" anyway — StateVerified is the
// documented common case ("in use -> ... unused N days -> archived") a
// human reviving a tool almost certainly means to restore to.
func (s ToolState) Revive() ToolState {
	if s.State != StateArchived {
		return s
	}
	next := s
	next.State = s.PreviousState
	if next.State == "" {
		next.State = StateVerified
	}
	next.PreviousState = ""
	return next
}

// CanUse reports whether s's tool may be invoked right now — §19.5 rule
// 1, generalized to every non-verified state: only StateVerified may run.
// StateUnverified has never passed a probe (or has been demoted since);
// StateBroken failed twice in real use and is waiting on tool_edit;
// StateArchived is out of the prompt entirely. None of the three is "the
// model tried to call it and got an error" — Guard/Registry's own
// dispatch (Step 21's remaining wiring) is expected to consult this
// before a call ever reaches Run, not after.
func (s ToolState) CanUse() bool {
	return s.State == StateVerified
}

// IsStale reports whether s's tool has gone unused long enough (per
// archiveDays, translated from config.Tools.ArchiveDays by the caller —
// this package takes the plain int, not a config.Tools, per this file's
// own "minimal, purpose-built argument" pattern) to be a candidate for
// Archive. A tool with an empty LastUsed (never yet used even once,
// including one that just passed its first probe a moment ago) is never
// stale — §19.5's archive rule is about disuse *after* being put into
// service, not about a probation period for something brand new that
// has not had a chance to be used yet. archiveDays <= 0 disables
// archiving entirely (an explicit "never archive" configuration, and the
// safe reading of a caller that has not set config.Tools.ArchiveDays at
// all), matching how a zero timeout/budget already means "no limit"
// elsewhere in this codebase (engine's own MaxCallsPerTurn/BudgetUSD).
// A LastUsed value that fails to parse as YYYY-MM-DD is treated as not
// stale rather than erroring — a malformed date here should surface as a
// data-quality report elsewhere, not silently archive a tool that might
// still be in active, correct use.
func IsStale(s ToolState, today string, archiveDays int) bool {
	if archiveDays <= 0 || s.LastUsed == "" {
		return false
	}
	last, err := time.Parse("2006-01-02", s.LastUsed)
	if err != nil {
		return false
	}
	now, err := time.Parse("2006-01-02", today)
	if err != nil {
		return false
	}
	return now.Sub(last) >= time.Duration(archiveDays)*24*time.Hour
}

// sortedFileNames is a tiny helper kept for callers that want to hash a
// directory's files in a stable, name-sorted order rather than the fixed
// order ComputeHash itself takes as an explicit argument list — useful
// for a caller that does not yet know a script tool's exact sidecar file
// name (a future rung-2 executor might allow more than one script file
// per tool). Not used by this file itself; kept here rather than in a
// test-only helper because ComputeHash's own doc comment explicitly
// defers this ordering decision to the caller, and this is the
// straightforward implementation of the one sensible default ordering.
func sortedFileNames(names []string) []string {
	out := append([]string(nil), names...)
	sort.Strings(out)
	return out
}

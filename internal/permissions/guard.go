// Package permissions applies the runtime tool authorization policy.
package permissions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/MichiTrader/ishakat/internal/config"
)

// Tier describes the possible impact of running a tool.
type Tier uint8

const (
	Low Tier = iota
	Medium
	High
)

// Request is the immutable description shown to an approval UI.
type Request struct {
	Name      string
	Arguments json.RawMessage
	Tier      Tier
}

// Decision is the response supplied by a person reviewing a request.
type Decision struct {
	Allow        bool
	AllowSession bool
}

// Reviewer obtains an explicit approval. Implementations may present a TUI
// overlay, read an interactive terminal, or reject when no human is present.
type Reviewer interface {
	Review(context.Context, Request) (Decision, error)
}

// ErrDenied reports that a configured or explicit permission boundary refused
// the tool request. Its message is safe to return to the model as tool output.
var ErrDenied = errors.New("tool permission denied")

// refusal builds a denial that ENDS THE TURN, as opposed to one that is
// ordinary tool-error data. Both satisfy errors.Is(err, ErrDenied); only this
// one satisfies the Denied() contract internal/engine matches with errors.As
// (§21.9 fix 1, docs/BUG-rate-limit-amplifier.md).
//
// The line between the two is not "how bad is it" — it is a question about
// whether another provider request could possibly change the answer:
//
//   - **Turn-ending** (this function). A human refused, or no human can be
//     reached to ask. Nothing the model does next in this turn can be
//     approved, so every further request is pure amplification. This is the
//     defect that took a real user's account offline: the model receives the
//     refusal as data, tries a variant, and each variant is another request
//     carrying the whole grown history.
//
//   - **Data** (plain fmt.Errorf with %w). A configuration boundary refused
//     *these arguments* or *this tool*. A different path or a different tool
//     may well be allowed, and the model choosing one is correct recovery —
//     the error-is-data mechanism of §12bis working as intended (§3), which
//     is the reason ishakat needs no Planner. Bounding the abuse of that is
//     loop detection's job (fix 3), not this function's.
//
// So a `write_file` blocked by write_deny stays data: writing somewhere legal
// instead is the right next move. A human pressing "no" does not, because
// the human already considered the alternatives and declined.
func refusal(format string, a ...any) error {
	return &deniedError{msg: ErrDenied.Error() + ": " + fmt.Sprintf(format, a...)}
}

// deniedError is a refusal that ends the turn. It unwraps to ErrDenied so
// every existing errors.Is(err, ErrDenied) call site keeps working unchanged,
// and it reports Denied() so internal/engine can recognize it structurally
// without importing this package — the same technique provider.Error already
// uses for the retry hint.
type deniedError struct{ msg string }

func (e *deniedError) Error() string { return e.msg }

// Unwrap keeps errors.Is(err, ErrDenied) true for callers that only care that
// permission was refused, regardless of which kind of refusal it was.
func (e *deniedError) Unwrap() error { return ErrDenied }

// Denied satisfies the contract internal/engine matches with errors.As. It is
// the whole reason this type exists rather than a plain wrapped sentinel.
func (e *deniedError) Denied() bool { return true }

// Guard applies configured hard denies, policy modes, optional session grants,
// and an injected reviewer. It is safe for sequential or concurrent callers.
type Guard struct {
	permissions config.Permissions
	yolo        bool
	reviewer    Reviewer

	// tiers supplements tierFor's fixed switch (the eight native tools)
	// for names it does not recognize -- Step 20's declarative tools chief
	// among them. nil (the zero value, and every pre-Step-20 caller that
	// never calls SetToolTiers) means "no supplement": tierForRequest then
	// falls back to the exact same High/"ask" default this package always
	// had, so a caller that ignores this field sees no behavior change.
	tiers map[string]Tier

	mu      sync.Mutex
	session map[string]struct{}
}

// SetToolTiers registers the Tier a tool name outside tierFor's own fixed
// switch should use, both for the Request.Tier a Reviewer sees and for
// mode's default branch (see mode's own doc comment on how a Tier maps to a
// permission class there). A caller (internal/app) builds this from the
// real Registry's own Tool.Danger() -- the same one-way-ratcheted inference
// declarative.go's inferDanger already applies (§19.5 rule 2: a manifest
// may only ever raise its own risk tier, never lower it) -- so a Guard
// never has to import internal/tools to reason about a tool it did not
// itself define, preserving the package boundary between the two exactly
// as before. Names tierFor's own switch already covers (the eight native
// tools) are unaffected regardless of what this map says for them: a
// manifest naming itself "bash" cannot reduce bash's own hardcoded High
// tier by appearing here, because tierFor's fixed switch is always
// consulted first.
func (g *Guard) SetToolTiers(tiers map[string]Tier) {
	g.tiers = tiers
}

// New creates a guard for one application session. yolo changes only ask into
// allow for file writes and shell commands; it never overrides hard denies.
func New(permissions config.Permissions, yolo bool, reviewer Reviewer) *Guard {
	return &Guard{
		permissions: permissions,
		yolo:        yolo,
		reviewer:    reviewer,
		session:     make(map[string]struct{}),
	}
}

// Authorize permits a request or returns ErrDenied. A high-tier request cannot
// receive a session grant, even if the reviewer asks for one.
func (g *Guard) Authorize(ctx context.Context, name string, arguments json.RawMessage) error {
	req := Request{Name: name, Arguments: clone(arguments), Tier: g.tierFor(name)}
	if reason := g.hardDeny(req); reason != "" {
		return fmt.Errorf("%w: %s", ErrDenied, reason)
	}

	mode := g.mode(req)
	if mode == "deny" {
		return fmt.Errorf("%w: %s is disabled by configuration", ErrDenied, req.Name)
	}
	if g.yolo && (req.Name == "write_file" || req.Name == "edit_file" || req.Name == "bash") {
		return nil
	}
	if mode == "allow" || req.Tier == Low {
		return nil
	}

	key := requestKey(req)
	if req.Tier == Medium && g.hasSessionGrant(key) {
		return nil
	}
	// The three refusals below all end the turn (§21.9 fix 1). They share one
	// property the configuration refusals above do not: no human is available
	// to change the answer within this turn — either because there is nobody
	// to ask, because asking failed, or because the person was asked and said
	// no. Retrying a variant against any of them cannot succeed, so returning
	// them as data would buy nothing and cost a provider request each time.
	if g.reviewer == nil {
		return refusal("%s requires interactive approval, and no reviewer is available", req.Name)
	}

	decision, err := g.reviewer.Review(ctx, req)
	if err != nil {
		return refusal("approval failed: %v", err)
	}
	if !decision.Allow {
		return refusal("user declined %s", req.Name)
	}
	if decision.AllowSession && req.Tier == Medium && g.permissions.AllowSession {
		g.mu.Lock()
		g.session[key] = struct{}{}
		g.mu.Unlock()
	}
	return nil
}

// isNativeToolName reports whether name is one of tierFor's/mode's own
// eight recognized names (layer 1, §19.1) -- the boundary (*Guard).tierFor
// and (*Guard).mode use to decide whether a name may ever be supplemented
// by g.tiers: a manifest naming itself "bash" must never reduce bash's own
// hardcoded High tier by appearing in g.tiers, so both methods consult
// g.tiers only for names this reports false for. dispatch (Step 22) is
// listed here for the identical reason bash is: a manifest or declarative
// tool naming itself "dispatch" must never be able to reduce the tier a
// sub-agent's own second tool-calling loop is treated with.
func isNativeToolName(name string) bool {
	switch name {
	case "read_file", "glob", "grep", "fetch", "write_file", "edit_file", "bash", "dispatch":
		return true
	default:
		return false
	}
}

// tierFor is the fixed switch over layer 1's eight native tools -- kept as
// a free function, not a method, so guard_test.go's existing
// TestGuardFetchTierIsLow (calling tierFor("fetch") directly) keeps
// compiling unchanged, and so its own contract (these eight names, no
// more) can never quietly depend on a Guard's tiers map.
func tierFor(name string) Tier {
	switch name {
	case "read_file", "glob", "grep", "fetch":
		return Low
	case "write_file", "edit_file":
		return Medium
	case "bash":
		return High
	case "dispatch":
		// dispatch (Step 22) is explicit here for the same reason bash is,
		// not because the fallthrough default would give a different
		// answer: dispatch.go's own Danger() is already unconditionally
		// DangerHigh (§19.5 rule #2 -- a tier is inferred from what a tool
		// can do, and a sub-agent's own registry may itself contain bash,
		// write_file or another dispatch), so spelling the case out here
		// keeps this switch legible as the eight-tool table §19.1 actually
		// documents, rather than relying on readers to know the default
		// happens to agree.
		return High
	default:
		// Unknown and future tools must be reviewed rather than accidentally
		// inheriting a low-risk default.
		return High
	}
}

// tierFor is tierFor(name) supplemented by g.tiers for any name outside
// the fixed native eight -- Step 20's declarative tools chief among them.
// g.tiers == nil (no caller ever set it, matching every pre-Step-20 Guard
// and every Guard a caller builds without SetToolTiers) falls through to
// tierFor's own High default unchanged, so this method is a pure addition:
// nothing that worked before behaves differently now.
func (g *Guard) tierFor(name string) Tier {
	if isNativeToolName(name) {
		return tierFor(name)
	}
	if g.tiers != nil {
		if t, ok := g.tiers[name]; ok {
			return t
		}
	}
	return tierFor(name)
}

func (g *Guard) mode(req Request) string {
	switch req.Name {
	// fetch shares Read's policy knob rather than getting its own config
	// key: both are danger:low, read-only operations whose actual boundary
	// is enforced elsewhere (the filesystem for read_file/glob/grep, the
	// egress allowlist baked into the Fetch tool itself for fetch — see
	// fetch.go's doc comment and §19.8). A new host still needs its own
	// allowlist entry regardless of this mode, so "allow" here only means
	// "do not additionally prompt for hosts already on that list".
	case "read_file", "glob", "grep", "fetch":
		return g.permissions.Read
	case "write_file", "edit_file":
		return g.permissions.Write
	case "bash", "dispatch":
		return g.permissions.Shell
	default:
		// A name outside the native eight (Step 20's declarative tools
		// chief among them) reuses the policy knob matching req.Tier --
		// itself already resolved through g.tierFor, which honors
		// g.tiers/Tool.Danger() rather than assuming High. Low mirrors
		// read_file's own reasoning (reversible, no destructive local
		// effect); Medium mirrors write_file's (scoped, undoable); a tool
		// nothing marked otherwise stays High and reuses Shell's
		// generally-stricter default. A caller that never calls
		// SetToolTiers sees req.Tier == High here exactly as before (since
		// tierFor's own default is High), and defaults.toml ships
		// shell = "ask", the same value the old bare "ask" default
		// hardcoded -- so an install that has not touched
		// [tools.permissions] and has no declarative tools of its own sees
		// no change at all.
		switch req.Tier {
		case Low:
			return g.permissions.Read
		case Medium:
			return g.permissions.Write
		default:
			return g.permissions.Shell
		}
	}
}

func (g *Guard) hardDeny(req Request) string {
	var input struct {
		Path    string `json:"path"`
		Command string `json:"command"`
	}
	if err := json.Unmarshal(req.Arguments, &input); err != nil {
		return "tool arguments are not valid JSON"
	}
	if input.Path != "" {
		for _, pattern := range g.permissions.WriteDeny {
			if matchesPath(pattern, input.Path) {
				return fmt.Sprintf("path %q matches write_deny pattern %q", input.Path, pattern)
			}
		}
	}
	if req.Name == "bash" {
		for _, pattern := range g.permissions.ShellDeny {
			if matches(pattern, input.Command) {
				return fmt.Sprintf("command matches shell_deny pattern %q", pattern)
			}
		}
	}
	return ""
}

func (g *Guard) hasSessionGrant(key string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, ok := g.session[key]
	return ok
}

func requestKey(req Request) string {
	return req.Name + "\x00" + string(req.Arguments)
}

func clone(value json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), value...)
}

func matchesPath(pattern, value string) bool {
	if matches(filepath.ToSlash(pattern), filepath.ToSlash(value)) {
		return true
	}
	if strings.HasPrefix(value, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return matches(filepath.ToSlash(pattern), filepath.ToSlash(filepath.Join(home, value[2:])))
		}
	}
	return false
}

// matches accepts the documented ** glob form in addition to filepath.Match's
// single-path-component star. It deliberately remains small and deterministic.
func matches(pattern, value string) bool {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				b.WriteString(".*")
				i++
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString(".")
		case '.', '+', '(', ')', '[', ']', '{', '}', '^', '$', '|', '\\':
			b.WriteByte('\\')
			b.WriteByte(pattern[i])
		default:
			b.WriteByte(pattern[i])
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String()).MatchString(value)
}

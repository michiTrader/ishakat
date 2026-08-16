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

// Tier describes the possible impact of running a tool, using the four risk
// classes §21.5 defines rather than the three-level Low/Medium/High this
// package started with:
//
//   - Safe: no effect worth reviewing (read_file, glob, grep, fetch, and a
//     bash command §21.3 recognizes as read-only, e.g. "ls", "git status").
//     Bypasses review entirely.
//   - Controlled: executes code or writes derived artifacts, but stays
//     inside the project and is reversible via git (a bash command such as
//     "go build"/"go test"/"make"). As of this step Controlled also bypasses
//     review, the same as Safe -- NOT because it is risk-free, but because
//     §21.5's table only asks it under "readonly" autonomy, and autonomy
//     (readonly/agile/auto) does not exist in code yet (§21.14 assigns it to
//     Step 30). Introducing the class now, distinct from Safe, means Step 30
//     only has to add a readonly branch here -- nothing needs reclassifying.
//   - Sensitive: changes source, installs something, or reaches the network
//     (write_file, edit_file, and any bash command not otherwise
//     classified). This is the one class with session grants, and the class
//     --yolo turns ask into allow for.
//   - Critical: irreversible or externally visible (dispatch, and a bash
//     command shaped like "git push"). Never session-grantable and never
//     bypassed by --yolo either (§21.16 decision 2) -- Authorize enforces
//     this with an explicit req.Tier != Critical check in the yolo branch,
//     not by omission.
//
// Safe is the zero value only so the four constants read in increasing risk
// order; nothing in this package treats the zero value as a default --
// tierFor's own default (an unrecognized tool) is Critical, not Safe.
type Tier uint8

const (
	Safe Tier = iota
	Controlled
	Sensitive
	Critical
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

// Authorize permits a request or returns ErrDenied. A Critical request can
// never receive a session grant, and --yolo never bypasses one either
// (§21.16 decision 2) -- both checks below test req.Tier != Critical
// explicitly, rather than relying on Critical simply never matching the
// other branches' conditions.
func (g *Guard) Authorize(ctx context.Context, name string, arguments json.RawMessage) error {
	args := clone(arguments)
	req := Request{Name: name, Arguments: args, Tier: g.tierFor(name, args)}
	if reason := g.hardDeny(req); reason != "" {
		return fmt.Errorf("%w: %s", ErrDenied, reason)
	}

	mode := g.mode(req)
	if mode == "deny" {
		return fmt.Errorf("%w: %s is disabled by configuration", ErrDenied, req.Name)
	}
	if g.yolo && req.Tier != Critical && (req.Name == "write_file" || req.Name == "edit_file" || req.Name == "bash") {
		return nil
	}
	if mode == "allow" || req.Tier == Safe || req.Tier == Controlled {
		return nil
	}

	key := requestKey(req)
	if req.Tier == Sensitive && g.hasSessionGrant(key) {
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
	if decision.AllowSession && req.Tier == Sensitive && g.permissions.AllowSession {
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
// tier by appearing in g.tiers, so both methods consult g.tiers only for
// names this reports false for. dispatch (Step 22) is listed here for the
// identical reason bash is: a manifest or declarative tool naming itself
// "dispatch" must never be able to reduce the tier a sub-agent's own second
// tool-calling loop is treated with. Note that (*Guard).tierFor no longer
// even needs this function's help for bash specifically -- see that
// method's own doc comment -- but the guarantee it states remains true.
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
// TestGuardFetchTierIsSafe (calling tierFor("fetch") directly) keeps
// compiling unchanged, and so its own contract (these eight names, no
// more) can never quietly depend on a Guard's tiers map. bash's case here
// is only a fallback -- (*Guard).tierFor never actually reaches it, since
// bash is special-cased there before this function is called at all -- but
// it is kept here (rather than removed) so this switch still reads as the
// complete eight-tool table §19.1 documents.
func tierFor(name string) Tier {
	switch name {
	case "read_file", "glob", "grep", "fetch":
		return Safe
	case "write_file", "edit_file":
		return Sensitive
	case "bash":
		return Sensitive
	case "dispatch":
		// dispatch (Step 22) is explicit here for the same reason bash is,
		// not because the fallthrough default would give a different
		// answer: dispatch.go's own Danger() is already unconditionally
		// DangerHigh (§19.5 rule #2 -- a tier is inferred from what a tool
		// can do, and a sub-agent's own registry may itself contain bash,
		// write_file or another dispatch), so spelling the case out here
		// keeps this switch legible as the eight-tool table §19.1 actually
		// documents, rather than relying on readers to know the default
		// happens to agree. dispatch has no per-argument "obviously safe"
		// shape the way bash does (a sub-agent's own Guard and §21.11's
		// "cannot request a capability the parent lacks" already bound its
		// behavior), so it stays Critical unconditionally.
		return Critical
	default:
		// Unknown and future tools must be reviewed rather than accidentally
		// inheriting a low-risk default.
		return Critical
	}
}

// tierFor is tierFor(name) supplemented by g.tiers for any name outside
// the fixed native eight -- Step 20's declarative tools chief among them --
// with one exception: bash. bash is special-cased here, before even
// isNativeToolName is consulted, because bash fails §21.8's "entire tool is
// grantable" test -- its argument, not its name, determines the actual
// verb ("ls" and "git push" are both invocations of the same tool name).
// Routing bash straight to bashTier(args) means a manifest can never lower
// bash's tier via SetToolTiers by construction (g.tiers is never even
// consulted for it), the same guarantee isNativeToolName used to provide by
// gating, now provided structurally instead.
//
// g.tiers == nil (no caller ever set it, matching every pre-Step-20 Guard
// and every Guard a caller builds without SetToolTiers) falls through to
// tierFor's own Critical default unchanged, so this method is a pure
// addition for every other tool: nothing that worked before behaves
// differently now.
func (g *Guard) tierFor(name string, args json.RawMessage) Tier {
	if name == "bash" {
		return bashTier(args)
	}
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

// bashCommand mirrors the shape of tools.bash's own bashArgs{Command,
// TimeoutSeconds} just enough to read the command string back out of the
// raw JSON arguments a Reviewer/Guard sees. It is duplicated here rather
// than imported so internal/permissions does not have to depend on
// internal/tools to reason about bash's risk -- the same boundary
// SetToolTiers's own doc comment already describes for declarative tools.
type bashCommand struct {
	Command string `json:"command"`
}

// safeBashPrefixes are read-only commands §21.3's defect 1 asked for: bash
// was unconditionally High/Sensitive with no notion that some invocations
// have no effect worth reviewing at all. This list is deliberately short
// and literal, not an attempt at a general read-only classifier.
var safeBashPrefixes = []string{
	"ls", "pwd", "cat",
	"git status", "git diff", "git log",
	"node -v", "node --version",
}

// controlledBashPrefixes execute code or produce derived artifacts (a
// build's binary, a test binary's coverage output) but stay inside the
// project and are reversible via git -- §21.5's Controlled class.
var controlledBashPrefixes = []string{
	"go test", "go build", "go vet", "make", "npm test",
}

// criticalBashPrefixes are irreversible or externally visible -- §21.5's
// Critical class, never bypassed by --yolo and never session-grantable.
// git push --force* is additionally covered by defaults.toml's shell_deny
// hard-deny list (checked earlier in Authorize, before tierFor's result is
// ever consulted), so what this list actually governs is every other,
// legal form of git push.
var criticalBashPrefixes = []string{
	"git push",
}

// compoundShellMeta are the shell metacharacters that make a command's
// effect not fully described by its own leading word -- "ls && rm -rf
// /tmp/x" must not classify as Safe merely because it starts with "ls".
var compoundShellMeta = []string{";", "&&", "||", "|", "`", "$(", ">", "<"}

// isCompoundShellCommand reports whether cmd contains a shell metacharacter
// that lets more than one command run, or lets output escape into the
// filesystem or another process. This is a safety net, not a shell parser:
// it disqualifies Safe/Controlled classification for anything it cannot be
// sure about, falling back to Sensitive rather than trying to actually
// parse the compound shape.
func isCompoundShellCommand(cmd string) bool {
	for _, meta := range compoundShellMeta {
		if strings.Contains(cmd, meta) {
			return true
		}
	}
	return false
}

// hasWordPrefix reports whether cmd is exactly p, or begins with p followed
// by a space -- "git status" matches "git status --short" but not
// "git statusish". Modeled after matches()'s own glob-style simplicity
// elsewhere in this file, not a shell grammar.
func hasWordPrefix(cmd string, prefixes []string) bool {
	for _, p := range prefixes {
		if cmd == p || strings.HasPrefix(cmd, p+" ") {
			return true
		}
	}
	return false
}

// containsAfterMeta splits cmd on the sequencing metacharacters ;, |, and &
// and checks each resulting segment against prefixes -- so "go build ./...
// && git push origin main" is still caught as containing a Critical shape,
// even though the whole string does not itself start with "git push". This
// is checked before isCompoundShellCommand's disqualification so an
// embedded push is never under-classified as merely Sensitive.
func containsAfterMeta(cmd string, prefixes []string) bool {
	segments := strings.FieldsFunc(cmd, func(r rune) bool {
		return r == ';' || r == '|' || r == '&'
	})
	for _, seg := range segments {
		if hasWordPrefix(strings.TrimSpace(seg), prefixes) {
			return true
		}
	}
	return false
}

// bashTier classifies a bash invocation by its command argument, fixing
// §21.3 defect 1 (bash was unconditionally High/Sensitive with no read-only
// notion). An unparseable or empty command falls back to Sensitive, bash's
// prior default behavior, rather than guessing.
func bashTier(args json.RawMessage) Tier {
	var parsed bashCommand
	if err := json.Unmarshal(args, &parsed); err != nil {
		return Sensitive
	}
	cmd := strings.TrimSpace(parsed.Command)
	if cmd == "" {
		return Sensitive
	}
	// Critical is checked first, and checked even inside a compound command,
	// so an embedded "git push" is never under-classified by the compound
	// guard below.
	if hasWordPrefix(cmd, criticalBashPrefixes) || containsAfterMeta(cmd, criticalBashPrefixes) {
		return Critical
	}
	if isCompoundShellCommand(cmd) {
		return Sensitive
	}
	if hasWordPrefix(cmd, safeBashPrefixes) {
		return Safe
	}
	if hasWordPrefix(cmd, controlledBashPrefixes) {
		return Controlled
	}
	return Sensitive
}

// mode resolves req to one of config.Permissions's three ask/allow/deny
// knobs. Note this is a separate boundary from Authorize's Safe/Controlled
// review-skip: if Shell is configured "deny", a Safe or Controlled bash
// command is still refused here (the config boundary check for "shell
// disabled entirely" runs before Authorize ever looks at req.Tier), even
// though the same command would otherwise never have prompted a human.
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
		// g.tiers/Tool.Danger() rather than assuming Critical. Safe mirrors
		// read_file's own reasoning (reversible, no destructive local
		// effect); Sensitive mirrors write_file's (scoped, undoable);
		// Controlled and Critical both fall back to Shell's generally
		// stricter default. A caller that never calls SetToolTiers sees
		// req.Tier == Critical here exactly as before (since tierFor's own
		// default is Critical), and defaults.toml ships shell = "ask", the
		// same value the old bare "ask" default hardcoded -- so an install
		// that has not touched [tools.permissions] and has no declarative
		// tools of its own sees no change at all.
		switch req.Tier {
		case Safe:
			return g.permissions.Read
		case Sensitive:
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

// requestKey computes the session-grant key for req, fixing §21.3 defect 2
// for bash specifically: `requestKey(req Request) string { return req.Name +
// "\x00" + string(req.Arguments) }` keyed a grant on the exact argument
// bytes, so "allow for the session" covered "ls" and not "ls -la" -- the
// human grants, the agent varies one flag, the dialog returns.
//
// bash routes through bashSessionKey, which generalizes over flags (see its
// own doc comment for exactly how far, and why not further). Every other
// tool keeps the original exact-byte key unchanged: write_file/edit_file's
// arguments are a path and file content, not a command line with a
// flag/positional distinction, so there is no equivalent narrow
// generalization to apply without guessing -- widening their grants is not
// what defect 2's own worked example asks for, and is left to §21.12's
// richer [[permissions.rule]] pattern configuration (future work, not this
// step).
func requestKey(req Request) string {
	if req.Name == "bash" {
		if key, ok := bashSessionKey(req.Arguments); ok {
			return key
		}
	}
	return req.Name + "\x00" + string(req.Arguments)
}

// bashSessionKey builds bash's session-grant key from its command argument,
// or reports ok=false for an unparseable/empty command (in which case
// requestKey falls back to the exact-byte key, matching bashTier's own
// "cannot be sure, do not guess" fallback to Sensitive for the same input).
func bashSessionKey(args json.RawMessage) (key string, ok bool) {
	var parsed bashCommand
	if err := json.Unmarshal(args, &parsed); err != nil {
		return "", false
	}
	cmd := strings.TrimSpace(parsed.Command)
	if cmd == "" {
		return "", false
	}
	return "bash\x00family\x00" + bashFamily(cmd), true
}

// bashFamily reduces cmd to the sequence of tokens that do not look like a
// flag (do not start with "-"), joined back with single spaces. This is the
// "family" §21.8's own dialog mockup names for a generalized, editable
// pattern (e.g. `node tools/bench.js *`): two invocations agreeing on every
// non-flag token differ only in which flags they pass, exactly the axis
// defect 2's own worked example varies on ("ls" granting "ls -la"). Two
// invocations that disagree on a non-flag token (e.g. "echo one" vs "echo
// two") are genuinely different commands and must not share a grant --
// TestGuardDoesNotShareApprovalWithDifferentArguments pins that half.
//
// This is deliberately narrower than a general glob/pattern engine: it
// generalizes over flags only, never over positional arguments, so a grant
// can never silently widen to cover a filename, host or other value it was
// never shown approving. §21.12's own richer [[permissions.rule]] pattern
// configuration remains future work; this is the minimal fix defect 2 asks
// for, scoped to the one tool (bash) the closing criterion names.
func bashFamily(cmd string) string {
	tokens := strings.Fields(cmd)
	kept := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		if strings.HasPrefix(tok, "-") {
			continue
		}
		kept = append(kept, tok)
	}
	return strings.Join(kept, " ")
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

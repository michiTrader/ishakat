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
	"time"

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
//     "go build"/"go test"/"make"). Bypasses review the same as Safe under
//     every autonomy except Readonly (see Authorize's own autonomy gate,
//     added in Step 30) -- NOT because it is risk-free, but because §21.5's
//     table only asks it under "readonly" autonomy.
//   - Sensitive: changes source, installs something, or reaches the network
//     (write_file, edit_file, and any bash command not otherwise
//     classified). This is the one class with session grants, and the class
//     --yolo turns ask into allow for. Refused outright, with no dialog at
//     all, under Readonly autonomy.
//   - Critical: irreversible or externally visible (dispatch, and a bash
//     command shaped like "git push"). Never session-grantable and never
//     bypassed by --yolo either (§21.16 decision 2) -- Authorize enforces
//     this with an explicit req.Tier != Critical check in the yolo branch,
//     not by omission. Refused outright under Readonly, exactly like
//     Sensitive -- §21.5's table gives both the same "refuse" cell there.
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

// Autonomy is §21.4 layer 3: how much ishakat may decide without asking,
// set once by the human -- at the §21.4 layer 2 trust dialog (Step 30's own
// other half, see internal/trust), or later via a future /permissions (Step
// 32) -- and shown permanently in the status line (§21.1's own mockup:
// "auto·exec" left of the phase dot). It is a session-scoped Guard field,
// not a config.Permissions field, because it narrows what config's own
// ask/allow/deny mode is even allowed to answer, the same "a lower layer
// can never widen a higher one" rule §21.4's own table states for every
// pair of adjacent layers.
//
// Auto is the zero value so a Guard nobody ever calls SetAutonomy on --
// every pre-Step-30 caller, and every existing test in this file -- keeps
// exactly its previous behaviour: Authorize's own autonomy gate (see its
// doc comment) only ever changes a decision when the field is Readonly.
// Agile and Auto are deliberately left equivalent to each other inside
// this package for now: distinguishing them further -- §21.5's table gives
// Sensitive "ask" under Agile but "run" under Auto -- is future work this
// step's own closing criterion ("second run in a known project asks
// nothing", §21.14) does not require, since that criterion is about the
// trust question itself, not about every dialog Guard ever raises
// afterward. What Auto and Agile share today, and always have: Sensitive
// and Critical are governed by config.Permissions' own mode plus the
// reviewer, precisely as they were before this type existed.
type Autonomy uint8

const (
	Auto Autonomy = iota
	Agile
	Readonly
)

// ParseAutonomy converts the persisted/configured string form ("auto",
// "agile", "readonly") into an Autonomy, defaulting to Auto for an empty or
// unrecognized value -- matching the type's own zero-value contract, so a
// config with no [autonomy] table, or a trust record written before an
// unrecognized future value existed, behaves identically to one that spells
// out default = "auto".
func ParseAutonomy(s string) Autonomy {
	switch s {
	case "agile":
		return Agile
	case "readonly":
		return Readonly
	default:
		return Auto
	}
}

// String names a in the same lowercase form ParseAutonomy reads back and
// the status line displays (§21.1's own "auto·exec" mockup).
func (a Autonomy) String() string {
	switch a {
	case Agile:
		return "agile"
	case Readonly:
		return "readonly"
	default:
		return "auto"
	}
}

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

	// autonomy is §21.4 layer 3 (Autonomy's own doc comment). Read with
	// the mutex held, the same as session, because SetAutonomy (a future
	// /permissions command, Step 32) is expected to run concurrently with
	// an in-flight Authorize call the same way a session grant already
	// can.
	autonomy Autonomy

	// tiers supplements tierFor's fixed switch (the eight native tools)
	// for names it does not recognize -- Step 20's declarative tools chief
	// among them. nil (the zero value, and every pre-Step-20 caller that
	// never calls SetToolTiers) means "no supplement": tierForRequest then
	// falls back to the exact same High/"ask" default this package always
	// had, so a caller that ignores this field sees no behavior change.
	tiers map[string]Tier

	// missionDeny is §21.4 layer 4 (Mission, Step 31): the compiled
	// deny-only half of internal/mission.Compile's own output, in a shape
	// checked once inside hardDeny -- see that method's own doc comment
	// for exactly where in Authorize's sequence this runs, and why there,
	// not later. Read with the mutex held, the same as autonomy and
	// session, because AddMissionRules (a mid-session §21.6 confirmation
	// dialog, or a resumed session re-applying §21.16 decision 3's own
	// "constraints are re-applied before the first tool call") can run
	// concurrently with an in-flight Authorize call the same way
	// SetAutonomy already can. nil (every pre-Step-31 caller, and every
	// Guard a caller builds without ever calling AddMissionRules) means
	// "no mission constraint": hardDeny's own mission check is then a
	// no-op, so a caller that ignores this field sees no behavior change.
	missionDeny []MissionRule

	// bashScopeAllow is §21.6's second mockup ("Tools for this mission")
	// wired into real enforcement (Step 31, part 7): the bash subcommand
	// prefixes a chosen mission.ToolScope allows, checked once inside
	// hardDeny the same way missionDeny already is (see bashScopeHardDeny's
	// own doc comment for exactly how). nil (the zero value, and every
	// pre-part-7 caller that never calls SetBashScope) means "no scope
	// restriction" -- hardDeny's own bashScopeHardDeny check is then a
	// no-op, so a caller that ignores this field sees no behavior change,
	// matching missionDeny's own "nil means no constraint" contract.
	//
	// Unlike missionDeny, this field is *replaced*, not appended to, by
	// SetBashScope -- see that method's own doc comment for why: a
	// mission's deny rules are meant to accumulate for the life of a
	// session ("a second mission stated later... narrows further"), but a
	// tool scope is re-proposed fresh every time §21.6's second dialog
	// resolves for a new goal, and the mockup's own "Everything installed"
	// option is a real, intentional widening back to no restriction, not
	// a layer-ordering violation -- §21.4's "narrows, never widens" rule
	// governs missionDeny (a stated constraint enforced for the rest of
	// the session), not this per-task proposal a human re-confirms every
	// time it changes.
	bashScopeAllow []string

	// deniedHistory is §21.13's own acceptance-narrative item 10 ("a
	// recently-denied list"), the one piece of that item's four this
	// package did not already have a field for when the read-only
	// /permissions slice landed (Step 32 part 5's own doc comment on
	// permissions.go names this as the one gap left). Appended to by
	// recordDenial, called from every one of Authorize's denial/refusal
	// return points (see that method's own doc comment for exactly
	// which); trimmed to deniedHistoryLimit entries, oldest first
	// dropped, so a long session cannot grow this without bound the way
	// hardDeny's own config-driven lists never need to (those are fixed
	// at construction; this one grows for the life of a Guard). Read
	// with the mutex held, the same as missionDeny/bashScopeAllow/
	// session, since RecentDenials (a future /permissions render) can
	// run concurrently with an in-flight Authorize call the same way
	// every other read-back method here already can.
	deniedHistory []DeniedEntry

	// now stands in for time.Now in tests wanting a fixed clock for
	// DeniedEntry.When -- nil (every real caller, via New) means the
	// real wall clock. The same injectable-clock shape
	// tools.DeclarativeTool.Now and internal/app's own
	// ledgerObservingRunner already use elsewhere in this codebase, so a
	// test can assert an exact timestamp instead of merely "some time
	// close to now".
	now func() time.Time

	mu      sync.Mutex
	session map[string]struct{}
}

// deniedHistoryLimit bounds deniedHistory -- keeping only the most
// recently denied deniedHistoryLimit requests. A "recently-denied" display
// is meant to answer "what has this session refused lately", not to be a
// full audit log (internal/evolve's own ledger already exists for
// long-lived, persisted history; this is deliberately neither persisted
// nor unbounded -- see deniedHistory's own doc comment).
const deniedHistoryLimit = 20

// DeniedEntry is one recorded refusal -- §21.13's own "recently-denied
// list" acceptance-narrative item, made concrete. Tool and Reason mirror
// the exact strings a model or a human would already see (Reason is the
// same text refusal()/hardDeny's own fmt.Errorf calls already produce,
// with the "tool permission denied: " prefix trimmed, since a human
// reading a denial *list* already knows every row on it was a denial —
// repeating that prefix on every line would be noise ToolAuditEntry's own
// fields do not carry either). Tier is included because a human auditing
// "what did this session refuse" cares whether a denial was a Sensitive
// write or a Critical push, not only the tool name. When is recorded with
// g.now() (real wall clock unless a test overrides it), the same
// injectable-clock shape ledgerObservingRunner already established.
type DeniedEntry struct {
	Tool   string
	Reason string
	Tier   Tier
	When   time.Time
}

// recordDenial appends entry to g.deniedHistory, trimming the oldest entry
// off the front once deniedHistoryLimit is exceeded. Called from every one
// of Authorize's own denial/refusal return points (see that method's own
// doc comment for the full list) -- deliberately not centralized inside
// refusal() alone, since two of those points (hardDeny's reason, and
// mode == "deny") return a plain fmt.Errorf and never call refusal() at
// all (see refusal's own doc comment on why those stay "data", not
// "turn-ending" -- a distinction this history does not care about: a
// human auditing what was refused wants every refusal, regardless of
// which of the two kinds it was).
func (g *Guard) recordDenial(tool, reason string, tier Tier) {
	now := g.now
	if now == nil {
		now = time.Now
	}
	entry := DeniedEntry{Tool: tool, Reason: reason, Tier: tier, When: now()}
	g.mu.Lock()
	g.deniedHistory = append(g.deniedHistory, entry)
	if len(g.deniedHistory) > deniedHistoryLimit {
		g.deniedHistory = g.deniedHistory[len(g.deniedHistory)-deniedHistoryLimit:]
	}
	g.mu.Unlock()
}

// RecentDenials reports the requests this Guard has refused, oldest first,
// capped at deniedHistoryLimit -- used by a caller (internal/app's own
// permissionsLister) wanting to display "what has this session refused
// lately" the way §21.13's own acceptance-narrative item 10 names.
// Mirrors MissionRules'/BashScope's own defensive-copy-on-read shape, so a
// caller mutating the returned slice can never corrupt this Guard's own
// state.
func (g *Guard) RecentDenials() []DeniedEntry {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]DeniedEntry(nil), g.deniedHistory...)
}

// MissionRule is one Guard-enforceable constraint compiled from a goal's
// natural language by internal/mission.Compile (§21.6) -- kept as this
// package's own type, not a shared one with internal/mission, so neither
// package has to import the other (§6.1's own boundary: a caller in
// internal/app, the one package already trusted to bridge types across a
// seam, converts a mission.Rule into a MissionRule field-by-field). Only
// Effect == "deny" rules are meant to ever reach AddMissionRules -- see
// that method's own doc comment for why an "allow" mission.Rule is never
// converted into one of these at all.
type MissionRule struct {
	// Capability is "bash" or "fetch" -- the two native tools whose
	// argument can name an arbitrary external technology by string,
	// matching internal/mission.Rule.Capability's own doc comment on why
	// only these two appear in a compiled constraint.
	Capability string
	// Pattern is matched against the command (for "bash") or url (for
	// "fetch") argument using the same matches() glob engine hardDeny
	// already uses for ShellDeny/WriteDeny, so "*playwright*" behaves
	// identically to how a shell_deny pattern already would.
	Pattern string
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

// SetAutonomy sets the autonomy Authorize's own gate (see its doc comment)
// consults, following SetToolTiers' own late-binding shape: a Guard is
// constructed by New before the trust decision (internal/trust, §21.4
// layer 2) or a future /permissions command (Step 32) has necessarily been
// resolved, so this is a second step rather than a constructor argument,
// exactly like SetToolTiers already is. Safe to call while another
// goroutine is inside Authorize, guarded by the same mutex session grants
// use.
func (g *Guard) SetAutonomy(a Autonomy) {
	g.mu.Lock()
	g.autonomy = a
	g.mu.Unlock()
}

// Autonomy reports the autonomy Authorize is currently gating on -- Auto
// (the zero value) for a Guard that never had SetAutonomy called, matching
// every pre-Step-30 caller and test in this package.
func (g *Guard) Autonomy() Autonomy {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.autonomy
}

// AddMissionRules appends rules to the Guard's mission-deny list (§21.4
// layer 4, Step 31), following SetToolTiers'/SetAutonomy's own late-
// binding shape: a Guard exists before a goal has been stated (a mission
// is scoped to one task, not the whole session), so this is a call made
// once §21.6's confirmation dialog resolves, not a constructor argument.
//
// Appends, never replaces -- a second mission stated later in the same
// session (§21.16 decision 3's own "don't touch audio mid-run" narrative
// example) narrows further, it does not un-narrow an earlier constraint,
// matching §21.4's own table: "a lower layer can never widen a higher
// one". There is deliberately no RemoveMissionRules: a mission constraint
// is meant to hold until the session itself ends, the same "sticky until
// something more authoritative changes it" shape autonomy has, not
// something a tool call can quietly undo mid-turn.
//
// This method takes []MissionRule, not a mission.Mission or
// mission.Constraint directly, so this package never imports
// internal/mission (§6.1): the caller (internal/app) is the one place
// that both compiles a goal and holds a *Guard, and it converts the
// "deny"-effect half of a compiled Mission into MissionRule values one
// field at a time before calling this. An "allow"-effect
// mission.Constraint is never converted and never reaches this method at
// all -- §21.6's inverse example ("use Playwright if you think it helps")
// means auto decides freely, which is simply the absence of a deny rule,
// not a second kind of active grant this package would need machinery
// for.
//
// Safe to call while another goroutine is inside Authorize, guarded by
// the same mutex autonomy and session grants already use.
func (g *Guard) AddMissionRules(rules []MissionRule) {
	if len(rules) == 0 {
		return
	}
	g.mu.Lock()
	g.missionDeny = append(g.missionDeny, rules...)
	g.mu.Unlock()
}

// MissionRules reports the mission-deny rules currently in effect -- used
// by a caller wanting to display "no browser · no network" the way
// §21.11's own sub-agent mockup shows it on the children, and by tests
// asserting a dispatched sub-agent's *Guard (the very same pointer
// newSubAgentRunner threads through, see internal/app/dispatch.go's own
// doc comment on why the parent's *permissions.Guard is reused as-is, not
// rebuilt) carries the parent's own mission forward.
func (g *Guard) MissionRules() []MissionRule {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]MissionRule(nil), g.missionDeny...)
}

// SetBashScope sets (replacing any previous value -- see g.bashScopeAllow's
// own doc comment for why replace rather than append) the bash subcommand
// prefixes bash is scoped to for the rest of the session, following
// SetToolTiers'/SetAutonomy's own late-binding shape: a Guard exists before
// §21.6's second dialog has necessarily resolved, so this is a call made
// once resolveToolScope picks an option, not a constructor argument.
//
// allow is a list of bare subcommand names, e.g. []string{"node", "npm",
// "git"} -- exactly mission.ToolScope.BashAllow's own shape, so a caller
// (internal/tui, via a ToolScopeGuard-shaped seam) can pass that field
// straight through with no conversion, the same "the real type already
// fits" shortcut MissionGuard's own doc comment notes for AddMissionRules/
// MissionRules. allow == nil (this method's own zero-value default, and
// what "3. Everything installed" passes -- see resolveToolScope's own
// comment) clears any prior restriction: bashScopeHardDeny's own check
// then never fires, matching this field's "nil means no constraint"
// contract stated on bashScopeAllow above. An empty, non-nil slice
// ([]string{}) is meaningfully different: it would refuse every bash
// invocation not covered by safeBashPrefixes' own escape hatch -- no
// caller in this codebase constructs that today (ProposeTools' own
// BashAllow can never be empty, see its own doc comment), but the
// distinction is preserved rather than collapsed, the same way Go's own
// nil-vs-empty-slice convention already works.
//
// Safe to call while another goroutine is inside Authorize, guarded by the
// same mutex missionDeny/autonomy/session grants already use.
func (g *Guard) SetBashScope(allow []string) {
	g.mu.Lock()
	g.bashScopeAllow = allow
	g.mu.Unlock()
}

// BashScope reports the bash scope currently in effect -- nil when no
// SetBashScope call has narrowed it (or the most recent call passed nil,
// e.g. "Everything installed"). Mirrors MissionRules' own read-back shape,
// for a caller wanting to display the scope currently in force rather than
// only the one last proposed.
func (g *Guard) BashScope() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.bashScopeAllow...)
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
//
// ask_user (§19.1's ninth core tool, §21.16 decision 1) is checked first
// of all, before even the hard denies below: "a policy that could deny it
// would produce an agent that is blocked and cannot say so" is not one
// preference among the gates that follow, it is a property none of them
// may ever contradict -- a WriteDeny pattern that happens to match
// ask_user's own arguments, a Shell config set to "deny", Readonly
// autonomy: none of these may refuse the one tool whose entire effect is
// to stop and hand control back to a human, since refusing it would leave
// the agent with no way to report that it is stuck. This is a dedicated,
// unconditional early return rather than a Tier/mode combination threaded
// through the gates below, because every one of those gates independently
// has a path that can say no, and "never denyable" must not depend on all
// of them individually agreeing to let it through. ask_user is still
// bounded by the loop's own hard cap (§12bis) -- that limit is enforced
// by the engine's own turn loop, not by this function, so a model cannot
// spend an unbounded number of iterations merely asking in circles.
//
// The Readonly gate (Step 30, §21.5's own table) runs first of the
// ordinary gates, right after the hard denies and the configured deny
// mode -- both of those already narrow what any autonomy could otherwise
// permit, so checking Readonly after them, not before, keeps "a lower
// layer can never widen a higher one" true in both directions. Under
// Readonly: Safe still runs (reading cannot damage anything, regardless
// of who is deciding); Controlled asks instead of bypassing review the
// way it does under every other autonomy; Sensitive and Critical are
// refused outright, with no reviewer consulted at all -- §21.5's table
// gives both the same "refuse" cell, and refusing before ever building a
// Request for the reviewer is what makes this a genuinely quieter mode
// for an audit session, not merely a stricter one. Auto and Agile (this
// type's other two values) take neither branch below, which is why a
// Guard nobody ever calls SetAutonomy on -- every pre-Step-30 caller and
// test -- sees no behaviour change: Auto is the zero value.
func (g *Guard) Authorize(ctx context.Context, name string, arguments json.RawMessage) error {
	if name == askUserToolName {
		return nil
	}
	args := clone(arguments)
	req := Request{Name: name, Arguments: args, Tier: g.tierFor(name, args)}
	if reason := g.hardDeny(req); reason != "" {
		g.recordDenial(req.Name, reason, req.Tier)
		return fmt.Errorf("%w: %s", ErrDenied, reason)
	}

	mode := g.mode(req)
	if mode == "deny" {
		reason := req.Name + " is disabled by configuration"
		g.recordDenial(req.Name, reason, req.Tier)
		return fmt.Errorf("%w: %s", ErrDenied, reason)
	}

	autonomy := g.Autonomy()
	if autonomy == Readonly {
		if req.Tier == Sensitive || req.Tier == Critical {
			reason := req.Name + " is refused under read-only autonomy"
			g.recordDenial(req.Name, reason, req.Tier)
			return refusal("%s", reason)
		}
		if req.Tier == Safe {
			return nil
		}
		// Controlled falls through to the reviewer below instead of the
		// Safe/Controlled bypass two lines down -- the one row §21.5's
		// table asks Readonly to ask rather than run or refuse.
	} else if req.Tier == Safe || req.Tier == Controlled {
		return nil
	}

	if g.yolo && req.Tier != Critical && (req.Name == "write_file" || req.Name == "edit_file" || req.Name == "bash") {
		return nil
	}
	if mode == "allow" {
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
		reason := req.Name + " requires interactive approval, and no reviewer is available"
		g.recordDenial(req.Name, reason, req.Tier)
		return refusal("%s", reason)
	}

	decision, err := g.reviewer.Review(ctx, req)
	if err != nil {
		reason := fmt.Sprintf("approval failed: %v", err)
		g.recordDenial(req.Name, reason, req.Tier)
		return refusal("%s", reason)
	}
	if !decision.Allow {
		reason := "user declined " + req.Name
		g.recordDenial(req.Name, reason, req.Tier)
		return refusal("%s", reason)
	}
	if decision.AllowSession && req.Tier == Sensitive && g.permissions.AllowSession {
		g.mu.Lock()
		g.session[key] = struct{}{}
		g.mu.Unlock()
	}
	return nil
}

// askUserToolName is tools.AskUser's own Name() -- duplicated here as a
// bare string, not an import, so this package never depends on
// internal/tools to reason about the one tool whose bypass lives in Go
// rather than in any Tier/mode combination (Authorize's own doc comment
// explains why this must be a dedicated check, not a table entry). The
// same "duplicate a small constant rather than import the other package"
// choice bashCommand's own doc comment already makes for reading bash's
// command argument back out of raw JSON.
const askUserToolName = "ask_user"

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
//
// ask_user (§19.1's ninth core tool, Step 32) is listed here too, for the
// identical reason: a manifest or declarative tool naming itself
// "ask_user" must never be able to claim the Safe tier this function would
// otherwise hand out to it via g.tiers, even though Authorize's own
// unconditional bypass (see that method's doc comment) means this
// function's answer for the real ask_user is never actually consulted in
// practice -- the guarantee is stated here anyway, matching dispatch's own
// "explicit for legibility" precedent for a case its own gate structurally
// cannot reach either.
func isNativeToolName(name string) bool {
	switch name {
	case "read_file", "glob", "grep", "fetch", "write_file", "edit_file", "bash", "dispatch", askUserToolName:
		return true
	default:
		return false
	}
}

// tierFor is the fixed switch over layer 1's nine native tools (the
// original eight of §19.1 plus ask_user, §19.1's one documented
// exception, Step 32) -- kept as a free function, not a method, so
// guard_test.go's existing TestGuardFetchTierIsSafe (calling
// tierFor("fetch") directly) keeps compiling unchanged, and so its own
// contract can never quietly depend on a Guard's tiers map. bash's case
// here is only a fallback -- (*Guard).tierFor never actually reaches it,
// since bash is special-cased there before this function is called at
// all -- but it is kept here (rather than removed) so this switch still
// reads as the complete tool table §19.1 documents.
func tierFor(name string) Tier {
	switch name {
	case "read_file", "glob", "grep", "fetch":
		return Safe
	case askUserToolName:
		// ask_user is always Safe (§21.16 decision 1: "always present,
		// always safe, never denyable") -- Authorize's own unconditional
		// bypass means this case is never actually reached for a real
		// call, the same "never reached, stated anyway" precedent
		// dispatch's own case comment below already sets, kept for the
		// same legibility reason: a reader of this switch should be able
		// to answer "what tier is ask_user" without knowing Authorize
		// short-circuits it earlier.
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

// hardDeny checks, in order: configured write_deny (any tool with a path
// argument), configured shell_deny (bash only), this session's own
// mission-deny rules (§21.4 layer 4, Step 31) for both bash and fetch, then
// (bash only) the currently-chosen tool scope's own bash allow-list (§21.6's
// second dialog, Step 31 part 7). All four run before mode, before the
// Readonly gate, before --yolo, and before the reviewer -- which is what "a
// lower layer can never widen a higher one" (§21.4) actually requires here
// -- but config's own deny lists are checked first because they are a
// project-wide, human-authored floor that should be readable in isolation
// from whatever mission or tool scope happens to be active this task, the
// same way reading this function top to bottom should not require knowing
// whether a mission is even in play. The tool scope check runs last of the
// four because, unlike the other three (all deny-shaped: "refuse if this
// matches"), it is allow-shaped ("refuse unless this matches") -- reading
// it after everything else that can already refuse a bash call keeps this
// function's own shape "add reasons a call is refused", never "compute
// whether it might be allowed", which the first three checks alone already
// establish and the fourth should not have to break.
//
// A sub-agent's *Guard is the very same pointer as its parent's (see
// internal/app/dispatch.go's newSubAgentRunner doc comment), so a mission
// rule appended here is enforced on a child's own tool calls automatically
// -- inheritance is a consequence of pointer identity, not a second
// mechanism that could itself have a bug letting a child widen past its
// parent (§21.11's own "cannot request a capability the parent lacks"
// rule, and layer 1's own identical invariant, both fall out of this for
// free rather than needing their own enforcement path).
func (g *Guard) hardDeny(req Request) string {
	var input struct {
		Path    string `json:"path"`
		Command string `json:"command"`
		URL     string `json:"url"`
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
	if reason := g.missionHardDeny(req.Name, input.Command, input.URL); reason != "" {
		return reason
	}
	if req.Name == "bash" {
		if reason := g.bashScopeHardDeny(input.Command); reason != "" {
			return reason
		}
	}
	return ""
}

// missionHardDeny matches value (the bash command, or the fetch url) for
// name against every mission-deny rule whose Capability equals name,
// reusing the exact glob engine (matches, matchesPath's own sibling)
// hardDeny's config-driven checks above already use, so a mission-compiled
// "*playwright*" pattern behaves identically to a hand-written
// shell_deny entry of the same text -- no second pattern language for a
// human or a test to learn.
func (g *Guard) missionHardDeny(name, command, url string) string {
	g.mu.Lock()
	rules := g.missionDeny
	g.mu.Unlock()
	for _, rule := range rules {
		if rule.Capability != name {
			continue
		}
		value := command
		if name == "fetch" {
			value = url
		}
		if matches(rule.Pattern, value) {
			return fmt.Sprintf("%s matches a mission constraint (%s %s deny)", describeMissionValue(name, value), rule.Capability, rule.Pattern)
		}
	}
	return ""
}

// describeMissionValue names the value hardDeny's mission message quotes,
// kept as its own tiny function only so the message reads the same
// regardless of which capability triggered it ("command %q" for bash,
// "url %q" for fetch) without missionHardDeny's own loop needing an
// if/else at the call site.
func describeMissionValue(name, value string) string {
	if name == "fetch" {
		return fmt.Sprintf("url %q", value)
	}
	return fmt.Sprintf("command %q", value)
}

// bashScopeHardDeny refuses command when a tool scope is in effect
// (g.bashScopeAllow != nil, see that field's own doc comment) and command
// matches none of it. Command is checked, not merely allowed, in three
// escape hatches, each with its own reason:
//
//  1. safeBashPrefixes -- a scope restricts what a mission may *do*, not
//     whether it can look around at all. "ls"/"pwd"/"cat"/"git status" are
//     read-only regardless of which ecosystem a mission is scoped to, and
//     refusing them would make a narrow bash(node, npm, git) scope refuse
//     even the trust-building read-only commands §21.3's own defect 1
//     exists to never prompt for, let alone refuse outright.
//  2. An empty command (blank string) is let through here rather than
//     refused, matching bashTier's own "cannot be sure, do not guess"
//     shape for the identical input -- an empty command reaching this
//     point already failed to parse as anything actionable, and there is
//     nothing this check could meaningfully compare against g.bashScopeAllow.
//  3. hasWordPrefix, the same "cmd is exactly p, or begins with p followed
//     by a space" matcher bashTier's own Safe/Controlled/Critical
//     classification already uses -- so "npm install" matches the "npm"
//     entry the identical way "git status" already matches
//     safeBashPrefixes, and a scope entry never needs its own separate
//     pattern language from the tier classifier already governing the
//     same command.
//
// isCompoundShellCommand is deliberately not consulted here the way
// bashTier's own classification uses it: bashTier disqualifies a compound
// command from Safe/Controlled because its *net effect* cannot be inferred
// from its leading word alone, but a tool scope is checked against the
// *stated* command, not its effect -- "npm install && git push" containing
// "npm" is not evidence the scope was honoured (the same worked example
// hardDeny's own containsAfterMeta catches a hidden "git push" inside),
// so a compound command matching a scope's own leading word is refused all
// the same as a matching bare command unless the whole thing also matches
// (a caller wanting a compound command scoped per-segment the way
// containsAfterMeta does would need bashScopeHardDeny to say so
// explicitly; this pass keeps the check as simple as bashFamily's own
// "generalize over flags only, never positional arguments" scope, not a
// shell parser).
func (g *Guard) bashScopeHardDeny(command string) string {
	g.mu.Lock()
	allow := g.bashScopeAllow
	g.mu.Unlock()
	if allow == nil {
		return ""
	}
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return ""
	}
	if hasWordPrefix(cmd, safeBashPrefixes) {
		return ""
	}
	if hasWordPrefix(cmd, allow) {
		return ""
	}
	return fmt.Sprintf("command %q is outside this mission's tool scope (bash restricted to: %s)", cmd, strings.Join(allow, ", "))
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

// Package mission implements §21.6's constraint compiler: turning a goal
// stated in natural language ("fix the game but no Playwright") into
// permissions.MissionRule values a Guard can enforce in Go, rather than
// leaving the constraint as prompt text a model under pressure can
// rationalize past.
//
// This package is deliberately narrow, the same way safeBashPrefixes in
// internal/permissions/guard.go is "deliberately short and literal, not an
// attempt at a general read-only classifier": Compile recognizes a small,
// documented table of named technologies (knownKeywords below) and two
// small sets of negation/affirmation cue phrases, and returns no
// Constraint at all for a goal it does not recognize a keyword in. It is
// not, and is not trying to be, a general natural-language constraint
// parser — §21.15's own risk table names exactly this failure mode ("The
// constraint compiler mis-parses... show the compiled rule and require
// confirmation; never compile silently"), which is why Compile's result is
// meant to be shown to the human for one-keystroke confirmation before a
// caller ever calls Guard.AddMissionRules with it, not applied blind.
//
// This package stays presentation-free and dependency-free (stdlib only),
// the same rule internal/ask and internal/engine already hold (§6.1): it
// is pure text-in, data-out, so the exact same Compile result can back a
// TUI confirmation dialog, a serve-mode JSON event, or a headless log line
// without duplicating the parsing logic three times.
package mission

import "strings"

// Rule is one compiled permissions constraint, in the same shape
// permissions.MissionRule (internal/permissions/guard.go) mirrors so a
// caller (internal/app, the one package §6.1 already allows to bridge
// config/tui-adjacent types across a package boundary) can convert this
// package's own Rule into a Guard-enforceable one with a single field-by-
// field copy — never a shared type, so internal/mission never needs to
// import internal/permissions and vice versa.
//
// Capability names the tool family the Pattern is matched against: "bash"
// (matched against the command argument) or "fetch" (matched against the
// url argument) — the two native tools whose argument can name an
// arbitrary external technology by string, which is the entire reason a
// constraint like "no Playwright" needs compiling in the first place
// rather than mapping onto a single named tool the way "no dispatch" would
// (dispatch has no such argument to pattern-match; §21.11's own "cannot
// request a capability the parent lacks" rule already bounds it structurally).
type Rule struct {
	Capability string
	Pattern    string
	Effect     string // "deny" or "allow" — see Constraint's own doc comment
}

// Constraint is one compiled clause of a goal: the keyword Compile matched,
// whether the goal negated or affirmed it, and the Rule(s) it compiles to.
//
// Effect on every Rule inside a negated Constraint is "deny" — §21.6's own
// worked example ("no Playwright" compiling to "bash *playwright* deny").
// Effect on every Rule inside a non-negated (affirmed) Constraint is
// "allow" — §21.6's own inverse example ("use Playwright if you think it
// helps" becomes an allow rule and auto decides freely). An "allow" Rule is
// intentionally inert as far as permissions.Guard.AddMissionRules is
// concerned (see that method's own doc comment): it exists so a caller
// (the confirmation dialog) can show the human what was detected and why
// nothing is being restricted, not because Guard has an active override
// mechanism to widen past a project's own configured deny list. Widening
// past config is explicitly out of scope for this step, which is about
// narrowing (denying), the direction §21.6's requirement actually needed.
type Constraint struct {
	Keyword string
	Negated bool
	Rules   []Rule
}

// Mission is a goal plus every Constraint Compile found inside it.
type Mission struct {
	Goal        string
	Constraints []Constraint
}

// HasDeny reports whether m contains at least one negated constraint —
// the signal a caller uses to decide whether §21.6's confirmation dialog
// needs to appear at all ("This dialog is not shown for every task... It
// appears when the goal contains a constraint").
func (m Mission) HasDeny() bool {
	for _, c := range m.Constraints {
		if c.Negated {
			return true
		}
	}
	return false
}

// keywordRules is the fixed, documented table of named technologies this
// compiler recognizes, and what each one compiles to when negated. Every
// entry here is a browser-automation tool or a generic alias for the
// class of them, because that is §21.6's own worked example and the
// clearest case where "no X" needs to reach both a shell invocation
// (`npx playwright test`) and a network fetch (a CDN download of the
// browser binary) to actually be enforced — a rule against bash alone
// would leave the fetch half of "no Playwright" wide open. New keywords
// are meant to be added to this table by name, not by teaching Compile a
// general classifier: that is what keeps a caller able to trust that
// "the compiled rule" shown in the confirmation dialog really is exactly
// what will be enforced, with no hidden generalization behind it.
// Every pattern below uses the doubled-star ("**x**") form, not a single
// star ("*x*"), on purpose: internal/permissions's own matches() function
// (guard.go) gives "*" the same single-path-component meaning
// filepath.Match already does — it does not cross a "/" — while "**"
// crosses any character including "/", exactly the distinction that
// function's own doc comment draws ("the documented ** glob form in
// addition to filepath.Match's single-path-component star"). A real
// invocation this table needs to catch, "npx playwright test
// tests/e2e.spec.ts", contains a "/", and a real fetch URL always does;
// "*playwright*" would silently fail to match either, which is exactly
// the "compiles to nothing" failure §21.15's own risk table warns is
// worse than refusing. This was caught by hand-testing matches() directly
// against a realistic multi-argument command before shipping, not assumed
// from reading matches()'s doc comment alone.
var keywordRules = map[string][]Rule{
	"playwright": {
		{Capability: "bash", Pattern: "**playwright**"},
		{Capability: "fetch", Pattern: "**playwright**"},
	},
	"puppeteer": {
		{Capability: "bash", Pattern: "**puppeteer**"},
		{Capability: "fetch", Pattern: "**puppeteer**"},
	},
	"selenium": {
		{Capability: "bash", Pattern: "**selenium**"},
		{Capability: "fetch", Pattern: "**selenium**"},
	},
	"cypress": {
		{Capability: "bash", Pattern: "**cypress**"},
		{Capability: "fetch", Pattern: "**cypress**"},
	},
	"docker": {
		{Capability: "bash", Pattern: "**docker**"},
	},
	// "browser" is the one generic alias in this table, deliberately
	// expanding to the same fixed named tools above rather than to a
	// broader, harder-to-audit pattern like "bash **chrome**" that would
	// also catch an unrelated "chrome-icon.svg" read. A human saying "no
	// browser" almost always means "no browser automation", and the
	// confirmation dialog shows every one of these lines individually, so
	// nothing here is hidden.
	"browser": {
		{Capability: "bash", Pattern: "**playwright**"},
		{Capability: "bash", Pattern: "**puppeteer**"},
		{Capability: "bash", Pattern: "**selenium**"},
		{Capability: "fetch", Pattern: "**playwright**"},
		{Capability: "fetch", Pattern: "**puppeteer**"},
		{Capability: "fetch", Pattern: "**selenium**"},
	},
}

// negationCues and affirmationCues are the cue phrases Compile looks for
// immediately before a matched keyword to decide polarity. Both lists are
// checked together and whichever phrase occurs closest to (immediately
// before) the keyword wins — see nearestCue's own doc comment — so "use
// the tool, no wait, actually don't use Playwright" resolves on "don't",
// not on the earlier, now-superseded "use".
var negationCues = []string{
	"no ", "not ", "don't ", "do not ", "never ", "without ", "avoid ", "avoid using ", "stop using ", "skip ",
}

var affirmationCues = []string{
	"use ", "using ", "allow ", "feel free to use ", "ok to use ", "okay to use ", "can use ", "may use ", "with ",
}

// Compile scans goal for every keyword in keywordRules and returns the
// Mission of every Constraint it found, in the order their keywords first
// appear in goal. A goal containing no recognized keyword returns a
// Mission with an empty Constraints slice, not an error — most goals
// (§21.13's own narrative: "why does the game stutter?", "fix the
// difficulty") state no constraint at all, and that is the ordinary case,
// not a failure of this function.
func Compile(goal string) Mission {
	lower := strings.ToLower(goal)
	m := Mission{Goal: goal}

	// seen keeps each keyword's first-appearance compile from also
	// firing again if the same word appears twice in one goal ("no
	// Playwright, seriously, no Playwright") — one Constraint per
	// keyword, not one per occurrence, since the compiled Rules would be
	// identical either way and a confirmation dialog showing the same
	// line twice would look like a bug.
	seen := make(map[string]bool, len(keywordRules))

	for {
		keyword, idx := firstKeywordIn(lower, seen)
		if keyword == "" {
			break
		}
		seen[keyword] = true

		negated := nearestCue(lower[:idx]) != affirm
		rules := make([]Rule, len(keywordRules[keyword]))
		effect := "deny"
		if !negated {
			effect = "allow"
		}
		for i, r := range keywordRules[keyword] {
			rules[i] = Rule{Capability: r.Capability, Pattern: r.Pattern, Effect: effect}
		}

		m.Constraints = append(m.Constraints, Constraint{
			Keyword: keyword,
			Negated: negated,
			Rules:   rules,
		})
	}

	return m
}

// firstKeywordIn returns the keyword from keywordRules that appears
// earliest in lower among those not yet in seen, and the byte index of
// that occurrence — so Compile's loop above processes a goal mentioning
// several technologies in the order a human reading the goal would meet
// them, which is also the order §21.6's confirmation dialog should list
// them in.
func firstKeywordIn(lower string, seen map[string]bool) (keyword string, idx int) {
	bestIdx := -1
	for kw := range keywordRules {
		if seen[kw] {
			continue
		}
		if i := strings.Index(lower, kw); i >= 0 {
			if bestIdx == -1 || i < bestIdx {
				bestIdx = i
				keyword = kw
			}
		}
	}
	return keyword, bestIdx
}

// polarity is the two outcomes nearestCue can report, kept as a private
// type rather than a bare bool so the call site (Compile's own "negated :=
// nearestCue(...) != affirm" line) reads as a comparison against a named
// outcome instead of an unexplained boolean.
type polarity int

const (
	negate polarity = iota
	affirm
)

// nearestCue scans prefix (everything in the goal before a matched
// keyword) for the rightmost occurrence of any cue phrase in either
// negationCues or affirmationCues, and reports that phrase's polarity.
// "Rightmost" is what makes proximity the deciding factor: a cue phrase
// far from the keyword is weaker evidence of what the keyword's own
// clause says than one immediately next to it, and a goal can legitimately
// contain both ("I use TypeScript everywhere, but no Playwright" — "use"
// is present yet must not out-vote the much closer "no").
//
// A prefix with no recognized cue phrase at all defaults to negate: a bare
// keyword mention with no framing ("Playwright is flaky today") is treated
// as a constraint to compile and show for confirmation rather than
// silently ignored, matching this package's own doc comment on why Compile
// never applies a rule without a human confirming it first — a false
// positive here costs one dialog the human can dismiss with "3. Just a
// preference"; a false negative would silently fail to compile a real
// constraint, which §21.15's own risk table says is the worse of the two.
func nearestCue(prefix string) polarity {
	bestIdx := -1
	best := negate
	for _, cue := range negationCues {
		if i := strings.LastIndex(prefix, cue); i >= 0 && i > bestIdx {
			bestIdx = i
			best = negate
		}
	}
	for _, cue := range affirmationCues {
		if i := strings.LastIndex(prefix, cue); i >= 0 && i > bestIdx {
			bestIdx = i
			best = affirm
		}
	}
	return best
}

// ToolScope is §21.6's own second dialog ("Tools for this mission")
// compiled ahead of time — the same "compile now, dialog later" split part
// 1 used for the section's first mockup: Compile shipped alone before
// ModeMission's own confirmation dialog followed in a later pass.
// ProposeTools (below) is this dialog's Compile: a pure, stdlib-only
// function turning a goal into exactly the data the mockup's four rows
// need, with no TUI code and no Guard/tools.Registry restriction wired in
// this pass — that wiring, and the dialog itself, are later Step 31
// slices, following the identical order the first mockup's own compiler
// and dialog already did.
//
// This intentionally does not decide *whether* the dialog should open —
// see ProposeTools' own doc comment for why one of the section's other two
// trigger conditions, explicit "/plan", still has no Go concept to compile
// against (the other, "a capability outside current policy", now does:
// OutsidePolicy, added in Step 31 part 8, below). A future caller gates
// opening the dialog on those conditions plus this type's own data, the
// same way checkMission alone (not Compile) decides whether ModeMission
// opens.
type ToolScope struct {
	// Base is every capability proposed regardless of the goal's own
	// content, in the mockup's own listed order minus bash (see BashAllow
	// below for why bash gets its own field): "read", "edit", "dispatch".
	Base []string

	// BashAllow is the subcommand names bash should be scoped to, per the
	// mockup's own "bash(node, npm, git)" row. Detected from
	// bashSubcommandKeywords when the goal names a recognized ecosystem,
	// falling back to defaultBashAllow (see that var's own doc comment)
	// when it names none — never empty, matching the mockup's own worked
	// example, which never shows a bare "bash" with no subcommands at
	// all.
	BashAllow []string

	// BrowserOffered reports whether option 2 ("Proposed + browser")
	// should appear at all — see wantsBrowserOffer's own doc comment for
	// the exact three cases (no browser keyword, a negated one, an
	// affirmed one) and why only the last suppresses the offer.
	BrowserOffered bool

	// BrowserWeightMB is §21.6's own mockup figure, verbatim ("~180 MB
	// download; your phone will struggle") — a fixed, documented
	// constant, not a computed size. This package has no tool-weight
	// data model to compute a real figure from (searched: no
	// size_mb/SizeMB/download_size concept exists anywhere in this
	// codebase yet), and inventing one to back a single warning line
	// would itself be the kind of "the compiled rule differs from what
	// will actually be enforced" mismatch §21.15's own risk table warns
	// against for the *other* mockup — showing the plan's own documented
	// number is honest about what this pass actually knows. A later pass
	// wiring this to a real tools directory can compute the true figure
	// per-install and replace this constant with that lookup without
	// changing ToolScope's own shape.
	BrowserWeightMB int
}

// baseCapabilities is every capability every mission proposes regardless
// of the goal's own content, in the mockup's own listed order: read,
// edit, dispatch. bash is deliberately not a fourth entry here — unlike
// these three, a bash grant is meaningfully more useful scoped to a
// subcommand list than left as a bare on/off entry, which is why it gets
// its own field (ToolScope.BashAllow) instead of a place in this slice.
var baseCapabilities = []string{"read", "edit", "dispatch"}

// bashSubcommandKeywords is this dialog's own small, documented keyword
// table — the same discipline keywordRules above states for itself: a
// small, named table a human confirming the dialog can trust completely,
// not an attempt at a general language-detection classifier. Multiple
// keywords may map to the same subcommand (aliases for one ecosystem);
// bashAllowFor's own fixed iteration order (not this map's) is what keeps
// the rendered row deterministic despite Go's randomized map order.
var bashSubcommandKeywords = map[string]string{
	"node.js":    "node",
	"nodejs":     "node",
	"node":       "node",
	"javascript": "node",
	"typescript": "node",
	"npm":        "npm",
	"python3":    "python",
	"python":     "python",
	"pip":        "pip",
	"golang":     "go",
	"rust":       "cargo",
	"cargo":      "cargo",
	"git":        "git",
}

// bashAllowOrder is bashSubcommandKeywords' own subcommand values, fixed
// so bashAllowFor's result reads the same way every run for the identical
// goal — Go map iteration order is deliberately randomized, and this
// dialog's own row must not reshuffle between two runs over one unchanged
// goal.
var bashAllowOrder = []string{"node", "npm", "python", "pip", "go", "cargo", "git"}

// defaultBashAllow is §21.6's own mockup's literal worked answer for its
// one worked example ("fix orbital-dash"): bash(node, npm, git).
// bashAllowFor falls back to exactly this when the goal names none of
// bashSubcommandKeywords' own entries — the same "a goal with no
// recognized keyword compiles to the ordinary case, not an error"
// contract Compile's own doc comment states for itself, applied here as
// "the ordinary case is this project's own most common stack", which is
// what the mockup's own worked example already assumes without its goal
// text ever spelling out "Node.js" anywhere in it.
var defaultBashAllow = []string{"node", "npm", "git"}

// browserWeightMB backs ToolScope.BrowserWeightMB — see that field's own
// doc comment for why this is a fixed, documented constant rather than a
// computed size.
const browserWeightMB = 180

// browserKeywords is the subset of keywordRules' own keys that actually
// name browser-automation technologies — every one of them, today, since
// this package's own file comment states keywordRules currently holds
// only "a browser-automation tool or a generic alias for the class of
// them" — except that is not quite true: "docker" is also a keywordRules
// entry and is not browser automation at all. wantsBrowserOffer needs the
// narrower, correct set (this var), not keywordRules' own full key list,
// so that a goal saying "use Docker" is never mistaken for one that
// already granted browser automation.
var browserKeywords = map[string]bool{
	"playwright": true,
	"puppeteer":  true,
	"selenium":   true,
	"cypress":    true,
	"browser":    true,
}

// ProposeTools compiles a stated goal into §21.6's second mockup ("Tools
// for this mission"): option 1's own proposed capability set (Base plus
// BashAllow), and whether option 2 ("Proposed + browser") should be
// offered at all. Like Compile, this is pure and dependency-free.
//
// This is deliberately the compiler alone — see ToolScope's own doc
// comment for what is intentionally still missing: the dialog itself, and
// wiring its outcome to a real Guard/tools.Registry restriction. Of
// §21.6's three stated trigger conditions for the dialog opening at all,
// explicit "/plan" is still out of scope for this pass (not yet a real
// slash.Kind, per docs/PLAN.md's own Step 32 row); "a capability outside
// current policy" now has a Go concept to compile against — OutsidePolicy,
// added in Step 31 part 8, below — but is not consulted here, since it
// answers a different question (whether the dialog should open at all,
// not what it should propose) the same way HasDeny() answers it for the
// first mockup without Compile itself calling HasDeny(). ProposeTools
// therefore only answers "what would auto propose from this goal's own
// text", leaving "should the dialog even open" — every one of the three
// triggers, "the goal contains a constraint" (checkMission's own
// Compile(text).HasDeny()) included — to a future caller, the same way
// this package's own Compile never decided whether ModeMission should
// open.
func ProposeTools(goal string) ToolScope {
	lower := strings.ToLower(goal)
	return ToolScope{
		Base:            append([]string(nil), baseCapabilities...),
		BashAllow:       bashAllowFor(lower),
		BrowserOffered:  wantsBrowserOffer(Compile(goal)),
		BrowserWeightMB: browserWeightMB,
	}
}

// bashAllowFor returns every bashSubcommandKeywords match found in lower,
// deduplicated (a goal naming both "javascript" and "typescript" proposes
// "node" once, not twice) and ordered by bashAllowOrder rather than by
// where each keyword appears in the goal — unlike Compile's own
// appearance-order Constraints, this is an unordered *set* being rendered
// as a fixed list, so a stable, goal-independent order is what keeps it
// legible. Falls back to defaultBashAllow when the goal names none of the
// table's keywords at all.
func bashAllowFor(lower string) []string {
	seen := make(map[string]bool, len(bashAllowOrder))
	for kw, sub := range bashSubcommandKeywords {
		if strings.Contains(lower, kw) {
			seen[sub] = true
		}
	}
	if len(seen) == 0 {
		return append([]string(nil), defaultBashAllow...)
	}
	out := make([]string, 0, len(seen))
	for _, sub := range bashAllowOrder {
		if seen[sub] {
			out = append(out, sub)
		}
	}
	return out
}

// wantsBrowserOffer reports whether ProposeTools should offer option 2
// ("Proposed + browser") for a goal that already compiled to m. It reuses
// Compile itself instead of re-matching browserKeywords' own entries a
// second time against the raw goal text, so this dialog and the first
// mockup's own confirmation dialog can never disagree about what counts
// as "the goal already talked about browser automation" — an independent
// second scan could drift from Compile's own table the moment either one
// gains a new entry, exactly the kind of silent divergence a single
// shared source of truth cannot suffer from.
//
// True whenever the goal mentions no recognized browser-automation
// keyword at all (the ordinary case: nothing was said either way, so
// option 2 is a plain widen-if-you-want offer) or mentions one negated
// (the goal already denies it via a mission constraint — option 2 shows
// this as what auto "considered and rejected", the exact §21.6 line: "the
// reason shown"). False only when the goal already affirms a recognized
// browser-automation keyword ("use Playwright") — Compile's own Rule for
// that case already carries effect "allow", so browser automation is
// already part of what auto is proposing and there is nothing left for
// option 2 to add. A goal affirming an unrelated keywordRules entry
// (e.g. "use Docker") must not suppress the offer — see browserKeywords'
// own doc comment for why this checks that narrower set, not
// keywordRules' full key list.
func wantsBrowserOffer(m Mission) bool {
	for _, c := range m.Constraints {
		if browserKeywords[c.Keyword] && !c.Negated {
			return false
		}
	}
	return true
}

// Policy is the subset of a project's own configured permissions
// (config.Permissions, internal/config/schema.go) OutsidePolicy (below)
// needs to detect §21.6's second dialog-opening trigger: "the mission
// requests a capability outside current policy". Kept as this package's
// own small mirror type, not a shared one with config.Permissions, for
// the identical §6.1 reason Rule mirrors permissions.MissionRule instead
// of importing it (see Rule's own doc comment): internal/mission never
// imports internal/config or internal/permissions, and a caller
// (internal/app, the one package already trusted to bridge config/tui-
// adjacent types across this seam — see denyRulesOf's own doc comment for
// the identical bridge already built for MissionRule) converts a real
// config.Permissions into this shape with a field-by-field copy.
type Policy struct {
	// ShellAllowed is config.Permissions.Shell != "deny" -- whether bash
	// may run at all under this project's own configured policy, a
	// question entirely independent of any mission or autonomy layer
	// above it (§21.4's layer 5 sits below layer 4's mission, and layer 4
	// sits below nothing this package can see, so a project that has
	// turned bash off entirely makes *any* bash-shaped affirmation
	// "outside policy" regardless of which technology was named).
	ShellAllowed bool

	// ShellDeny mirrors config.Permissions.ShellDeny verbatim -- patterns
	// a bash command may never match regardless of any approval (§19.8).
	// A goal affirming a technology whose own compiled bash Rule names
	// something already forbidden here is asking for exactly what layer
	// 5 already refuses, which is what "outside current policy" means
	// for this pass: not a new policy concept, but a mission's own stated
	// intent already colliding with one that exists today.
	ShellDeny []string
}

// keywordName strips keywordRules' own doubled-star bracketing
// ("**playwright**" -> "playwright") from a compiled Rule's own Pattern,
// so OutsidePolicy can compare what a goal affirms against a project's
// own free-text ShellDeny patterns by the plain word they both ultimately
// name, without this package needing to know shell_deny's own glob syntax
// at all -- permissions.matches' glob engine (internal/permissions/
// guard.go) lives only in that package, kept out of this one by the
// identical §6.1 boundary Rule's own doc comment already states. This is
// a small, literal string trim, not a parser: keywordRules' own patterns
// are the only Pattern values this function is ever asked to strip, and
// every one of them is already known, by construction, to be wrapped in
// exactly this "**word**" shape (see keywordRules' own doc comment on why
// the doubled-star form was chosen).
func keywordName(pattern string) string {
	return strings.Trim(pattern, "*")
}

// OutsidePolicy reports whether goal affirms (does not negate) a
// recognized keywordRules technology that collides with policy: either
// policy.ShellAllowed is false (bash is off project-wide, so any
// affirmed bash-shaped capability is outside policy by definition), or
// the technology's own name already appears in one of policy.ShellDeny's
// patterns (the project has already, separately, forbidden exactly what
// this goal is now asking auto to use freely).
//
// This is §21.6's second stated dialog-opening trigger ("the mission
// requests a capability outside current policy") — the one condition
// ProposeTools' own doc comment, and every earlier Step 31 part's own
// "still missing" list, named as having no Go concept to compile against
// yet. Policy (above) is that concept: "current policy" means
// config.Permissions' own Shell mode and ShellDeny list, bridged into
// Policy by a caller the same way denyRulesOf already bridges
// permissions.MissionRule.
//
// Only affirmed constraints are checked, never negated ones: a goal that
// already says "no Playwright" has nothing left to flag here — checkMission's
// own HasDeny() trigger already opens a dialog for it, and this function
// firing too would mean two dialogs opening in a row for the exact same
// recognized word, which is worse than either alone (§21.15's own
// "death by a thousand dialogs" risk, applied to this trigger
// specifically). A goal Compile does not recognize at all (zero
// Constraints) can never trigger this either — the identical "no
// keyword, no dialog" contract every earlier compiler in this package
// already follows, applied here rather than invented anew.
//
// Like Compile and ProposeTools, this is the compiler alone: it answers
// "does this goal collide with policy", not "which dialog should open,
// and with what message" — that wiring (a caller checking this alongside
// checkMission's own HasDeny() check) is a later Step 31 slice, following
// the identical "compile now, dialog later" order parts 1→2 and 5→6
// already used for this section's other two triggers.
func OutsidePolicy(goal string, policy Policy) bool {
	m := Compile(goal)
	for _, c := range m.Constraints {
		if c.Negated {
			continue
		}
		for _, r := range c.Rules {
			if r.Capability != "bash" {
				continue
			}
			if !policy.ShellAllowed {
				return true
			}
			name := keywordName(r.Pattern)
			for _, deny := range policy.ShellDeny {
				if strings.Contains(strings.ToLower(deny), name) {
					return true
				}
			}
		}
	}
	return false
}

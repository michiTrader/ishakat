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

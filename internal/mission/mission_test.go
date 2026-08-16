package mission

import (
	"reflect"
	"testing"
)

// TestCompileNoPlaywrightProducesDenyRules is §21.6's own worked example,
// verbatim: "fix orbital-dash... no Playwright" must compile to exactly the
// two deny rules the mockup in docs/PLAN.md §21.6 shows (bash and fetch,
// both **playwright**), and nothing else.
func TestCompileNoPlaywrightProducesDenyRules(t *testing.T) {
	m := Compile("fix orbital-dash performance properly, no Playwright")
	if !m.HasDeny() {
		t.Fatal("HasDeny() = false, want true")
	}
	if len(m.Constraints) != 1 {
		t.Fatalf("Constraints = %d, want 1: %+v", len(m.Constraints), m.Constraints)
	}
	c := m.Constraints[0]
	if c.Keyword != "playwright" || !c.Negated {
		t.Fatalf("constraint = %+v, want keyword=playwright negated=true", c)
	}
	want := []Rule{
		{Capability: "bash", Pattern: "**playwright**", Effect: "deny"},
		{Capability: "fetch", Pattern: "**playwright**", Effect: "deny"},
	}
	if len(c.Rules) != len(want) {
		t.Fatalf("Rules = %+v, want %+v", c.Rules, want)
	}
	for i, r := range c.Rules {
		if r != want[i] {
			t.Errorf("Rules[%d] = %+v, want %+v", i, r, want[i])
		}
	}
}

// TestCompileInverseProducesAllowRule is §21.6's own stated inverse: "use
// Playwright if you think it helps" becomes an allow rule, not a deny one.
func TestCompileInverseProducesAllowRule(t *testing.T) {
	m := Compile("use Playwright if you think it helps")
	if m.HasDeny() {
		t.Fatal("HasDeny() = true, want false (this goal affirms, not denies)")
	}
	if len(m.Constraints) != 1 {
		t.Fatalf("Constraints = %d, want 1", len(m.Constraints))
	}
	c := m.Constraints[0]
	if c.Negated {
		t.Fatal("Negated = true, want false")
	}
	for _, r := range c.Rules {
		if r.Effect != "allow" {
			t.Errorf("rule effect = %q, want allow: %+v", r.Effect, r)
		}
	}
}

// TestCompilePlainGoalHasNoConstraints is §21.6's own "this dialog is not
// shown for every task" contract: a goal like the acceptance narrative's
// "why does the game stutter?" must compile to zero constraints, not a
// false positive.
func TestCompilePlainGoalHasNoConstraints(t *testing.T) {
	m := Compile("why does the game stutter?")
	if len(m.Constraints) != 0 {
		t.Fatalf("Constraints = %+v, want none", m.Constraints)
	}
	if m.HasDeny() {
		t.Fatal("HasDeny() = true, want false")
	}
}

// TestCompileNearestCueWins covers the proximity rule nearestCue's own doc
// comment describes: an earlier affirmation must not out-vote a later,
// closer negation for the same keyword.
func TestCompileNearestCueWins(t *testing.T) {
	m := Compile("I use TypeScript everywhere, but no Playwright please")
	if len(m.Constraints) != 1 {
		t.Fatalf("Constraints = %+v, want exactly 1 (typescript is not a known keyword)", m.Constraints)
	}
	if !m.Constraints[0].Negated {
		t.Fatalf("constraint = %+v, want negated=true (closer cue is 'no')", m.Constraints[0])
	}
}

// TestCompileMultipleKeywordsOrderedByAppearance covers a goal naming two
// different technologies with different polarity, in the order they
// appear — the order §21.6's own confirmation dialog should list them in.
func TestCompileMultipleKeywordsOrderedByAppearance(t *testing.T) {
	m := Compile("no docker, but use cypress for the e2e tests")
	if len(m.Constraints) != 2 {
		t.Fatalf("Constraints = %+v, want 2", m.Constraints)
	}
	if m.Constraints[0].Keyword != "docker" || !m.Constraints[0].Negated {
		t.Errorf("Constraints[0] = %+v, want docker negated", m.Constraints[0])
	}
	if m.Constraints[1].Keyword != "cypress" || m.Constraints[1].Negated {
		t.Errorf("Constraints[1] = %+v, want cypress affirmed", m.Constraints[1])
	}
}

// TestCompileBrowserAliasExpandsToNamedTools covers the one generic alias
// in keywordRules: "no browser" must expand to the same three named
// automation tools' patterns, not to a single broad "**browser**" pattern
// that would also match unrelated strings like "chrome-icon.svg".
func TestCompileBrowserAliasExpandsToNamedTools(t *testing.T) {
	m := Compile("iterate on the game, no browser stuff")
	if len(m.Constraints) != 1 {
		t.Fatalf("Constraints = %+v, want 1", m.Constraints)
	}
	c := m.Constraints[0]
	if c.Keyword != "browser" || !c.Negated {
		t.Fatalf("constraint = %+v, want keyword=browser negated=true", c)
	}
	if len(c.Rules) != 6 {
		t.Fatalf("Rules = %+v, want 6 (3 bash + 3 fetch)", c.Rules)
	}
	for _, r := range c.Rules {
		if r.Pattern == "**browser**" {
			t.Errorf("rule pattern is the generic **browser**, want a named tool: %+v", r)
		}
	}
}

// TestCompileNoRecognizedKeywordDefaultsToNegate covers nearestCue's own
// "a bare keyword mention with no framing... is treated as a constraint to
// compile and show for confirmation rather than silently ignored" rule.
func TestCompileNoRecognizedKeywordDefaultsToNegate(t *testing.T) {
	m := Compile("Playwright is flaky today, investigate")
	if len(m.Constraints) != 1 {
		t.Fatalf("Constraints = %+v, want 1", m.Constraints)
	}
	if !m.Constraints[0].Negated {
		t.Fatal("Negated = false, want true (default polarity with no cue phrase)")
	}
}

// TestProposeToolsWorkedExampleMatchesTheMockupVerbatim is §21.6's own
// second mockup's worked example: "fix orbital-dash" (no ecosystem
// keyword named at all) must propose exactly "read · edit · bash(node,
// npm, git) · dispatch", the mockup's own literal row 1 text.
func TestProposeToolsWorkedExampleMatchesTheMockupVerbatim(t *testing.T) {
	got := ProposeTools("fix orbital-dash performance properly")
	wantBase := []string{"read", "edit", "dispatch"}
	if !reflect.DeepEqual(got.Base, wantBase) {
		t.Errorf("Base = %v, want %v", got.Base, wantBase)
	}
	wantBash := []string{"node", "npm", "git"}
	if !reflect.DeepEqual(got.BashAllow, wantBash) {
		t.Errorf("BashAllow = %v, want %v (the mockup's own default)", got.BashAllow, wantBash)
	}
	if !got.BrowserOffered {
		t.Error("BrowserOffered = false, want true (goal never mentions browser automation)")
	}
	if got.BrowserWeightMB != 180 {
		t.Errorf("BrowserWeightMB = %d, want 180 (the mockup's own figure)", got.BrowserWeightMB)
	}
}

// TestProposeToolsDetectsNamedEcosystem covers bashAllowFor's own keyword
// table: a goal naming Python's own ecosystem must propose "python" (and
// "pip" if pip itself is named), not silently fall back to the Node.js
// default meant for goals naming no ecosystem at all.
func TestProposeToolsDetectsNamedEcosystem(t *testing.T) {
	got := ProposeTools("fix the flaky pip install in this Python script")
	want := []string{"python", "pip"}
	if !reflect.DeepEqual(got.BashAllow, want) {
		t.Errorf("BashAllow = %v, want %v", got.BashAllow, want)
	}
}

// TestProposeToolsDeduplicatesAliasedKeywords covers bashAllowFor's own
// "javascript" and "typescript" both meaning "node" — a goal naming both
// must propose "node" once, not twice.
func TestProposeToolsDeduplicatesAliasedKeywords(t *testing.T) {
	got := ProposeTools("migrate this TypeScript file, it started as plain JavaScript")
	want := []string{"node"}
	if !reflect.DeepEqual(got.BashAllow, want) {
		t.Errorf("BashAllow = %v, want %v (deduplicated, not doubled)", got.BashAllow, want)
	}
}

// TestProposeToolsBrowserOfferedWhenGoalIsSilentOnIt is the ordinary case:
// a goal that never mentions browser automation at all still offers
// option 2, since nothing has ruled it in or out yet.
func TestProposeToolsBrowserOfferedWhenGoalIsSilentOnIt(t *testing.T) {
	got := ProposeTools("why does the game stutter?")
	if !got.BrowserOffered {
		t.Error("BrowserOffered = false, want true (goal says nothing about browser automation)")
	}
}

// TestProposeToolsBrowserOfferedWhenGoalDeniesIt covers §21.6's own "what
// auto considered and rejected, with the reason shown" line: a goal that
// already denies Playwright still offers option 2 — the offer itself IS
// the surfaced reasoning, not something a denial should hide.
func TestProposeToolsBrowserOfferedWhenGoalDeniesIt(t *testing.T) {
	got := ProposeTools("fix orbital-dash, no playwright")
	if !got.BrowserOffered {
		t.Error("BrowserOffered = false, want true (a denial is shown, not hidden)")
	}
}

// TestProposeToolsBrowserNotOfferedWhenGoalAlreadyAffirmsIt covers the one
// case ProposeTools' own doc comment names as correctly suppressing the
// offer: a goal that already says "use Playwright" already has browser
// automation in its proposed set (via Compile's own "allow" rule), so
// option 2 has nothing left to add.
func TestProposeToolsBrowserNotOfferedWhenGoalAlreadyAffirmsIt(t *testing.T) {
	got := ProposeTools("use Playwright if you think it helps")
	if got.BrowserOffered {
		t.Error("BrowserOffered = true, want false (goal already affirms Playwright)")
	}
}

// TestProposeToolsBrowserOfferedForAnUnrelatedAffirmedKeyword pins
// browserKeywords' own narrower set against keywordRules' full one: a
// goal affirming "docker" (a keywordRules entry that is not browser
// automation) must not be mistaken for one that already granted browser
// automation.
func TestProposeToolsBrowserOfferedForAnUnrelatedAffirmedKeyword(t *testing.T) {
	got := ProposeTools("use Docker for the build")
	if !got.BrowserOffered {
		t.Error("BrowserOffered = false, want true (docker is not browser automation)")
	}
}

// TestOutsidePolicyTrueWhenShellIsDeniedEntirely covers Policy.ShellAllowed
// == false: a goal affirming a recognized bash-shaped technology is
// outside policy regardless of which one it names, because the project
// has turned bash off entirely.
func TestOutsidePolicyTrueWhenShellIsDeniedEntirely(t *testing.T) {
	got := OutsidePolicy("use Playwright if you think it helps", Policy{ShellAllowed: false})
	if !got {
		t.Error("OutsidePolicy = false, want true (shell is not allowed at all)")
	}
}

// TestOutsidePolicyTrueWhenAffirmedKeywordMatchesShellDeny covers the
// "current policy" half of the trigger: a goal affirming a technology
// whose own name already appears in the project's own configured
// shell_deny list is asking for exactly what layer 5 already refuses.
func TestOutsidePolicyTrueWhenAffirmedKeywordMatchesShellDeny(t *testing.T) {
	got := OutsidePolicy("use Playwright if you think it helps", Policy{
		ShellAllowed: true,
		ShellDeny:    []string{"**playwright**"},
		FetchAllowed: true,
	})
	if !got {
		t.Error("OutsidePolicy = false, want true (playwright already matches shell_deny)")
	}
}

// TestOutsidePolicyFalseWhenNothingCollides is the ordinary case: shell and
// fetch are both allowed, nothing in ShellDeny names the affirmed
// technology, so the goal's own stated intent does not collide with
// anything configured.
func TestOutsidePolicyFalseWhenNothingCollides(t *testing.T) {
	got := OutsidePolicy("use Playwright if you think it helps", Policy{
		ShellAllowed: true,
		ShellDeny:    []string{"**rm -rf**"},
		FetchAllowed: true,
	})
	if got {
		t.Error("OutsidePolicy = true, want false (nothing configured collides with playwright)")
	}
}

// TestOutsidePolicyFalseForANegatedConstraint covers the "do not double up
// with checkMission's own dialog" rule: a goal that already denies a
// keyword has nothing left to flag here, even under a policy that would
// otherwise flag it, because HasDeny() already opens a dialog for it.
func TestOutsidePolicyFalseForANegatedConstraint(t *testing.T) {
	got := OutsidePolicy("fix orbital-dash, no playwright", Policy{ShellAllowed: false})
	if got {
		t.Error("OutsidePolicy = true, want false (a negated constraint is checkMission's own trigger, not this one)")
	}
}

// TestOutsidePolicyFalseForAPlainGoalWithNoRecognizedKeyword covers the
// "no keyword, no dialog" contract every earlier compiler in this package
// already follows: a goal Compile does not recognize at all can never
// collide with policy, regardless of how restrictive that policy is.
func TestOutsidePolicyFalseForAPlainGoalWithNoRecognizedKeyword(t *testing.T) {
	got := OutsidePolicy("why does the game stutter?", Policy{ShellAllowed: false})
	if got {
		t.Error("OutsidePolicy = true, want false (no recognized keyword to collide with anything)")
	}
}

// TestOutsidePolicyIgnoresUnrelatedShellDenyEntries confirms a ShellDeny
// list is checked for the affirmed keyword's own name specifically, not
// treated as "any non-empty ShellDeny means outside policy": a project
// that already forbids something unrelated (e.g. "rm -rf") must not make
// every other affirmed technology look like a collision by accident.
func TestOutsidePolicyIgnoresUnrelatedShellDenyEntries(t *testing.T) {
	got := OutsidePolicy("use Docker for the build", Policy{
		ShellAllowed: true,
		ShellDeny:    []string{"**rm -rf**", "**curl**"},
	})
	if got {
		t.Error("OutsidePolicy = true, want false (docker matches neither configured deny pattern)")
	}
}

// TestOutsidePolicyTrueWhenFetchIsDeniedEntirely covers Policy.FetchAllowed
// == false, the fetch-capability half of the collision check: a goal
// affirming a technology whose keywordRules entry compiles a fetch Rule
// (playwright does) is outside policy when fetch is off project-wide,
// mirroring ShellAllowed's own "off entirely" reasoning for bash.
func TestOutsidePolicyTrueWhenFetchIsDeniedEntirely(t *testing.T) {
	got := OutsidePolicy("use Playwright if you think it helps", Policy{
		ShellAllowed: true,
		FetchAllowed: false,
	})
	if !got {
		t.Error("OutsidePolicy = false, want true (fetch is not allowed at all)")
	}
}

// TestOutsidePolicyFalseWhenBothCapabilitiesAllowed is the ordinary case
// for the fetch half: both bash and fetch are allowed project-wide, so an
// affirmed keyword with both capability Rules does not collide with
// anything.
func TestOutsidePolicyFalseWhenBothCapabilitiesAllowed(t *testing.T) {
	got := OutsidePolicy("use Playwright if you think it helps", Policy{
		ShellAllowed: true,
		FetchAllowed: true,
	})
	if got {
		t.Error("OutsidePolicy = true, want false (both bash and fetch are allowed)")
	}
}

// TestOutsidePolicyFetchDeniedDoesNotFlagABashOnlyKeyword confirms
// FetchAllowed is only consulted for a Rule whose own Capability is
// "fetch": "docker" compiles to a bash-only Rule (keywordRules has no
// fetch entry for it), so turning fetch off entirely must not make an
// affirmed "docker" look like a collision it never asked about.
func TestOutsidePolicyFetchDeniedDoesNotFlagABashOnlyKeyword(t *testing.T) {
	got := OutsidePolicy("use Docker for the build", Policy{
		ShellAllowed: true,
		FetchAllowed: false,
	})
	if got {
		t.Error("OutsidePolicy = true, want false (docker has no fetch-capability Rule to collide with FetchAllowed)")
	}
}

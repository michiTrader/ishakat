package mission

import "testing"

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

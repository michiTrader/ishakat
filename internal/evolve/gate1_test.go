package evolve

import (
	"strings"
	"testing"
)

func containsReason(v Verdict, substr string) bool {
	for _, r := range v.Reasons {
		if strings.Contains(r, substr) {
			return true
		}
	}
	return false
}

func TestAgentInitiatedBelowMinRepeatsIsBlocked(t *testing.T) {
	v := Evaluate(DefaultThresholds(), Candidate{
		Name: "bybit_balance", Description: "check bybit balance",
		Origin: OriginAgent, Repetitions: 2,
	}, nil)
	if v.Allowed {
		t.Fatal("expected the proposal to be blocked below MinRepeats")
	}
	if !containsReason(v, "repetition") {
		t.Errorf("expected a repetition reason, got %v", v.Reasons)
	}
}

func TestAgentInitiatedAtMinRepeatsPasses(t *testing.T) {
	v := Evaluate(DefaultThresholds(), Candidate{
		Name: "bybit_balance", Description: "check bybit balance",
		Origin: OriginAgent, Repetitions: 3, ExpectedUses: 0,
	}, nil)
	if !v.Allowed {
		t.Fatalf("expected the proposal to pass at exactly MinRepeats, got reasons: %v", v.Reasons)
	}
}

func TestUserDeclaredSkipsRepetitionEvidence(t *testing.T) {
	// §19.6: "your stated intent is the evidence" -- Repetitions is 0 and
	// that must not fail the proposal for OriginUserDeclared.
	v := Evaluate(DefaultThresholds(), Candidate{
		Name: "bybit_prices", Description: "check bybit prices daily",
		Origin: OriginUserDeclared, Repetitions: 0,
	}, nil)
	if !v.Allowed {
		t.Fatalf("expected a user-declared proposal to bypass repetition evidence, got: %v", v.Reasons)
	}
}

func TestUserForcedSkipsRepetitionAndStability(t *testing.T) {
	v := Evaluate(DefaultThresholds(), Candidate{
		Name: "weird_tool", Description: "does something unusual",
		Origin: OriginUserForced, Repetitions: 0, VaryingArgs: 99,
	}, nil)
	if !v.Allowed {
		t.Fatalf("expected a forced proposal to bypass repetition and stability, got: %v", v.Reasons)
	}
}

func TestNoDuplicateBlocksNearIdenticalDescription(t *testing.T) {
	existing := []ExistingTool{
		{Name: "git_status_short", Description: "show abbreviated git status output"},
	}
	v := Evaluate(DefaultThresholds(), Candidate{
		Name: "git_status_brief", Description: "show abbreviated git status output",
		Origin: OriginUserForced,
	}, existing)
	if v.Allowed {
		t.Fatal("expected a near-identical description to be flagged as a duplicate")
	}
	if !containsReason(v, "duplicate") {
		t.Errorf("expected a duplicate reason, got %v", v.Reasons)
	}
}

func TestNoDuplicateAllowsDistinctTool(t *testing.T) {
	existing := []ExistingTool{
		{Name: "git_status_short", Description: "show abbreviated git status output"},
	}
	v := Evaluate(DefaultThresholds(), Candidate{
		Name: "bybit_balance", Description: "fetch account balance from bybit",
		Origin: OriginUserForced,
	}, existing)
	if !v.Allowed {
		t.Fatalf("expected a genuinely distinct tool to pass dedup, got: %v", v.Reasons)
	}
}

func TestDedupAppliesRegardlessOfOrigin(t *testing.T) {
	// Dedup and Budget are structural facts about the system, not
	// evidence of need -- §19.6 -- so even a forced proposal must not
	// bypass them.
	existing := []ExistingTool{
		{Name: "git_status_short", Description: "show abbreviated git status output"},
	}
	for _, origin := range []Origin{OriginAgent, OriginUserDeclared, OriginUserForced} {
		v := Evaluate(DefaultThresholds(), Candidate{
			Name: "git_status_brief", Description: "show abbreviated git status output",
			Origin: origin, Repetitions: 10,
		}, existing)
		if v.Allowed {
			t.Errorf("origin %v: expected dedup to block a near-identical tool regardless of origin", origin)
		}
	}
}

func TestStabilityBlocksTooManyVaryingArgs(t *testing.T) {
	v := Evaluate(DefaultThresholds(), Candidate{
		Name: "generic_runner", Description: "runs anything",
		Origin: OriginAgent, Repetitions: 5, VaryingArgs: 5,
	}, nil)
	if v.Allowed {
		t.Fatal("expected 5 varying args (over the default ceiling of 4) to fail stability")
	}
	if !containsReason(v, "stability") {
		t.Errorf("expected a stability reason, got %v", v.Reasons)
	}
}

func TestStabilityAtCeilingPasses(t *testing.T) {
	v := Evaluate(DefaultThresholds(), Candidate{
		Name: "flexible_tool", Description: "some flexible workflow",
		Origin: OriginAgent, Repetitions: 5, VaryingArgs: 4,
	}, nil)
	if !v.Allowed {
		t.Fatalf("expected exactly the ceiling (4) to pass, got: %v", v.Reasons)
	}
}

func TestBudgetBlocksAtCeiling(t *testing.T) {
	existing := make([]ExistingTool, 40)
	for i := range existing {
		existing[i] = ExistingTool{Name: "tool_" + string(rune('a'+i%26)), Description: "unrelated"}
	}
	v := Evaluate(DefaultThresholds(), Candidate{
		Name: "one_more", Description: "brand new distinct capability",
		Origin: OriginUserForced,
	}, existing)
	if v.Allowed {
		t.Fatal("expected the proposal to be blocked at the 40-tool budget ceiling")
	}
	if !containsReason(v, "budget") {
		t.Errorf("expected a budget reason, got %v", v.Reasons)
	}
}

func TestBudgetBelowCeilingPasses(t *testing.T) {
	existing := make([]ExistingTool, 39)
	for i := range existing {
		existing[i] = ExistingTool{Name: "tool_" + string(rune('a'+i%26)), Description: "unrelated"}
	}
	v := Evaluate(DefaultThresholds(), Candidate{
		Name: "one_more", Description: "brand new distinct capability",
		Origin: OriginUserForced,
	}, existing)
	if !v.Allowed {
		t.Fatalf("expected 39 existing tools (below the 40 ceiling) to pass budget, got: %v", v.Reasons)
	}
}

func TestProfitabilitySkippedWhenExpectedUsesIsZero(t *testing.T) {
	v := Evaluate(DefaultThresholds(), Candidate{
		Name: "unmeasured_tool", Description: "no estimate supplied",
		Origin: OriginUserForced, ExpectedUses: 0,
		CreationCostTokens: 999999, PerUseSavingTokens: 0,
	}, nil)
	if !v.Allowed {
		t.Fatalf("expected profitability to be skipped (not failed) with no estimate, got: %v", v.Reasons)
	}
}

func TestProfitabilityBlocksWhenSavingsDoNotAmortize(t *testing.T) {
	v := Evaluate(DefaultThresholds(), Candidate{
		Name: "expensive_tool", Description: "costly to build, rarely saves much",
		Origin:             OriginUserForced,
		CreationCostTokens: 4100, PerUseSavingTokens: 10, ExpectedUses: 5,
	}, nil)
	if v.Allowed {
		t.Fatal("expected 10*5=50 saved tokens against a 4100-token cost to fail profitability")
	}
	if !containsReason(v, "profitability") {
		t.Errorf("expected a profitability reason, got %v", v.Reasons)
	}
}

func TestProfitabilityPassesWhenSavingsAmortize(t *testing.T) {
	v := Evaluate(DefaultThresholds(), Candidate{
		Name: "cheap_tool", Description: "cheap to build, saves a lot",
		Origin:             OriginUserForced,
		CreationCostTokens: 4100, PerUseSavingTokens: 3980, ExpectedUses: 12,
	}, nil)
	if !v.Allowed {
		t.Fatalf("expected §19.4's own 12-use amortization example to pass, got: %v", v.Reasons)
	}
}

func TestEvaluateCollectsEveryFailureNotJustTheFirst(t *testing.T) {
	existing := make([]ExistingTool, 40)
	for i := range existing {
		existing[i] = ExistingTool{Name: "filler", Description: "filler tool"}
	}
	existing[0] = ExistingTool{Name: "near_dup", Description: "a duplicate style match"}
	v := Evaluate(DefaultThresholds(), Candidate{
		Name: "near_dup_2", Description: "a duplicate style match",
		Origin: OriginAgent, Repetitions: 1, VaryingArgs: 10,
		CreationCostTokens: 1000, PerUseSavingTokens: 1, ExpectedUses: 1,
	}, existing)
	if v.Allowed {
		t.Fatal("expected this candidate to fail on multiple criteria")
	}
	// repetition, duplicate, stability, budget, profitability -- all five.
	if len(v.Reasons) < 5 {
		t.Errorf("expected all failing criteria to be reported, got only %d: %v", len(v.Reasons), v.Reasons)
	}
}

func TestZeroThresholdsFallBackToDefaults(t *testing.T) {
	// A caller translating a never-configured config.Evolve/config.Tools
	// (every field at its Go zero value) must see §19.6's real defaults,
	// not an accidentally permissive or impossible gate.
	v := Evaluate(Thresholds{}, Candidate{
		Name: "x", Description: "y", Origin: OriginAgent, Repetitions: 3,
	}, nil)
	if !v.Allowed {
		t.Fatalf("expected zero-value Thresholds to normalize to defaults (MinRepeats=3), got: %v", v.Reasons)
	}
}

func TestCustomThresholdsOverrideDefaults(t *testing.T) {
	v := Evaluate(Thresholds{MinRepeats: 10}, Candidate{
		Name: "x", Description: "y", Origin: OriginAgent, Repetitions: 5,
	}, nil)
	if v.Allowed {
		t.Fatal("expected a custom, stricter MinRepeats to be honored over the default")
	}
}

// fakeToolsSource is a minimal, hand-rolled ExistingToolsSource whose
// Count() and FindSimilar() deliberately disagree with each other -- the
// exact scenario ExistingToolsSource's own doc comment names as the reason
// it is two methods rather than one ("a registry that excludes
// quarantined tools from FindSimilar's candidate pool but still counts
// them toward the budget ceiling"). It exists only in this test file: it
// is not a production shape, only proof that EvaluateAgainst really does
// call through the interface rather than silently assuming a slice.
type fakeToolsSource struct {
	count   int
	similar []ExistingTool
}

func (f fakeToolsSource) Count() int { return f.count }
func (f fakeToolsSource) FindSimilar(name, description string) []ExistingTool {
	return f.similar
}

func TestEvaluateAgainstUsesCountIndependentlyOfFindSimilar(t *testing.T) {
	// Budget must read Count(), not len(FindSimilar(...)) -- a source
	// whose two methods disagree must still be blocked on budget using
	// its own reported count, even though FindSimilar's pool here is
	// small enough that dedup alone would never trigger.
	src := fakeToolsSource{count: 40, similar: nil}
	v := EvaluateAgainst(DefaultThresholds(), Candidate{
		Name: "one_more", Description: "brand new distinct capability",
		Origin: OriginUserForced,
	}, src)
	if v.Allowed {
		t.Fatal("expected EvaluateAgainst to block on budget using Count(), independent of FindSimilar's own pool")
	}
	if !containsReason(v, "budget") {
		t.Errorf("expected a budget reason, got %v", v.Reasons)
	}
}

func TestEvaluateAgainstUsesFindSimilarIndependentlyOfCount(t *testing.T) {
	// Dedup must read FindSimilar(...)'s own pool, not derive it from
	// Count() -- a source reporting a low count but still surfacing a
	// near-identical tool through FindSimilar must still be blocked on
	// duplicate.
	src := fakeToolsSource{
		count: 1,
		similar: []ExistingTool{
			{Name: "git_status_short", Description: "show abbreviated git status output"},
		},
	}
	v := EvaluateAgainst(DefaultThresholds(), Candidate{
		Name: "git_status_brief", Description: "show abbreviated git status output",
		Origin: OriginUserForced,
	}, src)
	if v.Allowed {
		t.Fatal("expected EvaluateAgainst to block on duplicate using FindSimilar's own pool, independent of Count()")
	}
	if !containsReason(v, "duplicate") {
		t.Errorf("expected a duplicate reason, got %v", v.Reasons)
	}
}

func TestEvaluateAgainstNilSourceBehavesLikeEmptyCatalogue(t *testing.T) {
	// A nil ExistingToolsSource must be as harmless as an empty slice --
	// no caller of EvaluateAgainst should need a nil guard of its own.
	v := EvaluateAgainst(DefaultThresholds(), Candidate{
		Name: "x", Description: "y", Origin: OriginAgent, Repetitions: 3,
	}, nil)
	if !v.Allowed {
		t.Fatalf("expected a nil source to behave like an empty catalogue, got: %v", v.Reasons)
	}
}

func TestEvaluateDelegatesToEvaluateAgainstViaExistingToolsSlice(t *testing.T) {
	// Evaluate's own long-standing []ExistingTool signature must still
	// produce byte-identical verdicts to calling EvaluateAgainst directly
	// through ExistingToolsSlice -- this is the whole point of keeping
	// Evaluate as a thin wrapper: every pre-interface call site keeps
	// compiling and behaving exactly as before.
	existing := []ExistingTool{
		{Name: "git_status_short", Description: "show abbreviated git status output"},
	}
	candidate := Candidate{
		Name: "git_status_brief", Description: "show abbreviated git status output",
		Origin: OriginUserForced,
	}
	viaSlice := Evaluate(DefaultThresholds(), candidate, existing)
	viaInterface := EvaluateAgainst(DefaultThresholds(), candidate, ExistingToolsSlice(existing))
	if viaSlice.Allowed != viaInterface.Allowed || len(viaSlice.Reasons) != len(viaInterface.Reasons) {
		t.Fatalf("Evaluate and EvaluateAgainst(ExistingToolsSlice(...)) diverged: %+v vs %+v", viaSlice, viaInterface)
	}
}

func TestExistingToolsSliceCountAndFindSimilar(t *testing.T) {
	s := ExistingToolsSlice{
		{Name: "a", Description: "does a"},
		{Name: "b", Description: "does b"},
	}
	if got := s.Count(); got != 2 {
		t.Fatalf("Count() = %d, want 2", got)
	}
	got := s.FindSimilar("anything", "ignored")
	if len(got) != 2 || got[0].Name != "a" || got[1].Name != "b" {
		t.Fatalf("FindSimilar() = %+v, want the whole slice unchanged regardless of arguments", got)
	}
}

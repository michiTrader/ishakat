package evolve

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNextSuggestionPicksMostRepeatedEligiblePattern(t *testing.T) {
	records := []Record{
		{Pattern: "curl a", N: 3, Last: "2026-08-01"},
		{Pattern: "curl b", N: 7, Last: "2026-08-03"},
	}
	got, ok := NextSuggestion(records, DefaultThresholds())
	if !ok {
		t.Fatal("expected a suggestion, got none")
	}
	if got.Pattern != "curl b" || got.N != 7 {
		t.Fatalf("got %+v, want the most-repeated pattern", got)
	}
}

func TestNextSuggestionSkipsPatternsBelowMinRepeats(t *testing.T) {
	records := []Record{
		{Pattern: "curl a", N: 2, Last: "2026-08-01"},
	}
	_, ok := NextSuggestion(records, DefaultThresholds())
	if ok {
		t.Fatal("expected no suggestion below MinRepeats (default 3)")
	}
}

func TestNextSuggestionSkipsDismissedPatternsEvenIfMostRepeated(t *testing.T) {
	records := []Record{
		{Pattern: "curl a", N: 3, Last: "2026-08-01"},
		{Pattern: "curl b", N: 9, Last: "2026-08-03", Dismissed: true},
	}
	got, ok := NextSuggestion(records, DefaultThresholds())
	if !ok {
		t.Fatal("expected the non-dismissed pattern to still be offered")
	}
	if got.Pattern != "curl a" {
		t.Fatalf("got %q, want the non-dismissed pattern even though it repeats less", got.Pattern)
	}
}

func TestNextSuggestionReturnsFalseOnEmptyLedger(t *testing.T) {
	if _, ok := NextSuggestion(nil, DefaultThresholds()); ok {
		t.Fatal("expected no suggestion from an empty ledger")
	}
}

func TestNextSuggestionReturnsFalseWhenEveryRecordIsDismissed(t *testing.T) {
	records := []Record{
		{Pattern: "curl a", N: 10, Last: "2026-08-01", Dismissed: true},
	}
	if _, ok := NextSuggestion(records, DefaultThresholds()); ok {
		t.Fatal("expected no suggestion when every eligible record is dismissed")
	}
}

func TestDismissPatternMarksExactMatchOnly(t *testing.T) {
	l := &Ledger{Records: []Record{
		{Pattern: "curl a", N: 5},
		{Pattern: "curl b", N: 5},
	}}
	l.DismissPattern("curl a")
	if !l.Records[0].Dismissed {
		t.Error("expected \"curl a\" to be dismissed")
	}
	if l.Records[1].Dismissed {
		t.Error("expected \"curl b\" to remain untouched")
	}
}

func TestDismissPatternOnUnknownPatternIsANoOp(t *testing.T) {
	l := &Ledger{Records: []Record{{Pattern: "curl a", N: 5}}}
	l.DismissPattern("curl z")
	if l.Records[0].Dismissed {
		t.Error("dismissing an unrelated pattern must not touch curl a")
	}
}

func TestLoadSuggestStateMissingFileReturnsZeroValueNotError(t *testing.T) {
	dir := t.TempDir()
	s, err := LoadSuggestState(filepath.Join(dir, "does-not-exist.json"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.WeekStart != "" || s.WeekCount != 0 || s.ConsecutiveRejects != 0 {
		t.Fatalf("expected a zero-value state, got %+v", s)
	}
}

func TestSaveAndLoadSuggestStateRoundTrips(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "suggest_state.json")
	want := &SuggestState{WeekStart: "2026-08-01", WeekCount: 2, ConsecutiveRejects: 1}
	if err := SaveSuggestState(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := LoadSuggestState(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if *got != *want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestLoadSuggestStateCorruptedContentReturnsZeroValueNotError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "suggest_state.json")
	if err := os.WriteFile(path, []byte("{not json\n"), 0o600); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}
	s, err := LoadSuggestState(path)
	if err != nil {
		t.Fatalf("unexpected error on corrupted content: %v", err)
	}
	if s.WeekCount != 0 {
		t.Fatalf("expected a fresh zero-value state on corruption, got %+v", s)
	}
}

func TestRollWeekStartsAFreshWindowOnFirstCall(t *testing.T) {
	s := &SuggestState{}
	s.RollWeek("2026-08-01")
	if s.WeekStart != "2026-08-01" {
		t.Fatalf("WeekStart = %q, want 2026-08-01", s.WeekStart)
	}
	if s.WeekCount != 0 {
		t.Fatalf("WeekCount = %d, want 0 on a fresh window", s.WeekCount)
	}
}

func TestRollWeekKeepsCountingWithinTheSameSevenDayWindow(t *testing.T) {
	s := &SuggestState{WeekStart: "2026-08-01", WeekCount: 2}
	s.RollWeek("2026-08-05")
	if s.WeekStart != "2026-08-01" || s.WeekCount != 2 {
		t.Fatalf("got %+v, want the window and count untouched within 7 days", s)
	}
}

func TestRollWeekResetsOnceSevenDaysHavePassed(t *testing.T) {
	s := &SuggestState{WeekStart: "2026-08-01", WeekCount: 3}
	s.RollWeek("2026-08-08")
	if s.WeekStart != "2026-08-08" {
		t.Fatalf("WeekStart = %q, want 2026-08-08", s.WeekStart)
	}
	if s.WeekCount != 0 {
		t.Fatalf("WeekCount = %d, want 0 after the window rolled", s.WeekCount)
	}
}

func TestRollWeekWithMalformedWeekStartStartsOverRatherThanErroring(t *testing.T) {
	s := &SuggestState{WeekStart: "not-a-date", WeekCount: 9}
	s.RollWeek("2026-08-01")
	if s.WeekStart != "2026-08-01" || s.WeekCount != 0 {
		t.Fatalf("got %+v, want a fresh window on malformed WeekStart", s)
	}
}

func TestRecordShownIncrementsWeekCount(t *testing.T) {
	s := &SuggestState{}
	s.RecordShown()
	s.RecordShown()
	if s.WeekCount != 2 {
		t.Fatalf("WeekCount = %d, want 2", s.WeekCount)
	}
}

func TestRecordAcceptanceResetsConsecutiveRejects(t *testing.T) {
	s := &SuggestState{ConsecutiveRejects: 2}
	s.RecordAcceptance()
	if s.ConsecutiveRejects != 0 {
		t.Fatalf("ConsecutiveRejects = %d, want 0 after an acceptance", s.ConsecutiveRejects)
	}
}

func TestRecordRejectionReportsDecayOnlyOnTheExactTransition(t *testing.T) {
	s := &SuggestState{}
	if decayed := s.RecordRejection(3); decayed {
		t.Fatal("1st rejection must not decay with decayAfter=3")
	}
	if decayed := s.RecordRejection(3); decayed {
		t.Fatal("2nd rejection must not decay with decayAfter=3")
	}
	if decayed := s.RecordRejection(3); !decayed {
		t.Fatal("3rd rejection must decay with decayAfter=3")
	}
	if decayed := s.RecordRejection(3); decayed {
		t.Fatal("4th rejection must not decay again -- only the exact transition reports true")
	}
	if s.ConsecutiveRejects != 4 {
		t.Fatalf("ConsecutiveRejects = %d, want 4 (counting continues past decay)", s.ConsecutiveRejects)
	}
}

func TestRecordRejectionWithZeroDecayAfterNeverDecays(t *testing.T) {
	s := &SuggestState{}
	for i := 0; i < 5; i++ {
		if decayed := s.RecordRejection(0); decayed {
			t.Fatal("decayAfter=0 must disable decay entirely")
		}
	}
}

func TestDecideSuggestionOffersWhenBudgetsAllow(t *testing.T) {
	records := []Record{{Pattern: "curl a", N: 5}}
	d := DecideSuggestion(records, SuggestState{}, DefaultThresholds(), 0, 1, 3)
	if !d.Offer {
		t.Fatal("expected an offer when both budgets have room")
	}
	if d.Candidate.Pattern != "curl a" {
		t.Fatalf("Candidate.Pattern = %q, want curl a", d.Candidate.Pattern)
	}
}

func TestDecideSuggestionBlocksAtSessionBudget(t *testing.T) {
	records := []Record{{Pattern: "curl a", N: 5}}
	d := DecideSuggestion(records, SuggestState{}, DefaultThresholds(), 1, 1, 3)
	if d.Offer {
		t.Fatal("expected no offer once the session budget is already spent")
	}
}

func TestDecideSuggestionBlocksAtWeekBudget(t *testing.T) {
	records := []Record{{Pattern: "curl a", N: 5}}
	state := SuggestState{WeekCount: 3}
	d := DecideSuggestion(records, state, DefaultThresholds(), 0, 1, 3)
	if d.Offer {
		t.Fatal("expected no offer once the week budget is already spent")
	}
}

func TestDecideSuggestionWithZeroBudgetsMeansUnlimited(t *testing.T) {
	records := []Record{{Pattern: "curl a", N: 5}}
	state := SuggestState{WeekCount: 999}
	d := DecideSuggestion(records, state, DefaultThresholds(), 999, 0, 0)
	if !d.Offer {
		t.Fatal("expected 0 to mean unlimited for both session and week budgets")
	}
}

func TestDecideSuggestionReturnsNoOfferWhenNoPatternQualifies(t *testing.T) {
	d := DecideSuggestion(nil, SuggestState{}, DefaultThresholds(), 0, 1, 3)
	if d.Offer {
		t.Fatal("expected no offer from an empty ledger regardless of budgets")
	}
}

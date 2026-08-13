package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/evolve"
)

// fakeEvolveStore is EvolveStore's own test double, the same
// three-line-per-method shape fakeRecorder (session_internal_test.go)
// already establishes for its own interface.
type fakeEvolveStore struct {
	ledger  *evolve.Ledger
	state   *evolve.SuggestState
	decayed bool

	loadLedgerErr error
	saveLedgerErr error
	loadStateErr  error
	saveStateErr  error
	decayErr      error
	savedStates   []evolve.SuggestState
	savedLedgers  []evolve.Ledger
}

func newFakeEvolveStore(ledger *evolve.Ledger, state *evolve.SuggestState) *fakeEvolveStore {
	if ledger == nil {
		ledger = &evolve.Ledger{}
	}
	if state == nil {
		state = &evolve.SuggestState{}
	}
	return &fakeEvolveStore{ledger: ledger, state: state}
}

func (f *fakeEvolveStore) LoadLedger() (*evolve.Ledger, error) {
	if f.loadLedgerErr != nil {
		return nil, f.loadLedgerErr
	}
	cp := *f.ledger
	cp.Records = append([]evolve.Record(nil), f.ledger.Records...)
	return &cp, nil
}

func (f *fakeEvolveStore) SaveLedger(l *evolve.Ledger) error {
	if f.saveLedgerErr != nil {
		return f.saveLedgerErr
	}
	f.ledger = l
	f.savedLedgers = append(f.savedLedgers, *l)
	return nil
}

func (f *fakeEvolveStore) LoadSuggestState() (*evolve.SuggestState, error) {
	if f.loadStateErr != nil {
		return nil, f.loadStateErr
	}
	cp := *f.state
	return &cp, nil
}

func (f *fakeEvolveStore) SaveSuggestState(s *evolve.SuggestState) error {
	if f.saveStateErr != nil {
		return f.saveStateErr
	}
	f.state = s
	f.savedStates = append(f.savedStates, *s)
	return nil
}

func (f *fakeEvolveStore) Decay() error {
	if f.decayErr != nil {
		return f.decayErr
	}
	f.decayed = true
	return nil
}

func rootWithEvolve(store EvolveStore) Root {
	root := newHeadlessRoot()
	root.evolveStore = store
	root.evolveThresholds = evolve.Thresholds{MinRepeats: 3, DedupThreshold: 0.8, MaxTools: 40}
	root.suggestPerSession = 1
	root.suggestPerWeek = 3
	root.decayAfterRejects = 3
	return root
}

// TestCheckSuggestNilStoreIsInert covers the "feature not active" default
// (Options.EvolveStore's own comment): a nil store must never open
// ModeSuggest, the same nil-is-safe rule Recorder/SessionLister already
// follow for their own concerns.
func TestCheckSuggestNilStoreIsInert(t *testing.T) {
	root := newHeadlessRoot()
	if root.evolveStore != nil {
		t.Fatal("newHeadlessRoot should not wire an EvolveStore by default")
	}
	m, cmd := root.checkSuggest()
	got := m.(Root)
	if got.mode != ModeChat {
		t.Fatalf("mode = %v, want ModeChat: a nil store must never open ModeSuggest", got.mode)
	}
	if cmd != nil {
		t.Errorf("checkSuggest with a nil store should be a synchronous no-op, got cmd %v", cmd)
	}
}

// TestCheckSuggestOffersWhenThresholdCrossed covers the ordinary path: a
// pattern that has crossed MinRepeats and is not dismissed, with budget
// still available, must open ModeSuggest and record the offer as shown.
func TestCheckSuggestOffersWhenThresholdCrossed(t *testing.T) {
	ledger := &evolve.Ledger{Records: []evolve.Record{
		{Pattern: "curl bybit ticker", N: 5, Last: "2026-08-01"},
	}}
	store := newFakeEvolveStore(ledger, &evolve.SuggestState{})
	root := rootWithEvolve(store)

	m, cmd := root.checkSuggest()
	if cmd != nil {
		t.Errorf("checkSuggest is fully synchronous (no clock/filesystem call blocks Update), got cmd %v", cmd)
	}
	got := m.(Root)
	if got.mode != ModeSuggest {
		t.Fatalf("mode = %v, want ModeSuggest", got.mode)
	}
	if got.suggest.candidate.Pattern != "curl bybit ticker" {
		t.Errorf("candidate = %+v, want the one eligible pattern", got.suggest.candidate)
	}
	if got.suggestSessionCount != 1 {
		t.Errorf("suggestSessionCount = %d, want 1 after the first suggestion actually shown", got.suggestSessionCount)
	}
	if len(store.savedStates) != 1 || store.savedStates[0].WeekCount != 1 {
		t.Errorf("SaveSuggestState should have recorded WeekCount=1 (RecordShown), got %+v", store.savedStates)
	}
}

// TestCheckSuggestRespectsDismissed covers rule 2 ("once per pattern,
// ever"): a Record.Dismissed = true must never be offered again, even if
// it still crosses MinRepeats.
func TestCheckSuggestRespectsDismissed(t *testing.T) {
	ledger := &evolve.Ledger{Records: []evolve.Record{
		{Pattern: "curl bybit ticker", N: 5, Last: "2026-08-01", Dismissed: true},
	}}
	store := newFakeEvolveStore(ledger, &evolve.SuggestState{})
	root := rootWithEvolve(store)

	m, _ := root.checkSuggest()
	got := m.(Root)
	if got.mode != ModeChat {
		t.Fatalf("mode = %v, want ModeChat: a dismissed pattern must never be offered again", got.mode)
	}
}

// TestCheckSuggestRespectsSessionBudget covers rule 3's session half:
// once suggestSessionCount already reached suggestPerSession, no further
// offer this process's lifetime, regardless of what else qualifies.
func TestCheckSuggestRespectsSessionBudget(t *testing.T) {
	ledger := &evolve.Ledger{Records: []evolve.Record{
		{Pattern: "curl bybit ticker", N: 5, Last: "2026-08-01"},
	}}
	store := newFakeEvolveStore(ledger, &evolve.SuggestState{})
	root := rootWithEvolve(store)
	root.suggestSessionCount = 1 // already at the per-session budget of 1

	m, _ := root.checkSuggest()
	got := m.(Root)
	if got.mode != ModeChat {
		t.Fatalf("mode = %v, want ModeChat: the session budget was already spent", got.mode)
	}
}

// TestUpdateSuggestMovesSelectionAndCancels covers up/down cursor
// movement, wrapping, and esc closing the dialog with no side effect at
// all — not a rejection.
func TestUpdateSuggestMovesSelectionAndCancels(t *testing.T) {
	store := newFakeEvolveStore(nil, nil)
	root := rootWithEvolve(store)
	root.mode = ModeSuggest
	root.suggest = newSuggestState(evolve.SuggestionCandidate{Pattern: "p", N: 4, Last: "2026-08-01"})

	if got := root.suggest.moveSel(-1).sel; got != len(root.suggest.options())-1 {
		t.Fatalf("moving up from row 0 = %d, want the last row (wrap)", got)
	}

	m, _ := root.updateSuggest(tea.KeyPressMsg{Code: tea.KeyDown})
	got := m.(Root)
	if got.suggest.sel != 1 {
		t.Fatalf("selection after one down = %d, want 1", got.suggest.sel)
	}

	m, _ = got.updateSuggest(tea.KeyPressMsg{Code: tea.KeyEsc})
	got = m.(Root)
	if got.mode != ModeChat {
		t.Fatalf("mode after esc = %v, want ModeChat", got.mode)
	}
	if got.suggest.candidate.Pattern != "" {
		t.Errorf("suggest state should be cleared after cancel, got %+v", got.suggest)
	}
	if len(store.savedStates) != 0 {
		t.Errorf("esc must not touch SuggestState at all (not a rejection), got %+v", store.savedStates)
	}
}

// TestResolveSuggestDetailTogglesWithoutClosing covers "[v] ver el
// código": selecting it must flip the detail flag and stay in
// ModeSuggest, never close the dialog or touch the store.
func TestResolveSuggestDetailTogglesWithoutClosing(t *testing.T) {
	store := newFakeEvolveStore(nil, nil)
	root := rootWithEvolve(store)
	root.mode = ModeSuggest
	root.suggest = newSuggestState(evolve.SuggestionCandidate{Pattern: "p", N: 4, Last: "2026-08-01"}).moveSel(1)
	if root.suggest.selected().kind != "detail" {
		t.Fatalf("row 1 should be the detail toggle, got %+v", root.suggest.selected())
	}

	m, _ := root.updateSuggest(tea.KeyPressMsg{Code: tea.KeyEnter})
	got := m.(Root)
	if got.mode != ModeSuggest {
		t.Fatalf("mode after toggling detail = %v, want ModeSuggest (still open)", got.mode)
	}
	if !got.suggest.detail {
		t.Fatal("detail flag should now be true")
	}
	if got.suggest.options()[1].label != "ocultar detalle" {
		t.Errorf("label should flip to \"ocultar detalle\" once shown, got %q", got.suggest.options()[1].label)
	}
}

// TestDismissSuggestionRecordsRejectionAndDismissesPattern covers "[n] no,
// ni ahora ni después": the ledger's own record must end up Dismissed,
// and the rejection must be counted, without yet triggering decay.
func TestDismissSuggestionRecordsRejectionAndDismissesPattern(t *testing.T) {
	ledger := &evolve.Ledger{Records: []evolve.Record{{Pattern: "curl bybit ticker", N: 5, Last: "2026-08-01"}}}
	store := newFakeEvolveStore(ledger, &evolve.SuggestState{})
	root := rootWithEvolve(store)
	root.mode = ModeSuggest
	root.suggest = newSuggestState(evolve.SuggestionCandidate{Pattern: "curl bybit ticker", N: 5, Last: "2026-08-01"}).moveSel(2)
	if root.suggest.selected().kind != "dismiss" {
		t.Fatalf("row 2 should be dismiss, got %+v", root.suggest.selected())
	}

	m, cmd := root.updateSuggest(tea.KeyPressMsg{Code: tea.KeyEnter})
	got := m.(Root)
	if got.mode != ModeChat {
		t.Fatalf("mode after dismiss = %v, want ModeChat", got.mode)
	}
	if cmd != nil {
		t.Errorf("a dismiss with no decay transition is synchronous, got cmd %v", cmd)
	}
	if len(store.savedLedgers) != 1 || !store.savedLedgers[0].Records[0].Dismissed {
		t.Fatalf("the ledger record should have been saved with Dismissed=true, got %+v", store.savedLedgers)
	}
	if len(store.savedStates) != 1 || store.savedStates[0].ConsecutiveRejects != 1 {
		t.Fatalf("ConsecutiveRejects should be 1 after the first rejection, got %+v", store.savedStates)
	}
	if store.decayed {
		t.Error("a single rejection must not trigger decay yet (decayAfterRejects=3)")
	}
}

// TestDismissSuggestionTriggersDecayOnceStreakHitsThreshold covers rule
// 4: the *third* consecutive rejection (decayAfterRejects=3 in
// rootWithEvolve) must call Decay() and surface a slashNotice, exactly
// once, on the transition.
func TestDismissSuggestionTriggersDecayOnceStreakHitsThreshold(t *testing.T) {
	store := newFakeEvolveStore(&evolve.Ledger{}, &evolve.SuggestState{ConsecutiveRejects: 2})
	root := rootWithEvolve(store)
	root.mode = ModeSuggest
	root.suggest = newSuggestState(evolve.SuggestionCandidate{Pattern: "p", N: 4, Last: "2026-08-01"}).moveSel(2)

	m, _ := root.dismissSuggestion()
	got := m.(Root)
	if !store.decayed {
		t.Fatal("the third consecutive rejection should have called Decay()")
	}
	if len(got.transcript) == 0 {
		t.Fatal("a decay transition should surface a slashNotice in the transcript")
	}
}

// TestAcceptSuggestionAppendsPromptAndStartsATurn covers "[t] crearla":
// it must append a user-role message describing the pattern, clear
// ConsecutiveRejects (RecordAcceptance), and hand off to startEngineTurn
// -- the exact same machinery /retry already drives -- rather than
// constructing a tool_create call by hand.
func TestAcceptSuggestionAppendsPromptAndStartsATurn(t *testing.T) {
	store := newFakeEvolveStore(&evolve.Ledger{}, &evolve.SuggestState{ConsecutiveRejects: 2})
	root := rootWithEvolve(store)
	eng, _ := echoEngine(false)
	root = withEngine(root, eng)
	root.mode = ModeSuggest
	root.suggest = newSuggestState(evolve.SuggestionCandidate{Pattern: "curl bybit ticker", N: 5, Last: "2026-08-01"})

	m, cmd := root.acceptSuggestion()
	got := m.(Root)
	if got.mode != ModeBusy {
		t.Fatalf("mode after accepting = %v, want ModeBusy: startEngineTurn should already be running", got.mode)
	}
	if cmd == nil {
		t.Fatal("acceptSuggestion should hand back startEngineTurn's own tea.Cmd")
	}
	if len(got.conv.Messages) == 0 {
		t.Fatal("acceptSuggestion should have appended a user message to m.conv")
	}
	last := got.conv.Messages[len(got.conv.Messages)-1]
	if !strings.Contains(last.Text(), "curl bybit ticker") {
		t.Errorf("the appended prompt should mention the pattern, got %q", last.Text())
	}
	if !strings.Contains(last.Text(), "tool_create") {
		t.Errorf("the appended prompt should ask the model to call tool_create itself, got %q", last.Text())
	}
	if len(store.savedStates) != 1 || store.savedStates[0].ConsecutiveRejects != 0 {
		t.Errorf("acceptance should reset ConsecutiveRejects to 0, got %+v", store.savedStates)
	}
}

// TestCheckEndOfTurnDoesNotSuggestBehindAnOpenCompact covers the ordering
// decision documented on checkEndOfTurn itself: if checkAutoCompact just
// opened ModeCompact, checkSuggest must not fire this turn at all -- the
// two overlays must never stack or race.
func TestCheckEndOfTurnDoesNotSuggestBehindAnOpenCompact(t *testing.T) {
	ledger := &evolve.Ledger{Records: []evolve.Record{{Pattern: "p", N: 5, Last: "2026-08-01"}}}
	store := newFakeEvolveStore(ledger, &evolve.SuggestState{})
	root := rootWithEvolve(store)
	root.compactAuto = true
	root.compactTriggerPct = 1 // trigger on almost anything
	// No catalog entry for root.model means m.cat.Get fails and
	// checkAutoCompact's own guard leaves it at ModeChat -- so instead
	// this test drives the *other* branch directly: force startCompact's
	// own effect by simulating "compaction opened its overlay" via mode.
	root.mode = ModeCompact

	next, _ := root.checkEndOfTurn()
	got := next.(Root)
	if got.mode != ModeCompact {
		t.Fatalf("mode = %v, want ModeCompact: checkSuggest must not run while compaction's own overlay is open", got.mode)
	}
}

// TestCheckEndOfTurnOffersSuggestionOnceChatIsClean is the positive
// counterpart: with no compaction pending, checkEndOfTurn must still
// offer a qualifying suggestion exactly as checkSuggest alone would.
func TestCheckEndOfTurnOffersSuggestionOnceChatIsClean(t *testing.T) {
	ledger := &evolve.Ledger{Records: []evolve.Record{{Pattern: "p", N: 5, Last: "2026-08-01"}}}
	store := newFakeEvolveStore(ledger, &evolve.SuggestState{})
	root := rootWithEvolve(store)
	root.mode = ModeChat

	next, _ := root.checkEndOfTurn()
	got := next.(Root)
	if got.mode != ModeSuggest {
		t.Fatalf("mode = %v, want ModeSuggest once checkAutoCompact left the turn settled in ModeChat", got.mode)
	}
}

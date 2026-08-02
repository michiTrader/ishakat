package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/catalog"
	"github.com/MichiTrader/ishakat/internal/convo"
	"github.com/MichiTrader/ishakat/internal/engine"
)

// errBoom is a fixed sentinel for the on_error tests below: they only care
// that finishCompact's error branch fires and that the error's own text
// makes it into the surfaced notice, not about any particular wording.
var errBoom = errors.New("boom: compact_model unreachable")

// fakeErrStreamer fails the handshake itself (Streamer's second return
// value), the same shape RunToCompletion's own "err := e.open(...)" branch
// expects — this is how the tests below drive engine.Summarize into its
// error path without a real network call.
func fakeErrStreamer(err error) engine.Streamer {
	return func(ctx context.Context, req engine.Request) (<-chan engine.Event, error) {
		return nil, err
	}
}

// bigCompactMessage mirrors confirm_internal_test.go's own bigConfirmMessage,
// kept as a separate name so this file does not depend on that one's
// existence to compile on its own.
func bigCompactMessage(role convo.Role, approxTokens int) convo.Message {
	return convo.NewMessage(role, convo.TextBlock(strings.Repeat("hola mundo ", approxTokens/2+1)))
}

// TestStartCompactWithNoEngineFallsBackToDropOldest covers Root.compactEng
// == nil (newHeadlessRoot never sets it): startCompact must skip ModeCompact
// entirely and go straight to the "discarded, no summary" marker rather than
// dereferencing a nil engine.
func TestStartCompactWithNoEngineFallsBackToDropOldest(t *testing.T) {
	root := newHeadlessRoot()
	for i := 0; i < 10; i++ {
		root.conv.Add(bigCompactMessage(convo.RoleUser, 50))
		root.conv.Add(bigCompactMessage(convo.RoleAssistant, 50))
	}

	m, cmd := root.startCompact("")
	if cmd != nil {
		t.Errorf("the no-engine fallback is synchronous, want a nil cmd, got %v", cmd)
	}
	got := m.(Root)
	if got.mode != ModeChat {
		t.Fatalf("mode = %v, want ModeChat: there is nothing to wait on without an engine", got.mode)
	}
	if len(got.transcript) != 1 {
		t.Fatalf("expected one compaction notice, got %v", got.transcript)
	}
	if !strings.Contains(got.transcript[0].text, "compactado") {
		t.Errorf("notice should report the compaction, got %q", got.transcript[0].text)
	}
	last := got.conv.Messages[len(got.conv.Messages)-1]
	if !last.Has(convo.BlockSummary) {
		t.Fatalf("expected a BlockSummary marker, got %+v", last)
	}
	if !strings.Contains(last.Text(), "descartados") {
		t.Errorf("the fallback marker should say nothing was summarized, got %q", last.Text())
	}
}

// TestStartCompactWithDropOldestStrategySkipsTheModelEvenWhenEngineIsSet
// covers [compact].strategy = "drop-oldest" (§5.2): a configured compact_model
// must never be called at all when the strategy itself says to skip
// summarizing, which is a different reason to fall back than "no engine".
func TestStartCompactWithDropOldestStrategySkipsTheModelEvenWhenEngineIsSet(t *testing.T) {
	called := false
	streamer := func(ctx context.Context, req engine.Request) (<-chan engine.Event, error) {
		called = true
		ch := make(chan engine.Event, 1)
		ch <- engine.Event{Kind: engine.EventDone}
		close(ch)
		return ch, nil
	}

	root := newHeadlessRoot()
	root.compactEng = engine.New(streamer, 0)
	root.compactModel = "compact/model"
	root.compactStrategy = "drop-oldest"
	for i := 0; i < 10; i++ {
		root.conv.Add(bigCompactMessage(convo.RoleUser, 50))
		root.conv.Add(bigCompactMessage(convo.RoleAssistant, 50))
	}

	m, cmd := root.startCompact("")
	if cmd != nil {
		t.Errorf("drop-oldest strategy is synchronous, want a nil cmd, got %v", cmd)
	}
	if called {
		t.Error("drop-oldest strategy must never call compact_model")
	}
	got := m.(Root)
	if got.mode != ModeChat {
		t.Fatalf("mode = %v, want ModeChat", got.mode)
	}
}

// TestStartCompactWithEmptyPlanSkipsStraightToTheSwitchWithNoNotice covers
// a Plan with nothing to replace (too few turns for compactKeepLastTurns to
// bite): no model call, no ModeCompact, and — unlike a real compaction — no
// "compactado" notice, since nothing was actually compacted.
func TestStartCompactWithEmptyPlanSkipsStraightToTheSwitchWithNoNotice(t *testing.T) {
	root := newHeadlessRoot()
	root.compactKeepLastTurns = 4
	root.conv.Add(convo.User("hola"))

	m, cmd := root.startCompact("b/small")
	if cmd != nil {
		t.Errorf("an empty plan is synchronous, want a nil cmd, got %v", cmd)
	}
	got := m.(Root)
	if got.mode != ModeChat {
		t.Fatalf("mode = %v, want ModeChat", got.mode)
	}
	if got.model != "b/small" {
		t.Errorf("model = %q, want the switch to still happen", got.model)
	}
	// A switch notice is expected (finishSwitchAfterCompact's own
	// confirmLine), but never a compaction one: reportCompactDone is never
	// reached on the plan.Empty() path.
	if len(got.transcript) != 1 {
		t.Fatalf("expected exactly one notice (the switch), got %v", got.transcript)
	}
	if strings.Contains(got.transcript[0].text, "compactado") {
		t.Errorf("nothing was compacted, notice should not claim so: %q", got.transcript[0].text)
	}
}

// TestSlashCompactRunsAsyncThroughModeCompact drives /compact end to end
// through the slash-command dispatch path (slashrun.go's KindCompact case),
// complementing confirm_internal_test.go's own coverage of the "compactar y
// cambiar" dialog path.
func TestSlashCompactRunsAsyncThroughModeCompact(t *testing.T) {
	root := newHeadlessRoot()
	root.compactEng = engine.New(fakeCompactStreamer("resumen vía slash."), 0)
	root.compactModel = "compact/model"
	for i := 0; i < 10; i++ {
		root.conv.Add(bigCompactMessage(convo.RoleUser, 50))
		root.conv.Add(bigCompactMessage(convo.RoleAssistant, 50))
	}

	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	for _, r := range "/compact" {
		m, _ = m.Update(tea.KeyPressMsg{Text: string(r), Code: r})
	}
	m, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	if m.(Root).mode != ModeCompact {
		t.Fatalf("mode = %v, want ModeCompact while compact_model answers", m.(Root).mode)
	}
	if cmd == nil {
		t.Fatal("/compact should schedule the summarize call")
	}
	done, ok := cmd().(compactDoneMsg)
	if !ok {
		t.Fatalf("expected a compactDoneMsg, got %T", cmd())
	}
	if done.err != nil {
		t.Fatalf("fakeCompactStreamer should not fail: %v", done.err)
	}
	m, _ = m.Update(done)

	got := m.(Root)
	if got.mode != ModeChat {
		t.Fatalf("mode = %v, want ModeChat after the summary lands", got.mode)
	}
	// A bare /compact has no switchTo attached, so only the compaction
	// notice is expected — not the §4.6 switch-confirmation line.
	if len(got.transcript) != 1 {
		t.Fatalf("expected exactly one notice (compaction, no switch), got %v", got.transcript)
	}
	if !strings.Contains(got.transcript[0].text, "compactado") {
		t.Errorf("notice should report the compaction, got %q", got.transcript[0].text)
	}
}

// TestFinishCompactOnErrorFallsBackToDropOldestWhenConfigured covers
// [compact].on_error = "drop-oldest" (the documented default): a failed
// compact_model call must still shrink the conversation via the same
// discard marker the no-engine/drop-oldest-strategy paths use, rather than
// leaving the oversized history untouched.
func TestFinishCompactOnErrorFallsBackToDropOldestWhenConfigured(t *testing.T) {
	root := newHeadlessRoot()
	root.compactEng = engine.New(fakeErrStreamer(errBoom), 0)
	root.compactModel = "compact/model"
	root.compactOnError = "drop-oldest"
	for i := 0; i < 10; i++ {
		root.conv.Add(bigCompactMessage(convo.RoleUser, 50))
		root.conv.Add(bigCompactMessage(convo.RoleAssistant, 50))
	}

	m, cmd := root.startCompact("")
	if m.(Root).mode != ModeCompact {
		t.Fatalf("mode = %v, want ModeCompact", m.(Root).mode)
	}
	if cmd == nil {
		t.Fatal("starting a real compaction should schedule the summarize call")
	}
	done, ok := cmd().(compactDoneMsg)
	if !ok {
		t.Fatalf("expected a compactDoneMsg, got %T", cmd())
	}
	if done.err == nil {
		t.Fatal("fakeErrStreamer should have produced an error")
	}

	final, _ := m.(Root).finishCompact(done.summary, done.err)
	got := final.(Root)
	if got.mode != ModeChat {
		t.Fatalf("mode = %v, want ModeChat even after a failed summary", got.mode)
	}
	if len(got.transcript) != 1 {
		t.Fatalf("expected one notice (the drop-oldest fallback), got %v", got.transcript)
	}
	if !strings.Contains(got.transcript[0].text, "compactado") {
		t.Errorf("notice should still report the fallback compaction, got %q", got.transcript[0].text)
	}
	last := got.conv.Messages[len(got.conv.Messages)-1]
	if !strings.Contains(last.Text(), "descartados") {
		t.Errorf("expected the discard marker, got %q", last.Text())
	}
}

// TestFinishCompactOnErrorOtherThanDropOldestSurfacesAWarningAndLeavesHistoryAlone
// covers any [compact].on_error value other than "drop-oldest": guessing a
// different remedy than what was configured would be worse than doing
// nothing (compact.go's own comment on finishCompact), so the conversation
// must be left exactly as it was before the failed attempt.
func TestFinishCompactOnErrorOtherThanDropOldestSurfacesAWarningAndLeavesHistoryAlone(t *testing.T) {
	root := newHeadlessRoot()
	root.compactEng = engine.New(fakeErrStreamer(errBoom), 0)
	root.compactModel = "compact/model"
	root.compactOnError = "" // NewRoot's own default only applies inside NewRoot itself
	for i := 0; i < 10; i++ {
		root.conv.Add(bigCompactMessage(convo.RoleUser, 50))
		root.conv.Add(bigCompactMessage(convo.RoleAssistant, 50))
	}
	beforeCount := len(root.conv.Messages)

	m, cmd := root.startCompact("")
	done := cmd().(compactDoneMsg)
	if done.err == nil {
		t.Fatal("fakeErrStreamer should have produced an error")
	}

	final, _ := m.(Root).finishCompact(done.summary, done.err)
	got := final.(Root)
	if got.mode != ModeChat {
		t.Fatalf("mode = %v, want ModeChat", got.mode)
	}
	if len(got.conv.Messages) != beforeCount {
		t.Errorf("len(Messages) = %d, want unchanged %d: a surfaced warning must not touch history", len(got.conv.Messages), beforeCount)
	}
	if len(got.transcript) != 1 {
		t.Fatalf("expected one warning notice, got %v", got.transcript)
	}
	if !strings.Contains(got.transcript[0].text, "compactación fallida") {
		t.Errorf("notice should say the compaction failed, got %q", got.transcript[0].text)
	}
	if !strings.Contains(got.transcript[0].text, errBoom.Error()) {
		t.Errorf("notice should include the underlying error, got %q", got.transcript[0].text)
	}
}

// TestCancelCompactCancelsTheInFlightCallAndLeavesHistoryAlone covers
// esc/ctrl+c while ModeCompact (§9.8): the in-flight context must be
// cancelled and ModeChat restored, with the conversation exactly as it was
// before compaction started — there is no partial summary worth keeping.
func TestCancelCompactCancelsTheInFlightCallAndLeavesHistoryAlone(t *testing.T) {
	root := newHeadlessRoot()
	root.conv.Add(convo.User("hola"))
	beforeCount := len(root.conv.Messages)

	cancelled := false
	root.compactCancel = func() { cancelled = true }
	root.compact = compactState{switchTo: "x", plan: convo.Plan{Replace: []int{0}}, before: 10}
	root.mode = ModeCompact

	m, cmd := root.cancelCompact()
	if cmd != nil {
		t.Errorf("cancelling is synchronous, want a nil cmd, got %v", cmd)
	}
	if !cancelled {
		t.Error("cancelCompact should call the stored cancel func")
	}
	got := m.(Root)
	if got.mode != ModeChat {
		t.Fatalf("mode = %v, want ModeChat after cancelling", got.mode)
	}
	if got.compactCancel != nil {
		t.Error("compactCancel should be cleared so a stale cancel func cannot be called twice")
	}
	if !got.compact.plan.Empty() || got.compact.switchTo != "" {
		t.Errorf("compact state should be reset, still has %+v", got.compact)
	}
	if len(got.conv.Messages) != beforeCount {
		t.Errorf("len(Messages) = %d, want unchanged %d: cancelling must not touch history", len(got.conv.Messages), beforeCount)
	}
}

// TestEscWhileModeCompactCancelsThroughTheRealKeyDispatch drives the same
// cancellation through the actual tea.Model.Update path (updateCompact's
// own key check), rather than calling cancelCompact directly, so a
// regression in updateCompact's key comparison would also be caught.
func TestEscWhileModeCompactCancelsThroughTheRealKeyDispatch(t *testing.T) {
	root := newHeadlessRoot()
	root.compactEng = engine.New(fakeCompactStreamer("no debería llegar."), 0)
	root.compactModel = "compact/model"
	for i := 0; i < 10; i++ {
		root.conv.Add(bigCompactMessage(convo.RoleUser, 50))
		root.conv.Add(bigCompactMessage(convo.RoleAssistant, 50))
	}
	beforeCount := len(root.conv.Messages)

	m, _ := root.startCompact("")
	if m.(Root).mode != ModeCompact {
		t.Fatalf("setup failed: mode = %v, want ModeCompact", m.(Root).mode)
	}

	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})

	got := m.(Root)
	if got.mode != ModeChat {
		t.Fatalf("mode = %v, want ModeChat after esc", got.mode)
	}
	if len(got.conv.Messages) != beforeCount {
		t.Errorf("len(Messages) = %d, want unchanged %d", len(got.conv.Messages), beforeCount)
	}

	// ctrl+c is handled one layer up, by handleGlobalKey, not by
	// updateCompact itself — exercised separately since it never reaches
	// the mode-specific switch at all.
	m2, _ := root.startCompact("")
	m2, _ = m2.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if m2.(Root).mode != ModeChat {
		t.Fatalf("mode = %v, want ModeChat after ctrl+c", m2.(Root).mode)
	}
}

// TestFinishTurnAutoTriggersCompactionWhenOverThreshold covers §10's
// auto-trigger: once a turn's own answer lands, finishTurn checks whether
// the conversation just crossed [compact].trigger_pct of the active
// model's window and starts a compaction on its own, without the user
// having to type /compact.
func TestFinishTurnAutoTriggersCompactionWhenOverThreshold(t *testing.T) {
	model := catalogModel("a/big", 2000, catalog.Caps{}, catalog.HealthOK)
	root := rootWithCatalog(catalogOf(model))
	root.model = model.Ref
	root.compactAuto = true
	root.compactTriggerPct = 50
	root.compactKeepLastTurns = 1
	root.compactEng = nil // exercise the drop-oldest fallback: synchronous, easy to assert on

	for i := 0; i < 20; i++ {
		root.conv.Add(bigCompactMessage(convo.RoleUser, 200))
		root.conv.Add(bigCompactMessage(convo.RoleAssistant, 200))
	}
	if !convo.NeedsCompact(root.conv.ContextTokens(), model.EffectiveContext(), root.compactTriggerPct) {
		t.Fatalf("setup failed: the conversation should already be over trigger_pct (context=%d, window=%d)",
			root.conv.ContextTokens(), model.EffectiveContext())
	}

	root.live.start(model.Ref)
	root.mode = ModeBusy

	m, _ := root.finishTurn(nil, false)
	got := m.(Root)
	if got.mode != ModeChat {
		t.Fatalf("mode = %v, want ModeChat: the no-engine fallback is synchronous", got.mode)
	}
	found := false
	for _, e := range got.transcript {
		if strings.Contains(e.text, "compactado") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected finishTurn to have triggered a compaction notice, got %v", got.transcript)
	}
}

// TestFinishTurnDoesNotAutoTriggerWhenCompactAutoIsOff covers the opposite
// of the previous test: [compact].auto = false must leave an oversized
// conversation alone even past trigger_pct, since the user has explicitly
// opted out of the automatic path.
func TestFinishTurnDoesNotAutoTriggerWhenCompactAutoIsOff(t *testing.T) {
	model := catalogModel("a/big", 2000, catalog.Caps{}, catalog.HealthOK)
	root := rootWithCatalog(catalogOf(model))
	root.model = model.Ref
	root.compactAuto = false
	root.compactTriggerPct = 50
	root.compactKeepLastTurns = 1

	for i := 0; i < 20; i++ {
		root.conv.Add(bigCompactMessage(convo.RoleUser, 200))
		root.conv.Add(bigCompactMessage(convo.RoleAssistant, 200))
	}
	beforeCount := len(root.conv.Messages)
	root.live.start(model.Ref)
	root.live.append("ok")
	root.mode = ModeBusy

	m, _ := root.finishTurn(nil, false)
	got := m.(Root)
	if got.mode != ModeChat {
		t.Fatalf("mode = %v, want ModeChat", got.mode)
	}
	// finishTurn itself appends exactly one assistant message for the
	// turn that just finished; no compaction marker should follow it.
	if len(got.conv.Messages) != beforeCount+1 {
		t.Errorf("len(Messages) = %d, want %d (the turn's own answer, nothing auto-compacted)",
			len(got.conv.Messages), beforeCount+1)
	}
}

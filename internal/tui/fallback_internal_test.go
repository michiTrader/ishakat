package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/MichiTrader/ishakat/internal/engine"
)

// fallback_internal_test.go covers §11 Phase 4's "automatic fallback to
// fallback_model if the active one fails twice in a row" — see root.go's
// checkFallback and finishTurn/agentturn.go's finishAgentTurn for the
// consecutiveFailures streak this exercises.

// TestFinishTurnTwoFailuresTriggersFallback is the core contract: two
// consecutive turns that end with err != nil switch m.model to
// fallbackModel, rebuild m.eng via engineFor, and leave a notice behind
// naming both the model that failed and the one just switched to.
func TestFinishTurnTwoFailuresTriggersFallback(t *testing.T) {
	factory, calls := trackingFactory(t)
	root := newHeadlessRoot()
	root.engineFor = factory
	root.model = "omni/son45"
	root.fallbackModel = "google/flash"
	originalEng := root.eng

	root.live.start("omni/son45")
	m, _ := root.finishTurn(errors.New("connection refused"), false)
	got := m.(Root)
	if got.consecutiveFailures != 1 {
		t.Fatalf("consecutiveFailures = %d after one failure, want 1", got.consecutiveFailures)
	}
	if got.model != "omni/son45" {
		t.Fatalf("model switched after only one failure: %q", got.model)
	}
	if len(*calls) != 0 {
		t.Fatalf("engineFor called after only one failure: %v", *calls)
	}

	got.live.start("omni/son45")
	m2, _ := got.finishTurn(errors.New("connection refused"), false)
	got2 := m2.(Root)

	if got2.model != "google/flash" {
		t.Fatalf("model = %q, want the fallback ref after the second consecutive failure", got2.model)
	}
	if got2.footer.Model != "google/flash" {
		t.Fatalf("footer.Model = %q, want the fallback ref", got2.footer.Model)
	}
	if got2.consecutiveFailures != 0 {
		t.Fatalf("consecutiveFailures = %d after the switch fired, want reset to 0", got2.consecutiveFailures)
	}
	if len(*calls) != 1 || (*calls)[0] != "google/flash" {
		t.Fatalf("engineFor calls = %v, want exactly one call for the fallback ref", *calls)
	}
	if got2.eng == originalEng {
		t.Fatal("eng must be rebuilt for the fallback provider, not left pointing at the failed one")
	}
	found := false
	for _, e := range got2.transcript {
		if strings.Contains(e.text, "omni/son45") && strings.Contains(e.text, "google/flash") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a transcript notice naming both models, got %v", got2.transcript)
	}
}

// TestFinishTurnSuccessResetsTheStreak covers the reset half of the
// contract: a turn that closes with err == nil (a normal, working answer)
// must zero the streak, so one failure followed by one success followed by
// one more failure never accumulates into a switch.
func TestFinishTurnSuccessResetsTheStreak(t *testing.T) {
	root := newHeadlessRoot()
	root.fallbackModel = "google/flash"
	root.model = "omni/son45"

	root.live.start("omni/son45")
	m, _ := root.finishTurn(errors.New("boom"), false)
	got := m.(Root)
	if got.consecutiveFailures != 1 {
		t.Fatalf("consecutiveFailures = %d, want 1", got.consecutiveFailures)
	}

	got.live.start("omni/son45")
	m2, _ := got.finishTurn(nil, false)
	got2 := m2.(Root)
	if got2.consecutiveFailures != 0 {
		t.Fatalf("consecutiveFailures = %d after a clean turn, want reset to 0", got2.consecutiveFailures)
	}
	if got2.model != "omni/son45" {
		t.Fatalf("model = %q, a reset streak must never switch it", got2.model)
	}
}

// TestFinishTurnAbortedDoesNotCountAsAFailure covers the same reset for
// the user's own cancellation (§7.4): esc/ctrl+c is never the model's
// fault, so it must not extend the streak either.
func TestFinishTurnAbortedDoesNotCountAsAFailure(t *testing.T) {
	root := newHeadlessRoot()
	root.fallbackModel = "google/flash"
	root.model = "omni/son45"
	root.consecutiveFailures = 1

	root.live.start("omni/son45")
	root.live.aborted = true
	m, _ := root.finishTurn(nil, true)
	got := m.(Root)
	if got.consecutiveFailures != 0 {
		t.Fatalf("consecutiveFailures = %d after a cancellation, want reset to 0", got.consecutiveFailures)
	}
}

// TestCheckFallbackIsANoOpWithNoFallbackConfigured is the "nothing wired,
// nothing happens" guarantee: fallbackModel == "" (defaults.toml's
// documented meaning) must never switch anything, no matter how many
// failures pile up.
func TestCheckFallbackIsANoOpWithNoFallbackConfigured(t *testing.T) {
	root := newHeadlessRoot()
	root.model = "omni/son45"
	root.consecutiveFailures = 2

	got, cmd := root.checkFallback()
	m := got.(Root)
	if cmd != nil {
		t.Fatal("checkFallback must return a nil Cmd when there is nothing to do")
	}
	if m.model != "omni/son45" {
		t.Fatalf("model = %q, want unchanged with fallbackModel empty", m.model)
	}
	if m.consecutiveFailures != 2 {
		t.Fatalf("consecutiveFailures = %d, want left untouched when the check is a no-op", m.consecutiveFailures)
	}
}

// TestCheckFallbackIsANoOpWhenFallbackEqualsActiveModel covers the other
// documented no-op: a fallback_model that is already the active model
// (e.g. the fallback itself is the one failing) has nothing to switch to.
func TestCheckFallbackIsANoOpWhenFallbackEqualsActiveModel(t *testing.T) {
	root := newHeadlessRoot()
	root.model = "google/flash"
	root.fallbackModel = "google/flash"
	root.consecutiveFailures = 2

	got, _ := root.checkFallback()
	m := got.(Root)
	if m.model != "google/flash" {
		t.Fatalf("model = %q, want unchanged", m.model)
	}
	if m.consecutiveFailures != 2 {
		t.Fatalf("consecutiveFailures = %d, want untouched by a no-op check", m.consecutiveFailures)
	}
}

// TestCheckFallbackReportsAFactoryErrorButStillSwitchesTheLabel mirrors
// applyModelChosen's own failed-rebuild behaviour (engineswitch_internal_
// test.go): if the fallback provider is itself unusable, the label still
// switches (so the footer/next turn are honest about what is active) but
// the notice becomes a double warning instead of a plain confirmation.
func TestCheckFallbackReportsAFactoryErrorButStillSwitchesTheLabel(t *testing.T) {
	wantErr := errors.New(`provider "google" is not declared`)
	root := newHeadlessRoot()
	root.model = "omni/son45"
	root.fallbackModel = "google/flash"
	root.consecutiveFailures = 2
	root.engineFor = failingFactory(wantErr)
	originalEng := root.eng

	got, _ := root.checkFallback()
	m := got.(Root)
	if m.model != "google/flash" {
		t.Fatalf("model = %q, want the fallback ref even when the rebuild failed", m.model)
	}
	if m.eng != originalEng {
		t.Fatal("a failed fallback rebuild must leave the previous engine in place")
	}
	if len(m.transcript) != 1 || !strings.Contains(m.transcript[0].text, wantErr.Error()) {
		t.Fatalf("expected a notice naming the factory error, got %v", m.transcript)
	}
}

// TestFinishAgentTurnTracksTheSameStreak is finishAgentTurn's own half of
// the contract (agentturn.go) — the tools-enabled path must feed the exact
// same consecutiveFailures counter finishTurn does, or a session with
// [tools].enabled = true would never trip the fallback at all.
func TestFinishAgentTurnTracksTheSameStreak(t *testing.T) {
	root := newHeadlessRoot()
	root.fallbackModel = "google/flash"
	root.model = "omni/son45"
	root.live.start("omni/son45")

	m, _ := root.finishAgentTurn(engine.AgentResult{}, errors.New("boom"))
	got := m.(Root)
	if got.consecutiveFailures != 1 {
		t.Fatalf("consecutiveFailures = %d after one agent-turn failure, want 1", got.consecutiveFailures)
	}

	got.live.start("omni/son45")
	m2, _ := got.finishAgentTurn(engine.AgentResult{Text: "ok"}, nil)
	got2 := m2.(Root)
	if got2.model != "omni/son45" {
		t.Fatalf("model = %q, want unchanged: a clean second turn must have reset the streak, never switching after only one failure", got2.model)
	}
	if got2.consecutiveFailures != 0 {
		t.Fatalf("consecutiveFailures = %d, want 0 after a clean agent turn", got2.consecutiveFailures)
	}
}

// TestFinishAgentTurnStoppedDoesNotCountAsAFailure covers result.Stopped
// (a cap/budget/loop-detection stop, agentturn.go's own comment): the loop
// ended on purpose, which says nothing about whether the provider itself
// is failing, so it must not extend the streak.
func TestFinishAgentTurnStoppedDoesNotCountAsAFailure(t *testing.T) {
	root := newHeadlessRoot()
	root.fallbackModel = "google/flash"
	root.model = "omni/son45"
	root.consecutiveFailures = 1
	root.live.start("omni/son45")

	m, _ := root.finishAgentTurn(engine.AgentResult{Stopped: "tool budget exhausted"}, nil)
	got := m.(Root)
	if got.consecutiveFailures != 0 {
		t.Fatalf("consecutiveFailures = %d after a Stopped turn, want reset to 0", got.consecutiveFailures)
	}
}

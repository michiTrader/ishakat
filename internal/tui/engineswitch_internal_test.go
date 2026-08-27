package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/engine"
)

// engineswitch_internal_test.go regression-tests the bug reported live: a
// model switch from /model, the picker, /resume or the confirm dialog's
// remedies used to relabel m.model/m.footer.Model without ever rebuilding
// m.eng, so every subsequent turn kept going out over whatever provider
// app.default_model had bound at startup — silently the wrong provider's
// base_url and credentials, not a missing one. See root.go's commitModelSwitch
// and engine.go's switchEngine/EngineFactory for the fix.

// trackingFactory returns an EngineFactory that records every ref it was
// asked to build for, and hands back a distinct *engine.Engine (via
// echoEngine) each time so a test can tell "the same engine as before" from
// "a freshly built one" by identity.
func trackingFactory(t *testing.T) (EngineFactory, *[]string) {
	t.Helper()
	var calls []string
	factory := func(ref string) (*engine.Engine, error) {
		calls = append(calls, ref)
		eng, _ := echoEngine(false)
		return eng, nil
	}
	return factory, &calls
}

// failingFactory always returns err, simulating a destination provider that
// is disabled, undeclared, or missing its API key — the exact failures
// internal/app.NewProvider already names.
func failingFactory(err error) EngineFactory {
	return func(string) (*engine.Engine, error) {
		return nil, err
	}
}

// TestModelSwitchViaPickerRebuildsTheEngine is the picker-path regression:
// choosing a model from /model's picker must call engineFor for the
// destination ref and swap m.eng, not just relabel m.model.
func TestModelSwitchViaPickerRebuildsTheEngine(t *testing.T) {
	factory, calls := trackingFactory(t)
	root := rootWithCatalog(catalogWithModels("omni/son45", "google/models/gemini-3.1-flash-lite"))
	root.engineFor = factory
	root.model = "omni/son45"
	originalEng := root.eng

	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	for _, r := range "gemini" {
		m, _ = m.Update(tea.KeyPressMsg{Text: string(r), Code: r})
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	var cmd tea.Cmd
	m, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter on a model row should return the modelChosenMsg command")
	}
	m, _ = m.Update(cmd())

	got := m.(Root)
	if got.model != "google/models/gemini-3.1-flash-lite" {
		t.Fatalf("model = %q, want the gemini ref", got.model)
	}
	if len(*calls) != 1 || (*calls)[0] != "google/models/gemini-3.1-flash-lite" {
		t.Fatalf("engineFor calls = %v, want exactly one call for the destination ref", *calls)
	}
	if got.eng == originalEng {
		t.Fatal("m.eng must be a freshly built engine after the switch, not the one from before it")
	}
}

// TestApplyModelChosenDirectlyRebuildsTheEngine covers /model's own
// direct-resolution branch (typing "/model <ref>" with no picker involved),
// which funnels through applyModelChosen exactly like the picker does.
func TestApplyModelChosenDirectlyRebuildsTheEngine(t *testing.T) {
	factory, calls := trackingFactory(t)
	root := rootWithCatalog(catalogWithModels("omni/son45", "google/models/gemini-3.1-flash-lite"))
	root.engineFor = factory
	root.model = "omni/son45"
	originalEng := root.eng

	got, _ := root.applyModelChosen("google/models/gemini-3.1-flash-lite")
	m := got.(Root)

	if m.model != "google/models/gemini-3.1-flash-lite" {
		t.Fatalf("model = %q, want the gemini ref", m.model)
	}
	if len(*calls) != 1 {
		t.Fatalf("engineFor calls = %v, want exactly one call", *calls)
	}
	if m.eng == originalEng {
		t.Fatal("m.eng must be rebuilt, not left pointing at the old provider's engine")
	}
}

// TestApplyModelChosenSurfacesAFactoryErrorButStillRelabels reproduces the
// reported symptom in miniature: the destination provider is unusable (bad
// key, disabled, undeclared). The label still switches — hiding the user's
// own choice would be a worse surprise — but the confirmation line becomes a
// warning instead of pretending the switch fully succeeded, and the old
// engine must not be silently kept in place mislabeled as the new provider.
func TestApplyModelChosenSurfacesAFactoryErrorButStillRelabels(t *testing.T) {
	wantErr := errors.New(`provider "google" is not declared`)
	root := rootWithCatalog(catalogWithModels("omni/son45", "google/models/gemini-3.1-flash-lite"))
	root.engineFor = failingFactory(wantErr)
	root.model = "omni/son45"
	originalEng := root.eng

	got, _ := root.applyModelChosen("google/models/gemini-3.1-flash-lite")
	m := got.(Root)

	if m.model != "google/models/gemini-3.1-flash-lite" {
		t.Fatalf("model = %q, the picker's own choice should still be shown", m.model)
	}
	if m.eng != originalEng {
		t.Fatal("a failed rebuild must leave the previous engine in place, not nil or half-built")
	}
	if len(m.transcript) != 1 || !strings.Contains(m.transcript[0].text, wantErr.Error()) {
		t.Fatalf("expected a warning naming the factory error, got %v", m.transcript)
	}
}

// TestApplyModelChosenWithNilEngineForKeepsTheOldRelabelOnlyBehaviour is the
// backward-compatibility guarantee: every existing test in this package (and
// any caller with nothing wired) never sets engineFor, and must keep
// switching only the label, exactly as before this fix.
func TestApplyModelChosenWithNilEngineForKeepsTheOldRelabelOnlyBehaviour(t *testing.T) {
	root := rootWithCatalog(catalogWithModels("omni/son45", "google/models/gemini-3.1-flash-lite"))
	root.model = "omni/son45"
	originalEng := root.eng

	got, _ := root.applyModelChosen("google/models/gemini-3.1-flash-lite")
	m := got.(Root)

	if m.model != "google/models/gemini-3.1-flash-lite" {
		t.Fatalf("model = %q, want the gemini ref", m.model)
	}
	if m.eng != originalEng {
		t.Fatal("with no engineFor wired, eng must be left exactly as it was")
	}
}

// TestSwitchEngineNilFactoryIsANoOp is switchEngine's own unit-level
// contract: nil engineFor returns the Root unchanged and no error, which is
// what lets every pre-existing test in this package (built via
// newHeadlessRoot, which never sets engineFor) keep passing unmodified.
func TestSwitchEngineNilFactoryIsANoOp(t *testing.T) {
	root := newHeadlessRoot()
	originalEng := root.eng

	got, err := switchEngine(root, "anything/at-all")
	if err != nil {
		t.Fatalf("switchEngine with nil engineFor returned an error: %v", err)
	}
	if got.eng != originalEng {
		t.Fatal("switchEngine with nil engineFor must not touch eng")
	}
}

// TestSwitchEngineUsesTheFactoryResult confirms switchEngine actually
// commits whatever engineFor returns on success.
func TestSwitchEngineUsesTheFactoryResult(t *testing.T) {
	wantEng, _ := echoEngine(false)
	root := newHeadlessRoot()
	root.engineFor = func(ref string) (*engine.Engine, error) {
		if ref != "target/ref" {
			t.Fatalf("engineFor called with ref = %q, want %q", ref, "target/ref")
		}
		return wantEng, nil
	}

	got, err := switchEngine(root, "target/ref")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.eng != wantEng {
		t.Fatal("switchEngine must commit the factory's returned engine into m.eng")
	}
}

// TestSwitchEngineLeavesEngUnchangedOnError is the other half: a factory
// error must not corrupt eng (no nil, no partially-built value) — the whole
// point is that a failed switch is a clean warning, not a crash on the very
// next turn.
func TestSwitchEngineLeavesEngUnchangedOnError(t *testing.T) {
	wantErr := errors.New("boom")
	root := newHeadlessRoot()
	originalEng := root.eng
	root.engineFor = failingFactory(wantErr)

	got, err := switchEngine(root, "target/ref")
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	if got.eng != originalEng {
		t.Fatal("a failed switchEngine must leave eng exactly as it was")
	}
}

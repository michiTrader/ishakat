package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/catalog"
	"github.com/MichiTrader/ishakat/internal/convo"
	"github.com/MichiTrader/ishakat/internal/engine"
)

// fakeCompactStreamer answers every request with text as a single delta —
// exactly the shape engine.Summarize's own RunToCompletion call expects
// (one finished string, §12) — so the confirm-dialog tests below can drive
// a real round trip through ModeCompact instead of falling into the
// no-engine drop-oldest fallback startCompact takes when root.compactEng is
// nil (see rootWithCatalog, which never sets it).
func fakeCompactStreamer(text string) engine.Streamer {
	return func(ctx context.Context, req engine.Request) (<-chan engine.Event, error) {
		ch := make(chan engine.Event, 2)
		ch <- engine.Event{Kind: engine.EventDelta, Text: text}
		ch <- engine.Event{Kind: engine.EventDone}
		close(ch)
		return ch, nil
	}
}

// catalogModel builds a catalog.Model directly, for the confirm-dialog tests
// below that need specific Context/Caps/Health values catalogWithModels'
// bare-ref shortcut does not carry.
func catalogModel(ref string, context int, caps catalog.Caps, health catalog.Health) catalog.Model {
	provider, wireID, _ := catalog.SplitRef(ref)
	return catalog.Model{Ref: ref, Provider: provider, WireID: wireID, Context: context, Caps: caps, Health: health}
}

func catalogOf(models ...catalog.Model) *catalog.Catalog {
	return &catalog.Catalog{Models: models}
}

// bigConfirmMessage mirrors internal/engine's own bigMessage test helper: a
// user turn whose estimated cost is roughly n tokens, built from repeated
// prose so the ~4 chars/token heuristic lands close to the target.
func bigConfirmMessage(role convo.Role, approxTokens int) convo.Message {
	return convo.NewMessage(role, convo.TextBlock(strings.Repeat("hola mundo ", approxTokens/2+1)))
}

func TestConfirmOpensOnContextConflictAndOffersCompactFirst(t *testing.T) {
	from := catalogModel("a/big", 200_000, catalog.Caps{}, catalog.HealthOK)
	to := catalogModel("b/small", 128_000, catalog.Caps{}, catalog.HealthOK)

	root := rootWithCatalog(catalogOf(from, to))
	root.model = from.Ref
	for i := 0; i < 20; i++ {
		root.conv.Add(bigConfirmMessage(convo.RoleUser, 3500))
		root.conv.Add(bigConfirmMessage(convo.RoleAssistant, 3500))
	}

	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/model "+to.Ref)

	got := m.(Root)
	if got.mode != ModeConfirm {
		t.Fatalf("mode = %v, want ModeConfirm: the conversation does not fit in %s's window", got.mode, to.Ref)
	}
	if got.model != from.Ref {
		t.Errorf("model = %q, want unchanged %q until the dialog resolves", got.model, from.Ref)
	}
	if len(got.confirm.options) != 3 {
		t.Fatalf("options = %v, want 3 rows for a context conflict", got.confirm.options)
	}
	if got.confirm.options[0].action != engine.ActionCompact {
		t.Errorf("options[0].action = %v, want ActionCompact first, matching the wireframe's default pointer", got.confirm.options[0].action)
	}
	if !strings.Contains(m.View().Content, "compactar") {
		t.Errorf("rendered dialog should mention compacting, got:\n%s", m.View().Content)
	}
}

func TestConfirmAcceptingCompactSwitchesAndShrinksTheConversation(t *testing.T) {
	from := catalogModel("a/big", 200_000, catalog.Caps{}, catalog.HealthOK)
	to := catalogModel("b/small", 128_000, catalog.Caps{}, catalog.HealthOK)

	root := rootWithCatalog(catalogOf(from, to))
	root.model = from.Ref
	// A real compact engine (Step 12): without one, startCompact skips
	// straight to the drop-oldest fallback (see fakeCompactStreamer's own
	// comment), which is a different code path than this test means to
	// cover.
	root.compactEng = engine.New(fakeCompactStreamer("resumen de prueba."), 0)
	root.compactModel = "compact/model"
	for i := 0; i < 20; i++ {
		root.conv.Add(bigConfirmMessage(convo.RoleUser, 3500))
		root.conv.Add(bigConfirmMessage(convo.RoleAssistant, 3500))
	}
	before := root.conv.ContextTokens()

	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/model "+to.Ref)
	if m.(Root).mode != ModeConfirm {
		t.Fatalf("setup failed: mode = %v, want ModeConfirm", m.(Root).mode)
	}

	// The compact row is selected by default (index 0): enter starts the
	// async compaction (Step 12), which opens ModeCompact until
	// compact_model answers instead of finishing the switch on the spot.
	m, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.(Root).mode != ModeCompact {
		t.Fatalf("mode = %v, want ModeCompact while compact_model answers", m.(Root).mode)
	}
	if cmd == nil {
		t.Fatal("accepting compact should schedule the summarize call")
	}
	done, ok := cmd().(compactDoneMsg)
	if !ok {
		t.Fatalf("expected a compactDoneMsg from the scheduled command, got %T", cmd())
	}
	if done.err != nil {
		t.Fatalf("fakeCompactStreamer should not fail: %v", done.err)
	}
	m, _ = m.Update(done)

	got := m.(Root)
	if got.mode != ModeChat {
		t.Fatalf("mode = %v, want ModeChat after the summary lands", got.mode)
	}
	if got.model != to.Ref {
		t.Errorf("model = %q, want %q", got.model, to.Ref)
	}
	if after := got.conv.ContextTokens(); after >= to.EffectiveContext() {
		t.Errorf("context after compacting = %d, want it under %d", after, to.EffectiveContext())
	}
	if after := got.conv.ContextTokens(); after >= before {
		t.Errorf("context after compacting = %d, want it smaller than the original %d", after, before)
	}
	// One notice for the §9.8 "compactado: ... tokens" line, one for the
	// §4.6 "── now: ... ──" switch confirmation — startCompact's
	// reportCompactDone and finishSwitchAfterCompact each append their own.
	if len(got.transcript) != 2 {
		t.Fatalf("expected two notices (compaction summary + model switch), got %v", got.transcript)
	}
	if !strings.Contains(got.transcript[0].text, "compactado") {
		t.Errorf("first notice should report the compaction, got %q", got.transcript[0].text)
	}
	if !strings.Contains(got.transcript[1].text, to.Ref) {
		t.Errorf("second notice should confirm the switch to %q, got %q", to.Ref, got.transcript[1].text)
	}
	// The summary text itself has to be the one the fake model answered
	// with, not a placeholder or a discard marker.
	last := got.conv.Messages[len(got.conv.Messages)-1]
	if !last.Has(convo.BlockSummary) {
		t.Fatalf("expected the last message to carry a BlockSummary after compacting, got %+v", last)
	}
	if got, want := last.Text(), "resumen de prueba."; got != want {
		t.Errorf("summary text = %q, want %q (the fake model's own answer)", got, want)
	}
}

func TestConfirmDropOldestDiscardsTheOldestMessages(t *testing.T) {
	from := catalogModel("a/big", 200_000, catalog.Caps{}, catalog.HealthOK)
	to := catalogModel("b/small", 128_000, catalog.Caps{}, catalog.HealthOK)

	root := rootWithCatalog(catalogOf(from, to))
	root.model = from.Ref
	for i := 0; i < 20; i++ {
		root.conv.Add(bigConfirmMessage(convo.RoleUser, 3500))
		root.conv.Add(bigConfirmMessage(convo.RoleAssistant, 3500))
	}
	beforeCount := len(root.conv.Messages)

	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/model "+to.Ref)

	// Move down once to reach "cambiar y recortar los turnos más viejos".
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	got := m.(Root)
	if got.mode != ModeChat {
		t.Fatalf("mode = %v, want ModeChat after accepting drop-oldest", got.mode)
	}
	if got.model != to.Ref {
		t.Errorf("model = %q, want %q", got.model, to.Ref)
	}
	if after := got.conv.ContextTokens(); after >= to.EffectiveContext() {
		t.Errorf("context after dropping the oldest turns = %d, want it under %d", after, to.EffectiveContext())
	}
	// Nothing is deleted from Messages (§10's auditability rule) — a marker
	// is appended, same shape as compaction.
	if got := len(got.conv.Messages); got != beforeCount+1 {
		t.Errorf("len(Messages) = %d, want %d (original history plus one discard marker)", got, beforeCount+1)
	}
}

func TestConfirmEscCancelsWithoutTouchingTheModelOrTheConversation(t *testing.T) {
	from := catalogModel("a/big", 200_000, catalog.Caps{}, catalog.HealthOK)
	to := catalogModel("b/small", 128_000, catalog.Caps{}, catalog.HealthOK)

	root := rootWithCatalog(catalogOf(from, to))
	root.model = from.Ref
	for i := 0; i < 20; i++ {
		root.conv.Add(bigConfirmMessage(convo.RoleUser, 3500))
		root.conv.Add(bigConfirmMessage(convo.RoleAssistant, 3500))
	}
	beforeCount := len(root.conv.Messages)

	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/model "+to.Ref)
	if m.(Root).mode != ModeConfirm {
		t.Fatalf("setup failed: mode = %v, want ModeConfirm", m.(Root).mode)
	}

	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})

	got := m.(Root)
	if got.mode != ModeChat {
		t.Fatalf("mode = %v, want ModeChat after esc", got.mode)
	}
	if got.model != from.Ref {
		t.Errorf("model = %q, want unchanged %q after cancelling", got.model, from.Ref)
	}
	if len(got.conv.Messages) != beforeCount {
		t.Errorf("len(Messages) = %d, want unchanged %d after cancelling", len(got.conv.Messages), beforeCount)
	}
	// Cancelling has to actually clear the dialog's state (confirmDialog{}),
	// not just leave ModeChat with a stale Plan sitting around for the next
	// /model attempt — or a future accidental read — to trip over.
	if len(got.confirm.options) != 0 {
		t.Errorf("confirm state should be reset after cancelling, still has %d options", len(got.confirm.options))
	}
}

func TestConfirmSelectingCancelRowAlsoCancels(t *testing.T) {
	from := catalogModel("a/big", 200_000, catalog.Caps{}, catalog.HealthOK)
	to := catalogModel("b/small", 128_000, catalog.Caps{}, catalog.HealthOK)

	root := rootWithCatalog(catalogOf(from, to))
	root.model = from.Ref
	for i := 0; i < 20; i++ {
		root.conv.Add(bigConfirmMessage(convo.RoleUser, 3500))
		root.conv.Add(bigConfirmMessage(convo.RoleAssistant, 3500))
	}

	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/model "+to.Ref)

	// Two downs from the default (index 0) land on "cancelar", the third
	// row of a context-conflict dialog.
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	got := m.(Root)
	if got.mode != ModeChat {
		t.Fatalf("mode = %v, want ModeChat after choosing cancel", got.mode)
	}
	if got.model != from.Ref {
		t.Errorf("model = %q, want unchanged %q", got.model, from.Ref)
	}
}

func TestConfirmNoAuthOnlyOffersCancel(t *testing.T) {
	from := catalogModel("a/one", 100_000, catalog.Caps{}, catalog.HealthOK)
	to := catalogModel("b/two", 100_000, catalog.Caps{}, catalog.HealthUnauthenticated)

	root := rootWithCatalog(catalogOf(from, to))
	root.model = from.Ref

	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/model "+to.Ref)

	got := m.(Root)
	if got.mode != ModeConfirm {
		t.Fatalf("mode = %v, want ModeConfirm: %s has no credential", got.mode, to.Ref)
	}
	if len(got.confirm.options) != 1 {
		t.Fatalf("options = %v, want exactly one row (cancel): a missing credential has no mechanical remedy", got.confirm.options)
	}
	if got.confirm.options[0].action != engine.ActionCancel {
		t.Errorf("options[0].action = %v, want ActionCancel", got.confirm.options[0].action)
	}

	// Accepting the only row cancels — there is nothing else it could mean.
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.(Root).mode != ModeChat {
		t.Fatalf("mode = %v, want ModeChat", m.(Root).mode)
	}
	if m.(Root).model != from.Ref {
		t.Errorf("model = %q, want unchanged %q: no credential means no switch", m.(Root).model, from.Ref)
	}
}

func TestConfirmMissingCapsOffersSwitchAnywayAndItActuallySwitches(t *testing.T) {
	from := catalogModel("a/vision", 100_000, catalog.Caps{Vision: true}, catalog.HealthOK)
	to := catalogModel("b/textonly", 100_000, catalog.Caps{}, catalog.HealthOK)

	root := rootWithCatalog(catalogOf(from, to))
	root.model = from.Ref
	root.conv.Add(convo.NewMessage(convo.RoleUser, convo.ImageBlock("image/png", []byte{1, 2, 3}, "foto.png")))
	beforeCount := len(root.conv.Messages)

	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/model "+to.Ref)

	got := m.(Root)
	if got.mode != ModeConfirm {
		t.Fatalf("mode = %v, want ModeConfirm: the history has an image and %s has no vision", got.mode, to.Ref)
	}
	if len(got.confirm.options) != 2 {
		t.Fatalf("options = %v, want 2 rows (switch anyway, cancel)", got.confirm.options)
	}
	if !got.confirm.options[0].proceed {
		t.Errorf("options[0] = %+v, want the default row to be 'switch anyway'", got.confirm.options[0])
	}
	if !strings.Contains(m.View().Content, "imágenes") {
		t.Errorf("dialog should warn about images specifically, got:\n%s", m.View().Content)
	}

	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	final := m.(Root)
	if final.mode != ModeChat {
		t.Fatalf("mode = %v, want ModeChat after switching anyway", final.mode)
	}
	if final.model != to.Ref {
		t.Errorf("model = %q, want %q", final.model, to.Ref)
	}
	// Switching past a caps warning does not touch the conversation itself
	// — there is nothing to compact or drop, only future turns degrade.
	if got := len(final.conv.Messages); got != beforeCount {
		t.Errorf("len(Messages) = %d, want unchanged %d: 'switch anyway' must not mutate history", got, beforeCount)
	}
}

// TestConfirmSkippedWhenEitherModelIsUnknownToTheCatalog preserves Step 10's
// unconditional-switch behaviour: CheckSwap needs both models to compare, so
// a reference the catalog cannot resolve (or no catalog at all — every
// pre-Step-11 test in this package) has nothing to conflict about.
func TestConfirmSkippedWhenEitherModelIsUnknownToTheCatalog(t *testing.T) {
	to := catalogModel("b/small", 1, catalog.Caps{}, catalog.HealthOK) // absurdly small window
	root := rootWithCatalog(catalogOf(to))
	root.model = "unknown/not-in-catalog"
	for i := 0; i < 20; i++ {
		root.conv.Add(bigConfirmMessage(convo.RoleUser, 3500))
	}

	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/model "+to.Ref)

	got := m.(Root)
	if got.mode != ModeChat {
		t.Fatalf("mode = %v, want ModeChat: the current model is not in the catalog, so CheckSwap cannot run", got.mode)
	}
	if got.model != to.Ref {
		t.Errorf("model = %q, want %q", got.model, to.Ref)
	}
}

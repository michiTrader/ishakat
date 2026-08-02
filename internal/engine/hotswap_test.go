package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/MichiTrader/ishakat/internal/catalog"
	"github.com/MichiTrader/ishakat/internal/convo"
)

func modelWith(ref string, context int, caps catalog.Caps, health catalog.Health) catalog.Model {
	providerID, wireID, _ := catalog.SplitRef(ref)
	return catalog.Model{
		Ref:      ref,
		Provider: providerID,
		WireID:   wireID,
		Context:  context,
		Caps:     caps,
		Health:   health,
	}
}

func TestCheckSwapWithNoConflictIsOK(t *testing.T) {
	from := modelWith("a/one", 200_000, catalog.Caps{}, catalog.HealthOK)
	to := modelWith("b/two", 200_000, catalog.Caps{}, catalog.HealthOK)
	var c convo.Conversation
	c.Add(convo.User("hola"))

	plan := CheckSwap(&c, from, to)
	if !plan.OK {
		t.Fatalf("plan.OK = false, want true (no conflicts): %+v", plan.Conflicts)
	}
	if len(plan.Conflicts) != 0 {
		t.Fatalf("plan.Conflicts = %v, want empty", plan.Conflicts)
	}
}

// TestCheckSwapNilConversationIsOK covers the "before the first turn" case:
// CheckSwap has to tolerate a nil *convo.Conversation without panicking.
func TestCheckSwapNilConversationIsOK(t *testing.T) {
	from := modelWith("a/one", 200_000, catalog.Caps{}, catalog.HealthOK)
	to := modelWith("b/two", 128_000, catalog.Caps{}, catalog.HealthOK)

	plan := CheckSwap(nil, from, to)
	if !plan.OK {
		t.Fatalf("plan.OK = false with a nil conversation, want true: %+v", plan.Conflicts)
	}
}

// bigMessage returns a user message whose estimated token cost is roughly n
// tokens, using convo's own ~4 chars/token latin-prose ratio so the test
// does not have to depend on EstimateText's internal constant directly.
func bigMessage(role convo.Role, approxTokens int) convo.Message {
	text := strings.Repeat("hola mundo ", approxTokens/2+1)
	return convo.NewMessage(role, convo.TextBlock(text))
}

// TestCheckSwapContextTooSmallOffersCompact is Step 11's own closing
// criterion (docs/PLAN.md §12): a conversation using ~142k tokens against a
// destination window of 128k has to report ContextTooSmall, suggest
// ActionCompact, and — this is the part that actually matters — accepting
// that suggestion has to leave a conversation whose *next* turn reaches the
// new model without the provider ever seeing something that does not fit.
func TestCheckSwapContextTooSmallOffersCompact(t *testing.T) {
	from := modelWith("a/big", 200_000, catalog.Caps{}, catalog.HealthOK)
	to := modelWith("b/small", 128_000, catalog.Caps{}, catalog.HealthOK)

	var c convo.Conversation
	// Twenty big turns comfortably clear 142k tokens under the ~4 chars/token
	// heuristic, then a couple of short, recent turns that defaultCompactKeepTurns
	// will keep untouched.
	for i := 0; i < 20; i++ {
		c.Add(bigMessage(convo.RoleUser, 3500))
		c.Add(bigMessage(convo.RoleAssistant, 3500))
	}
	c.Add(convo.User("uno más"))
	c.Add(convo.Assistant("listo", "a/big"))

	before := c.ContextTokens()
	if before <= to.EffectiveContext() {
		t.Fatalf("test setup is wrong: before=%d has to exceed to's window=%d", before, to.EffectiveContext())
	}

	plan := CheckSwap(&c, from, to)
	if plan.OK {
		t.Fatalf("plan.OK = true, want false: the conversation does not fit in %d", to.EffectiveContext())
	}
	if !plan.Has(ContextTooSmall) {
		t.Fatalf("plan.Conflicts = %v, want a ContextTooSmall entry", plan.Conflicts)
	}
	if plan.Suggested != ActionCompact {
		t.Fatalf("plan.Suggested = %v, want ActionCompact", plan.Suggested)
	}
	if plan.EstAfter <= 0 || plan.EstAfter >= before {
		t.Fatalf("plan.EstAfter = %d, want a positive estimate smaller than the current %d tokens", plan.EstAfter, before)
	}
	if plan.EstAfter >= to.EffectiveContext() {
		t.Fatalf("plan.EstAfter = %d, want it to actually fit under %d — otherwise accepting the suggestion would not fix anything", plan.EstAfter, to.EffectiveContext())
	}

	// Accept the suggestion: apply the same compaction plan estimateAfterCompact
	// scored, exactly the way internal/tui's confirm dialog will (a placeholder
	// summary text stands in for Step 12's real compact_model call).
	compactPlan := convo.PlanCompact(c.Messages, defaultCompactKeepTurns)
	if compactPlan.Empty() {
		t.Fatal("convo.PlanCompact returned nothing to replace — test setup is wrong")
	}
	c.ApplySummary(compactPlan, "(resumen provisional, Paso 12 aún no genera resúmenes reales)", "")

	after := c.ContextTokens()
	if after >= to.EffectiveContext() {
		t.Fatalf("after compacting, context = %d, still does not fit in %d", after, to.EffectiveContext())
	}

	// Re-checking against the now-compacted conversation has to come back OK:
	// nothing left to conflict about.
	if again := CheckSwap(&c, from, to); !again.OK {
		t.Fatalf("CheckSwap after compacting = %+v, want OK", again)
	}

	// And the actual point of the closing criterion: the *next* message
	// really does reach the new model. A fake Streamer stands in for the
	// provider — this package never talks to a real one.
	c.Add(convo.User("¿seguimos?"))

	var gotModel string
	var gotMessages int
	stream := func(ctx context.Context, req Request) (<-chan Event, error) {
		gotModel = req.Model
		gotMessages = len(req.Messages)
		return chanOf(
			Event{Kind: EventDelta, Text: "todo bien"},
			Event{Kind: EventDone},
		), nil
	}

	e := New(stream, 0)
	var buf StreamBuf
	e.Start(context.Background(), Request{
		Model:    to.Ref,
		Messages: c.Active(),
		System:   "",
	}, &buf)

	text, _, _, aborted, err := drainUntilDone(t, &buf, time.Second)
	if err != nil {
		t.Fatalf("turn against the new model failed: %v", err)
	}
	if aborted {
		t.Fatal("turn against the new model was reported aborted")
	}
	if text != "todo bien" {
		t.Fatalf("text = %q, want %q", text, "todo bien")
	}
	if gotModel != to.Ref {
		t.Fatalf("Streamer saw model %q, want %q", gotModel, to.Ref)
	}
	if gotMessages == 0 {
		t.Fatal("Streamer saw zero messages — the compacted history did not travel")
	}
}

func TestCheckSwapMissingCapsDetectsImageBlock(t *testing.T) {
	from := modelWith("a/vision", 100_000, catalog.Caps{Vision: true}, catalog.HealthOK)
	to := modelWith("b/textonly", 100_000, catalog.Caps{}, catalog.HealthOK)

	var c convo.Conversation
	c.Add(convo.NewMessage(convo.RoleUser, convo.ImageBlock("image/png", []byte{1, 2, 3}, "foto.png")))

	plan := CheckSwap(&c, from, to)
	if plan.OK {
		t.Fatalf("plan.OK = true, want false: the history has an image and `to` has no vision")
	}
	if !plan.Has(MissingCaps) {
		t.Fatalf("plan.Conflicts = %v, want a MissingCaps entry", plan.Conflicts)
	}
	for _, cf := range plan.Conflicts {
		if cf.Kind != MissingCaps {
			continue
		}
		if !cf.Missing.Vision {
			t.Errorf("Conflict.Missing = %+v, want Vision set", cf.Missing)
		}
		if cf.Missing.Tools {
			t.Errorf("Conflict.Missing = %+v, want Tools unset (no tool blocks in history)", cf.Missing)
		}
	}
}

func TestCheckSwapMissingCapsDetectsToolResultBlock(t *testing.T) {
	from := modelWith("a/tools", 100_000, catalog.Caps{Tools: true}, catalog.HealthOK)
	to := modelWith("b/notools", 100_000, catalog.Caps{}, catalog.HealthOK)

	var c convo.Conversation
	c.Add(convo.NewMessage(convo.RoleTool, convo.Block{Kind: convo.BlockToolResult, Name: "buscar", Text: "3 resultados"}))

	plan := CheckSwap(&c, from, to)
	if !plan.Has(MissingCaps) {
		t.Fatalf("plan.Conflicts = %v, want a MissingCaps entry", plan.Conflicts)
	}
}

// TestCheckSwapMissingCapsIgnoresFromModel confirms §4.6's own rule: what
// matters is what the history contains, not which model produced it. A
// destination that supports vision is never flagged even when `from` did
// not — there is nothing for it to fail to serve.
func TestCheckSwapMissingCapsIgnoresFromModel(t *testing.T) {
	from := modelWith("a/textonly", 100_000, catalog.Caps{}, catalog.HealthOK)
	to := modelWith("b/vision", 100_000, catalog.Caps{Vision: true}, catalog.HealthOK)

	var c convo.Conversation
	c.Add(convo.NewMessage(convo.RoleUser, convo.ImageBlock("image/png", []byte{1}, "x.png")))

	if plan := CheckSwap(&c, from, to); !plan.OK {
		t.Fatalf("plan = %+v, want OK: `to` supports vision", plan)
	}
}

func TestCheckSwapNoAuthBlocksTheSwitch(t *testing.T) {
	from := modelWith("a/one", 100_000, catalog.Caps{}, catalog.HealthOK)
	to := modelWith("b/two", 100_000, catalog.Caps{}, catalog.HealthUnauthenticated)

	var c convo.Conversation
	c.Add(convo.User("hola"))

	plan := CheckSwap(&c, from, to)
	if plan.OK {
		t.Fatal("plan.OK = true, want false: `to` has no resolved credential")
	}
	if !plan.Has(NoAuth) {
		t.Fatalf("plan.Conflicts = %v, want a NoAuth entry", plan.Conflicts)
	}
	if plan.Suggested != ActionCancel {
		t.Fatalf("plan.Suggested = %v, want ActionCancel: neither compacting nor dropping messages fixes a missing credential", plan.Suggested)
	}
}

// TestCheckSwapWithoutContextConflictSuggestsCancel covers a Plan whose only
// conflict is not ContextTooSmall: Suggested must not default to
// ActionCompact (the zero value would be misleading here — see
// hotswap.go's comment on Action's iota order).
func TestCheckSwapWithoutContextConflictSuggestsCancel(t *testing.T) {
	from := modelWith("a/tools", 100_000, catalog.Caps{Tools: true}, catalog.HealthOK)
	to := modelWith("b/notools", 100_000, catalog.Caps{}, catalog.HealthOK)

	var c convo.Conversation
	c.Add(convo.NewMessage(convo.RoleTool, convo.Block{Kind: convo.BlockToolResult, Name: "x", Text: "y"}))

	plan := CheckSwap(&c, from, to)
	if plan.Suggested != ActionCancel {
		t.Fatalf("plan.Suggested = %v, want ActionCancel", plan.Suggested)
	}
	if plan.EstAfter != 0 {
		t.Fatalf("plan.EstAfter = %d, want 0: no compaction was suggested", plan.EstAfter)
	}
}

func TestConflictKindAndActionString(t *testing.T) {
	cases := []struct {
		k    ConflictKind
		want string
	}{
		{ContextTooSmall, "context_too_small"},
		{MissingCaps, "missing_caps"},
		{NoAuth, "no_auth"},
		{ConflictKind(99), "unknown"},
	}
	for _, tc := range cases {
		if got := tc.k.String(); got != tc.want {
			t.Errorf("ConflictKind(%d).String() = %q, want %q", tc.k, got, tc.want)
		}
	}

	actions := []struct {
		a    Action
		want string
	}{
		{ActionCancel, "cancel"},
		{ActionCompact, "compact"},
		{ActionDropOldest, "drop_oldest"},
		{Action(99), "unknown"},
	}
	for _, tc := range actions {
		if got := tc.a.String(); got != tc.want {
			t.Errorf("Action(%d).String() = %q, want %q", tc.a, got, tc.want)
		}
	}
}

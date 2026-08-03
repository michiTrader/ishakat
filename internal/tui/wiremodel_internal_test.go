package tui

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/catalog"
	"github.com/MichiTrader/ishakat/internal/convo"
	"github.com/MichiTrader/ishakat/internal/engine"
)

// omniCatalog builds a one-model snapshot shaped like an OmniRoute entry:
// a Ref whose own tail contains a slash ("auto/coding"), which is exactly
// the case strings.Split(ref, "/") gets wrong and catalog.SplitRef's
// first-slash cut gets right.
func omniCatalog() *catalog.Catalog {
	return &catalog.Catalog{Models: []catalog.Model{
		{Ref: "omniroute/auto/coding", Provider: "omniroute", WireID: "auto/coding"},
	}}
}

// TestWireModelPrefersTheCatalog covers the common case: the catalog has an
// entry for ref, so its WireID wins outright — no splitting involved, and
// the multi-slash WireID ("auto/coding") comes back whole rather than cut
// at the wrong slash.
func TestWireModelPrefersTheCatalog(t *testing.T) {
	got := wireModel(omniCatalog(), "omniroute/auto/coding")
	if got != "auto/coding" {
		t.Fatalf("wireModel = %q, want %q (the catalog's WireID)", got, "auto/coding")
	}
}

// TestWireModelFallsBackToSplitRefWhenTheCatalogMisses covers a Ref the
// catalog does not know (no catalog fetched, or a model the fetch missed):
// the fallback is catalog.SplitRef's first-slash cut, the same one
// picker.go's renderPickerRow already relies on for display.
func TestWireModelFallsBackToSplitRefWhenTheCatalogMisses(t *testing.T) {
	got := wireModel(nil, "anthropic/claude-sonnet-4-5")
	if got != "claude-sonnet-4-5" {
		t.Fatalf("wireModel = %q, want %q (SplitRef's fallback)", got, "claude-sonnet-4-5")
	}
}

// TestWireModelFallsBackToTheRawRefWhenThereIsNoSlashToCut covers the
// degenerate case (a bare alias with no "/" at all): sending what the user
// actually typed is always a better failure than sending an empty string.
func TestWireModelFallsBackToTheRawRefWhenThereIsNoSlashToCut(t *testing.T) {
	got := wireModel(nil, "sonnet")
	if got != "sonnet" {
		t.Fatalf("wireModel = %q, want the ref unchanged (%q)", got, "sonnet")
	}
}

// spyStreamer is an engine.Streamer that records the Model field of every
// Request it receives and answers with a fixed one-shot text, which is all
// the two regression tests below need from it. engine.Engine.Start runs the
// Streamer in its own goroutine, so the write to *got races the test
// goroutine's read unless the caller waits on done first — closing done is
// the last thing this closure does before returning the answer channel, so
// by the time a caller has received from done, the write has already
// happened-before it in the memory model's sense, not just in wall time.
func spyStreamer(got *string, done chan<- struct{}) engine.Streamer {
	return func(ctx context.Context, req engine.Request) (<-chan engine.Event, error) {
		*got = req.Model
		close(done)
		out := make(chan engine.Event, 2)
		out <- engine.Event{Kind: engine.EventDelta, Text: "ok"}
		out <- engine.Event{Kind: engine.EventDone}
		close(out)
		return out, nil
	}
}

// TestStartEngineTurnSendsTheWireIDNotTheRef is the regression test for the
// bug the user hit with OmniRoute: submitting a message while m.model holds
// a Ref whose provider serves multi-segment wire ids ("omniroute/auto/coding")
// must reach the Streamer with the WireID ("auto/coding"), never the Ref —
// sending the Ref is exactly what made OmniRoute answer "no active
// credentials for provider: omniroute" instead of running the turn.
func TestStartEngineTurnSendsTheWireIDNotTheRef(t *testing.T) {
	var got string
	done := make(chan struct{})
	root := newHeadlessRoot()
	root.eng = engine.New(spyStreamer(&got, done), 0)
	root.cat = omniCatalog()
	root.model = "omniroute/auto/coding"
	root.footer.Model = root.model

	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "hola")
	<-done

	if got != "auto/coding" {
		t.Fatalf("engine.Request.Model = %q, want the WireID %q (got the Ref instead)", got, "auto/coding")
	}
}

// TestStartCompactSendsTheWireIDNotTheRef is startEngineTurn's regression
// test above, mirrored for /compact: compact_model is its own Ref
// (Root.compactModel), resolved independently of the main chat model, and
// startCompact must resolve it the same way before calling engine.Summarize.
func TestStartCompactSendsTheWireIDNotTheRef(t *testing.T) {
	var got string
	done := make(chan struct{})
	root := newHeadlessRoot()
	root.compactEng = engine.New(spyStreamer(&got, done), 0)
	root.compactModel = "omniroute/auto/coding"
	root.cat = omniCatalog()
	for i := 0; i < 10; i++ {
		root.conv.Add(bigCompactMessage(convo.RoleUser, 50))
		root.conv.Add(bigCompactMessage(convo.RoleAssistant, 50))
	}

	m, cmd := root.startCompact("")
	if cmd == nil {
		t.Fatal("startCompact with an engine and a non-empty plan should schedule the summarize call")
	}
	if _, ok := cmd().(compactDoneMsg); !ok {
		t.Fatalf("expected a compactDoneMsg, got %T", cmd())
	}
	_ = m

	if got != "auto/coding" {
		t.Fatalf("engine.Request.Model (via Summarize) = %q, want the WireID %q (got the Ref instead)", got, "auto/coding")
	}
}

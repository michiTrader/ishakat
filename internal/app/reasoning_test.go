// reasoning_test.go pins the layer whose absence caused the reported bug: the
// one that decides whether a turn asks the service for reasoning at all.
//
// Every other layer of this feature was already correct and already tested.
// The dialects emitted EventReasoning when a chunk carried reasoning, the
// engine collected it into AgentResult.Reasoning, and the interface rendered a
// two-line collapsible preview — each half verified against a fake on its own
// side of the seam. What nothing asserted was the request body: no layer ever
// set the flag that makes a thinking model narrate anything, so the answer was
// always the empty string and the preview correctly drew nothing. That is the
// same shape of failure as the Step 16 tools bug described in caps.go, and it
// is caught here for the same reason: by testing what actually goes out.
package app

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/convo"
	"github.com/MichiTrader/ishakat/internal/engine"
	"github.com/MichiTrader/ishakat/internal/provider"
	"github.com/MichiTrader/ishakat/internal/provider/fake"
)

// TestReasoningWantedReadsUIConfig pins the mapping from [ui].reasoning onto
// the request flag. The empty string is the case that matters most: it is what
// an unconfigured install has, and it must mean "collapsed" (the documented
// default), not "off". Reading it as off is what would leave the very user who
// reported this bug seeing nothing after the fix.
func TestReasoningWantedReadsUIConfig(t *testing.T) {
	for _, tc := range []struct {
		mode string
		want bool
	}{
		{"collapsed", true},
		{"full", true},
		{"", true}, // unset means the default, which is collapsed
		{"off", false},
		{"OFF", false},   // case-insensitive: TOML is written by humans
		{" off ", false}, // and humans leave whitespace
		{"nonsense", true},
	} {
		cfg := &config.Config{}
		cfg.UI.Reasoning = tc.mode
		if got := ReasoningWanted(cfg); got != tc.want {
			t.Errorf("ReasoningWanted(%q) = %v, want %v", tc.mode, got, tc.want)
		}
	}

	// A nil config is a caller with nothing configured at all; it must not
	// panic, and asking for nothing is the only safe answer available.
	if ReasoningWanted(nil) {
		t.Error("ReasoningWanted(nil) = true, want false")
	}
}

// TestStreamerAsksProviderForReasoning checks the one hop from NewStreamer's
// parameter into the provider.Request the dialect receives.
func TestStreamerAsksProviderForReasoning(t *testing.T) {
	for _, want := range []bool{true, false} {
		fp := fake.New("t", provider.Event{Kind: provider.EventDelta, Text: "hi"})

		streamer := NewStreamer(fp, provider.Caps{}, want)
		ch, err := streamer(context.Background(), engine.Request{Model: "m"})
		if err != nil {
			t.Fatalf("streamer handshake: %v", err)
		}
		for range ch { // drain so the turn completes
		}

		if got := fp.LastTurn().IncludeReasoning; got != want {
			t.Errorf("Request.IncludeReasoning = %v, want %v", got, want)
		}
	}
}

// TestBuildEngineNeverSendsGoogleFieldsToOtherServices is the blast-radius
// check at the level a user actually configures: a real turn against a real
// OpenAI-dialect server on a non-Google host, with reasoning asked for. The
// per-dialect wire shape is pinned inside internal/provider/openai; what this
// owns is that switching [ui].reasoning on cannot start decorating requests to
// every other service in the catalog.
func TestBuildEngineNeverSendsGoogleFieldsToOtherServices(t *testing.T) {
	var mu sync.Mutex
	var bodies []map[string]any

	srv := fake.SSEServer(fake.SSEOptions{
		Chunks: []string{fake.SSEDelta("hi"), fake.SSEDone()},
		OnRequest: func(_ *http.Request, body []byte) {
			var m map[string]any
			if err := json.Unmarshal(body, &m); err == nil {
				mu.Lock()
				bodies = append(bodies, m)
				mu.Unlock()
			}
		},
	})
	defer srv.Close()

	cfg := cfgFor(t, srv.URL) // cfgFor's provider is kind "openai", non-Google host
	cfg.UI.Reasoning = "full"

	eng, ref, _, _, err := BuildEngine(cfg, nil, "", "0.0.0-test", false)
	if err != nil {
		t.Fatalf("BuildEngine: %v", err)
	}

	var buf engine.StreamBuf
	eng.Start(context.Background(), engine.Request{
		Model:    ref.WireID,
		Messages: []convo.Message{convo.User("hi there")},
	}, &buf)
	if _, _, _, _, turnErr := drainEngineTest(t, &buf); turnErr != nil {
		t.Fatalf("turn error: %v", turnErr)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) == 0 {
		t.Fatal("the server received no request body to inspect")
	}
	if got, sent := bodies[0]["extra_body"]; sent {
		t.Errorf("extra_body must never reach a non-Google host, got %v", got)
	}
}

// captureKind is a provider kind registered once for this package's tests,
// recording the provider.Request each turn hands the dialect. It exists so a
// test can assert on the request a *real* BuildEngine produced, rather than on
// one the test assembled itself.
const captureKind = "capture-reasoning"

var captured struct {
	mu   sync.Mutex
	last provider.Request
}

func init() {
	provider.Register(captureKind, func(s provider.Settings) (provider.Provider, error) {
		return captureProvider{}, nil
	})
}

type captureProvider struct{}

func (captureProvider) ID() string { return captureKind }

func (captureProvider) Discover(context.Context) ([]provider.RawModel, error) {
	return nil, nil
}

func (captureProvider) Stream(_ context.Context, req provider.Request) (<-chan provider.Event, error) {
	captured.mu.Lock()
	captured.last = req
	captured.mu.Unlock()

	ch := make(chan provider.Event, 2)
	ch <- provider.Event{Kind: provider.EventDelta, Text: "hi"}
	ch <- provider.Event{Kind: provider.EventDone}
	close(ch)
	return ch, nil
}

// TestBuildEngineThreadsReasoningToTheRequest is the regression guard for the
// reported bug, and it goes through the real BuildEngine on purpose.
//
// An earlier draft of this test rebuilt BuildEngine's own two lines (CapsFor +
// NewStreamer) and asserted against a fake. It passed even when BuildEngine was
// mutated back to hard-coding false, because it was testing a copy of the
// wiring rather than the wiring — which is exactly the flaw that let the
// original bug ship: every layer verified in isolation, nothing checking what
// the assembled program actually sends. Driving BuildEngine and inspecting the
// provider.Request it caused is what makes this catch a regression.
func TestBuildEngineThreadsReasoningToTheRequest(t *testing.T) {
	for _, tc := range []struct {
		mode string
		want bool
	}{
		{"collapsed", true},
		{"full", true},
		{"", true}, // an unconfigured install must still show reasoning
		{"off", false},
	} {
		t.Run("mode="+tc.mode, func(t *testing.T) {
			captured.mu.Lock()
			captured.last = provider.Request{}
			captured.mu.Unlock()

			cfg := cfgFor(t, "http://127.0.0.1:1") // never dialled: the kind is synthetic
			cfg.UI.Reasoning = tc.mode
			cfg.Providers[0].Kind = captureKind

			eng, ref, _, _, err := BuildEngine(cfg, nil, "", "0.0.0-test", false)
			if err != nil {
				t.Fatalf("BuildEngine: %v", err)
			}

			var buf engine.StreamBuf
			eng.Start(context.Background(), engine.Request{
				Model:    ref.WireID,
				Messages: []convo.Message{convo.User("hi there")},
			}, &buf)
			if _, _, _, _, turnErr := drainEngineTest(t, &buf); turnErr != nil {
				t.Fatalf("turn error: %v", turnErr)
			}

			captured.mu.Lock()
			got := captured.last.IncludeReasoning
			captured.mu.Unlock()

			if got != tc.want {
				t.Errorf("with [ui].reasoning = %q, Request.IncludeReasoning = %v, want %v",
					tc.mode, got, tc.want)
			}
		})
	}
}

// TestNewEngineFactoryThreadsReasoning covers the second construction path.
// Switching models mid-session rebuilds the engine through NewEngineFactory, so
// if only BuildEngine were wired the reasoning preview would work at startup
// and vanish after the first ctrl+p — the "works after a restart but not from
// the picker" failure NewEngineFactory's own doc comment warns about.
func TestNewEngineFactoryThreadsReasoning(t *testing.T) {
	for _, tc := range []struct {
		mode string
		want bool
	}{
		{"collapsed", true},
		{"off", false},
	} {
		t.Run("mode="+tc.mode, func(t *testing.T) {
			captured.mu.Lock()
			captured.last = provider.Request{}
			captured.mu.Unlock()

			cfg := cfgFor(t, "http://127.0.0.1:1")
			cfg.UI.Reasoning = tc.mode
			cfg.Providers[0].Kind = captureKind

			factory := NewEngineFactory(cfg, nil, "0.0.0-test", false)
			eng, err := factory("omniroute/some-model")
			if err != nil {
				t.Fatalf("factory: %v", err)
			}

			var buf engine.StreamBuf
			eng.Start(context.Background(), engine.Request{
				Model:    "some-model",
				Messages: []convo.Message{convo.User("hi")},
			}, &buf)
			if _, _, _, _, turnErr := drainEngineTest(t, &buf); turnErr != nil {
				t.Fatalf("turn error: %v", turnErr)
			}

			captured.mu.Lock()
			got := captured.last.IncludeReasoning
			captured.mu.Unlock()

			if got != tc.want {
				t.Errorf("after a model switch with [ui].reasoning = %q, IncludeReasoning = %v, want %v",
					tc.mode, got, tc.want)
			}
		})
	}
}

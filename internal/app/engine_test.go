package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/convo"
	"github.com/MichiTrader/ishakat/internal/engine"
	"github.com/MichiTrader/ishakat/internal/provider/fake"
)

func TestBuildEngineResolvesTheDefaultModelAndRunsARealTurn(t *testing.T) {
	srv := fake.SSEServer(fake.SSEOptions{Chunks: []string{
		fake.SSEDelta("hi"),
		fake.SSEDone(),
	}})
	defer srv.Close()

	cfg := cfgFor(t, srv.URL)

	eng, ref, system, warn, err := BuildEngine(cfg, nil, "", "0.0.0-test", false)
	if err != nil {
		t.Fatalf("BuildEngine returned an error: %v", err)
	}
	if ref.Ref != "omniroute/auto/coding" {
		t.Errorf("ref.Ref = %q, want %q", ref.Ref, "omniroute/auto/coding")
	}
	if system != "" {
		t.Errorf("system = %q, want empty (cfgFor sets no system_prompt)", system)
	}
	if warn != "" {
		t.Errorf("warn = %q, want empty", warn)
	}

	var buf engine.StreamBuf
	req := engine.Request{Model: ref.WireID, Messages: []convo.Message{convo.User("hi there")}}
	eng.Start(context.Background(), req, &buf)

	text, _, _, _, turnErr := drainEngineTest(t, &buf)
	if turnErr != nil {
		t.Errorf("turn error = %v, want nil", turnErr)
	}
	if text != "hi" {
		t.Errorf("text = %q, want %q", text, "hi")
	}
}

func TestBuildEngineHonoursTheModelFlagOverTheDefault(t *testing.T) {
	srv := fake.SSEServer(fake.SSEOptions{Chunks: []string{fake.SSEDone()}})
	defer srv.Close()

	cfg := cfgFor(t, srv.URL)
	_, ref, _, _, err := BuildEngine(cfg, nil, "omniroute/gpt-5", "0.0.0-test", false)
	if err != nil {
		t.Fatalf("BuildEngine returned an error: %v", err)
	}
	if ref.WireID != "gpt-5" {
		t.Errorf("ref.WireID = %q, want %q", ref.WireID, "gpt-5")
	}
}

func TestBuildEngineFailsWithoutAnEnabledProvider(t *testing.T) {
	cfg := &config.Config{Schema: config.Schema}
	if _, _, _, _, err := BuildEngine(cfg, nil, "", "0.0.0-test", false); err == nil {
		t.Fatal("BuildEngine with no providers configured should return an error, not a usable engine")
	}
}

// TestBuildEngineFallsBackAndWarnsWhenDefaultModelIsUnusable is P2's
// integration test at BuildEngine's own level (as opposed to
// ResolveModelForBoot's unit tests in modelref_test.go): app.default_model
// names a disabled provider, a second provider is usable, and BuildEngine
// must succeed by using it — with warn naming exactly what happened,
// rather than returning err and leaving the caller with eng = nil the way
// it did before P2.
func TestBuildEngineFallsBackAndWarnsWhenDefaultModelIsUnusable(t *testing.T) {
	srv := fake.SSEServer(fake.SSEOptions{Chunks: []string{fake.SSEDone()}})
	defer srv.Close()

	cfg := cfgFor(t, srv.URL)
	cfg.App.DefaultModel = "omniroute/auto/coding"
	// omniroute is now disabled; google (a real preset id, so
	// config.VerifyModelFor has a wire id for it) points at the same fake
	// server and is the one that must end up serving the turn.
	cfg.Providers[0].Enabled = false
	cfg.Providers = append(cfg.Providers, config.Provider{
		ID: "google", Kind: "openai", BaseURL: srv.URL,
		APIKey: "test-key", Enabled: true, AuthOK: true,
	})

	eng, ref, _, warn, err := BuildEngine(cfg, nil, "", "0.0.0-test", false)
	if err != nil {
		t.Fatalf("BuildEngine returned an error: %v", err)
	}
	if eng == nil {
		t.Fatal("eng is nil: the whole point of P2 is that a fallback still produces a usable engine")
	}
	if ref.Provider != "google" {
		t.Errorf("ref.Provider = %q, want %q", ref.Provider, "google")
	}
	if warn == "" || !strings.Contains(warn, "google") {
		t.Errorf("warn = %q, want it to name the fallback provider", warn)
	}
}

// drainEngineTest polls Drain the same way internal/engine's own tests do
// (see drainUntilDone in engine_test.go), without needing Bubble Tea's
// runtime.
func drainEngineTest(t *testing.T, buf *engine.StreamBuf) (text, reasoning string, usage any, aborted bool, err error) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		chunk, rChunk, u, done, isAborted, e := buf.Drain()
		text += chunk
		reasoning += rChunk
		if u != nil {
			usage = u
		}
		if done {
			return text, reasoning, usage, isAborted, e
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("turn never finished: the engine is stuck")
	return "", "", nil, false, nil
}

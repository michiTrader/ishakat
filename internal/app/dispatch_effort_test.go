// dispatch_effort_test.go pins F9's own answer to "does a sub-agent inherit
// the parent turn's effort override?" (newSubAgentRunner's own doc comment,
// docs/ROADMAP-ux-2026-08-20.md W5): yes, whenever the caller has a live
// per-turn value to pass (runAgentTurnHeadless's own req.Params) — not the
// full three-request dispatch chain dispatch_e2e_test.go already exercises
// for the tool-result round trip, just the one new fact this file adds:
// the sub-agent's own nested request body carries the same effort override
// the parent turn's request did.
package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MichiTrader/ishakat/internal/engine"
	"github.com/MichiTrader/ishakat/internal/permissions"
	"github.com/MichiTrader/ishakat/internal/provider"
	"github.com/MichiTrader/ishakat/internal/provider/fake"
	"github.com/MichiTrader/ishakat/internal/tools"
)

// TestNewSubAgentRunnerInheritsParamsFromTheParentTurn drives
// newSubAgentRunner directly (rather than through the full dispatch tool
// call dispatch_e2e_test.go exercises) with a non-nil params map, and
// asserts the sub-agent's own nested request body carries it — the same
// wire key EffortParams would produce for --effort/msg.Effort/`/effort`,
// but this test only cares that whatever params map the caller passes in
// reaches the wire unchanged, not which dialect produced it.
func TestNewSubAgentRunnerInheritsParamsFromTheParentTurn(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		_, _ = w.Write([]byte(fake.SSEDelta("sub-agent answer")))
		if fl != nil {
			fl.Flush()
		}
		_, _ = w.Write([]byte(fake.SSEDone()))
	}))
	t.Cleanup(srv.Close)

	cfg := toolsCfg(t, srv.URL, "allow")
	reviewer := &recordingReviewer{decision: permissions.Decision{Allow: true}}
	guard := permissions.New(cfg.Tools.Permissions, false, reviewer)

	prov, err := NewProvider(cfg, cfg.Providers[0], "0.0.0-test")
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	eng := engine.New(NewStreamer(prov, provider.Caps{Tools: true}, false), 0)

	params := map[string]any{"reasoning_effort": "high"}
	runner := newSubAgentRunner(eng, "auto/coding", "", cfg.Tools, guard, nil, tools.Caps{}, false, nil, params)

	if _, err := runner(context.Background(), "do a small task"); err != nil {
		t.Fatalf("runner: %v", err)
	}

	if gotBody["reasoning_effort"] != "high" {
		t.Errorf("sub-agent's own request body does not carry the parent's effort override: %+v", gotBody)
	}
}

// TestNewSubAgentRunnerWithNilParamsSendsNoOverride is the negative
// companion: app.go's boot-time dispatchRunner call site passes nil (see
// newSubAgentRunner's own doc comment on why), and that must not add any
// params field at all to the sub-agent's own request.
func TestNewSubAgentRunnerWithNilParamsSendsNoOverride(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		_, _ = w.Write([]byte(fake.SSEDelta("sub-agent answer")))
		if fl != nil {
			fl.Flush()
		}
		_, _ = w.Write([]byte(fake.SSEDone()))
	}))
	t.Cleanup(srv.Close)

	cfg := toolsCfg(t, srv.URL, "allow")
	reviewer := &recordingReviewer{decision: permissions.Decision{Allow: true}}
	guard := permissions.New(cfg.Tools.Permissions, false, reviewer)

	prov, err := NewProvider(cfg, cfg.Providers[0], "0.0.0-test")
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	eng := engine.New(NewStreamer(prov, provider.Caps{Tools: true}, false), 0)

	runner := newSubAgentRunner(eng, "auto/coding", "", cfg.Tools, guard, nil, tools.Caps{}, false, nil, nil)

	if _, err := runner(context.Background(), "do a small task"); err != nil {
		t.Fatalf("runner: %v", err)
	}

	if _, has := gotBody["reasoning_effort"]; has {
		t.Errorf("sub-agent's own request body should not carry reasoning_effort with nil params: %+v", gotBody)
	}
}

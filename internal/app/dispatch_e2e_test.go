// dispatch_e2e_test.go is Step 22's own version of toolchain_e2e_test.go's
// exercise: walk the whole dispatch chain end to end, with no fake standing
// in for any link of it, and prove the one thing no per-component test
// (internal/tools/dispatch_test.go's fake Runner, internal/app/dispatch.go's
// own doc comment, permissions/guard_test.go's TestGuardDispatchTierIsHigh
// AndNative) can prove on its own: that a *real* newSubAgentRunner closure,
// wired into a *real* buildAgentOptions call and driven by the same
// *engine.Engine and provider the parent turn itself uses, actually starts
// a second, isolated agent turn and hands its answer back as the parent's
// own BlockToolResult -- not a string some test fabricated on dispatch's
// behalf.
//
// The shape is three requests against one fake server, not two:
//
//  1. the parent's first turn -- tools offered, the (scripted) model asks
//     for dispatch;
//  2. the sub-agent's own nested turn -- newSubAgentRunner's closure
//     starts a brand-new RunAgentTurn, with its own one-message history
//     (§3's "isolated context"), against the very same engine and server;
//  3. the parent's second turn -- the dispatch tool result (the sub-agent's
//     final text, verbatim) is now in history, and the model answers using
//     it.
//
// Nothing here is faked at the boundary this test cares about: the tool
// call comes off a real SSE body, permissions.Guard authorizes dispatch's
// own High tier for real, newSubAgentRunner is the actual production
// closure from dispatch.go, and the sub-agent's turn runs through
// engine.RunAgentTurn exactly as the parent's does. The only script is the
// fake HTTP server standing in for the model itself, the same substitution
// every other e2e test in this package already makes.
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MichiTrader/ishakat/internal/convo"
	"github.com/MichiTrader/ishakat/internal/engine"
	"github.com/MichiTrader/ishakat/internal/permissions"
	"github.com/MichiTrader/ishakat/internal/provider/fake"
)

// dispatchServer plays the three-request exchange described in this file's
// own doc comment. It tells the requests apart by content, not by counting,
// for the same reason twoTurnToolServer's own "offered" check exists: a
// server that routed on request order alone would keep answering exactly as
// scripted even if the real chain were broken and the sub-agent's own
// request never happened, or happened twice, or carried the parent's
// history instead of its own.
//
//   - a request whose messages include a role:"tool" entry is the parent's
//     *second* turn (the dispatch result already round-tripped into
//     history) -- answered with parentFinalText.
//   - a request whose messages contain taskMarker is the sub-agent's own
//     nested turn (newSubAgentRunner seeded its isolated history with
//     exactly the task string dispatch's arguments carried) -- answered
//     with subAgentAnswer.
//   - anything else is the parent's first turn -- answered with a dispatch
//     tool call carrying dispatchArgs, but only when tools were actually
//     offered (requireToolsOnWire's own reasoning: a server that offered a
//     tool call unconditionally would not be testing anything).
func dispatchServer(t *testing.T, taskMarker, subAgentAnswer, parentFinalText string, dispatchArgs json.RawMessage) (*httptest.Server, *atomic.Bool) {
	t.Helper()
	sawTools := &atomic.Bool{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Tools    []any `json:"tools"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if len(body.Tools) > 0 {
			sawTools.Store(true)
		}

		var hasToolResult, hasMarker bool
		for _, m := range body.Messages {
			if m.Role == "tool" {
				hasToolResult = true
			}
			if strings.Contains(m.Content, taskMarker) {
				hasMarker = true
			}
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		write := func(s string) {
			_, _ = w.Write([]byte(s))
			if fl != nil {
				fl.Flush()
			}
		}

		switch {
		case hasToolResult:
			// The parent's second turn: the sub-agent's answer is already
			// in context as the dispatch tool result.
			write(fake.SSEDelta(parentFinalText))
			write(fake.SSEDone())
		case hasMarker:
			// The sub-agent's own isolated turn.
			write(fake.SSEDelta(subAgentAnswer))
			write(fake.SSEDone())
		default:
			// The parent's first turn.
			write(fake.SSEChunk(fmt.Sprintf(
				`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call-1","function":{"name":"dispatch","arguments":%s}}]}}]}`,
				quoteJSON(string(dispatchArgs)))))
			write(fake.SSEChunk(`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`))
			write(fake.SSEDone())
		}
	}))
	t.Cleanup(srv.Close)
	return srv, sawTools
}

// TestDispatchSubAgentRoundTripsThroughToolResult is Step 22's closing
// criterion: a dispatched sub-agent call must actually run a second,
// isolated agent turn and hand its own final text back as the parent's
// BlockToolResult, verbatim -- not a stand-in, and not the parent's own
// second-turn script mistaken for it.
func TestDispatchSubAgentRoundTripsThroughToolResult(t *testing.T) {
	const taskMarker = "SUBAGENT-TASK-9f21"
	const subAgentAnswer = "Sub-agent finding: fetch.go enforces an egress allowlist before any request leaves the process."
	const parentFinalText = "Done -- the sub-agent confirmed the egress allowlist is enforced."

	args, err := json.Marshal(map[string]string{
		"task": "Investigate " + taskMarker + " (what fetch.go's egress allowlist does) and report back.",
	})
	if err != nil {
		t.Fatalf("marshal dispatch args: %v", err)
	}

	srv, sawTools := dispatchServer(t, taskMarker, subAgentAnswer, parentFinalText, args)

	// write = "allow" only because toolsCfg needs some value for it; this
	// test never calls write_file, and dispatch's own gate is Shell (see
	// permissions/guard.go's mode(), case "bash", "dispatch").
	cfg := toolsCfg(t, srv.URL, "allow")
	reviewer := &recordingReviewer{decision: permissions.Decision{Allow: true}}
	guard := permissions.New(cfg.Tools.Permissions, false, reviewer)

	caps, _ := CapsFor(cfg, nil, "omniroute/auto/coding", cfg.Tools.Enabled)
	prov, err := NewProvider(cfg, cfg.Providers[0], "0.0.0-test")
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	eng := engine.New(NewStreamer(prov, caps), 0)

	// The one line that makes this an end-to-end test rather than another
	// unit test with a fake Runner: the real closure dispatch.go's own
	// production call sites (app.go's Run, agentturn.go's
	// runAgentTurnHeadless) build, reusing this same *engine.Engine.
	dispatchRunner := newSubAgentRunner(eng, "auto/coding", "", cfg.Tools, guard, nil, false)
	opts, _ := buildAgentOptions(cfg.Tools, guard, nil, false, dispatchRunner)

	hist := &convo.Conversation{}
	hist.Add(convo.User("delegate research on fetch.go's egress allowlist to a sub-agent"))

	result, err := eng.RunAgentTurn(context.Background(),
		engine.Request{Model: "auto/coding"},
		opts,
		hist)
	if err != nil {
		t.Fatalf("RunAgentTurn: %v", err)
	}

	requireToolsOnWire(t, sawTools)

	// The human was consulted about dispatch itself -- High tier, so never
	// a session grant, but still a real Guard.Authorize call, same as
	// write_file's own approval path in toolchain_e2e_test.go.
	reqs := reviewer.requests()
	if len(reqs) != 1 {
		t.Fatalf("reviewer consulted %d times, want exactly 1", len(reqs))
	}
	if reqs[0].Name != "dispatch" {
		t.Errorf("reviewed tool = %q, want dispatch", reqs[0].Name)
	}
	if reqs[0].Tier != permissions.High {
		t.Errorf("dispatch tier = %v, want High", reqs[0].Tier)
	}

	if !historyHasToolCall(hist, "dispatch") {
		t.Fatal("history has no dispatch tool call block")
	}
	if historyHasToolError(hist) {
		t.Error("dispatch tool result was marked as an error")
	}

	// The actual round trip this file exists to prove: the tool RESULT
	// block's text is the sub-agent's OWN answer, produced by its own
	// nested RunAgentTurn call -- not empty, not the parent's own script,
	// not a placeholder dispatch.go might have synthesized on failure.
	var toolResultText string
	var found bool
	for _, m := range hist.Messages {
		for _, b := range m.Blocks {
			if b.Kind == convo.BlockToolResult && b.Name == "dispatch" {
				toolResultText = b.Text
				found = true
			}
		}
	}
	if !found {
		t.Fatal("no dispatch tool result block in history")
	}
	if toolResultText != subAgentAnswer {
		t.Errorf("dispatch tool result = %q, want the sub-agent's own answer %q", toolResultText, subAgentAnswer)
	}

	// And the parent's own second turn actually saw that result and used
	// it to answer -- the other half of "round trips through": the value
	// did not just land in history, it reached the model.
	if !strings.Contains(result.Text, parentFinalText) {
		t.Errorf("final text = %q, want the parent's second-turn answer %q", result.Text, parentFinalText)
	}
}

// TestDispatchWithoutRunnerReportsAsToolErrorNotPanic covers the other edge
// dispatch.go's own doc comment names explicitly: a session where dispatch
// is registered (WithMetaTools saw a non-nil DispatchRunner at
// buildAgentOptions time is not the case being tested here -- Runner itself
// is nil, the state newSubAgentRunner's own doc comment says is not a
// programmer error) must hand the model tool-error data it can react to,
// never a panic and never a silently empty answer.
func TestDispatchWithoutRunnerReportsAsToolErrorNotPanic(t *testing.T) {
	args, err := json.Marshal(map[string]string{"task": "anything"})
	if err != nil {
		t.Fatalf("marshal dispatch args: %v", err)
	}
	srv, sawTools := dispatchServer(t, "unused-marker", "unused", "Understood, dispatch is unavailable.", args)

	cfg := toolsCfg(t, srv.URL, "allow")
	reviewer := &recordingReviewer{decision: permissions.Decision{Allow: true}}
	guard := permissions.New(cfg.Tools.Permissions, false, reviewer)

	caps, _ := CapsFor(cfg, nil, "omniroute/auto/coding", cfg.Tools.Enabled)
	prov, err := NewProvider(cfg, cfg.Providers[0], "0.0.0-test")
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	eng := engine.New(NewStreamer(prov, caps), 0)

	// dispatchRunner is a Dispatch{Runner: nil} in effect: buildAgentOptions
	// still registers dispatch (a non-nil SubAgentRunner value is what
	// gates that, and this one is non-nil -- see registry.go's own
	// MetaToolsOptions.DispatchRunner doc comment), but the closure itself
	// always fails, exactly like an eng == nil newSubAgentRunner would.
	dispatchRunner := newSubAgentRunner(nil, "auto/coding", "", cfg.Tools, guard, nil, false)
	opts, _ := buildAgentOptions(cfg.Tools, guard, nil, false, dispatchRunner)

	hist := &convo.Conversation{}
	hist.Add(convo.User("delegate anything to a sub-agent"))

	result, err := eng.RunAgentTurn(context.Background(),
		engine.Request{Model: "auto/coding"},
		opts,
		hist)
	if err != nil {
		t.Fatalf("RunAgentTurn: %v", err)
	}

	requireToolsOnWire(t, sawTools)

	if !historyHasToolCall(hist, "dispatch") {
		t.Fatal("history has no dispatch tool call block")
	}
	if !historyHasToolError(hist) {
		t.Error("an unconfigured dispatch runner must reach the model as tool-error data, not silently succeed")
	}
	if !strings.Contains(result.Text, "Understood, dispatch is unavailable.") {
		t.Errorf("final text = %q, want the model's own answer after the error", result.Text)
	}
}

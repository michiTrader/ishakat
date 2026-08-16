// dispatch_mission_e2e_test.go closes Step 31's own last-named gap (part 2's
// "what remains" list, restated in part 3's own changelog entry): a deeper
// end-to-end test proving a mission rule added through the *dialog path* —
// internal/mission.Compile's own output, filtered to its deny-effect half
// exactly the way internal/tui/mission.go's resolveMission/denyRulesOf do,
// then handed to Guard.AddMissionRules — is actually enforced on a *real*
// dispatched sub-agent's own tool call, not merely proven by pointer
// identity (internal/permissions/mission_rules_test.go's own
// TestGuardMissionRuleInheritedBySubAgentGuard, which hands a hand-built
// MissionRule to a second reference of the same *Guard and never touches
// internal/mission.Compile at all) and not merely proven at the display
// level (internal/tui/mission_internal_test.go's own
// TestFinishAgentTurnShowsInheritedMissionRulesOnADispatchLine, which never
// dispatches a real sub-agent — it drives finishAgentTurn directly over an
// already-finished, hand-built history).
//
// This file borrows dispatch_e2e_test.go's own three-request shape (parent
// asks for dispatch -> sub-agent's own isolated turn -> parent's second
// turn) and toolchain_e2e_test.go's own reasoning for why an end-to-end test
// exists at all: a passing test per link (mission.Compile, AddMissionRules,
// hardDeny, newSubAgentRunner) is not the same thing as a test that the
// links are actually wired to each other, and Step 31's own three parts
// landed exactly at those seams.
//
// The sub-agent's own scripted tool call is a real "bash" call naming a
// real, disallowed command ("npx playwright test tests/e2e.spec.ts") — this
// is safe to run unmocked because hardDeny's mission check (guard.go) runs
// inside Authorize, strictly before Registry.Run ever executes anything;
// a denied call by construction never reaches tools.Bash.Run, so no shell
// command actually runs in this test regardless of the outcome asserted.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MichiTrader/ishakat/internal/convo"
	"github.com/MichiTrader/ishakat/internal/engine"
	"github.com/MichiTrader/ishakat/internal/mission"
	"github.com/MichiTrader/ishakat/internal/permissions"
	"github.com/MichiTrader/ishakat/internal/provider/fake"
	"github.com/MichiTrader/ishakat/internal/tools"
)

// missionDenyRulesFor mirrors internal/tui/mission.go's own denyRulesOf
// field-by-field, on purpose: that function is unexported (mission.go's own
// package comment explains why internal/tui is the one place allowed to
// hold both mission.Rule and permissions.MissionRule side by side), and
// duplicating its ten lines here is cheaper and more honest than exporting
// a bridge function whose only caller would be a test — the point of this
// file is that *this exact conversion*, run over a *real*
// mission.Compile(goal) result, produces rules the Guard actually enforces
// on a dispatched sub-agent, not that some other, hand-rolled conversion
// happens to look similar.
func missionDenyRulesFor(goal string) []permissions.MissionRule {
	compiled := mission.Compile(goal)
	var out []permissions.MissionRule
	for _, c := range compiled.Constraints {
		if !c.Negated {
			continue
		}
		for _, r := range c.Rules {
			if r.Effect != "deny" {
				continue
			}
			out = append(out, permissions.MissionRule{Capability: r.Capability, Pattern: r.Pattern})
		}
	}
	return out
}

// dispatchMissionServer is dispatchServer's own sibling for this file's own,
// slightly richer request shape. Because hardDeny's mission check is a
// **data** refusal, not a turn-ending one (see permissions/guard.go's own
// refusal() doc comment: "A configuration boundary refused these arguments
// or this tool[...] the model choosing [a different path] is correct
// recovery" — a mission-denied bash call is exactly that kind of boundary,
// on purpose, so the sub-agent can explain the refusal in its own final
// answer instead of the turn simply going silent), there are FOUR requests
// here, not three:
//
//  1. the parent's first turn — tools offered, the (scripted) model asks
//     for dispatch;
//  2. the sub-agent's own first nested turn — its isolated history has only
//     taskMarker in it, no tool result yet — it calls bash with the
//     mission-forbidden command;
//  3. the sub-agent's own SECOND nested turn — its isolated history now
//     also has a role:"tool" entry (the mission's denial, returned as
//     data), which is what makes this request's own Messages contain BOTH
//     taskMarker and a tool-role entry — the model explains the refusal in
//     its final text;
//  4. the parent's second turn — the dispatch tool result (the sub-agent's
//     own final text from request 3, verbatim) is now in the PARENT's own
//     context, which has no taskMarker (the parent's own history only ever
//     saw dispatch's arguments as a tool_calls entry, never as message
//     Content — see FromConvo's own BlockToolCall handling, which puts
//     arguments in the wire tool_calls array, not Content).
//
// Requests 3 and 4 are the pair a naive "has a tool-role message => this is
// the parent's second turn" rule (dispatchServer's own, sufficient for its
// own three-request shape) cannot tell apart: both carry a role:"tool"
// message once the sub-agent's own denied bash call round-trips into its
// own isolated history. The ToolCallID on that tool message is what
// actually distinguishes them — "call-2" (bash, scripted by request 2 below)
// is the sub-agent's own call; "call-1" (dispatch, scripted by request 1)
// is the parent's. Routing on that id, still content off the wire and never
// on request order, is what this server does.
func dispatchMissionServer(t *testing.T, taskMarker, bashCommand, subAgentFinalText, parentFinalText string, dispatchArgs json.RawMessage) (*httptest.Server, *atomic.Bool) {
	t.Helper()
	sawTools := &atomic.Bool{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Tools    []any `json:"tools"`
			Messages []struct {
				Role       string `json:"role"`
				Content    string `json:"content"`
				ToolCallID string `json:"tool_call_id"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if len(body.Tools) > 0 {
			sawTools.Store(true)
		}

		var hasMarker, hasBashToolResult, hasDispatchToolResult bool
		for _, m := range body.Messages {
			if strings.Contains(m.Content, taskMarker) {
				hasMarker = true
			}
			if m.Role == "tool" && m.ToolCallID == "call-2" {
				hasBashToolResult = true
			}
			if m.Role == "tool" && m.ToolCallID == "call-1" {
				hasDispatchToolResult = true
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
		case hasDispatchToolResult:
			// The parent's second turn (request 4): the dispatch tool
			// result is the sub-agent's own final text, already in
			// context.
			write(fake.SSEDelta(parentFinalText))
			write(fake.SSEDone())
		case hasBashToolResult:
			// The sub-agent's own SECOND nested turn (request 3): its own
			// bash call came back as tool-error data (a mission-deny
			// refusal is data, not turn-ending — see this function's own
			// doc comment), and it now explains that in its final answer.
			write(fake.SSEDelta(subAgentFinalText))
			write(fake.SSEDone())
		case hasMarker:
			// The sub-agent's own FIRST nested turn (request 2): it
			// immediately calls bash with the mission-forbidden command.
			bashArgs, err := json.Marshal(map[string]string{"command": bashCommand})
			if err != nil {
				t.Fatalf("marshal bash args: %v", err)
			}
			write(fake.SSEChunk(fmt.Sprintf(
				`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call-2","function":{"name":"bash","arguments":%s}}]}}]}`,
				quoteJSON(string(bashArgs)))))
			write(fake.SSEChunk(`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`))
			write(fake.SSEDone())
		default:
			// The parent's first turn (request 1).
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

// TestDispatchedSubAgentIsRefusedByADialogPathMissionRule is this file's own
// closing criterion: a mission rule compiled the same way the §21.6 dialog
// compiles one (mission.Compile, filtered to its deny-effect half) and
// applied to a Guard the same way resolveMission applies it
// (AddMissionRules) must refuse a *real* dispatched sub-agent's own
// matching bash call — end to end, through the real engine, the real
// registry, and the real newSubAgentRunner closure dispatch.go's own
// production call sites build, with no fake standing in for any link.
//
// The refusal itself surfaces as tool-error DATA inside the sub-agent's own
// turn, not as a turn-ending denial: hardDeny's mission check
// (missionHardDeny, guard.go) returns a plain wrapped ErrDenied, not the
// unexported deniedError type refusal() builds for a human's own "no" or an
// absent reviewer (see refusal()'s own doc comment: "A configuration
// boundary refused these arguments or this tool[...] the model choosing
// [a different path] is correct recovery"). So the sub-agent's own
// RunAgentTurn does not stop after the denied bash call — it sees the
// refusal message as an ordinary tool result and gets one more iteration
// to react to it, exactly like any other config-driven refusal (§12bis's
// error-is-data contract). This test's own dispatchMissionServer accounts
// for that fourth request; see its doc comment for the full shape.
func TestDispatchedSubAgentIsRefusedByADialogPathMissionRule(t *testing.T) {
	const taskMarker = "SUBAGENT-PLAYWRIGHT-7c3a"
	const bashCommand = "npx playwright test tests/e2e.spec.ts"
	const subAgentFinalText = "I could not run Playwright: the mission forbids it."
	const parentFinalText = "Done -- the sub-agent's Playwright run was blocked by your mission constraint."

	args, err := json.Marshal(map[string]string{
		"task": "Run " + bashCommand + " (marker " + taskMarker + ") and report the result.",
	})
	if err != nil {
		t.Fatalf("marshal dispatch args: %v", err)
	}

	srv, sawTools := dispatchMissionServer(t, taskMarker, bashCommand, subAgentFinalText, parentFinalText, args)

	cfg := toolsCfg(t, srv.URL, "allow")
	reviewer := &recordingReviewer{decision: permissions.Decision{Allow: true}}
	guard := permissions.New(cfg.Tools.Permissions, false, reviewer)

	// The dialog path itself: a real mission.Compile result, filtered to
	// its deny-effect half exactly the way resolveMission's own
	// denyRulesOf does, applied via AddMissionRules -- never a
	// hand-written MissionRule, which is the one thing distinguishing this
	// test from TestGuardMissionRuleInheritedBySubAgentGuard.
	rules := missionDenyRulesFor("fix orbital-dash, no playwright")
	if len(rules) == 0 {
		t.Fatal("missionDenyRulesFor compiled no rules for a goal containing a recognized constraint")
	}
	guard.AddMissionRules(rules)

	caps, _ := CapsFor(cfg, nil, "omniroute/auto/coding", cfg.Tools.Enabled)
	prov, err := NewProvider(cfg, cfg.Providers[0], "0.0.0-test")
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	eng := engine.New(NewStreamer(prov, caps), 0)

	// The same real closure dispatch.go's own production call sites build,
	// threading the identical *Guard -- so a rule the parent's own mission
	// dialog would have added is enforced on the child by pointer
	// identity, exactly as dispatch.go's own doc comment claims, but now
	// exercised starting from the dialog's own compiled output rather than
	// a hand-built MissionRule.
	dispatchRunner := newSubAgentRunner(eng, "auto/coding", "", cfg.Tools, guard, nil, tools.Caps{}, false, nil)
	opts, _ := buildAgentOptions(cfg.Tools, guard, nil, tools.Caps{}, false, dispatchRunner, nil)

	hist := &convo.Conversation{}
	hist.Add(convo.User("delegate the Playwright run to a sub-agent, but no Playwright"))

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

	// The dispatch tool RESULT block is the sub-agent's own final text
	// (subAgentFinalText, request 3's scripted answer) -- proving the
	// sub-agent's own turn ran to completion *after* its bash call was
	// refused, not that it crashed, hung, or silently produced nothing.
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
	if toolResultText != subAgentFinalText {
		t.Errorf("dispatch tool result = %q, want the sub-agent's own final answer %q", toolResultText, subAgentFinalText)
	}

	if !strings.Contains(result.Text, parentFinalText) {
		t.Errorf("final text = %q, want the parent's second-turn answer %q", result.Text, parentFinalText)
	}

	// The direct, unambiguous proof that the mission rule -- not luck, not
	// a scripted answer that merely claims to be a refusal -- is what
	// actually stopped the sub-agent's own bash call: guard.Authorize on
	// that exact command still refuses it, with errors.Is(err, ErrDenied),
	// using the very same *Guard the sub-agent used (pointer identity is
	// dispatch.go's own inheritance mechanism; this pins that the rule
	// was live throughout, not merely present before or after).
	err = guard.Authorize(context.Background(), "bash", json.RawMessage(fmt.Sprintf(`{"command":%q}`, bashCommand)))
	if err == nil {
		t.Fatal("guard.Authorize(bash, playwright command) = nil after the dispatched turn, want the mission rule to still refuse it")
	}
	if !errors.Is(err, permissions.ErrDenied) {
		t.Errorf("guard.Authorize error = %v, want it to wrap permissions.ErrDenied", err)
	}
	if !strings.Contains(err.Error(), "mission constraint") {
		t.Errorf("guard.Authorize error = %v, want it to name the mission constraint as the reason", err)
	}
}

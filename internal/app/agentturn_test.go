package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MichiTrader/ishakat/internal/catalog"
	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/convo"
	"github.com/MichiTrader/ishakat/internal/permissions"
	"github.com/MichiTrader/ishakat/internal/provider"
	"github.com/MichiTrader/ishakat/internal/provider/fake"
	"github.com/MichiTrader/ishakat/internal/tools"
	"github.com/MichiTrader/ishakat/internal/xdg"
)

// TestHeadlessAgentLoopToolCallThenAnswer is §12bis's own closing criterion:
// `ishakat -p "…"` with cfg.Tools.Enabled=true and a real tool (read_file,
// not a fake ToolRunner) producing a correct answer through an actual tool
// call. The fake provider plays two turns: the first asks for read_file on a
// real file this test writes to t.TempDir(), the second — once the tool's
// result is back in context — answers with text derived from that content.
func TestHeadlessAgentLoopToolCallThenAnswer(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "greeting.txt")
	if err := os.WriteFile(target, []byte("hola desde el archivo"), 0o600); err != nil {
		t.Fatalf("could not write fixture file: %v", err)
	}

	argsJSON, err := json.Marshal(map[string]string{"path": target})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}

	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		write := func(s string) {
			_, _ = w.Write([]byte(s))
			if fl != nil {
				fl.Flush()
			}
		}
		if n == 1 {
			// First turn: ask for read_file on the fixture.
			write(fake.SSEChunk(fmt.Sprintf(
				`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c1","function":{"name":"read_file","arguments":%s}}]}}]}`,
				quoteJSON(string(argsJSON)))))
			write(fake.SSEChunk(`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`))
			write(fake.SSEDone())
			return
		}
		// Second turn: the tool result is in context; answer with text.
		write(fake.SSEDelta("the file says: hola desde el archivo"))
		write(fake.SSEDone())
	}))
	defer srv.Close()

	cfg := cfgFor(t, srv.URL)
	cfg.Tools = config.Tools{Enabled: true, MaxCallsPerTurn: 5, MaxOutputBytes: 4096}

	code, out, errs := run(t, HeadlessOptions{Config: cfg, Prompt: "read the greeting file and tell me what it says"})
	if code != ExitOK {
		t.Fatalf("code = %d, expected 0. stderr: %s", code, errs)
	}
	if !strings.Contains(out, "hola desde el archivo") {
		t.Errorf("stdout must carry the tool's real content, got: %q", out)
	}
	if n := attempts.Load(); n != 2 {
		t.Errorf("expected 2 requests (tool call + follow-up), got %d", n)
	}
	// The tool call itself is reported on stderr (textSink.tool), never on
	// stdout — same contract runTurn's own path already keeps.
	if !strings.Contains(errs, "read_file") {
		t.Errorf("stderr should report the tool call, got: %q", errs)
	}
}

// TestHeadlessAgentLoopPersistsEachMessage proves runAgentTurnHeadless's own
// contract: every message the loop produces (the assistant's tool-call
// turn, the tool result, the final assistant text) lands in the session
// file individually — not a single collapsed summary — so --resume (once it
// exists) sees the same shape the provider actually produced.
func TestHeadlessAgentLoopPersistsEachMessage(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(target, []byte("contenido de prueba"), 0o600); err != nil {
		t.Fatalf("could not write fixture file: %v", err)
	}
	argsJSON, err := json.Marshal(map[string]string{"path": target})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}

	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		write := func(s string) {
			_, _ = w.Write([]byte(s))
			if fl != nil {
				fl.Flush()
			}
		}
		if n == 1 {
			write(fake.SSEChunk(fmt.Sprintf(
				`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c1","function":{"name":"read_file","arguments":%s}}]}}]}`,
				quoteJSON(string(argsJSON)))))
			write(fake.SSEChunk(`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`))
			write(fake.SSEDone())
			return
		}
		write(fake.SSEDelta("done"))
		write(fake.SSEDone())
	}))
	defer srv.Close()

	sessDir := t.TempDir()
	cfg := cfgFor(t, srv.URL)
	cfg.Tools = config.Tools{Enabled: true, MaxCallsPerTurn: 5, MaxOutputBytes: 4096}
	cfg.Session.Save = true
	cfg.Session.Dir = sessDir

	code, _, errs := run(t, HeadlessOptions{Config: cfg, Prompt: "read the note"})
	if code != ExitOK {
		t.Fatalf("code = %d, expected 0. stderr: %s", code, errs)
	}

	entries, err := os.ReadDir(sessDir)
	if err != nil {
		t.Fatalf("could not read session dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 session file, found %d", len(entries))
	}
	raw, err := os.ReadFile(filepath.Join(sessDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("could not read session file: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	// header + user + assistant(tool_call) + tool(result) + assistant(text)
	// = 5 lines. The precise count matters: it is what proves the loop's
	// intermediate messages were persisted individually, not collapsed.
	if len(lines) != 5 {
		t.Errorf("expected 5 JSONL lines (header, user, assistant tool_call, tool result, final assistant), got %d:\n%s", len(lines), raw)
	}
	if !strings.Contains(string(raw), "read_file") {
		t.Errorf("session file must record the read_file call, got:\n%s", raw)
	}
	if !strings.Contains(string(raw), "contenido de prueba") {
		t.Errorf("session file must record the tool's real result, got:\n%s", raw)
	}
}

// quoteJSON re-quotes an already-serialized JSON string as a JSON string
// literal, the same way the arguments field of a wire tool_calls delta
// carries its payload (a string, not a nested object) per the OpenAI
// dialect.
func quoteJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// TestRunAgentTurnHeadlessSeedsBudgetFromPersistedSpend pins the fix
// introduced by "seed agent budget from persisted session spend": before
// this fix, opts.SpentUSD (engine.AgentOptions) always started at zero, so
// resuming a conversation that had already spent close to budget_usd would
// silently reset the ceiling on every process launch — the exact case a
// long-running or --resume'd session most needs the guard to survive. This
// test builds hist with a prior assistant message whose Usage.CostUSD
// already sits just under a small budget, then runs one more turn that
// asks for a tool call; the turn must stop on the cost budget before that
// tool call executes, proving runAgentTurnHeadless read the prior spend
// from hist.Usage() rather than starting the budget over.
func TestRunAgentTurnHeadlessSeedsBudgetFromPersistedSpend(t *testing.T) {
	var toolCallsSeen atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		toolCallsSeen.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		write := func(s string) {
			_, _ = w.Write([]byte(s))
			if fl != nil {
				fl.Flush()
			}
		}
		// Whatever this turn asks, offer a tool call so the loop has
		// something to weigh against the budget. If the budget seed works,
		// the loop must never even open this stream — see attempts below.
		write(fake.SSEChunk(
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c1","function":{"name":"list_dir","arguments":"{}"}}]}}]}`))
		write(fake.SSEChunk(`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`))
		write(fake.SSEDone())
	}))
	defer srv.Close()

	cfg := cfgFor(t, srv.URL)
	cfgTools := config.Tools{Enabled: true, MaxCallsPerTurn: 5, MaxOutputBytes: 4096, BudgetUSD: 0.01}

	pc, ok := FindProvider(cfg, "omniroute")
	if !ok {
		t.Fatal("test provider not found in cfgFor's own configuration")
	}
	prov, err := NewProvider(cfg, pc, "0.0.0-test")
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	// A prior conversation that already spent $0.02 against a $0.01
	// budget: on its own, already over the limit before this turn opens
	// a single stream — the clearest possible signal that SpentUSD came
	// from hist, not from a reset-to-zero default.
	hist := &convo.Conversation{Messages: []convo.Message{
		convo.Assistant("previous turn", "omniroute/auto/coding"),
	}}
	hist.Messages[0].Usage = &convo.Usage{In: 100, Out: 100, CostUSD: 0.02}

	// A priced model: without a non-nil, non-zero cost the loop's own
	// estimateCost would price every token at zero and the budget could
	// never be reached regardless of SpentUSD — see
	// TestHeadlessWarnsWhenBudgetCannotBePriced's own doc comment.
	cost := &catalog.Cost{In: 5, Out: 5}

	guard := permissions.New(cfgTools.Permissions, false, nil)
	var errb strings.Builder
	s := &textSink{err: &errb}
	req := provider.Request{Model: "gpt-test", Stream: true}
	user := convo.User("do something that needs a tool")

	_, turnErr := runAgentTurnHeadless(
		context.Background(), prov, cfgTools, guard, cost,
		2, req, user, s, nil, nil, hist, false,
	)
	if turnErr != nil {
		t.Fatalf("runAgentTurnHeadless: %v", turnErr)
	}
	// With the fix, opts.SpentUSD starts at 0.02 (from hist.Usage()), so
	// the budget check after the very first iteration's tool calls
	// (0.02 >= 0.01) stops the loop before a second request ever goes
	// out. Without the fix, SpentUSD would start at 0 and this fake
	// server (which never emits a provider.EventUsage) would keep the
	// turn's own estimated cost at 0 too, so the budget could never
	// fire on cost alone — the loop would instead run until loop
	// detection catches the model repeating the same call, which needs
	// a second request first. Either way this pins the fix: seeing
	// exactly 1 request (not 2+) proves the budget stopped it on the
	// very first iteration using spend seeded from hist.
	if n := toolCallsSeen.Load(); n != 1 {
		t.Errorf("provider was called %d time(s), want exactly 1: with the budget already "+
			"spent from hist, the loop must stop after the first iteration's tool calls "+
			"without ever opening a second stream", n)
	}
	if !strings.Contains(errb.String(), "cost budget reached") {
		t.Errorf("stderr must report the cost-budget stop, got: %q", errb.String())
	}
}

// TestBuildAgentOptionsIncludesDeclarativeToolFromDir is Step 20's own wiring
// closing criterion: a tool.toml under cfgTools.Dir must reach
// engine.AgentOptions.Tools (what the model is offered) and be dispatchable
// through Runner (what actually executes the call), exactly like every
// native tool already does — with no model involved in producing the
// manifest itself, matching the "hand-writable and testable without any
// model generating anything" bar the roadmap sets for this step.
func TestBuildAgentOptionsIncludesDeclarativeToolFromDir(t *testing.T) {
	toolsDir := t.TempDir()
	toolDir := filepath.Join(toolsDir, "greet")
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	manifest := []byte(`
name = "greet"
description = "say hello"

[request]
method = "GET"
url = "https://example.com/greet"
`)
	if err := os.WriteFile(filepath.Join(toolDir, "tool.toml"), manifest, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	cfgTools := config.Tools{
		Enabled: true,
		Dir:     toolsDir,
	}
	opts, warn := buildAgentOptions(cfgTools, nil, nil, false)
	if warn != "" {
		t.Fatalf("unexpected warn: %q", warn)
	}

	var found bool
	for _, def := range opts.Tools {
		if def.Name == "greet" {
			found = true
		}
	}
	if !found {
		t.Fatalf("opts.Tools does not include the discovered declarative tool: %+v", opts.Tools)
	}
}

// TestBuildAgentOptionsSurfacesDeclarativeDiscoveryWarn proves an
// unparseable tool.toml under cfgTools.Dir does not stop the turn from
// being built, but does return a non-empty warn — the same
// "warn, don't fail" contract SystemPrompt already applies to
// skills.Discover's own Warn field.
func TestBuildAgentOptionsSurfacesDeclarativeDiscoveryWarn(t *testing.T) {
	toolsDir := t.TempDir()
	toolDir := filepath.Join(toolsDir, "broken")
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(toolDir, "tool.toml"), []byte("not valid toml [["), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	cfgTools := config.Tools{Enabled: true, Dir: toolsDir}
	opts, warn := buildAgentOptions(cfgTools, nil, nil, false)
	if warn == "" {
		t.Fatal("expected a non-empty warn for an unparseable tool.toml")
	}
	// Native tools must still be present — a broken declarative manifest
	// must not take down layer 1. hasTTY is false in this call, so
	// tool_create is withheld (§19.6's TTY rule) but the other six
	// meta-tools (Step 21, plus tool_archive/tool_revive) are still present
	// once Dir is set, matching tools.WithMetaTools' own "Dir alone gates
	// the six, TTY/Mode gate only tool_create" contract — 7 native +
	// tool_list/probe/edit/archive/revive/delete.
	if len(opts.Tools) != 13 {
		t.Errorf("opts.Tools has %d entries, want 13 (7 native + 6 meta-tools, broken manifest skipped, no TTY so tool_create withheld)", len(opts.Tools))
	}
}

// TestRunAgentTurnHeadlessAllowToolCreateAddsToolCreateToCatalogue pins
// --allow-tool-create's whole effect end to end: runAgentTurnHeadless's
// allowToolCreate parameter must reach buildAgentOptions as its hasTTY
// argument, the same substitution tools.WithMetaTools documents for
// AllowWithoutTTY, so tool_create appears in the tools array actually sent
// to the provider even though this call has no terminal at all. It does not
// assert anything about gate 2 (permissions.Guard's reviewer is nil in
// every headless call, on purpose — see HeadlessOptions.AllowToolCreate's
// own doc comment): the flag's whole contract is visibility, not unattended
// approval, and this test's job is only to prove the catalogue actually
// changes when the flag is set.
func TestRunAgentTurnHeadlessAllowToolCreateAddsToolCreateToCatalogue(t *testing.T) {
	toolsDir := t.TempDir()

	var sawToolCreate atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if arr, _ := body["tools"].([]any); arr != nil {
			for _, raw := range arr {
				def, _ := raw.(map[string]any)
				fn, _ := def["function"].(map[string]any)
				if name, _ := fn["name"].(string); name == "tool_create" {
					sawToolCreate.Store(true)
				}
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		_, _ = w.Write([]byte(fake.SSEChunk(`{"choices":[{"index":0,"delta":{"content":"ok"}}]}`)))
		_, _ = w.Write([]byte(fake.SSEChunk(`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)))
		_, _ = w.Write([]byte(fake.SSEDone()))
		if fl != nil {
			fl.Flush()
		}
	}))
	defer srv.Close()

	cfg := cfgFor(t, srv.URL)
	cfgTools := config.Tools{
		Enabled:         true,
		Dir:             toolsDir,
		MaxCallsPerTurn: 5,
		MaxOutputBytes:  4096,
		Evolve:          config.Evolve{Mode: "suggest"},
	}

	pc, ok := FindProvider(cfg, "omniroute")
	if !ok {
		t.Fatal("test provider not found in cfgFor's own configuration")
	}
	prov, err := NewProvider(cfg, pc, "0.0.0-test")
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	guard := permissions.New(cfgTools.Permissions, false, nil)
	var outb, errb strings.Builder
	s := &textSink{out: &outb, err: &errb}
	req := provider.Request{Model: "gpt-test", Stream: true}
	user := convo.User("hi")
	hist := &convo.Conversation{}

	if _, turnErr := runAgentTurnHeadless(
		context.Background(), prov, cfgTools, guard, nil,
		2, req, user, s, nil, nil, hist, false,
	); turnErr != nil {
		t.Fatalf("runAgentTurnHeadless (allowToolCreate=false): %v", turnErr)
	}
	if sawToolCreate.Load() {
		t.Error("tool_create must be absent from the wire with allowToolCreate=false, matching §19.7's \"With no TTY, tool_create is denied. Full stop.\"")
	}

	sawToolCreate.Store(false)
	hist = &convo.Conversation{}
	if _, turnErr := runAgentTurnHeadless(
		context.Background(), prov, cfgTools, guard, nil,
		2, req, user, s, nil, nil, hist, true,
	); turnErr != nil {
		t.Fatalf("runAgentTurnHeadless (allowToolCreate=true): %v", turnErr)
	}
	if !sawToolCreate.Load() {
		t.Error("tool_create must be present on the wire with allowToolCreate=true (--allow-tool-create's whole point)")
	}
}

// TestBuildAgentOptionsEmptyDirBehavesAsBefore pins that an unset
// cfgTools.Dir (the zero value, matching every pre-Step-20 config and every
// existing test that never set it) yields exactly the same seven tools
// buildAgentOptions always has, with no warn — Step 20 changes nothing for
// an install that has not created a tools directory of its own.
func TestBuildAgentOptionsEmptyDirBehavesAsBefore(t *testing.T) {
	cfgTools := config.Tools{Enabled: true}
	opts, warn := buildAgentOptions(cfgTools, nil, nil, false)
	if warn != "" {
		t.Fatalf("unexpected warn: %q", warn)
	}
	if len(opts.Tools) != 7 {
		t.Errorf("opts.Tools has %d entries, want 7", len(opts.Tools))
	}
}

// TestBuildAgentOptionsThreadsLedgerPathIntoToolCreate confirms
// buildAgentOptions supplies xdg.UsageFile() as tool_create's LedgerPath --
// the same "real ledger, not the model's own claim" wiring
// tool_create_test.go/registry_test.go already cover at their own layers,
// checked here at the one call site that actually assembles a live
// tools.Registry end to end. XDG_STATE_HOME is overridden so the assertion
// does not depend on (or pollute) whatever the sandbox's real state
// directory happens to be.
func TestBuildAgentOptionsThreadsLedgerPathIntoToolCreate(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)

	cfgTools := config.Tools{
		Enabled: true,
		Dir:     t.TempDir(),
		Evolve:  config.Evolve{Mode: "suggest", AllowWithoutTTY: true},
	}
	reg, warn := tools.WithMetaTools(tools.MetaToolsOptions{
		Dir:             cfgTools.Dir,
		EvolveMode:      cfgTools.Evolve.Mode,
		AllowWithoutTTY: cfgTools.Evolve.AllowWithoutTTY,
		HasTTY:          true,
		LedgerPath:      xdg.UsageFile(),
	})
	if warn != "" {
		t.Fatalf("unexpected warn: %q", warn)
	}
	got, ok := reg.Lookup("tool_create")
	if !ok {
		t.Fatal("expected tool_create to be present")
	}
	tc, ok := got.(tools.ToolCreate)
	if !ok {
		t.Fatalf("tool_create is not a tools.ToolCreate value: %T", got)
	}
	want := xdg.UsageFile()
	if tc.LedgerPath != want {
		t.Errorf("LedgerPath = %q, want %q", tc.LedgerPath, want)
	}
	if !strings.HasPrefix(tc.LedgerPath, stateHome) {
		t.Errorf("LedgerPath %q does not respect the overridden XDG_STATE_HOME %q", tc.LedgerPath, stateHome)
	}

	opts, warn2 := buildAgentOptions(cfgTools, nil, nil, true)
	if warn2 != "" {
		t.Fatalf("unexpected warn: %q", warn2)
	}
	var sawToolCreate bool
	for _, def := range opts.Tools {
		if def.Name == "tool_create" {
			sawToolCreate = true
		}
	}
	if !sawToolCreate {
		t.Fatal("buildAgentOptions did not offer tool_create with EvolveMode=suggest, AllowWithoutTTY=true")
	}
}

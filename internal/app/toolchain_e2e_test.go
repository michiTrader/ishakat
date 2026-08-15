// toolchain_e2e_test.go walks the whole Step 16 chain in one test, end to
// end, with no fake standing in for any link of it:
//
//	model asks for a tool  →  the `tools` array was on the wire to make that
//	possible  →  engine.RunAgentTurn dispatches it  →  the real
//	permissions.Guard authorizes it  →  the real reviewer bridge is consulted
//	→  a decision comes back  →  the real tools.WriteFile runs  →  the file
//	exists on disk  →  the model's second turn answers using the result.
//
// This file exists because Step 16 shipped with every individual link
// covered and the chain itself broken. The reviewer had a passing test
// against a fake program; the dialog had one against a fake reply channel;
// the guard had one against a fake reviewer; the loop had one against a fake
// runner. All green, and the feature could not create a file — because no
// test ever asserted that the links were actually connected to each other.
// A test per component is not the same thing as a test of the system, and
// the difference is exactly where this bug lived.
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
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/convo"
	"github.com/MichiTrader/ishakat/internal/engine"
	"github.com/MichiTrader/ishakat/internal/permissions"
	"github.com/MichiTrader/ishakat/internal/provider"
	"github.com/MichiTrader/ishakat/internal/provider/fake"
	"github.com/MichiTrader/ishakat/internal/tools"
)

// recordingReviewer is a permissions.Reviewer that answers with a fixed
// decision and remembers every request it was asked about. It stands in for
// the *human*, not for any code under test: the real toolReviewer bridge
// needs a running tea.Program (and therefore a terminal), which is the one
// thing a test cannot supply. Everything on either side of the human — the
// guard that asks, the loop that dispatches, the tool that runs — is real
// here.
type recordingReviewer struct {
	decision permissions.Decision

	mu   sync.Mutex
	seen []permissions.Request
}

func (r *recordingReviewer) Review(_ context.Context, req permissions.Request) (permissions.Decision, error) {
	r.mu.Lock()
	r.seen = append(r.seen, req)
	r.mu.Unlock()
	return r.decision, nil
}

func (r *recordingReviewer) requests() []permissions.Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]permissions.Request, len(r.seen))
	copy(out, r.seen)
	return out
}

// noToolsReply is what the server answers when it was offered no tools: the
// prose the user actually saw in the bug report, describing a shell command
// instead of calling anything.
const noToolsReply = "You can create it yourself by running: echo 'hi' > file.txt"

// twoTurnToolServer plays the two turns a real tool-using exchange takes:
// first a tool call for `name` with `args`, then — once the tool's result is
// back in context — a plain text answer.
//
// It only offers to call a tool when the request actually carried a `tools`
// array; with no tools on the wire it answers noToolsReply instead. That
// conditional is not decoration, it is the whole reason this file catches
// the bug: a server that emits a tool call unconditionally is modelling a
// provider that hallucinates functions it was never given, and every
// assertion downstream of it — the guard ran, the reviewer was asked, the
// file appeared — then passes just as happily with `tools` missing from the
// request. That fiction is precisely what let Step 16 ship green and inert.
// Here, no tools on the wire means no tool call, and the chain visibly
// breaks at the first link.
//
// turns is returned so a test can assert how many provider requests one turn
// actually cost. That count is the subject of §21.9's closing criterion 1:
// the rate-limit defect is invisible to every assertion about files,
// reviewers and final text, and shows up only as a request count that grows
// when it should not.
func twoTurnToolServer(t *testing.T, name string, args json.RawMessage, finalText string) (*httptest.Server, *atomic.Bool, *atomic.Int32) {
	t.Helper()
	sawTools := &atomic.Bool{}
	turns := &atomic.Int32{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		arr, _ := body["tools"].([]any)
		offered := len(arr) > 0
		if offered {
			sawTools.Store(true)
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

		if offered && turns.Add(1) == 1 {
			write(fake.SSEChunk(fmt.Sprintf(
				`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call-1","function":{"name":%q,"arguments":%s}}]}}]}`,
				name, quoteJSON(string(args)))))
			write(fake.SSEChunk(`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`))
			write(fake.SSEDone())
			return
		}
		if !offered {
			write(fake.SSEDelta(noToolsReply))
			write(fake.SSEDone())
			return
		}
		write(fake.SSEDelta(finalText))
		write(fake.SSEDone())
	}))
	t.Cleanup(srv.Close)
	return srv, sawTools, turns
}

// runToolTurn drives one full agent turn through the real engine, the real
// registry and the real guard, exactly the way runAgentTurnHeadless does.
func runToolTurn(t *testing.T, cfg *config.Config, guard *permissions.Guard) (engine.AgentResult, *convo.Conversation) {
	t.Helper()
	// Caps come from CapsFor, the same call the real entry points make —
	// not hard-coded true here, so these tests also fail if CapsFor
	// regresses, which is the whole point of the exercise.
	caps, _ := CapsFor(cfg, nil, "omniroute/auto/coding", cfg.Tools.Enabled)
	return runToolTurnWithCaps(t, cfg, guard, caps)
}

// runToolTurnWithCaps is runToolTurn with the caps supplied directly, so a
// test can reproduce the broken wiring on purpose instead of only asserting
// that the fixed wiring works.
func runToolTurnWithCaps(t *testing.T, cfg *config.Config, guard *permissions.Guard, caps provider.Caps) (engine.AgentResult, *convo.Conversation) {
	t.Helper()
	prov, err := NewProvider(cfg, cfg.Providers[0], "0.0.0-test")
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	eng := engine.New(NewStreamer(prov, caps), 0)

	hist := &convo.Conversation{}
	hist.Add(convo.User("create the file"))
	opts, _ := buildAgentOptions(cfg.Tools, guard, nil, tools.Caps{}, false, nil)
	result, err := eng.RunAgentTurn(context.Background(),
		engine.Request{Model: "auto/coding"},
		opts,
		hist)
	if err != nil {
		t.Fatalf("RunAgentTurn: %v", err)
	}
	return result, hist
}

// toolsCfg is cfgFor plus the [tools] block a tool-using session needs. The
// caps are set explicitly rather than left to defaults so a change to the
// config defaults cannot quietly change what these tests are exercising.
func toolsCfg(t *testing.T, srvURL string, write string) *config.Config {
	t.Helper()
	cfg := cfgFor(t, srvURL)
	cfg.Tools = config.Tools{
		Enabled:         true,
		MaxCallsPerTurn: 5,
		MaxOutputBytes:  4096,
		Permissions: config.Permissions{
			Read: "allow", Write: write, Shell: "ask", AllowSession: true,
		},
	}
	return cfg
}

// TestToolChainApprovedWriteReachesDisk is the closing criterion the Step 16
// report was actually asking for: the user asks for a file, the approval is
// granted, and the file exists afterwards. Before the Caps fix this test
// could not even reach the reviewer — the model was offered no tools at all.
func TestToolChainApprovedWriteReachesDisk(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "step16-approval.txt")
	const content = "Step 16 approval works."

	args, err := json.Marshal(map[string]string{"path": target, "content": content})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	srv, sawTools, _ := twoTurnToolServer(t, "write_file", args, "Created the file.")

	cfg := toolsCfg(t, srv.URL, "ask")
	reviewer := &recordingReviewer{decision: permissions.Decision{Allow: true}}
	guard := permissions.New(cfg.Tools.Permissions, false, reviewer)

	result, hist := runToolTurn(t, cfg, guard)

	requireToolsOnWire(t, sawTools)

	// The human was actually consulted — the link that was unreachable
	// before, and the reason no dialog ever opened on screen.
	reqs := reviewer.requests()
	if len(reqs) != 1 {
		t.Fatalf("reviewer consulted %d times, want exactly 1", len(reqs))
	}
	if reqs[0].Name != "write_file" {
		t.Errorf("reviewed tool = %q, want write_file", reqs[0].Name)
	}
	if reqs[0].Tier != permissions.Medium {
		t.Errorf("write_file tier = %v, want Medium (the tier that may offer a session grant)", reqs[0].Tier)
	}

	// The actual point: the file is on disk.
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("the approved write never reached disk: %v", err)
	}
	if string(got) != content {
		t.Errorf("file content = %q, want %q", got, content)
	}

	if result.Calls != 1 {
		t.Errorf("result.Calls = %d, want 1", result.Calls)
	}
	if !strings.Contains(result.Text, "Created the file.") {
		t.Errorf("final text = %q, want the model's second-turn answer", result.Text)
	}
	// The tool call and its result must both be in history, so a --resume
	// of this session replays what happened rather than just the answer.
	if !historyHasToolCall(hist, "write_file") {
		t.Error("history has no write_file tool call block")
	}
}

// TestToolChainDeniedWriteNeverTouchesDisk is the same chain with the human
// saying no. Two things must hold: the file is never written, and the turn
// ENDS — it does not hand the refusal back for the model to work around.
//
// This test asserted the opposite until step 26, and the change is deliberate
// (§21.9 fix 1, docs/BUG-rate-limit-amplifier.md). The old contract — "a
// denial is data the model can react to" — reads as a virtue and is the
// outage mechanism: the model receives the refusal, tries a variant, and each
// variant is another provider request carrying the whole grown history. A
// real user was rate-limited off their own account this way.
//
// It is also this test's job to hold closing criterion 1: a turn in which the
// human denies one call issues EXACTLY ONE provider request, not N.
func TestToolChainDeniedWriteNeverTouchesDisk(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "must-not-exist.txt")

	args, err := json.Marshal(map[string]string{"path": target, "content": "nope"})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	srv, sawTools, turns := twoTurnToolServer(t, "write_file", args, "I was not allowed to write it.")

	cfg := toolsCfg(t, srv.URL, "ask")
	reviewer := &recordingReviewer{decision: permissions.Decision{Allow: false}}
	guard := permissions.New(cfg.Tools.Permissions, false, reviewer)

	result, hist := runToolTurn(t, cfg, guard)

	requireToolsOnWire(t, sawTools)

	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("a denied write_file created %s anyway (stat err = %v)", target, err)
	}
	if len(reviewer.requests()) != 1 {
		t.Errorf("reviewer consulted %d times, want 1", len(reviewer.requests()))
	}
	// The turn ended on the denial, and says so honestly.
	if result.Stopped == "" {
		t.Error("a denied turn must report why it stopped, or the user watches the agent go quiet for no stated reason")
	}
	if !strings.Contains(result.Stopped, "declined") {
		t.Errorf("Stopped = %q, want it to name the human's decision", result.Stopped)
	}

	// Closing criterion 1, asserted by counting. Counting is the point: every
	// other assertion in this test passed happily while the defect was live,
	// because a second request looks like a helpful model rather than like an
	// outage in the making.
	if got := turns.Load(); got != 1 {
		t.Errorf("provider requests = %d, want exactly 1: a denial must not buy the model another turn to route around it", got)
	}
	// The corollary: the model's second-turn answer is unreachable, because
	// the second turn never happens.
	if strings.Contains(result.Text, "not allowed") {
		t.Error("the model answered after the denial, so another provider request was made: this is the amplifier the fix removes")
	}

	// Bug 2 still holds: the refused call has its own tool reply, so the
	// assistant message carries no orphaned tool_call and a --resume of this
	// session does not 400 at the provider forever.
	if !historyHasToolError(hist) {
		t.Error("history carries no tool-error block: the refused call was left orphaned, which poisons every later request")
	}
}

// TestToolChainReadIsAllowedWithoutAsking pins the tier table at the chain
// level: read = "allow" means a Low-tier read never interrupts the user, so
// the reviewer must not be consulted at all. A tool layer that asked about
// every read would be unusable, and this is the guard against "when in
// doubt, ask" creeping into the low tier.
func TestToolChainReadIsAllowedWithoutAsking(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "readme.txt")
	if err := os.WriteFile(target, []byte("file content here"), 0o600); err != nil {
		t.Fatalf("fixture: %v", err)
	}

	args, err := json.Marshal(map[string]string{"path": target})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	srv, sawTools, _ := twoTurnToolServer(t, "read_file", args, "The file says: file content here")

	cfg := toolsCfg(t, srv.URL, "ask")
	reviewer := &recordingReviewer{decision: permissions.Decision{Allow: true}}
	guard := permissions.New(cfg.Tools.Permissions, false, reviewer)

	result, _ := runToolTurn(t, cfg, guard)

	requireToolsOnWire(t, sawTools)

	if n := len(reviewer.requests()); n != 0 {
		t.Errorf("reviewer consulted %d times for a read with read = \"allow\", want 0", n)
	}
	if !strings.Contains(result.Text, "file content here") {
		t.Errorf("final text = %q, want an answer derived from the tool result", result.Text)
	}
}

// TestToolChainWriteDenyIsNotEvenOffered covers the structural defence
// (§19.8): a write_deny path is refused outright, without a dialog. The
// value of that rule is precisely that nothing in the context — no
// persuasive prompt, no compromised model — can get it to say yes, so the
// reviewer must never be reached.
func TestToolChainWriteDenyIsNotEvenOffered(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "secrets.env")

	args, err := json.Marshal(map[string]string{"path": target, "content": "leak"})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	srv, sawTools, _ := twoTurnToolServer(t, "write_file", args, "That path is off limits.")

	cfg := toolsCfg(t, srv.URL, "ask")
	cfg.Tools.Permissions.WriteDeny = []string{"**/*.env"}
	reviewer := &recordingReviewer{decision: permissions.Decision{Allow: true}}
	guard := permissions.New(cfg.Tools.Permissions, false, reviewer)

	runToolTurn(t, cfg, guard)

	requireToolsOnWire(t, sawTools)

	if n := len(reviewer.requests()); n != 0 {
		t.Errorf("reviewer was consulted %d times about a write_deny path, want 0: "+
			"a hard deny must not be presented as an approvable choice", n)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("a write_deny path was written anyway: %s", target)
	}
}

// TestToolChainWithoutCapsExplainsInsteadOfActing reproduces the reported
// bug from the other side, and is the regression test proper: it pins what
// the user actually saw, so the failure is recognizable rather than abstract.
//
// The config asks for tools, but the caps arriving at the streamer are empty
// — the exact state both engine builders used to hard-code. Everything else
// is real. The observable result is the bug report, line for line: prose
// describing an `echo … > file` command, no file on disk, and a reviewer
// that was never consulted, which on screen is an approval dialog that
// never opens. The final assertion is the diagnosis: none of that is the
// tool layer failing, it is the tool layer never being reached.
func TestToolChainWithoutCapsExplainsInsteadOfActing(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "step16-approval.txt")

	args, err := json.Marshal(map[string]string{"path": target, "content": "Step 16 approval works."})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	srv, sawTools, _ := twoTurnToolServer(t, "write_file", args, "Created the file.")

	cfg := toolsCfg(t, srv.URL, "ask")
	reviewer := &recordingReviewer{decision: permissions.Decision{Allow: true}}
	guard := permissions.New(cfg.Tools.Permissions, false, reviewer)

	// The bug, reproduced at its source: empty caps despite tools enabled.
	result, hist := runToolTurnWithCaps(t, cfg, guard, provider.Caps{})

	if sawTools.Load() {
		t.Fatal("empty caps must strip the `tools` array from the request")
	}
	// Symptom 1: prose about a shell command instead of a tool call.
	if !strings.Contains(result.Text, "echo") {
		t.Errorf("final text = %q, want the prose the user saw", result.Text)
	}
	if result.Calls != 0 {
		t.Errorf("result.Calls = %d, want 0: nothing was callable", result.Calls)
	}
	if historyHasToolCall(hist, "write_file") {
		t.Error("history has a write_file call the model was never offered")
	}
	// Symptom 2: `ls` showed nothing.
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("a file appeared without any tool call: %s", target)
	}
	// Symptom 3: no approval dialog ever opened — because the guard was
	// never asked, not because the dialog is broken.
	if n := len(reviewer.requests()); n != 0 {
		t.Errorf("reviewer consulted %d times, want 0: the approval overlay "+
			"could not open because nothing ever requested a tool", n)
	}
}

// requireToolsOnWire fails the test when no request ever carried a `tools`
// array. Every test in this file asserts it, because it is the first link of
// the chain and the one that broke: with `tools` missing, a "passing" test
// downstream is only describing a fake provider's imagination.
func requireToolsOnWire(t *testing.T, sawTools *atomic.Bool) {
	t.Helper()
	if !sawTools.Load() {
		t.Fatal("no `tools` array ever reached the provider: the model was offered nothing to " +
			"call, which is the bug that made Step 16 inert (provider.Caps.Tools was false)")
	}
}

// historyHasToolCall reports whether any message carries a tool-call block
// for name.
func historyHasToolCall(hist *convo.Conversation, name string) bool {
	for _, m := range hist.Messages {
		for _, b := range m.Blocks {
			if b.Kind == convo.BlockToolCall && b.Name == name {
				return true
			}
		}
	}
	return false
}

// historyHasToolError reports whether any tool result came back marked as an
// error — which is how a denial reaches the model.
func historyHasToolError(hist *convo.Conversation) bool {
	for _, m := range hist.Messages {
		for _, b := range m.Blocks {
			if b.Kind == convo.BlockToolResult && b.IsError {
				return true
			}
		}
	}
	return false
}

// TestToolChainRateLimitWaitIsReportedNotSilent is the user-visible half of
// step 26's fix 2. The engine already waited out a Retry-After window; what
// it did not do was say so, and on a phone a silent 22-second pause is
// indistinguishable from a hung agent -- which is exactly when a user kills
// the process and starts again, adding load to an account that is already
// rate-limited.
//
// The window is scaled down to keep the suite fast; the property being
// pinned is that the notice reaches stderr through the real sink, and that
// it is rendered at human granularity rather than as nine digits of
// float-second noise.
func TestToolChainRateLimitWaitIsReportedNotSilent(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"error":{"message":"rate limit exceeded"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, fake.SSEDelta("answered after the wait")+fake.SSEDone())
	}))
	defer srv.Close()

	cfg := toolsCfg(t, srv.URL, "ask")
	code, out, errs := run(t, HeadlessOptions{Config: cfg, Prompt: "x"})

	if code != ExitOK {
		t.Fatalf("code = %d, stderr: %s", code, errs)
	}
	if !strings.Contains(out, "answered after the wait") {
		t.Errorf("stdout = %q, want the answer produced after the retry", out)
	}
	if n := attempts.Load(); n != 2 {
		t.Errorf("provider attempts = %d, want 2 (the 429 then the retry)", n)
	}
	if !strings.Contains(errs, "rate limited") {
		t.Errorf("the wait must be reported on stderr, stderr = %q", errs)
	}
	// Human granularity: "1s", never "1.0837462s".
	if strings.Contains(errs, "ms") && strings.Contains(errs, ".") {
		t.Errorf("the wait should be rounded for a 40-column screen, stderr = %q", errs)
	}
}

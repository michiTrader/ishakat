package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/provider/fake"
)

// Headless mode is the whole-system test bench (§Step 5): these tests walk
// configuration -> model resolution -> adapter -> SSE -> persistence without
// opening a terminal or touching the user's disk.

// cfgFor builds a minimal configuration pointing at the test server.
func cfgFor(t *testing.T, baseURL string) *config.Config {
	t.Helper()
	return &config.Config{
		Schema: config.Schema,
		App: config.App{
			DefaultModel:    "omniroute/auto/coding",
			Stream:          true,
			TimeoutS:        30,
			ConnectTimeoutS: 5,
			MaxRetries:      2,
		},
		Session: config.Session{Save: false, Dir: t.TempDir(), KeepLast: 10},
		UI:      config.UI{Reasoning: "collapsed", Color: "off"},
		Providers: []config.Provider{{
			ID: "omniroute", Kind: "openai", BaseURL: baseURL,
			APIKey: "test-key", Enabled: true, AuthOK: true,
		}},
	}
}

// run executes Headless with stdin closed and returns the exit code, stdout
// and stderr.
func run(t *testing.T, opts HeadlessOptions) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	opts.Stdout = &out
	opts.Stderr = &errb
	if opts.Stdin == nil {
		opts.Stdin = strings.NewReader("")
	}
	noTTY := false
	if opts.StdinTTY == nil {
		opts.StdinTTY = &noTTY
	}
	if opts.StderrTTY == nil {
		opts.StderrTTY = &noTTY
	}
	code := Headless(opts)
	return code, out.String(), errb.String()
}

func TestHeadlessCleanTextOnStdout(t *testing.T) {
	srv := fake.SSEServer(fake.SSEOptions{Chunks: []string{
		fake.SSEDelta("Hello"),
		fake.SSEDelta(", "),
		fake.SSEDelta("world"),
		fake.SSEChunk(`{"choices":[],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}`),
		fake.SSEDone(),
	}})
	defer srv.Close()

	code, out, errs := run(t, HeadlessOptions{
		Config: cfgFor(t, srv.URL),
		Prompt: "say hi",
	})

	if code != ExitOK {
		t.Fatalf("code = %d, expected 0. stderr: %s", code, errs)
	}
	// The contract with `| cat`: stdout carries only the response, with a
	// single trailing newline.
	if out != "Hello, world\n" {
		t.Errorf("stdout = %q, expected %q", out, "Hello, world\n")
	}
	if errs != "" {
		t.Errorf("stderr should be empty, got: %q", errs)
	}
}

// The trailing newline is only added if missing: a duplicate \n breaks
// $(…) in a script and a missing one glues the shell prompt to the last
// token.
func TestHeadlessDoesNotDuplicateTrailingNewline(t *testing.T) {
	srv := fake.SSEServer(fake.SSEOptions{Chunks: []string{
		fake.SSEDelta("one line\n"), fake.SSEDone(),
	}})
	defer srv.Close()

	_, out, _ := run(t, HeadlessOptions{Config: cfgFor(t, srv.URL), Prompt: "x"})
	if out != "one line\n" {
		t.Errorf("stdout = %q, expected no duplicated newline", out)
	}
}

func TestHeadlessJSONOneEventPerLine(t *testing.T) {
	srv := fake.SSEServer(fake.SSEOptions{Chunks: []string{
		fake.SSEDelta("one "),
		fake.SSEDelta("two"),
		fake.SSEChunk(`{"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`),
		fake.SSEDone(),
	}})
	defer srv.Close()

	code, out, _ := run(t, HeadlessOptions{
		Config: cfgFor(t, srv.URL), Prompt: "x", JSON: true,
	})
	if code != ExitOK {
		t.Fatalf("code = %d", code)
	}

	var kinds []string
	var text strings.Builder
	var doneEv jsonEvent
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		var ev jsonEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("line jq could not read: %q (%v)", line, err)
		}
		kinds = append(kinds, ev.Type)
		if ev.Type == "delta" {
			text.WriteString(ev.Text)
		}
		if ev.Type == "done" {
			doneEv = ev
		}
	}

	if kinds[0] != "meta" {
		t.Errorf("the first event must be meta, got %q", kinds[0])
	}
	if last := kinds[len(kinds)-1]; last != "done" {
		t.Errorf("the last event must be done, got %q", last)
	}
	if text.String() != "one two" {
		t.Errorf("deltas reconstruct %q, expected %q", text.String(), "one two")
	}
	if doneEv.Text != "one two" {
		t.Errorf("done.text = %q", doneEv.Text)
	}
	if doneEv.Usage == nil || doneEv.Usage.In != 5 || doneEv.Usage.Out != 2 {
		t.Errorf("done.usage = %+v, expected in=5 out=2", doneEv.Usage)
	}
}

// "If stdin isn't a TTY, read the prompt from stdin and append it":
// instruction first, material after.
func TestHeadlessConcatenatesStdin(t *testing.T) {
	var sent atomic.Value
	srv := fake.SSEServer(fake.SSEOptions{
		Chunks:    []string{fake.SSEDelta("ok"), fake.SSEDone()},
		OnRequest: func(_ *http.Request, body []byte) { sent.Store(string(body)) },
	})
	defer srv.Close()

	code, _, _ := run(t, HeadlessOptions{
		Config: cfgFor(t, srv.URL),
		Prompt: "explain this error:",
		Stdin:  strings.NewReader("panic: index out of range\n"),
	})
	if code != ExitOK {
		t.Fatalf("code = %d", code)
	}

	body, _ := sent.Load().(string)
	if !strings.Contains(body, "explain this error:") || !strings.Contains(body, "index out of range") {
		t.Errorf("the sent body doesn't concatenate flag + stdin: %s", body)
	}
	if i, j := strings.Index(body, "explain"), strings.Index(body, "panic"); i > j {
		t.Error("the order must be instruction then material")
	}
}

// Without -p but with stdin connected, all of stdin is the prompt:
// `echo hi | ishakat`.
func TestHeadlessStdinOnly(t *testing.T) {
	srv := fake.SSEServer(fake.SSEOptions{Chunks: []string{fake.SSEDelta("ok"), fake.SSEDone()}})
	defer srv.Close()

	code, out, errs := run(t, HeadlessOptions{
		Config: cfgFor(t, srv.URL),
		Stdin:  strings.NewReader("what's two plus two?"),
	})
	if code != ExitOK {
		t.Fatalf("code = %d, stderr: %s", code, errs)
	}
	if out != "ok\n" {
		t.Errorf("stdout = %q", out)
	}
}

func TestHeadlessNoPromptIsUsageError(t *testing.T) {
	srv := fake.SSEServer(fake.SSEOptions{Chunks: []string{fake.SSEDone()}})
	defer srv.Close()

	code, out, errs := run(t, HeadlessOptions{Config: cfgFor(t, srv.URL)})
	if code != ExitUsage {
		t.Errorf("code = %d, expected %d (usage error)", code, ExitUsage)
	}
	if out != "" {
		t.Errorf("stdout should be empty, got %q", out)
	}
	if !strings.Contains(errs, "-p") {
		t.Errorf("the error must say how to pass the prompt, it says: %q", errs)
	}
}

// TestHeadlessSilencesWarningsForUnusedProviders is the regression test for
// the "warnings by necessity" fix: a configuration that declares several
// providers but resolves to only one of them for this turn must not print
// a missing-credential warning about the other providers it never touches.
// This is the fix for the noise that once sent a debugging session chasing
// app.default_model/omniroute instead of the actual bug (see
// docs/PLAN.md's 2026-08-06 audit entries).
func TestHeadlessSilencesWarningsForUnusedProviders(t *testing.T) {
	srv := fake.SSEServer(fake.SSEOptions{Chunks: []string{
		fake.SSEDelta("hi"),
		fake.SSEDone(),
	}})
	defer srv.Close()

	cfg := cfgFor(t, srv.URL)
	cfg.Providers = append(cfg.Providers, config.Provider{
		ID: "openai", Kind: "openai", BaseURL: "https://api.openai.com/v1",
		Enabled: true, AuthOK: false, MissingEnv: "OPENAI_API_KEY",
	})
	cfg.Warnings = []config.Warning{
		{Where: "provider[openai]", Msg: "missing $OPENAI_API_KEY; the provider is left unauthenticated"},
	}

	code, _, errs := run(t, HeadlessOptions{Config: cfg, Prompt: "hi"})
	if code != ExitOK {
		t.Fatalf("code = %d, stderr: %s", code, errs)
	}
	if strings.Contains(errs, "OPENAI_API_KEY") {
		t.Errorf("stderr must not mention a provider this turn never used, got: %q", errs)
	}
}

// TestHeadlessKeepsWarningForTheProviderActuallyUsed is the other half of
// the same fix: a missing-credential warning about the provider this turn
// DOES resolve to must still be printed, not silenced along with the
// unrelated ones.
func TestHeadlessKeepsWarningForTheProviderActuallyUsed(t *testing.T) {
	srv := fake.SSEServer(fake.SSEOptions{Chunks: []string{fake.SSEDone()}})
	defer srv.Close()

	cfg := cfgFor(t, srv.URL)
	cfg.Warnings = []config.Warning{
		{Where: "provider[omniroute]", Msg: "missing $OMNIROUTE_API_KEY; the provider is left unauthenticated"},
	}

	_, _, errs := run(t, HeadlessOptions{Config: cfg, Prompt: "hi"})
	if !strings.Contains(errs, "OMNIROUTE_API_KEY") {
		t.Errorf("stderr must keep the warning about the provider this turn used, got: %q", errs)
	}
}

// A 401 must come out on stderr, with exit code 1 and nothing written to
// stdout: a script that redirects the output should not end up with a file
// that contains an error message.
func TestHeadlessHTTPErrorDoesNotPollutStdout(t *testing.T) {
	srv := fake.SSEServer(fake.SSEOptions{
		Status: http.StatusUnauthorized,
		Body:   `{"error":{"message":"invalid key","type":"invalid_request_error"}}`,
	})
	defer srv.Close()

	code, out, errs := run(t, HeadlessOptions{Config: cfgFor(t, srv.URL), Prompt: "x"})
	if code != ExitError {
		t.Errorf("code = %d, expected 1", code)
	}
	if out != "" {
		t.Errorf("stdout should be empty, got %q", out)
	}
	if !strings.Contains(errs, "invalid key") {
		t.Errorf("stderr must carry the service's message, got: %q", errs)
	}
}

// 429 with Retry-After: the handshake is retried honoring the header and the
// turn finishes successfully. Retrying here is safe because nothing was
// printed yet; mid-stream is never retried.
func TestHeadlessRetries429(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"error":{"message":"too many requests"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, fake.SSEDelta("on the second try")+fake.SSEDone())
	}))
	defer srv.Close()

	code, out, errs := run(t, HeadlessOptions{Config: cfgFor(t, srv.URL), Prompt: "x"})
	if code != ExitOK {
		t.Fatalf("code = %d, stderr: %s", code, errs)
	}
	if out != "on the second try\n" {
		t.Errorf("stdout = %q", out)
	}
	if n := attempts.Load(); n != 2 {
		t.Errorf("attempts = %d, expected 2", n)
	}
	if !strings.Contains(errs, "retry") {
		t.Errorf("the retry must be reported on stderr, stderr = %q", errs)
	}
}

// A stream that cuts off without [DONE] keeps what was received, warns, and
// returns 1. Losing already-generated text would be worse than the cut
// itself.
func TestHeadlessTruncatedStreamKeepsThePartial(t *testing.T) {
	srv := fake.SSEServer(fake.SSEOptions{Chunks: []string{
		fake.SSEDelta("half of the resp"),
	}})
	defer srv.Close()

	code, out, errs := run(t, HeadlessOptions{Config: cfgFor(t, srv.URL), Prompt: "x"})
	if code != ExitError {
		t.Errorf("code = %d, expected 1", code)
	}
	if !strings.Contains(out, "half of the resp") {
		t.Errorf("the partial response must reach stdout, stdout = %q", out)
	}
	if !strings.Contains(errs, "✗") {
		t.Errorf("the cut must be reported on stderr, stderr = %q", errs)
	}
}

func TestHeadlessSavesSessionJSONL(t *testing.T) {
	srv := fake.SSEServer(fake.SSEOptions{Chunks: []string{
		fake.SSEDelta("saved response"),
		fake.SSEChunk(`{"choices":[],"usage":{"prompt_tokens":4,"completion_tokens":2}}`),
		fake.SSEDone(),
	}})
	defer srv.Close()

	dir := t.TempDir()
	cfg := cfgFor(t, srv.URL)
	cfg.Session.Save = true
	cfg.Session.Dir = dir

	code, _, errs := run(t, HeadlessOptions{Config: cfg, Prompt: "long question\nsecond line"})
	if code != ExitOK {
		t.Fatalf("code = %d, stderr: %s", code, errs)
	}

	files, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if len(files) != 1 {
		t.Fatalf("session files = %d, expected 1", len(files))
	}
	raw, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines = %d, expected 3 (header, user, assistant):\n%s", len(lines), raw)
	}
	if !strings.Contains(lines[0], `"type":"header"`) {
		t.Errorf("the first line must be the header: %s", lines[0])
	}
	// The title is the first line of the prompt, not the whole prompt.
	if !strings.Contains(lines[0], `"title":"long question"`) {
		t.Errorf("unexpected title: %s", lines[0])
	}
	if !strings.Contains(lines[1], `"role":"user"`) {
		t.Errorf("the second line must be the user's message: %s", lines[1])
	}
	if !strings.Contains(lines[2], `"role":"assistant"`) || !strings.Contains(lines[2], "saved response") {
		t.Errorf("the third line must be the response: %s", lines[2])
	}
	// The model is recorded with the full Ref, not the wire_id.
	if !strings.Contains(lines[2], `"model":"omniroute/auto/coding"`) {
		t.Errorf("model Ref missing on the message: %s", lines[2])
	}
}

func TestHeadlessNoSaveWritesNothing(t *testing.T) {
	srv := fake.SSEServer(fake.SSEOptions{Chunks: []string{fake.SSEDelta("ok"), fake.SSEDone()}})
	defer srv.Close()

	dir := t.TempDir()
	cfg := cfgFor(t, srv.URL)
	cfg.Session.Save = true
	cfg.Session.Dir = dir
	no := false

	if code, _, errs := run(t, HeadlessOptions{Config: cfg, Prompt: "x", Save: &no}); code != ExitOK {
		t.Fatalf("code = %d, stderr: %s", code, errs)
	}
	if files, _ := filepath.Glob(filepath.Join(dir, "*.jsonl")); len(files) != 0 {
		t.Errorf("--no-save wrote %d files", len(files))
	}
}

// --no-stream requests the full response: the sent body carries
// stream:false and the event channel still delivers the text, in a single
// delta.
func TestHeadlessNoStreaming(t *testing.T) {
	var sent atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := new(bytes.Buffer)
		buf.ReadFrom(r.Body)
		sent.Store(buf.String())
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"index":0,"message":{"role":"assistant","content":"whole"}}],`+
			`"usage":{"prompt_tokens":3,"completion_tokens":1}}`)
	}))
	defer srv.Close()

	no := false
	code, out, errs := run(t, HeadlessOptions{
		Config: cfgFor(t, srv.URL), Prompt: "x", Stream: &no,
	})
	if code != ExitOK {
		t.Fatalf("code = %d, stderr: %s", code, errs)
	}
	if out != "whole\n" {
		t.Errorf("stdout = %q", out)
	}
	if body, _ := sent.Load().(string); !strings.Contains(body, `"stream":false`) {
		t.Errorf("the body should request stream:false, got: %s", body)
	}
}

// The flag's system prompt wins over the configuration's and travels as the
// first history message (§5.4).
func TestHeadlessSystemPrompt(t *testing.T) {
	var sent atomic.Value
	srv := fake.SSEServer(fake.SSEOptions{
		Chunks:    []string{fake.SSEDelta("ok"), fake.SSEDone()},
		OnRequest: func(_ *http.Request, body []byte) { sent.Store(string(body)) },
	})
	defer srv.Close()

	cfg := cfgFor(t, srv.URL)
	cfg.App.SystemPrompt = "the config one"

	if code, _, errs := run(t, HeadlessOptions{
		Config: cfg, Prompt: "x", System: "reply in Catalan",
	}); code != ExitOK {
		t.Fatalf("code = %d, stderr: %s", code, errs)
	}

	body, _ := sent.Load().(string)
	if !strings.Contains(body, "reply in Catalan") {
		t.Errorf("missing the flag's system prompt: %s", body)
	}
	if strings.Contains(body, "the config one") {
		t.Errorf("the flag's system prompt must win over the config's: %s", body)
	}
}

// TestHeadlessFallsBackWhenDefaultModelIsUnusable is P2's headless-mode
// integration test: app.default_model names a disabled provider, a second
// provider (gemini-direct, a real preset id) is usable and points at the
// same fake server, and `ishakat -p "..."` must still answer — with a
// single ⚠ line on stderr naming what it did, instead of exiting 1 the way
// it did before ResolveModelForBoot existed (the exact headless failure
// mode from the original bug report this session responds to).
func TestHeadlessFallsBackWhenDefaultModelIsUnusable(t *testing.T) {
	srv := fake.SSEServer(fake.SSEOptions{Chunks: []string{
		fake.SSEDelta("hi"), fake.SSEDone(),
	}})
	defer srv.Close()

	cfg := cfgFor(t, srv.URL)
	cfg.Providers[0].Enabled = false
	cfg.Providers = append(cfg.Providers, config.Provider{
		ID: "gemini-direct", Kind: "openai", BaseURL: srv.URL,
		APIKey: "test-key", Enabled: true, AuthOK: true,
	})

	code, out, errs := run(t, HeadlessOptions{Config: cfg, Prompt: "say hi"})
	if code != ExitOK {
		t.Fatalf("code = %d, expected 0. stderr: %s", code, errs)
	}
	if out != "hi\n" {
		t.Errorf("stdout = %q, expected %q", out, "hi\n")
	}
	if !strings.Contains(errs, "gemini-direct") {
		t.Errorf("stderr must name the fallback provider, got: %q", errs)
	}
}

// Reasoning must not pollute stdout with ui.reasoning = "collapsed", and it
// shows up on stderr with "full". In a pipe there's nothing to collapse.
func TestHeadlessReasoningStaysOffStdout(t *testing.T) {
	chunks := []string{
		fake.SSEChunk(`{"choices":[{"index":0,"delta":{"reasoning_content":"thinking…"}}]}`),
		fake.SSEDelta("response"),
		fake.SSEDone(),
	}

	srv := fake.SSEServer(fake.SSEOptions{Chunks: chunks})
	defer srv.Close()

	_, out, errs := run(t, HeadlessOptions{Config: cfgFor(t, srv.URL), Prompt: "x"})
	if out != "response\n" {
		t.Errorf("stdout = %q, reasoning must not be there", out)
	}
	if strings.Contains(errs, "thinking") {
		t.Errorf("with reasoning=collapsed it must not appear on stderr either: %q", errs)
	}

	srv2 := fake.SSEServer(fake.SSEOptions{Chunks: chunks})
	defer srv2.Close()
	cfg := cfgFor(t, srv2.URL)
	cfg.UI.Reasoning = "full"

	_, out2, errs2 := run(t, HeadlessOptions{Config: cfg, Prompt: "x"})
	if out2 != "response\n" {
		t.Errorf("stdout with reasoning=full = %q", out2)
	}
	if !strings.Contains(errs2, "thinking") {
		t.Errorf("with reasoning=full it should go to stderr, stderr = %q", errs2)
	}
}

// TestHeadlessWarnsWhenBudgetCannotBePriced is the regression for a bug
// found reviewing PR #82's cost-budget entry: buildAgentOptions
// (agentturn.go) leaves every *CostUSD field at zero when the catalog has
// no Cost for the active model (nil, not the distinct "genuinely free" case
// catalog.Cost.Zero documents), so engine.estimateCost can never reach a
// positive budget_usd no matter how many tool calls run — the ceiling
// silently stops doing anything on exactly the models (new, undiscovered,
// stale local catalog) most likely to need it. This pins the fix: Headless
// must warn once when budget_usd > 0 and the model's price is unknown, so
// the user is not left believing a guard is active when it cannot fire.
func TestHeadlessWarnsWhenBudgetCannotBePriced(t *testing.T) {
	srv := fake.SSEServer(fake.SSEOptions{Chunks: []string{
		fake.SSEDelta("hi"), fake.SSEDone(),
	}})
	defer srv.Close()

	cfg := cfgFor(t, srv.URL)
	cfg.Tools.Enabled = true
	cfg.Tools.BudgetUSD = 0.01
	cfg.Tools.MaxCallsPerTurn = 5

	code, _, errs := run(t, HeadlessOptions{Config: cfg, Prompt: "hi"})
	if code != ExitOK {
		t.Fatalf("code = %d, stderr: %s", code, errs)
	}
	if !strings.Contains(errs, "budget_usd") || !strings.Contains(errs, "cannot be enforced") {
		t.Errorf("stderr must warn that the budget cannot be enforced for an unpriced model, got: %q", errs)
	}
}

func TestBuildPrompt(t *testing.T) {
	cases := []struct {
		name     string
		flag     string
		stdin    string
		stdinTTY bool
		want     string
	}{
		{"flag only, stdin is a terminal", "hi", "ignored", true, "hi"},
		{"flag only, empty stdin", "hi", "", false, "hi"},
		{"flag plus stdin", "summarize:", "long text", false, "summarize:\n\nlong text"},
		{"stdin only", "", "long text", false, "long text"},
		{"blank stdin is not concatenated", "hi", "   \n\n", false, "hi"},
		{"trailing newline from stdin is trimmed", "hi", "line\n", false, "hi\n\nline"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := buildPrompt(c.flag, strings.NewReader(c.stdin), c.stdinTTY)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("buildPrompt = %q, expected %q", got, c.want)
			}
		})
	}
}

// TestHeadlessEffortFlagReachesTheWire pins F9's own headless-equivalent
// (docs/ROADMAP-ux-2026-08-20.md W5, HeadlessOptions.Effort's own doc
// comment): --effort must reach the request body through the exact same
// EffortParamsFor(pc, level) call the interactive TUI's EffortResolver
// makes, keyed by the resolved provider's own dialect ("openai" here, via
// cfgFor, so the wire key is the flat "reasoning_effort").
func TestHeadlessEffortFlagReachesTheWire(t *testing.T) {
	var gotBody map[string]any
	srv := fake.SSEServer(fake.SSEOptions{
		Chunks: []string{fake.SSEDelta("ok"), fake.SSEDone()},
		OnRequest: func(_ *http.Request, body []byte) {
			_ = json.Unmarshal(body, &gotBody)
		},
	})
	defer srv.Close()

	code, _, _ := run(t, HeadlessOptions{
		Config: cfgFor(t, srv.URL),
		Prompt: "hi",
		Effort: "high",
	})
	if code != ExitOK {
		t.Fatalf("code = %d", code)
	}

	if gotBody["reasoning_effort"] != "high" {
		t.Errorf("the sent body does not carry the --effort override: %+v", gotBody)
	}
}

// TestHeadlessNoEffortFlagOmitsTheParam is the companion negative case: an
// unset --effort (the ordinary, pre-F9 case) must not add any params
// field at all, matching EffortParams' own "nil, not an empty map" rule
// for "nothing to ask for" (effort_test.go's own
// TestEffortParamsEmptyLevelReturnsNil).
func TestHeadlessNoEffortFlagOmitsTheParam(t *testing.T) {
	var gotBody map[string]any
	srv := fake.SSEServer(fake.SSEOptions{
		Chunks: []string{fake.SSEDelta("ok"), fake.SSEDone()},
		OnRequest: func(_ *http.Request, body []byte) {
			_ = json.Unmarshal(body, &gotBody)
		},
	})
	defer srv.Close()

	code, _, _ := run(t, HeadlessOptions{
		Config: cfgFor(t, srv.URL),
		Prompt: "hi",
	})
	if code != ExitOK {
		t.Fatalf("code = %d", code)
	}

	if _, has := gotBody["reasoning_effort"]; has {
		t.Errorf("the sent body should not carry reasoning_effort when --effort is unset: %+v", gotBody)
	}
}

func TestTitleFrom(t *testing.T) {
	if got := titleFrom("  a question \n and more "); got != "a question" {
		t.Errorf("titleFrom = %q", got)
	}
	if got := titleFrom(""); got != "new conversation" {
		t.Errorf("titleFrom empty = %q", got)
	}
	long := strings.Repeat("a", 100)
	if got := titleFrom(long); len([]rune(got)) != 61 { // 60 runes + …
		t.Errorf("titleFrom did not trim to 60 runes: %d", len([]rune(got)))
	}
}

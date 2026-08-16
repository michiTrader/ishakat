// serve_test.go is Step 23's own offline end-to-end test, in the same
// "no mocks on the wire" spirit toolchain_e2e_test.go and
// dispatch_e2e_test.go already established: a real net.Listener, a real
// wsproto.Dial client, and Serve itself running against a fake HTTP
// provider -- nothing about the WebSocket transport or the NDJSON framing
// is faked, only the model behind the fake SSE server is scripted.
//
// What each test proves, matching this file's own closing checklist from
// the previous session:
//
//   - TestServeRoundTripsAPromptToDone: connect, get "hello", send a
//     "prompt", see "meta" then "delta" then "done" -- the ordinary,
//     no-tools path (cfg.Tools.Enabled == false), exercising runTurn.
//   - TestServePermissionRoundTrip: a tool call reaches a real
//     permissions.Guard, which (since a real, non-nil serveReviewer is
//     wired -- this file's own headline distinction from Headless) emits
//     a "permission_request" event over the socket and blocks until this
//     test's own fake client answers with "permission_response". This is
//     the §19.7 round trip serve.go's own doc comment describes: a
//     connected client is a genuine decision-maker, unlike headless's
//     always-nil reviewer.
//   - TestServeRejectsWrongToken / TestServeRejectsMissingToken: the
//     bearer-token check in wsServer.ServeHTTP, from outside the process.
//   - TestServeEnforcesMaxSessions: a session beyond MaxSessions gets
//     HTTP 503, and closing one frees the slot for the next connection.
//   - TestServeIdleTimeoutClosesConnection: a connection that sends
//     nothing for longer than IdleTimeoutS gets closed by the server.
package app

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/provider/fake"
	"github.com/MichiTrader/ishakat/internal/wsproto"
)

// serveCfg builds a minimal configuration for Serve, mirroring
// toolchain_e2e_test.go's own toolsCfg/cfgFor pair: a fake provider's
// baseURL stands in for a real one, and every path this test would
// otherwise touch on the real filesystem (catalog cache, sessions) is
// redirected into a t.TempDir() so the test never reads or writes the
// user's real XDG directories.
func serveCfg(t *testing.T, baseURL string, toolsEnabled bool) *config.Config {
	t.Helper()
	cfg := &config.Config{
		Schema: config.Schema,
		App: config.App{
			DefaultModel:    "omniroute/auto/coding",
			Stream:          true,
			TimeoutS:        30,
			ConnectTimeoutS: 5,
			MaxRetries:      0,
		},
		Session: config.Session{Save: false, Dir: t.TempDir(), KeepLast: 10},
		UI:      config.UI{Reasoning: "collapsed", Color: "off"},
		Catalog: config.Catalog{CacheFile: t.TempDir() + "/catalog.json", TTLHours: 24},
		Providers: []config.Provider{{
			ID: "omniroute", Kind: "openai", BaseURL: baseURL,
			APIKey: "test-key", Enabled: true, AuthOK: true,
		}},
		Serve: config.Serve{
			Addr:        "127.0.0.1:0",
			MaxSessions: 0,
		},
	}
	if toolsEnabled {
		cfg.Tools = config.Tools{
			Enabled:         true,
			MaxCallsPerTurn: 5,
			MaxOutputBytes:  4096,
			Permissions: config.Permissions{
				Read: "allow", Write: "ask", Shell: "ask", AllowSession: true,
			},
		}
	}
	return cfg
}

// startServe runs Serve on an ephemeral loopback listener in its own
// goroutine (opts.Listener is the test seam ServeOptions' own doc comment
// names for exactly this), and returns the ws:// URL to dial plus a
// cleanup function that stops the server and waits for Serve to return --
// so a test never leaves a background goroutine holding the listener open
// past its own end, which would otherwise leak across parallel test
// binaries reusing the same TCP stack.
func startServe(t *testing.T, opts ServeOptions) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	opts.Listener = ln
	opts.Version = "0.0.0-test"

	var out, errb discardWriter
	opts.Stdout = &out
	opts.Stderr = &errb

	done := make(chan int, 1)
	go func() { done <- Serve(opts) }()

	// Serve.go's own doc comment on shutdown relies on a signal.
	// NotifyContext -- there is no direct "stop" call exposed, so this
	// test closes the listener itself, which unblocks httpSrv.Serve(ln)
	// with http.ErrServerClosed (Serve's own error check already treats
	// that as a clean exit) exactly as a real SIGTERM would via ctx.Done.
	t.Cleanup(func() {
		_ = ln.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Log("serve did not exit within 2s of listener close")
		}
	})

	return "ws://" + ln.Addr().String() + "/"
}

// discardWriter is a minimal io.Writer that throws everything away --
// Serve's own Stdout/Stderr are only used for the one-line startup banner
// and warnings, neither of which this file's tests assert on.
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

// dialServe opens a client connection with header carrying the bearer
// token (when non-empty), the same "Authorization: Bearer <token>" shape
// checkBearerToken (serve.go) checks for first.
func dialServe(t *testing.T, wsURL, token string) *wsproto.Conn {
	t.Helper()
	var header http.Header
	if token != "" {
		header = http.Header{"Authorization": []string{"Bearer " + token}}
	}
	conn, err := wsproto.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// readEvent reads one NDJSON line off conn and decodes it into a
// serveEvent, failing the test after a generous timeout rather than
// hanging forever if the server never sends what a test expects --
// exactly the discipline wsproto_test.go's own TestRoundTripTextMessage
// applies to its "server never upgraded" branch.
func readEvent(t *testing.T, conn *wsproto.Conn) serveEvent {
	t.Helper()
	type result struct {
		ev  serveEvent
		err error
	}
	ch := make(chan result, 1)
	go func() {
		_, payload, err := conn.ReadMessage()
		if err != nil {
			ch <- result{err: err}
			return
		}
		var ev serveEvent
		if err := json.Unmarshal(payload, &ev); err != nil {
			ch <- result{err: fmt.Errorf("invalid JSON %q: %w", payload, err)}
			return
		}
		ch <- result{ev: ev}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("readEvent: %v", r.err)
		}
		return r.ev
	case <-time.After(5 * time.Second):
		t.Fatal("readEvent: timed out waiting for a server event")
		return serveEvent{}
	}
}

// readEventUntil reads events until one matches typ or the count limit is
// hit, skipping intermediate events (a "warning" from LoadCatalog's own
// stale-cache note, for instance) that are not the point of a given test.
func readEventUntil(t *testing.T, conn *wsproto.Conn, typ string) serveEvent {
	t.Helper()
	for i := 0; i < 20; i++ {
		ev := readEvent(t, conn)
		if ev.Type == typ {
			return ev
		}
	}
	t.Fatalf("never saw a %q event within 20 messages", typ)
	return serveEvent{}
}

func sendClientMsg(t *testing.T, conn *wsproto.Conn, msg clientMsg) {
	t.Helper()
	b, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal clientMsg: %v", err)
	}
	if err := conn.WriteMessage(wsproto.OpText, b); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}
}

// TestServeRoundTripsAPromptToDone drives the ordinary, tools-disabled
// path end to end: connect (hello), send a prompt, and see meta -> delta
// -> done in order, over a real socket against a real net.Listener.
func TestServeRoundTripsAPromptToDone(t *testing.T) {
	srv := fake.SSEServer(fake.SSEOptions{Chunks: []string{
		fake.SSEDelta("Hello"),
		fake.SSEDelta(" there"),
		fake.SSEDone(),
	}})
	defer srv.Close()

	cfg := serveCfg(t, srv.URL, false)
	wsURL := startServe(t, ServeOptions{Config: cfg})

	conn := dialServe(t, wsURL, "")

	hello := readEvent(t, conn)
	if hello.Type != "hello" {
		t.Fatalf("first event = %q, want hello", hello.Type)
	}
	if hello.Version != "0.0.0-test" {
		t.Errorf("hello.Version = %q, want 0.0.0-test", hello.Version)
	}

	sendClientMsg(t, conn, clientMsg{Type: "prompt", Text: "say hi"})

	meta := readEventUntil(t, conn, "meta")
	if meta.Model != "omniroute/auto/coding" {
		t.Errorf("meta.Model = %q, want omniroute/auto/coding", meta.Model)
	}

	var text strings.Builder
	var sawDelta, sawDone bool
	for i := 0; i < 20 && !sawDone; i++ {
		ev := readEvent(t, conn)
		switch ev.Type {
		case "delta":
			sawDelta = true
			text.WriteString(ev.Text)
		case "done":
			sawDone = true
			if !strings.Contains(ev.Text, "Hello there") {
				t.Errorf("done.Text = %q, want it to contain %q", ev.Text, "Hello there")
			}
		case "error":
			t.Fatalf("unexpected error event: %s", ev.Text)
		}
	}
	if !sawDelta {
		t.Error("never saw a delta event")
	}
	if !sawDone {
		t.Fatal("never saw a done event")
	}
	if !strings.Contains(text.String(), "Hello there") {
		t.Errorf("accumulated delta text = %q, want it to contain %q", text.String(), "Hello there")
	}
}

// TestServePermissionRoundTrip is this file's headline case, and the one
// no other test in the package can exercise: a real permission_request
// sent over the wire, answered by this test's own fake client (standing
// in for the "genuine decision-maker" serve.go's doc comment describes),
// unblocking the exact same permissions.Guard.Authorize call the TUI and
// dispatch e2e tests exercise through their own reviewers instead.
func TestServePermissionRoundTrip(t *testing.T) {
	var turn atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Tools []any `json:"tools"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		write := func(s string) {
			_, _ = w.Write([]byte(s))
			if fl != nil {
				fl.Flush()
			}
		}

		if turn.Add(1) == 1 {
			write(fake.SSEChunk(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call-1","function":{"name":"bash","arguments":"{\"command\":\"echo hi\"}"}}]}}]}`))
			write(fake.SSEChunk(`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`))
			write(fake.SSEDone())
			return
		}
		write(fake.SSEDelta("Ran it."))
		write(fake.SSEDone())
	}))
	defer srv.Close()

	cfg := serveCfg(t, srv.URL, true)
	wsURL := startServe(t, ServeOptions{Config: cfg})

	conn := dialServe(t, wsURL, "")
	_ = readEvent(t, conn) // hello

	sendClientMsg(t, conn, clientMsg{Type: "prompt", Text: "run echo hi"})

	req := readEventUntil(t, conn, "permission_request")
	if req.Name != "bash" {
		t.Errorf("permission_request.Name = %q, want bash", req.Name)
	}
	// "echo hi" is not one of guard.go's recognized safe/controlled/critical
	// bash prefixes, so bashTier correctly falls back to Sensitive ("medium"
	// on the wire) rather than the old unconditional High ("high") every
	// bash command used to get regardless of its argument.
	if req.Tier != "medium" {
		t.Errorf("permission_request.Tier = %q, want medium", req.Tier)
	}
	if req.ID == "" {
		t.Fatal("permission_request.ID is empty; the client has nothing to correlate its response with")
	}

	sendClientMsg(t, conn, clientMsg{Type: "permission_response", ID: req.ID, Allow: true})

	toolResult := readEventUntil(t, conn, "tool_result")
	if toolResult.Name != "bash" {
		t.Errorf("tool_result.Name = %q, want bash", toolResult.Name)
	}
	if toolResult.Error {
		t.Errorf("tool_result.Error = true, want false (the human allowed it): %s", toolResult.Text)
	}

	done := readEventUntil(t, conn, "done")
	if !strings.Contains(done.Text, "Ran it.") {
		t.Errorf("done.Text = %q, want it to contain %q", done.Text, "Ran it.")
	}
}

// TestServePermissionRoundTripDenied is the same shape with the opposite
// human answer, proving a "no" actually reaches the tool loop as a denial
// -- not merely that a "yes" happens to work.
//
// It also pins §21.9 over the socket: a refusal ends the turn. The server
// scripts a second turn that would say "Understood, not running it." and
// this test asserts the client never sees it, because the loop never asks
// for it. What the client gets instead is the tool_result marking the
// failure, a "warning" event carrying AgentResult.Stopped, and a done with
// no post-denial answer -- one provider request for the whole exchange.
//
// Reaching the model to be told what the human just said is precisely the
// amplifier this step removes: the denial was the answer.
func TestServePermissionRoundTripDenied(t *testing.T) {
	var turn atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		write := func(s string) {
			_, _ = w.Write([]byte(s))
			if fl != nil {
				fl.Flush()
			}
		}
		if turn.Add(1) == 1 {
			write(fake.SSEChunk(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call-1","function":{"name":"bash","arguments":"{\"command\":\"echo hi\"}"}}]}}]}`))
			write(fake.SSEChunk(`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`))
			write(fake.SSEDone())
			return
		}
		write(fake.SSEDelta("Understood, not running it."))
		write(fake.SSEDone())
	}))
	defer srv.Close()

	cfg := serveCfg(t, srv.URL, true)
	wsURL := startServe(t, ServeOptions{Config: cfg})

	conn := dialServe(t, wsURL, "")
	_ = readEvent(t, conn) // hello
	sendClientMsg(t, conn, clientMsg{Type: "prompt", Text: "run echo hi"})

	req := readEventUntil(t, conn, "permission_request")
	sendClientMsg(t, conn, clientMsg{Type: "permission_response", ID: req.ID, Allow: false})

	toolResult := readEventUntil(t, conn, "tool_result")
	if !toolResult.Error {
		t.Error("tool_result.Error = false, want true (the human denied it)")
	}

	// The reason the turn ended has to reach the client, or a denial would
	// look indistinguishable from the model simply falling silent.
	warn := readEventUntil(t, conn, "warning")
	if !strings.Contains(warn.Text, "declined") {
		t.Errorf("warning.Text = %q, want it to say the user declined", warn.Text)
	}

	done := readEventUntil(t, conn, "done")
	if strings.Contains(done.Text, "Understood, not running it.") {
		t.Errorf("done.Text = %q: the loop went back to the model after a refusal", done.Text)
	}

	// The closing criterion, measured rather than inferred: one prompt plus
	// one denial must cost exactly one provider request.
	if n := turn.Load(); n != 1 {
		t.Errorf("provider requests = %d, want 1; a denied turn must not ask the model again", n)
	}
}

// TestServeAskUserRoundTrip is serveAsker's own end-to-end proof, the
// ask_request/ask_response sibling of TestServePermissionRoundTrip above:
// the model calls ask_user, this test's own fake client answers with an
// ask_response naming the same question ID askUserQuestionID
// (internal/tools/ask_user.go) always uses, and the model's second turn
// receives that answer as the tool's OK result -- proving §21.7's own
// door table entry for serve ("ask available? yes, over WS") actually
// resolves a real ask_user call, not merely that the wire types compile.
func TestServeAskUserRoundTrip(t *testing.T) {
	var turn atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		write := func(s string) {
			_, _ = w.Write([]byte(s))
			if fl != nil {
				fl.Flush()
			}
		}
		if turn.Add(1) == 1 {
			write(fake.SSEChunk(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call-1","function":{"name":"ask_user","arguments":"{\"question\":\"which color?\",\"options\":[\"red\",\"blue\"]}"}}]}}]}`))
			write(fake.SSEChunk(`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`))
			write(fake.SSEDone())
			return
		}
		write(fake.SSEDelta("Got it: blue."))
		write(fake.SSEDone())
	}))
	defer srv.Close()

	cfg := serveCfg(t, srv.URL, true)
	wsURL := startServe(t, ServeOptions{Config: cfg})

	conn := dialServe(t, wsURL, "")
	_ = readEvent(t, conn) // hello

	sendClientMsg(t, conn, clientMsg{Type: "prompt", Text: "pick a color"})

	req := readEventUntil(t, conn, "ask_request")
	if req.ID == "" {
		t.Fatal("ask_request.ID is empty; the client has nothing to correlate its response with")
	}
	var form struct {
		Questions []struct {
			ID     string `json:"ID"`
			Prompt string `json:"Prompt"`
		} `json:"Questions"`
	}
	if err := json.Unmarshal(req.Form, &form); err != nil {
		t.Fatalf("ask_request.Form did not decode: %v", err)
	}
	if len(form.Questions) != 1 || form.Questions[0].Prompt != "which color?" {
		t.Fatalf("ask_request.Form questions = %+v, want one question prompting \"which color?\"", form.Questions)
	}
	qid := form.Questions[0].ID

	answers, err := json.Marshal(map[string]any{qid: map[string]string{"Value": "blue"}})
	if err != nil {
		t.Fatalf("marshal answers: %v", err)
	}
	sendClientMsg(t, conn, clientMsg{Type: "ask_response", ID: req.ID, Answers: answers})

	toolResult := readEventUntil(t, conn, "tool_result")
	if toolResult.Name != "ask_user" {
		t.Errorf("tool_result.Name = %q, want ask_user", toolResult.Name)
	}
	if toolResult.Error {
		t.Errorf("tool_result.Error = true, want false (the human answered): %s", toolResult.Text)
	}
	if toolResult.Text != "blue" {
		t.Errorf("tool_result.Text = %q, want %q", toolResult.Text, "blue")
	}

	done := readEventUntil(t, conn, "done")
	if !strings.Contains(done.Text, "Got it: blue.") {
		t.Errorf("done.Text = %q, want it to contain %q", done.Text, "Got it: blue.")
	}
}

// TestServeRejectsWrongToken confirms checkBearerToken's own constant-time
// comparison actually refuses a caller presenting the wrong secret, from
// outside the process -- a plain HTTP GET against the upgrade endpoint.
func TestServeRejectsWrongToken(t *testing.T) {
	cfg := serveCfg(t, "http://127.0.0.1:0", false)
	cfg.Serve.Token = "correct-token"
	wsURL := startServe(t, ServeOptions{Config: cfg})

	_, err := wsproto.Dial(wsURL, http.Header{"Authorization": []string{"Bearer wrong-token"}})
	if err == nil {
		t.Fatal("Dial succeeded with the wrong token, want an error")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("Dial error = %v, want it to mention HTTP 401", err)
	}
}

// TestServeRejectsMissingToken is the same check with no Authorization
// header at all, and no ?token= query parameter -- the ordinary case of a
// client that never learned the secret.
func TestServeRejectsMissingToken(t *testing.T) {
	cfg := serveCfg(t, "http://127.0.0.1:0", false)
	cfg.Serve.Token = "correct-token"
	wsURL := startServe(t, ServeOptions{Config: cfg})

	_, err := wsproto.Dial(wsURL, nil)
	if err == nil {
		t.Fatal("Dial succeeded with no token at all, want an error")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("Dial error = %v, want it to mention HTTP 401", err)
	}
}

// TestServeAcceptsCorrectToken is TestServeRejectsWrongToken's positive
// twin: the same configured token, presented correctly, must still let a
// legitimate caller in -- guarding against a fix for the two tests above
// that accidentally denies everyone.
func TestServeAcceptsCorrectToken(t *testing.T) {
	cfg := serveCfg(t, "http://127.0.0.1:0", false)
	cfg.Serve.Token = "correct-token"
	wsURL := startServe(t, ServeOptions{Config: cfg})

	conn := dialServe(t, wsURL, "correct-token")
	hello := readEvent(t, conn)
	if hello.Type != "hello" {
		t.Fatalf("first event = %q, want hello", hello.Type)
	}
}

// TestServeEnforcesMaxSessions confirms a session beyond MaxSessions is
// refused with HTTP 503, and that closing one connection frees its slot
// for the next caller -- the two halves of wsServer's own
// activeSessions.CompareAndSwap bookkeeping.
func TestServeEnforcesMaxSessions(t *testing.T) {
	srv := fake.SSEServer(fake.SSEOptions{Chunks: []string{fake.SSEDelta("hi"), fake.SSEDone()}})
	defer srv.Close()

	cfg := serveCfg(t, srv.URL, false)
	cfg.Serve.MaxSessions = 1
	wsURL := startServe(t, ServeOptions{Config: cfg})

	first := dialServe(t, wsURL, "")
	_ = readEvent(t, first) // hello

	_, err := wsproto.Dial(wsURL, nil)
	if err == nil {
		t.Fatal("second Dial succeeded past MaxSessions=1, want an error")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("second Dial error = %v, want it to mention HTTP 503", err)
	}

	// Freeing the first connection's slot must let a new one in.
	if err := first.Close(); err != nil {
		t.Fatalf("closing first connection: %v", err)
	}

	var lastErr error
	for i := 0; i < 20; i++ {
		var third *wsproto.Conn
		third, lastErr = wsproto.Dial(wsURL, nil)
		if lastErr == nil {
			_ = third.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("a new connection never succeeded after the slot was freed: %v", lastErr)
}

// TestServeIdleTimeoutClosesConnection confirms a connection that sends
// nothing for longer than IdleTimeoutS is closed by the server, not left
// open indefinitely against a caller that vanished (§Serve.IdleTimeoutS's
// own doc comment in config/schema.go).
func TestServeIdleTimeoutClosesConnection(t *testing.T) {
	cfg := serveCfg(t, "http://127.0.0.1:0", false)
	cfg.Serve.IdleTimeoutS = 1
	wsURL := startServe(t, ServeOptions{Config: cfg})

	conn := dialServe(t, wsURL, "")
	_ = readEvent(t, conn) // hello

	// Send nothing and wait past the idle timeout; the server-side
	// idleTimer (serve.go's run) should close the connection, which
	// surfaces here as ReadMessage returning an error rather than
	// blocking forever.
	errCh := make(chan error, 1)
	go func() {
		_, _, err := conn.ReadMessage()
		errCh <- err
	}()

	select {
	case err := <-errCh:
		if err == nil {
			t.Error("ReadMessage returned no error after the idle timeout; want the connection to have closed")
		}
	case <-time.After(4 * time.Second):
		t.Fatal("connection was not closed within 4s of exceeding a 1s idle timeout")
	}
}

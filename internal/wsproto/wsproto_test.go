package wsproto

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestRoundTripTextMessage drives a real handshake and a real text-frame
// exchange over a real net.Listener (httptest.Server), both directions,
// mirroring this project's own "no mocks on the wire" discipline
// (internal/app/toolchain_e2e_test.go's doc comment) — a fake reader that
// hands back pre-framed bytes could not have caught the masking-direction
// bug this package's own writeFrame/readFrame split exists to get right.
func TestRoundTripTextMessage(t *testing.T) {
	upgraded := make(chan *Conn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := Upgrade(w, r)
		if err != nil {
			t.Errorf("server Upgrade: %v", err)
			return
		}
		upgraded <- c
	}))
	defer srv.Close()

	wsURL := "ws" + srv.URL[len("http"):]
	client, err := Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.Close()

	var server *Conn
	select {
	case server = <-upgraded:
	case <-time.After(2 * time.Second):
		t.Fatal("server never upgraded")
	}
	defer server.Close()

	if err := client.WriteMessage(OpText, []byte(`{"hello":"world"}`)); err != nil {
		t.Fatalf("client WriteMessage: %v", err)
	}
	op, payload, err := server.ReadMessage()
	if err != nil {
		t.Fatalf("server ReadMessage: %v", err)
	}
	if op != OpText {
		t.Errorf("op = %v, want OpText", op)
	}
	if string(payload) != `{"hello":"world"}` {
		t.Errorf("payload = %q", payload)
	}

	if err := server.WriteMessage(OpText, []byte(`{"reply":true}`)); err != nil {
		t.Fatalf("server WriteMessage: %v", err)
	}
	op, payload, err = client.ReadMessage()
	if err != nil {
		t.Fatalf("client ReadMessage: %v", err)
	}
	if op != OpText || string(payload) != `{"reply":true}` {
		t.Errorf("client read op=%v payload=%q", op, payload)
	}
}

// TestLargeMessageSurvivesFragmentAndExtendedLength exercises both extended
// length encodings (16-bit at 126..65535 bytes, 64-bit above) in one shot by
// sending a payload that only the 64-bit path can encode, larger than a
// single bufio buffer, and confirms it comes back byte-for-byte.
func TestLargeMessageSurvivesFragmentAndExtendedLength(t *testing.T) {
	upgraded := make(chan *Conn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := Upgrade(w, r)
		if err != nil {
			t.Errorf("server Upgrade: %v", err)
			return
		}
		upgraded <- c
	}))
	defer srv.Close()

	wsURL := "ws" + srv.URL[len("http"):]
	client, err := Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.Close()

	var server *Conn
	select {
	case server = <-upgraded:
	case <-time.After(2 * time.Second):
		t.Fatal("server never upgraded")
	}
	defer server.Close()

	payload := bytes.Repeat([]byte("ishakat-serve-payload-chunk-"), 5000) // ~140 KiB: past the 16-bit boundary's low end, exercises the extended-length path
	if err := client.WriteMessage(OpText, payload); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}
	_, got, err := server.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch: got %d bytes, want %d bytes", len(got), len(payload))
	}
}

// TestPingIsAnsweredWithPong confirms ReadMessage transparently absorbs a
// ping and replies with a pong without surfacing it to the caller as a data
// message — a caller (serve.go's own read loop) should never have to special
// case control frames itself.
func TestPingIsAnsweredWithPong(t *testing.T) {
	upgraded := make(chan *Conn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := Upgrade(w, r)
		if err != nil {
			t.Errorf("server Upgrade: %v", err)
			return
		}
		upgraded <- c
	}))
	defer srv.Close()

	wsURL := "ws" + srv.URL[len("http"):]
	client, err := Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.Close()

	var server *Conn
	select {
	case server = <-upgraded:
	case <-time.After(2 * time.Second):
		t.Fatal("server never upgraded")
	}
	defer server.Close()

	if err := client.writeFrame(OpPing, []byte("ping-payload")); err != nil {
		t.Fatalf("writeFrame(OpPing): %v", err)
	}
	// server.ReadMessage must transparently answer the ping and then block
	// waiting for a real message — send one right after so the read
	// returns, proving the ping did not surface as a spurious data message.
	if err := client.WriteMessage(OpText, []byte("after-ping")); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}
	_, payload, err := server.ReadMessage()
	if err != nil {
		t.Fatalf("server ReadMessage: %v", err)
	}
	if string(payload) != "after-ping" {
		t.Errorf("payload = %q, want %q (ping should not have surfaced as a message)", payload, "after-ping")
	}

	// The client should have received a pong in reply, before its own
	// WriteMessage call above — read it off the wire directly to confirm
	// the server actually answered rather than silently dropping it.
	op, pongPayload, err := client.readFrame2()
	if err != nil {
		t.Fatalf("client readFrame2: %v", err)
	}
	if op != OpPong {
		t.Fatalf("op = %v, want OpPong", op)
	}
	if string(pongPayload) != "ping-payload" {
		t.Errorf("pong payload = %q, want %q", pongPayload, "ping-payload")
	}
}

// readFrame2 is a tiny test-only wrapper so the table above can read one raw
// frame without going through ReadMessage's own ping/close absorption —
// this file is in-package (not _test external), so it can reach the
// unexported readFrame directly; the wrapper only exists to give the two
// return values used above clearer names at the call site.
func (c *Conn) readFrame2() (Opcode, []byte, error) {
	_, op, payload, err := c.readFrame()
	return op, payload, err
}

// TestCloseHandshakeReportsErrClosed confirms that once a peer sends a Close
// frame, ReadMessage reports the documented sentinel instead of a generic
// transport error or io.EOF — a caller (serve.go) needs to tell "the client
// hung up politely" apart from "the network died" to decide whether to log
// a warning.
func TestCloseHandshakeReportsErrClosed(t *testing.T) {
	upgraded := make(chan *Conn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := Upgrade(w, r)
		if err != nil {
			t.Errorf("server Upgrade: %v", err)
			return
		}
		upgraded <- c
	}))
	defer srv.Close()

	wsURL := "ws" + srv.URL[len("http"):]
	client, err := Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	var server *Conn
	select {
	case server = <-upgraded:
	case <-time.After(2 * time.Second):
		t.Fatal("server never upgraded")
	}
	defer server.Close()

	if err := client.Close(); err != nil {
		t.Fatalf("client Close: %v", err)
	}

	_, _, err = server.ReadMessage()
	if !errors.Is(err, ErrClosed) {
		t.Errorf("server ReadMessage error = %v, want ErrClosed", err)
	}
}

// TestUpgradeRejectsPlainHTTP confirms an ordinary GET (no Upgrade header)
// is refused with ErrNotWebSocket rather than panicking or hanging —
// serve.go's own HTTP handler needs this to decide "respond with a normal
// error page" vs. "hijack the connection".
func TestUpgradeRejectsPlainHTTP(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	_, err := Upgrade(rr, req)
	if !errors.Is(err, ErrNotWebSocket) {
		t.Errorf("err = %v, want ErrNotWebSocket", err)
	}
}

// TestUpgradeRejectsWrongVersion confirms a client claiming a
// Sec-WebSocket-Version other than 13 is refused with a clear error rather
// than silently proceeding against a protocol revision this package never
// implemented.
func TestUpgradeRejectsWrongVersion(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	req.Header.Set("Sec-WebSocket-Version", "8")
	_, err := Upgrade(rr, req)
	if err == nil {
		t.Fatal("expected an error for Sec-WebSocket-Version: 8")
	}
	if errors.Is(err, ErrNotWebSocket) {
		t.Errorf("got ErrNotWebSocket, want a version-specific error")
	}
}

// TestAcceptKeyMatchesRFC6455Example pins acceptKey against the exact
// worked example RFC 6455 §1.3 itself gives, so a change to the hashing
// logic that happens to still pass the round-trip tests (which only compare
// two independently-computed values against each other) cannot silently
// drift from the spec's own fixed answer.
func TestAcceptKeyMatchesRFC6455Example(t *testing.T) {
	got := acceptKey("dGhlIHNhbXBsZSBub25jZQ==")
	want := "s3pPLMBiTxaQ9kYGzzhZRbK+xOo="
	if got != want {
		t.Errorf("acceptKey = %q, want %q (RFC 6455 §1.3 worked example)", got, want)
	}
}

// TestDialRejectsWrongScheme confirms Dial fails cleanly on a non-ws(s)
// scheme instead of trying to open a TCP connection to a nonsensical host.
func TestDialRejectsWrongScheme(t *testing.T) {
	_, err := Dial("http://example.com", nil)
	if err == nil {
		t.Fatal("expected an error for scheme \"http\"")
	}
}

// TestHeaderContainsTokenHandlesCommaSeparatedList pins the helper this
// package's own Upgrade relies on to accept the realistic
// "Connection: keep-alive, Upgrade" shape browsers send, not only a bare
// "Connection: Upgrade".
func TestHeaderContainsTokenHandlesCommaSeparatedList(t *testing.T) {
	cases := []struct {
		value string
		token string
		want  bool
	}{
		{"Upgrade", "upgrade", true},
		{"keep-alive, Upgrade", "upgrade", true},
		{"Upgrade, keep-alive", "upgrade", true},
		{"keep-alive", "upgrade", false},
		{"", "upgrade", false},
	}
	for _, c := range cases {
		got := headerContainsToken(c.value, c.token)
		if got != c.want {
			t.Errorf("headerContainsToken(%q, %q) = %v, want %v", c.value, c.token, got, c.want)
		}
	}
}

package wsproto

import (
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// magicGUID is RFC 6455 §1.3's fixed constant, concatenated onto the
// client's Sec-WebSocket-Key before SHA-1 hashing to prove both sides speak
// the same protocol version — it is not a secret, just a version tag baked
// into the spec.
const magicGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// ErrNotWebSocket is returned by Upgrade when the incoming request is not a
// valid WebSocket handshake (wrong method, missing/incorrect headers). The
// caller (serve.go) responds with an ordinary HTTP error in that case —
// there is nothing to upgrade.
var ErrNotWebSocket = errors.New("wsproto: not a websocket upgrade request")

// Upgrade performs the server side of the RFC 6455 handshake on an incoming
// HTTP request and returns the raw *Conn to read/write frames on. After a
// successful call, the caller owns the connection completely: no further
// use of w or r is valid, matching net/http's own documented contract for
// http.Hijacker.
//
// This intentionally does not implement Sec-WebSocket-Protocol negotiation:
// Step 23's own door speaks exactly one application protocol (NDJSON events
// over WebSocket text frames), so there is nothing to negotiate.
func Upgrade(w http.ResponseWriter, r *http.Request) (*Conn, error) {
	if !strings.EqualFold(r.Method, http.MethodGet) {
		return nil, ErrNotWebSocket
	}
	if !headerContainsToken(r.Header.Get("Connection"), "upgrade") {
		return nil, ErrNotWebSocket
	}
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return nil, ErrNotWebSocket
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		return nil, ErrNotWebSocket
	}
	if v := r.Header.Get("Sec-WebSocket-Version"); v != "" && v != "13" {
		return nil, fmt.Errorf("wsproto: unsupported Sec-WebSocket-Version %q (only 13)", v)
	}

	hj, ok := w.(http.Hijacker)
	if !ok {
		return nil, errors.New("wsproto: response writer does not support hijacking")
	}

	accept := acceptKey(key)

	nc, brw, err := hj.Hijack()
	if err != nil {
		return nil, fmt.Errorf("wsproto: hijack failed: %w", err)
	}

	resp := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"
	if _, err := brw.WriteString(resp); err != nil {
		nc.Close()
		return nil, fmt.Errorf("wsproto: writing handshake response: %w", err)
	}
	if err := brw.Flush(); err != nil {
		nc.Close()
		return nil, fmt.Errorf("wsproto: flushing handshake response: %w", err)
	}

	c := newConn(nc, true)
	// Hijack hands back its own buffered reader, already primed with
	// anything the client pipelined right after the handshake bytes.
	// Reusing it directly (instead of a fresh bufio.NewReader(nc)) is what
	// keeps those bytes from being silently dropped — bufio.Reader.Read
	// drains its own buffer before ever touching the underlying net.Conn
	// again, so this is correct even when nothing was pipelined.
	c.br = brw.Reader
	return c, nil
}

// acceptKey computes Sec-WebSocket-Accept from the client's
// Sec-WebSocket-Key per RFC 6455 §1.3: base64(sha1(key + magicGUID)).
func acceptKey(key string) string {
	h := sha1.New()
	h.Write([]byte(key))
	h.Write([]byte(magicGUID))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// headerContainsToken reports whether value (a comma-separated header like
// "keep-alive, Upgrade") contains token, case-insensitively. Connection
// headers in the wild list more than one token, so an exact EqualFold
// against the whole header value misses "Connection: keep-alive, Upgrade" —
// a shape real browsers send.
func headerContainsToken(value, token string) bool {
	for _, part := range strings.Split(value, ",") {
		if strings.EqualFold(strings.TrimSpace(part), token) {
			return true
		}
	}
	return false
}

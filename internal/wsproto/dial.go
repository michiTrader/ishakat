package wsproto

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// Dial opens a client-side WebSocket connection to a ws:// or wss:// URL.
// It exists so this package's own tests (and any future --serve client this
// project ships) can drive the real handshake end to end against a real
// net.Listener, the same "no mocks on the wire" discipline
// toolchain_e2e_test.go already established for the tool-calling loop —
// wsproto_test.go's own round-trip test is what actually exercises this,
// not serve.go, since Step 23's own client is external (n8n, a voice
// model's own WebSocket library, an editor plugin), never this binary
// dialing itself in production.
//
// header carries any additional request headers (e.g. Authorization for
// serve.go's own bearer-token check); it may be nil.
func Dial(rawURL string, header http.Header) (*Conn, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("wsproto: invalid URL: %w", err)
	}
	var network string
	switch u.Scheme {
	case "ws":
		network = "tcp"
		if u.Port() == "" {
			u.Host += ":80"
		}
	case "wss":
		return nil, fmt.Errorf("wsproto: wss:// (TLS) is not implemented; dial a ws:// endpoint behind your own TLS terminator")
	default:
		return nil, fmt.Errorf("wsproto: unsupported scheme %q (use ws://)", u.Scheme)
	}

	nc, err := net.Dial(network, u.Host)
	if err != nil {
		return nil, err
	}

	key, err := randomKey()
	if err != nil {
		nc.Close()
		return nil, err
	}

	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	if u.RawQuery != "" {
		path += "?" + u.RawQuery
	}

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "GET %s HTTP/1.1\r\n", path)
	fmt.Fprintf(&buf, "Host: %s\r\n", u.Host)
	buf.WriteString("Upgrade: websocket\r\n")
	buf.WriteString("Connection: Upgrade\r\n")
	fmt.Fprintf(&buf, "Sec-WebSocket-Key: %s\r\n", key)
	buf.WriteString("Sec-WebSocket-Version: 13\r\n")
	for name, values := range header {
		for _, v := range values {
			fmt.Fprintf(&buf, "%s: %s\r\n", name, v)
		}
	}
	buf.WriteString("\r\n")

	if _, err := nc.Write(buf.Bytes()); err != nil {
		nc.Close()
		return nil, err
	}

	br := bufio.NewReader(nc)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("wsproto: reading handshake response: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusSwitchingProtocols {
		nc.Close()
		return nil, fmt.Errorf("wsproto: handshake failed: HTTP %d", resp.StatusCode)
	}
	if !strings.EqualFold(resp.Header.Get("Upgrade"), "websocket") {
		nc.Close()
		return nil, fmt.Errorf("wsproto: handshake response missing Upgrade: websocket")
	}
	want := acceptKey(key)
	got := resp.Header.Get("Sec-WebSocket-Accept")
	if got != want {
		nc.Close()
		return nil, fmt.Errorf("wsproto: Sec-WebSocket-Accept mismatch (server may not speak RFC 6455)")
	}

	c := newConn(nc, false)
	c.br = br
	return c, nil
}

// randomKey generates the 16-byte, base64-encoded Sec-WebSocket-Key RFC 6455
// §1.3 requires the client to send.
func randomKey() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("wsproto: could not generate handshake key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(b[:]), nil
}

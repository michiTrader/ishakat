// Package wsproto is a minimal RFC 6455 WebSocket implementation, stdlib
// only (net, net/http, bufio, crypto/sha1, crypto/rand, encoding/base64,
// encoding/binary). It exists because Step 23 (docs/PLAN.md §11/§13) needs a
// WebSocket door for internal/app/serve.go, and §6.4's rule for the whole of
// Phase 2.5 is zero new dependencies — every capability the agent layer adds
// is built from what the standard library already offers. A hand-rolled
// codec is a few hundred lines for the subset this project actually needs
// (text frames carrying JSON, control frames handled automatically); a
// general-purpose WebSocket library is easily ten times that once it also
// covers compression extensions, subprotocol negotiation and binary framing
// this project never uses.
//
// What is deliberately NOT implemented, because Step 23's own door only
// needs unidirectional-at-a-time JSON messages, not a general transport:
// permessage-deflate (RFC 7692) compression, subprotocol negotiation, and
// binary frames (opcode 0x2) are accepted on the wire but never produced by
// this package's own Dial/Upgrade helpers. A message larger than
// MaxMessageSize closes the connection rather than growing an unbounded
// buffer — the same "cap it, do not trust the wire's own length claim" rule
// convo.Store.load already applies to a session file's line buffer.
package wsproto

import (
	"bufio"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
)

// Opcode identifies a WebSocket frame's payload type (RFC 6455 §5.2).
type Opcode byte

const (
	OpContinuation Opcode = 0x0
	OpText         Opcode = 0x1
	OpBinary       Opcode = 0x2
	OpClose        Opcode = 0x8
	OpPing         Opcode = 0x9
	OpPong         Opcode = 0xA
)

func (o Opcode) isControl() bool { return o >= OpClose }

// MaxMessageSize bounds one assembled message (after reassembling any
// fragmented frames). 4 MiB is generous for the JSON events this package
// carries — a turn's prompt or a tool's already-truncated 32 KiB output —
// while still refusing to let a malicious or buggy peer grow an unbounded
// buffer, mirroring buildPrompt's own 8 MiB stdin cap in internal/app
// (headless.go) for the identical reason.
const MaxMessageSize = 4 << 20

// ErrClosed is returned by ReadMessage once a Close frame has been read (or
// sent). It is intentionally distinct from io.EOF: an abrupt TCP close is a
// transport error, a Close frame is the protocol's own, orderly goodbye.
var ErrClosed = errors.New("wsproto: connection closed")

// ErrMessageTooLarge is returned by ReadMessage when an assembled message
// would exceed MaxMessageSize.
var ErrMessageTooLarge = errors.New("wsproto: message exceeds MaxMessageSize")

// Conn is one open WebSocket connection, server- or client-side. It is safe
// for one reader and one writer to use concurrently (matching net.Conn's own
// contract), but not for concurrent writers among themselves, nor concurrent
// readers among themselves — Step 23's own call sites never need that: each
// connection is driven by exactly one read loop and, in reply to it, one
// writer at a time.
type Conn struct {
	nc     net.Conn
	br     *bufio.Reader
	bw     *bufio.Writer
	server bool // server frames are never masked; client frames always are

	// closed is an atomic.Bool, not a plain bool, because Close() can run
	// concurrently with ReadMessage() on the same *Conn: serve.go's own
	// closeAll() calls Close() from the shutdown goroutine while a
	// session's own read loop is blocked inside ReadMessage() on another
	// goroutine, and ReadMessage() itself sets this field on the
	// close-frame path. Two unsynchronized writes to a plain bool from
	// different goroutines is a data race go test -race catches
	// immediately once both call sites are ever exercised in the same
	// process, which serve_test.go's shutdown path (Step 23) is the
	// first test in this repo to actually do.
	closed atomic.Bool

	// writeMu serializes writeFrame calls. This package's own doc comment
	// above documents "not safe for concurrent writers among themselves"
	// as a deliberate simplification for Step 23's own call sites -- but
	// Close() is the one write path that does NOT honor that rule by
	// construction: serve.go's closeAll() (shutdown) and the connection's
	// own read loop (echoing a peer's Close frame, or a ping's pong) can
	// both reach writeFrame concurrently on the same *Conn, independent
	// of whatever discipline a caller one level up (serveSession.writeMu
	// in internal/app/serve.go) applies to its own sendEvent calls. This
	// mutex is scoped to protect exactly that one accidental overlap, not
	// to promise this package supports arbitrary concurrent writers.
	writeMu sync.Mutex
}

// newConn wraps an already-upgraded net.Conn. server selects the masking
// rule this side of the connection must follow when writing (RFC 6455
// §5.1: "a client MUST mask all frames... a server MUST NOT mask").
func newConn(nc net.Conn, server bool) *Conn {
	return &Conn{nc: nc, br: bufio.NewReaderSize(nc, 4096), bw: bufio.NewWriterSize(nc, 4096), server: server}
}

// Close sends a Close frame (best-effort — a write failure here is not
// reported, since the connection is going away regardless) and closes the
// underlying net.Conn.
func (c *Conn) Close() error {
	if c.closed.CompareAndSwap(false, true) {
		_ = c.writeFrame(OpClose, closePayload(1000, ""))
	}
	return c.nc.Close()
}

// LocalAddr and RemoteAddr expose the underlying connection's addresses,
// mirroring net.Conn — useful for a doctor-style diagnostic later without
// this package needing to grow its own address type.
func (c *Conn) LocalAddr() net.Addr  { return c.nc.LocalAddr() }
func (c *Conn) RemoteAddr() net.Addr { return c.nc.RemoteAddr() }

// WriteMessage sends one unfragmented data frame (text or binary). It is the
// only send path this package's own callers use; control frames (pong,
// close) are written internally by readFrame/Close.
func (c *Conn) WriteMessage(op Opcode, payload []byte) error {
	if op != OpText && op != OpBinary {
		return fmt.Errorf("wsproto: WriteMessage only sends OpText or OpBinary, got %v", op)
	}
	return c.writeFrame(op, payload)
}

// ReadMessage returns the next complete data message, transparently
// answering any ping frames with a pong and reassembling any fragmented
// message (a FIN=0 frame followed by OpContinuation frames) before
// returning. A Close frame received from the peer echoes a Close frame back
// and returns ErrClosed.
func (c *Conn) ReadMessage() (Opcode, []byte, error) {
	var assembled []byte
	var msgOp Opcode

	for {
		fin, op, payload, err := c.readFrame()
		if err != nil {
			return 0, nil, err
		}

		switch op {
		case OpPing:
			if err := c.writeFrame(OpPong, payload); err != nil {
				return 0, nil, err
			}
			continue
		case OpPong:
			continue // nothing sends unsolicited pings from this side yet
		case OpClose:
			// Echo the close and report it as the sentinel error. RFC 6455
			// §5.5.1 asks for the same status code back when there is one;
			// closePayload(1000, "") is close enough for a peer that will
			// tear the socket down either way.
			_ = c.writeFrame(OpClose, closePayload(1000, ""))
			c.closed.Store(true)
			return 0, nil, ErrClosed
		}

		if len(assembled) == 0 && op != OpContinuation {
			msgOp = op
		}
		if op == OpContinuation && msgOp == 0 {
			return 0, nil, errors.New("wsproto: continuation frame with no preceding data frame")
		}

		if len(assembled)+len(payload) > MaxMessageSize {
			return 0, nil, ErrMessageTooLarge
		}
		assembled = append(assembled, payload...)

		if fin {
			return msgOp, assembled, nil
		}
	}
}

// closePayload encodes a Close frame body: a 2-byte big-endian status code
// followed by an optional UTF-8 reason (RFC 6455 §5.5.1).
func closePayload(code int, reason string) []byte {
	b := make([]byte, 2+len(reason))
	binary.BigEndian.PutUint16(b, uint16(code))
	copy(b[2:], reason)
	return b
}

// writeFrame sends one complete (FIN=1), unfragmented frame. This package
// never fragments its own writes — every message this project ever sends is
// well under MaxMessageSize, so there is no reason to pay for the
// complexity of a segmented sender.
func (c *Conn) writeFrame(op Opcode, payload []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	var header [14]byte
	header[0] = 0x80 | byte(op) // FIN=1, RSV=0, opcode

	maskBit := byte(0)
	if !c.server {
		maskBit = 0x80
	}

	n := len(payload)
	var headerLen int
	switch {
	case n <= 125:
		header[1] = maskBit | byte(n)
		headerLen = 2
	case n <= 0xFFFF:
		header[1] = maskBit | 126
		binary.BigEndian.PutUint16(header[2:4], uint16(n))
		headerLen = 4
	default:
		header[1] = maskBit | 127
		binary.BigEndian.PutUint64(header[2:10], uint64(n))
		headerLen = 10
	}

	if _, err := c.bw.Write(header[:headerLen]); err != nil {
		return err
	}

	if !c.server {
		var key [4]byte
		if _, err := rand.Read(key[:]); err != nil {
			return fmt.Errorf("wsproto: could not generate mask key: %w", err)
		}
		if _, err := c.bw.Write(key[:]); err != nil {
			return err
		}
		masked := make([]byte, n)
		for i := range payload {
			masked[i] = payload[i] ^ key[i%4]
		}
		if _, err := c.bw.Write(masked); err != nil {
			return err
		}
	} else if n > 0 {
		if _, err := c.bw.Write(payload); err != nil {
			return err
		}
	}

	return c.bw.Flush()
}

// readFrame reads exactly one frame off the wire. server, mirroring
// writeFrame, is used the other way around here: RFC 6455 §5.1 requires a
// server to reject an unmasked frame from a client, and a client to reject a
// masked frame from a server — both are protocol violations, not merely odd
// input, since a masked server frame usually means a proxy or middlebox is
// corrupting the stream.
func (c *Conn) readFrame() (fin bool, op Opcode, payload []byte, err error) {
	var header [2]byte
	if _, err := io.ReadFull(c.br, header[:]); err != nil {
		return false, 0, nil, err
	}
	fin = header[0]&0x80 != 0
	op = Opcode(header[0] & 0x0F)
	masked := header[1]&0x80 != 0
	length := uint64(header[1] & 0x7F)

	// c.server is true when this Conn is the server side of the exchange —
	// see newConn's own doc comment — so a server here expects masked
	// client frames, and a client expects unmasked server frames.
	if masked != c.server {
		return false, 0, nil, fmt.Errorf(
			"wsproto: protocol violation: expected masked=%v frame, got masked=%v", c.server, masked)
	}

	switch length {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(c.br, ext[:]); err != nil {
			return false, 0, nil, err
		}
		length = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(c.br, ext[:]); err != nil {
			return false, 0, nil, err
		}
		length = binary.BigEndian.Uint64(ext[:])
	}
	if length > MaxMessageSize {
		return false, 0, nil, ErrMessageTooLarge
	}

	var maskKey [4]byte
	if masked {
		if _, err := io.ReadFull(c.br, maskKey[:]); err != nil {
			return false, 0, nil, err
		}
	}

	payload = make([]byte, length)
	if _, err := io.ReadFull(c.br, payload); err != nil {
		return false, 0, nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= maskKey[i%4]
		}
	}

	if op.isControl() && !fin {
		return false, 0, nil, errors.New("wsproto: control frames must not be fragmented")
	}
	if op.isControl() && len(payload) > 125 {
		return false, 0, nil, errors.New("wsproto: control frame payload exceeds 125 bytes")
	}

	return fin, op, payload, nil
}

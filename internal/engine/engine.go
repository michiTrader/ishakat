package engine

import (
	"context"
	"time"
)

// Engine runs one turn at a time against a Streamer, feeding a StreamBuf
// that internal/tui drains on its own clock (§7.3/§7.4). It holds no
// provider-specific knowledge — that's the Streamer closure internal/app
// builds — which is what lets this package's tests run with a fake
// Streamer and no network at all.
type Engine struct {
	stream     Streamer
	maxRetries int
}

// New builds an Engine. maxRetries mirrors config's App.MaxRetries (§5.4):
// how many handshake failures retryAfter is allowed to consider before
// giving up.
func New(stream Streamer, maxRetries int) *Engine {
	return &Engine{stream: stream, maxRetries: maxRetries}
}

// Start begins one turn and returns immediately: the turn runs in its own
// goroutine, writing into buf, until it finishes or ctx is cancelled. The
// caller (internal/tui's Root) owns ctx's CancelFunc — calling it is the
// entire implementation of esc/ctrl+c per §7.4.
func (e *Engine) Start(ctx context.Context, req Request, buf *StreamBuf) {
	go e.run(ctx, req, buf)
}

// run is Start's goroutine body. Split out so tests can call it directly
// and block on its return instead of polling Drain() from a second
// goroutine.
func (e *Engine) run(ctx context.Context, req Request, buf *StreamBuf) {
	var ch <-chan Event
	for attempt := 0; ; attempt++ {
		var err error
		ch, err = e.stream(ctx, req)
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			buf.finish(nil, true)
			return
		}

		wait, retry := retryAfter(err, attempt, e.maxRetries)
		if !retry {
			buf.finish(err, false)
			return
		}
		select {
		case <-time.After(wait):
			continue
		case <-ctx.Done():
			buf.finish(nil, true)
			return
		}
	}

	var turnErr error
	for ev := range ch {
		switch ev.Kind {
		case EventDelta:
			buf.push(ev.Text)
		case EventReasoning:
			buf.pushReasoning(ev.Text)
		case EventUsage:
			buf.setUsage(ev.Usage)
		case EventWarning:
			// §7.4 doesn't route warnings through StreamBuf: they're
			// informational (a degraded capability, §4.6) rather than part
			// of the turn's outcome, and Root will surface them directly
			// from the Streamer's Event as they arrive via a dedicated
			// path once that wiring lands. For now, dropping them here
			// keeps this package's contract to exactly what §7.3's example
			// drains: text, usage, done, err.
		case EventError:
			// EventDone always follows (provider.Event's contract, §5.4):
			// the loop keeps draining instead of breaking so the
			// Streamer's goroutine is guaranteed to see the channel fully
			// read and can close cleanly.
			if turnErr == nil {
				turnErr = ev.Err
			}
			if ev.Usage != nil {
				buf.setUsage(ev.Usage)
			}
		case EventDone:
			if ev.Usage != nil {
				buf.setUsage(ev.Usage)
			}
		}
	}

	if ctx.Err() != nil {
		// Cancellation wins over any error that happened to be in flight
		// when the context died: the user hit esc, that's not a failure.
		buf.finish(nil, true)
		return
	}
	buf.finish(turnErr, false)
}

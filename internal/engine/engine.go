package engine

import (
	"context"
	"strings"
	"time"

	"github.com/MichiTrader/ishakat/internal/convo"
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
	ch, err := e.open(ctx, req)
	if err != nil {
		if ctx.Err() != nil {
			buf.finish(nil, true)
		} else {
			buf.finish(err, false)
		}
		return
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

// open runs the handshake-retry loop shared by run and RunToCompletion:
// call the Streamer, and if it fails with a hinted retryable error (per
// retryAfter), wait and try again until either it succeeds, the error turns
// out not to be retryable, maxRetries is exhausted, or ctx is cancelled.
// Pulled out of run's body (Step 12) so RunToCompletion — a call to
// compact_model that has no StreamBuf to write into — gets the exact same
// backoff/jitter policy instead of a second copy of it.
func (e *Engine) open(ctx context.Context, req Request) (<-chan Event, error) {
	for attempt := 0; ; attempt++ {
		ch, err := e.stream(ctx, req)
		if err == nil {
			return ch, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		wait, retry := retryAfter(err, attempt, e.maxRetries)
		if !retry {
			return nil, err
		}
		select {
		case <-time.After(wait):
			continue
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// Answer is the outcome of RunToCompletion: everything a caller that needs
// one finished piece of text — rather than a live-typed one — cares about.
type Answer struct {
	Text  string
	Usage *convo.Usage
}

// RunToCompletion runs req against the same Streamer and retry policy as
// Start, but blocks until the turn finishes and returns the whole answer
// instead of writing into a StreamBuf. It exists for callers that need a
// model's answer as a value, not as something to type onto a screen —
// Step 12's compaction summary (internal/engine/compact.go) and, later,
// autoname's session-title call. Reasoning deltas are dropped: nothing
// that calls this wants the model's scratch space, only its final text.
func (e *Engine) RunToCompletion(ctx context.Context, req Request) (Answer, error) {
	ch, err := e.open(ctx, req)
	if err != nil {
		return Answer{}, err
	}

	var b strings.Builder
	var usage *convo.Usage
	var turnErr error
	for ev := range ch {
		switch ev.Kind {
		case EventDelta:
			b.WriteString(ev.Text)
		case EventUsage:
			usage = ev.Usage
		case EventError:
			if turnErr == nil {
				turnErr = ev.Err
			}
			if ev.Usage != nil {
				usage = ev.Usage
			}
		case EventDone:
			if ev.Usage != nil {
				usage = ev.Usage
			}
		}
	}

	ans := Answer{Text: b.String(), Usage: usage}
	if ctx.Err() != nil {
		return ans, ctx.Err()
	}
	if turnErr != nil {
		return ans, turnErr
	}
	return ans, nil
}

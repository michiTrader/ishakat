package engine

import (
	"strings"
	"sync"

	"github.com/MichiTrader/ishakat/internal/convo"
)

// StreamBuf decouples the arrival rate of tokens from the repaint rate
// (§7.3): a Streamer's goroutine calls push as fast as the network hands it
// deltas, and internal/tui's streamTickMsg calls Drain on its own clock (20
// times a second, or slower on battery saver). Without this, a naive
// Cmd-reads-a-channel-and-re-emits loop means one full Update+View per
// token, which is 80-150 repaints a second and is exactly why AI TUIs feel
// slow on a phone.
type StreamBuf struct {
	mu        sync.Mutex
	text      strings.Builder
	reasoning strings.Builder
	usage     *convo.Usage
	done      bool
	aborted   bool
	err       error
}

// push appends a delta. Called only from the Streamer's goroutine (engine.go).
func (s *StreamBuf) push(delta string) {
	s.mu.Lock()
	s.text.WriteString(delta)
	s.mu.Unlock()
}

// pushReasoning is push's sibling for EventReasoning: kept in a separate
// builder because convo.Message distinguishes BlockReasoning from BlockText
// (§4), and coalescing them here would force the TUI to re-split them later.
func (s *StreamBuf) pushReasoning(delta string) {
	s.mu.Lock()
	s.reasoning.WriteString(delta)
	s.mu.Unlock()
}

// setUsage records the running total. Usage can arrive mid-stream
// (EventUsage) and again, definitively, on EventDone — either call
// overwrites, since both carry the provider's running total, not a delta.
func (s *StreamBuf) setUsage(u *convo.Usage) {
	s.mu.Lock()
	s.usage = u
	s.mu.Unlock()
}

// finish marks the turn over. aborted distinguishes a cancellation (§7.4:
// esc/ctrl+c closed the context, nothing went wrong) from err (the provider
// or the transport actually failed) — finishTurn on the TUI side needs that
// distinction to decide whether the assistant message gets Aborted: true or
// an error banner.
func (s *StreamBuf) finish(err error, aborted bool) {
	s.mu.Lock()
	s.done = true
	s.err = err
	s.aborted = aborted
	s.mu.Unlock()
}

// Drain empties the buffer and reports the turn's state. Safe to call from
// the Bubble Tea Update goroutine while push/setUsage/finish run
// concurrently from the Streamer's goroutine — that's StreamBuf's entire
// reason to exist.
func (s *StreamBuf) Drain() (chunk, reasoningChunk string, usage *convo.Usage, done, aborted bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	chunk = s.text.String()
	s.text.Reset()
	reasoningChunk = s.reasoning.String()
	s.reasoning.Reset()
	return chunk, reasoningChunk, s.usage, s.done, s.aborted, s.err
}

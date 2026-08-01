package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MichiTrader/ishakat/internal/convo"
)

// drainUntilDone polls Drain in a tight loop with a timeout, the way the TUI
// would via repeated streamTickMsg but without needing Bubble Tea's runtime
// in these tests.
func drainUntilDone(t *testing.T, buf *StreamBuf, budget time.Duration) (text, reasoning string, usage *convo.Usage, aborted bool, err error) {
	t.Helper()
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		chunk, rChunk, u, done, isAborted, e := buf.Drain()
		text += chunk
		reasoning += rChunk
		if u != nil {
			usage = u
		}
		if done {
			return text, reasoning, usage, isAborted, e
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("turn never finished within the budget: the engine is stuck")
	return "", "", nil, false, nil
}

func chanOf(events ...Event) <-chan Event {
	ch := make(chan Event, len(events))
	for _, ev := range events {
		ch <- ev
	}
	close(ch)
	return ch
}

func TestEngineDeliversDeltaReasoningAndUsage(t *testing.T) {
	stream := func(ctx context.Context, req Request) (<-chan Event, error) {
		return chanOf(
			Event{Kind: EventReasoning, Text: "thinking... "},
			Event{Kind: EventDelta, Text: "hel"},
			Event{Kind: EventDelta, Text: "lo"},
			Event{Kind: EventUsage, Usage: &convo.Usage{In: 10, Out: 2}},
			Event{Kind: EventDone},
		), nil
	}

	e := New(stream, 3)
	var buf StreamBuf
	e.Start(context.Background(), Request{Model: "m"}, &buf)

	text, reasoning, usage, aborted, err := drainUntilDone(t, &buf, time.Second)
	if text != "hello" {
		t.Errorf("text = %q, want %q", text, "hello")
	}
	if reasoning != "thinking... " {
		t.Errorf("reasoning = %q, want %q", reasoning, "thinking... ")
	}
	if usage == nil || usage.In != 10 || usage.Out != 2 {
		t.Errorf("usage = %+v, want {In:10 Out:2}", usage)
	}
	if aborted || err != nil {
		t.Errorf("aborted=%v err=%v, want false/nil", aborted, err)
	}
}

func TestEngineForwardsTheRequestUnchanged(t *testing.T) {
	want := Request{
		Model:    "anthropic/claude-sonnet-4-5",
		System:   "be terse",
		Messages: []convo.Message{convo.User("hi")},
	}

	var got Request
	stream := func(ctx context.Context, req Request) (<-chan Event, error) {
		got = req
		return chanOf(Event{Kind: EventDone}), nil
	}

	e := New(stream, 0)
	var buf StreamBuf
	e.Start(context.Background(), want, &buf)
	drainUntilDone(t, &buf, time.Second)

	if got.Model != want.Model || got.System != want.System {
		t.Errorf("Streamer received Model=%q System=%q, want Model=%q System=%q",
			got.Model, got.System, want.Model, want.System)
	}
	if len(got.Messages) != 1 || got.Messages[0].Text() != "hi" {
		t.Errorf("Streamer received Messages=%v, want a single 'hi' user message", got.Messages)
	}
}

func TestEngineRetriesAHandshakeFailureThenSucceeds(t *testing.T) {
	var calls int
	stream := func(ctx context.Context, req Request) (<-chan Event, error) {
		calls++
		if calls == 1 {
			return nil, fakeRetryable{wait: 5 * time.Millisecond, retryable: true}
		}
		return chanOf(Event{Kind: EventDelta, Text: "ok"}, Event{Kind: EventDone}), nil
	}

	e := New(stream, 3)
	var buf StreamBuf
	e.Start(context.Background(), Request{Model: "m"}, &buf)

	text, _, _, aborted, err := drainUntilDone(t, &buf, time.Second)
	if calls != 2 {
		t.Errorf("stream called %d times, want exactly 2 (1 failure + 1 success)", calls)
	}
	if text != "ok" || aborted || err != nil {
		t.Errorf("text=%q aborted=%v err=%v, want %q/false/nil", text, aborted, err, "ok")
	}
}

func TestEngineGivesUpAfterMaxRetries(t *testing.T) {
	var calls int
	failure := fakeRetryable{wait: 1 * time.Millisecond, retryable: true}
	stream := func(ctx context.Context, req Request) (<-chan Event, error) {
		calls++
		return nil, failure
	}

	e := New(stream, 2)
	var buf StreamBuf
	e.Start(context.Background(), Request{Model: "m"}, &buf)

	_, _, _, aborted, err := drainUntilDone(t, &buf, time.Second)
	// maxRetries=2 means attempts 0,1,2 are tried (3 calls: the original
	// plus 2 retries), and the 3rd failure (attempt==maxRetries) stops.
	if calls != 3 {
		t.Errorf("stream called %d times, want 3 (initial + 2 retries)", calls)
	}
	if aborted {
		t.Error("aborted = true, want false: this is exhaustion, not cancellation")
	}
	if !errors.Is(err, error(failure)) && err != failure {
		t.Errorf("err = %v, want the last handshake failure", err)
	}
}

func TestEngineNeverRetriesAMidStreamError(t *testing.T) {
	var calls int
	stream := func(ctx context.Context, req Request) (<-chan Event, error) {
		calls++
		return chanOf(
			Event{Kind: EventDelta, Text: "partial"},
			Event{Kind: EventError, Err: fakeRetryable{wait: time.Millisecond, retryable: true}},
			Event{Kind: EventDone},
		), nil
	}

	e := New(stream, 5)
	var buf StreamBuf
	e.Start(context.Background(), Request{Model: "m"}, &buf)

	text, _, _, aborted, err := drainUntilDone(t, &buf, time.Second)
	if calls != 1 {
		t.Errorf("stream called %d times, want exactly 1: a mid-stream error must never resend the turn", calls)
	}
	if text != "partial" {
		t.Errorf("text = %q, want the partial text delivered before the error", text)
	}
	if aborted {
		t.Error("aborted = true, want false: this was a stream error, not a cancellation")
	}
	if err == nil {
		t.Error("err = nil, want the mid-stream failure to surface")
	}
}

func TestEngineCancelDuringHandshakeBackoffWait(t *testing.T) {
	stream := func(ctx context.Context, req Request) (<-chan Event, error) {
		return nil, fakeRetryable{wait: time.Hour, retryable: true}
	}

	e := New(stream, 5)
	var buf StreamBuf
	ctx, cancel := context.WithCancel(context.Background())
	e.Start(ctx, Request{Model: "m"}, &buf)

	time.Sleep(10 * time.Millisecond) // let it enter the backoff wait
	cancel()

	_, _, _, aborted, err := drainUntilDone(t, &buf, time.Second)
	if !aborted || err != nil {
		t.Errorf("cancelling during a backoff wait must finish aborted with no error; got aborted=%v err=%v", aborted, err)
	}
}

func TestEngineCancelMidStream(t *testing.T) {
	release := make(chan struct{})
	stream := func(ctx context.Context, req Request) (<-chan Event, error) {
		ch := make(chan Event)
		go func() {
			defer close(ch)
			ch <- Event{Kind: EventDelta, Text: "he"}
			<-release // hold the stream open until the test cancels
			ch <- Event{Kind: EventDelta, Text: "llo"}
			ch <- Event{Kind: EventDone}
		}()
		return ch, nil
	}

	e := New(stream, 3)
	var buf StreamBuf
	ctx, cancel := context.WithCancel(context.Background())
	e.Start(ctx, Request{Model: "m"}, &buf)

	time.Sleep(10 * time.Millisecond) // let "he" land
	cancel()
	close(release) // unblock the goroutine so the channel closes and run() returns

	_, _, _, aborted, err := drainUntilDone(t, &buf, time.Second)
	if !aborted || err != nil {
		t.Errorf("a cancelled mid-stream turn must finish aborted with no error; got aborted=%v err=%v", aborted, err)
	}
}

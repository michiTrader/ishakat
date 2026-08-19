package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MichiTrader/ishakat/internal/convo"
	"github.com/MichiTrader/ishakat/internal/engine"
	"github.com/MichiTrader/ishakat/internal/provider"
	"github.com/MichiTrader/ishakat/internal/provider/fake"
)

func TestStreamerDeliversDeltaReasoningUsageAndDone(t *testing.T) {
	fp := fake.New("t",
		provider.Event{Kind: provider.EventReasoning, Text: "hmm "},
		provider.Event{Kind: provider.EventDelta, Text: "hel"},
		provider.Event{Kind: provider.EventDelta, Text: "lo"},
		fake.Usage(10, 2),
	)

	streamer := NewStreamer(fp, provider.Caps{}, false)
	ch, err := streamer(context.Background(), engine.Request{Model: "m"})
	if err != nil {
		t.Fatalf("streamer returned an error on handshake: %v", err)
	}

	var text, reasoning string
	var usage *convo.Usage
	var sawDone bool
	for ev := range ch {
		switch ev.Kind {
		case engine.EventDelta:
			text += ev.Text
		case engine.EventReasoning:
			reasoning += ev.Text
		case engine.EventUsage:
			usage = ev.Usage
		case engine.EventDone:
			sawDone = true
		}
	}

	if text != "hello" {
		t.Errorf("text = %q, want %q", text, "hello")
	}
	if reasoning != "hmm " {
		t.Errorf("reasoning = %q, want %q", reasoning, "hmm ")
	}
	if usage == nil || usage.In != 10 || usage.Out != 2 {
		t.Errorf("usage = %+v, want {In:10 Out:2}", usage)
	}
	if !sawDone {
		t.Error("the translated channel never delivered EventDone")
	}
}

func TestStreamerForwardsModelMessagesAndSystem(t *testing.T) {
	fp := fake.Text("t", "ok")
	streamer := NewStreamer(fp, provider.Caps{}, false)

	req := engine.Request{
		Model:    "anthropic/claude-sonnet-4-5",
		System:   "be terse",
		Messages: []convo.Message{convo.User("hi")},
	}
	ch, err := streamer(context.Background(), req)
	if err != nil {
		t.Fatalf("streamer returned an error: %v", err)
	}
	for range ch {
		// drain to completion so LastTurn is populated
	}

	last := fp.LastTurn()
	if last.Model != req.Model {
		t.Errorf("provider.Request.Model = %q, want %q", last.Model, req.Model)
	}
	if last.System != req.System {
		t.Errorf("provider.Request.System = %q, want %q", last.System, req.System)
	}
	if len(last.Messages) != 1 || last.Messages[0].Text() != "hi" {
		t.Errorf("provider.Request.Messages = %v, want a single 'hi' user message", last.Messages)
	}
	if !last.Stream {
		t.Error("provider.Request.Stream = false, want true: the TUI always streams")
	}
}

func TestStreamerPropagatesAHandshakeError(t *testing.T) {
	handshakeErr := &provider.Error{Retryable: true, RetryAfter: time.Second}
	fp := &fake.Provider{HandshakeErr: handshakeErr}

	streamer := NewStreamer(fp, provider.Caps{}, false)
	_, err := streamer(context.Background(), engine.Request{Model: "m"})
	if err == nil {
		t.Fatal("streamer returned no error for a handshake failure")
	}

	var pe *provider.Error
	if !errors.As(err, &pe) || pe != handshakeErr {
		t.Errorf("err = %v, want the exact *provider.Error the fake returned", err)
	}
	// This is the property retryAfter (internal/engine) actually depends
	// on: the error engine.Streamer returns must still satisfy engine's
	// unexported retryHint via errors.As. Retry() is public, so this test
	// can call it directly instead of duplicating the interface.
	if wait, retryable := pe.Retry(); wait != time.Second || !retryable {
		t.Errorf("pe.Retry() = (%v, %v), want (1s, true)", wait, retryable)
	}
}

func TestStreamerPropagatesAMidStreamError(t *testing.T) {
	streamErr := errors.New("boom mid-stream")
	fp := fake.New("t",
		provider.Event{Kind: provider.EventDelta, Text: "partial"},
		provider.Event{Kind: provider.EventError, Err: streamErr},
	)

	streamer := NewStreamer(fp, provider.Caps{}, false)
	ch, err := streamer(context.Background(), engine.Request{Model: "m"})
	if err != nil {
		t.Fatalf("streamer returned an error on handshake: %v", err)
	}

	var gotErr error
	var sawDone bool
	for ev := range ch {
		if ev.Kind == engine.EventError {
			gotErr = ev.Err
		}
		if ev.Kind == engine.EventDone {
			sawDone = true
		}
	}
	if gotErr != streamErr {
		t.Errorf("gotErr = %v, want %v", gotErr, streamErr)
	}
	if !sawDone {
		t.Error("EventDone must still follow EventError, per the channel's own contract")
	}
}

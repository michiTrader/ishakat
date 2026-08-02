package tui

import (
	"context"
	"time"

	"github.com/MichiTrader/ishakat/internal/engine"
)

const echoChunkSize = 3

// echoEngine returns a deterministic engine double for TUI tests. The gated
// form releases one response chunk at a time, which lets tests inspect the
// live frame between provider events without depending on scheduler timing.
func echoEngine(gated bool) (*engine.Engine, func()) {
	release := make(chan struct{})
	pushed := make(chan struct{})

	stream := func(ctx context.Context, req engine.Request) (<-chan engine.Event, error) {
		out := make(chan engine.Event)
		text := ""
		if len(req.Messages) > 0 {
			text = req.Messages[len(req.Messages)-1].Text()
		}
		go func() {
			defer close(out)
			for len(text) > 0 {
				n := echoChunkSize
				if len(text) < n {
					n = len(text)
				}
				chunk := text[:n]
				text = text[n:]
				if gated {
					select {
					case <-release:
					case <-ctx.Done():
						return
					}
				}
				select {
				case out <- engine.Event{Kind: engine.EventDelta, Text: chunk}:
				case <-ctx.Done():
					return
				}
				if gated {
					pushed <- struct{}{}
				}
			}
			out <- engine.Event{Kind: engine.EventDone}
		}()
		return out, nil
	}

	advance := func() {
		if !gated {
			return
		}
		release <- struct{}{}
		<-pushed
		// engine.run receives the event before the streamer can signal pushed,
		// but this small margin also lets StreamBuf.push complete before the
		// caller drains it.
		time.Sleep(time.Millisecond)
	}
	return engine.New(stream, 0), advance
}

func withEngine(root Root, eng *engine.Engine) Root {
	root.eng = eng
	return root
}

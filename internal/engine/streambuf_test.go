package engine

import (
	"sync"
	"testing"
)

type errUnitTest string

func (e errUnitTest) Error() string { return string(e) }

func TestStreamBufDrainCoalescesPushesAndResets(t *testing.T) {
	var s StreamBuf
	s.push("hel")
	s.push("lo")
	s.pushReasoning("thinking")

	chunk, reasoning, usage, done, aborted, err := s.Drain()
	if chunk != "hello" {
		t.Errorf("chunk = %q, want %q", chunk, "hello")
	}
	if reasoning != "thinking" {
		t.Errorf("reasoning = %q, want %q", reasoning, "thinking")
	}
	if usage != nil || done || aborted || err != nil {
		t.Errorf("unexpected state before finish: usage=%v done=%v aborted=%v err=%v", usage, done, aborted, err)
	}

	// A second Drain with nothing pushed in between must come back empty:
	// the whole point of Reset() is that text doesn't repeat itself.
	chunk, reasoning, _, _, _, _ = s.Drain()
	if chunk != "" || reasoning != "" {
		t.Errorf("second Drain() with no new pushes returned chunk=%q reasoning=%q, want both empty", chunk, reasoning)
	}
}

func TestStreamBufUsagePersistsAcrossDrains(t *testing.T) {
	var s StreamBuf
	u := &Usage{In: 10, Out: 5}
	s.setUsage(u)

	_, _, got, _, _, _ := s.Drain()
	if got != u {
		t.Fatalf("Drain() usage = %v, want the pointer set by setUsage", got)
	}

	// Usage is a running total, not a delta: it must still be there on the
	// next Drain even though nothing new was pushed.
	_, _, got, _, _, _ = s.Drain()
	if got != u {
		t.Errorf("Drain() usage on second call = %v, want it to persist", got)
	}
}

func TestStreamBufFinishDistinguishesAbortedFromError(t *testing.T) {
	var aborted StreamBuf
	aborted.finish(nil, true)
	_, _, _, done, isAborted, err := aborted.Drain()
	if !done || !isAborted || err != nil {
		t.Errorf("a cancelled turn must be done=true, aborted=true, err=nil; got done=%v aborted=%v err=%v", done, isAborted, err)
	}

	var failed StreamBuf
	failWith := errUnitTest("boom")
	failed.finish(failWith, false)
	chunk, reasoning, _, done, isAborted, err := failed.Drain()
	_ = chunk
	_ = reasoning
	if !done || isAborted || err != failWith {
		t.Errorf("a failed turn must be done=true, aborted=false, err=the failure; got done=%v aborted=%v err=%v", done, isAborted, err)
	}
}

// TestStreamBufConcurrentPushDrain exercises the one property StreamBuf
// exists for: push/setUsage/finish from one goroutine racing with Drain from
// another must never corrupt state or trip the race detector (go test -race
// is what actually enforces this; the assertions here just check the totals
// add up once both sides are done).
func TestStreamBufConcurrentPushDrain(t *testing.T) {
	var s StreamBuf
	const n = 500

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			s.push("x")
		}
		s.finish(nil, false)
	}()

	var total int
	for {
		chunk, _, _, done, _, _ := s.Drain()
		total += len(chunk)
		if done {
			// One last drain in case push() and finish() landed between our
			// last read and the done flag going up.
			chunk, _, _, _, _, _ := s.Drain()
			total += len(chunk)
			break
		}
	}
	if total != n {
		t.Errorf("total pushed bytes drained = %d, want %d", total, n)
	}

	wg.Wait()
}

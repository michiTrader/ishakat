package engine

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

type fakeRetryable struct {
	wait      time.Duration
	retryable bool
}

func (e fakeRetryable) Error() string                { return "fake retryable error" }
func (e fakeRetryable) Retry() (time.Duration, bool) { return e.wait, e.retryable }

func TestRetryAfterHonoursTheErrorsOwnWait(t *testing.T) {
	wait, retry := retryAfter(fakeRetryable{wait: 3 * time.Second, retryable: true}, 0, 3)
	if !retry {
		t.Fatal("retry = false, want true")
	}
	// A server-specified wait is jittered upward only (step 26, fix 2): the
	// band is [3s, 3.6s], never below. This assertion used to allow 2.4s.
	lo, hi := 3*time.Second, 3*time.Second*6/5
	if wait < lo || wait > hi {
		t.Errorf("wait = %v, want within [%v, %v]", wait, lo, hi)
	}
}

func TestRetryAfterFallsBackToBackoffWhenErrorHasNoWait(t *testing.T) {
	wait, retry := retryAfter(fakeRetryable{wait: 0, retryable: true}, 2, 5)
	if !retry {
		t.Fatal("retry = false, want true")
	}
	// attempt=2 → backoff(2) = 500ms<<2 = 2s, ±20% jitter.
	lo, hi := 2*time.Second*4/5, 2*time.Second*6/5
	if wait < lo || wait > hi {
		t.Errorf("wait = %v, want within ±20%% of 2s (%v..%v)", wait, lo, hi)
	}
}

func TestRetryAfterCapsAnOverlongWait(t *testing.T) {
	wait, retry := retryAfter(fakeRetryable{wait: time.Hour, retryable: true}, 0, 3)
	if !retry {
		t.Fatal("retry = false, want true")
	}
	if wait > maxRetryWait*6/5 { // allow for jitter headroom above the cap
		t.Errorf("wait = %v, want capped near %v", wait, maxRetryWait)
	}
}

func TestRetryAfterStopsAtMaxRetries(t *testing.T) {
	_, retry := retryAfter(fakeRetryable{wait: time.Second, retryable: true}, 3, 3)
	if retry {
		t.Error("attempt == maxRetries must not retry")
	}
	_, retry = retryAfter(fakeRetryable{wait: time.Second, retryable: true}, 4, 3)
	if retry {
		t.Error("attempt > maxRetries must not retry")
	}
}

func TestRetryAfterHonoursRetryableFalse(t *testing.T) {
	_, retry := retryAfter(fakeRetryable{wait: time.Second, retryable: false}, 0, 3)
	if retry {
		t.Error("an error that reports retryable=false must not retry")
	}
}

func TestRetryAfterRejectsUnhintedErrors(t *testing.T) {
	_, retry := retryAfter(errors.New("boom"), 0, 3)
	if retry {
		t.Error("a plain error with no retryHint must not retry")
	}
}

func TestRetryAfterFindsAWrappedHint(t *testing.T) {
	wrapped := fmt.Errorf("stream: %w", fakeRetryable{wait: time.Second, retryable: true})
	_, retry := retryAfter(wrapped, 0, 3)
	if !retry {
		t.Error("a wrapped retryHint must still be found through errors.As")
	}
}

// TestRetryAfterNeverRetriesEarlierThanTheServerSaid is step 26's fix 2 at
// its sharpest point. Symmetric ±20% jitter treated Retry-After as a
// midpoint, so a server saying "22" was measurably retried at ~17.6s --
// nearly five seconds inside a window the provider had explicitly closed.
// On an account that is already rate-limited that earns a second 429, which
// is how a pacing mechanism turns back into an amplifier
// (docs/BUG-rate-limit-amplifier.md).
//
// Randomized, so it is run many times rather than once: a one-shot check
// would pass roughly half the time against the very bug it is pinning.
func TestRetryAfterNeverRetriesEarlierThanTheServerSaid(t *testing.T) {
	const want = 22 * time.Second
	for i := 0; i < 500; i++ {
		wait, retry := retryAfter(fakeRetryable{wait: want, retryable: true}, 0, 3)
		if !retry {
			t.Fatal("a retryable 429 must retry")
		}
		if wait < want {
			t.Fatalf("wait = %v, %v earlier than the server permitted", wait, want-wait)
		}
		if wait > want*6/5 {
			t.Fatalf("wait = %v, more than +20%% above the server's %v", wait, want)
		}
	}
}

// TestRetryAfterStillJittersAnInventedBackoff is the other half of the same
// decision: only a *server-specified* wait is a floor. A backoff engine
// invented itself is a guess, and spreading it in both directions is what
// keeps a fleet of clients from resynchronizing after a shared outage. It
// would be easy to "fix" the test above by deleting jitter entirely; this
// makes that regression visible.
func TestRetryAfterStillJittersAnInventedBackoff(t *testing.T) {
	var sawBelow, sawAbove bool
	for i := 0; i < 500; i++ {
		// wait: 0 -> no server hint -> backoff(2) = 2s, jittered both ways.
		wait, retry := retryAfter(fakeRetryable{wait: 0, retryable: true}, 2, 5)
		if !retry {
			t.Fatal("a retryable error under maxRetries must retry")
		}
		switch {
		case wait < 2*time.Second:
			sawBelow = true
		case wait > 2*time.Second:
			sawAbove = true
		}
	}
	if !sawBelow || !sawAbove {
		t.Errorf("invented backoff should spread both ways; sawBelow=%v sawAbove=%v", sawBelow, sawAbove)
	}
}

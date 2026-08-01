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
	// Jitter is ±20%, so the result must stay within that band of the
	// requested wait.
	lo, hi := 3*time.Second*4/5, 3*time.Second*6/5
	if wait < lo || wait > hi {
		t.Errorf("wait = %v, want within ±20%% of 3s (%v..%v)", wait, lo, hi)
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

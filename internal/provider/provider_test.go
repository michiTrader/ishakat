package provider

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

// retryHint mirrors internal/engine's unexported interface of the same
// shape (Retry() (time.Duration, bool)) — duplicated here rather than
// imported, on purpose: this is exactly the structural contract engine
// relies on without either package importing the other, and the test
// documents that the shape actually lines up.
type retryHint interface {
	Retry() (time.Duration, bool)
}

func TestErrorRetryReflectsItsFields(t *testing.T) {
	e := &Error{RetryAfter: 3 * time.Second, Retryable: true}
	wait, retryable := e.Retry()
	if wait != 3*time.Second || !retryable {
		t.Errorf("Retry() = (%v, %v), want (3s, true)", wait, retryable)
	}

	e2 := &Error{Retryable: false}
	if _, retryable := e2.Retry(); retryable {
		t.Error("Retry() reported retryable=true for an error with Retryable=false")
	}
}

func TestErrorRetryOnNilIsSafe(t *testing.T) {
	var e *Error
	wait, retryable := e.Retry()
	if wait != 0 || retryable {
		t.Errorf("Retry() on a nil *Error = (%v, %v), want (0, false)", wait, retryable)
	}
}

// TestErrorSatisfiesRetryHintStructurally is the point of Retry() existing:
// internal/engine's retry.go finds this through errors.As without ever
// importing this package.
func TestErrorSatisfiesRetryHintStructurally(t *testing.T) {
	var err error = &Error{RetryAfter: time.Second, Retryable: true}

	var hint retryHint
	if !errors.As(err, &hint) {
		t.Fatal("*Error does not satisfy retryHint via errors.As")
	}
	if wait, retryable := hint.Retry(); wait != time.Second || !retryable {
		t.Errorf("hint.Retry() = (%v, %v), want (1s, true)", wait, retryable)
	}
}

// TestErrorSatisfiesRetryHintThroughWrapping checks the same match survives
// the kind of wrapping headless.go's runTurn and internal/app's Streamer
// adapter actually do (fmt.Errorf("%w", ...)).
func TestErrorSatisfiesRetryHintThroughWrapping(t *testing.T) {
	inner := &Error{RetryAfter: 2 * time.Second, Retryable: true}
	wrapped := fmt.Errorf("stream: %w", inner)

	var hint retryHint
	if !errors.As(wrapped, &hint) {
		t.Fatal("a wrapped *Error does not satisfy retryHint via errors.As")
	}
	if wait, _ := hint.Retry(); wait != 2*time.Second {
		t.Errorf("hint.Retry() wait = %v, want 2s", wait)
	}
}

package engine

import (
	"errors"
	"math/rand"
	"time"
)

// backoffBase and backoffCap match headless.go's runTurn (§5.4's honest
// minimum): 500ms, 1s, 2s, 4s, 8s, capped. Step 8 adds jitter on top so a
// batch of retries after a shared outage doesn't all land on the same
// millisecond — headless.go's non-interactive `-p` doesn't need that, an
// interactive TUI juggling this against a spinner does.
const (
	backoffBase   = 500 * time.Millisecond
	backoffCap    = 30 * time.Second
	maxRetryWait  = 30 * time.Second
	jitterPercent = 0.2 // ±20%
)

// retryAfter decides whether a handshake failure is worth retrying and how
// long to wait first. err is only ever the error a Streamer's opening call
// returned — the handshake, never a mid-stream cut, matching headless.go's
// existing policy: resending a turn that already printed part of an answer
// would duplicate it, so the engine never retries once EventDelta has
// started flowing (engine.go enforces that by only calling retryAfter
// before the channel exists).
//
// attempt is 0-based (this is the (attempt+1)-th failure); maxRetries is the
// config value (App.MaxRetries, §5.4) below which retrying is even
// considered.
func retryAfter(err error, attempt, maxRetries int) (wait time.Duration, retry bool) {
	if attempt >= maxRetries {
		return 0, false
	}

	var hint retryHint
	if !errors.As(err, &hint) {
		// Not a hinted error at all (a plain network error, a bug): engine
		// doesn't guess, it just doesn't retry. Streamer implementations
		// that want retries wrap their errors in something satisfying
		// retryHint — provider.Error already does.
		return 0, false
	}
	d, ok := hint.Retry()
	if !ok {
		return 0, false
	}

	if d <= 0 {
		d = backoff(attempt)
	}
	if d > maxRetryWait {
		d = maxRetryWait
	}
	return jitter(d), true
}

// backoff is the fallback when the hint didn't come with its own wait (no
// Retry-After header, just "yes this is retryable"): exponential, doubling
// from backoffBase, capped at backoffCap.
func backoff(attempt int) time.Duration {
	d := backoffBase << attempt
	if d > backoffCap || d <= 0 { // d<=0 catches the shift overflowing on a huge attempt
		d = backoffCap
	}
	return d
}

// jitter spreads a wait by ±jitterPercent so many clients retrying the same
// outage don't all wake up on the same tick.
func jitter(d time.Duration) time.Duration {
	spread := float64(d) * jitterPercent
	delta := (rand.Float64()*2 - 1) * spread
	return d + time.Duration(delta)
}

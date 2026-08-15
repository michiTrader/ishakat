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

	// Where the wait came from decides how it may be jittered, and step 26
	// turns on that distinction (docs/BUG-rate-limit-amplifier.md, fix 2).
	//
	// A backoff we invented is a guess, so spreading it in both directions
	// is free. A wait the *server* specified is not a guess: Retry-After: 22
	// means the window is closed for 22 seconds, and coming back at 17.6s —
	// which symmetric ±20% jitter did, measurably — earns a second 429. On
	// an account that is already rate-limited, that re-arms the very
	// amplifier this step exists to remove, and it does it at the worst
	// possible moment. So a server-specified wait is jittered upward only:
	// still spread, so a fleet of clients does not resynchronize, but never
	// earlier than the server permitted.
	if d <= 0 {
		if d = backoff(attempt); d > maxRetryWait {
			d = maxRetryWait
		}
		return jitter(d), true
	}
	if d > maxRetryWait {
		d = maxRetryWait
	}
	return jitterUp(d), true
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
// outage don't all wake up on the same tick. Only ever applied to a wait
// engine invented itself (backoff) — see jitterUp for the other case.
func jitter(d time.Duration) time.Duration {
	spread := float64(d) * jitterPercent
	delta := (rand.Float64()*2 - 1) * spread
	return d + time.Duration(delta)
}

// jitterUp spreads a wait by +jitterPercent only, never returning less than
// d. It is what a server-specified Retry-After gets: the spread still keeps
// many clients from waking on the same tick, but the floor the server named
// is treated as a floor rather than a midpoint.
func jitterUp(d time.Duration) time.Duration {
	return d + time.Duration(rand.Float64()*float64(d)*jitterPercent)
}

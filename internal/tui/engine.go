// engine.go is the seam between Root and the turn runner (§7.3/§7.4): it
// holds the fallback used when a caller builds a Root without giving it an
// *engine.Engine.
//
// The fallback exists so that "no engine" is an ordinary failed turn instead
// of a nil dereference in the middle of Update. Every test in this package
// builds a Root through NewRoot with a bare Options, and Step 8 replaced the
// Step 3 mannequin (pendingEcho/driveEcho) with a real engine call — without
// a default, that change would have turned every one of those tests into a
// panic the first time a message was submitted, and the interesting part of
// those tests (layout, wrapping, eviction, cursor position) has nothing to do
// with whether a provider was configured.
//
// Importing internal/engine here is allowed by §6.1 and deliberate: engine
// does not import internal/provider, precisely so that internal/tui can reach
// the turn runner without pulling net/http in behind it (see
// TestTUINoImportaHTTP in internal/arch_test.go, and engine's own package
// comment). The adapter that does know about providers lives in internal/app.
package tui

import (
	"context"
	"errors"

	"github.com/MichiTrader/ishakat/internal/engine"
)

// ErrNoProvider is the error a Root built without an Engine reports when a
// turn is submitted. It is exported so internal/app can compare against it
// (errors.Is) rather than match on the message, and its text is user-facing
// Spanish like the rest of the interface's strings.
var ErrNoProvider = errors.New("no hay proveedor configurado")

// noProviderStreamer is an engine.Streamer whose handshake always fails with
// ErrNoProvider. A plain errors.New value does not satisfy engine's retryHint
// interface, so retryAfter refuses to retry it and the turn closes on the
// first attempt — which is what we want: nothing about this failure is going
// to get better by waiting.
func noProviderStreamer(context.Context, engine.Request) (<-chan engine.Event, error) {
	return nil, ErrNoProvider
}

// engineOr returns e unchanged, or — when e is nil — an Engine that fails
// every turn immediately with ErrNoProvider. maxRetries is zero because
// retryAfter's first check is `attempt >= maxRetries`, so zero means "do not
// even consider retrying", saving the failing turn a pointless trip through
// the backoff arithmetic.
func engineOr(e *engine.Engine) *engine.Engine {
	if e != nil {
		return e
	}
	return engine.New(noProviderStreamer, 0)
}

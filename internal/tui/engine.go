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

	"github.com/MichiTrader/ishakat/internal/catalog"
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

// EngineFactory rebuilds an *engine.Engine bound to ref (a §4.2 Ref,
// "provider/model") — internal/app.NewEngineFactory is the real
// implementation, wrapping the exact same ResolveModel/FindProvider/
// NewProvider/NewStreamer path BuildEngine already walks at startup, so a
// model switch mid-session and the one at boot can never disagree about
// what a given Ref resolves to. It lives here, as a function type rather
// than an interface, purely so this package's own tests can hand Root a
// three-line closure instead of a real provider — the same reason Streamer
// itself (internal/engine) is a function type and not an interface.
//
// tui still does not know what a provider is (§6.1): this signature never
// mentions internal/provider or internal/config, only the engine package
// this file already imports.
type EngineFactory func(ref string) (*engine.Engine, error)

// switchEngine is every model-switch site's shared last step: ask
// m.engineFor for a fresh Engine bound to ref, and only commit it into m.eng
// if that succeeds. A nil engineFor (no factory wired — most of this
// package's own tests) or an error (the destination provider is disabled,
// undeclared, or missing its API key — the exact failures NewProvider
// already names) leaves m.eng untouched and returns that reason instead of
// silently pretending the switch bound a new client: the caller decides
// whether that is worth surfacing as a warning, but the label (m.model) and
// the transport (m.eng) must never disagree about which provider a turn
// will actually reach, which is the bug this function exists to close (see
// Root.eng's own comment).
func switchEngine(m Root, ref string) (Root, error) {
	if m.engineFor == nil {
		return m, nil
	}
	eng, err := m.engineFor(ref)
	if err != nil {
		return m, err
	}
	m.eng = eng
	return m, nil
}

// wireModel resolves ref — a §4.2 Ref ("provider/model", the form every
// Root field that holds a model — m.model, m.compactModel — is documented
// to carry, never the wire id) — to the WireID that actually belongs in the
// request body's "model" field.
//
// OmniRoute is the reason this exists: its own served model identifiers can
// contain slashes ("auto/coding"), so the Ref ends up as
// "omniroute/auto/coding" and a naive strings.Split on "/" — or, as it
// happened here, forwarding the Ref itself unchanged — sends OmniRoute a
// model name it has never heard of, which it reports back as a misleading
// "no active credentials for provider" 404 instead of "unknown model".
//
// The catalog is the trustworthy source (cat.Get already knows each row's
// real WireID), so it is tried first. When the catalog does not have an
// entry for ref — no catalog at all, or a model the fetch missed — this
// falls back to catalog.SplitRef's first-slash cut, the same fallback
// picker.go's renderPickerRow already relies on for its own display of the
// wire id. If even that fails (ref has no slash to cut), ref is returned
// unchanged rather than empty: sending what the user actually typed is
// always a better failure than sending nothing.
func wireModel(cat *catalog.Catalog, ref string) string {
	if model, ok := cat.Get(ref); ok {
		return model.WireID
	}
	if _, wireID, ok := catalog.SplitRef(ref); ok {
		return wireID
	}
	return ref
}

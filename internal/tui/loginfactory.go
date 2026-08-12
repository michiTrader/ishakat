// loginfactory.go names the seam ModeLogin's HTTP-driving half crosses
// (§6.1, the same boundary engine.go's EngineFactory already draws for
// switching models): internal/tui cannot import net/http
// (internal/arch_test.go's TestTUINoImportaHTTP), so the actual
// RequestDeviceCode/PollForToken calls against internal/oauth have to live
// on the far side of a function-typed seam this package only calls
// through, never implements. internal/app.NewLoginFactory (on the far side
// of that same §6.1 boundary EngineFactory's own comment describes) is the
// real implementation, reusing the exact request→display→poll→verify→save
// sequence cmd/ishakat/login.go's runLogin already established for the CLI
// door.
package tui

import "context"

// LoginDeviceCode is RFC 8628 §3.2's device-authorization response,
// reduced to only what a wizard screen needs to render — the code the
// user types and the URL they visit — so this package never has to
// import internal/oauth's own DeviceCodeResponse (whose ExpiresIn/
// Interval fields exist for PollForToken's own pacing, not for display).
type LoginDeviceCode struct {
	UserCode        string
	VerificationURI string
}

// LoginWaiter is the second half of a login attempt: whatever
// LoginFactory returned alongside the device code, held by loginState
// until either the wizard's own esc/ctrl+c cancels ctx or Wait returns.
// It exists as an interface, not a bare function, purely so
// engineswitch_internal_test.go's own trackingFactory/failingFactory
// pattern has something to fake without a real network call — the same
// reason EngineFactory is a function type and not a concrete struct.
type LoginWaiter interface {
	// Wait blocks until the device flow finishes: PollForToken's own
	// (up to 15-minute) wait, followed by verify-then-write. note is the
	// one-line success message to show (mirroring runLogin's "Configured
	// X via OAuth device flow." line); err is nil exactly when note is
	// meaningful. ctx cancellation (this wizard's esc/ctrl+c) is how a
	// caller gives up early — Wait has no separate cancel method, the
	// same "ctx already is one" rule internal/oauth.PollForToken's own
	// doc comment states.
	Wait(ctx context.Context) (note string, err error)
}

// LoginFactory begins a device-flow login for a provider preset id (e.g.
// "openai") — the exact identifier cmd/ishakat/login.go's own
// config.ResolveProviderPreset already resolves from the CLI's
// positional argument. It performs the one quick RequestDeviceCode call
// itself (so the wizard has something to show before it ever opens
// ModeLogin's overlay) and returns a LoginWaiter for the slow half.
//
// nil is a supported value (most of this package's own tests, which never
// touch a real provider): startLogin reports that /login has nothing
// wired to it rather than dereferencing a nil factory, the same "no
// silent panic on an unwired dependency" rule switchEngine's own nil
// check for engineFor already follows.
type LoginFactory func(ctx context.Context, providerID string) (LoginDeviceCode, LoginWaiter, error)

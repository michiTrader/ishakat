// Package oauth implements the OAuth 2.0 Device Authorization Grant
// (RFC 8628) used by Step 24's "/login" (docs/PLAN.md §11): a way to
// authenticate a provider without ever asking the user to paste a static
// API key, for services that support it (GitHub's own OAuth apps, which is
// the door this build uses to reach GitHub Copilot's OpenAI-compatible
// endpoint — see internal/config/credentials.go's provider presets for
// where the API-key wizard half of this step already lives).
//
// This package knows nothing about ishakat's config, credentials file, or
// TUI: it is a generic RFC 8628 client, parameterized by the three URLs and
// the client_id a specific provider's device flow needs (see Config below).
// cmd/ishakat/login.go (the CLI entry point) and any TUI-side /login wizard
// are the callers that know which provider they are authenticating against;
// this package only knows the protocol.
//
// The three-step dance this package implements is exactly RFC 8628 §3.1-3.5:
//
//  1. RequestDeviceCode: POST to the provider's device authorization
//     endpoint with client_id (and, for some providers, scope). The response
//     carries a device_code (kept secret, never shown to the user), a
//     user_code (shown to the user, typed into a browser) and a
//     verification_uri (where to type it).
//  2. The caller displays user_code and verification_uri, and the user
//     visits that URL in any browser — on the same machine or a different
//     one, which is the entire point of the device flow: it works over SSH,
//     inside a container, on a headless CI runner, anywhere a browser
//     cannot be launched by the process itself.
//  3. PollForToken: POST to the provider's token endpoint at the server's
//     own requested interval until the user finishes step 2 (success),
//     explicitly denies the request, or the device_code expires. This
//     function owns the entire polling loop, including the "slow_down"
//     backoff RFC 8628 §3.5 mandates, so a caller never has to reimplement
//     retry timing to stay within a provider's rate limit.
package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Config names one provider's device-flow endpoints and client_id. Every
// field here is public information — a client_id for the device flow is
// not a secret (RFC 8628 assumes it can be embedded in a binary; there is
// no client_secret anywhere in this package, deliberately, since the device
// flow does not use one) — so Config never needs anything from
// internal/config/credentials.go's private credentials file.
type Config struct {
	// ClientID identifies the OAuth application requesting the device code.
	ClientID string

	// Scope is the space-delimited list of scopes requested, or "" for a
	// provider that has none / does not require one on this endpoint.
	Scope string

	// DeviceCodeURL is where RequestDeviceCode POSTs
	// (client_id, scope) -> (device_code, user_code, verification_uri, ...).
	DeviceCodeURL string

	// TokenURL is where PollForToken POSTs
	// (client_id, device_code, grant_type) -> (access_token, ...) or one of
	// RFC 8628's polling error codes (authorization_pending, slow_down, ...).
	TokenURL string

	// HTTPClient is the client used for both requests. nil means
	// http.DefaultClient. Tests inject a client pointed at an httptest
	// server here instead of dialing a live provider — the same "no
	// network in tests" discipline internal/app/serve_test.go already
	// follows for the WebSocket door.
	HTTPClient *http.Client
}

func (c Config) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

// DeviceCodeResponse is RFC 8628 §3.2's response to the device
// authorization request: the two codes and the URL the user needs, plus
// the two timing fields PollForToken consumes on the caller's behalf.
type DeviceCodeResponse struct {
	// DeviceCode identifies this authorization request to the token
	// endpoint. Never shown to the user — displaying it would let anyone
	// who saw it complete the flow on the user's behalf without their
	// device_code being tied to the display.
	DeviceCode string

	// UserCode is what the caller must show the user, to be typed into
	// VerificationURI's page. Some providers also expose
	// VerificationURIComplete, which pre-fills the code into the URL
	// itself (RFC 8628 §3.3.1); this struct does not carry it, since none
	// of this build's own callers use it yet, and adding an unused field
	// speculatively would be exactly the kind of premature parameterizing
	// this codebase's own doc comments elsewhere warn against.
	UserCode string

	// VerificationURI is the page the user visits to enter UserCode.
	VerificationURI string

	// ExpiresIn is how many seconds DeviceCode and UserCode remain valid
	// (RFC 8628 default: 900 = 15 minutes). After this, a fresh
	// RequestDeviceCode call is required; PollForToken surfaces this as
	// ErrExpired once the server itself reports expired_token, rather than
	// racing its own local timer against the server's clock.
	ExpiresIn int

	// Interval is the minimum number of seconds the caller must wait
	// between token-endpoint polls (RFC 8628 default: 5). PollForToken
	// honours this itself; a caller driving RequestDeviceCode and
	// PollForToken separately (rather than through Login, below) needs it
	// only to render "checking again in Ns" to a human, since PollForToken
	// does not expose a per-attempt callback.
	Interval int
}

// TokenResponse is RFC 8628 §3.5's success response: the token a caller
// hands to whatever API it is now authorized to call, as
// "Authorization: Bearer <AccessToken>" or the provider's own documented
// header shape (GitHub's device flow issues a token used as
// "Authorization: token <AccessToken>" against api.github.com, and as
// "Authorization: Bearer <AccessToken>" once exchanged for a Copilot
// session token — this package returns the raw value either way; the
// caller's provider-specific code decides the header).
type TokenResponse struct {
	AccessToken string
	TokenType   string
	Scope       string
}

// deviceCodeErrors are the RFC 8628 §3.5 polling error codes this package
// understands. authorization_pending and slow_down are not failures: they
// are PollForToken's own loop condition, handled internally and never
// returned to the caller. The rest end the loop.
const (
	errAuthorizationPending = "authorization_pending"
	errSlowDown             = "slow_down"
	errExpiredToken         = "expired_token"
	errAccessDenied         = "access_denied"
)

// Sentinel errors PollForToken (and Login) can return, so a caller can
// branch with errors.Is instead of matching on a message string that might
// change wording between this package's versions.
var (
	// ErrAccessDenied is returned when the user explicitly declined the
	// authorization request on the provider's own page (RFC 8628's
	// access_denied). The device_code and user_code cannot be reused after
	// this — a fresh RequestDeviceCode is required to try again.
	ErrAccessDenied = errors.New("oauth: the user denied the authorization request")

	// ErrExpired is returned when the device_code expired before the user
	// completed the flow (RFC 8628's expired_token) — the default window
	// is 15 minutes. Same remedy as ErrAccessDenied: request a new code.
	ErrExpired = errors.New("oauth: the device code expired before authorization completed")
)

// RequestDeviceCode performs RFC 8628 §3.1's device authorization request:
// one POST to cfg.DeviceCodeURL, decoding the JSON response into a
// DeviceCodeResponse. It sends "Accept: application/json" because GitHub's
// own device-flow endpoint (the provider this build's first caller targets)
// defaults to a form-encoded response body without it — see the package
// comment on docs.github.com's own device-flow page, which documents both
// shapes side by side.
func RequestDeviceCode(ctx context.Context, cfg Config) (DeviceCodeResponse, error) {
	form := url.Values{"client_id": {cfg.ClientID}}
	if cfg.Scope != "" {
		form.Set("scope", cfg.Scope)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.DeviceCodeURL, strings.NewReader(form.Encode()))
	if err != nil {
		return DeviceCodeResponse{}, fmt.Errorf("oauth: build device code request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := cfg.httpClient().Do(req)
	if err != nil {
		return DeviceCodeResponse{}, fmt.Errorf("oauth: device code request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return DeviceCodeResponse{}, fmt.Errorf("oauth: read device code response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return DeviceCodeResponse{}, fmt.Errorf("oauth: device code request returned HTTP %d: %s", resp.StatusCode, shortBody(body))
	}

	var raw struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		ExpiresIn       int    `json:"expires_in"`
		Interval        int    `json:"interval"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return DeviceCodeResponse{}, fmt.Errorf("oauth: decode device code response: %w (body: %s)", err, shortBody(body))
	}
	if raw.DeviceCode == "" || raw.UserCode == "" || raw.VerificationURI == "" {
		return DeviceCodeResponse{}, fmt.Errorf("oauth: device code response missing required fields: %s", shortBody(body))
	}
	if raw.Interval <= 0 {
		// RFC 8628 §3.2 says interval defaults to 5 when the server omits
		// it. Zero would make PollForToken's loop spin with no delay at
		// all, hammering the token endpoint until it rate-limits — the
		// exact "slow_down" cycle the polling interval exists to prevent.
		raw.Interval = 5
	}
	if raw.ExpiresIn <= 0 {
		raw.ExpiresIn = 900
	}

	return DeviceCodeResponse{
		DeviceCode:      raw.DeviceCode,
		UserCode:        raw.UserCode,
		VerificationURI: raw.VerificationURI,
		ExpiresIn:       raw.ExpiresIn,
		Interval:        raw.Interval,
	}, nil
}

// deviceGrantType is RFC 8628 §3.4's required grant_type value for the
// token-endpoint poll. Not a Config field: it is fixed by the RFC, not
// something any provider's device flow can vary.
const deviceGrantType = "urn:ietf:params:oauth:grant-type:device_code"

// PollForToken performs RFC 8628 §3.4/§3.5's polling loop: it POSTs to
// cfg.TokenURL at the pace dc.Interval (extended by 5s on every slow_down,
// exactly as the RFC's error response instructs) until the provider
// reports success, the user denies the request (ErrAccessDenied), the
// device code expires (ErrExpired), or ctx is done — whichever happens
// first. There is no separate "cancel" argument because ctx already is
// one: a caller wanting a "give up after N minutes" ceiling, or an
// interactive "press q to cancel", wires that into ctx with
// context.WithTimeout / context.WithCancel rather than this package
// inventing a second cancellation mechanism to keep in sync with the first.
//
// authorization_pending is the expected, silent steady state while the
// user has not yet visited VerificationURI: it produces no error and no
// log line here, since a caller polling once every 5 seconds for up to 15
// minutes printing something every time would be far noisier than the
// "waiting..." spinner it should be showing instead.
func PollForToken(ctx context.Context, cfg Config, dc DeviceCodeResponse) (TokenResponse, error) {
	interval := time.Duration(dc.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	// RFC 8628 §3.5's slow_down remedy is specified in whole seconds, but
	// pollLoop's step parameter is also handed a millisecond-scale value by
	// this package's own tests (see device_test.go's fast* helpers), which
	// need the loop to run in milliseconds rather than RFC 8628's real-world
	// 5-second cadence without duplicating this function's logic to do it.
	return pollLoop(ctx, cfg, dc, interval, 5*time.Second)
}

// pollLoop is PollForToken's actual loop, factored out so this package's
// own tests can drive it with a millisecond-scale interval/slowDownStep
// (see device_test.go) while exercising the exact same code a real caller
// runs — rather than a second, hand-duplicated copy of the loop that could
// silently drift from what PollForToken really does.
func pollLoop(ctx context.Context, cfg Config, dc DeviceCodeResponse, interval, slowDownStep time.Duration) (TokenResponse, error) {
	deadline := time.Now().Add(time.Duration(dc.ExpiresIn) * time.Second)

	for {
		select {
		case <-ctx.Done():
			return TokenResponse{}, ctx.Err()
		case <-time.After(interval):
		}

		if time.Now().After(deadline) {
			return TokenResponse{}, ErrExpired
		}

		tok, pollErr, err := pollOnce(ctx, cfg, dc.DeviceCode)
		if err != nil {
			return TokenResponse{}, err
		}
		switch pollErr {
		case "":
			return tok, nil
		case errAuthorizationPending:
			continue
		case errSlowDown:
			// RFC 8628 §3.5: "the interval MUST be increased by 5 seconds
			// for all subsequent requests". This value is meant to persist
			// for the rest of this poll loop, not just the next attempt —
			// a provider issuing slow_down once is telling the client its
			// steady-state pace was too aggressive, not asking for a single
			// one-off delay.
			interval += slowDownStep
		case errExpiredToken:
			return TokenResponse{}, ErrExpired
		case errAccessDenied:
			return TokenResponse{}, ErrAccessDenied
		default:
			return TokenResponse{}, fmt.Errorf("oauth: token endpoint returned unrecognized error %q", pollErr)
		}
	}
}

// pollOnce performs exactly one POST to cfg.TokenURL. It returns either a
// populated TokenResponse (pollErr == "", err == nil), a recognized RFC
// 8628 polling error code (tok is zero, err == nil — PollForToken's loop
// decides what that code means), or a transport/decode failure (err != nil,
// which always ends PollForToken's loop rather than being retried: a
// malformed response or a network failure is not one of the polling states
// RFC 8628 defines, so silently retrying it could spin forever on a
// provider outage instead of surfacing it).
func pollOnce(ctx context.Context, cfg Config, deviceCode string) (TokenResponse, string, error) {
	form := url.Values{
		"client_id":   {cfg.ClientID},
		"device_code": {deviceCode},
		"grant_type":  {deviceGrantType},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return TokenResponse{}, "", fmt.Errorf("oauth: build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := cfg.httpClient().Do(req)
	if err != nil {
		return TokenResponse{}, "", fmt.Errorf("oauth: token request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return TokenResponse{}, "", fmt.Errorf("oauth: read token response: %w", err)
	}

	var raw struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		Scope       string `json:"scope"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
		Interval    int    `json:"interval"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return TokenResponse{}, "", fmt.Errorf("oauth: decode token response: %w (body: %s)", err, shortBody(body))
	}

	if raw.Error != "" {
		return TokenResponse{}, raw.Error, nil
	}
	if raw.AccessToken == "" {
		// A 200 with neither an access_token nor a recognized error field
		// is not a shape RFC 8628 defines. Treating it as
		// authorization_pending would be guessing at what the provider
		// meant; surfacing it as a decode-shaped error is honest about not
		// knowing.
		return TokenResponse{}, "", fmt.Errorf("oauth: token response had neither access_token nor error: %s", shortBody(body))
	}

	return TokenResponse{
		AccessToken: raw.AccessToken,
		TokenType:   raw.TokenType,
		Scope:       raw.Scope,
	}, "", nil
}

// shortBody truncates a raw response body for an error message, the same
// "don't dump an entire HTML error page into a CLI error line" rule
// cmd/ishakat/verify.go's own shortMsg already applies to provider
// verification failures.
func shortBody(b []byte) string {
	const max = 200
	s := strings.TrimSpace(string(b))
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

// FormatWait renders how long is left before dc's device code expires, in
// a form suitable for a CLI prompt ("14m32s"). It exists so
// cmd/ishakat/login.go does not need its own time-formatting logic for a
// single line of output.
func FormatWait(dc DeviceCodeResponse) string {
	return (time.Duration(dc.ExpiresIn) * time.Second).String()
}

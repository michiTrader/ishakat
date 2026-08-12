package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

// Every test in this file runs entirely offline against an httptest.Server
// (the same discipline internal/app/serve_test.go already follows for the
// WebSocket door): no test here ever dials a real provider, and PollForToken
// is always driven with a sub-second dc.Interval so the suite runs in
// milliseconds rather than RFC 8628's real-world 5-second cadence.

func jsonHandler(t *testing.T, fn func(r *http.Request) (int, any)) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		status, body := fn(r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if body != nil {
			if err := json.NewEncoder(w).Encode(body); err != nil {
				t.Fatalf("encode test response: %v", err)
			}
		}
	}
}

func TestRequestDeviceCodeParsesTheDocumentedShape(t *testing.T) {
	srv := httptest.NewServer(jsonHandler(t, func(r *http.Request) (int, any) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("expected Accept: application/json, got %q", got)
		}
		body, _ := url.ParseQuery(mustReadBody(t, r))
		if body.Get("client_id") != "test-client" {
			t.Errorf("client_id = %q, want test-client", body.Get("client_id"))
		}
		if body.Get("scope") != "read:user" {
			t.Errorf("scope = %q, want read:user", body.Get("scope"))
		}
		return http.StatusOK, map[string]any{
			"device_code":      "dc-123",
			"user_code":        "WDJB-MJHT",
			"verification_uri": "https://example.test/login/device",
			"expires_in":       900,
			"interval":         5,
		}
	}))
	defer srv.Close()

	cfg := Config{ClientID: "test-client", Scope: "read:user", DeviceCodeURL: srv.URL, HTTPClient: srv.Client()}
	dc, err := RequestDeviceCode(context.Background(), cfg)
	if err != nil {
		t.Fatalf("RequestDeviceCode: %v", err)
	}
	if dc.DeviceCode != "dc-123" || dc.UserCode != "WDJB-MJHT" || dc.VerificationURI != "https://example.test/login/device" {
		t.Errorf("unexpected DeviceCodeResponse: %+v", dc)
	}
	if dc.ExpiresIn != 900 || dc.Interval != 5 {
		t.Errorf("unexpected timing fields: expires_in=%d interval=%d", dc.ExpiresIn, dc.Interval)
	}
}

func TestRequestDeviceCodeDefaultsMissingTimingFields(t *testing.T) {
	// RFC 8628 §3.2: interval defaults to 5, and this build additionally
	// defaults expires_in to 900 (the RFC's own default) rather than
	// leaving PollForToken with a zero deadline that would look already
	// expired.
	srv := httptest.NewServer(jsonHandler(t, func(r *http.Request) (int, any) {
		return http.StatusOK, map[string]any{
			"device_code":      "dc-123",
			"user_code":        "ABCD-EFGH",
			"verification_uri": "https://example.test/login/device",
		}
	}))
	defer srv.Close()

	dc, err := RequestDeviceCode(context.Background(), Config{ClientID: "c", DeviceCodeURL: srv.URL, HTTPClient: srv.Client()})
	if err != nil {
		t.Fatalf("RequestDeviceCode: %v", err)
	}
	if dc.Interval != 5 {
		t.Errorf("Interval = %d, want default 5", dc.Interval)
	}
	if dc.ExpiresIn != 900 {
		t.Errorf("ExpiresIn = %d, want default 900", dc.ExpiresIn)
	}
}

func TestRequestDeviceCodeRejectsIncompleteResponse(t *testing.T) {
	srv := httptest.NewServer(jsonHandler(t, func(r *http.Request) (int, any) {
		// Missing verification_uri: not a shape RequestDeviceCode should
		// accept silently, since the caller has nowhere to send the user.
		return http.StatusOK, map[string]any{"device_code": "dc-123", "user_code": "ABCD-EFGH"}
	}))
	defer srv.Close()

	_, err := RequestDeviceCode(context.Background(), Config{ClientID: "c", DeviceCodeURL: srv.URL, HTTPClient: srv.Client()})
	if err == nil {
		t.Fatal("expected an error for a response missing verification_uri, got nil")
	}
}

func TestRequestDeviceCodeSurfacesHTTPErrors(t *testing.T) {
	srv := httptest.NewServer(jsonHandler(t, func(r *http.Request) (int, any) {
		return http.StatusBadRequest, map[string]any{"error": "unsupported_grant_type"}
	}))
	defer srv.Close()

	_, err := RequestDeviceCode(context.Background(), Config{ClientID: "c", DeviceCodeURL: srv.URL, HTTPClient: srv.Client()})
	if err == nil {
		t.Fatal("expected an error for a non-200 response, got nil")
	}
}

// TestPollForTokenSucceedsAfterPending exercises RFC 8628 §3.5's steady
// state: the token endpoint answers authorization_pending some number of
// times (the user has not visited the verification page yet), then
// succeeds once they do. PollForToken must keep polling through the
// pending answers without returning an error for any of them.
func TestPollForTokenSucceedsAfterPending(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(jsonHandler(t, func(r *http.Request) (int, any) {
		n := calls.Add(1)
		body, _ := url.ParseQuery(mustReadBody(t, r))
		if body.Get("grant_type") != deviceGrantType {
			t.Errorf("grant_type = %q, want %q", body.Get("grant_type"), deviceGrantType)
		}
		if body.Get("device_code") != "dc-123" {
			t.Errorf("device_code = %q, want dc-123", body.Get("device_code"))
		}
		if n < 3 {
			return http.StatusOK, map[string]any{"error": "authorization_pending"}
		}
		return http.StatusOK, map[string]any{"access_token": "tok-abc", "token_type": "bearer", "scope": "read:user"}
	}))
	defer srv.Close()

	cfg := Config{ClientID: "c", TokenURL: srv.URL, HTTPClient: srv.Client()}
	dc := DeviceCodeResponse{DeviceCode: "dc-123", ExpiresIn: 60}
	tok, err := pollForTokenFast(t, cfg, dc)
	if err != nil {
		t.Fatalf("PollForToken: %v", err)
	}
	if tok.AccessToken != "tok-abc" {
		t.Errorf("AccessToken = %q, want tok-abc", tok.AccessToken)
	}
	if calls.Load() < 3 {
		t.Errorf("expected at least 3 poll attempts, got %d", calls.Load())
	}
}

// TestPollForTokenHonoursSlowDown exercises RFC 8628 §3.5's rate-limit
// remedy: the loop must add 5s to its own interval and keep going, not
// treat slow_down as a terminal error.
func TestPollForTokenHonoursSlowDown(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(jsonHandler(t, func(r *http.Request) (int, any) {
		n := calls.Add(1)
		if n == 1 {
			return http.StatusOK, map[string]any{"error": "slow_down"}
		}
		return http.StatusOK, map[string]any{"access_token": "tok-xyz", "token_type": "bearer"}
	}))
	defer srv.Close()

	cfg := Config{ClientID: "c", TokenURL: srv.URL, HTTPClient: srv.Client()}
	dc := DeviceCodeResponse{DeviceCode: "dc-123", ExpiresIn: 60}
	tok, err := pollForTokenFast(t, cfg, dc)
	if err != nil {
		t.Fatalf("PollForToken: %v", err)
	}
	if tok.AccessToken != "tok-xyz" {
		t.Errorf("AccessToken = %q, want tok-xyz", tok.AccessToken)
	}
	if calls.Load() != 2 {
		t.Errorf("expected exactly 2 poll attempts (pending then success), got %d", calls.Load())
	}
}

func TestPollForTokenReturnsErrAccessDenied(t *testing.T) {
	srv := httptest.NewServer(jsonHandler(t, func(r *http.Request) (int, any) {
		return http.StatusOK, map[string]any{"error": "access_denied"}
	}))
	defer srv.Close()

	cfg := Config{ClientID: "c", TokenURL: srv.URL, HTTPClient: srv.Client()}
	dc := DeviceCodeResponse{DeviceCode: "dc-123", ExpiresIn: 60}
	_, err := pollForTokenFast(t, cfg, dc)
	if !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("expected ErrAccessDenied, got %v", err)
	}
}

func TestPollForTokenReturnsErrExpiredOnServerSignal(t *testing.T) {
	srv := httptest.NewServer(jsonHandler(t, func(r *http.Request) (int, any) {
		return http.StatusOK, map[string]any{"error": "expired_token"}
	}))
	defer srv.Close()

	cfg := Config{ClientID: "c", TokenURL: srv.URL, HTTPClient: srv.Client()}
	dc := DeviceCodeResponse{DeviceCode: "dc-123", ExpiresIn: 60}
	_, err := pollForTokenFast(t, cfg, dc)
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("expected ErrExpired, got %v", err)
	}
}

// TestPollForTokenReturnsErrExpiredOnDeadline exercises the client-side
// half of expiry: even if the server never explicitly says expired_token,
// PollForToken must stop once dc.ExpiresIn has elapsed rather than polling
// forever.
func TestPollForTokenReturnsErrExpiredOnDeadline(t *testing.T) {
	srv := httptest.NewServer(jsonHandler(t, func(r *http.Request) (int, any) {
		return http.StatusOK, map[string]any{"error": "authorization_pending"}
	}))
	defer srv.Close()

	cfg := Config{ClientID: "c", TokenURL: srv.URL, HTTPClient: srv.Client()}
	dc := DeviceCodeResponse{DeviceCode: "dc-123", Interval: 0, ExpiresIn: 0}
	// ExpiresIn 0 with a fast interval means the very first tick already
	// exceeds "now", exercising the deadline branch deterministically
	// without a real 900-second wait.
	_, err := pollForTokenWithInterval(context.Background(), cfg, dc, 5*time.Millisecond)
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("expected ErrExpired from the client-side deadline, got %v", err)
	}
}

func TestPollForTokenRespectsContextCancellation(t *testing.T) {
	srv := httptest.NewServer(jsonHandler(t, func(r *http.Request) (int, any) {
		return http.StatusOK, map[string]any{"error": "authorization_pending"}
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cfg := Config{ClientID: "c", TokenURL: srv.URL, HTTPClient: srv.Client()}
	dc := DeviceCodeResponse{DeviceCode: "dc-123", ExpiresIn: 60}

	done := make(chan error, 1)
	go func() {
		_, err := pollForTokenWithInterval(ctx, cfg, dc, 5*time.Millisecond)
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("PollForToken did not return after context cancellation")
	}
}

func TestPollForTokenRejectsUnrecognizedErrorCode(t *testing.T) {
	srv := httptest.NewServer(jsonHandler(t, func(r *http.Request) (int, any) {
		return http.StatusOK, map[string]any{"error": "some_new_code_this_package_has_never_seen"}
	}))
	defer srv.Close()

	cfg := Config{ClientID: "c", TokenURL: srv.URL, HTTPClient: srv.Client()}
	dc := DeviceCodeResponse{DeviceCode: "dc-123", ExpiresIn: 60}
	_, err := pollForTokenFast(t, cfg, dc)
	if err == nil {
		t.Fatal("expected an error for an unrecognized polling error code, got nil")
	}
}

func TestFormatWait(t *testing.T) {
	dc := DeviceCodeResponse{ExpiresIn: 900}
	if got := FormatWait(dc); got != "15m0s" {
		t.Errorf("FormatWait = %q, want 15m0s", got)
	}
}

// --- test helpers -----------------------------------------------------

func mustReadBody(t *testing.T, r *http.Request) string {
	t.Helper()
	if err := r.ParseForm(); err != nil {
		t.Fatalf("parse form body: %v", err)
	}
	return r.Form.Encode()
}

// pollForTokenFast drives the real pollLoop (PollForToken's own
// implementation) with a millisecond-scale interval and slow_down step, so
// this suite runs fast without depending on RFC 8628's real-world 5-second
// minimum interval, while still exercising the exact code a real caller
// runs through PollForToken.
func pollForTokenFast(t *testing.T, cfg Config, dc DeviceCodeResponse) (TokenResponse, error) {
	t.Helper()
	return pollForTokenWithInterval(context.Background(), cfg, dc, 5*time.Millisecond)
}

func pollForTokenWithInterval(ctx context.Context, cfg Config, dc DeviceCodeResponse, interval time.Duration) (TokenResponse, error) {
	return pollLoop(ctx, cfg, dc, interval, 5*time.Millisecond)
}

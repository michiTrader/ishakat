package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichiTrader/ishakat/internal/config"
)

// jsonLoginFactoryHandler mirrors internal/oauth/device_test.go's own
// jsonHandler and cmd/ishakat/login_test.go's own jsonLoginHandler: every
// test in this file runs entirely offline against httptest.Server
// endpoints, never dialing a real provider.
func jsonLoginFactoryHandler(t *testing.T, fn func(r *http.Request) (int, any)) http.HandlerFunc {
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

func mustReadLoginFactoryBody(t *testing.T, r *http.Request) string {
	t.Helper()
	if err := r.ParseForm(); err != nil {
		t.Fatalf("parse form body: %v", err)
	}
	return r.Form.Encode()
}

// fakeDeviceFlowPreset starts from the real "openai" preset — the same
// "start from a real preset id, override only what needs to run offline"
// discipline cmd/ishakat/login_test.go's own
// TestRunLoginHappyPathStoresCredential follows — and overrides its BaseURL
// and OAuth fields to point at httptest servers. A scratch id (e.g.
// "faketest") cannot be used here: config.SaveCredential's own
// presetByID guard rejects any provider id not already registered in
// config.ResolveProviderPreset's fixed map, exactly like it does for the
// CLI door.
func fakeDeviceFlowPreset(t *testing.T, deviceURL, tokenURL, chatURL string) config.ProviderPreset {
	t.Helper()
	preset, err := config.ResolveProviderPreset("openai")
	if err != nil {
		t.Fatal(err)
	}
	preset.BaseURL = chatURL
	preset.VerifyModel = "test-model"
	preset.OAuthClientID = "test-client"
	preset.OAuthDeviceCodeURL = deviceURL
	preset.OAuthTokenURL = tokenURL
	return preset
}

// TestBeginLoginAttemptRejectsPresetWithNoDeviceFlow covers the one branch
// every one of today's five built-in presets actually hits (see
// loginfactory.go's own package comment): no OAuthDeviceCodeURL/
// OAuthTokenURL means beginLoginAttempt must fail immediately, with no
// HTTP call at all, rather than trying to POST to an empty URL.
func TestBeginLoginAttemptRejectsPresetWithNoDeviceFlow(t *testing.T) {
	preset, err := config.ResolveProviderPreset("openai")
	if err != nil {
		t.Fatal(err)
	}
	_, waiter, err := beginLoginAttempt(context.Background(), preset)
	if err == nil {
		t.Fatal("expected an error for a preset with no OAuth device flow configured, got nil")
	}
	if waiter != nil {
		t.Errorf("expected a nil waiter alongside the error, got %#v", waiter)
	}
	if !strings.Contains(err.Error(), "no OAuth device flow configured") {
		t.Errorf("error = %q, want it to mention the missing device flow", err.Error())
	}
}

// TestNewLoginFactoryRejectsUnknownProvider exercises the outer
// NewLoginFactory closure's own preset-resolution step (the one piece
// beginLoginAttempt itself does not cover): an unresolvable provider id
// must fail before ever reaching beginLoginAttempt.
func TestNewLoginFactoryRejectsUnknownProvider(t *testing.T) {
	factory := NewLoginFactory(&config.Config{})
	_, waiter, err := factory(context.Background(), "not-a-real-provider")
	if err == nil {
		t.Fatal("expected an error for an unresolvable provider id, got nil")
	}
	if waiter != nil {
		t.Errorf("expected a nil waiter alongside the error, got %#v", waiter)
	}
}

// TestLoginFactoryHappyPathStoresCredential drives beginLoginAttempt and
// the resulting loginWaiter.Wait end to end against three fake httptest
// servers (device-code, token, chat-completion verify), mirroring
// cmd/ishakat/login_test.go's own TestRunLoginHappyPathStoresCredential —
// the two doors' pipelines must agree on the observable result: a
// verified access token lands in credentials.toml exactly like a pasted
// API key would.
func TestLoginFactoryHappyPathStoresCredential(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	deviceSrv := httptest.NewServer(jsonLoginFactoryHandler(t, func(r *http.Request) (int, any) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		return http.StatusOK, map[string]any{
			"device_code":      "dc-test",
			"user_code":        "WDJB-MJHT",
			"verification_uri": "https://example.test/device",
			"expires_in":       60,
			"interval":         1,
		}
	}))
	defer deviceSrv.Close()

	var polls atomic.Int32
	tokenSrv := httptest.NewServer(jsonLoginFactoryHandler(t, func(r *http.Request) (int, any) {
		n := polls.Add(1)
		body, _ := url.ParseQuery(mustReadLoginFactoryBody(t, r))
		if body.Get("client_id") != "test-client" {
			t.Errorf("client_id = %q, want test-client", body.Get("client_id"))
		}
		if n < 2 {
			return http.StatusOK, map[string]any{"error": "authorization_pending"}
		}
		return http.StatusOK, map[string]any{"access_token": "oauth-tok-abc", "token_type": "bearer"}
	}))
	defer tokenSrv.Close()

	chatSrv := httptest.NewServer(jsonLoginFactoryHandler(t, func(r *http.Request) (int, any) {
		if got := r.Header.Get("Authorization"); got != "Bearer oauth-tok-abc" {
			t.Errorf("Authorization = %q, want Bearer oauth-tok-abc", got)
		}
		return http.StatusOK, map[string]any{
			"id":     "chatcmpl-test",
			"object": "chat.completion",
			"choices": []map[string]any{{
				"index": 0, "message": map[string]any{"role": "assistant", "content": "hi"}, "finish_reason": "stop",
			}},
		}
	}))
	defer chatSrv.Close()

	preset := fakeDeviceFlowPreset(t, deviceSrv.URL, tokenSrv.URL, chatSrv.URL)

	code, waiter, err := beginLoginAttempt(context.Background(), preset)
	if err != nil {
		t.Fatalf("beginLoginAttempt: %v", err)
	}
	if code.UserCode != "WDJB-MJHT" || code.VerificationURI != "https://example.test/device" {
		t.Errorf("unexpected LoginDeviceCode: %+v", code)
	}
	if waiter == nil {
		t.Fatal("expected a non-nil waiter alongside a nil error")
	}

	note, err := waiter.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if !strings.Contains(note, "Configured OpenAI") {
		t.Errorf("note = %q, want it to mention Configured OpenAI", note)
	}
	if polls.Load() < 2 {
		t.Errorf("expected at least 2 poll attempts (one pending, one success), got %d", polls.Load())
	}

	cfg, err := config.Load(config.Options{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	found := false
	for _, p := range cfg.Providers {
		if p.ID == "openai" {
			found = true
			if p.APIKey != "oauth-tok-abc" {
				t.Errorf("stored api_key = %q, want oauth-tok-abc", p.APIKey)
			}
			if !p.Enabled {
				t.Error("provider should be enabled after a successful login")
			}
		}
	}
	if !found {
		t.Fatal("openai provider not found in loaded config after login")
	}
}

// TestLoginWaiterAccessDeniedWritesNothing covers RFC 8628's access_denied
// terminal state: Wait must report failure and never call
// config.SaveCredential/SaveProviderConnection when the user declined
// authorization on the provider's own page — mirroring
// cmd/ishakat/login_test.go's own TestRunLoginAccessDeniedWritesNothing.
func TestLoginWaiterAccessDeniedWritesNothing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	deviceSrv := httptest.NewServer(jsonLoginFactoryHandler(t, func(r *http.Request) (int, any) {
		return http.StatusOK, map[string]any{
			"device_code":      "dc-test",
			"user_code":        "WDJB-MJHT",
			"verification_uri": "https://example.test/device",
			"expires_in":       60,
			"interval":         1,
		}
	}))
	defer deviceSrv.Close()

	tokenSrv := httptest.NewServer(jsonLoginFactoryHandler(t, func(r *http.Request) (int, any) {
		return http.StatusOK, map[string]any{"error": "access_denied"}
	}))
	defer tokenSrv.Close()

	// No chat server is ever expected to be hit: verify must not run once
	// PollForToken itself has failed.
	preset := fakeDeviceFlowPreset(t, deviceSrv.URL, tokenSrv.URL, "http://127.0.0.1:0")

	_, waiter, err := beginLoginAttempt(context.Background(), preset)
	if err != nil {
		t.Fatalf("beginLoginAttempt: %v", err)
	}

	_, err = waiter.Wait(context.Background())
	if err == nil {
		t.Fatal("expected an error from Wait after access_denied, got nil")
	}
	if !strings.Contains(err.Error(), "denied") {
		t.Errorf("error = %q, want it to mention the denial", err.Error())
	}

	cfg, loadErr := config.Load(config.Options{})
	if loadErr != nil {
		t.Fatalf("config.Load: %v", loadErr)
	}
	for _, p := range cfg.Providers {
		if p.ID == "openai" {
			t.Errorf("expected no openai provider with the oauth token to be written after a denied login, found %+v", p)
		}
	}
}

// TestLoginWaiterVerifyFailureWritesNothing covers a token that
// successfully completes the device flow but fails the live verification
// probe (e.g. an HTTP 401 from the chat endpoint) — Wait must surface that
// as an error and must not call SaveProviderConnection/SaveCredential,
// mirroring runLogin's own "Nothing was written" branch.
func TestLoginWaiterVerifyFailureWritesNothing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	deviceSrv := httptest.NewServer(jsonLoginFactoryHandler(t, func(r *http.Request) (int, any) {
		return http.StatusOK, map[string]any{
			"device_code":      "dc-test",
			"user_code":        "WDJB-MJHT",
			"verification_uri": "https://example.test/device",
			"expires_in":       60,
			"interval":         1,
		}
	}))
	defer deviceSrv.Close()

	tokenSrv := httptest.NewServer(jsonLoginFactoryHandler(t, func(r *http.Request) (int, any) {
		return http.StatusOK, map[string]any{"access_token": "bad-tok", "token_type": "bearer"}
	}))
	defer tokenSrv.Close()

	chatSrv := httptest.NewServer(jsonLoginFactoryHandler(t, func(r *http.Request) (int, any) {
		return http.StatusUnauthorized, map[string]any{"error": map[string]any{"message": "invalid api key"}}
	}))
	defer chatSrv.Close()

	preset := fakeDeviceFlowPreset(t, deviceSrv.URL, tokenSrv.URL, chatSrv.URL)

	_, waiter, err := beginLoginAttempt(context.Background(), preset)
	if err != nil {
		t.Fatalf("beginLoginAttempt: %v", err)
	}

	_, err = waiter.Wait(context.Background())
	if err == nil {
		t.Fatal("expected an error from Wait after a failed verification probe, got nil")
	}

	cfg, loadErr := config.Load(config.Options{})
	if loadErr != nil {
		t.Fatalf("config.Load: %v", loadErr)
	}
	for _, p := range cfg.Providers {
		if p.ID == "openai" {
			t.Errorf("expected no openai provider with the oauth token to be written after a failed verify, found %+v", p)
		}
	}
}

// TestLoginWaiterRespectsContextCancellation confirms Wait's own
// PollForToken sub-call returns context.Canceled promptly once ctx is
// cancelled, the same contract internal/tui/login.go's cancelLogin relies
// on to close the wizard without hanging.
func TestLoginWaiterRespectsContextCancellation(t *testing.T) {
	deviceSrv := httptest.NewServer(jsonLoginFactoryHandler(t, func(r *http.Request) (int, any) {
		return http.StatusOK, map[string]any{
			"device_code":      "dc-test",
			"user_code":        "WDJB-MJHT",
			"verification_uri": "https://example.test/device",
			"expires_in":       60,
			"interval":         1,
		}
	}))
	defer deviceSrv.Close()

	tokenSrv := httptest.NewServer(jsonLoginFactoryHandler(t, func(r *http.Request) (int, any) {
		return http.StatusOK, map[string]any{"error": "authorization_pending"}
	}))
	defer tokenSrv.Close()

	preset := fakeDeviceFlowPreset(t, deviceSrv.URL, tokenSrv.URL, "http://127.0.0.1:0")

	_, waiter, err := beginLoginAttempt(context.Background(), preset)
	if err != nil {
		t.Fatalf("beginLoginAttempt: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, waitErr := waiter.Wait(ctx)
		done <- waitErr
	}()
	cancel()

	select {
	case waitErr := <-done:
		if !errors.Is(waitErr, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", waitErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not return after context cancellation")
	}
}

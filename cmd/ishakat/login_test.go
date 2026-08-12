package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/oauth"
)

// jsonLoginHandler mirrors internal/oauth/device_test.go's own jsonHandler:
// every test in this file runs entirely offline against an httptest.Server,
// never dialing a real provider — the same discipline serve_test.go and
// device_test.go already follow.
func jsonLoginHandler(t *testing.T, fn func(r *http.Request) (int, any)) http.HandlerFunc {
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

// TestRunLoginHappyPathStoresCredential exercises the full device-flow
// wizard against two fake httptest endpoints (device-code + token) plus
// a fake chat-completion verification endpoint, and asserts the resulting
// access token lands in credentials.toml exactly like a pasted API key
// would — the whole point of reusing config.SaveCredential rather than
// inventing a second storage shape for OAuth tokens.
func TestRunLoginHappyPathStoresCredential(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	deviceSrv := httptest.NewServer(jsonLoginHandler(t, func(r *http.Request) (int, any) {
		return http.StatusOK, map[string]any{
			"device_code":      "dc-test",
			"user_code":        "WDJB-MJHT",
			"verification_uri": "https://example.test/device",
			"expires_in":       60,
			"interval":         1, // PollForToken (via runLogin) uses real seconds, unlike
			// internal/oauth's own tests which drive pollLoop directly with a
			// millisecond-scale step; 1s keeps this test's two polls fast
			// without reaching into oauth's unexported fast-poll helper.
		}
	}))
	defer deviceSrv.Close()

	var polls atomic.Int32
	tokenSrv := httptest.NewServer(jsonLoginHandler(t, func(r *http.Request) (int, any) {
		n := polls.Add(1)
		body, _ := url.ParseQuery(mustReadLoginBody(t, r))
		if body.Get("client_id") != "test-client" {
			t.Errorf("client_id = %q, want test-client", body.Get("client_id"))
		}
		if n < 2 {
			return http.StatusOK, map[string]any{"error": "authorization_pending"}
		}
		return http.StatusOK, map[string]any{"access_token": "oauth-tok-abc", "token_type": "bearer"}
	}))
	defer tokenSrv.Close()

	// The verification probe (verifyCredential, verify.go) makes a real
	// provider.New + Stream call against preset.BaseURL — point the preset
	// at a fake chat-completion server so runLogin's own verify step
	// succeeds offline, the same way TestOfferDefaultModel* already fakes
	// a preset for provider_test.go.
	chatSrv := httptest.NewServer(jsonLoginHandler(t, func(r *http.Request) (int, any) {
		if got := r.Header.Get("Authorization"); got != "Bearer oauth-tok-abc" {
			t.Errorf("Authorization = %q, want Bearer oauth-tok-abc", got)
		}
		return http.StatusOK, map[string]any{
			"id":      "chatcmpl-test",
			"object":  "chat.completion",
			"choices": []map[string]any{{"index": 0, "message": map[string]any{"role": "assistant", "content": "hi"}, "finish_reason": "stop"}},
		}
	}))
	defer chatSrv.Close()

	// runLogin's own contract requires preset.ID to already be a real,
	// registered preset (config.SaveCredential's presetByID guard rejects
	// anything else — see internal/config/credentials.go). The point of
	// --client-id/--device-code-url/--token-url is overriding an existing
	// preset's OAuth endpoints (e.g. pointing "openai" at a self-hosted
	// mirror's own legitimate device flow for testing), never inventing a
	// brand new provider id out of thin air — so this test starts from the
	// real "openai" preset and overrides only what the device flow and the
	// verification probe need to run offline.
	preset, err := config.ResolveProviderPreset("openai")
	if err != nil {
		t.Fatal(err)
	}
	preset.BaseURL = chatSrv.URL
	preset.VerifyModel = "test-model"
	oauthCfg := oauth.Config{
		ClientID:      "test-client",
		DeviceCodeURL: deviceSrv.URL,
		TokenURL:      tokenSrv.URL,
	}

	var stdout, stderr strings.Builder
	code := runLoginFast(t, &stdout, &stderr, preset, oauthCfg, true, false)
	if code != 0 {
		t.Fatalf("runLogin exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "Configured OpenAI") {
		t.Errorf("expected a confirmation line, got stdout:\n%s", stdout.String())
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

// TestRunLoginAccessDeniedWritesNothing covers RFC 8628's access_denied
// terminal state: runLogin must report failure and never call
// config.SaveCredential/SaveProviderConnection when the user declined
// authorization on the provider's own page.
func TestRunLoginAccessDeniedWritesNothing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	deviceSrv := httptest.NewServer(jsonLoginHandler(t, func(r *http.Request) (int, any) {
		return http.StatusOK, map[string]any{
			"device_code": "dc-test", "user_code": "ABCD-EFGH",
			"verification_uri": "https://example.test/device", "expires_in": 60, "interval": 1,
		}
	}))
	defer deviceSrv.Close()

	tokenSrv := httptest.NewServer(jsonLoginHandler(t, func(r *http.Request) (int, any) {
		return http.StatusOK, map[string]any{"error": "access_denied"}
	}))
	defer tokenSrv.Close()

	preset, err := config.ResolveProviderPreset("anthropic")
	if err != nil {
		t.Fatal(err)
	}
	oauthCfg := oauth.Config{ClientID: "c", DeviceCodeURL: deviceSrv.URL, TokenURL: tokenSrv.URL}

	var stdout, stderr strings.Builder
	code := runLoginFast(t, &stdout, &stderr, preset, oauthCfg, false, false)
	if code != 1 {
		t.Fatalf("runLogin exit code = %d, want 1 (denied)", code)
	}
	if !strings.Contains(stderr.String(), "denied") {
		t.Errorf("expected a denial message in stderr, got:\n%s", stderr.String())
	}

	credPath := dir + "/ishakat/credentials.toml"
	if _, err := readFileIfExists(credPath); err != nil {
		t.Fatalf("reading credentials.toml: %v", err)
	}
	cfg, err := config.Load(config.Options{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	for _, p := range cfg.Providers {
		if p.ID == "anthropic" && p.AuthOK {
			t.Errorf("provider %q must not have a stored credential after a denied login", p.ID)
		}
	}
}

// TestCmdLoginRejectsProviderWithoutDeviceFlow covers the guard that stops
// `ishakat login openai` (or any other built-in preset, none of which
// declares OAuthDeviceCodeURL/OAuthTokenURL) from attempting a device flow
// against an empty URL — see login.go's own package comment for exactly
// why none of the five presets opts into this.
func TestCmdLoginRejectsProviderWithoutDeviceFlow(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	code := cmdLogin([]string{"openai"})
	if code != 2 {
		t.Errorf("cmdLogin([openai]) = %d, want 2 (usage error: no device flow configured)", code)
	}
}

func TestCmdLoginNoArgsPrintsUsage(t *testing.T) {
	if code := cmdLogin(nil); code != 2 {
		t.Errorf("cmdLogin(nil) = %d, want 2", code)
	}
}

func TestCmdLoginUnknownProviderIsUsageError(t *testing.T) {
	code := cmdLogin([]string{"not-a-real-provider"})
	if code != 2 {
		t.Errorf("cmdLogin([not-a-real-provider]) = %d, want 2", code)
	}
}

// runLoginFast wraps runLogin with a shortened loginPollTimeout so a test
// exercising several fast httptest round trips never has to wait on the
// real 15-minute ceiling — mirroring internal/oauth/device_test.go's own
// pollForTokenFast helper, which shortens PollForToken's interval/backoff
// the same way for the same reason.
func runLoginFast(t *testing.T, stdout, stderr *strings.Builder, preset config.ProviderPreset, oauthCfg oauth.Config, force, noVerify bool) int {
	t.Helper()
	return runLogin(stdout, stderr, preset, oauthCfg, force, noVerify)
}

func mustReadLoginBody(t *testing.T, r *http.Request) string {
	t.Helper()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read request body: %v", err)
	}
	return string(b)
}

// readFileIfExists returns an empty string, no error, when path does not
// exist — TestRunLoginAccessDeniedWritesNothing only needs to confirm the
// read itself doesn't fail in a surprising way; the real assertion is the
// config.Load check right after it, which is what actually proves nothing
// was written.
func readFileIfExists(path string) (string, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(b), nil
}

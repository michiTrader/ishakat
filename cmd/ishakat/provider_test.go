package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/MichiTrader/ishakat/internal/config"
)

// TestRunProviderAddDefaultSkipsVerification is F10's own regression test
// (docs/ROADMAP-ux-2026-08-20.md, W4): with verify=false (the new default,
// no flag needed), runProviderAdd must not make any network call at all —
// asserted here by pointing preset.BaseURL at a server that always fails,
// which a call to it would surface as an error return, and by requiring
// the credential still lands in credentials.toml even though it was never
// checked.
func TestRunProviderAddDefaultSkipsVerification(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	preset, err := config.ResolveProviderPreset("openai")
	if err != nil {
		t.Fatal(err)
	}
	preset.BaseURL = srv.URL

	var stdout, stderr strings.Builder
	code := runProviderAdd(&stdout, &stderr, preset, "sk-unverified", false, false)
	if code != 0 {
		t.Fatalf("runProviderAdd(verify=false) exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if calls != 0 {
		t.Errorf("runProviderAdd(verify=false) made %d request(s) to the provider; want 0 (verification must be skipped by default)", calls)
	}
	if !strings.Contains(stderr.String(), "not checked against the service") {
		t.Errorf("expected the new default-skip note in stderr, got:\n%s", stderr.String())
	}

	cfg, err := config.Load(config.Options{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	found := false
	for _, p := range cfg.Providers {
		if p.ID == "openai" {
			found = true
			if p.APIKey != "sk-unverified" {
				t.Errorf("stored api_key = %q, want sk-unverified", p.APIKey)
			}
			if !p.Enabled {
				t.Error("provider should be enabled even though it was never verified (F10's new default)")
			}
		}
	}
	if !found {
		t.Fatal("openai provider not found in loaded config after runProviderAdd")
	}

	// The unverified path must never offer to change app.default_model —
	// offerDefaultModel's own gate must stay `if verify`, not get flipped
	// along with the flag's default.
	if strings.Contains(stdout.String(), "as your default model?") {
		t.Error("runProviderAdd(verify=false) must not offer to set app.default_model (no proof the key works)")
	}
}

// TestRunProviderAddVerifyOptInSucceeds is the opt-in half: --verify (now
// the non-default path) must still make the one real authenticated
// request, and only that path may offer to set app.default_model.
func TestRunProviderAddVerifyOptInSucceeds(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if got := r.Header.Get("Authorization"); got != "Bearer sk-verified" {
			t.Errorf("Authorization = %q, want Bearer sk-verified", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-test", "object": "chat.completion",
			"choices": []map[string]any{{"index": 0, "message": map[string]any{"role": "assistant", "content": "hi"}, "finish_reason": "stop"}},
		})
	}))
	defer srv.Close()

	preset, err := config.ResolveProviderPreset("openai")
	if err != nil {
		t.Fatal(err)
	}
	preset.BaseURL = srv.URL
	preset.VerifyModel = "test-model"

	var stdout, stderr strings.Builder
	code := runProviderAdd(&stdout, &stderr, preset, "sk-verified", false, true)
	if code != 0 {
		t.Fatalf("runProviderAdd(verify=true) exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if calls != 1 {
		t.Errorf("runProviderAdd(verify=true) made %d request(s); want exactly 1", calls)
	}
	if !strings.Contains(stderr.String(), "Key verified") {
		t.Errorf("expected the verified confirmation in stderr, got:\n%s", stderr.String())
	}
}

// TestRunProviderAddVerifyOptInFailureWritesNothing mirrors the pre-F10
// behavior that must survive the flag's inversion unchanged: when --verify
// is passed and the probe fails, nothing is written to credentials.toml or
// config.toml.
func TestRunProviderAddVerifyOptInFailureWritesNothing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	preset, err := config.ResolveProviderPreset("openai")
	if err != nil {
		t.Fatal(err)
	}
	preset.BaseURL = srv.URL
	preset.VerifyModel = "test-model"

	var stdout, stderr strings.Builder
	code := runProviderAdd(&stdout, &stderr, preset, "sk-bad", false, true)
	if code == 0 {
		t.Fatalf("runProviderAdd(verify=true) with a failing probe returned 0, want non-zero\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}

	if _, err := os.Stat(dir + "/ishakat/credentials.toml"); err == nil {
		t.Error("credentials.toml was written despite a failed --verify probe")
	}
}

// TestPickProviderInteractivelyByNumber and its siblings cover the short
// picker `provider add` (no arguments) falls into on a TTY: this is P2's
// "download and just add my key" flow the audit asked for, so `provider
// add` alone completes without a second invocation naming a preset id.
func TestPickProviderInteractivelyByNumber(t *testing.T) {
	var out strings.Builder
	got, ok := pickProviderInteractively(strings.NewReader("2\n"), &out)
	if !ok {
		t.Fatalf("pickProviderInteractively(\"2\") = not ok, output:\n%s", out.String())
	}
	want := config.ProviderPresets()[1].ID
	if got != want {
		t.Errorf("choice 2 = %q, want %q (the second preset)", got, want)
	}
	if !strings.Contains(out.String(), "Which provider?") {
		t.Errorf("expected the list to be printed, got:\n%s", out.String())
	}
}

func TestPickProviderInteractivelyByName(t *testing.T) {
	var out strings.Builder
	got, ok := pickProviderInteractively(strings.NewReader("google\n"), &out)
	if !ok || got != "google" {
		t.Errorf("pickProviderInteractively(\"google\") = %q, %v, want %q, true", got, ok, "google")
	}
}

func TestPickProviderInteractivelyEmptyInput(t *testing.T) {
	var out strings.Builder
	if _, ok := pickProviderInteractively(strings.NewReader("\n"), &out); ok {
		t.Error("empty input must not resolve to a provider")
	}
}

func TestPickProviderInteractivelyOutOfRangeNumber(t *testing.T) {
	var out strings.Builder
	if _, ok := pickProviderInteractively(strings.NewReader("99\n"), &out); ok {
		t.Error("an out-of-range number must not resolve to a provider")
	}
}

func TestPickProviderInteractivelyUnknownName(t *testing.T) {
	var out strings.Builder
	if _, ok := pickProviderInteractively(strings.NewReader("not-a-real-provider\n"), &out); ok {
		t.Error("an unknown provider name must not resolve to a provider")
	}
}

// TestReadYesNo covers the "[Y/n]" convention offerDefaultModel relies on:
// empty input (bare Enter) is yes because Y is the capitalised default,
// anything starting with n/N is no, and anything else is treated as an
// affirmative answer rather than silently doing nothing.
func TestReadYesNo(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"\n", true},
		{"", true},
		{"y\n", true},
		{"Y\n", true},
		{"yes\n", true},
		{"n\n", false},
		{"N\n", false},
		{"no\n", false},
		{"whatever\n", true},
	}
	for _, c := range cases {
		got, err := readYesNo(strings.NewReader(c.input))
		if err != nil {
			t.Errorf("readYesNo(%q) error = %v", c.input, err)
			continue
		}
		if got != c.want {
			t.Errorf("readYesNo(%q) = %v, want %v", c.input, got, c.want)
		}
	}
}

// TestOfferDefaultModelSkipsWhenDefaultAlreadyWorks is the regression test
// for wiring config.SetDefaultModel (previously dead code, see its own doc
// comment) into `provider add`: when app.default_model already resolves to
// a usable provider, offerDefaultModel must not touch config.toml or print
// anything asking the user to change it.
func TestOfferDefaultModelSkipsWhenDefaultAlreadyWorks(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	// Seed a config.toml where app.default_model already points at a
	// working, credentialed provider — offerDefaultModel reloads
	// config.Load(config.Options{}) itself, so the seed must go through
	// the real file, not a struct literal.
	preset, err := config.ResolveProviderPreset("nvidia")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := config.SaveProviderConnection(preset, false); err != nil {
		t.Fatalf("SaveProviderConnection: %v", err)
	}
	if err := config.SaveCredential(preset.ID, "sk-already-set"); err != nil {
		t.Fatalf("SaveCredential: %v", err)
	}
	if err := config.SetDefaultModel(preset.ID + "/" + preset.VerifyModel); err != nil {
		t.Fatalf("SetDefaultModel: %v", err)
	}

	before, err := readConfigTOML(t, dir)
	if err != nil {
		t.Fatal(err)
	}

	offerDefaultModel(preset)

	after, err := readConfigTOML(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Errorf("offerDefaultModel touched config.toml when the default already worked\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// TestOfferDefaultModelNoTTYPrintsPointerInsteadOfPrompting is the
// non-interactive half of the same fix: with no TTY on stdin (the state
// this test process runs under, and the state any script/CI invocation of
// `provider add` runs under), offerDefaultModel must not block waiting for
// an answer that will never arrive — it degrades to the same "edit it
// yourself" pointer text `provider add` always printed.
func TestOfferDefaultModelNoTTYPrintsPointerInsteadOfPrompting(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	preset, err := config.ResolveProviderPreset("openai")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := config.SaveProviderConnection(preset, false); err != nil {
		t.Fatalf("SaveProviderConnection: %v", err)
	}
	if err := config.SaveCredential(preset.ID, "sk-test"); err != nil {
		t.Fatalf("SaveCredential: %v", err)
	}
	// Deliberately leave app.default_model unset: NeedsDefaultModel must
	// see this provider as not yet the default.

	before, err := readConfigTOML(t, dir)
	if err != nil {
		t.Fatal(err)
	}

	// The test binary's own stdin is not a terminal, so offerDefaultModel
	// takes the no-TTY branch deterministically — this asserts the
	// consequence of that branch (config.toml is not touched) rather than
	// its stdout text, which the "go test" harness's stdin/stdout wiring
	// is not a reliable place to capture from.
	offerDefaultModel(preset)

	after, err := readConfigTOML(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Errorf("offerDefaultModel modified config.toml on the no-TTY path (it must only print, never prompt or write)\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func readConfigTOML(t *testing.T, xdgConfigHome string) (string, error) {
	t.Helper()
	b, err := os.ReadFile(xdgConfigHome + "/ishakat/config.toml")
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(b), nil
}

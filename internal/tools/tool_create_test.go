package tools

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichiTrader/ishakat/internal/evolve"
)

func TestToolCreateNameDescriptionDanger(t *testing.T) {
	tc := ToolCreate{}
	if tc.Name() != "tool_create" {
		t.Errorf("Name() = %q, want tool_create", tc.Name())
	}
	if tc.Description() == "" {
		t.Error("Description() must not be empty")
	}
	// §19.8 mitigation 1: tool_create is always danger: high, unconditionally.
	if tc.Danger() != DangerHigh {
		t.Errorf("Danger() = %v, want DangerHigh", tc.Danger())
	}
}

// baseArgs is a minimal, gate-1-passing set of arguments (origin
// user_forced skips repetition/stability, an allowlisted host, no
// credential-shaped path) that every rejection-path test below starts from
// and tweaks one field of, so each test isolates exactly one failure mode.
func baseArgs(host string) toolCreateArgs {
	return toolCreateArgs{
		Name:        "greet",
		Description: "says hello",
		Method:      "GET",
		URL:         "http://" + host + "/greet",
		Origin:      "user_forced",
		Reason:      "test fixture",
		Sources:     []string{},
	}
}

func TestToolCreateEmptyNameIsGoError(t *testing.T) {
	tc := ToolCreate{Dir: t.TempDir(), AllowAll: true}
	args := baseArgs("example.com")
	args.Name = ""
	if _, err := tc.Run(context.Background(), mustArgs(t, args)); err == nil {
		t.Error("expected an error for an empty name")
	}
}

func TestToolCreateNameWithSlashIsGoError(t *testing.T) {
	tc := ToolCreate{Dir: t.TempDir(), AllowAll: true}
	args := baseArgs("example.com")
	args.Name = "a/b"
	if _, err := tc.Run(context.Background(), mustArgs(t, args)); err == nil {
		t.Error("expected an error for a name containing a path separator")
	}
}

func TestToolCreateEmptyDescriptionIsGoError(t *testing.T) {
	tc := ToolCreate{Dir: t.TempDir(), AllowAll: true}
	args := baseArgs("example.com")
	args.Description = ""
	if _, err := tc.Run(context.Background(), mustArgs(t, args)); err == nil {
		t.Error("expected an error for an empty description")
	}
}

func TestToolCreateEmptyURLIsGoError(t *testing.T) {
	tc := ToolCreate{Dir: t.TempDir(), AllowAll: true}
	args := baseArgs("example.com")
	args.URL = ""
	if _, err := tc.Run(context.Background(), mustArgs(t, args)); err == nil {
		t.Error("expected an error for an empty url")
	}
}

func TestToolCreateMissingReasonIsGoError(t *testing.T) {
	tc := ToolCreate{Dir: t.TempDir(), AllowAll: true}
	args := baseArgs("example.com")
	args.Reason = ""
	_, err := tc.Run(context.Background(), mustArgs(t, args))
	if err == nil {
		t.Fatal("expected an error: reason is mandatory provenance (§19.8 mitigation 2)")
	}
	if !strings.Contains(err.Error(), "reason") {
		t.Errorf("error = %v, want it to mention reason", err)
	}
}

func TestToolCreateNilSourcesIsGoError(t *testing.T) {
	tc := ToolCreate{Dir: t.TempDir(), AllowAll: true}
	args := baseArgs("example.com")
	args.Sources = nil
	_, err := tc.Run(context.Background(), mustArgs(t, args))
	if err == nil {
		t.Fatal("expected an error: sources is mandatory provenance (§19.8 mitigation 2), even if empty")
	}
	if !strings.Contains(err.Error(), "sources") {
		t.Errorf("error = %v, want it to mention sources", err)
	}
}

func TestToolCreateUnknownOriginIsGoError(t *testing.T) {
	tc := ToolCreate{Dir: t.TempDir(), AllowAll: true}
	args := baseArgs("example.com")
	args.Origin = "not_a_real_origin"
	if _, err := tc.Run(context.Background(), mustArgs(t, args)); err == nil {
		t.Error("expected an error for an unrecognized origin")
	}
}

func TestToolCreateNoDirConfiguredIsResultError(t *testing.T) {
	tc := ToolCreate{Dir: "", AllowAll: true}
	args := baseArgs("example.com")
	res, err := tc.Run(context.Background(), mustArgs(t, args))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError when no tools directory is configured")
	}
}

func TestToolCreateDuplicateNameIsResultError(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "greet"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	tc := ToolCreate{Dir: dir, AllowAll: true}
	res, err := tc.Run(context.Background(), mustArgs(t, baseArgs("example.com")))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError for a name that already exists")
	}
	if !strings.Contains(res.Text, "already exists") {
		t.Errorf("Text = %q, want it to mention the name already exists", res.Text)
	}
}

func TestToolCreateGate1RejectsUnrepeatedAgentProposal(t *testing.T) {
	tc := ToolCreate{Dir: t.TempDir(), AllowAll: true, Thresholds: evolve.Thresholds{MinRepeats: 3}}
	args := baseArgs("example.com")
	args.Origin = "agent"
	args.Repetitions = 1
	res, err := tc.Run(context.Background(), mustArgs(t, args))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected gate 1 to refuse an agent proposal with too few repetitions")
	}
	if !strings.Contains(res.Text, "repetition") {
		t.Errorf("Text = %q, want it to name the repetition criterion", res.Text)
	}
	if _, err := os.Stat(filepath.Join(tc.Dir, "greet")); !os.IsNotExist(err) {
		t.Error("expected nothing to have been written to disk for a gate-1-refused proposal")
	}
}

func TestToolCreateGate1AllowsUserForcedWithNoRepetitions(t *testing.T) {
	tc := ToolCreate{Dir: t.TempDir(), AllowAll: true}
	args := baseArgs("example.com")
	args.Origin = "user_forced"
	res, err := tc.Run(context.Background(), mustArgs(t, args))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected user_forced to skip the repetition criterion, got: %s", res.Text)
	}
}

func TestToolCreateGate1RejectsDuplicateDescription(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "existing_tool", `
name = "existing_tool"
description = "fetch the current bitcoin price in usd"

[request]
method = "GET"
url = "http://example.com/x"
`)
	tc := ToolCreate{Dir: dir, AllowAll: true}
	args := baseArgs("example.com")
	args.Name = "new_tool"
	args.Description = "fetch the current bitcoin price in usd"
	args.Origin = "user_forced"
	res, err := tc.Run(context.Background(), mustArgs(t, args))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected gate 1 to refuse a near-duplicate of an existing tool")
	}
	if !strings.Contains(res.Text, "duplicate") {
		t.Errorf("Text = %q, want it to name the duplicate criterion", res.Text)
	}
}

func TestToolCreateGate1RejectsAtBudgetCeiling(t *testing.T) {
	tc := ToolCreate{Dir: t.TempDir(), AllowAll: true, Thresholds: evolve.Thresholds{MaxTools: 7}}
	// The native catalogue alone already has 7 tools, so MaxTools: 7 is
	// already at the ceiling before any layer-2 tool exists on disk.
	args := baseArgs("example.com")
	args.Origin = "user_forced"
	res, err := tc.Run(context.Background(), mustArgs(t, args))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected gate 1 to refuse once the native catalogue alone is at the budget ceiling")
	}
	if !strings.Contains(res.Text, "budget") {
		t.Errorf("Text = %q, want it to name the budget criterion", res.Text)
	}
}

func TestToolCreateUnallowlistedHostIsResultError(t *testing.T) {
	tc := ToolCreate{Dir: t.TempDir(), Allow: []string{"other.example.com"}}
	args := baseArgs("not-allowlisted.example.com")
	res, err := tc.Run(context.Background(), mustArgs(t, args))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected a host not on the egress allowlist to be refused")
	}
	if !strings.Contains(res.Text, "egress allowlist") {
		t.Errorf("Text = %q, want it to mention the egress allowlist", res.Text)
	}
	if _, err := os.Stat(filepath.Join(tc.Dir, "greet")); !os.IsNotExist(err) {
		t.Error("expected nothing to have been written to disk for an un-allowlisted host")
	}
}

func TestToolCreateAllowAllBypassesEgressCheck(t *testing.T) {
	tc := ToolCreate{Dir: t.TempDir(), AllowAll: true}
	args := baseArgs("anything.example.com")
	res, err := tc.Run(context.Background(), mustArgs(t, args))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected AllowAll to bypass the egress check, got: %s", res.Text)
	}
}

func TestToolCreateCredentialPathInURLIsHardBlocked(t *testing.T) {
	tc := ToolCreate{Dir: t.TempDir(), AllowAll: true}
	args := baseArgs("example.com")
	args.URL = "http://example.com/read?path=~/.ssh/id_rsa"
	res, err := tc.Run(context.Background(), mustArgs(t, args))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected a credential-shaped path in the URL to be hard-blocked")
	}
	if !strings.Contains(res.Text, "hard block") {
		t.Errorf("Text = %q, want it to say this is a hard block, not a confirmation", res.Text)
	}
	if _, err := os.Stat(filepath.Join(tc.Dir, "greet")); !os.IsNotExist(err) {
		t.Error("expected nothing to have been written to disk for a hard-blocked proposal")
	}
}

func TestToolCreateCredentialPathInBodyIsHardBlocked(t *testing.T) {
	tc := ToolCreate{Dir: t.TempDir(), AllowAll: true}
	args := baseArgs("example.com")
	args.Method = "POST"
	args.Body = "upload ~/.aws/credentials please"
	res, err := tc.Run(context.Background(), mustArgs(t, args))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected a credential-shaped path in the body to be hard-blocked")
	}
}

func TestToolCreateCredentialPathInHeaderIsHardBlocked(t *testing.T) {
	tc := ToolCreate{Dir: t.TempDir(), AllowAll: true}
	args := baseArgs("example.com")
	args.Headers = map[string]string{"X-Config": "config.toml contents go here"}
	res, err := tc.Run(context.Background(), mustArgs(t, args))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected a credential-shaped path in a header to be hard-blocked")
	}
}

func TestToolCreateSuccessWritesParseableManifestUnverified(t *testing.T) {
	dir := t.TempDir()
	tc := ToolCreate{Dir: dir, Allow: []string{"api.example.com"}}
	args := toolCreateArgs{
		Name:        "bybit_price",
		Description: "get the current price for a coin",
		Params: map[string]toolCreateParamArg{
			"coin": {Type: "string", Required: true, Description: "the coin symbol"},
		},
		Method:  "GET",
		URL:     "http://api.example.com/price",
		Query:   map[string]string{"symbol": "{{.coin}}"},
		Extract: "data.price",
		SelftestArgs: map[string]string{
			"coin": "BTC",
		},
		SelftestExpect: "price",
		Origin:         "user_declared",
		Reason:         "the user said they check this daily",
		Sources:        []string{"https://api.example.com/docs"},
		SessionID:      "sess-123",
	}
	res, err := tc.Run(context.Background(), mustArgs(t, args))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected creation to succeed, got: %s", res.Text)
	}
	if !strings.Contains(res.Text, "unverified") {
		t.Errorf("Text = %q, want it to mention the tool starts unverified", res.Text)
	}

	toolDir := filepath.Join(dir, "bybit_price")
	body, err := os.ReadFile(filepath.Join(toolDir, ManifestFileName))
	if err != nil {
		t.Fatalf("expected a readable manifest on disk: %v", err)
	}
	m, err := parseManifest(body)
	if err != nil {
		t.Fatalf("written manifest failed to parse: %v", err)
	}
	if m.Name != "bybit_price" {
		t.Errorf("m.Name = %q, want bybit_price", m.Name)
	}
	if m.Request.URL != "http://api.example.com/price" {
		t.Errorf("m.Request.URL = %q", m.Request.URL)
	}
	if m.Params["coin"].Type != "string" || !m.Params["coin"].Required {
		t.Errorf("m.Params[coin] = %+v, want a required string param", m.Params["coin"])
	}
	if m.Origin.Reason != "the user said they check this daily" {
		t.Errorf("m.Origin.Reason = %q, provenance was not written", m.Origin.Reason)
	}
	if len(m.Origin.Sources) != 1 || m.Origin.Sources[0] != "https://api.example.com/docs" {
		t.Errorf("m.Origin.Sources = %v, provenance was not written", m.Origin.Sources)
	}
	if m.Selftest.Expect != "price" {
		t.Errorf("m.Selftest.Expect = %q", m.Selftest.Expect)
	}
	if m.Selftest.Args["coin"] != "BTC" {
		t.Errorf("m.Selftest.Args[coin] = %q, want BTC", m.Selftest.Args["coin"])
	}

	// The new tool must start unverified -- no state.json is written by
	// tool_create itself, relying on LoadState's own "missing file ->
	// StateUnverified" default (§19.5 rule 1).
	if _, err := os.Stat(filepath.Join(toolDir, StateFileName)); !os.IsNotExist(err) {
		t.Error("expected no state.json to be written by tool_create")
	}
	state, err := LoadState(toolDir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if state.State != StateUnverified {
		t.Errorf("state = %q, want unverified for a freshly created tool", state.State)
	}
	if state.CanUse() {
		t.Error("a freshly created, unverified tool must not be usable yet (§19.5 rule 1)")
	}
}

func TestToolCreateWrittenManifestIsUsableByToolProbe(t *testing.T) {
	srv, host := newDeclarativeServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "", http.StatusOK)
		_, _ = w.Write([]byte("status_ok"))
	})

	dir := t.TempDir()
	tc := ToolCreate{Dir: dir, Allow: []string{host}}
	args := baseArgs(host)
	args.URL = srv.URL + "/greet"
	args.SelftestExpect = "status_ok"
	if res, err := tc.Run(context.Background(), mustArgs(t, args)); err != nil || res.IsError {
		t.Fatalf("Run: err=%v res=%+v", err, res)
	}

	tp := ToolProbe{Dir: dir, Allow: []string{host}}
	res, err := tp.Run(context.Background(), mustArgs(t, toolProbeArgs{Name: "greet"}))
	if err != nil {
		t.Fatalf("tool_probe Run: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected the newly created tool's self-test to pass, got: %s", res.Text)
	}

	state, err := LoadState(filepath.Join(dir, "greet"))
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if state.State != StateVerified {
		t.Errorf("state = %q, want verified after tool_probe", state.State)
	}
}

func TestToolCreateDefaultsMethodToGET(t *testing.T) {
	dir := t.TempDir()
	tc := ToolCreate{Dir: dir, AllowAll: true}
	args := baseArgs("example.com")
	args.Method = ""
	if res, err := tc.Run(context.Background(), mustArgs(t, args)); err != nil || res.IsError {
		t.Fatalf("Run: err=%v res=%+v", err, res)
	}
	body, err := os.ReadFile(filepath.Join(dir, "greet", ManifestFileName))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	m, err := parseManifest(body)
	if err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	if m.Request.Method != "GET" {
		t.Errorf("m.Request.Method = %q, want GET", m.Request.Method)
	}
}

func TestToolCreateNonGetMethodInfersHighDangerButCreationStillDangerHigh(t *testing.T) {
	// inferDanger's own ratchet already forces DangerHigh for a non-GET
	// method (declarative.go) -- this test only asserts that a POST
	// manifest is still writable (gate 1 + egress + exfiltration are the
	// only refusal paths tool_create itself adds) and that the resulting
	// manifest reports as high danger when read back.
	dir := t.TempDir()
	tc := ToolCreate{Dir: dir, AllowAll: true}
	args := baseArgs("example.com")
	args.Method = "POST"
	args.Body = "hello"
	res, err := tc.Run(context.Background(), mustArgs(t, args))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected creation to succeed, got: %s", res.Text)
	}
	body, err := os.ReadFile(filepath.Join(dir, "greet", ManifestFileName))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	m, err := parseManifest(body)
	if err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	if inferDanger(m) != DangerHigh {
		t.Errorf("inferDanger(written manifest) = %v, want DangerHigh for a non-GET method", inferDanger(m))
	}
}

func TestToolCreateCancelledContextIsGoError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tc := ToolCreate{Dir: t.TempDir(), AllowAll: true}
	_, err := tc.Run(ctx, mustArgs(t, baseArgs("example.com")))
	if err == nil {
		t.Error("expected the cancelled context's error to surface")
	}
}

func TestToolCreateMalformedURLIsResultError(t *testing.T) {
	tc := ToolCreate{Dir: t.TempDir(), AllowAll: true}
	args := baseArgs("example.com")
	args.URL = "http://[::1]:namedport/x" // invalid port -> url.Parse error
	res, err := tc.Run(context.Background(), mustArgs(t, args))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !res.IsError {
		t.Error("expected a malformed URL to be reported as a Result error")
	}
}

func TestParseOrigin(t *testing.T) {
	cases := map[string]evolve.Origin{
		"":              evolve.OriginAgent,
		"agent":         evolve.OriginAgent,
		"AGENT":         evolve.OriginAgent,
		"user_declared": evolve.OriginUserDeclared,
		"user_forced":   evolve.OriginUserForced,
	}
	for in, want := range cases {
		got, err := parseOrigin(in)
		if err != nil {
			t.Errorf("parseOrigin(%q): unexpected error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseOrigin(%q) = %v, want %v", in, got, want)
		}
	}
	if _, err := parseOrigin("bogus"); err == nil {
		t.Error("parseOrigin(\"bogus\") should error")
	}
}

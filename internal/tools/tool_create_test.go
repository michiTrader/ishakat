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

// writeLedgerFixture writes a usage.jsonl under dir/usage.jsonl whose
// records, once merged by evolve.Ledger.Observe, will report
// evolve.CountFor(records, urls[0]) == len(urls) -- i.e. it replays urls in
// order through a real Ledger exactly as internal/app's
// ledgerObservingRunner would have, rather than hand-authoring Record.N
// values that might not agree with shapeKey's own matching rules. Returns
// the ledger's path for use as ToolCreate.LedgerPath.
func writeLedgerFixture(t *testing.T, dir string, urls ...string) string {
	t.Helper()
	path := filepath.Join(dir, "usage.jsonl")
	l := &evolve.Ledger{}
	for _, u := range urls {
		l.Observe(u, "2026-01-01")
	}
	if err := evolve.Save(path, l); err != nil {
		t.Fatalf("writeLedgerFixture: %v", err)
	}
	return path
}

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

// TestToolCreateWithLedgerPathUsesRealCountNotModelClaim is this
// feature's core claim: an origin=agent proposal whose args.Repetitions is
// far below the threshold gate 1 would otherwise refuse on, but whose
// LedgerPath-backed real count clears it, must still be allowed --
// realRepetitions substitutes the verified count in, it does not merely
// add a second check alongside the model's own claim.
func TestToolCreateWithLedgerPathUsesRealCountNotModelClaim(t *testing.T) {
	dir := t.TempDir()
	url := "http://example.com/greet"
	ledgerPath := writeLedgerFixture(t, t.TempDir(), url, url, url)

	tc := ToolCreate{Dir: dir, AllowAll: true, LedgerPath: ledgerPath}
	args := baseArgs("example.com")
	args.Origin = "agent"
	args.URL = url
	args.Repetitions = 0 // the model's own (unverified, and here wrong) claim

	res, err := tc.Run(context.Background(), mustArgs(t, args))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected creation to succeed using the ledger's real count, got: %s", res.Text)
	}
}

// TestToolCreateWithLedgerPathIgnoresAnInflatedModelClaim is the
// complementary case: a model claiming many repetitions it cannot back up
// in the ledger must still be refused -- the ledger overrides the claim in
// both directions, not just upward.
func TestToolCreateWithLedgerPathIgnoresAnInflatedModelClaim(t *testing.T) {
	dir := t.TempDir()
	url := "http://example.com/greet"
	// The ledger only ever saw a *different* URL -- CountFor(records, url)
	// for this one is 0, regardless of args.Repetitions below.
	ledgerPath := writeLedgerFixture(t, t.TempDir(), "http://example.com/other")

	tc := ToolCreate{Dir: dir, AllowAll: true, LedgerPath: ledgerPath}
	args := baseArgs("example.com")
	args.Origin = "agent"
	args.URL = url
	args.Repetitions = 999 // inflated claim the ledger does not support

	res, err := tc.Run(context.Background(), mustArgs(t, args))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected gate 1 to refuse an inflated repetitions claim the ledger does not support")
	}
}

// TestToolCreateWithoutLedgerPathTrustsModelClaimUnchanged is the backward
// compatibility case: a zero-value LedgerPath (every caller before this
// field existed, and any install with Evolve.Mode == "off") must behave
// exactly as before -- args.Repetitions is used as-is, unverified.
func TestToolCreateWithoutLedgerPathTrustsModelClaimUnchanged(t *testing.T) {
	dir := t.TempDir()
	tc := ToolCreate{Dir: dir, AllowAll: true} // LedgerPath left zero-value
	args := baseArgs("example.com")
	args.Origin = "agent"
	args.Repetitions = 5 // whatever gate 1's default MinRepeats requires

	res, err := tc.Run(context.Background(), mustArgs(t, args))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected the model's own claim to be trusted with no LedgerPath configured, got: %s", res.Text)
	}
}

// TestToolCreateWithLedgerPathDoesNotAffectUserForcedOrigin confirms
// realRepetitions leaves non-agent origins alone: OriginUserForced skips
// the Repetition criterion entirely (gate1.go's own doc comment), so a
// LedgerPath that would otherwise refuse an agent-origin proposal must not
// leak into refusing a user_forced one.
func TestToolCreateWithLedgerPathDoesNotAffectUserForcedOrigin(t *testing.T) {
	dir := t.TempDir()
	url := "http://example.com/greet"
	// An empty ledger: CountFor would answer 0 for anything.
	ledgerPath := writeLedgerFixture(t, t.TempDir())

	tc := ToolCreate{Dir: dir, AllowAll: true, LedgerPath: ledgerPath}
	args := baseArgs("example.com")
	args.URL = url // Origin stays "user_forced" per baseArgs

	res, err := tc.Run(context.Background(), mustArgs(t, args))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected user_forced to bypass the Repetition criterion regardless of LedgerPath, got: %s", res.Text)
	}
}

// TestRealRepetitionsUnreadableLedgerFallsBackToZeroNotModelClaim and
// TestRealRepetitionsGenuineLoadErrorFallsBackToZeroNotModelClaim both
// exercise realRepetitions directly (rather than through Run) since a
// LoadLedger error is otherwise hard to trigger deterministically through
// the full Run path -- see ToolCreate.LedgerPath's own doc comment on why
// "fails to load" falls back to 0, not args.Repetitions.
func TestRealRepetitionsUnreadableLedgerFallsBackToZeroNotModelClaim(t *testing.T) {
	dir := t.TempDir()
	// A directory where a file is expected: os.Open will fail with
	// something other than os.IsNotExist.
	ledgerPath := filepath.Join(dir, "usage-is-a-dir.jsonl")
	if err := os.MkdirAll(ledgerPath, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}

	tc := ToolCreate{LedgerPath: ledgerPath}
	args := baseArgs("example.com")
	args.Repetitions = 42 // must NOT be returned on a genuine load error

	got := tc.realRepetitions(evolve.OriginAgent, args)
	if got != 0 {
		t.Errorf("realRepetitions() = %d, want 0 (not the model's claim of %d) on a genuine ledger load error", got, args.Repetitions)
	}
}

func TestRealRepetitionsGenuineLoadErrorFallsBackToZeroNotModelClaim(t *testing.T) {
	dir := t.TempDir()
	ledgerPath := filepath.Join(dir, "sub", "usage.jsonl")
	if err := os.MkdirAll(filepath.Dir(ledgerPath), 0o000); err != nil {
		t.Fatalf("setup: %v", err)
	}
	t.Cleanup(func() { os.Chmod(filepath.Dir(ledgerPath), 0o755) })

	tc := ToolCreate{LedgerPath: filepath.Join(ledgerPath, "unreachable")}
	args := baseArgs("example.com")
	args.Repetitions = 7

	got := tc.realRepetitions(evolve.OriginAgent, args)
	if got != 0 {
		t.Errorf("realRepetitions() = %d, want 0 on a genuine ledger load error", got)
	}
}

// TestToolCreateCreatedByReflectsOrigin confirms buildManifest's
// Origin.CreatedBy tracks the real, parsed origin rather than always
// writing "agent" -- §19.6's own three-origin table pairs both
// user_declared and user_forced with created_by = "user"; only agent
// writes "agent".
func TestToolCreateCreatedByReflectsOrigin(t *testing.T) {
	for _, tc := range []struct {
		origin string
		name   string
		want   string
	}{
		// agent and "" (parseOrigin's own default) both need SkipGate1
		// here -- gate 1's own repetition criterion would otherwise
		// refuse an unrepeated OriginAgent proposal before CreatedBy is
		// ever reached, and that refusal is already covered by
		// TestToolCreateGate1RejectsUnrepeatedAgentProposal; this test
		// isolates buildManifest's CreatedBy mapping alone.
		{"agent", "greet_agent", "agent"},
		{"", "greet_default", "agent"},
		{"user_declared", "greet_user_declared", "user"},
		{"user_forced", "greet_user_forced", "user"},
	} {
		t.Run(tc.origin, func(t *testing.T) {
			dir := t.TempDir()
			toolCreate := ToolCreate{Dir: dir, AllowAll: true, SkipGate1: true}
			args := baseArgs("example.com")
			args.Name = tc.name
			args.Origin = tc.origin
			res, err := toolCreate.Run(context.Background(), mustArgs(t, args))
			if err != nil {
				t.Fatalf("unexpected Go error: %v", err)
			}
			if res.IsError {
				t.Fatalf("expected creation to succeed, got: %s", res.Text)
			}
			body, err := os.ReadFile(filepath.Join(dir, args.Name, ManifestFileName))
			if err != nil {
				t.Fatalf("expected a readable manifest: %v", err)
			}
			m, err := parseManifest(body)
			if err != nil {
				t.Fatalf("written manifest failed to parse: %v", err)
			}
			if m.Origin.CreatedBy != tc.want {
				t.Errorf("m.Origin.CreatedBy = %q, want %q for origin %q", m.Origin.CreatedBy, tc.want, tc.origin)
			}
		})
	}
}

// TestToolCreateSkipGate1BypassesRepetitionAndStability confirms
// SkipGate1 lets through an OriginAgent proposal that unmodified gate 1
// would refuse (no LedgerPath, args.Repetitions == 0, well below
// DefaultThresholds().MinRepeats) -- the exact refusal
// TestToolCreateGate1RejectsUnrepeatedAgentProposal already exercises
// without SkipGate1.
func TestToolCreateSkipGate1BypassesRepetitionAndStability(t *testing.T) {
	tc := ToolCreate{Dir: t.TempDir(), AllowAll: true, SkipGate1: true}
	args := baseArgs("example.com")
	args.Origin = "agent"
	args.Repetitions = 0
	res, err := tc.Run(context.Background(), mustArgs(t, args))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected SkipGate1 to bypass gate 1 entirely for an unrepeated agent proposal, got: %s", res.Text)
	}
}

// TestToolCreateSkipGate1StillEnforcesEgressAndCredentialChecks confirms
// SkipGate1 only bypasses gate 1 (evolve.Evaluate) -- §19.8's own
// mitigations 4 (egress allowlist) and 5 (structural exfiltration
// detection) are never skipped by this field, per SkipGate1's own doc
// comment.
func TestToolCreateSkipGate1StillEnforcesEgressAndCredentialChecks(t *testing.T) {
	t.Run("egress", func(t *testing.T) {
		tc := ToolCreate{Dir: t.TempDir(), SkipGate1: true, Allow: []string{"other.example.com"}}
		args := baseArgs("not-allowlisted.example.com")
		res, err := tc.Run(context.Background(), mustArgs(t, args))
		if err != nil {
			t.Fatalf("unexpected Go error: %v", err)
		}
		if !res.IsError {
			t.Fatal("expected SkipGate1 to still refuse an un-allowlisted host")
		}
		if !strings.Contains(res.Text, "egress allowlist") {
			t.Errorf("Text = %q, want it to mention the egress allowlist", res.Text)
		}
	})

	t.Run("credential path", func(t *testing.T) {
		tc := ToolCreate{Dir: t.TempDir(), AllowAll: true, SkipGate1: true}
		args := baseArgs("example.com")
		args.URL = "http://example.com/read?path=~/.ssh/id_rsa"
		res, err := tc.Run(context.Background(), mustArgs(t, args))
		if err != nil {
			t.Fatalf("unexpected Go error: %v", err)
		}
		if !res.IsError {
			t.Fatal("expected SkipGate1 to still hard-block a credential-shaped path")
		}
	})
}

// TestToolCreateSkipGate1PrependsReasonMarker confirms the "logs it" half
// of §13's own row: a --force creation's manifest carries a fixed,
// greppable marker prepended to Origin.Reason, with the human-supplied
// reason preserved right after it.
func TestToolCreateSkipGate1PrependsReasonMarker(t *testing.T) {
	dir := t.TempDir()
	tc := ToolCreate{Dir: dir, AllowAll: true, SkipGate1: true}
	args := baseArgs("example.com")
	args.Origin = "agent"
	args.Reason = "an operator typed --force"
	res, err := tc.Run(context.Background(), mustArgs(t, args))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected creation to succeed, got: %s", res.Text)
	}
	body, err := os.ReadFile(filepath.Join(dir, args.Name, ManifestFileName))
	if err != nil {
		t.Fatalf("expected a readable manifest: %v", err)
	}
	m, err := parseManifest(body)
	if err != nil {
		t.Fatalf("written manifest failed to parse: %v", err)
	}
	if !strings.HasPrefix(m.Origin.Reason, skipGate1ReasonMarker) {
		t.Errorf("m.Origin.Reason = %q, want it to start with %q", m.Origin.Reason, skipGate1ReasonMarker)
	}
	if !strings.Contains(m.Origin.Reason, "an operator typed --force") {
		t.Errorf("m.Origin.Reason = %q, want the original reason preserved after the marker", m.Origin.Reason)
	}
}

// TestToolCreateWithoutSkipGate1NoMarkerIsWritten confirms the marker is
// exclusive to SkipGate1 -- an ordinary creation's Reason must round-trip
// unmodified, matching TestToolCreateSuccessWritesParseableManifestUnverified's
// own assertion for a different field set.
func TestToolCreateWithoutSkipGate1NoMarkerIsWritten(t *testing.T) {
	dir := t.TempDir()
	tc := ToolCreate{Dir: dir, AllowAll: true}
	args := baseArgs("example.com")
	args.Reason = "an ordinary, non-forced reason"
	res, err := tc.Run(context.Background(), mustArgs(t, args))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected creation to succeed, got: %s", res.Text)
	}
	body, err := os.ReadFile(filepath.Join(dir, args.Name, ManifestFileName))
	if err != nil {
		t.Fatalf("expected a readable manifest: %v", err)
	}
	m, err := parseManifest(body)
	if err != nil {
		t.Fatalf("written manifest failed to parse: %v", err)
	}
	if m.Origin.Reason != "an ordinary, non-forced reason" {
		t.Errorf("m.Origin.Reason = %q, want the reason unmodified without SkipGate1", m.Origin.Reason)
	}
}

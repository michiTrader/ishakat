package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newDeclarativeServer mirrors newFetchServer's exact shape (fetch_test.go):
// an httptest.Server plus the bare hostname it needs adding to an allowlist,
// since hostAllowed matches on Hostname() alone (port stripped). handler is
// given directly, rather than a fixed body/content-type pair, because
// declarative tests need to assert on the *request* (method, headers, query,
// body) as much as they need to control the response.
func newDeclarativeServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, string) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	host := strings.TrimPrefix(strings.TrimPrefix(srv.URL, "http://"), "https://")
	host = host[:strings.Index(host, ":")]
	return srv, host
}

// writeManifest creates baseDir/subdir/tool.toml with contents, failing the
// test on any filesystem error. Used by the DiscoverDeclarative tests below,
// which need real directories on disk (Discover itself does no faking).
func writeManifest(t *testing.T, baseDir, subdir, contents string) {
	t.Helper()
	dir := filepath.Join(baseDir, subdir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestFileName), []byte(contents), 0o644); err != nil {
		t.Fatalf("write manifest %s: %v", dir, err)
	}
}

// --- parseManifest / DiscoverDeclarative -----------------------------------

func TestParseManifestValid(t *testing.T) {
	body := []byte(`
name = "bybit_balance"
description = "Get Bybit wallet balance"
danger = "low"

[origin]
created_by = "human"
reason = "manual test fixture"

[params.coin]
type = "string"
required = false
description = "filter by coin"

[request]
method = "GET"
url = "https://api.bybit.com/v5/account/wallet-balance"

[request.query]
coin = "{{.coin}}"

[response]
extract = "result.list[0].coin[*].{coin, walletBalance, usdValue}"
`)
	m, err := parseManifest(body)
	if err != nil {
		t.Fatalf("parseManifest: %v", err)
	}
	if m.Name != "bybit_balance" {
		t.Errorf("Name = %q, want bybit_balance", m.Name)
	}
	if m.Request.Method != "GET" {
		t.Errorf("Method = %q, want GET", m.Request.Method)
	}
	if _, ok := m.Params["coin"]; !ok {
		t.Errorf("expected params.coin to be parsed")
	}
	if m.Response.Extract == "" {
		t.Errorf("expected response.extract to be parsed")
	}
}

func TestParseManifestDefaultsMethodToGet(t *testing.T) {
	m, err := parseManifest([]byte(`
name = "no_method"
[request]
url = "https://example.com/x"
`))
	if err != nil {
		t.Fatalf("parseManifest: %v", err)
	}
	if m.Request.Method != http.MethodGet {
		t.Errorf("Method = %q, want GET (default)", m.Request.Method)
	}
}

func TestParseManifestMissingURLIsError(t *testing.T) {
	_, err := parseManifest([]byte(`
name = "broken"
[request]
method = "GET"
`))
	if err == nil {
		t.Fatal("expected error for missing [request].url")
	}
}

func TestParseManifestInvalidTOMLIsError(t *testing.T) {
	_, err := parseManifest([]byte(`not valid toml === {{{`))
	if err == nil {
		t.Fatal("expected error for invalid TOML")
	}
}

func TestParseManifestIgnoresUnknownTables(t *testing.T) {
	// §20.11 item 1: [package] must be accepted and silently ignored -- no
	// error, no warning field on Manifest to check either, since there is
	// deliberately none. [selftest] is no longer one of these tables (see
	// TestParseManifestDecodesSelftest below) -- it decodes into
	// Manifest.Selftest now that Step 21's tool_probe reads it.
	_, err := parseManifest([]byte(`
name = "with_extras"
[request]
url = "https://example.com/x"
[package]
id = "com.example.with_extras"
`))
	if err != nil {
		t.Fatalf("parseManifest: unexpected error for reserved tables: %v", err)
	}
}

func TestParseManifestDecodesSelftest(t *testing.T) {
	m, err := parseManifest([]byte(`
name = "with_selftest"
[request]
url = "https://example.com/x"
[selftest]
args = { coin = "BTC" }
env = { X_TESTNET = "1" }
expect = "status_ok"
`))
	if err != nil {
		t.Fatalf("parseManifest: unexpected error: %v", err)
	}
	if m.Selftest.Args["coin"] != "BTC" {
		t.Errorf("Selftest.Args[coin] = %q, want BTC", m.Selftest.Args["coin"])
	}
	if m.Selftest.Env["X_TESTNET"] != "1" {
		t.Errorf("Selftest.Env[X_TESTNET] = %q, want 1", m.Selftest.Env["X_TESTNET"])
	}
	if m.Selftest.Expect != "status_ok" {
		t.Errorf("Selftest.Expect = %q, want status_ok", m.Selftest.Expect)
	}
}

func TestParseManifestNoSelftestTableIsZeroValue(t *testing.T) {
	m, err := parseManifest([]byte(`
name = "no_selftest"
[request]
url = "https://example.com/x"
`))
	if err != nil {
		t.Fatalf("parseManifest: unexpected error: %v", err)
	}
	if m.Selftest.Expect != "" || len(m.Selftest.Args) != 0 || len(m.Selftest.Env) != 0 {
		t.Errorf("Selftest = %+v, want the zero value for a manifest with no [selftest] table", m.Selftest)
	}
}

func TestDiscoverDeclarativeMissingDirIsNotError(t *testing.T) {
	res := DiscoverDeclarative("/nonexistent/path/for/ishakat/tests")
	if res.Warn != "" {
		t.Errorf("Warn = %q, want empty for a missing directory", res.Warn)
	}
	if len(res.Tools) != 0 {
		t.Errorf("Tools = %v, want empty", res.Tools)
	}
}

func TestDiscoverDeclarativeEmptyDirName(t *testing.T) {
	res := DiscoverDeclarative("")
	if res.Warn != "" || len(res.Tools) != 0 {
		t.Errorf("expected empty result for empty dir, got %+v", res)
	}
}

func TestDiscoverDeclarativeFindsAndSorts(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "zzz_tool", `
name = "zzz_tool"
[request]
url = "https://example.com/z"
`)
	writeManifest(t, dir, "aaa_tool", `
name = "aaa_tool"
[request]
url = "https://example.com/a"
`)
	// A subdirectory with no tool.toml at all is skipped, not an error.
	if err := os.MkdirAll(filepath.Join(dir, "no_manifest_here"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	res := DiscoverDeclarative(dir)
	if res.Warn != "" {
		t.Fatalf("unexpected Warn: %s", res.Warn)
	}
	if len(res.Tools) != 2 {
		t.Fatalf("got %d tools, want 2: %+v", len(res.Tools), res.Tools)
	}
	if res.Tools[0].Name != "aaa_tool" || res.Tools[1].Name != "zzz_tool" {
		t.Errorf("tools not sorted by name: %q, %q", res.Tools[0].Name, res.Tools[1].Name)
	}
}

func TestDiscoverDeclarativeFallsBackToDirName(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "my_tool_dir", `
[request]
url = "https://example.com/x"
`)
	res := DiscoverDeclarative(dir)
	if len(res.Tools) != 1 {
		t.Fatalf("got %d tools, want 1", len(res.Tools))
	}
	if res.Tools[0].Name != "my_tool_dir" {
		t.Errorf("Name = %q, want fallback to directory name", res.Tools[0].Name)
	}
}

func TestDiscoverDeclarativeWarnsOnceOnFirstParseFailure(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "bad_tool", `not valid toml === {{{`)
	res := DiscoverDeclarative(dir)
	if res.Warn == "" {
		t.Fatal("expected a Warn for the unparsable manifest")
	}
	if len(res.Tools) != 0 {
		t.Errorf("expected no tools discovered, got %+v", res.Tools)
	}
}

// --- Manifest.Unsatisfied ----------------------------------------------------

func TestManifestUnsatisfiedNilWhenNoRequirements(t *testing.T) {
	m := Manifest{}
	if got := m.Unsatisfied(Caps{}); got != nil {
		t.Errorf("Unsatisfied = %v, want nil", got)
	}
}

func TestManifestUnsatisfiedChecksCaps(t *testing.T) {
	m := Manifest{RequiresCaps: []string{"vision", "unknown_cap"}}
	got := m.Unsatisfied(Caps{Vision: false})
	if len(got) != 2 {
		t.Fatalf("got %d unsatisfied entries, want 2: %v", len(got), got)
	}
}

func TestManifestUnsatisfiedCapsSatisfied(t *testing.T) {
	m := Manifest{RequiresCaps: []string{"vision", "tools"}}
	got := m.Unsatisfied(Caps{Vision: true, Tools: true})
	if got != nil {
		t.Errorf("Unsatisfied = %v, want nil", got)
	}
}

func TestManifestUnsatisfiedMinContext(t *testing.T) {
	m := Manifest{MinContext: 100000}
	if got := m.Unsatisfied(Caps{Context: 8000}); len(got) != 1 {
		t.Errorf("got %v, want one unsatisfied entry for min_context", got)
	}
	// Unknown context (0) on either side never fails min_context.
	if got := m.Unsatisfied(Caps{Context: 0}); got != nil {
		t.Errorf("got %v, want nil when active context is unknown", got)
	}
	if got := (Manifest{}).Unsatisfied(Caps{Context: 100}); got != nil {
		t.Errorf("got %v, want nil when manifest declares no min_context", got)
	}
}

// --- inferDanger --------------------------------------------------------

func TestInferDangerGetToOrdinaryHostDefaultsLow(t *testing.T) {
	m := Manifest{Request: RequestSpec{Method: "GET", URL: "https://example.com/x"}}
	if got := inferDanger(m); got != DangerLow {
		t.Errorf("inferDanger = %v, want DangerLow", got)
	}
}

func TestInferDangerManifestMayRaiseNeverLower(t *testing.T) {
	// A manifest may claim "medium" for an otherwise-low-risk GET and that
	// claim is honoured.
	m := Manifest{Danger: "medium", Request: RequestSpec{Method: "GET", URL: "https://example.com/x"}}
	if got := inferDanger(m); got != DangerMedium {
		t.Errorf("inferDanger = %v, want DangerMedium (manifest's own raised claim)", got)
	}

	// But a manifest cannot claim "low" for a POST -- that is the ratchet.
	m2 := Manifest{Danger: "low", Request: RequestSpec{Method: "POST", URL: "https://example.com/x"}}
	if got := inferDanger(m2); got != DangerHigh {
		t.Errorf("inferDanger = %v, want DangerHigh (non-GET forces high regardless of claim)", got)
	}
}

func TestInferDangerFinanceHostForcesHighRegardlessOfMethod(t *testing.T) {
	m := Manifest{Danger: "low", Request: RequestSpec{Method: "GET", URL: "https://api.bybit.com/v5/x"}}
	if got := inferDanger(m); got != DangerHigh {
		t.Errorf("inferDanger = %v, want DangerHigh for a finance-list host", got)
	}
	// Subdomains of a finance host also count.
	m2 := Manifest{Request: RequestSpec{Method: "GET", URL: "https://api.binance.com/x"}}
	if got := inferDanger(m2); got != DangerHigh {
		t.Errorf("inferDanger = %v, want DangerHigh for a finance-list subdomain", got)
	}
	// A host that merely contains a finance name as a substring, without
	// being that domain or a subdomain of it, must not match.
	m3 := Manifest{Request: RequestSpec{Method: "GET", URL: "https://notbybit.com.evil.example/x"}}
	if got := inferDanger(m3); got != DangerLow {
		t.Errorf("inferDanger = %v, want DangerLow: notbybit.com.evil.example is not a finance host", got)
	}
}

func TestDeclarativeToolDangerUsesInference(t *testing.T) {
	d := DeclarativeTool{Manifest: Manifest{Danger: "low", Request: RequestSpec{Method: "DELETE", URL: "https://example.com/x"}}}
	if got := d.Danger(); got != DangerHigh {
		t.Errorf("Danger() = %v, want DangerHigh", got)
	}
}

// --- Parameters() ---------------------------------------------------------

func TestDeclarativeToolParametersBuildsSchema(t *testing.T) {
	d := DeclarativeTool{Manifest: Manifest{
		Params: map[string]ParamSpec{
			"coin":   {Type: "string", Required: true, Description: "coin symbol"},
			"limit":  {Type: "number", Required: false},
			"status": {Required: false, Enum: []string{"open", "closed"}},
		},
	}}
	var schema struct {
		Type       string `json:"type"`
		Properties map[string]struct {
			Type string   `json:"type"`
			Enum []string `json:"enum,omitempty"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(d.Parameters(), &schema); err != nil {
		t.Fatalf("Parameters() did not produce valid JSON: %v", err)
	}
	if schema.Type != "object" {
		t.Errorf("Type = %q, want object", schema.Type)
	}
	if len(schema.Required) != 1 || schema.Required[0] != "coin" {
		t.Errorf("Required = %v, want [coin]", schema.Required)
	}
	if schema.Properties["status"].Type != "string" {
		t.Errorf("status.Type = %q, want string (default fallback)", schema.Properties["status"].Type)
	}
	if len(schema.Properties["status"].Enum) != 2 {
		t.Errorf("status.Enum = %v, want 2 entries", schema.Properties["status"].Enum)
	}
}

// --- Run(): success, templating, egress, status, extract -------------------

func TestDeclarativeRunSuccessGET(t *testing.T) {
	srv, host := newDeclarativeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("got method %s, want GET", r.Method)
		}
		if got := r.URL.Query().Get("coin"); got != "BTC" {
			t.Errorf("query coin = %q, want BTC", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	d := DeclarativeTool{
		Manifest: Manifest{
			Name: "t",
			Params: map[string]ParamSpec{
				"coin": {Type: "string"},
			},
			Request: RequestSpec{
				Method: "GET",
				URL:    srv.URL + "/balance",
				Query:  map[string]string{"coin": "{{.coin}}"},
			},
		},
		Allow:      []string{host},
		HTTPClient: srv.Client(),
	}
	res, err := d.Run(context.Background(), mustArgs(t, map[string]string{"coin": "BTC"}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Text)
	}
	if res.Text != `{"ok":true}` {
		t.Errorf("Text = %q, want raw JSON body", res.Text)
	}
}

func TestDeclarativeRunTemplatesHeadersAndBody(t *testing.T) {
	srv, host := newDeclarativeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Session"); got != "abc123" {
			t.Errorf("X-Session header = %q, want abc123", got)
		}
		b, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(b), `"amount":"42"`) {
			t.Errorf("body = %q, want to contain amount:42", string(b))
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"created"}`))
	})
	d := DeclarativeTool{
		Manifest: Manifest{
			Name: "t",
			Params: map[string]ParamSpec{
				"session": {Type: "string"},
				"amount":  {Type: "string"},
			},
			Request: RequestSpec{
				Method:  "POST",
				URL:     srv.URL + "/orders",
				Headers: map[string]string{"X-Session": "{{.session}}"},
				Body:    `{"amount":"{{.amount}}"}`,
			},
		},
		Allow:      []string{host},
		HTTPClient: srv.Client(),
	}
	res, err := d.Run(context.Background(), mustArgs(t, map[string]string{"session": "abc123", "amount": "42"}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Text)
	}
}

func TestDeclarativeRunUsesDefaultWhenArgOmitted(t *testing.T) {
	srv, host := newDeclarativeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("coin"); got != "ETH" {
			t.Errorf("query coin = %q, want ETH (default)", got)
		}
		_, _ = w.Write([]byte(`{}`))
	})
	d := DeclarativeTool{
		Manifest: Manifest{
			Params: map[string]ParamSpec{"coin": {Type: "string", Default: "ETH"}},
			Request: RequestSpec{
				Method: "GET",
				URL:    srv.URL,
				Query:  map[string]string{"coin": "{{.coin}}"},
			},
		},
		Allow:      []string{host},
		HTTPClient: srv.Client(),
	}
	res, err := d.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Text)
	}
}

func TestDeclarativeRunOmittedOptionalQueryParamIsDropped(t *testing.T) {
	srv, host := newDeclarativeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("coin") {
			t.Errorf("expected coin query param to be dropped when empty, got %q", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{}`))
	})
	d := DeclarativeTool{
		Manifest: Manifest{
			Params: map[string]ParamSpec{"coin": {Type: "string"}}, // no default
			Request: RequestSpec{
				Method: "GET",
				URL:    srv.URL,
				Query:  map[string]string{"coin": "{{.coin}}"},
			},
		},
		Allow:      []string{host},
		HTTPClient: srv.Client(),
	}
	res, err := d.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Text)
	}
}

func TestDeclarativeRunRejectsHostNotOnAllowlist(t *testing.T) {
	srv, _ := newDeclarativeServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("server should never be called: request must be rejected before dialing")
	})
	d := DeclarativeTool{
		Manifest: Manifest{
			Request: RequestSpec{Method: "GET", URL: srv.URL},
		},
		Allow:      []string{"totally-different-host.example"},
		HTTPClient: srv.Client(),
	}
	res, err := d.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("Run: unexpected Go error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected an error result for a disallowed host")
	}
	if !strings.Contains(res.Text, "egress allowlist") {
		t.Errorf("Text = %q, want mention of the egress allowlist", res.Text)
	}
}

func TestDeclarativeRunAllowAllBypassesAllowlist(t *testing.T) {
	srv, _ := newDeclarativeServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	d := DeclarativeTool{
		Manifest:   Manifest{Request: RequestSpec{Method: "GET", URL: srv.URL}},
		AllowAll:   true,
		HTTPClient: srv.Client(),
	}
	res, err := d.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Text)
	}
}

func TestDeclarativeRunNonGETMethodForcesHighDangerButStillRuns(t *testing.T) {
	srv, host := newDeclarativeServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	d := DeclarativeTool{
		Manifest:   Manifest{Danger: "low", Request: RequestSpec{Method: "POST", URL: srv.URL}},
		Allow:      []string{host},
		HTTPClient: srv.Client(),
	}
	if got := d.Danger(); got != DangerHigh {
		t.Fatalf("Danger() = %v, want DangerHigh", got)
	}
	res, err := d.Run(context.Background(), nil)
	if err != nil || res.IsError {
		t.Fatalf("Run should still succeed at the HTTP level: err=%v res=%+v", err, res)
	}
}

func TestDeclarativeRunNon2xxStatusIsErrorResult(t *testing.T) {
	srv, host := newDeclarativeServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	})
	d := DeclarativeTool{
		Manifest:   Manifest{Request: RequestSpec{Method: "GET", URL: srv.URL}},
		Allow:      []string{host},
		HTTPClient: srv.Client(),
	}
	res, err := d.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("Run: unexpected Go error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected an error result for a 500 status")
	}
	if !strings.Contains(res.Text, "500") || !strings.Contains(res.Text, "boom") {
		t.Errorf("Text = %q, want status code and body quoted", res.Text)
	}
}

func TestDeclarativeRunRejectsUnsupportedScheme(t *testing.T) {
	d := DeclarativeTool{
		Manifest: Manifest{Request: RequestSpec{Method: "GET", URL: "ftp://example.com/x"}},
		AllowAll: true,
	}
	res, err := d.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("Run: unexpected Go error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected an error result for a non-http(s) scheme")
	}
}

func TestDeclarativeRunAppliesResponseExtract(t *testing.T) {
	srv, host := newDeclarativeServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"result":{"list":[{"coin":[
			{"coin":"BTC","walletBalance":"1.5","usdValue":"90000","extra":"ignored"},
			{"coin":"ETH","walletBalance":"10","usdValue":"30000","extra":"ignored"}
		]}]}}`))
	})
	d := DeclarativeTool{
		Manifest: Manifest{
			Request:  RequestSpec{Method: "GET", URL: srv.URL},
			Response: ResponseSpec{Extract: "result.list[0].coin[*].{coin, walletBalance, usdValue}"},
		},
		Allow:      []string{host},
		HTTPClient: srv.Client(),
	}
	res, err := d.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Text)
	}
	var got []map[string]string
	if err := json.Unmarshal([]byte(res.Text), &got); err != nil {
		t.Fatalf("output is not the expected JSON shape: %v (%s)", err, res.Text)
	}
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2", len(got))
	}
	if got[0]["coin"] != "BTC" || got[0]["walletBalance"] != "1.5" {
		t.Errorf("got[0] = %v, want coin=BTC walletBalance=1.5", got[0])
	}
	if _, ok := got[0]["extra"]; ok {
		t.Errorf("got[0] = %v, want 'extra' field projected away", got[0])
	}
}

func TestDeclarativeRunExtractFailureIsErrorResultNotSwallowed(t *testing.T) {
	srv, host := newDeclarativeServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"unexpected":"shape"}`))
	})
	d := DeclarativeTool{
		Manifest: Manifest{
			Request:  RequestSpec{Method: "GET", URL: srv.URL},
			Response: ResponseSpec{Extract: "result.list[0].coin"},
		},
		Allow:      []string{host},
		HTTPClient: srv.Client(),
	}
	res, err := d.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("Run: unexpected Go error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected an error result when extract does not match the response shape")
	}
	if !strings.Contains(res.Text, "extract") {
		t.Errorf("Text = %q, want mention of the failing extract expression", res.Text)
	}
}

// --- auth schemes ----------------------------------------------------------

func TestDeclarativeRunAuthBearer(t *testing.T) {
	t.Setenv("TEST_BEARER_TOKEN", "sekret")
	srv, host := newDeclarativeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer sekret" {
			t.Errorf("Authorization = %q, want Bearer sekret", got)
		}
		_, _ = w.Write([]byte(`{}`))
	})
	d := DeclarativeTool{
		Manifest: Manifest{Request: RequestSpec{
			Method: "GET", URL: srv.URL,
			Auth: AuthSpec{Scheme: "bearer", SecretEnv: "TEST_BEARER_TOKEN"},
		}},
		Allow:      []string{host},
		HTTPClient: srv.Client(),
	}
	res, err := d.Run(context.Background(), nil)
	if err != nil || res.IsError {
		t.Fatalf("Run: err=%v res=%+v", err, res)
	}
}

func TestDeclarativeRunAuthHeaderCustomName(t *testing.T) {
	t.Setenv("TEST_API_KEY", "k-123")
	srv, host := newDeclarativeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Custom-Key"); got != "k-123" {
			t.Errorf("X-Custom-Key = %q, want k-123", got)
		}
		_, _ = w.Write([]byte(`{}`))
	})
	d := DeclarativeTool{
		Manifest: Manifest{Request: RequestSpec{
			Method: "GET", URL: srv.URL,
			Auth: AuthSpec{Scheme: "header", SecretEnv: "TEST_API_KEY", Header: "X-Custom-Key"},
		}},
		Allow:      []string{host},
		HTTPClient: srv.Client(),
	}
	res, err := d.Run(context.Background(), nil)
	if err != nil || res.IsError {
		t.Fatalf("Run: err=%v res=%+v", err, res)
	}
}

func TestDeclarativeRunAuthQuery(t *testing.T) {
	t.Setenv("TEST_QUERY_KEY", "q-456")
	srv, host := newDeclarativeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("api_key"); got != "q-456" {
			t.Errorf("api_key query = %q, want q-456", got)
		}
		_, _ = w.Write([]byte(`{}`))
	})
	d := DeclarativeTool{
		Manifest: Manifest{Request: RequestSpec{
			Method: "GET", URL: srv.URL,
			Auth: AuthSpec{Scheme: "query", SecretEnv: "TEST_QUERY_KEY"},
		}},
		Allow:      []string{host},
		HTTPClient: srv.Client(),
	}
	res, err := d.Run(context.Background(), nil)
	if err != nil || res.IsError {
		t.Fatalf("Run: err=%v res=%+v", err, res)
	}
}

func TestDeclarativeRunAuthHMACSHA256(t *testing.T) {
	t.Setenv("TEST_HMAC_KEY", "key-1")
	t.Setenv("TEST_HMAC_SECRET", "shh")
	fixedNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	srv, host := newDeclarativeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-API-KEY"); got != "key-1" {
			t.Errorf("X-API-KEY = %q, want key-1", got)
		}
		if got := r.Header.Get("X-API-TIMESTAMP"); got == "" {
			t.Errorf("X-API-TIMESTAMP missing")
		}
		if got := r.Header.Get("X-API-SIGNATURE"); got == "" {
			t.Errorf("X-API-SIGNATURE missing")
		}
		_, _ = w.Write([]byte(`{}`))
	})
	d := DeclarativeTool{
		Manifest: Manifest{Request: RequestSpec{
			Method: "GET", URL: srv.URL,
			Auth: AuthSpec{Scheme: "hmac_sha256", KeyEnv: "TEST_HMAC_KEY", SecretEnv: "TEST_HMAC_SECRET"},
		}},
		Allow:      []string{host},
		HTTPClient: srv.Client(),
		Now:        func() time.Time { return fixedNow },
	}
	res, err := d.Run(context.Background(), nil)
	if err != nil || res.IsError {
		t.Fatalf("Run: err=%v res=%+v", err, res)
	}
}

func TestDeclarativeRunAuthMissingEnvIsErrorResultNotGoError(t *testing.T) {
	srv, host := newDeclarativeServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("server should never be called when auth setup fails")
	})
	d := DeclarativeTool{
		Manifest: Manifest{Request: RequestSpec{
			Method: "GET", URL: srv.URL,
			Auth: AuthSpec{Scheme: "bearer", SecretEnv: "ISHAKAT_TEST_DOES_NOT_EXIST"},
		}},
		Allow:      []string{host},
		HTTPClient: srv.Client(),
	}
	_, err := d.Run(context.Background(), nil)
	if err == nil {
		t.Fatal("expected a Go error: auth could not even be attempted")
	}
}

func TestDeclarativeRunUnknownAuthSchemeIsGoError(t *testing.T) {
	srv, host := newDeclarativeServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("server should never be called for an unknown auth scheme")
	})
	d := DeclarativeTool{
		Manifest: Manifest{Request: RequestSpec{
			Method: "GET", URL: srv.URL,
			Auth: AuthSpec{Scheme: "made_up_scheme"},
		}},
		Allow:      []string{host},
		HTTPClient: srv.Client(),
	}
	_, err := d.Run(context.Background(), nil)
	if err == nil {
		t.Fatal("expected a Go error for an unregistered auth scheme")
	}
}

// --- extractJSON / parseExtractPath / evalExtractPath -----------------------

func TestExtractJSONIllustrativeExpression(t *testing.T) {
	body := []byte(`{"result":{"list":[{"coin":[
		{"coin":"BTC","walletBalance":"1.5","usdValue":"90000"},
		{"coin":"ETH","walletBalance":"10","usdValue":"30000"}
	]}]}}`)
	out, err := extractJSON(body, "result.list[0].coin[*].{coin, walletBalance, usdValue}")
	if err != nil {
		t.Fatalf("extractJSON: %v", err)
	}
	var got []map[string]string
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output not valid JSON: %v (%s)", err, out)
	}
	if len(got) != 2 || got[1]["coin"] != "ETH" {
		t.Errorf("got %v, want 2 entries with ETH second", got)
	}
}

func TestExtractJSONSimpleDottedField(t *testing.T) {
	out, err := extractJSON([]byte(`{"a":{"b":"c"}}`), "a.b")
	if err != nil {
		t.Fatalf("extractJSON: %v", err)
	}
	if out != `"c"` {
		t.Errorf("out = %q, want \"c\"", out)
	}
}

func TestExtractJSONNumericIndex(t *testing.T) {
	out, err := extractJSON([]byte(`{"a":["x","y","z"]}`), "a[1]")
	if err != nil {
		t.Fatalf("extractJSON: %v", err)
	}
	if out != `"y"` {
		t.Errorf("out = %q, want \"y\"", out)
	}
}

func TestExtractJSONInvalidJSONBody(t *testing.T) {
	_, err := extractJSON([]byte(`not json`), "a.b")
	if err == nil {
		t.Fatal("expected error for a non-JSON body")
	}
}

func TestExtractJSONMissingFieldIsError(t *testing.T) {
	_, err := extractJSON([]byte(`{"a":1}`), "b.c")
	if err == nil {
		t.Fatal("expected error for a missing field")
	}
}

func TestExtractJSONOutOfRangeIndexIsError(t *testing.T) {
	_, err := extractJSON([]byte(`{"a":["x"]}`), "a[5]")
	if err == nil {
		t.Fatal("expected error for an out-of-range index")
	}
}

func TestExtractJSONNonArrayIndexIsError(t *testing.T) {
	_, err := extractJSON([]byte(`{"a":"not an array"}`), "a[0]")
	if err == nil {
		t.Fatal("expected error indexing into a non-array")
	}
}

func TestExtractJSONProjectionOnNonObjectIsError(t *testing.T) {
	_, err := extractJSON([]byte(`{"a":"scalar"}`), "a.{x,y}")
	if err == nil {
		t.Fatal("expected error projecting fields out of a scalar")
	}
}

func TestParseExtractPathRejectsEmptySegment(t *testing.T) {
	if _, err := parseExtractPath("a..b"); err == nil {
		t.Fatal("expected error for an empty path segment")
	}
}

func TestParseExtractPathRejectsUnterminatedIndex(t *testing.T) {
	if _, err := parseExtractPath("a[0"); err == nil {
		t.Fatal("expected error for an unterminated index")
	}
}

func TestParseExtractPathRejectsUnterminatedProjection(t *testing.T) {
	if _, err := parseExtractPath("a.{x,y"); err == nil {
		t.Fatal("expected error for an unterminated projection")
	}
}

func TestParseExtractPathRejectsInvalidIndex(t *testing.T) {
	if _, err := parseExtractPath("a[abc]"); err == nil {
		t.Fatal("expected error for a non-numeric, non-* index")
	}
}

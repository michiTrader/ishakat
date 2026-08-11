package tools

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestToolProbeNameDescriptionDanger(t *testing.T) {
	tp := ToolProbe{}
	if tp.Name() != "tool_probe" {
		t.Errorf("Name() = %q, want tool_probe", tp.Name())
	}
	if tp.Description() == "" {
		t.Error("Description() must not be empty")
	}
	if tp.Danger() != DangerLow {
		t.Errorf("Danger() = %v, want DangerLow", tp.Danger())
	}
}

func TestToolProbeEmptyNameIsArgError(t *testing.T) {
	tp := ToolProbe{Dir: t.TempDir()}
	_, err := tp.Run(context.Background(), mustArgs(t, toolProbeArgs{Name: ""}))
	if err == nil {
		t.Error("expected an error for an empty name")
	}
}

func TestToolProbeUnknownNameIsResultError(t *testing.T) {
	tp := ToolProbe{Dir: t.TempDir()}
	res, err := tp.Run(context.Background(), mustArgs(t, toolProbeArgs{Name: "does_not_exist"}))
	if err != nil {
		t.Fatalf("an unknown tool name must be Result.IsError data, not a Go error: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected IsError for an unknown tool, got: %s", res.Text)
	}
}

func TestToolProbeNoDirConfiguredIsResultError(t *testing.T) {
	tp := ToolProbe{Dir: ""}
	res, err := tp.Run(context.Background(), mustArgs(t, toolProbeArgs{Name: "anything"}))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError when no tools directory is configured")
	}
}

func TestToolProbePassingSelftestVerifiesTool(t *testing.T) {
	srv, host := newDeclarativeServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "status_ok")
	})
	_ = srv

	dir := t.TempDir()
	toolDir := filepath.Join(dir, "greet")
	manifest := fmt.Sprintf(`
name = "greet"
description = "say hello"

[request]
method = "GET"
url = "%s/greet"

[selftest]
expect = "status_ok"
`, srv.URL)
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(toolDir, ManifestFileName), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	tp := ToolProbe{Dir: dir, Allow: []string{host}}
	res, err := tp.Run(context.Background(), mustArgs(t, toolProbeArgs{Name: "greet"}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected the probe to pass, got error: %s", res.Text)
	}
	if !strings.Contains(res.Text, "verified") {
		t.Errorf("Text = %q, want it to mention the tool is now verified", res.Text)
	}

	state, err := LoadState(toolDir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if state.State != StateVerified {
		t.Errorf("state = %q, want verified", state.State)
	}
	if state.Hash == "" {
		t.Error("expected a non-empty Hash to be pinned after a passing probe")
	}
}

func TestToolProbeFailingExpectKeepsToolUnverified(t *testing.T) {
	srv, host := newDeclarativeServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "something else entirely")
	})

	dir := t.TempDir()
	toolDir := filepath.Join(dir, "greet")
	manifest := fmt.Sprintf(`
name = "greet"
description = "say hello"

[request]
method = "GET"
url = "%s/greet"

[selftest]
expect = "status_ok"
`, srv.URL)
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(toolDir, ManifestFileName), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	tp := ToolProbe{Dir: dir, Allow: []string{host}}
	res, err := tp.Run(context.Background(), mustArgs(t, toolProbeArgs{Name: "greet"}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected the probe to fail (expect mismatch), got: %s", res.Text)
	}
	if !strings.Contains(res.Text, "status_ok") {
		t.Errorf("Text = %q, want the expected substring named in the error", res.Text)
	}

	state, err := LoadState(toolDir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if state.State != StateUnverified {
		t.Errorf("state = %q, want unverified after a failing probe", state.State)
	}
	if state.LastError == "" {
		t.Error("expected LastError to be recorded after a failing probe")
	}
}

func TestToolProbeHTTPFailureIsResultErrorAndKeepsUnverified(t *testing.T) {
	srv, host := newDeclarativeServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "boom")
	})

	dir := t.TempDir()
	toolDir := filepath.Join(dir, "flaky")
	manifest := fmt.Sprintf(`
name = "flaky"
description = "fails"

[request]
method = "GET"
url = "%s/flaky"
`, srv.URL)
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(toolDir, ManifestFileName), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	tp := ToolProbe{Dir: dir, Allow: []string{host}}
	res, err := tp.Run(context.Background(), mustArgs(t, toolProbeArgs{Name: "flaky"}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected the probe to fail on a 500 response")
	}

	state, err := LoadState(toolDir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if state.State != StateUnverified {
		t.Errorf("state = %q, want unverified after an HTTP failure", state.State)
	}
}

func TestToolProbeNoSelftestTableStillRunsRealCallOnce(t *testing.T) {
	var called bool
	srv, host := newDeclarativeServer(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
		fmt.Fprint(w, "anything at all")
	})

	dir := t.TempDir()
	toolDir := filepath.Join(dir, "nocheck")
	manifest := fmt.Sprintf(`
name = "nocheck"
description = "no selftest table"

[request]
method = "GET"
url = "%s/x"
`, srv.URL)
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(toolDir, ManifestFileName), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	tp := ToolProbe{Dir: dir, Allow: []string{host}}
	res, err := tp.Run(context.Background(), mustArgs(t, toolProbeArgs{Name: "nocheck"}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected the probe to pass with no [selftest].expect to check, got: %s", res.Text)
	}
	if !called {
		t.Error("expected the real HTTP call to have happened")
	}
}

func TestToolProbeAppliesSelftestEnvForCallDurationOnly(t *testing.T) {
	var seenKey string
	srv, host := newDeclarativeServer(t, func(w http.ResponseWriter, r *http.Request) {
		seenKey = r.Header.Get("Authorization")
		fmt.Fprint(w, "status_ok")
	})

	dir := t.TempDir()
	toolDir := filepath.Join(dir, "authed")
	manifest := fmt.Sprintf(`
name = "authed"
description = "needs a key"

[request]
method = "GET"
url = "%s/x"

[request.auth]
scheme  = "header"
key_env = "PROBE_TEST_KEY"

[selftest]
env    = { PROBE_TEST_KEY = "probe-secret" }
expect = "status_ok"
`, srv.URL)
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(toolDir, ManifestFileName), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	if _, ok := os.LookupEnv("PROBE_TEST_KEY"); ok {
		t.Fatal("PROBE_TEST_KEY should not be set before the test runs")
	}

	tp := ToolProbe{Dir: dir, Allow: []string{host}}
	res, err := tp.Run(context.Background(), mustArgs(t, toolProbeArgs{Name: "authed"}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected the probe to pass, got: %s", res.Text)
	}
	if seenKey != "probe-secret" {
		t.Errorf("server saw Authorization=%q, want probe-secret (from [selftest].env)", seenKey)
	}
	if _, ok := os.LookupEnv("PROBE_TEST_KEY"); ok {
		t.Error("PROBE_TEST_KEY should be unset again after the probe returns")
	}
}

func TestToolProbeRestoresPreviousEnvValueAfterward(t *testing.T) {
	const key = "PROBE_TEST_RESTORE_KEY"
	if err := os.Setenv(key, "original"); err != nil {
		t.Fatalf("Setenv: %v", err)
	}
	t.Cleanup(func() { os.Unsetenv(key) })

	srv, host := newDeclarativeServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "status_ok")
	})

	dir := t.TempDir()
	toolDir := filepath.Join(dir, "restoretest")
	manifest := fmt.Sprintf(`
name = "restoretest"
description = "checks env restore"

[request]
method = "GET"
url = "%s/x"

[selftest]
env    = { PROBE_TEST_RESTORE_KEY = "overridden" }
expect = "status_ok"
`, srv.URL)
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(toolDir, ManifestFileName), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	tp := ToolProbe{Dir: dir, Allow: []string{host}}
	if _, err := tp.Run(context.Background(), mustArgs(t, toolProbeArgs{Name: "restoretest"})); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := os.Getenv(key); got != "original" {
		t.Errorf("%s = %q after probe, want restored to original", key, got)
	}
}

func TestToolProbeUsesSelftestArgsAsCallArguments(t *testing.T) {
	var seenQuery string
	srv, host := newDeclarativeServer(t, func(w http.ResponseWriter, r *http.Request) {
		seenQuery = r.URL.Query().Get("coin")
		fmt.Fprint(w, "status_ok")
	})

	dir := t.TempDir()
	toolDir := filepath.Join(dir, "priced")
	manifest := fmt.Sprintf(`
name = "priced"
description = "needs a coin param"

[params]
coin = { type = "string", required = false }

[request]
method = "GET"
url    = "%s/x"
query  = { coin = "{{.coin}}" }

[selftest]
args   = { coin = "BTC" }
expect = "status_ok"
`, srv.URL)
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(toolDir, ManifestFileName), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	tp := ToolProbe{Dir: dir, Allow: []string{host}}
	res, err := tp.Run(context.Background(), mustArgs(t, toolProbeArgs{Name: "priced"}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected the probe to pass, got: %s", res.Text)
	}
	if seenQuery != "BTC" {
		t.Errorf("server saw coin=%q, want BTC (from [selftest].args)", seenQuery)
	}
}

func TestToolProbeReprobingAfterFixUpdatesToVerified(t *testing.T) {
	var respond string
	srv, host := newDeclarativeServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, respond)
	})

	dir := t.TempDir()
	toolDir := filepath.Join(dir, "iterate")
	manifest := fmt.Sprintf(`
name = "iterate"
description = "fixed after a failed first probe"

[request]
method = "GET"
url = "%s/x"

[selftest]
expect = "status_ok"
`, srv.URL)
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(toolDir, ManifestFileName), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	tp := ToolProbe{Dir: dir, Allow: []string{host}}

	respond = "wrong output"
	res, err := tp.Run(context.Background(), mustArgs(t, toolProbeArgs{Name: "iterate"}))
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected the first probe to fail")
	}

	respond = "status_ok"
	res, err = tp.Run(context.Background(), mustArgs(t, toolProbeArgs{Name: "iterate"}))
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected the second probe to pass, got: %s", res.Text)
	}

	state, err := LoadState(toolDir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if state.State != StateVerified {
		t.Errorf("state = %q, want verified after the fixed re-probe", state.State)
	}
	if state.LastError != "" {
		t.Errorf("LastError = %q, want cleared after a passing probe", state.LastError)
	}
}

func TestToolProbeCancelledContextIsGoError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tp := ToolProbe{Dir: t.TempDir()}
	_, err := tp.Run(ctx, mustArgs(t, toolProbeArgs{Name: "anything"}))
	if err == nil {
		t.Error("expected the cancelled context's error to surface")
	}
}

func TestSetEnvTemporarilyNoopForEmptyMap(t *testing.T) {
	restore := setEnvTemporarily(nil)
	restore() // must not panic
	restore2 := setEnvTemporarily(map[string]string{})
	restore2() // must not panic
}

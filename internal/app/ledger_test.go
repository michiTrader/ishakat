package app

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichiTrader/ishakat/internal/engine"
	"github.com/MichiTrader/ishakat/internal/evolve"
)

func fixedNow(s string) func() time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return func() time.Time { return t }
}

func fakeRunner(text string, isError bool) engine.ToolRunner {
	return func(context.Context, string, json.RawMessage) (engine.ToolResult, error) {
		return engine.ToolResult{Text: text, IsError: isError}, nil
	}
}

func TestLedgerObservingRunnerRecordsBashCommand(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "usage.jsonl")
	run := ledgerObservingRunner(fakeRunner("ok", false), path, fixedNow("2026-08-11"))

	_, err := run(context.Background(), "bash", json.RawMessage(`{"command":"ls -la"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	l, err := evolve.LoadLedger(path)
	if err != nil {
		t.Fatalf("LoadLedger: %v", err)
	}
	if len(l.Records) != 1 {
		t.Fatalf("got %d records, want 1", len(l.Records))
	}
	if l.Records[0].Pattern != "ls -la" {
		t.Errorf("pattern = %q, want %q", l.Records[0].Pattern, "ls -la")
	}
	if l.Records[0].N != 1 {
		t.Errorf("N = %d, want 1", l.Records[0].N)
	}
	if l.Records[0].Last != "2026-08-11" {
		t.Errorf("last = %q, want %q", l.Records[0].Last, "2026-08-11")
	}
}

func TestLedgerObservingRunnerRecordsFetchURL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "usage.jsonl")
	run := ledgerObservingRunner(fakeRunner("ok", false), path, fixedNow("2026-08-11"))

	_, err := run(context.Background(), "fetch", json.RawMessage(`{"url":"https://example.com/a"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	l, err := evolve.LoadLedger(path)
	if err != nil {
		t.Fatalf("LoadLedger: %v", err)
	}
	if len(l.Records) != 1 {
		t.Fatalf("got %d records, want 1", len(l.Records))
	}
	if l.Records[0].Pattern != "https://example.com/a" {
		t.Errorf("pattern = %q, want %q", l.Records[0].Pattern, "https://example.com/a")
	}
}

func TestLedgerObservingRunnerIgnoresOtherTools(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "usage.jsonl")
	run := ledgerObservingRunner(fakeRunner("ok", false), path, fixedNow("2026-08-11"))

	_, err := run(context.Background(), "read_file", json.RawMessage(`{"path":"a.txt"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	l, err := evolve.LoadLedger(path)
	if err != nil {
		t.Fatalf("LoadLedger: %v", err)
	}
	if len(l.Records) != 0 {
		t.Fatalf("got %d records, want 0 (read_file must not be observed)", len(l.Records))
	}
}

func TestLedgerObservingRunnerAccumulatesAcrossCalls(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "usage.jsonl")
	run := ledgerObservingRunner(fakeRunner("ok", false), path, fixedNow("2026-08-11"))

	for _, cmd := range []string{
		`{"command":"curl -s api.bybit.com/v5/market/tickers?symbol=BTCUSDT"}`,
		`{"command":"curl -s api.bybit.com/v5/market/tickers?symbol=ETHUSDT"}`,
		`{"command":"curl -s api.bybit.com/v5/market/tickers?symbol=SOLUSDT"}`,
	} {
		if _, err := run(context.Background(), "bash", json.RawMessage(cmd)); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	l, err := evolve.LoadLedger(path)
	if err != nil {
		t.Fatalf("LoadLedger: %v", err)
	}
	if len(l.Records) != 1 {
		t.Fatalf("got %d records, want 1 (all three should merge into one shape)", len(l.Records))
	}
	if l.Records[0].N != 3 {
		t.Errorf("N = %d, want 3", l.Records[0].N)
	}
	want := "curl -s api.bybit.com/v5/market/tickers*"
	if l.Records[0].Pattern != want {
		t.Errorf("pattern = %q, want %q", l.Records[0].Pattern, want)
	}
}

func TestLedgerObservingRunnerRecordsEvenOnToolError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "usage.jsonl")
	run := ledgerObservingRunner(fakeRunner("boom", true), path, fixedNow("2026-08-11"))

	res, err := run(context.Background(), "bash", json.RawMessage(`{"command":"false"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected the wrapped runner's IsError to pass through unchanged")
	}

	l, err := evolve.LoadLedger(path)
	if err != nil {
		t.Fatalf("LoadLedger: %v", err)
	}
	if len(l.Records) != 1 {
		t.Fatalf("got %d records, want 1 (a failed call is still evidence of what was asked)", len(l.Records))
	}
}

func TestLedgerObservingRunnerNilRunnerReturnsNil(t *testing.T) {
	if got := ledgerObservingRunner(nil, "/tmp/whatever.jsonl", nil); got != nil {
		t.Error("nil next runner must return nil, matching ToolRunnerWithGuard's own nil-guard shape")
	}
}

func TestLedgerObservingRunnerEmptyPathReturnsNextUnchanged(t *testing.T) {
	next := fakeRunner("hi", false)
	got := ledgerObservingRunner(next, "", nil)

	res, err := got(context.Background(), "bash", json.RawMessage(`{"command":"ls"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Text != "hi" {
		t.Errorf("got %q, want %q (empty path must be a no-op wrapper)", res.Text, "hi")
	}
}

func TestRawInvocationExtractsBashCommand(t *testing.T) {
	raw, ok := rawInvocation("bash", json.RawMessage(`{"command":"echo hi","timeout_seconds":5}`))
	if !ok {
		t.Fatal("expected ok=true for a valid bash call")
	}
	if raw != "echo hi" {
		t.Errorf("got %q, want %q", raw, "echo hi")
	}
}

func TestRawInvocationExtractsFetchURL(t *testing.T) {
	raw, ok := rawInvocation("fetch", json.RawMessage(`{"url":"https://example.com"}`))
	if !ok {
		t.Fatal("expected ok=true for a valid fetch call")
	}
	if raw != "https://example.com" {
		t.Errorf("got %q, want %q", raw, "https://example.com")
	}
}

func TestRawInvocationIgnoresUnknownToolNames(t *testing.T) {
	if _, ok := rawInvocation("write_file", json.RawMessage(`{"path":"a.txt"}`)); ok {
		t.Error("expected ok=false for a non-bash/fetch tool name")
	}
}

func TestRawInvocationRejectsEmptyValues(t *testing.T) {
	if _, ok := rawInvocation("bash", json.RawMessage(`{"command":""}`)); ok {
		t.Error("expected ok=false for an empty command")
	}
	if _, ok := rawInvocation("fetch", json.RawMessage(`{"url":""}`)); ok {
		t.Error("expected ok=false for an empty url")
	}
}

func TestRawInvocationRejectsMalformedJSON(t *testing.T) {
	if _, ok := rawInvocation("bash", json.RawMessage(`not json`)); ok {
		t.Error("expected ok=false for malformed arguments")
	}
}

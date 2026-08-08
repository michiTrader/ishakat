package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/provider/fake"
)

// TestHeadlessAgentLoopToolCallThenAnswer is §12bis's own closing criterion:
// `ishakat -p "…"` with cfg.Tools.Enabled=true and a real tool (read_file,
// not a fake ToolRunner) producing a correct answer through an actual tool
// call. The fake provider plays two turns: the first asks for read_file on a
// real file this test writes to t.TempDir(), the second — once the tool's
// result is back in context — answers with text derived from that content.
func TestHeadlessAgentLoopToolCallThenAnswer(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "greeting.txt")
	if err := os.WriteFile(target, []byte("hola desde el archivo"), 0o600); err != nil {
		t.Fatalf("could not write fixture file: %v", err)
	}

	argsJSON, err := json.Marshal(map[string]string{"path": target})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}

	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		write := func(s string) {
			_, _ = w.Write([]byte(s))
			if fl != nil {
				fl.Flush()
			}
		}
		if n == 1 {
			// First turn: ask for read_file on the fixture.
			write(fake.SSEChunk(fmt.Sprintf(
				`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c1","function":{"name":"read_file","arguments":%s}}]}}]}`,
				quoteJSON(string(argsJSON)))))
			write(fake.SSEChunk(`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`))
			write(fake.SSEDone())
			return
		}
		// Second turn: the tool result is in context; answer with text.
		write(fake.SSEDelta("the file says: hola desde el archivo"))
		write(fake.SSEDone())
	}))
	defer srv.Close()

	cfg := cfgFor(t, srv.URL)
	cfg.Tools = config.Tools{Enabled: true, MaxCallsPerTurn: 5, MaxOutputBytes: 4096}

	code, out, errs := run(t, HeadlessOptions{Config: cfg, Prompt: "read the greeting file and tell me what it says"})
	if code != ExitOK {
		t.Fatalf("code = %d, expected 0. stderr: %s", code, errs)
	}
	if !strings.Contains(out, "hola desde el archivo") {
		t.Errorf("stdout must carry the tool's real content, got: %q", out)
	}
	if n := attempts.Load(); n != 2 {
		t.Errorf("expected 2 requests (tool call + follow-up), got %d", n)
	}
	// The tool call itself is reported on stderr (textSink.tool), never on
	// stdout — same contract runTurn's own path already keeps.
	if !strings.Contains(errs, "read_file") {
		t.Errorf("stderr should report the tool call, got: %q", errs)
	}
}

// TestHeadlessAgentLoopPersistsEachMessage proves runAgentTurnHeadless's own
// contract: every message the loop produces (the assistant's tool-call
// turn, the tool result, the final assistant text) lands in the session
// file individually — not a single collapsed summary — so --resume (once it
// exists) sees the same shape the provider actually produced.
func TestHeadlessAgentLoopPersistsEachMessage(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(target, []byte("contenido de prueba"), 0o600); err != nil {
		t.Fatalf("could not write fixture file: %v", err)
	}
	argsJSON, err := json.Marshal(map[string]string{"path": target})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}

	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		write := func(s string) {
			_, _ = w.Write([]byte(s))
			if fl != nil {
				fl.Flush()
			}
		}
		if n == 1 {
			write(fake.SSEChunk(fmt.Sprintf(
				`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c1","function":{"name":"read_file","arguments":%s}}]}}]}`,
				quoteJSON(string(argsJSON)))))
			write(fake.SSEChunk(`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`))
			write(fake.SSEDone())
			return
		}
		write(fake.SSEDelta("done"))
		write(fake.SSEDone())
	}))
	defer srv.Close()

	sessDir := t.TempDir()
	cfg := cfgFor(t, srv.URL)
	cfg.Tools = config.Tools{Enabled: true, MaxCallsPerTurn: 5, MaxOutputBytes: 4096}
	cfg.Session.Save = true
	cfg.Session.Dir = sessDir

	code, _, errs := run(t, HeadlessOptions{Config: cfg, Prompt: "read the note"})
	if code != ExitOK {
		t.Fatalf("code = %d, expected 0. stderr: %s", code, errs)
	}

	entries, err := os.ReadDir(sessDir)
	if err != nil {
		t.Fatalf("could not read session dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 session file, found %d", len(entries))
	}
	raw, err := os.ReadFile(filepath.Join(sessDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("could not read session file: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	// header + user + assistant(tool_call) + tool(result) + assistant(text)
	// = 5 lines. The precise count matters: it is what proves the loop's
	// intermediate messages were persisted individually, not collapsed.
	if len(lines) != 5 {
		t.Errorf("expected 5 JSONL lines (header, user, assistant tool_call, tool result, final assistant), got %d:\n%s", len(lines), raw)
	}
	if !strings.Contains(string(raw), "read_file") {
		t.Errorf("session file must record the read_file call, got:\n%s", raw)
	}
	if !strings.Contains(string(raw), "contenido de prueba") {
		t.Errorf("session file must record the tool's real result, got:\n%s", raw)
	}
}

// quoteJSON re-quotes an already-serialized JSON string as a JSON string
// literal, the same way the arguments field of a wire tool_calls delta
// carries its payload (a string, not a nested object) per the OpenAI
// dialect.
func quoteJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

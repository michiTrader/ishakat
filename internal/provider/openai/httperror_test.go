package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MichiTrader/ishakat/internal/convo"
	"github.com/MichiTrader/ishakat/internal/provider"
)

// errorBodyServer replies to every request with status and body, so a test can
// pin what the user ends up reading.
func errorBodyServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// errText drives a real turn against srv and returns the error string the
// interface would show.
func errText(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	p, err := New(provider.Settings{ID: "google", BaseURL: srv.URL, APIKey: "k"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// A request with no messages is rejected locally, before any HTTP call,
	// so it would never reach the server this test is about.
	_, err = p.Stream(context.Background(), provider.Request{
		Model:    "m",
		Stream:   true,
		Messages: []convo.Message{convo.User("hola")},
	})
	if err == nil {
		t.Fatal("expected an error from a non-200 response")
	}
	return err.Error()
}

// TestHTTPErrorReadsGeminiArrayEnvelope is the regression test for an error
// message that destroyed its own diagnostic. A user on Gemini saw exactly
// `⚠ google: HTTP 400: [{` on every failing turn — enough to know
// something broke and nothing else, which is worse than no message at all
// because it looks like the program already explained itself.
//
// Two things combined to produce it. Gemini's OpenAI-compatible layer wraps
// its errors in a JSON *array*, which the object decode could not read; and
// the fallback used firstLine on a pretty-printed body, so it cut at the first
// newline and returned the opening bracket.
func TestHTTPErrorReadsGeminiArrayEnvelope(t *testing.T) {
	// The real shape, pretty-printed the way Gemini sends it.
	body := `[
  {
    "error": {
      "code": 400,
      "message": "Unable to submit request because it has an unrecognized field \"index\" in tool_calls.",
      "status": "INVALID_ARGUMENT"
    }
  }
]`
	got := errText(t, errorBodyServer(t, http.StatusBadRequest, body))

	if !strings.Contains(got, "unrecognized field") {
		t.Errorf("error text = %q; it must carry the service's own explanation, "+
			"which is the only thing that makes a 400 actionable", got)
	}
	if strings.HasSuffix(strings.TrimSpace(got), "[{") {
		t.Errorf("error text = %q: this is the original bug — the diagnostic was "+
			"truncated to the opening bracket of a pretty-printed array", got)
	}
}

// TestHTTPErrorStillReadsObjectEnvelope guards the common case while fixing
// the array one: OpenAI, OmniRoute and DeepSeek all use `{"error": {...}}`.
func TestHTTPErrorStillReadsObjectEnvelope(t *testing.T) {
	body := `{"error":{"message":"model not found","type":"invalid_request_error","code":"model_not_found"}}`
	got := errText(t, errorBodyServer(t, http.StatusNotFound, body))

	if !strings.Contains(got, "model not found") {
		t.Errorf("error text = %q, want the service's message", got)
	}
}

// TestHTTPErrorOnUnparseableBodyKeepsSomethingReadable covers the last rung.
// A gateway in trouble answers with an HTML page or a bare string, and the
// user still deserves more than a status code. The body is collapsed onto one
// line so truncation keeps words rather than punctuation.
func TestHTTPErrorOnUnparseableBodyKeepsSomethingReadable(t *testing.T) {
	body := "<html>\n  <head><title>502 Bad Gateway</title></head>\n  <body>upstream connect error</body>\n</html>"
	got := errText(t, errorBodyServer(t, http.StatusBadGateway, body))

	if !strings.Contains(got, "502 Bad Gateway") {
		t.Errorf("error text = %q; a body that is not JSON must still reach the user "+
			"in readable form, collapsed rather than cut at the first newline", got)
	}
}

// TestCollapseJSONTruncatesOnRuneBoundaries pins the detail that a byte-slice
// cut would get wrong: truncating mid-character puts a replacement glyph in
// the middle of an error message, which reads like a second, imaginary bug.
func TestCollapseJSONTruncatesOnRuneBoundaries(t *testing.T) {
	got := collapseJSON(strings.Repeat("é", 50), 10)
	runes := []rune(got)
	if len(runes) != 11 { // 10 kept + the ellipsis
		t.Fatalf("got %d runes (%q), want 10 plus an ellipsis", len(runes), got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncated text %q should end with an ellipsis", got)
	}
	if strings.ContainsRune(got, '\uFFFD') {
		t.Error("truncation split a multi-byte character")
	}
}

// TestCollapseJSONFlattensWhitespace states the property the fix depends on:
// a pretty-printed body becomes one line, so a length cap keeps information.
func TestCollapseJSONFlattensWhitespace(t *testing.T) {
	got := collapseJSON("[\n  {\n    \"error\": {\n      \"message\": \"boom\"\n    }\n  }\n]", 300)
	if strings.ContainsAny(got, "\n\r\t") {
		t.Errorf("collapsed text still contains line breaks: %q", got)
	}
	if !strings.Contains(got, "boom") {
		t.Errorf("collapsed text lost the message: %q", got)
	}
}

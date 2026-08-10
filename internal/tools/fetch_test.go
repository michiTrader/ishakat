package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newFetchServer builds an httptest.Server serving body with contentType,
// and returns a Fetch configured to reach only that server's host — the
// allowlist has to name the real ephemeral host httptest picks, which is
// why every test in this file builds its own Fetch value instead of
// sharing one with a fixed Allow list.
func newFetchServer(t *testing.T, contentType, body string) (*httptest.Server, Fetch) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	host := strings.TrimPrefix(strings.TrimPrefix(srv.URL, "http://"), "https://")
	// srv.URL's host includes the port (127.0.0.1:PORT); hostAllowed
	// matches on Hostname() alone (port stripped), so the allow entry
	// must be just the host part.
	host = host[:strings.Index(host, ":")]
	return srv, Fetch{Allow: []string{host}, HTTPClient: srv.Client()}
}

func TestFetchPlainTextPassesThrough(t *testing.T) {
	srv, f := newFetchServer(t, "text/plain", "hello, world")
	res, err := f.Run(context.Background(), mustArgs(t, fetchArgs{URL: srv.URL}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Text)
	}
	if res.Text != "hello, world" {
		t.Errorf("got %q, want %q", res.Text, "hello, world")
	}
}

func TestFetchStripsHTMLTagsScriptsAndStyles(t *testing.T) {
	html := `<html><head><style>.x{color:red}</style></head>
<body>
<script>alert("nope")</script>
<h1>Title</h1>
<p>First paragraph.</p>
<p>Second paragraph with <b>bold</b> text.</p>
</body></html>`
	srv, f := newFetchServer(t, "text/html", html)
	res, err := f.Run(context.Background(), mustArgs(t, fetchArgs{URL: srv.URL}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Text)
	}
	for _, want := range []string{"Title", "First paragraph.", "Second paragraph with bold text."} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("output missing %q, got: %s", want, res.Text)
		}
	}
	for _, unwanted := range []string{"<h1>", "<p>", "<script>", "alert(", "color:red"} {
		if strings.Contains(res.Text, unwanted) {
			t.Errorf("output should not contain %q, got: %s", unwanted, res.Text)
		}
	}
}

func TestFetchUnescapesHTMLEntities(t *testing.T) {
	srv, f := newFetchServer(t, "text/html", "<p>Tom &amp; Jerry &mdash; caf&eacute;</p>")
	res, err := f.Run(context.Background(), mustArgs(t, fetchArgs{URL: srv.URL}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(res.Text, "Tom & Jerry") {
		t.Errorf("entities not unescaped, got: %q", res.Text)
	}
	if !strings.Contains(res.Text, "café") {
		t.Errorf("named entity not unescaped, got: %q", res.Text)
	}
}

func TestFetchSniffsHTMLWithoutContentType(t *testing.T) {
	srv, f := newFetchServer(t, "", "<p>sniffed</p>")
	res, err := f.Run(context.Background(), mustArgs(t, fetchArgs{URL: srv.URL}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(res.Text, "sniffed") || strings.Contains(res.Text, "<p>") {
		t.Errorf("expected HTML to be sniffed and stripped, got: %q", res.Text)
	}
}

func TestFetchJSONPassesThroughUnmangled(t *testing.T) {
	body := `{"a": 1, "b": "<not-a-tag>"}`
	srv, f := newFetchServer(t, "application/json", body)
	res, err := f.Run(context.Background(), mustArgs(t, fetchArgs{URL: srv.URL}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Text != body {
		t.Errorf("JSON body should pass through unchanged, got %q, want %q", res.Text, body)
	}
}

func TestFetchDeniesHostNotOnAllowlist(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	f := Fetch{Allow: []string{"example.com"}, HTTPClient: srv.Client()}
	res, err := f.Run(context.Background(), mustArgs(t, fetchArgs{URL: srv.URL}))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected a denial for a host not on the allowlist")
	}
	if !strings.Contains(res.Text, "not on the egress allowlist") {
		t.Errorf("denial message should explain why, got: %q", res.Text)
	}
}

func TestFetchAllowAllBypassesAllowlist(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	f := Fetch{AllowAll: true, HTTPClient: srv.Client()}
	res, err := f.Run(context.Background(), mustArgs(t, fetchArgs{URL: srv.URL}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Text)
	}
	if res.Text != "ok" {
		t.Errorf("got %q, want %q", res.Text, "ok")
	}
}

func TestFetchWildcardAllowsSubdomain(t *testing.T) {
	// hostAllowed is exercised directly here (rather than through a real
	// subdomain, which httptest cannot give us) — Run's own allowlist
	// plumbing is already covered by the allow/deny tests above, so this
	// isolates the *.example.com matching rule itself.
	cases := []struct {
		host  string
		allow []string
		want  bool
	}{
		{"raw.githubusercontent.com", []string{"*.githubusercontent.com"}, true},
		{"githubusercontent.com", []string{"*.githubusercontent.com"}, true},
		{"evilgithubusercontent.com", []string{"*.githubusercontent.com"}, false},
		{"api.github.com", []string{"api.github.com"}, true},
		{"API.GitHub.com", []string{"api.github.com"}, true},
		{"sub.api.github.com", []string{"api.github.com"}, false},
	}
	for _, c := range cases {
		if got := hostAllowed(c.host, c.allow); got != c.want {
			t.Errorf("hostAllowed(%q, %v) = %v, want %v", c.host, c.allow, got, c.want)
		}
	}
}

func TestFetchRejectsNonHTTPScheme(t *testing.T) {
	f := Fetch{AllowAll: true}
	res, err := f.Run(context.Background(), mustArgs(t, fetchArgs{URL: "file:///etc/passwd"}))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected an error result for a non-http(s) scheme")
	}
	if !strings.Contains(res.Text, "only http and https") {
		t.Errorf("error should explain the scheme restriction, got: %q", res.Text)
	}
}

func TestFetchMissingURLIsGoError(t *testing.T) {
	f := Fetch{AllowAll: true}
	_, err := f.Run(context.Background(), mustArgs(t, fetchArgs{URL: ""}))
	if err == nil {
		t.Fatal("expected a Go error for a missing url")
	}
}

func TestFetchInvalidJSONIsGoError(t *testing.T) {
	f := Fetch{AllowAll: true}
	_, err := f.Run(context.Background(), []byte(`not json`))
	if err == nil {
		t.Fatal("expected a Go error for invalid arguments JSON")
	}
}

func TestFetchNonSuccessStatusIsErrorResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	f := Fetch{AllowAll: true, HTTPClient: srv.Client()}
	res, err := f.Run(context.Background(), mustArgs(t, fetchArgs{URL: srv.URL}))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected an error result for a 404 response")
	}
	if !strings.Contains(res.Text, "404") {
		t.Errorf("error should mention the status code, got: %q", res.Text)
	}
}

func TestFetchTruncatesOversizedOutput(t *testing.T) {
	big := strings.Repeat("a", maxFetchOutputBytes+1000)
	srv, f := newFetchServer(t, "text/plain", big)
	res, err := f.Run(context.Background(), mustArgs(t, fetchArgs{URL: srv.URL}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Text)
	}
	if len(res.Text) > maxFetchOutputBytes+200 {
		t.Errorf("output not truncated: got %d bytes", len(res.Text))
	}
	if !strings.Contains(res.Text, "truncated") {
		t.Error("truncated output should say so")
	}
}

func TestFetchNoBodyIsOKNotError(t *testing.T) {
	srv, f := newFetchServer(t, "text/html", "   ")
	res, err := f.Run(context.Background(), mustArgs(t, fetchArgs{URL: srv.URL}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError {
		t.Fatalf("an empty page is not a tool error, got: %s", res.Text)
	}
	if !strings.Contains(res.Text, "no readable text") {
		t.Errorf("expected a message about no readable text, got: %q", res.Text)
	}
}

func TestFetchCancelledContextIsGoError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	f := Fetch{AllowAll: true, HTTPClient: srv.Client()}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := f.Run(ctx, mustArgs(t, fetchArgs{URL: srv.URL}))
	if err == nil {
		t.Fatal("expected a Go error when the caller's own context is already cancelled")
	}
}

func TestFetchDefaultDangerIsLow(t *testing.T) {
	f := Fetch{}
	if f.Danger() != DangerLow {
		t.Error("fetch's Danger tier must be Low")
	}
	if f.Name() != "fetch" {
		t.Errorf("Name() = %q, want %q", f.Name(), "fetch")
	}
}

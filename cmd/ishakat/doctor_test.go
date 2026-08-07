package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestHTTPSProbeReportsRealRoundTrip covers the closing criterion §13bis
// states in plain words: "doctor reports a successful HTTPS request to a
// remote host", not a DNS lookup wearing that name. A DNS-only check would
// have stayed green through the android/arm64 CGO bug (§3) — the resolver
// finds an address, TLS/HTTP still never happens — so this test asserts on
// the HTTP round trip specifically, via a local server standing in for
// models.dev.
func TestHTTPSProbeReportsRealRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Errorf("method = %s, want HEAD (a doctor probe must not download the payload)", r.Method)
		}
		if ua := r.Header.Get("User-Agent"); !strings.HasPrefix(ua, "ishakat-doctor/") {
			t.Errorf("User-Agent = %q, want the ishakat-doctor/ prefix", ua)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	got := httpsProbe(srv.URL)
	if !strings.HasPrefix(got, "OK (HTTP 200") {
		t.Errorf("httpsProbe(reachable server) = %q, want it to start with %q", got, "OK (HTTP 200")
	}
}

// TestHTTPSProbeReportsFailureHonestly covers the other half of the same
// contract: an unreachable host must say FALLÓ, not silently report OK
// because DNS alone resolved something. This is what would have surfaced the
// android/arm64 bug immediately instead of weeks later.
func TestHTTPSProbeReportsFailureHonestly(t *testing.T) {
	// Port 0 on loopback: nothing is listening, so the dial itself fails
	// (connection refused) — no live network access needed for this test.
	got := httpsProbe("http://127.0.0.1:1/api.json")
	if !strings.HasPrefix(got, "FALLÓ:") {
		t.Errorf("httpsProbe(unreachable) = %q, want it to start with %q", got, "FALLÓ:")
	}
}

// TestHTTPSProbeReportsHTTPErrorStatus covers the case DNS+TLS+connect all
// succeed but the server itself answers with an error — a different failure
// mode from "unreachable" and one a naive err == nil check would miss.
func TestHTTPSProbeReportsHTTPErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	got := httpsProbe(srv.URL)
	if !strings.HasPrefix(got, "FALLÓ: HTTP 503") {
		t.Errorf("httpsProbe(503 server) = %q, want it to start with %q", got, "FALLÓ: HTTP 503")
	}
}

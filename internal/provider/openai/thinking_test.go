// thinking_test.go pins thought summaries on Google's OpenAI-compatible shim:
// the extra_body opt-in on the way out, and thought-marked content becoming
// provider.EventReasoning on the way back.
//
// This is the path that actually matters for a user on `google`: the
// "gemini" credentials preset is Kind "openai", not the native adapter, so
// fixing only internal/provider/gemini would have left the reported symptom
// exactly where it was — a reasoning preview that never shows anything.
//
// The host gate gets as much attention as the feature, because extra_body is
// Gemini-specific: the risk of this change is not that it fails to ask, it is
// that it asks everyone.
package openai_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/MichiTrader/ishakat/internal/provider"
)

// googleBase is Google's documented OpenAI-compatibility base URL, which is
// also what the "gemini" credentials preset uses.
const googleBase = "https://generativelanguage.googleapis.com/v1beta/openai"

// bodyOf runs one turn against a fake server and returns the request body it
// received, so a test can assert on what went out on the wire.
func bodyOf(t *testing.T, base string, req provider.Request, sse string) map[string]any {
	t.Helper()

	var got map[string]any
	srv := sseServer(t, []string{fixture(t, sse)}, func(_ *http.Request, body []byte) {
		if err := json.Unmarshal(body, &got); err != nil {
			t.Errorf("request body was not valid JSON: %v", err)
		}
	})

	// The provider must believe it is talking to `base` while the bytes go to
	// the test server, which is the only way to exercise the host gate without
	// reaching the network: BaseURL decides the gate, the transport decides
	// where it lands.
	p := newProvider(t, srv.URL, func(s *provider.Settings) {
		s.BaseURL = base
		s.HTTPClient = srv.Client()
		s.HTTPClient.Transport = redirectTo(srv.URL)
	})

	ch, err := p.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	drain(t, ch)
	return got
}

// redirectTo sends every request to the test server regardless of the URL the
// adapter built, so the adapter can be pointed at a real hostname it must
// recognise without any traffic leaving the process.
func redirectTo(target string) http.RoundTripper {
	return roundTripFunc(func(r *http.Request) (*http.Response, error) {
		u, err := http.NewRequest(r.Method, target+r.URL.Path, r.Body)
		if err != nil {
			return nil, err
		}
		u.Header = r.Header
		return http.DefaultTransport.RoundTrip(u.WithContext(r.Context()))
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// thinkingConfig digs out extra_body.google.thinking_config, returning nil
// when any level of the nesting is absent.
func thinkingConfig(body map[string]any) map[string]any {
	eb, ok := body["extra_body"].(map[string]any)
	if !ok {
		return nil
	}
	g, ok := eb["google"].(map[string]any)
	if !ok {
		return nil
	}
	tc, _ := g["thinking_config"].(map[string]any)
	return tc
}

// TestBuildBodyAsksGoogleForThoughtSummaries is the sending half. Google's
// thinking models narrate their reasoning only when the request opts in, and
// on this endpoint the opt-in lives inside extra_body under the provider key.
func TestBuildBodyAsksGoogleForThoughtSummaries(t *testing.T) {
	req := hola()
	req.IncludeReasoning = true

	body := bodyOf(t, googleBase, req, "stream_pensamiento_google.sse")

	tc := thinkingConfig(body)
	if tc == nil {
		t.Fatalf("extra_body.google.thinking_config missing from the body: %v", body)
	}
	if tc["include_thoughts"] != true {
		t.Errorf("include_thoughts = %v, want true", tc["include_thoughts"])
	}

	// reasoning_effort sets how much the model thinks, never whether the
	// thinking comes back, and Google documents that it cannot be combined
	// with thinking_config. Sending both would spend tokens and still show
	// nothing.
	if _, ok := body["reasoning_effort"]; ok {
		t.Error("reasoning_effort must not be sent alongside thinking_config: Google rejects the combination")
	}
}

// TestBuildBodyOmitsThinkingConfigByDefault is the regression guard: a turn
// that did not ask for reasoning must serialize exactly as it did before this
// feature existed. Reasoning tokens are billed, so asking by default would
// spend the user's money without being told to.
func TestBuildBodyOmitsThinkingConfigByDefault(t *testing.T) {
	body := bodyOf(t, googleBase, hola(), "stream_normal.sse") // IncludeReasoning false

	if _, ok := body["extra_body"]; ok {
		t.Errorf("extra_body must not appear when reasoning was not asked for: %v", body["extra_body"])
	}
}

// TestBuildBodyDoesNotSendGoogleFieldsToOtherHosts is the blast-radius guard,
// and the one that keeps this fix from becoming a new bug. extra_body.google
// is Gemini's private extension: OpenAI, Groq and DeepSeek would ignore or
// reject it, and they already narrate reasoning through reasoning_content with
// no opt-in at all. A Gemini model reached *through* OmniRoute is likewise not
// on a Google host.
func TestBuildBodyDoesNotSendGoogleFieldsToOtherHosts(t *testing.T) {
	for _, base := range []string{
		"https://api.openai.com/v1",
		"https://openrouter.ai/api/v1",
		"https://api.deepseek.com/v1",
		// A host that merely mentions Google in a path or query must not pass
		// the gate: substring matching would have accepted this.
		"https://evil.example.com/v1?upstream=generativelanguage.googleapis.com",
	} {
		t.Run(base, func(t *testing.T) {
			req := hola()
			req.IncludeReasoning = true

			body := bodyOf(t, base, req, "stream_normal.sse")

			if _, ok := body["extra_body"]; ok {
				t.Errorf("extra_body leaked to a non-Google host %q: %v", base, body["extra_body"])
			}
		})
	}
}

// TestParamsCanOverrideThinkingConfig pins the escape hatch. The opt-in is
// written before the [provider.params] overrides on purpose: Google has
// changed the shape of these fields before, and when it does the user must be
// able to correct or remove it from the TOML instead of waiting for a release.
func TestParamsCanOverrideThinkingConfig(t *testing.T) {
	req := hola()
	req.IncludeReasoning = true
	req.Params = map[string]any{"extra_body": nil} // nil deletes the key

	body := bodyOf(t, googleBase, req, "stream_normal.sse")

	if _, ok := body["extra_body"]; ok {
		t.Errorf("params must be able to delete extra_body, got %v", body["extra_body"])
	}
}

// TestStreamThoughtContentBecomesReasoning is the receiving half, and the
// reason the wire flag has to be read at all: on this shim a thought summary
// arrives in the ordinary `content` field, distinguished only by
// extra_content.google.thought. Treating it as answer text is precisely the
// symptom the user reported on a gateway that flattens reasoning into content
// — headings like "**Defining the Query**" printed as if they were the reply.
func TestStreamThoughtContentBecomesReasoning(t *testing.T) {
	srv := sseServer(t, []string{fixture(t, "stream_pensamiento_google.sse")}, nil)

	req := hola()
	req.IncludeReasoning = true

	p := newProvider(t, srv.URL)
	ch, err := p.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	d := drain(t, ch)

	if want := "The user greets me. A short reply fits."; d.reasoning != want {
		t.Errorf("reasoning = %q, want %q", d.reasoning, want)
	}
	if want := "Hola, ishakat en línea."; d.text != want {
		t.Errorf("answer = %q, want %q", d.text, want)
	}
	if strings.Contains(d.text, "greets") {
		t.Error("a thought leaked into the answer: reasoning must never be concatenated into EventDelta")
	}
}

// TestStreamReasoningTokensReportedSeparately pins the accounting: Google
// reports thinking tokens inside completion_tokens, so they land in
// Usage.Reasoning and are discounted from Out rather than counted twice.
func TestStreamReasoningTokensReportedSeparately(t *testing.T) {
	srv := sseServer(t, []string{fixture(t, "stream_pensamiento_google.sse")}, nil)

	req := hola()
	req.IncludeReasoning = true

	p := newProvider(t, srv.URL)
	ch, err := p.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	d := drain(t, ch)

	if d.usage == nil {
		t.Fatal("usage never reached Usage")
	}
	if d.usage.Reasoning != 40 {
		t.Errorf("Usage.Reasoning = %d, want 40 from reasoning_tokens", d.usage.Reasoning)
	}
	if d.usage.Out != 12 {
		t.Errorf("Usage.Out = %d, want 12 (52 completion - 40 reasoning): thought tokens must not be double-counted", d.usage.Out)
	}
}

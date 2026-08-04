package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MichiTrader/ishakat/internal/convo"
	"github.com/MichiTrader/ishakat/internal/provider"
)

func TestResponsesStreamUsesResponsesEndpointAndEvents(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("request path = %q, want /responses", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("authorization = %q, want bearer token", got)
		}
		var body struct {
			Model string `json:"model"`
			Input []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"input"`
			Stream bool `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body.Model != "gpt-5.6-sol" || len(body.Input) != 1 || body.Input[0].Content != "hello" || !body.Stream {
			t.Fatalf("unexpected Responses request: %#v", body)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n")
		_, _ = io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":3,\"output_tokens\":2}}}\n\n")
	}))
	defer server.Close()

	p, err := New(provider.Settings{
		ID:             "aerolink",
		Kind:           "responses",
		BaseURL:        server.URL,
		APIKey:         "test-key",
		Timeout:        time.Second,
		ConnectTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ch, err := p.Stream(context.Background(), provider.Request{
		Model:    "gpt-5.6-sol",
		Messages: []convo.Message{convo.User("hello")},
		Stream:   true,
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	var text string
	var usage *convo.Usage
	var done bool
	for event := range ch {
		switch event.Kind {
		case provider.EventDelta:
			text += event.Text
		case provider.EventUsage:
			usage = event.Usage
		case provider.EventDone:
			done = true
		case provider.EventError:
			t.Fatalf("stream error: %v", event.Err)
		}
	}
	if text != "hi" || usage == nil || usage.In != 3 || usage.Out != 2 || !done {
		t.Fatalf("events produced text=%q usage=%#v done=%v", text, usage, done)
	}
}

func TestResponsesCanBeSelectedWithWireAPIOnOpenAIKind(t *testing.T) {
	p, err := New(provider.Settings{
		ID:      "aerolink",
		Kind:    "openai",
		WireAPI: "responses",
		BaseURL: "https://example.invalid",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !strings.EqualFold(p.(*Provider).set.WireAPI, "responses") {
		t.Fatalf("wire API = %q, want responses", p.(*Provider).set.WireAPI)
	}
}

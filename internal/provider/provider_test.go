package provider_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MichiTrader/ishakat/internal/provider"
)

func TestStreamChatSuccess(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path inesperado: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("header Authorization incorrecto: %s", r.Header.Get("Authorization"))
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		events := []string{
			`data: {"id":"1","choices":[{"delta":{"role":"assistant","reasoning_content":"Pensando..."}}]}`,
			`data: {"id":"1","choices":[{"delta":{"content":"Hola "}}]}`,
			`data: {"id":"1","choices":[{"delta":{"content":"mundo"}}]}`,
			`data: {"id":"1","choices":[],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`,
			`data: [DONE]`,
		}

		for _, e := range events {
			_, _ = fmt.Fprintf(w, "%s\n\n", e)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
	defer ts.Close()

	client := provider.NewClient(ts.URL, "test-key", nil)

	var fullContent, fullReasoning string
	var finalUsage *provider.Usage
	var done bool

	err := client.StreamChat(context.Background(), provider.ChatRequest{
		Model: "test-model",
		Messages: []provider.ChatMessage{
			{Role: "user", Content: "Hola"},
		},
	}, func(ev provider.ChunkEvent) error {
		fullContent += ev.Content
		fullReasoning += ev.Reasoning
		if ev.Usage != nil {
			finalUsage = ev.Usage
		}
		if ev.Done {
			done = true
		}
		return nil
	})

	if err != nil {
		t.Fatalf("StreamChat falló inesperadamente: %v", err)
	}

	if !done {
		t.Error("esperado evento Done = true")
	}
	if fullContent != "Hola mundo" {
		t.Errorf("esperado 'Hola mundo', obtenido '%s'", fullContent)
	}
	if fullReasoning != "Pensando..." {
		t.Errorf("esperado reasoning 'Pensando...', obtenido '%s'", fullReasoning)
	}
	if finalUsage == nil || finalUsage.TotalTokens != 7 {
		t.Errorf("uso de tokens no coincide: %+v", finalUsage)
	}
}

func TestStreamChatErrorStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"api key invalida"}`))
	}))
	defer ts.Close()

	client := provider.NewClient(ts.URL, "invalid-key", nil)

	err := client.StreamChat(context.Background(), provider.ChatRequest{
		Model: "test-model",
	}, func(ev provider.ChunkEvent) error {
		return nil
	})

	if err == nil {
		t.Fatal("se esperaba error en status 401")
	}
}

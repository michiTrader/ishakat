// thinking_test.go pins the thought-summary opt-in of this dialect:
// generationConfig.thinkingConfig.includeThoughts on the way out, and
// `thought`-marked Parts becoming provider.EventReasoning on the way back.
//
// Both halves need pinning together because either one alone is silently
// useless. Before this, the receiving half already existed (stream.go's
// `case part.Thought`) and was unreachable: buildBody never asked for
// thoughts, so Google never sent a thought Part, so the interface showed an
// empty reasoning preview no matter what ui.reasoning was set to — a whole
// feature that looked implemented at every layer and produced nothing.
package gemini_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/MichiTrader/ishakat/internal/provider"
)

// TestBuildBodyOmitsThinkingConfigByDefault is the regression guard for the
// wire-compatibility half of the change: a turn that did not ask for
// reasoning must serialize exactly as it did before thinkingConfig existed.
// Sending it unconditionally would bill reasoning tokens nobody asked for
// (§4.2) and hand an extra field to every gateway that validates unknown
// fields strictly.
func TestBuildBodyOmitsThinkingConfigByDefault(t *testing.T) {
	var got map[string]any
	srv := sseServer(t, []string{fixture(t, "stream_normal.sse")}, func(_ *http.Request, body []byte) {
		_ = json.Unmarshal(body, &got)
	})

	p := newProvider(t, srv.URL)
	ch, err := p.Stream(context.Background(), hola()) // IncludeReasoning stays false
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	drain(t, ch)

	gc, ok := got["generationConfig"]
	if ok {
		if m, isMap := gc.(map[string]any); !isMap || m["thinkingConfig"] != nil {
			t.Errorf("generationConfig must not carry thinkingConfig when reasoning was not asked for: %v", gc)
		}
	}
}

// TestBuildBodyAsksForThoughtSummaries is the sending half: with
// IncludeReasoning set, the body must carry
// generationConfig.thinkingConfig.includeThoughts = true — the exact shape
// Google's own thinking guide documents for the native generateContent API.
// The literal `true` is asserted, not merely the key's presence: a
// `thinkingConfig: {}` (which is what an `omitempty` on the field would have
// produced) is accepted by the service and asks for nothing.
func TestBuildBodyAsksForThoughtSummaries(t *testing.T) {
	var got map[string]any
	srv := sseServer(t, []string{fixture(t, "stream_pensamiento.sse")}, func(_ *http.Request, body []byte) {
		_ = json.Unmarshal(body, &got)
	})

	req := hola()
	req.IncludeReasoning = true

	p := newProvider(t, srv.URL)
	ch, err := p.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	drain(t, ch)

	gc, ok := got["generationConfig"].(map[string]any)
	if !ok {
		t.Fatalf("generationConfig missing from the body: %v", got)
	}
	tc, ok := gc["thinkingConfig"].(map[string]any)
	if !ok {
		t.Fatalf("thinkingConfig missing from generationConfig: %v", gc)
	}
	if tc["includeThoughts"] != true {
		t.Errorf("includeThoughts = %v, want true; an empty thinkingConfig asks for nothing", tc["includeThoughts"])
	}
}

// TestBuildBodyParamsNestedKeySurvivesTypedGenerationConfig is F9's
// regression guard for applyParam's dotted-key extension on this dialect
// specifically: buildBody sets body["generationConfig"] as a *typed*
// wireGenConfig struct (not a map[string]any) whenever IncludeReasoning is
// true, so a params override like
// "generationConfig.thinkingConfig.thinkingLevel" must reach descend's
// JSON-round-trip branch (not its `nil` branch, which the openai dialect's
// own equivalent test exercises) — and the struct's own fields
// (includeThoughts, set by buildBody itself) must survive the round-trip
// instead of being silently discarded by a naive map overwrite.
//
// The value used is lowercase ("low"), not uppercase: confirmed against
// Google's own generateContent-specific thinking docs
// (https://ai.google.dev/gemini-api/docs/generate-content/thinking, the
// non-Interactions-API view — its own curl example shows
// "thinkingLevel": "low"), which is the authoritative source for THIS
// endpoint. The Discovery Document's OpenAPI schema shows the enum in
// uppercase, but that turned out to describe the schema's own
// documentation convention, not what the wire actually requires; the
// service's own docs win. app.EffortParams (internal/app/effort.go) is
// what actually produces this value at a real call site — it passes the
// model's own catalog.Model.EffortLevels string through unchanged, which
// per models.dev's own live snapshot is always lowercase already.
func TestBuildBodyParamsNestedKeySurvivesTypedGenerationConfig(t *testing.T) {
	var got map[string]any
	srv := sseServer(t, []string{fixture(t, "stream_pensamiento.sse")}, func(_ *http.Request, body []byte) {
		_ = json.Unmarshal(body, &got)
	})

	req := hola()
	req.IncludeReasoning = true // buildBody sets generationConfig as a wireGenConfig struct
	req.Params = map[string]any{
		"generationConfig.thinkingConfig.thinkingLevel": "low",
	}

	p := newProvider(t, srv.URL)
	ch, err := p.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	drain(t, ch)

	gc, ok := got["generationConfig"].(map[string]any)
	if !ok {
		t.Fatalf("generationConfig missing or not an object after the nested params override: %v", got)
	}
	tc, ok := gc["thinkingConfig"].(map[string]any)
	if !ok {
		t.Fatalf("thinkingConfig missing or not an object: %v", gc)
	}
	if tc["includeThoughts"] != true {
		t.Errorf("includeThoughts must survive the round-trip descend() does to reach thinkingLevel: %v", tc)
	}
	if tc["thinkingLevel"] != "low" {
		t.Errorf("thinkingLevel = %v, want \"low\" from the nested params override", tc["thinkingLevel"])
	}
}

// TestStreamThoughtPartsBecomeReasoningEvents is the receiving half: a
// `thought`-marked Part must arrive as EventReasoning and must NOT be
// concatenated into the answer. Keeping the two apart is the whole point of
// the separate event kind (provider.EventKind's own doc comment): flattening
// reasoning into EventDelta would make the interface print the model's
// scratch work as if it were the reply.
func TestStreamThoughtPartsBecomeReasoningEvents(t *testing.T) {
	srv := sseServer(t, []string{fixture(t, "stream_pensamiento.sse")}, nil)

	req := hola()
	req.IncludeReasoning = true

	p := newProvider(t, srv.URL)
	ch, err := p.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	var reasoning, text string
	for ev := range ch {
		switch ev.Kind {
		case provider.EventReasoning:
			reasoning += ev.Text
		case provider.EventDelta:
			text += ev.Text
		}
	}

	if want := "The user greets me. A short reply fits."; reasoning != want {
		t.Errorf("reasoning = %q, want %q", reasoning, want)
	}
	if want := "Hola, ishakat en línea."; text != want {
		t.Errorf("answer = %q, want %q", text, want)
	}
	if strings.Contains(text, "greets") {
		t.Error("a thought Part leaked into the answer: reasoning must never be concatenated into EventDelta")
	}
}

// TestStreamThoughtTokensReportedAsReasoningUsage pins the accounting side:
// thoughtsTokenCount lands in Usage.Reasoning rather than being folded into
// Out, so the footer and the cost ledger can tell "tokens the user read"
// from "tokens the model spent thinking" — two numbers billed the same and
// meaning different things.
func TestStreamThoughtTokensReportedAsReasoningUsage(t *testing.T) {
	srv := sseServer(t, []string{fixture(t, "stream_pensamiento.sse")}, nil)

	req := hola()
	req.IncludeReasoning = true

	p := newProvider(t, srv.URL)
	ch, err := p.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	d := drain(t, ch)

	if d.usage == nil {
		t.Fatal("usageMetadata never reached Usage")
	}
	if d.usage.Reasoning != 40 {
		t.Errorf("Usage.Reasoning = %d, want 40 from thoughtsTokenCount", d.usage.Reasoning)
	}
	if d.usage.Out != 12 {
		t.Errorf("Usage.Out = %d, want 12; thought tokens must not be folded into the output count", d.usage.Out)
	}
}

// thoughtsig_test.go pins the round-trip of Gemini's thought_signature.
//
// Every fixture in this file is a byte-for-byte capture of what
// generativelanguage.googleapis.com actually answered for
// gemini-3.1-flash-lite-preview, not a reconstruction from the docs. That
// matters because the two defects fixed here were both invisible to a reading
// of the specification: the signature lives in a field the OpenAI schema does
// not have, and Gemini omits `index` on streaming tool calls even for parallel
// calls, which no vendor documentation states.
//
// The failure these tests prevent is not subtle: without the signature coming
// back, Gemini 3 answers every turn after a tool call with
//
//	HTTP 400 Function call is missing a thought_signature in functionCall parts
//
// so the agent loop can never take a second step.
package openai

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichiTrader/ishakat/internal/convo"
	"github.com/MichiTrader/ishakat/internal/provider"
)

// liveSignature is a real signature as returned by the API. Its exact bytes do
// not matter to the code — it is opaque — but using a real one keeps the tests
// honest about length and alphabet (base64 with + and /).
const liveSignature = "EjQKMgERTTIP5dUAayC6LbAWlNm+V/Mc6murQghINat3P5cBUGQl5flFeHJPGX08FAInqF78"

// drainToolCalls collects the tool-call events a pump produced.
func drainToolCalls(t *testing.T, run func(ch chan provider.Event) error) []provider.Event {
	t.Helper()
	ch := make(chan provider.Event, 32)
	if err := run(ch); err != nil {
		t.Fatalf("pump returned an error: %v", err)
	}
	close(ch)
	var out []provider.Event
	for ev := range ch {
		if ev.Kind == provider.EventToolCall {
			out = append(out, ev)
		}
	}
	return out
}

// TestStreamCapturesThoughtSignature is the first half of the round-trip: the
// signature has to survive being read off the wire. This is the exact chunk
// Gemini sends for a single tool call.
func TestStreamCapturesThoughtSignature(t *testing.T) {
	body := `data: {"choices":[{"delta":{"role":"assistant","tool_calls":[{"extra_content":{"google":{"thought_signature":"` + liveSignature + `"}},"function":{"arguments":"{\"city\":\"Paris\"}","name":"get_weather"},"id":"z7uCjJDW","type":"function"}]},"index":0}],"model":"gemini-3.1-flash-lite-preview","object":"chat.completion.chunk"}

data: [DONE]

`
	p := &Provider{set: provider.Settings{ID: "gemini-direct"}}
	calls := drainToolCalls(t, func(ch chan provider.Event) error {
		return p.pumpSSE(context.Background(), strings.NewReader(body), ch)
	})

	if len(calls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(calls))
	}
	if calls[0].Signature != liveSignature {
		t.Errorf("Signature = %q, want the signature from the chunk.\n"+
			"Dropping it here is the whole bug: Gemini 3 answers HTTP 400 "+
			"on the next turn when the signature does not come back.",
			calls[0].Signature)
	}
	if calls[0].ID != "z7uCjJDW" {
		t.Errorf("ID = %q, want z7uCjJDW", calls[0].ID)
	}
}

// TestFromConvoReattachesThoughtSignature is the second half: the signature
// has to go back out in the shape Google validates. Asserting on the marshalled
// bytes rather than the struct is deliberate — the previous Gemini 400 was a
// key that existed on the wire and nowhere in any struct-level assertion.
func TestFromConvoReattachesThoughtSignature(t *testing.T) {
	history := []convo.Message{
		convo.User("weather in Paris?"),
		convo.NewMessage(convo.RoleAssistant,
			convo.ToolCallBlock("z7uCjJDW", "get_weather",
				json.RawMessage(`{"city":"Paris"}`)).WithSignature(liveSignature)),
		convo.NewMessage(convo.RoleTool,
			convo.ToolResultBlock("z7uCjJDW", "get_weather", `{"temp":"15C"}`)),
	}

	msgs, _ := FromConvo(history, provider.Caps{Tools: true})
	wire := marshalMessages(t, msgs)

	calls, ok := wire[1]["tool_calls"].([]any)
	if !ok || len(calls) != 1 {
		t.Fatalf("messages[1].tool_calls = %v, want one call", wire[1]["tool_calls"])
	}
	call := calls[0].(map[string]any)

	extra, ok := call["extra_content"].(map[string]any)
	if !ok {
		t.Fatalf("tool_calls[0] has no extra_content object; Gemini 3 rejects "+
			"this request with HTTP 400. Got keys: %v", keysOf(call))
	}
	google, ok := extra["google"].(map[string]any)
	if !ok {
		t.Fatalf("extra_content has no \"google\" object: the signature must be "+
			"namespaced by provider. Got: %v", extra)
	}
	if google["thought_signature"] != liveSignature {
		t.Errorf("thought_signature = %v, want it returned verbatim; Google "+
			"validates the exact bytes", google["thought_signature"])
	}
}

// TestFromConvoOmitsExtraContentWithoutSignature is the blast-radius test, and
// the reason `extra_content` is a pointer with omitempty.
//
// Every other provider ishakat talks to — OpenAI, OmniRoute, DeepSeek, Ollama
// — signs nothing. If this fix made their requests grow a key, it would have
// traded one provider's 400 for everyone else's, which is exactly the class of
// mistake that produced the previous Gemini bug (a field serialized in a
// direction where it did not belong).
func TestFromConvoOmitsExtraContentWithoutSignature(t *testing.T) {
	msgs, _ := FromConvo(toolRoundTripHistory(), provider.Caps{Tools: true})
	raw, err := json.Marshal(msgs)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "extra_content") {
		t.Errorf("a history with no signature still serialized extra_content.\n"+
			"Body: %s", raw)
	}
}

// TestStreamSeparatesParallelCallsWithoutIndex is the second defect, and it was
// found by probing the real API rather than by reading code.
//
// Gemini does not send `index` on streaming tool calls — not even for parallel
// ones. The accumulator used to key on a plain int, so absent and 0 were the
// same slot: both calls collapsed into one and their arguments concatenated
// into `{"city":"Paris"}{"city":"London"}`, which is not valid JSON. The model
// asked for two things and the tool layer would have run one malformed call.
//
// This is the verbatim pair of chunks from the live stream.
func TestStreamSeparatesParallelCallsWithoutIndex(t *testing.T) {
	body := `data: {"choices":[{"delta":{"role":"assistant","tool_calls":[{"extra_content":{"google":{"thought_signature":"` + liveSignature + `"}},"function":{"arguments":"{\"city\":\"Paris\"}","name":"get_weather"},"id":"Xd1000Cz","type":"function"}]},"index":0}],"object":"chat.completion.chunk"}

data: {"choices":[{"delta":{"role":"assistant","tool_calls":[{"function":{"arguments":"{\"city\":\"London\"}","name":"get_weather"},"id":"Tt4mpal2","type":"function"}]},"index":0}],"object":"chat.completion.chunk"}

data: [DONE]

`
	p := &Provider{set: provider.Settings{ID: "gemini-direct"}}
	calls := drainToolCalls(t, func(ch chan provider.Event) error {
		return p.pumpSSE(context.Background(), strings.NewReader(body), ch)
	})

	if len(calls) != 2 {
		t.Fatalf("got %d tool calls, want 2. Gemini sends no \"index\", so keying "+
			"the accumulator on a plain int merged both calls into one and "+
			"concatenated their arguments into invalid JSON. Got: %+v",
			len(calls), calls)
	}

	// Order must be preserved: Gemini puts the signature only on the first
	// call of a parallel group, so reordering would move it to a position the
	// API rejects.
	if calls[0].ID != "Xd1000Cz" || calls[1].ID != "Tt4mpal2" {
		t.Errorf("ids = %q, %q; want Xd1000Cz then Tt4mpal2 (arrival order)",
			calls[0].ID, calls[1].ID)
	}
	if calls[0].Signature != liveSignature {
		t.Errorf("first call Signature = %q, want the signature", calls[0].Signature)
	}
	if calls[1].Signature != "" {
		t.Errorf("second parallel call Signature = %q, want empty: Google attaches "+
			"a signature only to the first function-call part", calls[1].Signature)
	}

	// Each call's arguments must be valid JSON on its own — the actual damage
	// the old code did.
	for i, c := range calls {
		var decoded map[string]any
		if err := json.Unmarshal(c.Args, &decoded); err != nil {
			t.Errorf("tool call %d arguments are not valid JSON (%q): %v", i, c.Args, err)
		}
	}
}

// TestStreamStillGroupsByIndexWhenPresent guards the other convention. OpenAI
// does send `index`, and fragments of one call arrive with nothing but the
// index and a slice of the argument string. Making the accumulator tolerant of
// a missing index must not make it deaf to a present one — otherwise every
// OpenAI tool call would shatter into one call per chunk.
func TestStreamStillGroupsByIndexWhenPresent(t *testing.T) {
	body := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"write_file","arguments":"{\"path\":"}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"a.txt\"}"}}]}}]}

data: [DONE]

`
	p := &Provider{set: provider.Settings{ID: "openai"}}
	calls := drainToolCalls(t, func(ch chan provider.Event) error {
		return p.pumpSSE(context.Background(), strings.NewReader(body), ch)
	})

	if len(calls) != 1 {
		t.Fatalf("got %d tool calls, want 1: fragments sharing an index are one call", len(calls))
	}
	if got := string(calls[0].Args); got != `{"path":"a.txt"}` {
		t.Errorf("Args = %s, want the reassembled object", got)
	}
	if calls[0].Name != "write_file" {
		t.Errorf("Name = %q, want write_file", calls[0].Name)
	}
}

// TestPumpWholeCarriesIDAndSignature covers app.stream = false. The
// non-streaming path had its own copy of the translation and dropped both the
// id and the signature, so a user who turned streaming off got a tool loop
// that could neither correlate its results nor continue on Gemini.
func TestPumpWholeCarriesIDAndSignature(t *testing.T) {
	body := `{"choices":[{"message":{"role":"assistant","tool_calls":[{"extra_content":{"google":{"thought_signature":"` + liveSignature + `"}},"function":{"arguments":"{\"city\":\"Paris\"}","name":"get_weather"},"id":"z7uCjJDW","type":"function"}]}}]}`

	p := &Provider{set: provider.Settings{ID: "gemini-direct"}}
	calls := drainToolCalls(t, func(ch chan provider.Event) error {
		return p.pumpWhole(context.Background(), strings.NewReader(body), ch)
	})

	if len(calls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(calls))
	}
	if calls[0].ID != "z7uCjJDW" {
		t.Errorf("ID = %q, want z7uCjJDW: without it the tool result cannot be "+
			"correlated back to its call", calls[0].ID)
	}
	if calls[0].Signature != liveSignature {
		t.Errorf("Signature = %q, want the signature", calls[0].Signature)
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

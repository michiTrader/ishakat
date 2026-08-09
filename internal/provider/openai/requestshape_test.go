// requestshape_test.go asserts on the *bytes* of an outgoing request, not on
// the Go structs behind them.
//
// This distinction is the whole point of the file. Every existing test in this
// package checks the fields of the []ChatMessage that FromConvo returns, and
// all of them passed while ishakat sent a request that Gemini rejected with
// HTTP 400 on every turn following a tool call. The offending field was
// invisible at the struct level: `index` was legitimately present on the type
// (streaming needs it) and simply should never have been serialized outbound.
// A test that reads `msgs[1].ToolCalls[0].Function.Name` cannot see a stray
// key in the JSON; only marshalling and looking at the result can.
package openai

import (
	"encoding/json"
	"testing"

	"github.com/MichiTrader/ishakat/internal/convo"
	"github.com/MichiTrader/ishakat/internal/provider"
)

// toolRoundTripHistory is the shortest history that reproduces the bug: a
// user turn, an assistant turn that called a tool, and the tool's result.
// Sending this back is what every second turn of a tool-using exchange does,
// which is why the failure looked like "the first tool call works and then it
// breaks".
func toolRoundTripHistory() []convo.Message {
	return []convo.Message{
		convo.User("create a file"),
		convo.NewMessage(convo.RoleAssistant,
			convo.ToolCallBlock("call_1", "write_file",
				json.RawMessage(`{"path":"a.txt","content":"x"}`))),
		convo.NewMessage(convo.RoleTool,
			convo.ToolResultBlock("call_1", "write_file", "wrote 1 bytes to a.txt")),
	}
}

// marshalMessages serializes the way buildBody does, then decodes into
// map[string]any so a test can ask which keys actually exist on the wire —
// the question the struct-level tests structurally cannot ask.
func marshalMessages(t *testing.T, msgs []ChatMessage) []map[string]any {
	t.Helper()
	raw, err := json.Marshal(msgs)
	if err != nil {
		t.Fatalf("marshal messages: %v", err)
	}
	var out []map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode messages: %v", err)
	}
	return out
}

// TestRequestToolCallCarriesNoStreamingIndex is the regression test for the
// HTTP 400 a user hit on Gemini: every turn after a tool call failed, while
// the same conversation worked against OmniRoute/DeepSeek.
//
// The cause was one struct serving two directions. `index` is required when a
// tool call *arrives* in streaming — fragments of one call are reassembled by
// it — and it is not part of the request schema at all. Because it carried no
// `omitempty` (correctly: in a stream, index 0 is a real value that must not
// vanish), every outgoing assistant message shipped `"index": 0`. OpenAI
// ignores unknown fields, so this was invisible for the entire development of
// the tool layer; Gemini's OpenAI-compatibility layer validates them and
// returns 400.
//
// Asserting on absence is unusual and deliberate: the bug was not a wrong
// value, it was a field that should not exist.
func TestRequestToolCallCarriesNoStreamingIndex(t *testing.T) {
	msgs, _ := FromConvo(toolRoundTripHistory(), provider.Caps{Tools: true})
	wire := marshalMessages(t, msgs)

	var checked int
	for i, m := range wire {
		calls, ok := m["tool_calls"].([]any)
		if !ok {
			continue
		}
		for j, c := range calls {
			call, ok := c.(map[string]any)
			if !ok {
				t.Fatalf("messages[%d].tool_calls[%d] is not an object", i, j)
			}
			if _, present := call["index"]; present {
				t.Errorf("messages[%d].tool_calls[%d] carries \"index\": that field belongs to "+
					"the streaming *response* format, not to a request, and sending it is what "+
					"made Gemini reject every turn after a tool call with HTTP 400", i, j)
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no tool_calls were serialized at all: the test is not exercising what it claims")
	}
}

// TestRequestToolCallKeepsWhatTheDialectRequires is the other half of the
// assertion above. Removing a field is easy to overdo, and `id` in
// particular is load-bearing: the tool result correlates to its call through
// `tool_call_id`, so dropping it would trade a 400 for a subtler failure
// where the model cannot tell which call a result belongs to.
func TestRequestToolCallKeepsWhatTheDialectRequires(t *testing.T) {
	msgs, _ := FromConvo(toolRoundTripHistory(), provider.Caps{Tools: true})
	wire := marshalMessages(t, msgs)

	if len(wire) != 3 {
		t.Fatalf("got %d messages, want 3 (user, assistant+tool_calls, tool result)", len(wire))
	}

	calls, ok := wire[1]["tool_calls"].([]any)
	if !ok || len(calls) != 1 {
		t.Fatalf("messages[1].tool_calls = %v, want exactly one call", wire[1]["tool_calls"])
	}
	call := calls[0].(map[string]any)

	if call["id"] != "call_1" {
		t.Errorf("tool_calls[0].id = %v, want call_1: the tool result correlates through it", call["id"])
	}
	if call["type"] != "function" {
		t.Errorf("tool_calls[0].type = %v, want function", call["type"])
	}
	fn, ok := call["function"].(map[string]any)
	if !ok {
		t.Fatalf("tool_calls[0].function is missing or not an object")
	}
	if fn["name"] != "write_file" {
		t.Errorf("function.name = %v, want write_file", fn["name"])
	}
	// arguments is a JSON *string*, not a nested object, in this dialect.
	args, ok := fn["arguments"].(string)
	if !ok {
		t.Fatalf("function.arguments = %T, want a JSON string", fn["arguments"])
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(args), &decoded); err != nil {
		t.Fatalf("function.arguments is not valid JSON: %v", err)
	}
	if decoded["path"] != "a.txt" {
		t.Errorf("arguments.path = %v, want a.txt", decoded["path"])
	}

	// The tool result must be its own message, correlated by id.
	if wire[2]["role"] != "tool" {
		t.Errorf("messages[2].role = %v, want tool", wire[2]["role"])
	}
	if wire[2]["tool_call_id"] != "call_1" {
		t.Errorf("messages[2].tool_call_id = %v, want call_1", wire[2]["tool_call_id"])
	}
}

// TestRequestMessagesCarryNoUnknownKeys is the general form of the bug, and
// the reason this file is worth more than a single assertion about `index`.
// A strict compatibility layer rejects *any* field it does not recognize, so
// the risk is not one specific key but the habit of reusing response types to
// build requests. Pinning the exact allowed key set means the next such field
// fails here, in a unit test, instead of as an opaque 400 in a user's
// terminal on one provider only.
func TestRequestMessagesCarryNoUnknownKeys(t *testing.T) {
	allowed := map[string]bool{
		"role": true, "content": true, "name": true,
		"tool_calls": true, "tool_call_id": true,
	}
	// extra_content is allowed on a tool call, and only there: it is the one
	// field Google requires to travel back, and Gemini 3 answers 400 without
	// it. It is absent unless the call actually carried a signature, which the
	// history below does not — so this test still proves no stray key appears
	// for a provider that signs nothing.
	allowedCall := map[string]bool{"id": true, "type": true, "function": true, "extra_content": true}
	allowedFunc := map[string]bool{"name": true, "arguments": true}

	msgs, _ := FromConvo(toolRoundTripHistory(), provider.Caps{Tools: true})
	for i, m := range marshalMessages(t, msgs) {
		for k := range m {
			if !allowed[k] {
				t.Errorf("messages[%d] carries unexpected key %q; a strict "+
					"OpenAI-compatible service (Gemini) rejects the whole request with 400", i, k)
			}
		}
		calls, ok := m["tool_calls"].([]any)
		if !ok {
			continue
		}
		for j, c := range calls {
			call := c.(map[string]any)
			for k := range call {
				if !allowedCall[k] {
					t.Errorf("messages[%d].tool_calls[%d] carries unexpected key %q", i, j, k)
				}
			}
			fn, ok := call["function"].(map[string]any)
			if !ok {
				continue
			}
			for k := range fn {
				if !allowedFunc[k] {
					t.Errorf("messages[%d].tool_calls[%d].function carries unexpected key %q", i, j, k)
				}
			}
		}
	}
}

package openai_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MichiTrader/ishakat/internal/convo"
	"github.com/MichiTrader/ishakat/internal/provider"
	"github.com/MichiTrader/ishakat/internal/provider/openai"
)

// TestFromConvoToolsCapableSerializesToolCallsAndResults is the §12bis #5 case
// the dialect has to pass: when Caps.Tools is true, a BlockToolCall becomes the
// assistant message's `tool_calls` array (with the service-assigned id), and a
// BlockToolResult becomes its own role:"tool" message carrying tool_call_id.
func TestFromConvoToolsCapableSerializesToolCallsAndResults(t *testing.T) {
	caps := provider.Caps{Tools: true}
	msgs := []convo.Message{
		convo.User("what files are here?"),
		// An assistant turn that requested one tool.
		convo.NewMessage(convo.RoleAssistant,
			convo.TextBlock("let me check"),
			convo.ToolCallBlock("call_42", "list", json.RawMessage(`{"dir":"."}`)),
		),
		// The tool's result, as its own message.
		convo.NewMessage(convo.RoleTool,
			convo.ToolResultBlock("call_42", "list", "a.go\nb.go"),
		),
		convo.NewMessage(convo.RoleAssistant,
			convo.TextBlock("the directory has a.go and b.go"),
		),
	}

	out, deg := openai.FromConvo(msgs, caps)
	if deg.ToolsFlattened != 0 {
		t.Errorf("with Caps.Tools true, nothing should be flattened: %+v", deg)
	}
	if deg.Any() {
		t.Errorf("no degradation expected: %+v", deg)
	}

	// Expected wire order: user, assistant(tool_calls), tool, assistant(text).
	if len(out) != 4 {
		t.Fatalf("expected 4 wire messages, got %d: %+v", len(out), out)
	}
	if out[0].Role != "user" {
		t.Errorf("out[0].Role = %q, want user", out[0].Role)
	}
	if out[1].Role != "assistant" {
		t.Errorf("out[1].Role = %q, want assistant", out[1].Role)
	}
	if len(out[1].ToolCalls) != 1 {
		t.Fatalf("assistant should carry 1 tool_call, got %d", len(out[1].ToolCalls))
	}
	tc := out[1].ToolCalls[0]
	if tc.ID != "call_42" {
		t.Errorf("tool_call id = %q, want call_42", tc.ID)
	}
	if tc.Type != "function" {
		t.Errorf("tool_call type = %q, want function", tc.Type)
	}
	if tc.Function.Name != "list" {
		t.Errorf("tool_call name = %q, want list", tc.Function.Name)
	}
	if tc.Function.Arguments != `{"dir":"."}` {
		t.Errorf("tool_call arguments = %q, want {\"dir\":\".\"}", tc.Function.Arguments)
	}
	// The assistant message that requested tools still carries its text.
	if !strings.Contains(out[1].Content, "let me check") {
		t.Errorf("assistant text should be preserved: %q", out[1].Content)
	}
	// The tool result is its own role:"tool" message with tool_call_id.
	if out[2].Role != "tool" {
		t.Errorf("out[2].Role = %q, want tool", out[2].Role)
	}
	if out[2].ToolCallID != "call_42" {
		t.Errorf("tool message tool_call_id = %q, want call_42", out[2].ToolCallID)
	}
	if out[2].Content != "a.go\nb.go" {
		t.Errorf("tool message content = %q, want a.go\\nb.go", out[2].Content)
	}
	// The final assistant text turn.
	if out[3].Role != "assistant" || !strings.Contains(out[3].Content, "a.go") {
		t.Errorf("out[3] should be the final assistant text: %+v", out[3])
	}
}

// TestFromConvoToolsCapableMultipleCallsPreserveOrder verifies that multiple
// tool calls in one assistant turn produce multiple tool messages in order,
// each with its own tool_call_id.
func TestFromConvoToolsCapableMultipleCallsPreserveOrder(t *testing.T) {
	caps := provider.Caps{Tools: true}
	msgs := []convo.Message{
		convo.NewMessage(convo.RoleAssistant,
			convo.ToolCallBlock("c1", "read", json.RawMessage(`{"f":"a"}`)),
			convo.ToolCallBlock("c2", "read", json.RawMessage(`{"f":"b"}`)),
		),
		convo.NewMessage(convo.RoleTool, convo.ToolResultBlock("c1", "read", "A")),
		convo.NewMessage(convo.RoleTool, convo.ToolResultBlock("c2", "read", "B")),
	}
	out, _ := openai.FromConvo(msgs, caps)
	// out[0] = assistant with 2 tool_calls, out[1] = tool c1, out[2] = tool c2.
	if len(out) != 3 {
		t.Fatalf("expected 3 wire messages, got %d", len(out))
	}
	if len(out[0].ToolCalls) != 2 {
		t.Fatalf("expected 2 tool_calls, got %d", len(out[0].ToolCalls))
	}
	if out[0].ToolCalls[0].ID != "c1" || out[0].ToolCalls[1].ID != "c2" {
		t.Errorf("tool_call ids out of order: %s, %s", out[0].ToolCalls[0].ID, out[0].ToolCalls[1].ID)
	}
	if out[1].ToolCallID != "c1" || out[2].ToolCallID != "c2" {
		t.Errorf("tool result ids out of order: %s, %s", out[1].ToolCallID, out[2].ToolCallID)
	}
	if out[1].Content != "A" || out[2].Content != "B" {
		t.Errorf("tool result contents wrong: %q, %q", out[1].Content, out[2].Content)
	}
}

// TestFromConvoAssistantOnlyToolCallsNoText: an assistant message that only
// requested tools (no text) still serializes — content is empty but the
// tool_calls are present, which is the legal wire form.
func TestFromConvoAssistantOnlyToolCallsNoText(t *testing.T) {
	caps := provider.Caps{Tools: true}
	msgs := []convo.Message{
		convo.NewMessage(convo.RoleAssistant,
			convo.ToolCallBlock("c1", "list", json.RawMessage(`{}`)),
		),
		convo.NewMessage(convo.RoleTool, convo.ToolResultBlock("c1", "list", "x")),
	}
	out, _ := openai.FromConvo(msgs, caps)
	if len(out) != 2 {
		t.Fatalf("expected 2 wire messages, got %d", len(out))
	}
	if len(out[0].ToolCalls) != 1 {
		t.Errorf("tool_calls should be present even with no text: %d", len(out[0].ToolCalls))
	}
	if out[0].Content != "" {
		t.Errorf("content should be empty, got %q", out[0].Content)
	}
}

// TestFromConvoToolErrorPreservedInRoleTool: a BlockToolResult with IsError
// still serializes as a role:"tool" message (the error flag is for the model
// to see in the text, not a wire-level field in the OpenAI dialect).
func TestFromConvoToolErrorPreservedInRoleTool(t *testing.T) {
	caps := provider.Caps{Tools: true}
	msgs := []convo.Message{
		convo.NewMessage(convo.RoleAssistant,
			convo.ToolCallBlock("c1", "bash", json.RawMessage(`{}`)),
		),
		convo.NewMessage(convo.RoleTool,
			convo.ToolErrorBlock("c1", "bash", "exit 1: permission denied"),
		),
	}
	out, _ := openai.FromConvo(msgs, caps)
	if len(out) != 2 {
		t.Fatalf("expected 2 wire messages, got %d", len(out))
	}
	if out[1].Role != "tool" {
		t.Errorf("error result should still be role:tool, got %q", out[1].Role)
	}
	if !strings.Contains(out[1].Content, "permission denied") {
		t.Errorf("error text should reach the model: %q", out[1].Content)
	}
	if out[1].ToolCallID != "c1" {
		t.Errorf("tool_call_id should be preserved on error results: %q", out[1].ToolCallID)
	}
}

// TestFromConvoToolsIncapableFlattensEverything: when Caps.Tools is false,
// tool calls and results flatten to descriptive text and count in
// Degradation.ToolsFlattened — the model still sees what happened but cannot
// request more tools. This is the §4.6 degradation path.
func TestFromConvoToolsIncapableFlattensEverything(t *testing.T) {
	caps := provider.Caps{Tools: false}
	msgs := []convo.Message{
		convo.NewMessage(convo.RoleAssistant,
			convo.TextBlock("let me check"),
			convo.ToolCallBlock("c1", "list", json.RawMessage(`{"dir":"."}`)),
		),
		convo.NewMessage(convo.RoleTool,
			convo.ToolResultBlock("c1", "list", "a.go"),
		),
	}
	out, deg := openai.FromConvo(msgs, caps)
	if deg.ToolsFlattened != 2 {
		t.Errorf("both the call and the result should be flattened: %+v", deg)
	}
	// No wire-level tool_calls or role:tool messages.
	for _, m := range out {
		if len(m.ToolCalls) > 0 {
			t.Errorf("flattened message should not carry tool_calls: %+v", m)
		}
		if m.Role == "tool" {
			t.Errorf("flattened message should not be role:tool: %+v", m)
		}
		if m.ToolCallID != "" {
			t.Errorf("flattened message should not carry tool_call_id: %+v", m)
		}
	}
	// The flattened text should mention the tool name and the args/result.
	var allContent string
	for _, m := range out {
		allContent += m.Content + "\n"
	}
	if !strings.Contains(allContent, "list") {
		t.Errorf("flattened text should mention the tool name: %q", allContent)
	}
	if !strings.Contains(allContent, "a.go") {
		t.Errorf("flattened text should include the result: %q", allContent)
	}
}

// TestMarshalToolsEmptyReturnsNil: an empty tool list produces nil, so the
// caller can omit the `tools` field entirely (some gateways reject []).
func TestMarshalToolsEmptyReturnsNil(t *testing.T) {
	if got := openai.MarshalTools(nil); got != nil {
		t.Errorf("MarshalTools(nil) = %v, want nil", got)
	}
	if got := openai.MarshalTools([]provider.ToolDef{}); got != nil {
		t.Errorf("MarshalTools([]) = %v, want nil", got)
	}
}

// TestMarshalToolsProducesFunctionArray: a non-empty list produces the
// {type:"function", function:{name, description, parameters}} shape the
// dialect requires.
func TestMarshalToolsProducesFunctionArray(t *testing.T) {
	defs := []provider.ToolDef{
		{
			Name:        "list",
			Description: "list files in a directory",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"dir":{"type":"string"}},"required":["dir"]}`),
		},
	}
	out := openai.MarshalTools(defs)
	if len(out) != 1 {
		t.Fatalf("expected 1 tool def, got %d", len(out))
	}
	if out[0].Type != "function" {
		t.Errorf("type = %q, want function", out[0].Type)
	}
	if out[0].Function.Name != "list" {
		t.Errorf("name = %q, want list", out[0].Function.Name)
	}
	if out[0].Function.Description != "list files in a directory" {
		t.Errorf("description = %q", out[0].Function.Description)
	}
	// Parameters round-trip as raw JSON.
	var params map[string]any
	if err := json.Unmarshal(out[0].Function.Parameters, &params); err != nil {
		t.Fatalf("parameters should be valid JSON: %v", err)
	}
	if params["type"] != "object" {
		t.Errorf("parameters.type = %v, want object", params["type"])
	}
}

// TestMarshalToolsEmptyParametersGetsDefaultSchema: a tool with no parameters
// gets an explicit empty object schema, because some services reject a tool
// with no `parameters` field.
func TestMarshalToolsEmptyParametersGetsDefaultSchema(t *testing.T) {
	defs := []provider.ToolDef{{Name: "ping", Description: "ping"}}
	out := openai.MarshalTools(defs)
	if len(out) != 1 {
		t.Fatalf("expected 1 tool def, got %d", len(out))
	}
	var params map[string]any
	if err := json.Unmarshal(out[0].Function.Parameters, &params); err != nil {
		t.Fatalf("default schema should be valid JSON: %v", err)
	}
	if params["type"] != "object" {
		t.Errorf("default schema type = %v, want object", params["type"])
	}
	props, ok := params["properties"]
	if !ok {
		t.Fatal("default schema should have a properties field")
	}
	propsMap, ok := props.(map[string]any)
	if !ok || len(propsMap) != 0 {
		t.Errorf("default schema properties should be an empty object: %v", props)
	}
}

// TestBuildBodyIncludesToolsArray: when req.Tools is non-empty, the request
// body the provider sends to the service includes a top-level `tools` array.
// This is the end-to-end check that the tools reach the wire.
func TestBuildBodyIncludesToolsArray(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := readAll(t, r)
		_ = json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	p := newProvider(t, srv.URL)
	req := hola()
	req.Caps.Tools = true
	req.Tools = []provider.ToolDef{
		{Name: "list", Description: "list files", Parameters: json.RawMessage(`{"type":"object"}`)},
	}
	ch, err := p.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	drain(t, ch)

	tools, ok := gotBody["tools"]
	if !ok {
		t.Fatal("request body should include a `tools` field when req.Tools is non-empty")
	}
	arr, ok := tools.([]any)
	if !ok || len(arr) != 1 {
		t.Fatalf("tools should be an array of 1, got %T %+v", tools, tools)
	}
	first, _ := arr[0].(map[string]any)
	if first == nil || first["type"] != "function" {
		t.Errorf("tools[0].type should be function: %+v", first)
	}
	fn, _ := first["function"].(map[string]any)
	if fn == nil || fn["name"] != "list" {
		t.Errorf("tools[0].function.name should be list: %+v", fn)
	}
}

// TestBuildBodyOmitsToolsWhenCapsToolsFalse is the regression test for Bug 3:
// req.Tools non-empty but req.Caps.Tools false (the model's declared
// capability, per the catalog) must NOT put a `tools` field on the wire.
// provider.Request.Tools's own doc comment says "cuando Caps.Tools es
// false, el adaptador lo deja vacío" — before the fix, buildBody called
// MarshalTools(req.Tools) unconditionally, so a tools-incapable model still
// received the array and the service rejected the request with a 400.
func TestBuildBodyOmitsToolsWhenCapsToolsFalse(t *testing.T) {
	var gotBody map[string]any
	srv := sseServer(t, []string{"data: [DONE]\n\n"}, func(_ *http.Request, body []byte) {
		_ = json.Unmarshal(body, &gotBody)
	})
	defer srv.Close()

	p := newProvider(t, srv.URL)
	req := hola()
	req.Tools = []provider.ToolDef{
		{Name: "list", Description: "list files"},
	}
	// req.Caps.Tools is the zero value: false. This is the exact
	// misconfiguration that reaches the provider whenever a caller builds
	// Request.Tools without having first checked the model's capability.
	ch, err := p.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	drain(t, ch)

	if _, ok := gotBody["tools"]; ok {
		t.Error("body should not include a `tools` field when Caps.Tools is false, even if req.Tools is non-empty")
	}
}

// TestBuildBodyIncludesToolsWhenCapsToolsTrue is the positive counterpart:
// with Caps.Tools true and req.Tools non-empty, the array must still reach
// the wire — this pins the case the fix for Bug 3 must not break.
func TestBuildBodyIncludesToolsWhenCapsToolsTrue(t *testing.T) {
	var gotBody map[string]any
	srv := sseServer(t, []string{"data: [DONE]\n\n"}, func(_ *http.Request, body []byte) {
		_ = json.Unmarshal(body, &gotBody)
	})
	defer srv.Close()

	p := newProvider(t, srv.URL)
	req := hola()
	req.Caps.Tools = true
	req.Tools = []provider.ToolDef{
		{Name: "list", Description: "list files"},
	}
	ch, err := p.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	drain(t, ch)

	if _, ok := gotBody["tools"]; !ok {
		t.Error("body should include a `tools` field when Caps.Tools is true and req.Tools is non-empty")
	}
}

// TestBuildBodyOmitsToolsWhenEmpty: when req.Tools is empty, the body has no
// `tools` field at all (not []), because some gateways reject an empty array.
func TestBuildBodyOmitsToolsWhenEmpty(t *testing.T) {
	var gotBody map[string]any
	srv := sseServer(t, []string{"data: [DONE]\n\n"}, func(_ *http.Request, body []byte) {
		_ = json.Unmarshal(body, &gotBody)
	})
	defer srv.Close()

	p := newProvider(t, srv.URL)
	req := hola()
	// req.Tools is nil — the default.
	ch, err := p.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	drain(t, ch)

	if _, ok := gotBody["tools"]; ok {
		t.Error("body should not include a `tools` field when req.Tools is empty")
	}
}

// TestStreamToolCallIDPropagatedInEvent: the tool_call_id that arrives in the
// first chunk of a streamed tool call is emitted as Event.ID, so the agent loop
// can copy it into the BlockToolCall and the result can round-trip it. This is
// the §12bis #5 correlation requirement, tested at the wire-event level.
func TestStreamToolCallIDPropagatedInEvent(t *testing.T) {
	chunks := []string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_99","function":{"name":"grep","arguments":"{\"q\":"}}]}}]}` + "\n\n",
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"x\"}"}}]}}]}` + "\n\n",
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n",
		"data: [DONE]\n\n",
	}
	srv := sseServer(t, chunks, nil)
	defer srv.Close()

	p := newProvider(t, srv.URL)
	ch, err := p.Stream(context.Background(), hola())
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	d := drain(t, ch)

	if len(d.tools) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(d.tools))
	}
	if d.tools[0].ID != "call_99" {
		t.Errorf("Event.ID = %q, want call_99", d.tools[0].ID)
	}
	if d.tools[0].Name != "grep" {
		t.Errorf("Event.Name = %q, want grep", d.tools[0].Name)
	}
	if string(d.tools[0].Args) != `{"q":"x"}` {
		t.Errorf("Event.Args = %q, want {\"q\":\"x\"}", string(d.tools[0].Args))
	}
}

// TestStreamMultipleToolCallsBothGetIDs: two tool calls streamed in parallel
// each get their own id in the first chunk of their index, and both ids
// propagate to the events.
func TestStreamMultipleToolCallsBothGetIDs(t *testing.T) {
	chunks := []string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","function":{"name":"read","arguments":"{\"f\":"}},{"index":1,"id":"c2","function":{"name":"read","arguments":"{\"f\":"}}]}}]}` + "\n\n",
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"a\""}},{"index":1,"function":{"arguments":"\"b\""}}]}}]}` + "\n\n",
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"}"}},{"index":1,"function":{"arguments":"}"}}]}}]}` + "\n\n",
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n",
		"data: [DONE]\n\n",
	}
	srv := sseServer(t, chunks, nil)
	defer srv.Close()

	p := newProvider(t, srv.URL)
	ch, err := p.Stream(context.Background(), hola())
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	d := drain(t, ch)

	if len(d.tools) != 2 {
		t.Fatalf("expected 2 tool calls reassembled, got %d", len(d.tools))
	}
	// The accumulator flushes in index order.
	if d.tools[0].ID != "c1" || d.tools[1].ID != "c2" {
		t.Errorf("ids = %q, %q; want c1, c2", d.tools[0].ID, d.tools[1].ID)
	}
	if d.tools[0].Name != "read" || d.tools[1].Name != "read" {
		t.Errorf("names = %q, %q; want read, read", d.tools[0].Name, d.tools[1].Name)
	}
	if string(d.tools[0].Args) != `{"f":"a"}` {
		t.Errorf("args[0] = %q, want {\"f\":\"a\"}", string(d.tools[0].Args))
	}
	if string(d.tools[1].Args) != `{"f":"b"}` {
		t.Errorf("args[1] = %q, want {\"f\":\"b\"}", string(d.tools[1].Args))
	}
}

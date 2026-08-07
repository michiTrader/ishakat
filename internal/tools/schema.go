package tools

import "encoding/json"

// prop describes one JSON-schema property in the shallow shape every tool in
// this package needs: a type, a human description, and (for string
// enumerations) a fixed set of allowed values. It exists so each tool's
// Parameters() builds its schema from typed Go values instead of a
// hand-quoted JSON string literal, which is where a missing comma or a
// mismatched brace would otherwise go unnoticed until a model's tool call
// fails to validate against it.
type prop struct {
	Type        string   `json:"type"`
	Description string   `json:"description,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Items       *prop    `json:"items,omitempty"`
}

// objectSchema builds the `{"type":"object","properties":{...},"required":[...]}`
// shape every one of this package's tools uses for Parameters(), matching
// engine.ToolDef.Parameters's json.RawMessage field. required lists the
// property names that must not be omitted; a property absent from required
// is optional. Marshalling static, compile-time-known data cannot fail, so
// the error from json.Marshal is deliberately discarded — panicking on a
// programmer mistake here would still happen at package init via the
// package-level var declarations in each tool file, which is exactly when a
// typo should be caught, not hidden behind a runtime code path nothing
// exercises until a model happens to call that tool.
func objectSchema(properties map[string]prop, required ...string) json.RawMessage {
	if required == nil {
		required = []string{}
	}
	out, _ := json.Marshal(struct {
		Type       string          `json:"type"`
		Properties map[string]prop `json:"properties"`
		Required   []string        `json:"required"`
	}{
		Type:       "object",
		Properties: properties,
		Required:   required,
	})
	return out
}

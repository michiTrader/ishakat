package tools

import (
	"context"
	"encoding/json"
	"testing"
)

func TestCoreRegistersAllSixToolsByName(t *testing.T) {
	r := Core()
	want := []string{"read_file", "write_file", "edit_file", "bash", "glob", "grep"}
	got := r.Tools()
	if len(got) != len(want) {
		t.Fatalf("Core(): got %d tools, want %d", len(got), len(want))
	}
	for i, name := range want {
		if got[i].Name() != name {
			t.Errorf("Core() tool %d: got %q, want %q", i, got[i].Name(), name)
		}
		if _, ok := r.Lookup(name); !ok {
			t.Errorf("Core(): Lookup(%q) found nothing", name)
		}
	}
}

func TestRegistryLookupUnknownName(t *testing.T) {
	r := Core()
	if _, ok := r.Lookup("does_not_exist"); ok {
		t.Error("Lookup(\"does_not_exist\") should not find a tool")
	}
}

func TestRegistryRunDispatchesByName(t *testing.T) {
	r := NewRegistry(fakeNamedTool{name: "echo", text: "hello from echo"})
	res, err := r.Run(context.Background(), "echo", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Text != "hello from echo" {
		t.Errorf("got %q, want %q", res.Text, "hello from echo")
	}
	if res.IsError {
		t.Error("expected IsError=false")
	}
}

func TestRegistryRunUnknownNameIsGoError(t *testing.T) {
	r := NewRegistry(fakeNamedTool{name: "echo", text: "x"})
	_, err := r.Run(context.Background(), "no_such_tool", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected an error for an unregistered tool name")
	}
}

func TestRegistryRunPropagatesToolError(t *testing.T) {
	r := NewRegistry(fakeNamedTool{name: "fails", text: "boom", asError: true})
	res, err := r.Run(context.Background(), "fails", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !res.IsError {
		t.Error("expected Result.IsError to be true")
	}
	if res.Text != "boom" {
		t.Errorf("got %q, want %q", res.Text, "boom")
	}
}

func TestNilRegistryToolsIsNil(t *testing.T) {
	var r *Registry
	if got := r.Tools(); got != nil {
		t.Errorf("nil Registry.Tools(): got %v, want nil", got)
	}
}

func TestNilRegistryLookupIsNotFound(t *testing.T) {
	var r *Registry
	if _, ok := r.Lookup("anything"); ok {
		t.Error("nil Registry.Lookup should never find a tool")
	}
}

func TestRegistryToolsPreservesRegistrationOrder(t *testing.T) {
	r := NewRegistry(
		fakeNamedTool{name: "third", text: "3"},
		fakeNamedTool{name: "first", text: "1"},
		fakeNamedTool{name: "second", text: "2"},
	)
	got := r.Tools()
	want := []string{"third", "first", "second"}
	for i, name := range want {
		if got[i].Name() != name {
			t.Errorf("position %d: got %q, want %q", i, got[i].Name(), name)
		}
	}
}

func TestRegistryDuplicateNameKeepsFirstPositionLastValue(t *testing.T) {
	first := fakeNamedTool{name: "dup", text: "first registration"}
	second := fakeNamedTool{name: "dup", text: "second registration"}
	r := NewRegistry(
		fakeNamedTool{name: "before", text: "before"},
		first,
		fakeNamedTool{name: "after", text: "after"},
		second,
	)
	got := r.Tools()
	wantOrder := []string{"before", "dup", "after"}
	if len(got) != len(wantOrder) {
		t.Fatalf("got %d tools, want %d (duplicate name should not add a slot)", len(got), len(wantOrder))
	}
	for i, name := range wantOrder {
		if got[i].Name() != name {
			t.Errorf("position %d: got %q, want %q", i, got[i].Name(), name)
		}
	}
	res, err := r.Run(context.Background(), "dup", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Text != "second registration" {
		t.Errorf("Lookup/Run should resolve to the last registration: got %q", res.Text)
	}
}

func TestCoreEachCallReturnsIndependentRegistry(t *testing.T) {
	a := Core()
	b := Core()
	if a == b {
		t.Error("Core() should build a fresh Registry each call")
	}
}

// fakeNamedTool is a minimal Tool double for registry_test.go: enough to
// exercise Registry's lookup/dispatch logic without depending on the real
// six tools' own argument shapes or side effects.
type fakeNamedTool struct {
	name    string
	text    string
	asError bool
}

func (f fakeNamedTool) Name() string                { return f.name }
func (f fakeNamedTool) Description() string         { return "fake tool for registry_test.go" }
func (f fakeNamedTool) Danger() Danger              { return DangerLow }
func (f fakeNamedTool) Parameters() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }

func (f fakeNamedTool) Run(context.Context, json.RawMessage) (Result, error) {
	if f.asError {
		return ErrorResult(f.text), nil
	}
	return OKResult(f.text), nil
}

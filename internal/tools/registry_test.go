package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCoreRegistersAllSevenToolsByName(t *testing.T) {
	r := Core(nil, false)
	want := []string{"read_file", "write_file", "edit_file", "bash", "glob", "grep", "fetch"}
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
	r := Core(nil, false)
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
	a := Core(nil, false)
	b := Core(nil, false)
	if a == b {
		t.Error("Core() should build a fresh Registry each call")
	}
}

func TestCorePassesEgressAllowlistToFetch(t *testing.T) {
	r := Core([]string{"example.com"}, false)
	tool, ok := r.Lookup("fetch")
	if !ok {
		t.Fatal("Core(): fetch not registered")
	}
	f, ok := tool.(Fetch)
	if !ok {
		t.Fatalf("Core(): fetch tool has type %T, want Fetch", tool)
	}
	if len(f.Allow) != 1 || f.Allow[0] != "example.com" {
		t.Errorf("Core(): fetch.Allow = %v, want [example.com]", f.Allow)
	}
	if f.AllowAll {
		t.Error("Core([]string{...}, false): fetch.AllowAll should be false")
	}
}

// --- DeclarativeTools / WithDeclarative -------------------------------------

func TestDeclarativeToolsEmptyDirReturnsNilAndNoWarn(t *testing.T) {
	got, warn := DeclarativeTools("", nil, false)
	if got != nil {
		t.Errorf("DeclarativeTools(\"\", ...): got %v, want nil", got)
	}
	if warn != "" {
		t.Errorf("DeclarativeTools(\"\", ...): warn = %q, want empty", warn)
	}
}

func TestDeclarativeToolsMissingDirReturnsNilAndNoWarn(t *testing.T) {
	got, warn := DeclarativeTools(filepath.Join(t.TempDir(), "does-not-exist"), nil, false)
	if got != nil {
		t.Errorf("DeclarativeTools(missing dir): got %v, want nil", got)
	}
	if warn != "" {
		t.Errorf("DeclarativeTools(missing dir): warn = %q, want empty", warn)
	}
}

func TestDeclarativeToolsFindsToolAndCarriesAllowlist(t *testing.T) {
	dir := t.TempDir()
	toolDir := filepath.Join(dir, "greet")
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	manifest := []byte(`
name = "greet"
description = "say hello"

[request]
method = "GET"
url = "https://example.com/greet"
`)
	if err := os.WriteFile(filepath.Join(toolDir, ManifestFileName), manifest, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	got, warn := DeclarativeTools(dir, []string{"example.com"}, false)
	if warn != "" {
		t.Fatalf("unexpected warn: %q", warn)
	}
	if len(got) != 1 {
		t.Fatalf("got %d tools, want 1", len(got))
	}
	if got[0].Name() != "greet" {
		t.Errorf("Name() = %q, want greet", got[0].Name())
	}
	dt, ok := got[0].(DeclarativeTool)
	if !ok {
		t.Fatalf("tool has type %T, want DeclarativeTool", got[0])
	}
	if len(dt.Allow) != 1 || dt.Allow[0] != "example.com" {
		t.Errorf("Allow = %v, want [example.com]", dt.Allow)
	}
	if dt.AllowAll {
		t.Error("AllowAll should be false")
	}
}

func TestWithDeclarativeNoDirBehavesLikeCore(t *testing.T) {
	reg, warn := WithDeclarative(nil, false, "")
	if warn != "" {
		t.Errorf("unexpected warn: %q", warn)
	}
	want := []string{"read_file", "write_file", "edit_file", "bash", "glob", "grep", "fetch"}
	got := reg.Tools()
	if len(got) != len(want) {
		t.Fatalf("WithDeclarative(nil, false, \"\"): got %d tools, want %d", len(got), len(want))
	}
	for i, name := range want {
		if got[i].Name() != name {
			t.Errorf("position %d: got %q, want %q", i, got[i].Name(), name)
		}
	}
}

func TestWithDeclarativeAppendsDiscoveredToolsAfterNativeSeven(t *testing.T) {
	dir := t.TempDir()
	toolDir := filepath.Join(dir, "greet")
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	manifest := []byte(`
name = "greet"
description = "say hello"

[request]
method = "GET"
url = "https://example.com/greet"
`)
	if err := os.WriteFile(filepath.Join(toolDir, ManifestFileName), manifest, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	reg, warn := WithDeclarative(nil, false, dir)
	if warn != "" {
		t.Fatalf("unexpected warn: %q", warn)
	}
	got := reg.Tools()
	if len(got) != 8 {
		t.Fatalf("got %d tools, want 8 (7 native + 1 declarative)", len(got))
	}
	if got[7].Name() != "greet" {
		t.Errorf("tool at position 7 = %q, want greet", got[7].Name())
	}
	if _, ok := reg.Lookup("greet"); !ok {
		t.Error("Lookup(\"greet\") found nothing")
	}
	// Native tools must still resolve exactly as Core() alone provides.
	if _, ok := reg.Lookup("fetch"); !ok {
		t.Error("Lookup(\"fetch\") found nothing after adding declarative tools")
	}
}

func TestWithDeclarativeSurfacesDiscoveryWarn(t *testing.T) {
	dir := t.TempDir()
	toolDir := filepath.Join(dir, "broken")
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(toolDir, ManifestFileName), []byte("not valid toml [["), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	reg, warn := WithDeclarative(nil, false, dir)
	if warn == "" {
		t.Fatal("expected a non-empty warn for an unparseable tool.toml")
	}
	want := []string{"read_file", "write_file", "edit_file", "bash", "glob", "grep", "fetch"}
	got := reg.Tools()
	if len(got) != len(want) {
		t.Fatalf("a broken manifest should not add a tool: got %d, want %d", len(got), len(want))
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

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

// TestWithMetaToolsEmptyDirBehavesLikeWithDeclarative pins that an unset
// Dir (the zero value, matching every install that has not configured a
// layer-2 tools directory) yields exactly the native seven with none of
// §19.5's five meta-tools added -- WithMetaTools' own doc comment's "no Dir
// means nothing to act on yet" contract, mirrored from DeclarativeTools'
// identical "dir == \"\" is a no-op" rule.
func TestWithMetaToolsEmptyDirBehavesLikeWithDeclarative(t *testing.T) {
	reg, warn := WithMetaTools(MetaToolsOptions{EvolveMode: "suggest", HasTTY: true})
	if warn != "" {
		t.Fatalf("unexpected warn: %q", warn)
	}
	if len(reg.Tools()) != 7 {
		t.Errorf("got %d tools, want 7 (native only, no Dir configured)", len(reg.Tools()))
	}
	for _, name := range []string{"tool_list", "tool_probe", "tool_create", "tool_edit", "tool_delete"} {
		if _, ok := reg.Lookup(name); ok {
			t.Errorf("Lookup(%q) found a meta-tool with no Dir configured", name)
		}
	}
}

// TestWithMetaToolsDirSetAddsFourAlwaysAvailableMetaTools proves
// tool_list/tool_probe/tool_edit/tool_delete are added as soon as Dir is
// set, with no further gate -- EvolveMode "off" and HasTTY false here on
// purpose, to isolate that these four do not depend on either.
func TestWithMetaToolsDirSetAddsFourAlwaysAvailableMetaTools(t *testing.T) {
	dir := t.TempDir()
	reg, warn := WithMetaTools(MetaToolsOptions{Dir: dir, EvolveMode: "off", HasTTY: false})
	if warn != "" {
		t.Fatalf("unexpected warn: %q", warn)
	}
	for _, name := range []string{"tool_list", "tool_probe", "tool_edit", "tool_delete"} {
		if _, ok := reg.Lookup(name); !ok {
			t.Errorf("Lookup(%q) found nothing with Dir configured", name)
		}
	}
	if _, ok := reg.Lookup("tool_create"); ok {
		t.Error("tool_create must not be present when EvolveMode is \"off\"")
	}
	// 7 native + 4 meta-tools, tool_create withheld.
	if got := len(reg.Tools()); got != 11 {
		t.Errorf("got %d tools, want 11", got)
	}
}

// TestWithMetaToolsModeOffOmitsToolCreateEntirely is §19.7's own table,
// quoted verbatim in MetaToolsOptions' doc comment: "off" means
// "tool_create is absent from the registry", checked here with an
// otherwise-permissive HasTTY=true so the omission can only be Mode's
// doing, not the TTY rule's.
func TestWithMetaToolsModeOffOmitsToolCreateEntirely(t *testing.T) {
	dir := t.TempDir()
	reg, _ := WithMetaTools(MetaToolsOptions{Dir: dir, EvolveMode: "off", HasTTY: true})
	if _, ok := reg.Lookup("tool_create"); ok {
		t.Error("tool_create must be absent from the registry when EvolveMode is \"off\", even with a TTY present")
	}
}

// TestWithMetaToolsNoTTYOmitsToolCreateEntirely is §19.6's own rule, quoted
// verbatim in docs/PLAN.md §19.7: "With no TTY, tool_create is denied. Full
// stop." Checked here with an otherwise-permissive EvolveMode so the
// omission can only be the TTY rule's doing, not Mode's.
func TestWithMetaToolsNoTTYOmitsToolCreateEntirely(t *testing.T) {
	dir := t.TempDir()
	reg, _ := WithMetaTools(MetaToolsOptions{Dir: dir, EvolveMode: "suggest", HasTTY: false})
	if _, ok := reg.Lookup("tool_create"); ok {
		t.Error("tool_create must be absent from the registry with no TTY and AllowWithoutTTY unset")
	}
}

// TestWithMetaToolsAllowWithoutTTYSubstitutesForHasTTY proves
// AllowWithoutTTY (the future --allow-tool-create flag's config-level
// stand-in, per its own doc comment) grants the identical outcome HasTTY
// would, for a caller that has deliberately opted in.
func TestWithMetaToolsAllowWithoutTTYSubstitutesForHasTTY(t *testing.T) {
	dir := t.TempDir()
	reg, _ := WithMetaTools(MetaToolsOptions{Dir: dir, EvolveMode: "suggest", HasTTY: false, AllowWithoutTTY: true})
	if _, ok := reg.Lookup("tool_create"); !ok {
		t.Error("tool_create should be present when AllowWithoutTTY is true, even with no TTY")
	}
}

// TestWithMetaToolsModeAndTTYBothSatisfiedAddsToolCreate is the positive
// case every other test in this group isolates a single failing condition
// against: both §19.7's Mode gate and §19.6's TTY gate satisfied together
// add all five meta-tools, in the fixed order WithMetaTools' own doc
// comment states (list, probe, create, edit, delete).
func TestWithMetaToolsModeAndTTYBothSatisfiedAddsToolCreate(t *testing.T) {
	dir := t.TempDir()
	reg, warn := WithMetaTools(MetaToolsOptions{Dir: dir, EvolveMode: "suggest", HasTTY: true})
	if warn != "" {
		t.Fatalf("unexpected warn: %q", warn)
	}
	wantOrder := []string{
		"read_file", "write_file", "edit_file", "bash", "glob", "grep", "fetch",
		"tool_list", "tool_probe", "tool_create", "tool_edit", "tool_delete",
	}
	got := reg.Tools()
	if len(got) != len(wantOrder) {
		t.Fatalf("got %d tools, want %d: %+v", len(got), len(wantOrder), got)
	}
	for i, name := range wantOrder {
		if got[i].Name() != name {
			t.Errorf("position %d: got %q, want %q", i, got[i].Name(), name)
		}
	}
}

// TestWithMetaToolsOnRequestAndAutoBehaveLikeSuggest proves every Mode
// value other than "off" (including one MetaToolsOptions.EvolveMode's own
// doc comment names explicitly, "on_request"/"auto", plus an empty string
// for a never-configured install) still admits tool_create once HasTTY is
// satisfied -- only "off" has a registry-shape consequence; the rest is a
// civility question for the agent's own judgement, not this function's.
func TestWithMetaToolsOnRequestAndAutoBehaveLikeSuggest(t *testing.T) {
	for _, mode := range []string{"on_request", "auto", "", "SUGGEST"} {
		dir := t.TempDir()
		reg, _ := WithMetaTools(MetaToolsOptions{Dir: dir, EvolveMode: mode, HasTTY: true})
		if _, ok := reg.Lookup("tool_create"); !ok {
			t.Errorf("EvolveMode=%q: tool_create should be present", mode)
		}
	}
}

// TestWithMetaToolsDeclarativeToolStillDiscovered proves WithMetaTools does
// not drop WithDeclarative's own job -- a real tool.toml under Dir must
// still reach the registry alongside the meta-tools, in native-then-
// declarative-then-meta order (WithDeclarative's own contract, extended).
func TestWithMetaToolsDeclarativeToolStillDiscovered(t *testing.T) {
	dir := t.TempDir()
	toolDir := filepath.Join(dir, "greet")
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	manifest := []byte("name = \"greet\"\ndescription = \"say hello\"\n\n[request]\nmethod = \"GET\"\nurl = \"https://example.com/greet\"\n")
	if err := os.WriteFile(filepath.Join(toolDir, ManifestFileName), manifest, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	reg, warn := WithMetaTools(MetaToolsOptions{Dir: dir, EvolveMode: "suggest", HasTTY: true})
	if warn != "" {
		t.Fatalf("unexpected warn: %q", warn)
	}
	for _, name := range []string{"greet", "tool_list", "tool_create", "tool_delete"} {
		if _, ok := reg.Lookup(name); !ok {
			t.Errorf("Lookup(%q) found nothing", name)
		}
	}
	// 7 native + 1 declarative + 5 meta-tools.
	if got := len(reg.Tools()); got != 13 {
		t.Errorf("got %d tools, want 13", got)
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

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
	got, warn := DeclarativeTools("", nil, false, Caps{})
	if got != nil {
		t.Errorf("DeclarativeTools(\"\", ...): got %v, want nil", got)
	}
	if warn != "" {
		t.Errorf("DeclarativeTools(\"\", ...): warn = %q, want empty", warn)
	}
}

func TestDeclarativeToolsMissingDirReturnsNilAndNoWarn(t *testing.T) {
	got, warn := DeclarativeTools(filepath.Join(t.TempDir(), "does-not-exist"), nil, false, Caps{})
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

	got, warn := DeclarativeTools(dir, []string{"example.com"}, false, Caps{})
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

// TestDeclarativeToolsExcludesManifestWithUnmetRequiresCaps is §20.11 item
// 4's own closing criterion at this layer: a tool.toml naming a
// requires_caps entry the activeCaps argument does not satisfy is left out
// of DeclarativeTools' returned []Tool entirely (not included with a
// warning attached to the tool itself) -- see DeclarativeTools' own doc
// comment for why exclusion, not a CheckSwap-style report, is the right
// shape here. The exclusion is also surfaced once via the returned warn
// string, so an install still has a way to notice why a tool disappeared.
func TestDeclarativeToolsExcludesManifestWithUnmetRequiresCaps(t *testing.T) {
	dir := t.TempDir()
	toolDir := filepath.Join(dir, "vision_tool")
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	manifest := []byte(`
name = "vision_tool"
description = "needs vision"
requires_caps = ["vision"]

[request]
method = "GET"
url = "https://example.com/x"
`)
	if err := os.WriteFile(filepath.Join(toolDir, ManifestFileName), manifest, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	// Active model lacks vision: the tool must be excluded, with a warn.
	got, warn := DeclarativeTools(dir, nil, false, Caps{Vision: false})
	if len(got) != 0 {
		t.Errorf("got %d tools, want 0 (vision_tool must be excluded)", len(got))
	}
	if warn == "" {
		t.Error("expected a non-empty warn explaining the exclusion")
	}

	// Active model has vision: the tool must be present, no warn.
	got, warn = DeclarativeTools(dir, nil, false, Caps{Vision: true})
	if len(got) != 1 {
		t.Fatalf("got %d tools, want 1 (vision_tool must be included once satisfied)", len(got))
	}
	if warn != "" {
		t.Errorf("unexpected warn once requires_caps is satisfied: %q", warn)
	}
	if got[0].Name() != "vision_tool" {
		t.Errorf("Name() = %q, want vision_tool", got[0].Name())
	}
}

// TestWithDeclarativeExcludesManifestWithUnmetMinContext is the same
// exclusion proved one layer up, through WithDeclarative, and for
// min_context rather than requires_caps -- both fields feed the same
// Manifest.Unsatisfied check, but a regression in either the caller's own
// wiring or the field-specific comparison should be caught independently.
func TestWithDeclarativeExcludesManifestWithUnmetMinContext(t *testing.T) {
	dir := t.TempDir()
	toolDir := filepath.Join(dir, "big_context_tool")
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	manifest := []byte(`
name = "big_context_tool"
description = "needs a big window"
min_context = 100000

[request]
method = "GET"
url = "https://example.com/x"
`)
	if err := os.WriteFile(filepath.Join(toolDir, ManifestFileName), manifest, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	reg, warn := WithDeclarative(nil, false, dir, Caps{Context: 8000})
	if warn == "" {
		t.Error("expected a non-empty warn explaining the exclusion")
	}
	if _, ok := reg.Lookup("big_context_tool"); ok {
		t.Error("big_context_tool must not be registered against an 8k-context active model")
	}

	reg, warn = WithDeclarative(nil, false, dir, Caps{Context: 200000})
	if warn != "" {
		t.Errorf("unexpected warn once min_context is satisfied: %q", warn)
	}
	if _, ok := reg.Lookup("big_context_tool"); !ok {
		t.Error("big_context_tool must be registered against a 200k-context active model")
	}
}

func TestWithDeclarativeNoDirBehavesLikeCore(t *testing.T) {
	reg, warn := WithDeclarative(nil, false, "", Caps{})
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

	reg, warn := WithDeclarative(nil, false, dir, Caps{})
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

	reg, warn := WithDeclarative(nil, false, dir, Caps{})
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
// layer-2 tools directory) yields exactly the native seven plus ask_user
// (Step 32 part 1's ninth core tool, always present regardless of Dir --
// see WithMetaTools' own doc comment) with none of §19.5's five meta-tools
// added -- WithMetaTools' own doc comment's "no Dir means nothing to act
// on yet" contract, mirrored from DeclarativeTools' identical "dir == \"\"
// is a no-op" rule.
func TestWithMetaToolsEmptyDirBehavesLikeWithDeclarative(t *testing.T) {
	reg, warn := WithMetaTools(MetaToolsOptions{EvolveMode: "suggest", HasTTY: true})
	if warn != "" {
		t.Fatalf("unexpected warn: %q", warn)
	}
	if len(reg.Tools()) != 8 {
		t.Errorf("got %d tools, want 8 (native seven + ask_user, no Dir configured)", len(reg.Tools()))
	}
	if _, ok := reg.Lookup("ask_user"); !ok {
		t.Error("Lookup(\"ask_user\") found nothing with no Dir configured -- ask_user must be always present")
	}
	for _, name := range []string{"tool_list", "tool_probe", "tool_create", "tool_edit", "tool_delete"} {
		if _, ok := reg.Lookup(name); ok {
			t.Errorf("Lookup(%q) found a meta-tool with no Dir configured", name)
		}
	}
}

// TestWithMetaToolsDirSetAddsSixAlwaysAvailableMetaTools proves
// tool_list/tool_probe/tool_edit/tool_archive/tool_revive/tool_delete are
// added as soon as Dir is set, with no further gate -- EvolveMode "off" and
// HasTTY false here on purpose, to isolate that these six do not depend on
// either.
func TestWithMetaToolsDirSetAddsSixAlwaysAvailableMetaTools(t *testing.T) {
	dir := t.TempDir()
	reg, warn := WithMetaTools(MetaToolsOptions{Dir: dir, EvolveMode: "off", HasTTY: false})
	if warn != "" {
		t.Fatalf("unexpected warn: %q", warn)
	}
	for _, name := range []string{"tool_list", "tool_probe", "tool_edit", "tool_archive", "tool_revive", "tool_delete"} {
		if _, ok := reg.Lookup(name); !ok {
			t.Errorf("Lookup(%q) found nothing with Dir configured", name)
		}
	}
	if _, ok := reg.Lookup("tool_create"); ok {
		t.Error("tool_create must not be present when EvolveMode is \"off\"")
	}
	// 7 native + ask_user + 6 meta-tools, tool_create withheld.
	if got := len(reg.Tools()); got != 14 {
		t.Errorf("got %d tools, want 14", got)
	}
}

// TestWithMetaToolsNilDispatchRunnerOmitsDispatch pins the "absent, not
// merely denied" contract MetaToolsOptions.DispatchRunner's own doc comment
// states: a nil Runner (the zero value, matching every install that has
// not wired a sub-agent capability) means dispatch never appears in the
// registry at all -- not even with a Dir configured, since dispatch's own
// gate is DispatchRunner alone, not the layer-2 tools directory.
func TestWithMetaToolsNilDispatchRunnerOmitsDispatch(t *testing.T) {
	dir := t.TempDir()
	reg, _ := WithMetaTools(MetaToolsOptions{Dir: dir, EvolveMode: "suggest", HasTTY: true})
	if _, ok := reg.Lookup("dispatch"); ok {
		t.Error("dispatch must not be present with a nil DispatchRunner")
	}
}

// TestWithMetaToolsDispatchRunnerAddsDispatchRegardlessOfDir proves
// dispatch is added whenever DispatchRunner != nil, both with and without a
// layer-2 tools directory configured -- dispatch has nothing to do with the
// directory every meta-tool and declarative tool acts on.
func TestWithMetaToolsDispatchRunnerAddsDispatchRegardlessOfDir(t *testing.T) {
	runner := func(ctx context.Context, task string) (string, error) { return "ok: " + task, nil }

	for _, dir := range []string{"", t.TempDir()} {
		reg, warn := WithMetaTools(MetaToolsOptions{Dir: dir, DispatchRunner: runner})
		if warn != "" {
			t.Fatalf("unexpected warn for dir=%q: %q", dir, warn)
		}
		tool, ok := reg.Lookup("dispatch")
		if !ok {
			t.Fatalf("dispatch missing for dir=%q", dir)
		}
		if tool.Danger() != DangerHigh {
			t.Errorf("dispatch.Danger() = %v, want DangerHigh (dir=%q)", tool.Danger(), dir)
		}
		res, err := reg.Run(context.Background(), "dispatch", json.RawMessage(`{"task":"x"}`))
		if err != nil {
			t.Fatalf("dispatch run failed for dir=%q: %v", dir, err)
		}
		if res.Text != "ok: x" {
			t.Errorf("dispatch run text = %q for dir=%q, want %q", res.Text, dir, "ok: x")
		}
	}
}

// TestWithMetaToolsAskUserAlwaysPresentRegardlessOfAskerOrDir pins §19.1's
// own contract for ask_user (Step 32 part 1, the ninth core tool): it is
// always present, safe and never denyable, so unlike DispatchRunner and
// tool_create it has no "absent, not merely denied" mode at all -- a nil
// Asker (every call site before the TUI/serve bridge lands) and/or an
// unset Dir must still leave ask_user in the registry, with DangerLow.
func TestWithMetaToolsAskUserAlwaysPresentRegardlessOfAskerOrDir(t *testing.T) {
	for _, dir := range []string{"", t.TempDir()} {
		reg, _ := WithMetaTools(MetaToolsOptions{Dir: dir})
		tool, ok := reg.Lookup("ask_user")
		if !ok {
			t.Fatalf("ask_user missing for dir=%q with a nil Asker", dir)
		}
		if tool.Danger() != DangerLow {
			t.Errorf("ask_user.Danger() = %v for dir=%q, want DangerLow", tool.Danger(), dir)
		}
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

// TestWithMetaToolsArchiveReviveDoNotDependOnEvolveModeOrTTY proves
// tool_archive/tool_revive behave exactly like tool_list/tool_probe/
// tool_edit/tool_delete on this axis -- present whenever Dir is set,
// regardless of EvolveMode or HasTTY -- because neither acquires a new
// capability (§19.6/§19.7's governance question), it only moves an
// existing tool along the lifecycle diagram's archive/revive edge. Checked
// with the single most restrictive combination (Mode "off", no TTY) that
// omits tool_create, to isolate that this restriction is specific to
// tool_create and does not leak onto its neighbors.
func TestWithMetaToolsArchiveReviveDoNotDependOnEvolveModeOrTTY(t *testing.T) {
	dir := t.TempDir()
	reg, _ := WithMetaTools(MetaToolsOptions{Dir: dir, EvolveMode: "off", HasTTY: false})
	for _, name := range []string{"tool_archive", "tool_revive"} {
		if _, ok := reg.Lookup(name); !ok {
			t.Errorf("Lookup(%q) found nothing even though only tool_create should be gated", name)
		}
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
// add every meta-tool, in the fixed order WithMetaTools' own doc comment
// states (list, probe, create, edit, archive, revive, delete).
func TestWithMetaToolsModeAndTTYBothSatisfiedAddsToolCreate(t *testing.T) {
	dir := t.TempDir()
	reg, warn := WithMetaTools(MetaToolsOptions{Dir: dir, EvolveMode: "suggest", HasTTY: true})
	if warn != "" {
		t.Fatalf("unexpected warn: %q", warn)
	}
	wantOrder := []string{
		"read_file", "write_file", "edit_file", "bash", "glob", "grep", "fetch",
		"ask_user",
		"tool_list", "tool_probe", "tool_create", "tool_edit", "tool_archive", "tool_revive", "tool_delete",
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
	// 7 native + 1 declarative + ask_user + 7 meta-tools.
	if got := len(reg.Tools()); got != 16 {
		t.Errorf("got %d tools, want 16", got)
	}
}

// TestWithMetaToolsThreadsLedgerPathIntoToolCreate confirms
// MetaToolsOptions.LedgerPath reaches the tool_create meta-tool's own
// LedgerPath field unchanged -- WithMetaTools' own job here is pure
// plumbing (see MetaToolsOptions.LedgerPath's doc comment), so this test
// only asserts the field survives the trip, not any of ToolCreate's own
// realRepetitions behavior (covered in tool_create_test.go).
func TestWithMetaToolsThreadsLedgerPathIntoToolCreate(t *testing.T) {
	dir := t.TempDir()
	reg, _ := WithMetaTools(MetaToolsOptions{Dir: dir, EvolveMode: "suggest", HasTTY: true, LedgerPath: "/tmp/some-usage.jsonl"})
	got, ok := reg.Lookup("tool_create")
	if !ok {
		t.Fatal("expected tool_create to be present")
	}
	tc, ok := got.(ToolCreate)
	if !ok {
		t.Fatalf("tool_create is not a ToolCreate value: %T", got)
	}
	if tc.LedgerPath != "/tmp/some-usage.jsonl" {
		t.Errorf("LedgerPath = %q, want %q", tc.LedgerPath, "/tmp/some-usage.jsonl")
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

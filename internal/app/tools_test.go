package app

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/permissions"
	"github.com/MichiTrader/ishakat/internal/tools"
)

func TestToolDefsFromCopiesNameDescriptionParameters(t *testing.T) {
	reg := tools.Core()
	defs := ToolDefsFrom(reg)
	if len(defs) != 6 {
		t.Fatalf("got %d ToolDefs, want 6", len(defs))
	}
	byName := map[string]bool{}
	for i, d := range defs {
		byName[d.Name] = true
		if d.Description == "" {
			t.Errorf("ToolDef %d (%s): empty Description", i, d.Name)
		}
		if len(d.Parameters) == 0 {
			t.Errorf("ToolDef %d (%s): empty Parameters", i, d.Name)
		}
	}
	for _, name := range []string{"read_file", "write_file", "edit_file", "bash", "glob", "grep"} {
		if !byName[name] {
			t.Errorf("ToolDefsFrom: missing %q", name)
		}
	}
}

func TestToolDefsFromEmptyRegistryIsNil(t *testing.T) {
	reg := tools.NewRegistry()
	if got := ToolDefsFrom(reg); got != nil {
		t.Errorf("ToolDefsFrom(empty registry): got %v, want nil", got)
	}
}

func TestToolRunnerFromDispatchesByName(t *testing.T) {
	reg := tools.NewRegistry(fakeAppTool{name: "echo", text: "hi from echo"})
	run := ToolRunnerFrom(reg)

	res, err := run(context.Background(), "echo", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Text != "hi from echo" {
		t.Errorf("got %q, want %q", res.Text, "hi from echo")
	}
	if res.IsError {
		t.Error("expected IsError=false")
	}
}

func TestToolRunnerFromPropagatesToolError(t *testing.T) {
	reg := tools.NewRegistry(fakeAppTool{name: "fails", text: "boom", asError: true})
	run := ToolRunnerFrom(reg)

	res, err := run(context.Background(), "fails", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError=true")
	}
	if res.Text != "boom" {
		t.Errorf("got %q, want %q", res.Text, "boom")
	}
}

func TestToolRunnerFromUnknownNameIsGoError(t *testing.T) {
	reg := tools.NewRegistry(fakeAppTool{name: "echo", text: "x"})
	run := ToolRunnerFrom(reg)

	if _, err := run(context.Background(), "no_such_tool", json.RawMessage(`{}`)); err == nil {
		t.Fatal("expected an error for an unregistered tool name")
	}
}

func TestToolRunnerWithGuardReturnsDeniedRequestAsToolError(t *testing.T) {
	reg := tools.NewRegistry(fakeAppTool{name: "write_file", text: "must not run"})
	guard := permissions.New(config.Permissions{Write: "deny"}, false, nil)
	run := ToolRunnerWithGuard(reg, guard)

	res, err := run(context.Background(), "write_file", json.RawMessage(`{"path":"note.txt","content":"x"}`))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !res.IsError {
		t.Fatal("permission denial must be model-visible tool error data")
	}
	if res.Text == "must not run" {
		t.Fatal("runner executed a denied tool")
	}
}

// fakeAppTool is a minimal tools.Tool double, local to this test file so it
// does not need internal/tools to export one — it only exercises the
// adapter's copy of fields, not any real tool's own behaviour.
type fakeAppTool struct {
	name    string
	text    string
	asError bool
}

func (f fakeAppTool) Name() string         { return f.name }
func (f fakeAppTool) Description() string  { return "fake tool for tools_test.go" }
func (f fakeAppTool) Danger() tools.Danger { return tools.DangerLow }
func (f fakeAppTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}

func (f fakeAppTool) Run(context.Context, json.RawMessage) (tools.Result, error) {
	if f.asError {
		return tools.ErrorResult(f.text), nil
	}
	return tools.OKResult(f.text), nil
}

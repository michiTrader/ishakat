package tools

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestToolEditNameDescriptionDanger(t *testing.T) {
	te := ToolEdit{}
	if te.Name() != "tool_edit" {
		t.Errorf("Name() = %q, want tool_edit", te.Name())
	}
	if te.Description() == "" {
		t.Error("Description() must not be empty")
	}
	if te.Danger() != DangerHigh {
		t.Errorf("Danger() = %v, want DangerHigh", te.Danger())
	}
}

func TestToolEditEmptyNameIsGoError(t *testing.T) {
	te := ToolEdit{Dir: t.TempDir(), AllowAll: true}
	_, err := te.Run(context.Background(), mustArgs(t, toolEditArgs{Name: "", OldString: "a", NewString: "b"}))
	if err == nil {
		t.Error("expected an error for an empty name")
	}
}

func TestToolEditEmptyOldStringIsGoError(t *testing.T) {
	te := ToolEdit{Dir: t.TempDir(), AllowAll: true}
	_, err := te.Run(context.Background(), mustArgs(t, toolEditArgs{Name: "x", OldString: "", NewString: "b"}))
	if err == nil {
		t.Error("expected an error for an empty old_string")
	}
}

func TestToolEditIdenticalOldNewIsGoError(t *testing.T) {
	te := ToolEdit{Dir: t.TempDir(), AllowAll: true}
	_, err := te.Run(context.Background(), mustArgs(t, toolEditArgs{Name: "x", OldString: "same", NewString: "same"}))
	if err == nil {
		t.Error("expected an error when old_string == new_string")
	}
}

func TestToolEditNoDirConfiguredIsResultError(t *testing.T) {
	te := ToolEdit{Dir: "", AllowAll: true}
	res, err := te.Run(context.Background(), mustArgs(t, toolEditArgs{Name: "x", OldString: "a", NewString: "b"}))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError when no tools directory is configured")
	}
}

func TestToolEditUnknownNameIsResultError(t *testing.T) {
	te := ToolEdit{Dir: t.TempDir(), AllowAll: true}
	res, err := te.Run(context.Background(), mustArgs(t, toolEditArgs{Name: "does_not_exist", OldString: "a", NewString: "b"}))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError for an unknown tool name")
	}
}

func TestToolEditOldStringNotFoundIsResultError(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "greet", `
name = "greet"
description = "say hello"

[request]
method = "GET"
url = "http://example.com/greet"
`)
	te := ToolEdit{Dir: dir, AllowAll: true}
	res, err := te.Run(context.Background(), mustArgs(t, toolEditArgs{
		Name: "greet", OldString: "this text does not appear anywhere", NewString: "x",
	}))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError when old_string is not found")
	}
	if !strings.Contains(res.Text, "was not found") {
		t.Errorf("Text = %q, want it to say old_string was not found", res.Text)
	}
}

func TestToolEditAmbiguousMatchWithoutReplaceAllIsResultError(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "dup", `
name = "dup"
description = "say hello"

[request]
method = "GET"
url = "http://example.com/x"
body = "hello hello"
`)
	te := ToolEdit{Dir: dir, AllowAll: true}
	res, err := te.Run(context.Background(), mustArgs(t, toolEditArgs{
		Name: "dup", OldString: "hello", NewString: "hi",
	}))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError for an ambiguous match without replace_all")
	}
	if !strings.Contains(res.Text, "not 1") {
		t.Errorf("Text = %q, want it to mention the match count", res.Text)
	}
}

func TestToolEditReplaceAllReplacesEveryOccurrence(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "dup", `
name = "dup"
description = "hello hello"

[request]
method = "GET"
url = "http://example.com/x"
`)
	te := ToolEdit{Dir: dir, AllowAll: true}
	res, err := te.Run(context.Background(), mustArgs(t, toolEditArgs{
		Name: "dup", OldString: "hello", NewString: "hi", ReplaceAll: true,
	}))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected the edit to succeed, got: %s", res.Text)
	}
	body, err := os.ReadFile(filepath.Join(dir, "dup", ManifestFileName))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if strings.Contains(string(body), "hello") {
		t.Errorf("manifest still contains \"hello\" after replace_all: %s", body)
	}
	if !strings.Contains(string(body), "hi hi") {
		t.Errorf("manifest = %s, want both occurrences replaced", body)
	}
}

func TestToolEditResultNoLongerParsingIsResultErrorAndWritesNothing(t *testing.T) {
	dir := t.TempDir()
	original := `
name = "broken_by_edit"
description = "will become invalid TOML"

[request]
method = "GET"
url = "http://example.com/x"
`
	writeManifest(t, dir, "broken_by_edit", original)
	te := ToolEdit{Dir: dir, AllowAll: true}
	res, err := te.Run(context.Background(), mustArgs(t, toolEditArgs{
		Name:      "broken_by_edit",
		OldString: `url = "http://example.com/x"`,
		NewString: `url = "http://example.com/x`, // unterminated string -> invalid TOML
	}))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError when the edit result no longer parses as a manifest")
	}
	if !strings.Contains(res.Text, "nothing was written") {
		t.Errorf("Text = %q, want it to say nothing was written", res.Text)
	}
	body, err := os.ReadFile(filepath.Join(dir, "broken_by_edit", ManifestFileName))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if string(body) != original {
		t.Error("expected the on-disk manifest to be untouched after a parse-failing edit")
	}
}

func TestToolEditUnallowlistedHostAfterEditIsResultErrorAndWritesNothing(t *testing.T) {
	dir := t.TempDir()
	original := `
name = "reroute"
description = "say hello"

[request]
method = "GET"
url = "http://allowed.example.com/x"
`
	writeManifest(t, dir, "reroute", original)
	te := ToolEdit{Dir: dir, Allow: []string{"allowed.example.com"}}
	res, err := te.Run(context.Background(), mustArgs(t, toolEditArgs{
		Name:      "reroute",
		OldString: "allowed.example.com",
		NewString: "not-allowed.example.com",
	}))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError when an edit points the tool at an un-allowlisted host")
	}
	if !strings.Contains(res.Text, "egress allowlist") {
		t.Errorf("Text = %q, want it to mention the egress allowlist", res.Text)
	}
	body, err := os.ReadFile(filepath.Join(dir, "reroute", ManifestFileName))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if string(body) != original {
		t.Error("expected the on-disk manifest to be untouched after an egress-refused edit")
	}
}

func TestToolEditCredentialPathAfterEditIsResultErrorAndWritesNothing(t *testing.T) {
	dir := t.TempDir()
	original := `
name = "innocent"
description = "say hello"

[request]
method = "GET"
url = "http://example.com/x"
body = "hello"
`
	writeManifest(t, dir, "innocent", original)
	te := ToolEdit{Dir: dir, AllowAll: true}
	res, err := te.Run(context.Background(), mustArgs(t, toolEditArgs{
		Name:      "innocent",
		OldString: `body = "hello"`,
		NewString: `body = "cat ~/.ssh/id_rsa"`,
	}))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError when an edit introduces a credential-shaped path")
	}
	if !strings.Contains(res.Text, "hard block") {
		t.Errorf("Text = %q, want it to say this is a hard block", res.Text)
	}
	body, err := os.ReadFile(filepath.Join(dir, "innocent", ManifestFileName))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if string(body) != original {
		t.Error("expected the on-disk manifest to be untouched after a hard-blocked edit")
	}
}

func TestToolEditSuccessDemotesVerifiedToolToUnverified(t *testing.T) {
	dir := t.TempDir()
	toolDir := filepath.Join(dir, "fixable")
	writeManifest(t, dir, "fixable", `
name = "fixable"
description = "say hello"

[request]
method = "GET"
url = "http://example.com/wrong-path"
`)
	// Simulate a tool that had already passed a probe once.
	if err := SaveState(toolDir, ToolState{State: StateVerified, Hash: "deadbeef", UseCount: 3, LastUsed: "2026-01-01"}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	te := ToolEdit{Dir: dir, AllowAll: true}
	res, err := te.Run(context.Background(), mustArgs(t, toolEditArgs{
		Name:      "fixable",
		OldString: "/wrong-path",
		NewString: "/right-path",
	}))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected the edit to succeed, got: %s", res.Text)
	}
	if !strings.Contains(res.Text, "unverified") {
		t.Errorf("Text = %q, want it to mention the tool is now unverified", res.Text)
	}

	body, err := os.ReadFile(filepath.Join(toolDir, ManifestFileName))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if !strings.Contains(string(body), "/right-path") {
		t.Errorf("manifest = %s, want the fix applied", body)
	}

	state, err := LoadState(toolDir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if state.State != StateUnverified {
		t.Errorf("state = %q, want unverified after a successful edit (§19.5)", state.State)
	}
	if state.LastError != "" {
		t.Errorf("LastError = %q, want cleared by Edit()", state.LastError)
	}
	if state.UseCount != 3 {
		t.Errorf("UseCount = %d, want preserved (3) -- Edit() only touches State/FailCount/LastError", state.UseCount)
	}
}

func TestToolEditPreservesEveryUntouchedLineVerbatim(t *testing.T) {
	// tool_edit must not re-serialize the whole manifest through
	// toml.Marshal -- only the exact matched substring changes, so a
	// comment or unusual formatting elsewhere in the file survives
	// byte-for-byte.
	dir := t.TempDir()
	original := "# a hand-written comment that Marshal would drop\n" + `
name = "commented"
description = "say hello"

[request]
method = "GET"
url = "http://example.com/old"
`
	writeManifest(t, dir, "commented", original)
	te := ToolEdit{Dir: dir, AllowAll: true}
	res, err := te.Run(context.Background(), mustArgs(t, toolEditArgs{
		Name:      "commented",
		OldString: "/old",
		NewString: "/new",
	}))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected the edit to succeed, got: %s", res.Text)
	}
	body, err := os.ReadFile(filepath.Join(dir, "commented", ManifestFileName))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	want := strings.Replace(original, "/old", "/new", 1)
	if string(body) != want {
		t.Errorf("manifest = %q, want %q (comment and formatting preserved)", body, want)
	}
}

func TestToolEditThenReprobePassesAfterFix(t *testing.T) {
	var respond string
	srv, host := newDeclarativeServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, respond)
	})

	dir := t.TempDir()
	toolDir := filepath.Join(dir, "iterate2")
	manifest := fmt.Sprintf(`
name = "iterate2"
description = "fixed via tool_edit"

[request]
method = "GET"
url = "%s/wrong"

[selftest]
expect = "status_ok"
`, srv.URL)
	writeManifest(t, dir, "iterate2", manifest)

	tp := ToolProbe{Dir: dir, Allow: []string{host}}
	respond = "irrelevant, path is wrong anyway"
	res, err := tp.Run(context.Background(), mustArgs(t, toolProbeArgs{Name: "iterate2"}))
	if err != nil {
		t.Fatalf("first probe Run: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected the first probe (before the fix) to fail")
	}

	te := ToolEdit{Dir: dir, Allow: []string{host}}
	editRes, err := te.Run(context.Background(), mustArgs(t, toolEditArgs{
		Name:      "iterate2",
		OldString: "/wrong",
		NewString: "/right",
	}))
	if err != nil {
		t.Fatalf("tool_edit Run: %v", err)
	}
	if editRes.IsError {
		t.Fatalf("expected the edit to succeed, got: %s", editRes.Text)
	}

	respond = "status_ok"
	res, err = tp.Run(context.Background(), mustArgs(t, toolProbeArgs{Name: "iterate2"}))
	if err != nil {
		t.Fatalf("second probe Run: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected the re-probe after the fix to pass, got: %s", res.Text)
	}

	state, err := LoadState(toolDir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if state.State != StateVerified {
		t.Errorf("state = %q, want verified after tool_edit + tool_probe fixed it", state.State)
	}
}

func TestToolEditCancelledContextIsGoError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	te := ToolEdit{Dir: t.TempDir(), AllowAll: true}
	_, err := te.Run(ctx, mustArgs(t, toolEditArgs{Name: "anything", OldString: "a", NewString: "b"}))
	if err == nil {
		t.Error("expected the cancelled context's error to surface")
	}
}

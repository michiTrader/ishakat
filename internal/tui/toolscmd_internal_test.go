package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// fakeToolsLister is ToolsLister's own test double, the same shape
// fakeSessionLister/fakeRecorder already follow for their own concerns
// (session_internal_test.go).
type fakeToolsLister struct {
	res         ToolsListResult
	manifests   map[string]string
	manifestErr error
	auditRes    ToolsAuditResult
	reviveOK    map[string]string
	reviveErr   map[string]error
	deleteOK    map[string]string
	deleteErr   map[string]error
	editOK      map[string]string
	editErr     map[string]error
	createOK    map[string]string
	createErr   map[string]error
}

func (f *fakeToolsLister) ListTools() ToolsListResult { return f.res }

func (f *fakeToolsLister) ToolManifest(name string) (string, error) {
	if f.manifestErr != nil {
		return "", f.manifestErr
	}
	if body, ok := f.manifests[name]; ok {
		return body, nil
	}
	return "", errors.New("no existe ninguna herramienta llamada \"" + name + "\"")
}

func (f *fakeToolsLister) AuditTools() ToolsAuditResult { return f.auditRes }

func (f *fakeToolsLister) ReviveTool(name string) (string, error) {
	if err, ok := f.reviveErr[name]; ok {
		return "", err
	}
	if status, ok := f.reviveOK[name]; ok {
		return status, nil
	}
	return "", errors.New("no existe ninguna herramienta llamada \"" + name + "\"")
}

func (f *fakeToolsLister) DeleteTool(name string, confirm bool) (string, error) {
	if err, ok := f.deleteErr[name]; ok {
		return "", err
	}
	if status, ok := f.deleteOK[name]; ok {
		return status, nil
	}
	return "", errors.New("no existe ninguna herramienta llamada \"" + name + "\"")
}

// EditTool ignores oldString/newString/replaceAll's own values: every
// test in this file that exercises /tools edit cares about the
// dispatch/parsing/rendering path, not about what a real tools.ToolEdit
// would do with those specific strings (that behavior is already covered
// by tool_edit_test.go and toolslister_test.go's own real-round-trip
// tests) -- keyed on name alone, the same shape reviveOK/reviveErr and
// deleteOK/deleteErr already use.
func (f *fakeToolsLister) EditTool(name, oldString, newString string, replaceAll bool) (string, error) {
	if err, ok := f.editErr[name]; ok {
		return "", err
	}
	if status, ok := f.editOK[name]; ok {
		return status, nil
	}
	return "", errors.New("no existe ninguna herramienta llamada \"" + name + "\"")
}

// CreateTool ignores description/url/method/reason/sources/force's own
// values -- every test in this file that exercises /tools create cares
// about the dispatch/parsing/rendering path, not about what a real
// tools.ToolCreate would do with those specific strings (that behavior
// is already covered by tool_create_test.go and toolslister_test.go's
// own real-round-trip tests) -- keyed on name alone, the same shape
// editOK/editErr already use.
func (f *fakeToolsLister) CreateTool(name, description, url, method, reason string, sources []string, force bool) (string, error) {
	if err, ok := f.createErr[name]; ok {
		return "", err
	}
	if status, ok := f.createOK[name]; ok {
		return status, nil
	}
	return "", errors.New("no se pudo crear \"" + name + "\"")
}

// withToolsLister mirrors withSessionLister/withRecorder: it assigns the
// private field directly for every test in this file except the one
// (TestOptionsToolsListerIsWiredIntoRoot below) whose entire point is to
// go through NewRoot(Options{...}) instead.
func withToolsLister(root Root, tl ToolsLister) Root {
	root.toolsLister = tl
	return root
}

// TestOptionsToolsListerIsWiredIntoRoot is ToolsLister's own regression
// test, the exact mirror of TestOptionsSessionListerIsWiredIntoRoot
// (session_internal_test.go): internal/app.Run only ever has Options, so
// if NewRoot drops Options.ToolsLister on the floor, /tools would silently
// have nothing to list while every test in this file that assigns the
// private field directly kept passing regardless.
func TestOptionsToolsListerIsWiredIntoRoot(t *testing.T) {
	tl := &fakeToolsLister{}
	root := NewRoot(Options{ToolsLister: tl})
	if root.toolsLister == nil {
		t.Fatal("NewRoot did not wire Options.ToolsLister into Root.toolsLister — /tools would have nothing to list")
	}
}

func TestSlashToolsWithNoneConfiguredSaysSo(t *testing.T) {
	var m tea.Model = newHeadlessRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/tools")

	root := m.(Root)
	if root.mode != ModeChat {
		t.Fatalf("mode = %v, want ModeChat: /tools reports inline, it does not open an overlay", root.mode)
	}
	if len(root.transcript) != 1 {
		t.Fatalf("expected one notice entry, got %d: %v", len(root.transcript), root.transcript)
	}
	if !strings.Contains(root.transcript[0].text, "no hay herramientas de capa 2 configuradas") {
		t.Errorf("notice should explain tools are not configured, got %q", root.transcript[0].text)
	}
}

func TestSlashToolsWithNoneCreatedSaysSo(t *testing.T) {
	root := withToolsLister(newHeadlessRoot(), &fakeToolsLister{})

	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/tools")

	got := m.(Root)
	if len(got.transcript) != 1 {
		t.Fatalf("expected one notice entry, got %d: %v", len(got.transcript), got.transcript)
	}
	if !strings.Contains(got.transcript[0].text, "no se ha creado ninguna herramienta") {
		t.Errorf("notice should explain no tools exist yet, got %q", got.transcript[0].text)
	}
}

func TestSlashToolsListsStatusOriginUsage(t *testing.T) {
	tl := &fakeToolsLister{res: ToolsListResult{Tools: []ToolSummary{
		{Name: "weather", Description: "consulta el clima", Danger: "low", State: "verified", UseCount: 3, LastUsed: "2026-08-10"},
		{Name: "wire-transfer", Description: "transferencia bancaria", Danger: "high", State: "broken", UseCount: 1, LastError: "timeout tras 30s"},
		{Name: "unused-tool", Danger: "low", State: "unverified"},
	}}}
	root := withToolsLister(newHeadlessRoot(), tl)

	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/tools")

	got := m.(Root)
	if len(got.transcript) != 1 {
		t.Fatalf("expected one notice entry, got %d: %v", len(got.transcript), got.transcript)
	}
	text := got.transcript[0].text
	for _, want := range []string{
		"weather", "danger=low", "state=verified", "use_count=3", "2026-08-10",
		"wire-transfer", "danger=high", "state=broken", "timeout tras 30s",
		"unused-tool", "state=unverified", "never",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("notice missing %q, got:\n%s", want, text)
		}
	}
}

func TestSlashToolsSurfacesTheDiscoverWarning(t *testing.T) {
	tl := &fakeToolsLister{res: ToolsListResult{
		Tools: []ToolSummary{{Name: "weather", State: "verified"}},
		Warn:  "could not parse tool.toml in /tmp/tools/broken: missing [request]",
	}}
	root := withToolsLister(newHeadlessRoot(), tl)

	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/tools")

	got := m.(Root)
	if !strings.Contains(got.transcript[0].text, "could not parse tool.toml") {
		t.Errorf("notice should surface the discovery warning, got %q", got.transcript[0].text)
	}
}

func TestSlashToolsCodeShowsTheManifest(t *testing.T) {
	tl := &fakeToolsLister{manifests: map[string]string{
		"weather": "[tool]\nname = \"weather\"\n",
	}}
	root := withToolsLister(newHeadlessRoot(), tl)

	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/tools code weather")

	got := m.(Root)
	if len(got.transcript) != 1 {
		t.Fatalf("expected one notice entry, got %d: %v", len(got.transcript), got.transcript)
	}
	text := got.transcript[0].text
	if !strings.Contains(text, "tools code") || !strings.Contains(text, "weather") {
		t.Errorf("notice should name the tool, got %q", text)
	}
	if !strings.Contains(text, "[tool]") {
		t.Errorf("notice should include the manifest body, got %q", text)
	}
}

func TestSlashToolsCodeWithUnknownNameReportsTheError(t *testing.T) {
	tl := &fakeToolsLister{}
	root := withToolsLister(newHeadlessRoot(), tl)

	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/tools code ghost")

	got := m.(Root)
	if !strings.Contains(got.transcript[0].text, "no existe ninguna herramienta llamada") {
		t.Errorf("notice should report the missing tool, got %q", got.transcript[0].text)
	}
}

func TestSlashToolsAuditWithNoneConfiguredSaysSo(t *testing.T) {
	var m tea.Model = newHeadlessRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/tools audit")

	root := m.(Root)
	if !strings.Contains(root.transcript[0].text, "no hay herramientas de capa 2 configuradas") {
		t.Errorf("notice should explain tools are not configured, got %q", root.transcript[0].text)
	}
}

func TestSlashToolsAuditWithNoneCreatedSaysSo(t *testing.T) {
	root := withToolsLister(newHeadlessRoot(), &fakeToolsLister{})

	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/tools audit")

	got := m.(Root)
	if !strings.Contains(got.transcript[0].text, "no se ha creado ninguna herramienta") {
		t.Errorf("notice should explain no tools exist yet, got %q", got.transcript[0].text)
	}
}

func TestSlashToolsAuditListsOriginAndHash(t *testing.T) {
	tl := &fakeToolsLister{auditRes: ToolsAuditResult{Tools: []ToolAuditEntry{
		{
			Name:        "weather",
			CreatedBy:   "model",
			Reason:      "user asked for the forecast",
			Repetitions: 2,
			SessionID:   "sess-42",
			Sources:     []string{"https://example.com/api-docs"},
			Hash:        "abc123",
		},
		{
			Name:      "wire-transfer",
			CreatedBy: "user",
			Hash:      "def456",
			Tampered:  true,
		},
	}}}
	root := withToolsLister(newHeadlessRoot(), tl)

	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/tools audit")

	got := m.(Root)
	if len(got.transcript) != 1 {
		t.Fatalf("expected one notice entry, got %d: %v", len(got.transcript), got.transcript)
	}
	text := got.transcript[0].text
	for _, want := range []string{
		"weather", "created_by=model", "repetitions=2", "session_id=sess-42",
		"user asked for the forecast", "https://example.com/api-docs", "sha256=abc123",
		"wire-transfer", "created_by=user", "session_id=never", "sources=none", "sha256=def456",
		"tampered",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("notice missing %q, got:\n%s", want, text)
		}
	}
}

func TestSlashToolsAuditReportsHashError(t *testing.T) {
	tl := &fakeToolsLister{auditRes: ToolsAuditResult{Tools: []ToolAuditEntry{
		{Name: "weather", HashError: "could not read tool.toml for hashing: no such file"},
	}}}
	root := withToolsLister(newHeadlessRoot(), tl)

	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/tools audit")

	got := m.(Root)
	if !strings.Contains(got.transcript[0].text, "sha256 unavailable") {
		t.Errorf("notice should report the hash error, got %q", got.transcript[0].text)
	}
}

func TestSlashToolsAuditSurfacesTheDiscoverWarning(t *testing.T) {
	tl := &fakeToolsLister{auditRes: ToolsAuditResult{
		Tools: []ToolAuditEntry{{Name: "weather", Hash: "abc123"}},
		Warn:  "could not parse tool.toml in /tmp/tools/broken: missing [request]",
	}}
	root := withToolsLister(newHeadlessRoot(), tl)

	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/tools audit")

	got := m.(Root)
	if !strings.Contains(got.transcript[0].text, "could not parse tool.toml") {
		t.Errorf("notice should surface the discovery warning, got %q", got.transcript[0].text)
	}
}

func TestSlashToolsReviveReportsSuccess(t *testing.T) {
	tl := &fakeToolsLister{reviveOK: map[string]string{
		"weather": "\"weather\" revivida; su estado ahora es verified.",
	}}
	root := withToolsLister(newHeadlessRoot(), tl)

	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/tools revive weather")

	got := m.(Root)
	if len(got.transcript) != 1 {
		t.Fatalf("expected one notice entry, got %d: %v", len(got.transcript), got.transcript)
	}
	text := got.transcript[0].text
	if !strings.Contains(text, "tools revive") || !strings.Contains(text, "revivida") {
		t.Errorf("notice should report the revive status, got %q", text)
	}
}

func TestSlashToolsReviveWithUnknownNameReportsTheError(t *testing.T) {
	tl := &fakeToolsLister{}
	root := withToolsLister(newHeadlessRoot(), tl)

	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/tools revive ghost")

	got := m.(Root)
	if !strings.Contains(got.transcript[0].text, "no existe ninguna herramienta llamada") {
		t.Errorf("notice should report the missing tool, got %q", got.transcript[0].text)
	}
}

func TestSlashToolsReviveWithNoneConfiguredSaysSo(t *testing.T) {
	var m tea.Model = newHeadlessRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/tools revive weather")

	root := m.(Root)
	if !strings.Contains(root.transcript[0].text, "no hay herramientas de capa 2 configuradas") {
		t.Errorf("notice should explain tools are not configured, got %q", root.transcript[0].text)
	}
}

func TestSlashToolsDeleteReportsSuccess(t *testing.T) {
	tl := &fakeToolsLister{deleteOK: map[string]string{
		"weather": "\"weather\" borrada de forma permanente. \"weather\" esta actualmente verified (nunca usada).",
	}}
	root := withToolsLister(newHeadlessRoot(), tl)

	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/tools delete weather confirm")

	got := m.(Root)
	if len(got.transcript) != 1 {
		t.Fatalf("expected one notice entry, got %d: %v", len(got.transcript), got.transcript)
	}
	text := got.transcript[0].text
	if !strings.Contains(text, "tools delete") || !strings.Contains(text, "borrada de forma permanente") {
		t.Errorf("notice should report the delete status, got %q", text)
	}
}

func TestSlashToolsDeleteWithoutConfirmReportsRefusal(t *testing.T) {
	tl := &fakeToolsLister{deleteOK: map[string]string{
		"weather": "se rehuso a borrar \"weather\" sin confirmacion. \"weather\" esta actualmente verified (nunca usada).",
	}}
	root := withToolsLister(newHeadlessRoot(), tl)

	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/tools delete weather")

	got := m.(Root)
	if !strings.Contains(got.transcript[0].text, "se rehuso a borrar") {
		t.Errorf("notice should report the refusal, got %q", got.transcript[0].text)
	}
}

func TestSlashToolsDeleteWithUnknownNameReportsTheError(t *testing.T) {
	tl := &fakeToolsLister{}
	root := withToolsLister(newHeadlessRoot(), tl)

	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/tools delete ghost confirm")

	got := m.(Root)
	if !strings.Contains(got.transcript[0].text, "no existe ninguna herramienta llamada") {
		t.Errorf("notice should report the missing tool, got %q", got.transcript[0].text)
	}
}

func TestSlashToolsDeleteWithNoneConfiguredSaysSo(t *testing.T) {
	var m tea.Model = newHeadlessRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/tools delete weather confirm")

	root := m.(Root)
	if !strings.Contains(root.transcript[0].text, "no hay herramientas de capa 2 configuradas") {
		t.Errorf("notice should explain tools are not configured, got %q", root.transcript[0].text)
	}
}

// typeToolsEditAndEnter feeds "/tools edit <name>", then old_string,
// then a literal "---" separator line, then new_string, each of those
// three body lines terminated by ctrl+j (a literal newline inserted into
// the textarea without submitting, exactly what a human would press) —
// never a literal "\n" rune, which typeAndEnter would otherwise submit
// prematurely on. Enter is pressed exactly once at the very end, mirroring
// how a human actually drives the ctrl+j-then-enter multi-line input
// mechanism root.go's own updateChat documents.
func typeToolsEditAndEnter(m tea.Model, firstLine string, bodyLines ...string) tea.Model {
	for _, r := range firstLine {
		m, _ = m.Update(tea.KeyPressMsg{Text: string(r), Code: r})
	}
	for _, line := range bodyLines {
		m, _ = m.Update(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl})
		for _, r := range line {
			m, _ = m.Update(tea.KeyPressMsg{Text: string(r), Code: r})
		}
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	return m
}

func TestSlashToolsEditReportsSuccess(t *testing.T) {
	tl := &fakeToolsLister{editOK: map[string]string{
		"weather": "replaced 1 occurrence in \"weather\"'s tool.toml. State: unverified -- run tool_probe before using it again.",
	}}
	root := withToolsLister(newHeadlessRoot(), tl)

	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeToolsEditAndEnter(m, "/tools edit weather",
		`url = "http://example.com/wrong"`,
		"---",
		`url = "http://example.com/right"`,
	)

	got := m.(Root)
	if len(got.transcript) != 1 {
		t.Fatalf("expected one notice entry, got %d: %v", len(got.transcript), got.transcript)
	}
	text := got.transcript[0].text
	if !strings.Contains(text, "tools edit") || !strings.Contains(text, "unverified") {
		t.Errorf("notice should report the edit status, got %q", text)
	}
}

func TestSlashToolsEditWithUnknownNameReportsTheError(t *testing.T) {
	tl := &fakeToolsLister{}
	root := withToolsLister(newHeadlessRoot(), tl)

	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeToolsEditAndEnter(m, "/tools edit ghost", "old", "---", "new")

	got := m.(Root)
	if !strings.Contains(got.transcript[0].text, "no existe ninguna herramienta llamada") {
		t.Errorf("notice should report the missing tool, got %q", got.transcript[0].text)
	}
}

func TestSlashToolsEditWithNoneConfiguredSaysSo(t *testing.T) {
	var m tea.Model = newHeadlessRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeToolsEditAndEnter(m, "/tools edit weather", "old", "---", "new")

	root := m.(Root)
	if !strings.Contains(root.transcript[0].text, "no hay herramientas de capa 2 configuradas") {
		t.Errorf("notice should explain tools are not configured, got %q", root.transcript[0].text)
	}
}

func TestSlashToolsEditWithReplaceAllPassesItThrough(t *testing.T) {
	var gotReplaceAll bool
	tl := &fakeToolsListerCapturingEdit{fakeToolsLister: &fakeToolsLister{
		editOK: map[string]string{"dup": "replaced 2 occurrence(s) in \"dup\"'s tool.toml. State: unverified -- run tool_probe before using it again."},
	}, onEdit: func(name, oldString, newString string, replaceAll bool) {
		gotReplaceAll = replaceAll
	}}
	root := withToolsLister(newHeadlessRoot(), tl)

	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeToolsEditAndEnter(m, "/tools edit dup", "hello", "---", "hi", "replace_all")

	got := m.(Root)
	if !strings.Contains(got.transcript[0].text, "tools edit") {
		t.Fatalf("notice should report the edit status, got %q", got.transcript[0].text)
	}
	if !gotReplaceAll {
		t.Error("expected replace_all to be passed through as true")
	}
}

// fakeToolsListerCapturingEdit wraps fakeToolsLister to additionally
// observe EditTool's own arguments -- a second, deliberately narrow test
// double rather than adding an observer field to fakeToolsLister itself,
// since no other test in this file needs to inspect EditTool's own
// arguments this closely.
type fakeToolsListerCapturingEdit struct {
	*fakeToolsLister
	onEdit func(name, oldString, newString string, replaceAll bool)
}

func (f *fakeToolsListerCapturingEdit) EditTool(name, oldString, newString string, replaceAll bool) (string, error) {
	f.onEdit(name, oldString, newString, replaceAll)
	return f.fakeToolsLister.EditTool(name, oldString, newString, replaceAll)
}

func TestParseToolsReviveArg(t *testing.T) {
	cases := []struct {
		args     string
		wantName string
		wantOK   bool
	}{
		{"", "", false},
		{"revive", "", false},
		{"revive ", "", false},
		{"revive weather", "weather", true},
		{"  revive   weather  ", "weather", true},
		{"revivex weather", "", false},
		{"weather", "", false},
		{"code weather", "", false},
	}
	for _, c := range cases {
		name, ok := parseToolsReviveArg(c.args)
		if ok != c.wantOK || name != c.wantName {
			t.Errorf("parseToolsReviveArg(%q) = (%q, %v), want (%q, %v)", c.args, name, ok, c.wantName, c.wantOK)
		}
	}
}

func TestParseToolsDeleteArg(t *testing.T) {
	cases := []struct {
		args        string
		wantName    string
		wantConfirm bool
		wantOK      bool
	}{
		{"", "", false, false},
		{"delete", "", false, false},
		{"delete ", "", false, false},
		{"delete weather", "weather", false, true},
		{"delete weather confirm", "weather", true, true},
		{"  delete   weather   confirm  ", "weather", true, true},
		// "confirm" alone as the bare argument is consumed as the trailing
		// confirm-word first, leaving an empty name — ambiguous, so this is
		// rejected rather than guessed at (matching the "no safe reading"
		// philosophy behind Confirm itself).
		{"delete confirm", "", false, false},
		{"delete confirm confirm", "confirm", true, true},
		{"delete weather confirm now", "", false, false},
		{"delete weather now", "", false, false},
		{"deletex weather", "", false, false},
		{"weather", "", false, false},
		{"revive weather", "", false, false},
	}
	for _, c := range cases {
		name, confirm, ok := parseToolsDeleteArg(c.args)
		if ok != c.wantOK || name != c.wantName || confirm != c.wantConfirm {
			t.Errorf("parseToolsDeleteArg(%q) = (%q, %v, %v), want (%q, %v, %v)",
				c.args, name, confirm, ok, c.wantName, c.wantConfirm, c.wantOK)
		}
	}
}

func TestParseToolsEditArg(t *testing.T) {
	cases := []struct {
		name           string
		args           string
		wantName       string
		wantOld        string
		wantNew        string
		wantReplaceAll bool
		wantOK         bool
	}{
		{
			name:     "basic single-line old/new",
			args:     "edit weather\nold_text\n---\nnew_text",
			wantName: "weather", wantOld: "old_text", wantNew: "new_text", wantOK: true,
		},
		{
			name:     "multi-line old and new bodies",
			args:     "edit weather\nline1\nline2\n---\nreplacement1\nreplacement2",
			wantName: "weather", wantOld: "line1\nline2", wantNew: "replacement1\nreplacement2", wantOK: true,
		},
		{
			name:           "trailing replace_all line",
			args:           "edit dup\nhello\n---\nhi\nreplace_all",
			wantName:       "dup",
			wantOld:        "hello",
			wantNew:        "hi",
			wantReplaceAll: true,
			wantOK:         true,
		},
		{
			name:     "empty new_string is allowed (a pure deletion)",
			args:     "edit weather\nold_text\n---\n",
			wantName: "weather", wantOld: "old_text", wantNew: "", wantOK: true,
		},
		{name: "empty args", args: "", wantOK: false},
		{name: "just the word edit, nothing else", args: "edit", wantOK: false},
		{name: "name with no body at all", args: "edit weather", wantOK: false},
		{name: "no separator line", args: "edit weather\nold_text\nnew_text", wantOK: false},
		{name: "empty old_string is rejected", args: "edit weather\n---\nnew_text", wantOK: false},
		{name: "name with embedded whitespace is rejected", args: "edit weather now\nold\n---\nnew", wantOK: false},
		{name: "different first word falls through", args: "revive weather\nold\n---\nnew", wantOK: false},
		{name: "editx falls through (whole-word match only)", args: "editx weather\nold\n---\nnew", wantOK: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			name, oldString, newString, replaceAll, ok := parseToolsEditArg(c.args)
			if ok != c.wantOK {
				t.Fatalf("parseToolsEditArg(%q) ok = %v, want %v", c.args, ok, c.wantOK)
			}
			if !ok {
				return
			}
			if name != c.wantName || oldString != c.wantOld || newString != c.wantNew || replaceAll != c.wantReplaceAll {
				t.Errorf("parseToolsEditArg(%q) = (%q, %q, %q, %v), want (%q, %q, %q, %v)",
					c.args, name, oldString, newString, replaceAll,
					c.wantName, c.wantOld, c.wantNew, c.wantReplaceAll)
			}
		})
	}
}

func TestIsToolsAuditArg(t *testing.T) {
	cases := []struct {
		args string
		want bool
	}{
		{"", false},
		{"audit", true},
		{"  audit  ", true},
		{"auditx", false},
		{"audit weather", false},
		{"code weather", false},
	}
	for _, c := range cases {
		if got := isToolsAuditArg(c.args); got != c.want {
			t.Errorf("isToolsAuditArg(%q) = %v, want %v", c.args, got, c.want)
		}
	}
}

func TestParseToolsCodeArg(t *testing.T) {
	cases := []struct {
		args     string
		wantName string
		wantOK   bool
	}{
		{"", "", false},
		{"code", "", false},
		{"code ", "", false},
		{"code weather", "weather", true},
		{"  code   weather  ", "weather", true},
		{"codex weather", "", false},
		{"weather", "", false},
	}
	for _, c := range cases {
		name, ok := parseToolsCodeArg(c.args)
		if ok != c.wantOK || name != c.wantName {
			t.Errorf("parseToolsCodeArg(%q) = (%q, %v), want (%q, %v)", c.args, name, ok, c.wantName, c.wantOK)
		}
	}
}

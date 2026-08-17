package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// fakePermissionsLister is PermissionsLister's own test double, the same
// shape fakeToolsLister/fakeMissionGuard already follow for their own
// concerns.
type fakePermissionsLister struct {
	snap PermissionsSnapshot
}

func (f *fakePermissionsLister) Snapshot() PermissionsSnapshot { return f.snap }

// withPermissionsLister mirrors withToolsLister: it assigns the private
// field directly for every test in this file except
// TestOptionsPermissionsListerIsWiredIntoRoot below, whose entire point is
// to go through NewRoot(Options{...}) instead.
func withPermissionsLister(root Root, pl PermissionsLister) Root {
	root.permissionsLister = pl
	return root
}

// TestOptionsPermissionsListerIsWiredIntoRoot is PermissionsLister's own
// regression test, the exact mirror of TestOptionsToolsListerIsWiredIntoRoot:
// internal/app.Run only ever has Options, so if NewRoot drops
// Options.PermissionsLister on the floor, /permissions would silently have
// nothing to show while every test in this file that assigns the private
// field directly kept passing regardless.
func TestOptionsPermissionsListerIsWiredIntoRoot(t *testing.T) {
	pl := &fakePermissionsLister{}
	root := NewRoot(Options{PermissionsLister: pl})
	if root.permissionsLister == nil {
		t.Fatal("NewRoot did not wire Options.PermissionsLister into Root.permissionsLister — /permissions would have nothing to show")
	}
}

func TestSlashPermissionsWithNoneConfiguredSaysSo(t *testing.T) {
	var m tea.Model = newHeadlessRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/permissions")

	root := m.(Root)
	if root.mode != ModeChat {
		t.Fatalf("mode = %v, want ModeChat: /permissions reports inline, it does not open an overlay", root.mode)
	}
	if len(root.transcript) != 1 {
		t.Fatalf("expected one notice entry, got %d: %v", len(root.transcript), root.transcript)
	}
	if !strings.Contains(root.transcript[0].text, "no hay una politica de permisos activa en esta sesion") {
		t.Errorf("notice should explain no policy is active, got %q", root.transcript[0].text)
	}
}

func TestSlashPermissionsWithEmptySnapshotShowsDefaults(t *testing.T) {
	pl := &fakePermissionsLister{snap: PermissionsSnapshot{
		Autonomy: "auto",
		Read:     "allow",
		Write:    "ask",
		Shell:    "ask",
	}}
	root := withPermissionsLister(newHeadlessRoot(), pl)

	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/permissions")

	got := m.(Root)
	if len(got.transcript) != 1 {
		t.Fatalf("expected one notice entry, got %d: %v", len(got.transcript), got.transcript)
	}
	text := got.transcript[0].text
	for _, want := range []string{
		"autonomy: auto",
		"read   allow",
		"write  ask",
		"shell  ask",
		"allow_session  false",
		"(no active mission constraints)",
		"bash scope: everything installed",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("notice missing %q, got:\n%s", want, text)
		}
	}
}

func TestSlashPermissionsWithPopulatedSnapshotListsRulesAndInvariants(t *testing.T) {
	pl := &fakePermissionsLister{snap: PermissionsSnapshot{
		Autonomy:     "agile",
		Read:         "allow",
		Write:        "ask",
		Shell:        "ask",
		AllowSession: true,
		MissionRules: []PermissionsMissionRule{
			{Capability: "bash", Pattern: "*playwright*"},
		},
		BashScope: []string{"git", "npm"},
		ShellDeny: []string{"rm -rf /", "mkfs*"},
		WriteDeny: []string{"~/.ssh/**", ".env"},
	}}
	root := withPermissionsLister(newHeadlessRoot(), pl)

	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/permissions")

	got := m.(Root)
	if len(got.transcript) != 1 {
		t.Fatalf("expected one notice entry, got %d: %v", len(got.transcript), got.transcript)
	}
	text := got.transcript[0].text
	for _, want := range []string{
		"autonomy: agile",
		"allow_session  true",
		"deny  bash   *playwright*",
		"bash scope: git, npm",
		"shell_deny  rm -rf /, mkfs*",
		"write_deny  ~/.ssh/**, .env",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("notice missing %q, got:\n%s", want, text)
		}
	}
}

// withTrustStoreRoot mirrors withPermissionsLister: it assigns the private
// trustStore field directly, the same field runSlashLine's own
// "/permissions autonomy <level>" branch reads, without going through
// NewRoot(Options{...}) at all -- TestOptionsPermissionsListerIsWiredIntoRoot
// above already covers that NewRoot wiring, for TrustStore specifically
// trust_internal_test.go's own newTrustRoot already covers the same thing.
func withTrustStoreRoot(root Root, store TrustStore) Root {
	root.trustStore = store
	return root
}

// TestSlashPermissionsAutonomyAppliesAndPersists is this slice's own
// central case: "/permissions autonomy agile" should behave exactly like
// choosing "2. Ask before changes" in the /trust dialog (resolveTrust) --
// same footer.Autonomy update, same TrustStore.Save call -- reusing that
// seam rather than a second write path is this slice's whole point (see
// permissionscmd.go's own package comment).
func TestSlashPermissionsAutonomyAppliesAndPersists(t *testing.T) {
	store := &fakeTrustStore{}
	root := withTrustStoreRoot(newHeadlessRoot(), store)

	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/permissions autonomy agile")

	got := m.(Root)
	if got.footer.Autonomy != "agile" {
		t.Fatalf("footer.Autonomy = %q, want %q", got.footer.Autonomy, "agile")
	}
	if len(store.saved) != 1 || store.saved[0] != "agile" {
		t.Fatalf("store.saved = %v, want [agile]", store.saved)
	}
	if len(got.transcript) != 1 {
		t.Fatalf("expected one notice entry, got %d: %v", len(got.transcript), got.transcript)
	}
	if !strings.Contains(got.transcript[0].text, "permissions autonomy") || !strings.Contains(got.transcript[0].text, "agile") {
		t.Errorf("notice should confirm the new autonomy, got %q", got.transcript[0].text)
	}
}

// TestSlashPermissionsAutonomyEveryLevel walks all three recognized
// levels through the full slash-command path, confirming
// parsePermissionsAutonomyArg's own vocabulary (permissionsAutonomyLevels)
// matches what actually gets applied -- not just "agile" as the single
// case above already checks in more depth.
func TestSlashPermissionsAutonomyEveryLevel(t *testing.T) {
	for _, level := range []string{"auto", "agile", "readonly"} {
		store := &fakeTrustStore{}
		root := withTrustStoreRoot(newHeadlessRoot(), store)

		var m tea.Model = root
		m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
		m = typeAndEnter(m, "/permissions autonomy "+level)

		got := m.(Root)
		if got.footer.Autonomy != level {
			t.Errorf("level %q: footer.Autonomy = %q, want %q", level, got.footer.Autonomy, level)
		}
		if len(store.saved) != 1 || store.saved[0] != level {
			t.Errorf("level %q: store.saved = %v, want [%s]", level, store.saved, level)
		}
	}
}

// TestSlashPermissionsAutonomyNilTrustStoreStillAppliesForSession mirrors
// TestUpdateTrustEscResolvesToAgileNotCancel's own "nil is a supported
// value" case, but for the write half of /permissions: a session with no
// TrustStore wired (Root.trustStore's own doc comment) still changes
// footer.Autonomy for the running session, it just cannot survive a
// restart -- reported inline, not silently.
func TestSlashPermissionsAutonomyNilTrustStoreStillAppliesForSession(t *testing.T) {
	root := newHeadlessRoot() // trustStore is nil here — never set.

	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/permissions autonomy readonly")

	got := m.(Root)
	if got.footer.Autonomy != "readonly" {
		t.Fatalf("footer.Autonomy = %q, want %q", got.footer.Autonomy, "readonly")
	}
	if len(got.transcript) != 1 {
		t.Fatalf("expected one notice entry, got %d: %v", len(got.transcript), got.transcript)
	}
	if !strings.Contains(got.transcript[0].text, "solo esta sesion") {
		t.Errorf("notice should say the change is session-only, got %q", got.transcript[0].text)
	}
}

// TestSlashPermissionsAutonomySaveFailureIsReportedNotSwallowed mirrors
// resolveTrust's own "the display already changed, hiding a write
// failure would be a worse surprise" reasoning: footer.Autonomy must
// still change even when persistence fails, and the failure must show up
// inline.
func TestSlashPermissionsAutonomySaveFailureIsReportedNotSwallowed(t *testing.T) {
	store := &fakeTrustStore{err: errPermissionsAutonomySaveTest}
	root := withTrustStoreRoot(newHeadlessRoot(), store)

	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/permissions autonomy agile")

	got := m.(Root)
	if got.footer.Autonomy != "agile" {
		t.Fatalf("footer.Autonomy = %q, want %q (a save failure must not roll back the live session)", got.footer.Autonomy, "agile")
	}
	if !strings.Contains(got.transcript[0].text, "no se pudo guardar") {
		t.Errorf("notice should report the save failure, got %q", got.transcript[0].text)
	}
}

// TestSlashPermissionsAutonomyUnknownLevelIsRejected confirms the one
// place this write half deliberately diverges from
// permissions.ParseAutonomy's own lenient default: an unrecognized level
// is refused outright, footer.Autonomy is left completely untouched, and
// nothing is ever handed to TrustStore.Save.
func TestSlashPermissionsAutonomyUnknownLevelIsRejected(t *testing.T) {
	store := &fakeTrustStore{}
	root := withTrustStoreRoot(newHeadlessRoot(), store)
	root.footer.Autonomy = "auto"

	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeAndEnter(m, "/permissions autonomy yolo")

	got := m.(Root)
	if got.footer.Autonomy != "auto" {
		t.Fatalf("footer.Autonomy = %q, want unchanged %q", got.footer.Autonomy, "auto")
	}
	if len(store.saved) != 0 {
		t.Fatalf("store.saved = %v, want none: an unrecognized level must never reach TrustStore.Save", store.saved)
	}
	if !strings.Contains(got.transcript[0].text, "nivel desconocido") {
		t.Errorf("notice should say the level is unknown, got %q", got.transcript[0].text)
	}
}

// errPermissionsAutonomySaveTest is a stable sentinel for
// TestSlashPermissionsAutonomySaveFailureIsReportedNotSwallowed's own
// fakeTrustStore.err, avoiding a fmt.Errorf allocation for a single test.
var errPermissionsAutonomySaveTest = errPermissionsAutonomySave{}

type errPermissionsAutonomySave struct{}

func (errPermissionsAutonomySave) Error() string { return "disco lleno" }

// TestParsePermissionsAutonomyArgTable exercises
// parsePermissionsAutonomyArg directly across the shapes
// runPermissionsCommand's own dispatch relies on: the recognized levels,
// wrong word count, an unrelated sub-command name, and empty args (the
// bare "/permissions" case, which must fall through to the snapshot
// rather than be treated as a rejected autonomy command).
func TestParsePermissionsAutonomyArgTable(t *testing.T) {
	cases := []struct {
		args      string
		wantLevel string
		wantOK    bool
	}{
		{"autonomy auto", "auto", true},
		{"autonomy agile", "agile", true},
		{"autonomy readonly", "readonly", true},
		{"autonomy yolo", "", false},
		{"autonomy", "", false},
		{"autonomy agile extra", "", false},
		{"", "", false},
		{"code some-tool", "", false},
	}
	for _, c := range cases {
		level, ok := parsePermissionsAutonomyArg(c.args)
		if ok != c.wantOK || level != c.wantLevel {
			t.Errorf("parsePermissionsAutonomyArg(%q) = (%q, %v), want (%q, %v)", c.args, level, ok, c.wantLevel, c.wantOK)
		}
	}
}

// TestIsPermissionsAutonomyArgTable exercises the narrower helper that
// distinguishes "autonomy <bad level>" (reject with a message) from
// "anything else" (fall through to the read-only snapshot) --
// runPermissionsCommand's own two-call shape depends on this distinction,
// see isPermissionsAutonomyArg's own doc comment.
func TestIsPermissionsAutonomyArgTable(t *testing.T) {
	cases := []struct {
		args string
		want bool
	}{
		{"autonomy", true},
		{"autonomy yolo", true},
		{"autonomy agile", true},
		{"", false},
		{"code some-tool", false},
		{"  ", false},
	}
	for _, c := range cases {
		if got := isPermissionsAutonomyArg(c.args); got != c.want {
			t.Errorf("isPermissionsAutonomyArg(%q) = %v, want %v", c.args, got, c.want)
		}
	}
}

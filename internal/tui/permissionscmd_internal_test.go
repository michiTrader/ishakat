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

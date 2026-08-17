package app

import (
	"testing"

	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/permissions"
)

func TestNewPermissionsListerNilGuardReturnsNil(t *testing.T) {
	if l := NewPermissionsLister(nil, config.Permissions{}); l != nil {
		t.Errorf("NewPermissionsLister(nil, ...) = %v, want nil", l)
	}
}

func TestNewPermissionsListerWithGuardReturnsNonNil(t *testing.T) {
	g := permissions.New(config.Permissions{}, false, nil)
	if l := NewPermissionsLister(g, config.Permissions{}); l == nil {
		t.Error("NewPermissionsLister(guard, ...) = nil, want a usable PermissionsLister")
	}
}

func TestPermissionsListerSnapshotReflectsGuardAndConfig(t *testing.T) {
	cfgPerms := config.Permissions{
		Read:         "allow",
		Write:        "ask",
		Shell:        "ask",
		AllowSession: true,
		ShellDeny:    []string{"rm -rf /"},
		WriteDeny:    []string{"~/.ssh/**"},
	}
	g := permissions.New(cfgPerms, false, nil)
	g.SetAutonomy(permissions.Agile)
	g.AddMissionRules([]permissions.MissionRule{{Capability: "bash", Pattern: "*playwright*"}})
	g.SetBashScope([]string{"git", "npm"})

	l := NewPermissionsLister(g, cfgPerms)
	snap := l.Snapshot()

	if snap.Autonomy != "agile" {
		t.Errorf("Autonomy = %q, want %q", snap.Autonomy, "agile")
	}
	if snap.Read != "allow" || snap.Write != "ask" || snap.Shell != "ask" {
		t.Errorf("Read/Write/Shell = %q/%q/%q, want allow/ask/ask", snap.Read, snap.Write, snap.Shell)
	}
	if !snap.AllowSession {
		t.Error("AllowSession = false, want true")
	}
	if len(snap.MissionRules) != 1 || snap.MissionRules[0].Capability != "bash" || snap.MissionRules[0].Pattern != "*playwright*" {
		t.Errorf("MissionRules = %+v, want one {bash, *playwright*} rule", snap.MissionRules)
	}
	if len(snap.BashScope) != 2 || snap.BashScope[0] != "git" || snap.BashScope[1] != "npm" {
		t.Errorf("BashScope = %v, want [git npm]", snap.BashScope)
	}
	if len(snap.ShellDeny) != 1 || snap.ShellDeny[0] != "rm -rf /" {
		t.Errorf("ShellDeny = %v, want [rm -rf /]", snap.ShellDeny)
	}
	if len(snap.WriteDeny) != 1 || snap.WriteDeny[0] != "~/.ssh/**" {
		t.Errorf("WriteDeny = %v, want [~/.ssh/**]", snap.WriteDeny)
	}
}

func TestPermissionsListerSnapshotWithNoMissionOrScopeIsEmptyNotNilProof(t *testing.T) {
	g := permissions.New(config.Permissions{}, false, nil)
	l := NewPermissionsLister(g, config.Permissions{})
	snap := l.Snapshot()

	if len(snap.MissionRules) != 0 {
		t.Errorf("MissionRules = %+v, want empty when no mission is active", snap.MissionRules)
	}
	if len(snap.BashScope) != 0 {
		t.Errorf("BashScope = %v, want empty when unrestricted", snap.BashScope)
	}
}

package app

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/trust"
)

// TestResolveProjectTrustFirstRunAsks is Step 30's own opening half of its
// closing criterion: a project with no trust.json record at all must open
// the dialog.
func TestResolveProjectTrustFirstRunAsks(t *testing.T) {
	dir := t.TempDir()
	trustFile := filepath.Join(dir, "trust.json") // does not exist yet
	cfg := &config.Config{Autonomy: config.Autonomy{Remember: true}}

	needsTrust, _, initialAutonomy, store := resolveProjectTrustWithFile(cfg, filepath.Join(dir, "project"), nil, trustFile)
	if !needsTrust {
		t.Errorf("needsTrust = false, want true for a project with no saved decision")
	}
	if initialAutonomy != "" {
		t.Errorf("initialAutonomy = %q, want empty when the dialog is about to open", initialAutonomy)
	}
	if store == nil {
		t.Errorf("trustStore = nil, want a non-nil TrustStore even before any decision exists")
	}
}

// TestResolveProjectTrustSecondRunAsksNothing is Step 30's own closing
// criterion (docs/PLAN.md §21.14 row 30) exercised directly against
// resolveProjectTrust: a project that already answered must not reopen the
// dialog, and must report back exactly the autonomy it was given.
func TestResolveProjectTrustSecondRunAsksNothing(t *testing.T) {
	dir := t.TempDir()
	project := filepath.Join(dir, "project")
	trustFile := filepath.Join(dir, "trust.json")

	store := &trust.Store{}
	store.Set(project, "auto", time.Now())
	if err := trust.Save(trustFile, store); err != nil {
		t.Fatalf("trust.Save: %v", err)
	}

	cfg := &config.Config{Autonomy: config.Autonomy{Remember: true}}
	needsTrust, _, initialAutonomy, _ := resolveProjectTrustWithFile(cfg, project, nil, trustFile)
	if needsTrust {
		t.Errorf("needsTrust = true, want false: this project already has a saved decision")
	}
	if initialAutonomy != "auto" {
		t.Errorf("initialAutonomy = %q, want %q", initialAutonomy, "auto")
	}
}

// TestResolveProjectTrustAncestorDecisionCoversChild exercises trust.Store's
// own "a parent-directory decision covers children" rule (§21.4 layer 2)
// through resolveProjectTrust, not just through internal/trust directly.
func TestResolveProjectTrustAncestorDecisionCoversChild(t *testing.T) {
	dir := t.TempDir()
	parent := filepath.Join(dir, "project")
	child := filepath.Join(parent, "sub", "dir")
	trustFile := filepath.Join(dir, "trust.json")

	store := &trust.Store{}
	store.Set(parent, "readonly", time.Now())
	if err := trust.Save(trustFile, store); err != nil {
		t.Fatalf("trust.Save: %v", err)
	}

	cfg := &config.Config{Autonomy: config.Autonomy{Remember: true}}
	needsTrust, _, initialAutonomy, _ := resolveProjectTrustWithFile(cfg, child, nil, trustFile)
	if needsTrust {
		t.Errorf("needsTrust = true, want false: the parent directory already answered")
	}
	if initialAutonomy != "readonly" {
		t.Errorf("initialAutonomy = %q, want %q", initialAutonomy, "readonly")
	}
}

// TestResolveProjectTrustRememberFalseAlwaysAsks covers
// config.Autonomy.Remember = false's own documented behaviour: even a
// project with an existing record is asked again, every run.
func TestResolveProjectTrustRememberFalseAlwaysAsks(t *testing.T) {
	dir := t.TempDir()
	project := filepath.Join(dir, "project")
	trustFile := filepath.Join(dir, "trust.json")

	store := &trust.Store{}
	store.Set(project, "auto", time.Now())
	if err := trust.Save(trustFile, store); err != nil {
		t.Fatalf("trust.Save: %v", err)
	}

	cfg := &config.Config{Autonomy: config.Autonomy{Remember: false}}
	needsTrust, _, _, _ := resolveProjectTrustWithFile(cfg, project, nil, trustFile)
	if !needsTrust {
		t.Errorf("needsTrust = false, want true: [autonomy].remember = false must ask every run")
	}
}

// TestResolveProjectTrustMissingFileIsNotAnError covers Load's own
// "absence is not failure" contract flowing through unchanged:
// resolveProjectTrust must treat a trust.json that has never been created
// exactly like an empty Store, not like a read error.
func TestResolveProjectTrustMissingFileIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{Autonomy: config.Autonomy{Remember: true}}

	needsTrust, gitInfo, _, _ := resolveProjectTrustWithFile(cfg, dir, nil, filepath.Join(dir, "does-not-exist", "trust.json"))
	if !needsTrust {
		t.Errorf("needsTrust = false, want true for a missing trust.json")
	}
	if gitInfo.InGit {
		t.Errorf("gitInfo.InGit = true, want false for a plain temp directory")
	}
}

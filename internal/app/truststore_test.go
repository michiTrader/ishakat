package app

import (
	"path/filepath"
	"testing"

	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/permissions"
	"github.com/MichiTrader/ishakat/internal/trust"
)

func TestFileTrustStoreSavePersistsRecord(t *testing.T) {
	dir := t.TempDir()
	trustFile := filepath.Join(dir, "trust.json")
	project := filepath.Join(dir, "project")

	store := &fileTrustStore{path: project, trustFile: trustFile}
	if err := store.Save("auto"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := trust.Load(trustFile)
	if err != nil {
		t.Fatalf("trust.Load: %v", err)
	}
	rec, ok := loaded.Lookup(project)
	if !ok {
		t.Fatalf("Lookup(%q) found nothing after Save", project)
	}
	if rec.Autonomy != "auto" {
		t.Errorf("rec.Autonomy = %q, want %q", rec.Autonomy, "auto")
	}
}

func TestFileTrustStoreSaveDoesNotClobberOtherProjects(t *testing.T) {
	dir := t.TempDir()
	trustFile := filepath.Join(dir, "trust.json")
	other := filepath.Join(dir, "other-project")
	project := filepath.Join(dir, "project")

	if err := (&fileTrustStore{path: other, trustFile: trustFile}).Save("readonly"); err != nil {
		t.Fatalf("Save (other): %v", err)
	}
	if err := (&fileTrustStore{path: project, trustFile: trustFile}).Save("auto"); err != nil {
		t.Fatalf("Save (project): %v", err)
	}

	loaded, err := trust.Load(trustFile)
	if err != nil {
		t.Fatalf("trust.Load: %v", err)
	}
	if rec, ok := loaded.Lookup(other); !ok || rec.Autonomy != "readonly" {
		t.Errorf("Lookup(%q) = %+v, %v; want readonly, true", other, rec, ok)
	}
	if rec, ok := loaded.Lookup(project); !ok || rec.Autonomy != "auto" {
		t.Errorf("Lookup(%q) = %+v, %v; want auto, true", project, rec, ok)
	}
}

func TestFileTrustStoreSaveUpdatesLiveGuard(t *testing.T) {
	dir := t.TempDir()
	trustFile := filepath.Join(dir, "trust.json")
	project := filepath.Join(dir, "project")

	guard := permissions.New(config.Permissions{}, false, nil)
	store := &fileTrustStore{path: project, trustFile: trustFile, guard: guard}
	if err := store.Save("readonly"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if guard.Autonomy() != permissions.Readonly {
		t.Errorf("guard.Autonomy() = %v, want Readonly", guard.Autonomy())
	}
}

func TestFileTrustStoreSaveNilGuardIsSafe(t *testing.T) {
	dir := t.TempDir()
	trustFile := filepath.Join(dir, "trust.json")
	store := &fileTrustStore{path: filepath.Join(dir, "project"), trustFile: trustFile, guard: nil}
	if err := store.Save("auto"); err != nil {
		t.Fatalf("Save with nil guard: %v", err)
	}
}

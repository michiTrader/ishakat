package skills

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSkill(t *testing.T, dir, name, content string) {
	t.Helper()
	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, FileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverMissingDirectoryIsNotAnError(t *testing.T) {
	res := Discover(filepath.Join(t.TempDir(), "does-not-exist"))
	if res.Warn != "" {
		t.Errorf("Warn = %q, want empty for a missing skills directory", res.Warn)
	}
	if len(res.Skills) != 0 {
		t.Errorf("got %d skills, want 0", len(res.Skills))
	}
}

func TestDiscoverEmptyPathIsNotAnError(t *testing.T) {
	res := Discover("")
	if res.Warn != "" || len(res.Skills) != 0 {
		t.Errorf("Discover(\"\") = %+v, want zero Result", res)
	}
}

func TestDiscoverReadsNameAndDescriptionFromFrontmatter(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "bybit", "---\nname: bybit-balance\ndescription: Read the unified account balance on Bybit.\n---\n\n# Body\n\nSome prose.\n")

	res := Discover(dir)
	if res.Warn != "" {
		t.Fatalf("unexpected Warn: %q", res.Warn)
	}
	if len(res.Skills) != 1 {
		t.Fatalf("got %d skills, want 1", len(res.Skills))
	}
	sk := res.Skills[0]
	if sk.Name != "bybit-balance" {
		t.Errorf("Name = %q, want %q", sk.Name, "bybit-balance")
	}
	if sk.Description != "Read the unified account balance on Bybit." {
		t.Errorf("Description = %q", sk.Description)
	}
	if sk.File != filepath.Join(dir, "bybit", FileName) {
		t.Errorf("File = %q", sk.File)
	}
}

func TestDiscoverFallsBackToDirectoryNameWhenFrontmatterHasNoName(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "no-name-field", "---\ndescription: has a description but no name\n---\nbody\n")

	res := Discover(dir)
	if len(res.Skills) != 1 {
		t.Fatalf("got %d skills, want 1", len(res.Skills))
	}
	if res.Skills[0].Name != "no-name-field" {
		t.Errorf("Name = %q, want the directory name as fallback", res.Skills[0].Name)
	}
}

func TestDiscoverSkipsSubdirectoryWithoutSkillFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "not-a-skill"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeSkill(t, dir, "real-skill", "---\nname: real\ndescription: d\n---\nbody\n")

	res := Discover(dir)
	if len(res.Skills) != 1 || res.Skills[0].Name != "real" {
		t.Fatalf("got %+v, want exactly the one real skill", res.Skills)
	}
}

func TestDiscoverSkipsPlainFilesAtTopLevel(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("not a skill directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeSkill(t, dir, "real-skill", "---\nname: real\ndescription: d\n---\nbody\n")

	res := Discover(dir)
	if len(res.Skills) != 1 {
		t.Fatalf("got %d skills, want 1 (README.md is a file, not a skill directory)", len(res.Skills))
	}
}

func TestDiscoverSortsByName(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "zzz-dir", "---\nname: alpha\ndescription: d\n---\n")
	writeSkill(t, dir, "aaa-dir", "---\nname: beta\ndescription: d\n---\n")

	res := Discover(dir)
	if len(res.Skills) != 2 {
		t.Fatalf("got %d skills, want 2", len(res.Skills))
	}
	if res.Skills[0].Name != "alpha" || res.Skills[1].Name != "beta" {
		t.Errorf("order = [%s, %s], want [alpha, beta] (sorted by Name, not directory order)",
			res.Skills[0].Name, res.Skills[1].Name)
	}
}

func TestDiscoverUnclosedFrontmatterIsWarnedAndSkipped(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "broken", "---\nname: broken\ndescription: never closed\nbody with no closing delimiter\n")
	writeSkill(t, dir, "good", "---\nname: good\ndescription: d\n---\nbody\n")

	res := Discover(dir)
	if res.Warn == "" {
		t.Error("expected a Warn for the unclosed frontmatter")
	}
	if len(res.Skills) != 1 || res.Skills[0].Name != "good" {
		t.Fatalf("got %+v, want only the well-formed skill", res.Skills)
	}
}

func TestDiscoverNoFrontmatterStillLoadsWithEmptyDescription(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "bare", "# Just a heading\n\nNo frontmatter block at all.\n")

	res := Discover(dir)
	if res.Warn != "" {
		t.Fatalf("unexpected Warn: %q", res.Warn)
	}
	if len(res.Skills) != 1 {
		t.Fatalf("got %d skills, want 1", len(res.Skills))
	}
	if res.Skills[0].Name != "bare" {
		t.Errorf("Name = %q, want the directory name", res.Skills[0].Name)
	}
	if res.Skills[0].Description != "" {
		t.Errorf("Description = %q, want empty (no frontmatter to read one from)", res.Skills[0].Description)
	}
}

func TestSkillBodyStripsFrontmatter(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "s", "---\nname: s\ndescription: d\n---\n\n# Heading\n\nThe actual prose.\n")

	res := Discover(dir)
	if len(res.Skills) != 1 {
		t.Fatalf("got %d skills, want 1", len(res.Skills))
	}
	body, err := res.Skills[0].Body()
	if err != nil {
		t.Fatalf("Body() error = %v", err)
	}
	if body != "# Heading\n\nThe actual prose." {
		t.Errorf("Body() = %q", body)
	}
}

func TestSummaryFormatsNameColonDescription(t *testing.T) {
	list := []Skill{
		{Name: "alpha", Description: "does alpha things"},
		{Name: "beta", Description: "does beta things"},
	}
	want := "alpha: does alpha things\nbeta: does beta things"
	if got := Summary(list); got != want {
		t.Errorf("Summary() = %q, want %q", got, want)
	}
}

func TestSummaryOfEmptyListIsEmptyString(t *testing.T) {
	if got := Summary(nil); got != "" {
		t.Errorf("Summary(nil) = %q, want empty", got)
	}
}

func TestSummaryHandlesMissingDescription(t *testing.T) {
	list := []Skill{{Name: "alpha", Description: ""}}
	got := Summary(list)
	if got != "alpha: (sin descripcion)" {
		t.Errorf("Summary() = %q", got)
	}
}

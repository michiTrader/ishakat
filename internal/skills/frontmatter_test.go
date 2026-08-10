package skills

import "testing"

func TestSplitFrontmatterOrdinaryBlock(t *testing.T) {
	content := "---\nname: x\ndescription: y\n---\nbody text\nsecond line\n"
	front, body, err := splitFrontmatter(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if front != "name: x\ndescription: y" {
		t.Errorf("front = %q", front)
	}
	if body != "body text\nsecond line" {
		t.Errorf("body = %q", body)
	}
}

func TestSplitFrontmatterNoDelimiterReturnsWholeFileAsBody(t *testing.T) {
	content := "just prose, no frontmatter\n"
	front, body, err := splitFrontmatter(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if front != "" {
		t.Errorf("front = %q, want empty", front)
	}
	if body != content {
		t.Errorf("body = %q, want the whole input unchanged", body)
	}
}

func TestSplitFrontmatterUnclosedIsError(t *testing.T) {
	_, _, err := splitFrontmatter("---\nname: x\nno closing delimiter\n")
	if err == nil {
		t.Fatal("expected an error for an unclosed frontmatter block")
	}
}

func TestSplitFrontmatterDashesNotAtLineStartAreNotADelimiter(t *testing.T) {
	// "---" has to open the very first line; a hyphen rule appearing later
	// in ordinary prose must never be mistaken for one.
	content := "prose first\n---\nthis is not frontmatter, just a markdown rule\n"
	front, body, err := splitFrontmatter(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if front != "" || body != content {
		t.Errorf("front=%q body=%q, want the whole content treated as body", front, body)
	}
}

func TestParseFrontmatterBasicFields(t *testing.T) {
	got := parseFrontmatter("name: my-skill\ndescription: Does a thing.\n")
	if got["name"] != "my-skill" {
		t.Errorf("name = %q", got["name"])
	}
	if got["description"] != "Does a thing." {
		t.Errorf("description = %q", got["description"])
	}
}

func TestParseFrontmatterIgnoresComments(t *testing.T) {
	got := parseFrontmatter("# a comment\nname: x\n")
	if got["name"] != "x" {
		t.Errorf("name = %q", got["name"])
	}
	if len(got) != 1 {
		t.Errorf("got %d fields, want 1 (comment must not become a key)", len(got))
	}
}

func TestParseFrontmatterIgnoresIndentedContinuationLines(t *testing.T) {
	front := "name: gsk-aidrive\ndescription: 'AI-Drive file storage. Actions: ls, find.\n  This continuation line must not become its own key.'\n"
	got := parseFrontmatter(front)
	if got["name"] != "gsk-aidrive" {
		t.Errorf("name = %q", got["name"])
	}
	// The folded continuation line is dropped rather than mis-parsed; the
	// description therefore ends at the first physical line only. That is
	// an accepted, documented limitation of this scanner (frontmatter.go's
	// own doc comment), not a bug this test is pinning as correct behaviour
	// to preserve accidentally.
	if _, ok := got["This continuation line must not become its own key."]; ok {
		t.Error("an indented continuation line must never be read as a new key")
	}
}

func TestParseFrontmatterStripsQuotes(t *testing.T) {
	got := parseFrontmatter(`description: "quoted value"`)
	if got["description"] != "quoted value" {
		t.Errorf("description = %q, want quotes stripped", got["description"])
	}
}

func TestParseFrontmatterFirstOccurrenceWins(t *testing.T) {
	got := parseFrontmatter("name: first\nname: second\n")
	if got["name"] != "first" {
		t.Errorf("name = %q, want %q (first occurrence wins)", got["name"], "first")
	}
}

func TestParseFrontmatterEmptyValueIsIgnored(t *testing.T) {
	got := parseFrontmatter("name:\ndescription: has a value\n")
	if _, ok := got["name"]; ok {
		t.Error("an empty value must not produce a map entry")
	}
	if got["description"] != "has a value" {
		t.Errorf("description = %q", got["description"])
	}
}

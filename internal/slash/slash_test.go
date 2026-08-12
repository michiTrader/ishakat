package slash_test

import (
	"strings"
	"testing"

	"github.com/MichiTrader/ishakat/internal/slash"
)

func TestUsageAddsArgHintOnlyWhenPresent(t *testing.T) {
	cases := []struct {
		cmd  slash.Command
		want string
	}{
		{slash.Command{Name: "help"}, "/help"},
		{slash.Command{Name: "model", ArgHint: "[texto]"}, "/model [texto]"},
	}
	for _, c := range cases {
		if got := c.cmd.Usage(); got != c.want {
			t.Errorf("Usage() = %q, want %q", got, c.want)
		}
	}
}

func TestLookupResolvesNameAndAliasCaseInsensitively(t *testing.T) {
	r := slash.NewRegistry([]slash.Command{
		{Name: "exit", Aliases: []string{"quit"}},
	})
	for _, ref := range []string{"exit", "EXIT", "quit", "QUIT"} {
		if _, ok := r.Lookup(ref); !ok {
			t.Errorf("Lookup(%q) = not found, want found", ref)
		}
	}
	if _, ok := r.Lookup("bye"); ok {
		t.Error("Lookup(\"bye\") = found, want not found")
	}
}

func TestDefaultRegistryCoversTheFullPlanTable(t *testing.T) {
	// §13 names fifteen commands by name; every one of them has to resolve,
	// or /help and the dropdown would silently disagree with the PLAN.
	want := []string{
		"help", "model", "models", "skills", "theme", "compact", "new", "resume",
		"clear", "copy", "retry", "stats", "config", "debug", "login", "exit",
	}
	r := slash.Default()
	if got := len(r.All()); got != len(want) {
		t.Fatalf("Default() has %d commands, want %d", got, len(want))
	}
	for _, name := range want {
		if _, ok := r.Lookup(name); !ok {
			t.Errorf("Default(): %q is missing from the registry", name)
		}
	}
}

func TestFilterMatchesPrefixOnNameOrAlias(t *testing.T) {
	r := slash.NewRegistry([]slash.Command{
		{Name: "model"},
		{Name: "models"},
		{Name: "compact"},
		{Name: "exit", Aliases: []string{"quit"}},
	})

	cases := []struct {
		prefix string
		want   []string
	}{
		{"mo", []string{"model", "models"}},
		{"model", []string{"model", "models"}},
		{"models", []string{"models"}},
		{"c", []string{"compact"}},
		{"qu", []string{"exit"}}, // matches through the alias
		{"", []string{"model", "models", "compact", "exit"}},
		{"zzz", nil},
	}
	for _, c := range cases {
		got := names(r.Filter(c.prefix))
		if !equalStrings(got, c.want) {
			t.Errorf("Filter(%q) = %v, want %v", c.prefix, got, c.want)
		}
	}
}

func TestFilterPreservesTableOrder(t *testing.T) {
	// The dropdown draws rows in this order (§9.6); table order is the only
	// tie-break Filter has, so a match must never come back re-sorted.
	r := slash.Default()
	got := names(r.Filter(""))
	want := names(slash.Commands)
	if !equalStrings(got, want) {
		t.Errorf("Filter(\"\") reordered the table:\n got  %v\nwant %v", got, want)
	}
}

func TestIsCommandOnlyMatchesALeadingSlash(t *testing.T) {
	cases := map[string]bool{
		"/help":               true,
		"/":                   true,
		"":                    false,
		"help":                false,
		"look at https://x/y": false,
		" /help":              false, // caller trims before calling IsCommand
	}
	for in, want := range cases {
		if got := slash.IsCommand(in); got != want {
			t.Errorf("IsCommand(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestParseSplitsNameAndArgs(t *testing.T) {
	r := slash.NewRegistry([]slash.Command{
		{Name: "model", ArgHint: "[texto]"},
		{Name: "clear"},
	})

	p := slash.Parse("/model  son45", r)
	if !p.Found || p.Command.Name != "model" {
		t.Fatalf("Parse(%q) command = %+v, want model", "/model  son45", p.Command)
	}
	if p.Args != "son45" {
		t.Errorf("Parse(%q) args = %q, want %q", "/model  son45", p.Args, "son45")
	}

	p = slash.Parse("/clear", r)
	if !p.Found || p.Args != "" {
		t.Errorf("Parse(%q) = %+v, want found with empty args", "/clear", p)
	}

	p = slash.Parse("/nope arg", r)
	if p.Found {
		t.Errorf("Parse(%q).Found = true, want false", "/nope arg")
	}
	if p.Raw != "nope" {
		t.Errorf("Parse(%q).Raw = %q, want %q", "/nope arg", p.Raw, "nope")
	}
}

func TestHelpLinesAlignsOnASharedColumn(t *testing.T) {
	r := slash.NewRegistry([]slash.Command{
		{Name: "help", Describe: "esta pantalla"},
		{Name: "model", ArgHint: "[texto]", Describe: "cambiar modelo"},
	})
	lines := r.HelpLines()
	if len(lines) != 2 {
		t.Fatalf("HelpLines() returned %d lines, want 2", len(lines))
	}
	col := func(s string) int { return strings.Index(s, "esta") }
	descCol := strings.Index(lines[1], "cambiar")
	if strings.Index(lines[0], "esta") == -1 || descCol == -1 {
		t.Fatalf("HelpLines() dropped a description: %v", lines)
	}
	if col(lines[0]) != descCol {
		t.Errorf("descriptions are not aligned:\n%q\n%q", lines[0], lines[1])
	}
	for _, l := range lines {
		if !strings.HasPrefix(l, "/") {
			t.Errorf("HelpLines() line %q does not start with its usage", l)
		}
	}
}

func names(cmds []slash.Command) []string {
	out := make([]string, len(cmds))
	for i, c := range cmds {
		out[i] = c.Name
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

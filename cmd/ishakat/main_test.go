package main

import "testing"

// TestClosestSubcommand covers the "did you mean" suggestion used by
// cmdUnknownSubcommand — the fix for the bug where a mistyped subcommand
// (e.g. `add provider nvidia`, the reversed words of `provider add nvidia`)
// used to silently fall through into the chat-prompt flag parser instead of
// producing a usage error. See the comment on main()'s `default` case.
func TestClosestSubcommand(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"doctro", "doctor"},
		{"providr", "provider"},
		{"provder", "provider"},
		{"confg", "config"},
		{"modles", "models"},
		{"versoin", "version"},
		{"add", ""},        // the actual audit scenario: no close match
		{"frobnicate", ""}, // unrelated word: no misleading suggestion
		{"", ""},
	}
	for _, c := range cases {
		if got := closestSubcommand(c.input); got != c.want {
			t.Errorf("closestSubcommand(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestLevenshtein(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"config", "config", 0},
		{"doctro", "doctor", 2},
		{"kitten", "sitting", 3},
		{"", "abc", 3},
	}
	for _, c := range cases {
		if got := levenshtein(c.a, c.b); got != c.want {
			t.Errorf("levenshtein(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

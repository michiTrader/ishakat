package tui_test

import (
	"strings"
	"testing"

	"github.com/MichiTrader/ishakat/internal/tui"
)

func TestShortenPath(t *testing.T) {
	cases := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{
			name: "a path that fits is untouched",
			in:   "~/projects/ishakat",
			max:  40,
			want: "~/projects/ishakat",
		},
		{
			name: "parents shrink one at a time, leftmost first",
			in:   "~/projects/ishakat/internal/tui",
			max:  24,
			want: "~/p/ishakat/internal/tui",
		},
		{
			name: "a tighter budget shrinks more parents but never the leaf",
			in:   "~/projects/ishakat/internal/tui",
			max:  12,
			want: "~/p/i/i/tui",
		},
		{
			name: "the leaf is truncated only when it alone busts the budget",
			in:   "~/projects/supercalifragilistic",
			max:  8,
			want: "superca…",
		},
		{
			name: "a Windows drive is never reduced to a bare letter",
			in:   `D:\projects\ishakat\internal`,
			max:  20,
			want: `D:\p\i\internal`,
		},
		{
			name: "an absolute Unix path keeps its root separator",
			in:   "/srv/www/api/handlers",
			max:  16,
			want: "/s/w/a/handlers",
		},
		{
			name: "a budget of zero produces nothing",
			in:   "~/projects/ishakat",
			max:  0,
			want: "",
		},
		{
			name: "empty input stays empty",
			in:   "",
			max:  40,
			want: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tui.ShortenPath(tc.in, tc.max)
			if got != tc.want {
				t.Errorf("ShortenPath(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
			}
		})
	}
}

// TestShortenPathNeverExceedsTheBudget is the property the callers rely on:
// the banner and the footer have already promised those columns to other
// items, so an overflow here shows up as a wrapped line on a 40-column
// terminal.
func TestShortenPathNeverExceedsTheBudget(t *testing.T) {
	paths := []string{
		"~/projects/ishakat/internal/tui",
		`D:\Users\Ana\Documents\code\ishakat`,
		"/srv/www/api/handlers/v2",
		"~",
		"/",
		"~/" + strings.Repeat("deep/", 12) + "leaf",
	}
	for _, p := range paths {
		for max := 0; max <= 45; max++ {
			got := tui.ShortenPath(p, max)
			if n := len([]rune(got)); n > max {
				t.Errorf("ShortenPath(%q, %d) = %q, which is %d runes wide", p, max, got, n)
			}
		}
	}
}

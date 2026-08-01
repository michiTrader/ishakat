package xdg_test

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/MichiTrader/ishakat/internal/xdg"
)

func TestPrettyRewritesTheHomePrefixKeepingEveryComponent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("this table uses Unix separators")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)

	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			// The reported Termux bug: the old helper printed ~/ishakat for
			// a project two levels down, inventing a directory that does not
			// exist.
			name: "nested project under home keeps its parents",
			in:   filepath.Join(home, "projects", "ishakat"),
			want: "~/projects/ishakat",
		},
		{
			name: "the home directory itself is just a tilde",
			in:   home,
			want: "~",
		},
		{
			name: "a path outside home is printed as-is",
			in:   "/srv/www/api",
			want: "/srv/www/api",
		},
		{
			name: "a sibling of home is not mistaken for being inside it",
			in:   home + "-backup",
			want: home + "-backup",
		},
		{
			name: "trailing separators are cleaned",
			in:   filepath.Join(home, "projects", "ishakat") + "/",
			want: "~/projects/ishakat",
		},
		{
			name: "empty input stays empty",
			in:   "",
			want: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := xdg.Pretty(tc.in); got != tc.want {
				t.Errorf("Pretty(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestPrettyLeavesForeignSeparatorsAlone(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("on Windows this string is a real path and gets cleaned")
	}
	// The PowerShell bug: a Windows path reaching a Unix-built formatter must
	// never grow a "~/" prefix. On a Unix host the string is meaningless as a
	// path, so the only correct answer is to hand it back untouched.
	const win = `D:\projects\ishakat`
	if got := xdg.Pretty(win); got != win {
		t.Errorf("Pretty(%q) = %q, want it unchanged", win, got)
	}
}

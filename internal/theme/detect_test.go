package theme_test

import (
	"testing"

	"github.com/MichiTrader/ishakat/internal/theme"
)

// TestDetectEnvOnConsolesWithoutTERM is the regression test for the reported
// difference between Termux (gradient) and PowerShell (everything white).
// Windows consoles do not set TERM, and the old hand-rolled detection read an
// empty TERM as "no colour at all", so the whole theme was built flat.
func TestDetectEnvOnConsolesWithoutTERM(t *testing.T) {
	cases := []struct {
		name string
		env  []string
		want theme.Capability
	}{
		{
			// Windows Terminal, the default shell host on Windows 11.
			name: "windows terminal",
			env:  []string{"WT_SESSION=6f1c-4a", "COMSPEC=C:\\Windows\\system32\\cmd.exe"},
			want: theme.CapTruecolor,
		},
		{
			// A console that says nothing about itself and is not a Windows
			// one either: no TERM means no promises, so no colour.
			name: "no TERM anywhere",
			env:  []string{"HOME=/home/user"},
			want: theme.CapNone,
		},
		{
			// CLICOLOR_FORCE is the documented way to ask for colour when the
			// environment cannot be trusted; honouring it comes for free with
			// colorprofile and is worth pinning.
			name: "CLICOLOR_FORCE without TERM",
			env:  []string{"CLICOLOR_FORCE=1"},
			want: theme.Cap16,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := theme.DetectEnv("auto", c.env); got != c.want {
				t.Errorf("DetectEnv(auto, %v) = %v, want %v", c.env, got, c.want)
			}
		})
	}
}

// TestDetectEnvReadsTheUsualTerminals keeps the classic Unix paths intact:
// they are what Termux, ssh and every Linux terminal go through, and they were
// the only ones the previous implementation got right.
func TestDetectEnvReadsTheUsualTerminals(t *testing.T) {
	cases := []struct {
		name string
		env  []string
		want theme.Capability
	}{
		{"xterm-256color", []string{"TERM=xterm-256color"}, theme.Cap256},
		{"COLORTERM truecolor", []string{"TERM=xterm-256color", "COLORTERM=truecolor"}, theme.CapTruecolor},
		{"plain xterm", []string{"TERM=xterm"}, theme.Cap16},
		{"dumb terminal", []string{"TERM=dumb"}, theme.CapNone},
		{"NO_COLOR wins over TERM", []string{"TERM=xterm-256color", "NO_COLOR=1"}, theme.CapNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := theme.DetectEnv("auto", c.env); got != c.want {
				t.Errorf("DetectEnv(auto, %v) = %v, want %v", c.env, got, c.want)
			}
		})
	}
}

// TestDetectEnvOverridesBeatDetection covers [ui] color: an explicit value is a
// user decision and must survive anything the environment says, including
// NO_COLOR, which is how someone forces colour through a pager or a CI log.
func TestDetectEnvOverridesBeatDetection(t *testing.T) {
	hostile := []string{"TERM=dumb", "NO_COLOR=1"}
	cases := map[string]theme.Capability{
		"never":     theme.CapNone,
		"off":       theme.CapNone,
		"16":        theme.Cap16,
		"256":       theme.Cap256,
		"truecolor": theme.CapTruecolor,
		"always":    theme.CapTruecolor,
	}
	for override, want := range cases {
		if got := theme.DetectEnv(override, hostile); got != want {
			t.Errorf("DetectEnv(%q, hostile env) = %v, want %v", override, got, want)
		}
	}
	// Anything unrecognised must fall through to detection rather than
	// silently disabling colour, or a typo in config.toml would look like a
	// broken theme.
	if got := theme.DetectEnv("purple", []string{"TERM=xterm-256color"}); got != theme.Cap256 {
		t.Errorf("an unknown override should fall through to detection, got %v", got)
	}
}

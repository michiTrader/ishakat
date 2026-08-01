package xdg

import (
	"path/filepath"
	"runtime"
	"strings"
)

// Pretty turns an absolute working directory into the form a human expects to
// read in the banner and the footer.
//
// The rule is deliberately boring: if the path lives under the user's home it
// is written with a leading "~", keeping every intermediate component; if it
// does not, it is printed as-is. Native separators are preserved, so Windows
// shows D:\projects\ishakat and Unix shows /srv/app.
//
// This replaces an earlier helper that split on "/" and unconditionally glued
// "~/" in front of the last component. It produced two wrong outputs reported
// from real terminals: ~/ishakat for a project actually living in
// ~/projects/ishakat (an invented path that does not exist), and the nonsense
// ~/D:\projects\ishakat on PowerShell, where "/" is not the separator and the
// path is not under the home directory at all.
func Pretty(p string) string {
	if strings.TrimSpace(p) == "" {
		return ""
	}
	clean := filepath.Clean(p)

	h := home()
	if h == "" || h == "." {
		return clean
	}
	h = filepath.Clean(h)

	if pathEqual(clean, h) {
		return "~"
	}
	prefix := h + string(filepath.Separator)
	if len(clean) > len(prefix) && pathEqual(clean[:len(prefix)], prefix) {
		return "~" + string(filepath.Separator) + clean[len(prefix):]
	}
	return clean
}

// pathEqual compares two path fragments with the case sensitivity of the host
// filesystem: Windows says C:\Users\Ana and c:\users\ana are the same place,
// Linux says they are not.
func pathEqual(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

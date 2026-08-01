package tui

import "strings"

// ellipsis is the single-cell character used when even the last component of a
// path has to be cut. A three-dot "..." would eat three columns out of the very
// budget we are trying to respect.
const ellipsis = "…"

// ShortenPath fits a display path (the output of xdg.Pretty) into max columns
// without lying about where the directory is.
//
// The strategy is the one every shell prompt converged on: keep the last
// component — the one the user actually recognises — whole, and shrink the
// parents to their initial one at a time from the left, which is the end of the
// path that carries the least information. So
// ~/projects/ishakat/internal/tui becomes ~/p/ishakat/internal/tui, then
// ~/p/i/internal/tui, and only as a last resort is the leaf itself truncated
// with an ellipsis.
//
// Two properties matter more than the exact output. First, the result never
// exceeds max, because the caller has already promised those columns to the
// banner or the footer. Second, native separators survive: a path that came in
// with backslashes goes out with backslashes, so Windows never sees a mangled
// D:/projects/ishakat.
func ShortenPath(p string, max int) string {
	if max <= 0 || p == "" {
		return ""
	}
	if runeLen(p) <= max {
		return p
	}

	sep := separatorOf(p)
	parts := strings.Split(p, sep)
	if len(parts) == 1 {
		return truncateRunes(p, max)
	}

	leaf := parts[len(parts)-1]
	for i := 0; i < len(parts)-1; i++ {
		if runeLen(strings.Join(parts, sep)) <= max {
			break
		}
		parts[i] = firstRune(parts[i])
	}
	if joined := strings.Join(parts, sep); runeLen(joined) <= max {
		return joined
	}
	// Every parent is down to a single character and it still does not fit:
	// the leaf alone is wider than the budget, so it is all we can show.
	return truncateRunes(leaf, max)
}

// separatorOf guesses which separator the path was written with. A string
// holding backslashes and no forward slash can only be a Windows path, and
// rewriting it with "/" is exactly the bug reported from PowerShell.
func separatorOf(p string) string {
	if strings.Contains(p, `\`) && !strings.Contains(p, "/") {
		return `\`
	}
	return "/"
}

// firstRune keeps the first rune of a path component. Two components are left
// whole because shortening them destroys information instead of saving space:
// a Windows drive ("D:", which without the colon stops being a drive) and the
// empty component, which is the leading "" of an absolute Unix path and is what
// keeps the root separator alive.
func firstRune(s string) string {
	r := []rune(s)
	if len(r) == 0 || isDriveLetter(s) {
		return s
	}
	return string(r[0])
}

// isDriveLetter reports whether s is a bare Windows drive such as "C:".
func isDriveLetter(s string) bool {
	if len(s) != 2 || s[1] != ':' {
		return false
	}
	c := s[0]
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func runeLen(s string) int { return len([]rune(s)) }

// truncateRunes cuts s to at most max runes, spending the last column on the
// ellipsis so the reader can tell something was removed.
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 1 {
		return string(r[:max])
	}
	return string(r[:max-1]) + ellipsis
}

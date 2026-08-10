package skills

import (
	"fmt"
	"strings"
)

// splitFrontmatter separates a SKILL.md's leading "---" delimited block from
// its body. The block must start on the file's very first line — a
// convention borrowed as-is from the SKILL.md files the Genspark CLI itself
// ships under skills/*/SKILL.md, so an author who has seen one of those
// already knows this shape. A file with no frontmatter at all (no "---" on
// line one) is not an error: front comes back empty and body is the whole
// file, so a bare-prose SKILL.md with no metadata still works, just without
// a Name/Description Discover can list — the same "missing is the ordinary
// case, not a failure" stance agentsmd.Resolve takes for an absent layer.
func splitFrontmatter(content string) (front, body string, err error) {
	const delim = "---"
	if !strings.HasPrefix(content, delim) {
		return "", content, nil
	}
	rest := content[len(delim):]
	// The delimiter line may carry a trailing newline (the ordinary case)
	// or be followed immediately by more dashes with no line break, which
	// is not a valid opening delimiter — require the rest of that first
	// line to be blank.
	nl := strings.IndexByte(rest, '\n')
	if nl == -1 {
		return "", "", fmt.Errorf("frontmatter opened with %q but the file has no second line", delim)
	}
	if strings.TrimSpace(rest[:nl]) != "" {
		return "", content, nil
	}
	rest = rest[nl+1:]

	end := strings.Index(rest, "\n"+delim)
	if end == -1 {
		return "", "", fmt.Errorf("frontmatter opened with %q but never closed", delim)
	}
	front = rest[:end]
	// Skip past the closing delimiter's own line.
	afterDelim := rest[end+1+len(delim):]
	if nl2 := strings.IndexByte(afterDelim, '\n'); nl2 != -1 {
		body = afterDelim[nl2+1:]
	}
	return front, strings.TrimSpace(body), nil
}

// parseFrontmatter reads the narrow "key: value" subset of YAML SKILL.md's
// convention actually needs — name and description as top-level scalar
// strings — one line at a time. It is deliberately not a general YAML
// parser: a real YAML library is easily the single largest dependency this
// feature could justify, for a format this package uses to read exactly two
// fields, and pulling one in would blow past §6.4's "Phase 2.5 adds zero
// dependencies" budget for something a 20-line scanner already does
// correctly for the inputs SKILL.md authors actually write. Anything this
// scanner does not recognize (nested maps, lists, multi-line block scalars,
// a metadata: block the way the CLI's own SKILL.md files carry one) is
// silently ignored rather than rejected — a skill with extra frontmatter
// fields this package does not care about yet must still load, the same
// forward-compatibility stance config.load.go takes for an unknown TOML key
// it merely warns about instead of failing on.
func parseFrontmatter(front string) map[string]string {
	fields := make(map[string]string, 4)
	for _, line := range strings.Split(front, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		// A line more indented than its key (continuation of a folded
		// scalar, as gsk-aidrive's own multi-line description shows) has no
		// ":" at the top level worth trusting as a new key — skip it rather
		// than risk parsing "lists ONE directory (non-recursive) — to
		// locate..." as a bogus key.
		if line != trimmed {
			continue
		}
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		if key == "" || value == "" {
			continue
		}
		if _, seen := fields[key]; seen {
			continue // first occurrence wins, matching TOML load's own precedent
		}
		fields[key] = value
	}
	return fields
}

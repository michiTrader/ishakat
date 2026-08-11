// ledger.go implements §19.7's "crystallization by observation": a small
// ledger of bash/fetch invocations, normalized to patterns with a count and
// a last-seen date, persisted as one JSON object per line at
// $XDG_STATE_HOME/ishakat/usage.jsonl (xdg.UsageFile — the caller's job to
// supply, matching every other path this package takes as a plain string
// rather than importing internal/xdg itself, per this package's own doc
// comment on why it stays independent of config/tools/xdg).
//
// This is gate 1's *source of evidence* for OriginAgent's Repetitions field
// (§19.6: "must prove the pattern exists"), not gate 1 itself — Evaluate in
// gate1.go never reads a Ledger; a caller reads CountFor's answer and
// passes it as Candidate.Repetitions, keeping Evaluate's own unit tests
// free of any filesystem dependency.
//
// Normalization strategy, and why: two raw invocations of "the same"
// command differ in their variable parts (a filename, a ticker symbol, a
// query string) but agree everywhere else. Rather than guess up front which
// parts are variable (a single command gives no evidence either way), a
// pattern is built incrementally by *merging* each new observation into
// whatever was recorded before: a token position that has been identical on
// every observation so far stays literal; the first position where two
// observations disagree becomes a wildcard, permanently (a wildcard token
// is never un-wildcarded by a later observation that happens to repeat an
// old value — that would make the ledger's growth non-monotonic for no
// good reason). This reproduces both of §19.7's own worked examples exactly
// (see ledger_test.go): repeated curl calls against the same host and path
// but a different ticker's query string converge on
// "curl -s api.bybit.com/v5/market/tickers*"; repeated ffmpeg calls with
// different input/output filenames but the same scale filter converge on
// "ffmpeg -i * -vf scale=1080:-1 *".
package evolve

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Record is one line of usage.jsonl: a normalized pattern, how many times
// it has been observed, and the date (YYYY-MM-DD) it was last seen.
// Exported field names are deliberately lowercase in JSON to match §19.7's
// own worked example verbatim ({"pattern":...,"n":...,"last":...}) — a
// human is meant to be able to read this file directly.
type Record struct {
	Pattern string `json:"pattern"`
	N       int    `json:"n"`
	Last    string `json:"last"`
}

// Ledger is the in-memory form of usage.jsonl: an ordered list of Records,
// order preserved across Load/Save round trips (append-only from the
// reader's point of view, matching the file's own line order) except that
// Observe moves an updated record's position to keep the file readable in
// roughly recency order — see Observe's own doc comment.
type Ledger struct {
	Records []Record
}

// LoadLedger reads path's JSONL content into a Ledger. A missing file is
// not an error — the ordinary case for an install that has not observed
// anything yet — and returns an empty Ledger, matching
// tools.DiscoverDeclarative's identical "absence is not failure" contract
// for a sibling on-disk, optional store. A line that fails to parse as one
// Record is skipped rather than failing the whole load: a ledger is
// disposable, best-effort memory (§19.7 never claims it is authoritative),
// so one corrupted line should not cost every other line's evidence.
func LoadLedger(path string) (*Ledger, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Ledger{}, nil
		}
		return nil, fmt.Errorf("could not open %s: %w", path, err)
	}
	defer f.Close()

	var l Ledger
	scanner := bufio.NewScanner(f)
	// A pattern built from a long bash command (or several already-merged
	// ones on one line, in principle) can exceed bufio.Scanner's 64KiB
	// default; a usage-ledger line is expected to be short, but this is a
	// cheap ceiling raise (1MiB, matching declarative.go's own
	// maxDeclarativeBodyBytes order of magnitude) against a scan failure
	// silently truncating the file's remaining lines.
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec Record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		l.Records = append(l.Records, rec)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("could not read %s: %w", path, err)
	}
	return &l, nil
}

// Save writes l back to path as one JSON object per line, atomically (a
// sibling temp file plus rename, the same pattern writeStringAtomic already
// uses in internal/tools for exactly the same "never a half-written file"
// reason — this package deliberately does not import internal/tools for
// one shared helper, keeping the two packages' dependency graphs
// independent per this file's own doc comment).
func Save(path string, l *Ledger) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("could not create %s: %w", dir, err)
	}

	var b strings.Builder
	for _, rec := range l.Records {
		line, err := json.Marshal(rec)
		if err != nil {
			return fmt.Errorf("could not encode record %q: %w", rec.Pattern, err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}

	tmp, err := os.CreateTemp(dir, ".ishakat-usage-*")
	if err != nil {
		return fmt.Errorf("could not create a temp file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	_, writeErr := tmp.WriteString(b.String())
	syncErr := tmp.Sync()
	closeErr := tmp.Close()
	if writeErr != nil {
		return writeErr
	}
	if syncErr != nil {
		return syncErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// Observe merges one new raw invocation (a full bash command line, or a
// fetch URL) into l, returning the resulting pattern and its updated
// count. It never itself reads or writes a file — LoadLedger/Save bracket
// a call to this, or a caller batches several Observe calls against one
// loaded Ledger before a single Save, whichever fits.
//
// Matching is by shapeKey (see mergeTokens' own doc comment): the first
// existing record whose pattern shares raw's shape is merged with it in
// place, moved to the end of l.Records (so the file's line order roughly
// tracks recency, which is the only ordering property §19.7's own example
// cares about — nothing reads this file expecting a stable sort). No
// matching record creates a brand new one, verbatim, with n=1: a single
// observation is not yet evidence that any part of it is variable.
func (l *Ledger) Observe(raw string, today string) (pattern string, count int) {
	key := shapeKey(tokenize(raw))
	for i, rec := range l.Records {
		if shapeKey(tokenize(rec.Pattern)) != key {
			continue
		}
		merged := mergePattern(rec.Pattern, raw)
		rec.Pattern = merged
		rec.N++
		rec.Last = today
		l.Records = append(l.Records[:i], l.Records[i+1:]...)
		l.Records = append(l.Records, rec)
		return rec.Pattern, rec.N
	}
	rec := Record{Pattern: raw, N: 1, Last: today}
	l.Records = append(l.Records, rec)
	return rec.Pattern, rec.N
}

// CountFor reports how many times a pattern matching raw's shape has been
// observed in records (0 if none), without mutating anything — the read
// side a caller uses to answer gate 1's "has this already repeated"
// question (Candidate.Repetitions) before ever calling Observe/Save for a
// proposal that might not even pass gate 1.
func CountFor(records []Record, raw string) int {
	key := shapeKey(tokenize(raw))
	for _, rec := range records {
		if shapeKey(tokenize(rec.Pattern)) == key {
			return rec.N
		}
	}
	return 0
}

// tokenize splits s on whitespace, treating a single- or double-quoted
// span as one token (its quotes kept, since two invocations quoting
// different values still need to line up as the same token position) —
// deliberately not a full shell lexer (no backslash escapes, no nested
// quoting): this package only ever compares tokens against each other for
// equality, it never re-executes or re-interprets them, so the exact
// quoting rules of a real shell are not this function's concern.
func tokenize(s string) []string {
	var tokens []string
	var cur strings.Builder
	var quote rune
	flush := func() {
		if cur.Len() > 0 {
			tokens = append(tokens, cur.String())
			cur.Reset()
		}
	}
	for _, r := range s {
		switch {
		case quote != 0:
			cur.WriteRune(r)
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"':
			quote = r
			cur.WriteRune(r)
		case r == ' ' || r == '\t' || r == '\n':
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return tokens
}

// shapeKey groups tokens into the bucket Observe/CountFor treat as "the
// same command": the first token (the program name, or a URL's own
// scheme+host+path prefix up to any query string) plus the total token
// count. This is a deliberately simple heuristic — two genuinely different
// workflows that happen to share a program name and argument count would
// collide into one merged (and increasingly wildcarded) pattern — refining
// it (e.g. also keying on which positions are flags) is exactly the kind
// of improvement real usage data should drive, the same "illustrative, not
// exhaustive" stance declarative.go's financeHosts list already takes for
// a different heuristic in this same step.
//
// The trailing TrimSuffix(first, "*") matters for a single-token pattern
// (a bare fetch URL with no wrapping command, e.g. tool.Fetch's own
// args.URL, as opposed to "curl -s <url>" where tokens[0] is the stable
// "curl" and this never triggers): once a second observation's differing
// query string has already merged tokens[0] into "<prefix>*"
// (mergeToken's own doc comment), this function is called again on that
// same merged Pattern to compute the record's own key on every subsequent
// Observe/CountFor call. Without stripping the trailing "*" here too, that
// third call's key ("<prefix>*\x00N") no longer equals a fresh
// observation's key ("<prefix>\x00N", stripped only at '?') and the match
// silently stops -- the third and every later observation of the same
// pattern would start a brand new record at N=1 instead of accumulating,
// exactly the failure this trims away.
func shapeKey(tokens []string) string {
	if len(tokens) == 0 {
		return ""
	}
	first := tokens[0]
	if idx := strings.IndexByte(first, '?'); idx >= 0 {
		first = first[:idx]
	}
	first = strings.TrimSuffix(first, "*")
	return first + "\x00" + strconv.Itoa(len(tokens))
}

// mergePattern merges newRaw into oldPattern token by token, per this
// file's own doc comment on the incremental-wildcarding strategy. Token
// counts are assumed equal (the caller only reaches this after shapeKey
// already matched, and shapeKey includes the token count); a defensive
// mismatch (should not happen) falls back to returning newRaw unmerged
// rather than panicking on an out-of-range index.
func mergePattern(oldPattern, newRaw string) string {
	oldTokens := tokenize(oldPattern)
	newTokens := tokenize(newRaw)
	if len(oldTokens) != len(newTokens) {
		return newRaw
	}
	merged := make([]string, len(oldTokens))
	for i := range oldTokens {
		merged[i] = mergeToken(oldTokens[i], newTokens[i])
	}
	return strings.Join(merged, " ")
}

// mergeToken merges one token position across two observations:
//   - already wildcarded stays wildcarded (a wildcard is never narrowed
//     back once evidence has shown the position varies);
//   - identical values stay that literal value;
//   - two URL-shaped values sharing everything up to their own '?' merge
//     into "<shared prefix>*" — dropping the '?' itself, matching §19.7's
//     own worked example verbatim ("api.bybit.com/v5/market/tickers*",
//     not ".../tickers?*") — rather than wildcarding the whole token, so a
//     query-only difference does not throw away the host+path structure
//     that is the actual evidence of a repeated pattern;
//   - anything else that disagrees wildcards the entire token.
func mergeToken(old, new string) string {
	if old == "*" {
		return "*"
	}
	if old == new {
		return old
	}
	oldPrefix, oldHasQuery := beforeQuery(old)
	newPrefix, newHasQuery := beforeQuery(new)
	if oldHasQuery && newHasQuery && oldPrefix == newPrefix {
		return oldPrefix + "*"
	}
	if !oldHasQuery && strings.HasSuffix(oldPrefix, "*") {
		// old was already merged into "<prefix>*" on an earlier round; a
		// new value sharing that same prefix (with or without its own
		// query string) stays merged rather than escalating to a
		// whole-token wildcard.
		prefix := strings.TrimSuffix(oldPrefix, "*")
		if strings.HasPrefix(newPrefix, prefix) {
			return prefix + "*"
		}
	}
	return "*"
}

// beforeQuery splits s at its first '?', reporting whether one was found.
// hasQuery=false returns s unchanged as the "prefix" so mergeToken's own
// literal-suffix check above can use the same helper uniformly.
func beforeQuery(s string) (prefix string, hasQuery bool) {
	if idx := strings.IndexByte(s, '?'); idx >= 0 {
		return s[:idx], true
	}
	return s, false
}

// SortedByCount returns a copy of records sorted by descending N (most
// repeated first), ties broken by Pattern for a stable, reproducible
// order — the shape a `/tools` audit view or a suggest-mode scan over
// "what has repeated the most" would want, without this package
// prescribing how that caller presents it.
func SortedByCount(records []Record) []Record {
	out := make([]Record, len(records))
	copy(out, records)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].N != out[j].N {
			return out[i].N > out[j].N
		}
		return out[i].Pattern < out[j].Pattern
	})
	return out
}

package evolve

import (
	"os"
	"path/filepath"
	"testing"
)

func TestObserveFirstCallCreatesVerbatimRecordWithCountOne(t *testing.T) {
	l := &Ledger{}
	pattern, n := l.Observe("curl -s https://api.bybit.com/v5/market/tickers?category=spot&symbol=BTCUSDT", "2026-08-01")
	if n != 1 {
		t.Fatalf("expected count 1 on first observation, got %d", n)
	}
	if pattern != "curl -s https://api.bybit.com/v5/market/tickers?category=spot&symbol=BTCUSDT" {
		t.Fatalf("expected the first observation to be recorded verbatim, got %q", pattern)
	}
}

func TestObserveMergesRepeatedBybitCallsIntoWildcardedQuery(t *testing.T) {
	// §19.7's own worked example: repeated curl calls against the same
	// host+path but a different ticker symbol converge on one pattern
	// with the query string wildcarded, not the whole token.
	l := &Ledger{}
	l.Observe("curl -s https://api.bybit.com/v5/market/tickers?category=spot&symbol=BTCUSDT", "2026-08-01")
	l.Observe("curl -s https://api.bybit.com/v5/market/tickers?category=spot&symbol=ETHUSDT", "2026-08-02")
	pattern, n := l.Observe("curl -s https://api.bybit.com/v5/market/tickers?category=spot&symbol=SOLUSDT", "2026-08-03")
	if n != 3 {
		t.Fatalf("expected count 3 after three observations, got %d", n)
	}
	want := "curl -s https://api.bybit.com/v5/market/tickers*"
	if pattern != want {
		t.Fatalf("pattern = %q, want %q", pattern, want)
	}
}

func TestObserveMergesFfmpegCallsWildcardingOnlyVaryingArgs(t *testing.T) {
	// §19.7's second worked example: different input/output filenames,
	// identical scale filter -- "ffmpeg -i * -vf scale=1080:-1 *".
	l := &Ledger{}
	l.Observe("ffmpeg -i input1.mp4 -vf scale=1080:-1 output1.mp4", "2026-08-01")
	pattern, n := l.Observe("ffmpeg -i input2.mp4 -vf scale=1080:-1 output2.mp4", "2026-08-02")
	if n != 2 {
		t.Fatalf("expected count 2, got %d", n)
	}
	want := "ffmpeg -i * -vf scale=1080:-1 *"
	if pattern != want {
		t.Fatalf("pattern = %q, want %q", pattern, want)
	}
}

func TestObserveNeverUnwildcardsAPosition(t *testing.T) {
	l := &Ledger{}
	l.Observe("ffmpeg -i input1.mp4 -vf scale=1080:-1 output1.mp4", "2026-08-01")
	l.Observe("ffmpeg -i input2.mp4 -vf scale=1080:-1 output2.mp4", "2026-08-02")
	// A third call happens to repeat "input1.mp4" exactly -- the merged
	// position must stay a wildcard, not revert to the literal value.
	pattern, _ := l.Observe("ffmpeg -i input1.mp4 -vf scale=1080:-1 output3.mp4", "2026-08-03")
	want := "ffmpeg -i * -vf scale=1080:-1 *"
	if pattern != want {
		t.Fatalf("pattern = %q, want %q (a wildcard position must never be un-wildcarded)", pattern, want)
	}
}

func TestObserveDistinctShapesStayAsSeparateRecords(t *testing.T) {
	l := &Ledger{}
	l.Observe("curl -s https://api.bybit.com/v5/market/tickers?symbol=BTCUSDT", "2026-08-01")
	l.Observe("ffmpeg -i in.mp4 -vf scale=1080:-1 out.mp4", "2026-08-02")
	if len(l.Records) != 2 {
		t.Fatalf("expected 2 distinct records for 2 distinct shapes, got %d", len(l.Records))
	}
}

func TestObserveUpdatesLastSeenDate(t *testing.T) {
	l := &Ledger{}
	l.Observe("curl -s https://api.bybit.com/v5/market/tickers?symbol=BTCUSDT", "2026-08-01")
	l.Observe("curl -s https://api.bybit.com/v5/market/tickers?symbol=ETHUSDT", "2026-08-05")
	if l.Records[0].Last != "2026-08-05" {
		t.Fatalf("expected Last to update to the most recent observation date, got %q", l.Records[0].Last)
	}
}

func TestCountForReportsZeroForUnknownPattern(t *testing.T) {
	if got := CountFor(nil, "curl -s https://example.com/x?y=1"); got != 0 {
		t.Fatalf("CountFor on an empty ledger = %d, want 0", got)
	}
}

func TestCountForMatchesByShapeNotExactText(t *testing.T) {
	l := &Ledger{}
	l.Observe("curl -s https://api.bybit.com/v5/market/tickers?symbol=BTCUSDT", "2026-08-01")
	l.Observe("curl -s https://api.bybit.com/v5/market/tickers?symbol=ETHUSDT", "2026-08-02")
	got := CountFor(l.Records, "curl -s https://api.bybit.com/v5/market/tickers?symbol=SOLUSDT")
	if got != 2 {
		t.Fatalf("CountFor = %d, want 2 (matched by shape, not exact text)", got)
	}
}

func TestSaveAndLoadLedgerRoundTrips(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state", "usage.jsonl")

	l := &Ledger{}
	l.Observe("curl -s https://api.bybit.com/v5/market/tickers?symbol=BTCUSDT", "2026-08-01")
	l.Observe("curl -s https://api.bybit.com/v5/market/tickers?symbol=ETHUSDT", "2026-08-02")
	l.Observe("ffmpeg -i in.mp4 -vf scale=1080:-1 out.mp4", "2026-08-03")

	if err := Save(path, l); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := LoadLedger(path)
	if err != nil {
		t.Fatalf("LoadLedger: %v", err)
	}
	if len(loaded.Records) != len(l.Records) {
		t.Fatalf("loaded %d records, want %d", len(loaded.Records), len(l.Records))
	}
	for i := range l.Records {
		if loaded.Records[i] != l.Records[i] {
			t.Errorf("record %d = %+v, want %+v", i, loaded.Records[i], l.Records[i])
		}
	}
}

func TestSaveCreatesParentDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "does", "not", "exist", "usage.jsonl")
	if err := Save(path, &Ledger{Records: []Record{{Pattern: "x", N: 1, Last: "2026-08-01"}}}); err != nil {
		t.Fatalf("Save into a missing directory tree: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected the file to exist after Save, stat error: %v", err)
	}
}

func TestSaveWritesFilePrivately(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "usage.jsonl")
	if err := Save(path, &Ledger{Records: []Record{{Pattern: "x", N: 1, Last: "2026-08-01"}}}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("usage.jsonl mode = %#o, want 0600", perm)
	}
}

func TestLoadLedgerMissingFileReturnsEmptyLedgerNotError(t *testing.T) {
	l, err := LoadLedger(filepath.Join(t.TempDir(), "does-not-exist.jsonl"))
	if err != nil {
		t.Fatalf("LoadLedger on a missing file: %v", err)
	}
	if len(l.Records) != 0 {
		t.Fatalf("expected an empty ledger, got %d records", len(l.Records))
	}
}

func TestLoadLedgerSkipsCorruptedLinesButKeepsGoodOnes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "usage.jsonl")
	content := `{"pattern":"good one","n":3,"last":"2026-08-01"}
not valid json at all
{"pattern":"good two","n":1,"last":"2026-08-02"}
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	l, err := LoadLedger(path)
	if err != nil {
		t.Fatalf("LoadLedger: %v", err)
	}
	if len(l.Records) != 2 {
		t.Fatalf("expected the 2 valid lines to survive a corrupted line in between, got %d records: %+v", len(l.Records), l.Records)
	}
	if l.Records[0].Pattern != "good one" || l.Records[1].Pattern != "good two" {
		t.Errorf("unexpected records after skipping corrupted line: %+v", l.Records)
	}
}

func TestSortedByCountOrdersMostRepeatedFirst(t *testing.T) {
	records := []Record{
		{Pattern: "b", N: 1, Last: "2026-08-01"},
		{Pattern: "a", N: 5, Last: "2026-08-01"},
		{Pattern: "c", N: 1, Last: "2026-08-01"},
	}
	got := SortedByCount(records)
	if got[0].Pattern != "a" {
		t.Fatalf("expected the highest-count record first, got %+v", got)
	}
	// Ties (b and c, both N=1) break by pattern name for a stable order.
	if got[1].Pattern != "b" || got[2].Pattern != "c" {
		t.Fatalf("expected tie-break by pattern name, got %+v", got)
	}
}

func TestSortedByCountDoesNotMutateInput(t *testing.T) {
	records := []Record{
		{Pattern: "b", N: 1, Last: "2026-08-01"},
		{Pattern: "a", N: 5, Last: "2026-08-01"},
	}
	_ = SortedByCount(records)
	if records[0].Pattern != "b" || records[1].Pattern != "a" {
		t.Fatalf("SortedByCount must not mutate its input, got %+v", records)
	}
}

// TestObserveMergesThreeBareURLObservationsPastTheSecond is a regression
// test for a shapeKey bug that affected exactly the shape internal/app's
// new ledgerObservingRunner feeds this package for `fetch` calls: a bare
// URL with no wrapping command, so tokens[0] IS the whole pattern (unlike
// "curl -s <url>", where the stable "curl" token never gets wildcarded and
// this bug never triggers). Before the fix, a second observation's
// differing query string wildcarded tokens[0] into "<prefix>*"
// (mergeToken), but shapeKey only ever stripped text after a literal '?',
// never a trailing '*' left by that merge -- so the *third* observation's
// freshly-computed key ("<prefix>\x00N") no longer matched the stored
// record's own key ("<prefix>*\x00N") and silently started a second,
// unrelated record at N=1 instead of accumulating to N=3.
func TestObserveMergesThreeBareURLObservationsPastTheSecond(t *testing.T) {
	l := &Ledger{}
	l.Observe("https://api.bybit.com/v5/market/tickers?symbol=BTCUSDT", "2026-08-01")
	l.Observe("https://api.bybit.com/v5/market/tickers?symbol=ETHUSDT", "2026-08-02")
	pattern, n := l.Observe("https://api.bybit.com/v5/market/tickers?symbol=SOLUSDT", "2026-08-03")

	if len(l.Records) != 1 {
		t.Fatalf("expected exactly one merged record after three observations of the same shape, got %d: %+v", len(l.Records), l.Records)
	}
	if n != 3 {
		t.Fatalf("expected count 3 after three observations, got %d", n)
	}
	want := "https://api.bybit.com/v5/market/tickers*"
	if pattern != want {
		t.Fatalf("pattern = %q, want %q", pattern, want)
	}
}

// TestCountForMatchesAThriceMergedBareURLPattern is CountFor's own half of
// the same regression: gate 1's repetition evidence (Candidate.Repetitions)
// is read through CountFor, not Observe's own return value, so a caller
// asking "how many times has a URL shaped like this one repeated" must see
// the same count Observe itself already converged on, using the identical
// shapeKey both functions share.
func TestCountForMatchesAThriceMergedBareURLPattern(t *testing.T) {
	l := &Ledger{}
	l.Observe("https://api.bybit.com/v5/market/tickers?symbol=BTCUSDT", "2026-08-01")
	l.Observe("https://api.bybit.com/v5/market/tickers?symbol=ETHUSDT", "2026-08-02")
	l.Observe("https://api.bybit.com/v5/market/tickers?symbol=SOLUSDT", "2026-08-03")

	got := CountFor(l.Records, "https://api.bybit.com/v5/market/tickers?symbol=XRPUSDT")
	if got != 3 {
		t.Fatalf("CountFor = %d, want 3", got)
	}
}

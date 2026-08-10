package tools

import (
	"os"
	"path/filepath"
	"testing"
)

// --- LoadState / SaveState ---

func TestLoadStateMissingFileReturnsUnverifiedNotError(t *testing.T) {
	dir := t.TempDir()
	s, err := LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState on a missing file returned an error: %v", err)
	}
	if s.State != StateUnverified {
		t.Fatalf("expected StateUnverified for a never-probed tool, got %q", s.State)
	}
	if s.Hash != "" {
		t.Fatalf("expected empty Hash for a never-probed tool, got %q", s.Hash)
	}
}

func TestSaveAndLoadStateRoundTrips(t *testing.T) {
	dir := t.TempDir()
	want := ToolState{
		State:     StateVerified,
		Hash:      "abc123",
		UseCount:  4,
		LastUsed:  "2026-08-01",
		FailCount: 1,
		LastError: "boom",
	}
	if err := SaveState(dir, want); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	got, err := LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if got != want {
		t.Fatalf("round trip mismatch: got %+v, want %+v", got, want)
	}
}

func TestSaveStateOverwritesExistingFile(t *testing.T) {
	dir := t.TempDir()
	if err := SaveState(dir, ToolState{State: StateUnverified}); err != nil {
		t.Fatalf("first SaveState: %v", err)
	}
	if err := SaveState(dir, ToolState{State: StateVerified, Hash: "h"}); err != nil {
		t.Fatalf("second SaveState: %v", err)
	}
	got, err := LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if got.State != StateVerified || got.Hash != "h" {
		t.Fatalf("expected the second write to win, got %+v", got)
	}
}

func TestSaveStateLeavesNoTempFilesBehind(t *testing.T) {
	dir := t.TempDir()
	if err := SaveState(dir, ToolState{State: StateVerified}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != StateFileName {
		t.Fatalf("expected exactly one file (%s), got %v", StateFileName, entries)
	}
}

func TestLoadStateCorruptedFileReturnsError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, StateFileName), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := LoadState(dir); err == nil {
		t.Fatalf("expected an error loading a corrupted state file, got nil")
	}
}

// --- ComputeHash / DetectTamper ---

func TestComputeHashIsStableForUnchangedContent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "tool.toml", "hello")
	h1, err := ComputeHash(dir, "tool.toml")
	if err != nil {
		t.Fatalf("ComputeHash: %v", err)
	}
	h2, err := ComputeHash(dir, "tool.toml")
	if err != nil {
		t.Fatalf("ComputeHash: %v", err)
	}
	if h1 != h2 {
		t.Fatalf("expected a stable hash for unchanged content, got %q then %q", h1, h2)
	}
}

func TestComputeHashChangesWhenContentChanges(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "tool.toml", "hello")
	h1, err := ComputeHash(dir, "tool.toml")
	if err != nil {
		t.Fatalf("ComputeHash: %v", err)
	}
	writeFile(t, dir, "tool.toml", "goodbye")
	h2, err := ComputeHash(dir, "tool.toml")
	if err != nil {
		t.Fatalf("ComputeHash: %v", err)
	}
	if h1 == h2 {
		t.Fatalf("expected the hash to change when content changes")
	}
}

func TestComputeHashChangesWhenFileSwapped(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "xx")
	writeFile(t, dir, "b.txt", "yy")
	h1, err := ComputeHash(dir, "a.txt", "b.txt")
	if err != nil {
		t.Fatalf("ComputeHash: %v", err)
	}
	// Swap contents between the two files: naive concatenation would
	// produce the same combined bytes, but ComputeHash prefixes each
	// chunk with its own file name, so this must differ.
	writeFile(t, dir, "a.txt", "yy")
	writeFile(t, dir, "b.txt", "xx")
	h2, err := ComputeHash(dir, "a.txt", "b.txt")
	if err != nil {
		t.Fatalf("ComputeHash: %v", err)
	}
	if h1 == h2 {
		t.Fatalf("expected swapping file contents between two names to change the hash")
	}
}

func TestComputeHashOrderMatters(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "xx")
	writeFile(t, dir, "b.txt", "yy")
	h1, err := ComputeHash(dir, "a.txt", "b.txt")
	if err != nil {
		t.Fatalf("ComputeHash: %v", err)
	}
	h2, err := ComputeHash(dir, "b.txt", "a.txt")
	if err != nil {
		t.Fatalf("ComputeHash: %v", err)
	}
	if h1 == h2 {
		t.Fatalf("expected a different path order to produce a different hash")
	}
}

func TestComputeHashMissingFileErrors(t *testing.T) {
	dir := t.TempDir()
	if _, err := ComputeHash(dir, "does-not-exist.txt"); err == nil {
		t.Fatalf("expected an error hashing a missing file")
	}
}

func TestDetectTamperNeverProbedIsNeverTampered(t *testing.T) {
	s := ToolState{State: StateUnverified}
	next, tampered := DetectTamper(s, "somehash")
	if tampered {
		t.Fatalf("expected no tamper report for a never-probed tool (empty Hash)")
	}
	if next != s {
		t.Fatalf("expected state to be unchanged, got %+v", next)
	}
}

func TestDetectTamperMatchingHashIsNotTampered(t *testing.T) {
	s := ToolState{State: StateVerified, Hash: "abc"}
	next, tampered := DetectTamper(s, "abc")
	if tampered {
		t.Fatalf("expected no tamper report when the hash matches")
	}
	if next != s {
		t.Fatalf("expected state to be unchanged, got %+v", next)
	}
}

func TestDetectTamperMismatchDemotesVerifiedToUnverified(t *testing.T) {
	s := ToolState{State: StateVerified, Hash: "abc", UseCount: 3}
	next, tampered := DetectTamper(s, "def")
	if !tampered {
		t.Fatalf("expected a tamper report when the hash mismatches")
	}
	if next.State != StateUnverified {
		t.Fatalf("expected demotion to StateUnverified, got %q", next.State)
	}
	if next.UseCount != 3 {
		t.Fatalf("expected other fields preserved, got UseCount=%d", next.UseCount)
	}
}

func TestDetectTamperMismatchOnNonVerifiedStateReportsButDoesNotChangeState(t *testing.T) {
	s := ToolState{State: StateBroken, Hash: "abc"}
	next, tampered := DetectTamper(s, "def")
	if !tampered {
		t.Fatalf("expected a tamper report even though the tool was not verified")
	}
	if next.State != StateBroken {
		t.Fatalf("expected StateBroken to remain unchanged (only StateVerified is demoted), got %q", next.State)
	}
}

// --- Probe ---

func TestProbePassTransitionsToVerifiedAndPinsHash(t *testing.T) {
	s := ToolState{State: StateUnverified, FailCount: 2, LastError: "old failure"}
	next := s.Probe(true, "newhash", "")
	if next.State != StateVerified {
		t.Fatalf("expected StateVerified, got %q", next.State)
	}
	if next.Hash != "newhash" {
		t.Fatalf("expected Hash to be pinned to the passing content, got %q", next.Hash)
	}
	if next.FailCount != 0 {
		t.Fatalf("expected FailCount reset to 0, got %d", next.FailCount)
	}
	if next.LastError != "" {
		t.Fatalf("expected LastError cleared, got %q", next.LastError)
	}
}

func TestProbeFailTransitionsToUnverifiedAndRecordsError(t *testing.T) {
	s := ToolState{State: StateBroken}
	next := s.Probe(false, "irrelevant", "self-test timed out")
	if next.State != StateUnverified {
		t.Fatalf("expected StateUnverified after a failed probe, got %q", next.State)
	}
	if next.LastError != "self-test timed out" {
		t.Fatalf("expected LastError recorded, got %q", next.LastError)
	}
}

func TestProbeFailDoesNotChangeHash(t *testing.T) {
	s := ToolState{State: StateVerified, Hash: "old"}
	next := s.Probe(false, "shouldnotbeused", "boom")
	if next.Hash != "old" {
		t.Fatalf("expected Hash unchanged on a failed probe, got %q", next.Hash)
	}
}

// --- Edit ---

func TestEditDemotesToUnverified(t *testing.T) {
	s := ToolState{State: StateVerified, Hash: "abc", FailCount: 2, LastError: "boom"}
	next := s.Edit()
	if next.State != StateUnverified {
		t.Fatalf("expected StateUnverified after Edit, got %q", next.State)
	}
	if next.FailCount != 0 {
		t.Fatalf("expected FailCount cleared, got %d", next.FailCount)
	}
	if next.LastError != "" {
		t.Fatalf("expected LastError cleared, got %q", next.LastError)
	}
}

func TestEditLeavesHashUntouched(t *testing.T) {
	s := ToolState{State: StateVerified, Hash: "abc"}
	next := s.Edit()
	if next.Hash != "abc" {
		t.Fatalf("expected Hash to remain the previous passing content's hash until re-probed, got %q", next.Hash)
	}
}

func TestEditFromBrokenAlsoGoesToUnverified(t *testing.T) {
	s := ToolState{State: StateBroken}
	next := s.Edit()
	if next.State != StateUnverified {
		t.Fatalf("expected StateUnverified, got %q", next.State)
	}
}

// --- RecordUse ---

func TestRecordUseSuccessIncrementsUseCountAndResetsFailCount(t *testing.T) {
	s := ToolState{State: StateVerified, UseCount: 2, FailCount: 1, LastError: "prior"}
	next := s.RecordUse("2026-08-10", true, "")
	if next.UseCount != 3 {
		t.Fatalf("expected UseCount incremented to 3, got %d", next.UseCount)
	}
	if next.LastUsed != "2026-08-10" {
		t.Fatalf("expected LastUsed updated, got %q", next.LastUsed)
	}
	if next.FailCount != 0 {
		t.Fatalf("expected FailCount reset to 0 on success, got %d", next.FailCount)
	}
	if next.LastError != "" {
		t.Fatalf("expected LastError cleared on success, got %q", next.LastError)
	}
	if next.State != StateVerified {
		t.Fatalf("expected State unchanged on success, got %q", next.State)
	}
}

func TestRecordUseSingleFailureDoesNotDemote(t *testing.T) {
	s := ToolState{State: StateVerified}
	next := s.RecordUse("2026-08-10", false, "timeout")
	if next.State != StateVerified {
		t.Fatalf("expected State to remain StateVerified after a single failure, got %q", next.State)
	}
	if next.FailCount != 1 {
		t.Fatalf("expected FailCount 1, got %d", next.FailCount)
	}
	if next.LastError != "timeout" {
		t.Fatalf("expected LastError recorded, got %q", next.LastError)
	}
}

func TestRecordUseTwoConsecutiveFailuresDemoteToBroken(t *testing.T) {
	s := ToolState{State: StateVerified}
	s = s.RecordUse("2026-08-09", false, "err1")
	s = s.RecordUse("2026-08-10", false, "err2")
	if s.State != StateBroken {
		t.Fatalf("expected StateBroken after 2 consecutive failures, got %q", s.State)
	}
	if s.FailCount != 2 {
		t.Fatalf("expected FailCount 2, got %d", s.FailCount)
	}
	if s.LastError != "err2" {
		t.Fatalf("expected LastError to be the most recent failure, got %q", s.LastError)
	}
}

func TestRecordUseSuccessBetweenFailuresResetsTheStreak(t *testing.T) {
	s := ToolState{State: StateVerified}
	s = s.RecordUse("2026-08-08", false, "err1")
	s = s.RecordUse("2026-08-09", true, "")
	s = s.RecordUse("2026-08-10", false, "err2")
	if s.State != StateVerified {
		t.Fatalf("expected StateVerified since failures were not consecutive, got %q", s.State)
	}
	if s.FailCount != 1 {
		t.Fatalf("expected FailCount 1 (streak reset by the success), got %d", s.FailCount)
	}
}

func TestRecordUseFailureOnUnverifiedStateDoesNotChangeState(t *testing.T) {
	s := ToolState{State: StateUnverified}
	s = s.RecordUse("2026-08-09", false, "e1")
	s = s.RecordUse("2026-08-10", false, "e2")
	if s.State != StateUnverified {
		t.Fatalf("expected StateUnverified to remain (no lower state to fall into), got %q", s.State)
	}
}

func TestRecordUseFailureOnBrokenStateStaysBroken(t *testing.T) {
	s := ToolState{State: StateBroken, FailCount: 5}
	next := s.RecordUse("2026-08-10", false, "still broken")
	if next.State != StateBroken {
		t.Fatalf("expected StateBroken to remain, got %q", next.State)
	}
	if next.FailCount != 6 {
		t.Fatalf("expected FailCount to keep incrementing, got %d", next.FailCount)
	}
}

// --- Archive / Revive ---

func TestArchiveRemembersPreviousState(t *testing.T) {
	s := ToolState{State: StateVerified}
	next := s.Archive()
	if next.State != StateArchived {
		t.Fatalf("expected StateArchived, got %q", next.State)
	}
	if next.PreviousState != StateVerified {
		t.Fatalf("expected PreviousState=StateVerified, got %q", next.PreviousState)
	}
}

func TestArchiveIsIdempotent(t *testing.T) {
	s := ToolState{State: StateArchived, PreviousState: StateVerified}
	next := s.Archive()
	if next != s {
		t.Fatalf("expected Archive on an already-archived state to be a no-op, got %+v", next)
	}
}

func TestReviveRestoresPreviousState(t *testing.T) {
	s := ToolState{State: StateVerified}
	archived := s.Archive()
	revived := archived.Revive()
	if revived.State != StateVerified {
		t.Fatalf("expected State restored to StateVerified, got %q", revived.State)
	}
	if revived.PreviousState != "" {
		t.Fatalf("expected PreviousState cleared after revive, got %q", revived.PreviousState)
	}
}

func TestReviveFromBrokenRestoresToBroken(t *testing.T) {
	s := ToolState{State: StateBroken}
	archived := s.Archive()
	revived := archived.Revive()
	if revived.State != StateBroken {
		t.Fatalf("expected State restored to StateBroken, got %q", revived.State)
	}
}

func TestReviveOnNonArchivedStateIsNoOp(t *testing.T) {
	s := ToolState{State: StateVerified}
	next := s.Revive()
	if next != s {
		t.Fatalf("expected Revive on a non-archived state to be a no-op, got %+v", next)
	}
}

func TestReviveWithEmptyPreviousStateFallsBackToVerified(t *testing.T) {
	// Defensive against a hand-edited state.json with State=archived but
	// no PreviousState recorded.
	s := ToolState{State: StateArchived, PreviousState: ""}
	next := s.Revive()
	if next.State != StateVerified {
		t.Fatalf("expected fallback to StateVerified, got %q", next.State)
	}
}

// --- CanUse ---

func TestCanUseOnlyTrueForVerified(t *testing.T) {
	cases := []struct {
		state LifecycleState
		want  bool
	}{
		{StateUnverified, false},
		{StateVerified, true},
		{StateBroken, false},
		{StateArchived, false},
	}
	for _, c := range cases {
		got := ToolState{State: c.state}.CanUse()
		if got != c.want {
			t.Errorf("CanUse() for state %q = %v, want %v", c.state, got, c.want)
		}
	}
}

// --- IsStale ---

func TestIsStaleFalseWhenArchiveDaysIsZeroOrNegative(t *testing.T) {
	s := ToolState{LastUsed: "2020-01-01"}
	if IsStale(s, "2026-08-10", 0) {
		t.Fatalf("expected archiveDays=0 to disable archiving entirely")
	}
	if IsStale(s, "2026-08-10", -5) {
		t.Fatalf("expected a negative archiveDays to disable archiving entirely")
	}
}

func TestIsStaleFalseWhenNeverUsed(t *testing.T) {
	s := ToolState{LastUsed: ""}
	if IsStale(s, "2026-08-10", 30) {
		t.Fatalf("expected a tool with no LastUsed to never be stale")
	}
}

func TestIsStaleTrueWhenExactlyAtThreshold(t *testing.T) {
	s := ToolState{LastUsed: "2026-07-11"} // exactly 30 days before 2026-08-10
	if !IsStale(s, "2026-08-10", 30) {
		t.Fatalf("expected exactly archiveDays elapsed to count as stale")
	}
}

func TestIsStaleFalseWhenBelowThreshold(t *testing.T) {
	s := ToolState{LastUsed: "2026-08-01"}
	if IsStale(s, "2026-08-10", 30) {
		t.Fatalf("expected 9 days elapsed to not be stale against a 30-day threshold")
	}
}

func TestIsStaleTrueWhenWellPastThreshold(t *testing.T) {
	s := ToolState{LastUsed: "2020-01-01"}
	if !IsStale(s, "2026-08-10", 30) {
		t.Fatalf("expected a tool unused for years to be stale")
	}
}

func TestIsStaleFalseOnMalformedLastUsed(t *testing.T) {
	s := ToolState{LastUsed: "not-a-date"}
	if IsStale(s, "2026-08-10", 1) {
		t.Fatalf("expected a malformed LastUsed to be treated as not stale")
	}
}

func TestIsStaleFalseOnMalformedToday(t *testing.T) {
	s := ToolState{LastUsed: "2020-01-01"}
	if IsStale(s, "not-a-date", 1) {
		t.Fatalf("expected a malformed today to be treated as not stale")
	}
}

// --- sortedFileNames ---

func TestSortedFileNamesSortsAndDoesNotMutateInput(t *testing.T) {
	in := []string{"b.txt", "a.txt", "c.txt"}
	got := sortedFileNames(in)
	want := []string{"a.txt", "b.txt", "c.txt"}
	if len(got) != len(want) {
		t.Fatalf("expected %d names, got %d", len(want), len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("index %d: got %q, want %q", i, got[i], want[i])
		}
	}
	if in[0] != "b.txt" || in[1] != "a.txt" {
		t.Fatalf("expected the input slice to remain unmutated, got %v", in)
	}
}

// --- integration: Probe + DetectTamper round trip ---

func TestFullLifecycleProbeUseTamperEditReprobe(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "tool.toml", "original manifest content")

	// Brand new tool: never probed.
	s, err := LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if s.CanUse() {
		t.Fatalf("expected a brand-new tool to not be usable before its first probe")
	}

	// Probe passes.
	hash, err := ComputeHash(dir, "tool.toml")
	if err != nil {
		t.Fatalf("ComputeHash: %v", err)
	}
	s = s.Probe(true, hash, "")
	if !s.CanUse() {
		t.Fatalf("expected the tool to be usable after a passing probe")
	}
	if err := SaveState(dir, s); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	// Two consecutive real-use failures demote to broken.
	s = s.RecordUse("2026-08-09", false, "e1")
	s = s.RecordUse("2026-08-10", false, "e2")
	if s.State != StateBroken {
		t.Fatalf("expected StateBroken after 2 consecutive failures, got %q", s.State)
	}
	if s.CanUse() {
		t.Fatalf("expected a broken tool to not be usable")
	}

	// tool_edit fixes it, demoting to unverified regardless.
	s = s.Edit()
	if s.State != StateUnverified {
		t.Fatalf("expected StateUnverified after Edit, got %q", s.State)
	}

	// Someone changes the file on disk without going through tool_edit's
	// own flow (simulated here directly) -- but since Edit already demoted
	// to unverified, DetectTamper should report no *additional* demotion
	// (it only demotes from StateVerified).
	writeFile(t, dir, "tool.toml", "tampered content")
	currentHash, err := ComputeHash(dir, "tool.toml")
	if err != nil {
		t.Fatalf("ComputeHash: %v", err)
	}
	_, tampered := DetectTamper(s, currentHash)
	if !tampered {
		t.Fatalf("expected DetectTamper to report the mismatch even though State was already Unverified")
	}

	// Re-probe against the new content succeeds.
	newHash, err := ComputeHash(dir, "tool.toml")
	if err != nil {
		t.Fatalf("ComputeHash: %v", err)
	}
	s = s.Probe(true, newHash, "")
	if !s.CanUse() {
		t.Fatalf("expected the tool to be usable again after a fresh passing probe")
	}

	// Now simulate tampering on a verified tool: DetectTamper must demote.
	writeFile(t, dir, "tool.toml", "sneaky change bypassing tool_edit")
	sneakyHash, err := ComputeHash(dir, "tool.toml")
	if err != nil {
		t.Fatalf("ComputeHash: %v", err)
	}
	s, tampered = DetectTamper(s, sneakyHash)
	if !tampered {
		t.Fatalf("expected tamper detection on a verified tool whose content changed on disk")
	}
	if s.CanUse() {
		t.Fatalf("expected the tampered tool to be demoted and therefore not usable")
	}
}

// --- test helper ---

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("writeFile(%s): %v", name, err)
	}
}

// suggest.go implements §19.7's decision of *whether* to offer a
// crystallization suggestion right now, and *which* pattern to offer --
// the read side of "crystallization by observation" that ledger.go's own
// SortedByCount doc comment already anticipates ("the shape ... a
// suggest-mode scan ... would want").
//
// This file deliberately answers a narrower question than gate 1
// (Evaluate, gate1.go): at the moment a suggestion might be offered there
// is no proposed tool name or description yet -- naming happens only once
// the user accepts (§19.7's worked example: the agent proposes
// `bybit_ticker` as part of the very offer), and gate 1's Dedup/Stability/
// Profitability criteria all need that name to compare against. So the
// only two of gate 1's criteria this file's NextSuggestion can meaningfully
// apply up front are Repetition (has this pattern's own count crossed
// MinRepeats) and, once the caller supplies the current catalogue size,
// Budget -- the two criteria that never depend on a name. Every other
// criterion is deferred to tool_create's own Evaluate call, once a real
// Candidate exists.
//
// Every §19.7 civility rule this package can answer without a clock or a
// terminal lives here too:
//  1. Never mid-task -- not this package's concern; a caller decides *when*
//     to ask (see internal/tui's own end-of-turn hook), never this one.
//  2. Once per pattern, ever -- Record.Dismissed (ledger.go) plus
//     DismissPattern below.
//  3. Suggestion budget (session/week) -- SuggestState plus DecideSuggestion
//     below.
//  4. Decay after N consecutive rejections -- SuggestState.RecordRejection.
//  5. Total silence with no TTY -- not this package's concern either; see
//     rule 1's note.
package evolve

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SuggestionCandidate is one already-repeated, not-yet-dismissed pattern
// eligible to be offered, named to keep gate1.Candidate's own fields
// (Name, Description, ...) untouched: this is deliberately *not* a
// Candidate, because at this point there is no proposed name or
// description yet for gate 1's Dedup/Stability/Profitability checks to
// compare against -- see this file's own package comment.
type SuggestionCandidate struct {
	Pattern string
	N       int
	Last    string
}

// NextSuggestion picks the most-repeated, non-dismissed record that has
// already reached thresholds.MinRepeats, or ok=false when none qualifies
// (an empty ledger, every record still under the threshold, or every
// record already dismissed). Ties are broken exactly as SortedByCount's
// own doc comment says: by Pattern, for a reproducible order across
// repeated calls against the same records.
//
// This never mutates records and never talks to a model -- the same
// "trivially unit-testable" bar gate1.go's own package comment holds
// itself to.
func NextSuggestion(records []Record, thresholds Thresholds) (SuggestionCandidate, bool) {
	t := thresholds.normalized()
	for _, rec := range SortedByCount(records) {
		if rec.Dismissed {
			continue
		}
		if rec.N < t.MinRepeats {
			continue
		}
		return SuggestionCandidate{Pattern: rec.Pattern, N: rec.N, Last: rec.Last}, true
	}
	return SuggestionCandidate{}, false
}

// DismissPattern marks the record whose Pattern matches exactly as
// permanently dismissed (§19.7 rule 2: "once per pattern, ever" -- "no
// remind me later"). Matched by exact Pattern text, not by shape
// (ledger.go's Observe/CountFor own matching): the caller already has the
// exact Pattern string from whichever SuggestionCandidate was just
// offered and rejected, so there is no ambiguity to resolve the way a
// fresh raw invocation's shape would need to.
//
// A pattern with no matching record is a no-op: there is nothing to
// dismiss that was never offered in the first place, the same
// "idempotent, no-op on absence" rule config.RemoveAlias already follows
// for a sibling removal case.
func (l *Ledger) DismissPattern(pattern string) {
	for i := range l.Records {
		if l.Records[i].Pattern == pattern {
			l.Records[i].Dismissed = true
			return
		}
	}
}

// SuggestState is §19.7's own persistent budget/decay bookkeeping,
// deliberately kept separate from the ledger (usage.jsonl records *what*
// has repeated; this records how the *suggest* feature itself has been
// behaving) -- a corrupt or hand-edited usage.jsonl can then never
// desynchronize these counters from what was actually shown, and vice
// versa.
//
// SessionCount (rule 3's "1 per session" half) is deliberately absent
// from this struct: a session is a single process's lifetime (there is no
// session-identifier concept elsewhere in this codebase -- see
// internal/convo.Conversation), so it belongs in the caller's own
// in-memory state, reset simply by the process exiting, never persisted
// here alongside the week counter that must survive a restart.
type SuggestState struct {
	// WeekStart is the first day (YYYY-MM-DD, ledger.go's own date
	// format) of the current rolling 7-day window. Empty means "no
	// window has started yet" -- RollWeek treats that as "start one
	// today" rather than an error, matching LoadLedger's own
	// "absence is not failure" contract for a fresh install.
	WeekStart string `json:"week_start"`
	// WeekCount is how many suggestions have been *shown* (RecordShown)
	// since WeekStart, regardless of whether they were accepted,
	// rejected, or dismissed -- rule 3's own text: "even if ten
	// opportunities were detected", the budget counts offers made, not
	// opportunities found.
	WeekCount int `json:"week_count"`
	// ConsecutiveRejects is rule 4's own counter: reset to 0 by
	// RecordAcceptance, incremented by RecordRejection, which reports
	// back once it has just reached DecayAfterRejects so the caller can
	// drop [tools.evolve].mode to "on_request" and say so.
	ConsecutiveRejects int `json:"consecutive_rejects"`
}

// suggestDateLayout matches ledger.go's own Record.Last format exactly --
// both are meant to be read by the same human looking at two small JSON
// files side by side under $XDG_STATE_HOME/ishakat.
const suggestDateLayout = "2006-01-02"

// LoadSuggestState reads path's JSON content into a SuggestState. A
// missing file is not an error -- the ordinary case for an install that
// has never shown a suggestion yet -- and returns a zero-value state,
// the same "absence is not failure" contract LoadLedger already applies
// to usage.jsonl.
func LoadSuggestState(path string) (*SuggestState, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &SuggestState{}, nil
		}
		return nil, fmt.Errorf("could not open %s: %w", path, err)
	}
	defer f.Close()

	var s SuggestState
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 4*1024), 1<<20)
	if scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			if err := json.Unmarshal([]byte(line), &s); err != nil {
				// A corrupted state file is disposable, best-effort
				// memory (the same stance ledger.go's own doc comment
				// takes for a corrupted usage.jsonl line): start over
				// rather than fail the whole load.
				return &SuggestState{}, nil
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("could not read %s: %w", path, err)
	}
	return &s, nil
}

// SaveSuggestState writes s back to path atomically (a sibling temp file
// plus rename), the same pattern ledger.go's own Save already follows for
// exactly the same "never a half-written file" reason.
func SaveSuggestState(path string, s *SuggestState) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("could not create %s: %w", dir, err)
	}

	line, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("could not encode suggest state: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".ishakat-suggest-*")
	if err != nil {
		return fmt.Errorf("could not create a temp file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	_, writeErr := tmp.Write(append(line, '\n'))
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

// RollWeek resets WeekCount to 0 and WeekStart to today, in place, once
// the current window has run its 7 days -- a rolling window measured from
// whenever the first suggestion of the current run of weeks was shown,
// not a calendar week, since nothing about rule 3 requires the two to
// align.
//
// An empty WeekStart (first run ever) starts a fresh window today rather
// than being treated as an error. A WeekStart that fails to parse (hand-
// edited, or from some future format) is treated the same way: as if no
// window had started, because trusting a value this function cannot
// itself verify would risk silently blocking every future suggestion
// forever on a typo.
func (s *SuggestState) RollWeek(today string) {
	if s.WeekStart == "" {
		s.WeekStart = today
		return
	}
	start, err := time.Parse(suggestDateLayout, s.WeekStart)
	if err != nil {
		s.WeekStart = today
		s.WeekCount = 0
		return
	}
	now, err := time.Parse(suggestDateLayout, today)
	if err != nil {
		// Cannot compare against a malformed "today" -- leave the state
		// untouched rather than guessing.
		return
	}
	if now.Sub(start) >= 7*24*time.Hour {
		s.WeekStart = today
		s.WeekCount = 0
	}
}

// RecordShown records that a suggestion was actually displayed to the
// user -- rule 3's own trigger, separate from merely detecting an
// eligible pattern (NextSuggestion returning ok=true does not by itself
// count against the budget; only actually showing it does).
func (s *SuggestState) RecordShown() { s.WeekCount++ }

// RecordAcceptance resets ConsecutiveRejects to 0: only *consecutive*
// rejections should ever drop the mode (rule 4), so a single "yes" after
// any number of "no"s clears the streak entirely rather than merely
// pausing it.
func (s *SuggestState) RecordAcceptance() { s.ConsecutiveRejects = 0 }

// RecordRejection increments ConsecutiveRejects and reports whether the
// streak has *just* reached decayAfter -- the caller's cue to drop
// [tools.evolve].mode to "on_request" and say so, exactly once, on the
// transition rather than on every rejection after it (decayedNow is only
// ever true the one time ConsecutiveRejects first equals decayAfter).
// decayAfter <= 0 disables decay entirely (always returns false), which is
// what a zero-value config.Evolve.DecayAfterRejects would otherwise
// silently do anyway (every ConsecutiveRejects == 0 comparison would be
// true on the very first rejection) -- see DecayAfterRejects' own comment
// on why 0 is not filled from a default the way MinRepeats/DedupThreshold
// are.
func (s *SuggestState) RecordRejection(decayAfter int) (decayedNow bool) {
	s.ConsecutiveRejects++
	return decayAfter > 0 && s.ConsecutiveRejects == decayAfter
}

// Decision is DecideSuggestion's outcome: whether to offer a suggestion
// right now and, if so, which pattern.
type Decision struct {
	Offer     bool
	Candidate SuggestionCandidate
}

// DecideSuggestion applies rule 3 (session/week budgets) on top of
// NextSuggestion's own rule-2-aware pick. It is pure and deterministic --
// every input is a plain value the caller already has in hand (no clock,
// no filesystem, no TTY check) -- because rules 1 ("never mid-task") and 5
// ("no TTY") are facts about *when* and *from where* this is even called,
// which only the caller (internal/tui's own end-of-turn hook) can know;
// baking either into this function would make it untestable without a
// terminal, the same reasoning gate1.Evaluate's own doc comment already
// applies to staying independent of internal/config and internal/tools.
//
// sessionCount/sessionBudget is the in-memory "1 per session" half (see
// SuggestState's own doc comment for why it is not a SuggestState field);
// weekBudget is compared against state.WeekCount, already rolled by the
// caller via RollWeek for "today".
func DecideSuggestion(records []Record, state SuggestState, thresholds Thresholds, sessionCount, sessionBudget, weekBudget int) Decision {
	if sessionBudget > 0 && sessionCount >= sessionBudget {
		return Decision{}
	}
	if weekBudget > 0 && state.WeekCount >= weekBudget {
		return Decision{}
	}
	cand, ok := NextSuggestion(records, thresholds)
	if !ok {
		return Decision{}
	}
	return Decision{Offer: true, Candidate: cand}
}

package catalog

import (
	"math"
	"sort"
	"strings"
	"time"
	"unicode"
)

// resolve.go implements §4.5: turning whatever the user typed into a model.
//
// This is the contract with the central requirement of the product —never
// having to type the exact id— so the rules are spelled out here rather
// than left implicit in the code.
//
// FOUR STAGES, IN ORDER, stopping at the first one that produces a clear
// winner:
//
//  1. Exact match against a Ref.
//  2. Exact match against an alias declared in the configuration.
//  3. Unique suffix. Two rungs: the normalized query is a word-aligned
//     suffix of exactly one reference, or failing that, the normalized
//     query is a whole word inside exactly one reference.
//  4. Fuzzy score over every reference, with the bonuses below.
//
// THE RULE THAT MATTERS MORE THAN THE SCORING: a query that does not
// produce a clear winner is NEVER an error. It opens the picker,
// prefiltered with what the user typed and with the ranked candidates
// already computed. "Model not found" on its own is forbidden by §4.5.

// AmbiguityMargin is the tiebreaker of §4.5: the best candidate has to beat
// the runner-up by more than 20% to be applied without asking. Below that
// the picker opens prefiltered.
const AmbiguityMargin = 0.20

// MinQuality is the floor under which a subsequence match is noise rather
// than an intention. A query that only matches by scattering its letters
// across a whole reference is not a match anybody meant.
const MinQuality = 0.35

// maxCandidates caps what travels to the picker. Nobody scrolls past fifty
// rows on a phone, and the list is already sorted best-first.
const maxCandidates = 50

// Scoring weights. They are constants and not configuration on purpose:
// they are a product decision, and exposing them would turn every bug
// report into "what do you have in your toml".
const (
	scoreMatch      = 1.00 // per matched rune
	bonusWordStart  = 0.60 // the rune starts a word in the original string
	bonusContiguous = 0.40 // the previous query rune matched right before it
	penaltyGap      = 0.04 // per skipped rune between two matches

	// bonusExactLeaf fires when the query normalizes to exactly the last
	// segment of the reference. This is what separates `gpt5` (which is
	// the whole leaf of gpt-5) from gpt-5-nano when both are present.
	bonusExactLeaf = 0.50

	// bonusLeafCoverage weights the fraction of the last segment that the
	// query actually covered: matching four of four runes is a much
	// stronger signal than matching four of eight.
	bonusLeafCoverage = 0.25

	// bonusProviderPrefix fires when the user explicitly named the
	// provider ("omni/son45"): they already narrowed it down themselves.
	bonusProviderPrefix = 0.15

	// Digit bonuses. This is the mechanism §4.5 calls out by name: it is
	// what makes `son45` win against `sonnet-4-0`.
	bonusDigitsEqual = 0.20 // same digits, same order
	bonusDigitsSub   = 0.10 // the query's digits are a subsequence
	penaltyDigitsBad = 0.25 // the query asked for digits that are not there

	// Local statistics, read from the cache. Frequency and recency, capped
	// low: they break ties, they do not decide.
	bonusFreqMax    = 0.10
	bonusRecentDay  = 0.06
	bonusRecentWeek = 0.03

	// penaltyDeprecated pushes models the provider is retiring down the
	// list without hiding them.
	penaltyDeprecated = 0.30

	// bonusFree only applies when the caller passes PreferFree.
	bonusFree = 0.08
)

// Outcome says how a query was resolved and, above all, whether the caller
// may act on it or has to ask the user.
type Outcome int

const (
	// OutcomeNone: there was nothing to resolve —an empty catalog, or an
	// empty query against an empty catalog.
	OutcomeNone Outcome = iota
	// OutcomeExact: the query is a reference that exists verbatim.
	OutcomeExact
	// OutcomeAlias: the query is an alias from the configuration.
	OutcomeAlias
	// OutcomeSuffix: exactly one reference ends with the query.
	OutcomeSuffix
	// OutcomeWord: the query is a whole word inside exactly one reference.
	OutcomeWord
	// OutcomeFuzzy: the fuzzy winner beat the runner-up by more than
	// AmbiguityMargin, so switching without asking is safe.
	OutcomeFuzzy
	// OutcomePicker: no clear winner. NOT an error (§4.5): the caller opens
	// the selector prefiltered with Query and the ranked Candidates.
	OutcomePicker
)

var outcomeNames = map[Outcome]string{
	OutcomeNone:   "none",
	OutcomeExact:  "exact",
	OutcomeAlias:  "alias",
	OutcomeSuffix: "suffix",
	OutcomeWord:   "word",
	OutcomeFuzzy:  "fuzzy",
	OutcomePicker: "picker",
}

func (o Outcome) String() string {
	if s, ok := outcomeNames[o]; ok {
		return s
	}
	return "unknown"
}

// Decided reports whether the outcome names one model the caller can use
// straight away.
func (o Outcome) Decided() bool {
	switch o {
	case OutcomeExact, OutcomeAlias, OutcomeSuffix, OutcomeWord, OutcomeFuzzy:
		return true
	}
	return false
}

// Candidate is a scored model, for the prefiltered picker and for test
// failure messages that need to explain themselves.
type Candidate struct {
	Model Model
	Score float64
}

// Resolution is the answer of Resolve. It is deliberately not
// `(Model, error)`: the interesting case —"I have five plausible ones"— is
// neither a value nor an error.
type Resolution struct {
	Outcome Outcome

	// Model is the winner. Only meaningful when Outcome.Decided().
	Model Model

	// Raw is what the user typed, and Query is what the picker should be
	// prefiltered with. They differ when an alias was expanded: typing
	// `smart` and getting a picker filtered by `smart` would show nothing.
	Raw   string
	Query string

	// Candidates are the plausible models, best first. Populated for the
	// picker outcomes and also, as an explanation, for the fuzzy one.
	Candidates []Candidate

	// Via records the alias chain that was walked, if any.
	Via string
}

// ResolveOptions carries what the catalog cannot know by itself. Aliases
// live in the configuration and this package must not import config (§6.1),
// so they arrive as a plain map.
type ResolveOptions struct {
	// Alias is [alias] from the configuration, keyed case-insensitively.
	Alias map[string]string

	// PreferFree mirrors the catalog setting: bonus for free models.
	PreferFree bool

	// Now is injectable so the recency bonus is testable.
	Now time.Time
}

// Resolve runs the four stages of §4.5.
//
// It never fails. The worst case is OutcomePicker with an empty candidate
// list, which the interface renders as "the selector, unfiltered" — never
// as an error.
func (c *Catalog) Resolve(text string, opts ResolveOptions) Resolution {
	raw := strings.TrimSpace(text)
	res := Resolution{Raw: raw, Query: raw}
	if c == nil || len(c.Models) == 0 {
		res.Outcome = OutcomeNone
		return res
	}
	if raw == "" {
		res.Outcome = OutcomePicker
		return res
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}

	// Stage 2 runs before the rest for every hop of the alias chain: an
	// alias is an explicit instruction from the user and outranks any
	// amount of string similarity. The hop cap is there because a cycle in
	// somebody's toml must not hang the program.
	q := raw
	via := ""
	seen := map[string]bool{}
	for hop := 0; hop < 8; hop++ {
		// Stage 1, on every hop: an alias that points at a real reference
		// is resolved here.
		if m, ok := c.Get(q); ok {
			res.Model = m
			res.Query = q
			res.Via = via
			if via != "" {
				res.Outcome = OutcomeAlias
			} else {
				res.Outcome = OutcomeExact
			}
			return res
		}
		next, ok := lookupAliasFold(opts.Alias, q)
		if !ok {
			break
		}
		if seen[strings.ToLower(q)] {
			// A cycle in the user's alias table. Guessing what they meant
			// by scoring the last hop —an alias name, not a model— would
			// pick something arbitrary, so hand it to the picker with the
			// original query instead.
			res.Outcome = OutcomePicker
			res.Query = raw
			res.Via = via
			return res
		}
		seen[strings.ToLower(q)] = true
		if via == "" {
			via = q
		} else {
			via += " → " + q
		}
		q = next
	}
	res.Query = q
	res.Via = via

	aliased := via != ""

	// Stage 3, first rung: a word-aligned suffix that only one model has.
	// This is what makes `claude-sonnet-4-5` work when a single provider
	// serves it, and what makes `gpt5` pick gpt-5 over gpt-5-nano.
	qn := normalizeRef(q)
	if len(qn.runes) > 0 {
		if hits := c.suffixHits(qn); len(hits) == 1 {
			res.Outcome = OutcomeSuffix
			if aliased {
				res.Outcome = OutcomeAlias
			}
			res.Model = c.Models[hits[0]]
			return res
		} else if len(hits) > 1 {
			// Ambiguous on purpose: two providers serving the same model
			// is exactly the case where guessing is wrong (§4.5).
			res.Outcome = OutcomePicker
			res.Candidates = c.candidatesFrom(hits, qn, q, opts)
			return res
		}

		// Stage 3, second rung: the query is a whole word inside exactly
		// one reference. `haiku` inside claude-haiku-4-5 is this case.
		if hits := c.wordHits(qn); len(hits) == 1 {
			res.Outcome = OutcomeWord
			if aliased {
				res.Outcome = OutcomeAlias
			}
			res.Model = c.Models[hits[0]]
			return res
		}
	}

	// Stage 4: fuzzy score over everything.
	cands := c.scoreAll(qn, q, opts)
	res.Candidates = cands
	switch {
	case len(cands) == 0:
		res.Outcome = OutcomePicker
	case len(cands) == 1 || clearWinner(cands[0].Score, cands[1].Score):
		res.Outcome = OutcomeFuzzy
		if aliased {
			res.Outcome = OutcomeAlias
		}
		res.Model = cands[0].Model
	default:
		res.Outcome = OutcomePicker
	}
	if len(res.Candidates) > maxCandidates {
		res.Candidates = res.Candidates[:maxCandidates]
	}
	return res
}

// Filter is the picker's incremental search (Step 10): the same scoring as
// stage 4, without any of the stage 1–3 shortcuts, returning everything
// plausible in ranked order. Sharing the scorer is the point — a picker
// that ordered results differently from `/model son45` would be a bug the
// user could see.
func (c *Catalog) Filter(text string, opts ResolveOptions) []Candidate {
	q := strings.TrimSpace(text)
	if c == nil || len(c.Models) == 0 {
		return nil
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	if q == "" {
		out := make([]Candidate, 0, len(c.Models))
		for _, m := range c.Models {
			out = append(out, Candidate{Model: m})
		}
		return out
	}
	cands := c.scoreAll(normalizeRef(q), q, opts)
	if len(cands) > maxCandidates {
		cands = cands[:maxCandidates]
	}
	return cands
}

// clearWinner applies the 20% rule of §4.5.
func clearWinner(best, second float64) bool {
	if second <= 0 {
		return best > 0
	}
	return best >= second*(1+AmbiguityMargin)
}

// lookupAliasFold reads the alias table case-insensitively, refusing an
// alias that points at itself.
func lookupAliasFold(alias map[string]string, q string) (string, bool) {
	if len(alias) == 0 {
		return "", false
	}
	if v, ok := alias[q]; ok {
		v = strings.TrimSpace(v)
		if v != "" && !strings.EqualFold(v, q) {
			return v, true
		}
		return "", false
	}
	for k, v := range alias {
		if !strings.EqualFold(k, q) {
			continue
		}
		v = strings.TrimSpace(v)
		if v != "" && !strings.EqualFold(v, q) {
			return v, true
		}
	}
	return "", false
}

// suffixHits returns the indexes of the models whose normalized reference
// ends with the normalized query, with the match starting at a word
// boundary. The boundary requirement is what stops `o` from matching every
// model that happens to end in that letter.
func (c *Catalog) suffixHits(qn normStr) []int {
	var out []int
	for i := range c.Models {
		tn := normalizeRef(c.Models[i].Ref)
		at := len(tn.runes) - len(qn.runes)
		if at < 0 || !tn.start[atSafe(at)] {
			continue
		}
		if equalRunes(qn.runes, tn.runes[at:]) {
			out = append(out, i)
		}
	}
	return out
}

// wordHits returns the models where the normalized query appears as a whole
// word: it starts at a word boundary and ends right before one (or at the
// end of the reference).
func (c *Catalog) wordHits(qn normStr) []int {
	var out []int
	for i := range c.Models {
		tn := normalizeRef(c.Models[i].Ref)
		n, m := len(qn.runes), len(tn.runes)
		if n == 0 || n > m {
			continue
		}
		for at := 0; at+n <= m; at++ {
			if !tn.start[at] {
				continue
			}
			if at+n < m && !tn.start[at+n] {
				continue // the word keeps going: not a whole word
			}
			if equalRunes(qn.runes, tn.runes[at:at+n]) {
				out = append(out, i)
				break
			}
		}
	}
	return out
}

func atSafe(i int) int {
	if i < 0 {
		return 0
	}
	return i
}

func equalRunes(a, b []rune) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// candidatesFrom scores an already-chosen subset, for the ambiguous-suffix
// path: the picker still wants them ordered.
func (c *Catalog) candidatesFrom(idx []int, qn normStr, q string, opts ResolveOptions) []Candidate {
	out := make([]Candidate, 0, len(idx))
	for _, i := range idx {
		s, ok := c.scoreModel(c.Models[i], qn, q, opts)
		if !ok {
			s = 0
		}
		out = append(out, Candidate{Model: c.Models[i], Score: s})
	}
	sortCandidates(out)
	return out
}

// scoreAll scores every model and drops the ones the query does not even
// appear in.
func (c *Catalog) scoreAll(qn normStr, q string, opts ResolveOptions) []Candidate {
	out := make([]Candidate, 0, len(c.Models))
	for _, m := range c.Models {
		s, ok := c.scoreModel(m, qn, q, opts)
		if !ok {
			continue
		}
		out = append(out, Candidate{Model: m, Score: s})
	}
	sortCandidates(out)
	return out
}

func sortCandidates(cs []Candidate) {
	sort.SliceStable(cs, func(i, j int) bool {
		if cs[i].Score != cs[j].Score {
			return cs[i].Score > cs[j].Score
		}
		return cs[i].Model.Ref < cs[j].Model.Ref
	})
}

// scoreModel is the whole of the §4.5 scoring for one model. It returns
// ok=false when the query is not even a subsequence of the reference, which
// is the cheap way to keep obvious non-matches out of the picker.
func (c *Catalog) scoreModel(m Model, qn normStr, q string, opts ResolveOptions) (float64, bool) {
	if len(qn.runes) == 0 {
		return 0, false
	}

	// A provider prefix the user typed themselves ("omni/son45") narrows
	// the search before any scoring happens.
	providerBonus := 0.0
	effQ := qn
	if head, tail, ok := strings.Cut(q, "/"); ok && tail != "" {
		hn := normalizeRef(head)
		pn := normalizeRef(m.Provider)
		if len(hn.runes) > 0 && hasPrefixRunes(pn.runes, hn.runes) {
			providerBonus = bonusProviderPrefix
			if t := normalizeRef(tail); len(t.runes) > 0 {
				effQ = t
			}
		}
	}

	tn := normalizeRef(m.Ref)
	quality, positions, ok := matchQuality(effQ, tn)
	if !ok {
		// Second chance against the human name: models.dev calls it
		// "Claude Sonnet 4.5" and people type it that way.
		if strings.TrimSpace(m.Name) != "" {
			nn := normalizeRef(m.Name)
			q2, _, ok2 := matchQuality(effQ, nn)
			if ok2 && q2 >= MinQuality {
				return q2 * 0.9, true // slightly below a reference match
			}
		}
		return 0, false
	}
	if quality < MinQuality {
		return 0, false
	}

	score := quality + providerBonus

	// Leaf coverage and the exact-leaf shortcut.
	leafLen := len(tn.runes) - tn.leafFrom
	if leafLen > 0 {
		inLeaf := 0
		for _, p := range positions {
			if p >= tn.leafFrom {
				inLeaf++
			}
		}
		score += bonusLeafCoverage * float64(inLeaf) / float64(leafLen)
		if equalRunes(effQ.runes, tn.runes[tn.leafFrom:]) {
			score += bonusExactLeaf
		}
	}

	// Digits. The mechanism §4.5 names explicitly.
	score += digitScore(effQ.digits, tn.digits)

	// Local statistics.
	score += statsScore(m, opts.Now)

	if m.Deprecated() {
		score -= penaltyDeprecated
	}
	if opts.PreferFree && m.Free() {
		score += bonusFree
	}
	return score, true
}

// digitScore compares the digits of the query with the digits of the
// target. Asking for digits that are not there is the strongest negative
// signal in the whole scorer: it is the difference between "4-5" and "4-0".
func digitScore(q, t []rune) float64 {
	if len(q) == 0 {
		return 0
	}
	switch {
	case equalRunes(q, t):
		return bonusDigitsEqual
	case isSubsequence(q, t):
		return bonusDigitsSub
	default:
		return -penaltyDigitsBad
	}
}

func isSubsequence(q, t []rune) bool {
	i := 0
	for _, r := range t {
		if i < len(q) && q[i] == r {
			i++
		}
	}
	return i == len(q)
}

// statsScore is the recency and frequency bonus of §4.5, read from the
// local cache. Capped low on purpose: it breaks ties between things that
// already matched, it does not promote something the user did not type.
func statsScore(m Model, now time.Time) float64 {
	s := 0.0
	if m.UseCount > 0 {
		s += math.Min(bonusFreqMax, 0.02*math.Log2(1+float64(m.UseCount)))
	}
	if !m.LastUsed.IsZero() && !now.IsZero() {
		switch age := now.Sub(m.LastUsed); {
		case age < 0:
			// A clock that went backwards; ignore rather than reward.
		case age <= 24*time.Hour:
			s += bonusRecentDay
		case age <= 7*24*time.Hour:
			s += bonusRecentWeek
		}
	}
	return s
}

// normStr is a string reduced to what matching cares about: lowercase runes
// with the separators of §4.5 (- _ / . :) removed, plus, for each surviving
// rune, whether it started a word in the original.
type normStr struct {
	runes []rune
	start []bool

	// leafFrom is where the last '/'-delimited segment begins, so coverage
	// can be measured against the model id and not against the provider
	// prefix that every sibling shares.
	leafFrom int

	// digits are the digit runes in order, extracted once.
	digits []rune
}

func isRefSep(r rune) bool {
	switch r {
	case '-', '_', '/', '.', ':', ' ', '\t', '(', ')', '[', ']', ',':
		return true
	}
	return false
}

// normalizeRef reduces a string to its matchable form.
//
// The letter↔digit transition counts as a word start, which is what makes
// `gpt5` and `gpt-5` normalize to the same shape and lets the "5" earn the
// word-start bonus in both.
func normalizeRef(s string) normStr {
	n := normStr{
		runes: make([]rune, 0, len(s)),
		start: make([]bool, 0, len(s)),
	}
	prevSep := true
	var prev rune
	for _, r := range s {
		if isRefSep(r) {
			if r == '/' {
				n.leafFrom = len(n.runes)
			}
			prevSep = true
			continue
		}
		lr := unicode.ToLower(r)
		start := prevSep
		if !start && prev != 0 {
			if unicode.IsDigit(lr) != unicode.IsDigit(prev) {
				start = true
			} else if unicode.IsUpper(r) && unicode.IsLower(prev) {
				start = true
			}
		}
		n.runes = append(n.runes, lr)
		n.start = append(n.start, start)
		if unicode.IsDigit(lr) {
			n.digits = append(n.digits, lr)
		}
		prevSep = false
		prev = lr
	}
	return n
}

func hasPrefixRunes(s, prefix []rune) bool {
	if len(prefix) > len(s) {
		return false
	}
	for i := range prefix {
		if s[i] != prefix[i] {
			return false
		}
	}
	return true
}

// matchQuality is the base score of §4.5: a subsequence match with a gap
// penalty, a word-start bonus and a contiguity bonus, normalized per query
// rune so queries of different lengths are comparable.
//
// The dynamic program is the usual one: best[i][j] is the score of matching
// the first i+1 query runes with q[i] landing exactly on t[j]. The running
// maximum over the previous row is decayed by penaltyGap on each column,
// which is how the gap penalty is charged in O(n·m) instead of O(n·m²).
func matchQuality(q, t normStr) (float64, []int, bool) {
	n, m := len(q.runes), len(t.runes)
	if n == 0 || m == 0 || n > m {
		return 0, nil, false
	}

	neg := math.Inf(-1)
	score := make([][]float64, n)
	back := make([][]int, n)
	for i := range score {
		score[i] = make([]float64, m)
		back[i] = make([]int, m)
	}

	for j := 0; j < m; j++ {
		back[0][j] = -1
		if q.runes[0] == t.runes[j] {
			// No leading-gap penalty: every reference starts with a
			// provider prefix nobody types, and charging for it would
			// punish long provider ids for existing.
			score[0][j] = scoreMatch + wordStart(t, j)
		} else {
			score[0][j] = neg
		}
	}

	for i := 1; i < n; i++ {
		bestVal, bestIdx := neg, -1
		for j := 0; j < m; j++ {
			if j > 0 {
				if bestIdx >= 0 {
					bestVal -= penaltyGap
				}
				if score[i-1][j-1] > bestVal {
					bestVal, bestIdx = score[i-1][j-1], j-1
				}
			}
			back[i][j] = -1
			if bestIdx < 0 || q.runes[i] != t.runes[j] {
				score[i][j] = neg
				continue
			}
			s := bestVal + scoreMatch + wordStart(t, j)
			if bestIdx == j-1 {
				s += bonusContiguous
			}
			score[i][j] = s
			back[i][j] = bestIdx
		}
	}

	bestJ, bestVal := -1, neg
	for j := 0; j < m; j++ {
		if score[n-1][j] > bestVal {
			bestVal, bestJ = score[n-1][j], j
		}
	}
	if bestJ < 0 || math.IsInf(bestVal, -1) {
		return 0, nil, false
	}

	positions := make([]int, n)
	j := bestJ
	for i := n - 1; i >= 0; i-- {
		positions[i] = j
		j = back[i][j]
		if j < 0 && i > 0 {
			return 0, nil, false
		}
	}

	quality := bestVal / (float64(n) * (scoreMatch + bonusContiguous))
	return quality, positions, true
}

func wordStart(t normStr, j int) float64 {
	if t.start[j] {
		return bonusWordStart
	}
	return 0
}

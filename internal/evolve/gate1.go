// Package evolve implements docs/PLAN.md's §19.6 gate 1 — the deterministic,
// Go-only admission check a proposed tool crystallization must pass before a
// human is ever asked (gate 2) or a self-test is ever run (gate 3). Gate 1 is
// never the model's call: "ask an LLM does this deserve a tool? and it says
// yes — it is agreeable, and you just handed it something called
// tool_create." This package is exactly the code the model cannot talk its
// way past.
//
// Deliberately independent of internal/config and internal/tools, the same
// "minimal, purpose-built argument" pattern internal/tools itself already
// uses for config.Egress -> Fetch.Allow/AllowAll (see fetch.go's own doc
// comment): a caller (internal/app, Step 21's meta-tools) translates
// config.Evolve and a live *tools.Registry into this package's own
// Thresholds and []ExistingTool, rather than this package importing either.
// This also keeps gate 1 trivially unit-testable "without any model
// generating anything" — the same closing bar Step 20 held itself to.
package evolve

import (
	"fmt"
	"strings"
)

// Origin classifies how a proposal claims gate 1 should evaluate it,
// mirroring §19.6's three legitimate origins table exactly. It is the input
// Evaluate switches on — the manifest's own [origin].reason this maps onto
// is Step 21's writer's job (tool_create), not this package's.
type Origin int

const (
	// OriginAgent is the agent's own initiative: "must prove the pattern
	// exists". Every criterion below applies in full.
	OriginAgent Origin = iota
	// OriginUserDeclared is a conversational "I'm going to do this
	// regularly" — "your stated intent is the evidence" (§19.6). The
	// Repetition criterion is satisfied by the declaration itself, not by
	// Candidate.Repetitions; Stability and Profitability are skipped
	// unless the caller supplies observed-pattern data anyway (non-zero
	// VaryingArgs/ExpectedUses), since there is nothing yet to measure by
	// default. Dedup and Budget are structural facts about the system,
	// not evidence of need, and always apply regardless of origin.
	OriginUserDeclared
	// OriginUserForced is an explicit `/tools create --force`: the
	// strongest override §19.6 names. Repetition and Stability are
	// skipped unconditionally — a human typed the override on purpose.
	// Dedup and Budget still apply: they protect the prompt and the
	// catalogue's own health, not evidence of need, and overriding past a
	// near-duplicate or an already-full catalogue is a different, larger
	// decision than this package makes on a human's behalf.
	OriginUserForced
)

// Thresholds are gate 1's configurable numbers, translated by the caller
// from config.Evolve (MinRepeats, DedupThreshold) and config.Tools
// (MaxTools) — see this package's own doc comment for why it takes these
// instead of importing config directly. A zero-value field is filled from
// DefaultThresholds by normalized(), so a caller that forgets to translate
// one field gets §19.6's documented default rather than an accidentally
// permissive (DedupThreshold: 0 would flag every existing tool as a
// "duplicate") or accidentally impossible (MaxTools: 0 would block every
// proposal outright) gate.
type Thresholds struct {
	// MinRepeats is how many times an agent-initiated pattern must have
	// repeated. §19.6 default: 3.
	MinRepeats int
	// DedupThreshold is the name/description similarity above which an
	// existing tool is treated as a duplicate. §19.6 default: 0.8.
	DedupThreshold float64
	// MaxVaryingArgs is the stability ceiling: more arguments varying
	// across observations than this means "this is bash, not a tool".
	// §19.6 default: 4. Not present in config.Evolve today (no TOML knob
	// exists for it yet) — a fixed constant until a real proposal needs
	// it configurable.
	MaxVaryingArgs int
	// MaxTools is the hard cap on active tools, translated from
	// config.Tools.MaxTools. §19.6 default: 40.
	MaxTools int
}

// DefaultThresholds returns §19.6's own stated defaults verbatim, for a
// caller building Thresholds from a zero-value config.Evolve/config.Tools
// (an install that has never touched these knobs).
func DefaultThresholds() Thresholds {
	return Thresholds{
		MinRepeats:     3,
		DedupThreshold: 0.8,
		MaxVaryingArgs: 4,
		MaxTools:       40,
	}
}

// normalized fills any zero-value field in t with DefaultThresholds()'s
// corresponding value.
func (t Thresholds) normalized() Thresholds {
	d := DefaultThresholds()
	if t.MinRepeats == 0 {
		t.MinRepeats = d.MinRepeats
	}
	if t.DedupThreshold == 0 {
		t.DedupThreshold = d.DedupThreshold
	}
	if t.MaxVaryingArgs == 0 {
		t.MaxVaryingArgs = d.MaxVaryingArgs
	}
	if t.MaxTools == 0 {
		t.MaxTools = d.MaxTools
	}
	return t
}

// ExistingTool is the minimal shape gate 1's dedup/budget checks need from
// an already-registered tool — name and description only, matching what
// tools.Tool.Name()/Description() already expose. The caller builds this
// slice from a live *tools.Registry (Step 21's meta-tools) and is expected
// to have already excluded archived tools (§19.5) — this package has no way
// to know a tool's archived state on its own, and archived tools do not
// count against the budget per §19.6's own text.
type ExistingTool struct {
	Name        string
	Description string
}

// Candidate is one proposed tool crystallization — gate 1's whole input
// besides Thresholds and the existing catalogue.
type Candidate struct {
	// Name and Description are the proposed tool's own identity, checked
	// against every ExistingTool for the dedup criterion.
	Name        string
	Description string
	// Origin selects which criteria apply and how Repetition is satisfied
	// — see Origin's own doc comment.
	Origin Origin
	// Repetitions is how many times this exact pattern has been observed.
	// Only read for OriginAgent; a declared or forced proposal is not
	// required to supply it (and it is not read when it is).
	Repetitions int
	// VaryingArgs is how many call arguments differed across the observed
	// repetitions of this pattern (0 meaning "not measured" — see the
	// Stability criterion in Evaluate for how that reads per origin).
	VaryingArgs int
	// CreationCostTokens, PerUseSavingTokens and ExpectedUses feed the
	// Profitability criterion: creation only amortizes if
	// PerUseSavingTokens * ExpectedUses > CreationCostTokens. ExpectedUses
	// == 0 means "not estimated" — skipped rather than failed, since a
	// missing estimate is not evidence of unprofitability, only of not
	// having measured yet.
	CreationCostTokens int
	PerUseSavingTokens int
	ExpectedUses       int
}

// Verdict is gate 1's outcome. Allowed=false means the proposal must never
// reach gate 2 — "the agent cannot even ask" — and Reasons names every
// criterion that failed, not just the first, so a caller building a
// suggest-mode explanation (§19.7) or a `/tools create` rejection message
// can show the complete picture in one shot rather than "stopped at the
// first problem found, try again to discover the second one".
type Verdict struct {
	Allowed bool
	Reasons []string
}

func fail(v *Verdict, format string, args ...any) {
	v.Allowed = false
	v.Reasons = append(v.Reasons, fmt.Sprintf(format, args...))
}

// Evaluate runs every gate 1 criterion §19.6's table names, in table order,
// against candidate. It never returns early on the first failure — Reasons
// collects all of them, because a caller deciding whether to even surface a
// suggestion (§19.7) needs the complete picture.
func Evaluate(thresholds Thresholds, candidate Candidate, existing []ExistingTool) Verdict {
	t := thresholds.normalized()
	v := Verdict{Allowed: true}

	// Repetition: only meaningful, and only checked, for the agent's own
	// initiative. A user's declaration or an explicit force *is* the
	// evidence (§19.6's asymmetry rule), so there is nothing to count.
	if candidate.Origin == OriginAgent && candidate.Repetitions < t.MinRepeats {
		fail(&v, "repetition: pattern observed %d time(s), needs at least %d before the agent may propose it", candidate.Repetitions, t.MinRepeats)
	}

	// No duplicate: always checked, regardless of origin — a near-
	// identical existing tool is a fact about the catalogue, not about how
	// much evidence justified this particular proposal.
	if dup, sim := mostSimilar(candidate, existing); dup != "" && sim > t.DedupThreshold {
		fail(&v, "duplicate: %.0f%% similar to existing tool %q (threshold %.0f%%) -- extend it instead of creating a sibling", sim*100, dup, t.DedupThreshold*100)
	}

	// Stability: only meaningful for a pattern actually observed
	// repeating. OriginAgent always supplies it; a declared/forced
	// proposal that also supplies VaryingArgs (rare, but not forbidden)
	// is held to the same bar rather than getting a silent pass.
	if candidate.Origin != OriginUserForced && candidate.VaryingArgs > t.MaxVaryingArgs {
		fail(&v, "stability: %d arguments vary across observations, more than %d means this is bash, not a tool", candidate.VaryingArgs, t.MaxVaryingArgs)
	}

	// Budget: always checked -- the prompt cannot grow forever regardless
	// of why a tool is being proposed.
	if len(existing) >= t.MaxTools {
		fail(&v, "budget: %d active tool(s) already at the %d-tool ceiling", len(existing), t.MaxTools)
	}

	// Profitability: checked whenever an estimate was actually supplied
	// (ExpectedUses > 0), regardless of origin -- a caller that bothered
	// to estimate is asking the question, so it gets answered. A missing
	// estimate is not evidence of unprofitability, only of not having
	// measured, so it is skipped rather than failed.
	if candidate.ExpectedUses > 0 {
		saved := candidate.PerUseSavingTokens * candidate.ExpectedUses
		if saved <= candidate.CreationCostTokens {
			fail(&v, "profitability: estimated saving %d token(s) over %d expected use(s) does not exceed the %d-token creation cost", saved, candidate.ExpectedUses, candidate.CreationCostTokens)
		}
	}

	return v
}

// mostSimilar returns the existing tool most similar to candidate (by
// combinedSimilarity) and its score, or ("", 0) when existing is empty.
func mostSimilar(candidate Candidate, existing []ExistingTool) (string, float64) {
	var bestName string
	var bestScore float64
	for _, e := range existing {
		s := combinedSimilarity(candidate.Name, candidate.Description, e.Name, e.Description)
		if s > bestScore {
			bestScore, bestName = s, e.Name
		}
	}
	return bestName, bestScore
}

// combinedSimilarity is gate 1's dedup metric: the higher of a name-only
// and a description-only word-overlap (Jaccard) score. Word overlap, not
// character-edit-distance, is deliberate: two descriptions that say the
// same thing in different words ("show git status, abbreviated" vs
// "abbreviated git status output") should score as similar, which an
// edit-distance metric would badly undercount. This is a deliberately
// simple, explainable metric, not a claim that it is optimal — refining it
// as real proposals expose its blind spots is exactly the kind of
// follow-up work §19.6's own text invites for this kind of illustrative,
// not exhaustive, mechanism (the same reasoning declarative.go's
// financeHosts list already documents for a different one).
func combinedSimilarity(nameA, descA, nameB, descB string) float64 {
	nameSim := jaccard(words(nameA), words(nameB))
	descSim := jaccard(words(descA), words(descB))
	if nameSim > descSim {
		return nameSim
	}
	return descSim
}

// words lowercases s and splits it into a deduplicated set of alphanumeric
// tokens, breaking on underscores, hyphens, punctuation and whitespace
// alike -- so "git_status_short" and "git status, short form" both yield a
// set containing {git, status, short} (the second also containing "form").
func words(s string) map[string]struct{} {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
	}
	set := make(map[string]struct{})
	for _, w := range strings.Fields(b.String()) {
		set[w] = struct{}{}
	}
	return set
}

// jaccard is |intersection| / |union| of two word sets, 1.0 for two empty
// sets (nothing to disagree about) rather than the more usual
// division-by-zero, so an empty name or description cannot poison
// combinedSimilarity's max() with a spurious value.
func jaccard(a, b map[string]struct{}) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1
	}
	inter := 0
	for w := range a {
		if _, ok := b[w]; ok {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 1
	}
	return float64(inter) / float64(union)
}

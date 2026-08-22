package catalog

import (
	"path"
	"strings"
)

// curate.go is Layer 1 of docs/DESIGN-model-curation.md: rules that are
// right for everybody, on by default, with zero configuration typed by
// the user. It is a pure function over an already-built Catalog — no
// clock, no network, no filesystem — which is what makes it table-testable
// (principle 5/6, §2 of that design) and safe to call from internal/app on
// every LoadCatalog/RefreshCatalog without adding a new failure mode.
//
// Layer 2 (the interactive picker hide/keep, curation.json) and Layer 3
// (the /config screen) are separate, later pieces of the same design and
// are not implemented in this file.

// Rules is the curation policy: which models are worth showing. The zero
// value shows everything, so a caller that does not care (or has not
// wired [catalog.curate] yet) is never surprised by models disappearing.
type Rules struct {
	// ChatOnly drops models that cannot answer a chat turn at all (§1.2's
	// three-signal disjunction: non-text output modality, a degenerate
	// output limit, or an explicitly non-sampled model with no tools and
	// no structured output).
	ChatOnly bool

	// HideDeprecated moved here from BuildInput (docs/DESIGN-model-curation.md
	// §1.1/§3, Layer 0): models.dev's own "status" field, now parsed by
	// applyModelsDev, is what makes this flag capable of finding anything
	// to hide. BuildInput.HideDeprecated stays wired for one release as a
	// compatibility alias — see Build's own call site.
	HideDeprecated bool

	// HideSuperseded hides "X-preview" only when the GA id "X" also
	// exists in the SAME provider (§1.3: 22 of Google's 41 ids contain
	// "preview", and hiding all of them by name shape would hide the
	// best model Google offers — this has to be relational).
	HideSuperseded bool

	// HideDatedTwins hides "X-<date>" only when the undated id "X" also
	// exists in the same provider, reusing NormalizeID's own date-suffix
	// stripping so the two rules never disagree about what counts as a
	// "twin".
	HideDatedTwins bool

	// HideLatest hides "X-latest" aliases. Off by default: a moving
	// target that never needs a config edit is a legitimate, deliberate
	// choice for some users, not noise (§3.1.2).
	HideLatest bool

	// Hide is the user's own glob list (path.Match syntax against the
	// model's WireID), merged with any per-provider Hide the caller
	// passes via ProviderRules.
	Hide []string

	// Keep wins over every rule above, including ChatOnly and
	// HideDeprecated — "show me this one" is a more specific instruction
	// than "hide that class of thing" (§2.2).
	Keep []string

	// Providers holds per-provider hide/keep glob lists (§1.3's
	// "per-provider policy the report asked for"), keyed by provider id.
	// Provider lists MERGE with the global Hide/Keep above; they do not
	// replace them.
	Providers map[string]ProviderRules

	// KeepRefs and HideRefs are Layer 2's own contribution (design doc
	// §2.2/§2.3): exact, fully-qualified refs ("provider/wire_id") from
	// the user's curation.json, one keystroke at a time from the picker,
	// rather than the glob-against-WireID lists above (which come from
	// config.toml and are typed by hand). They are refs, not globs, for
	// a reason: a ctrl+x press knows exactly which model it is hiding —
	// turning that into a glob pattern would risk silently catching a
	// sibling model the user never looked at, and design doc §2.2's own
	// worked JSON example stores plain refs, not patterns.
	//
	// Precedence (design doc §2.2, weakest to strongest): built-in
	// defaults < [catalog.curate] < [[provider]] hide/keep <
	// curation.json — "what you just pressed always wins". KeepRefs is
	// therefore checked first, before even r.Keep, and HideRefs is
	// checked LAST, after every automatic rule and after the glob-based
	// Hide/Providers checks — a curation.json hide must be able to
	// remove a model none of the automatic rules would have caught
	// (principle 9's "ranking beats filtering" is not in tension here:
	// this is the one layer where a human, not a heuristic, is deciding).
	KeepRefs []string
	HideRefs []string
}

// ProviderRules is one provider's own hide/keep glob lists, additive with
// Rules.Hide/Rules.Keep (§1.3: "provider rules merge with the global
// ones... no override semantics").
type ProviderRules struct {
	Hide []string
	Keep []string
}

// Reason is why one model was dropped — carried so the interface can
// explain itself later (Layer 2's undo notice, `models --why`).
type Reason string

const (
	ReasonNonChatModality Reason = "no text output"      // §1.2 signal 1
	ReasonNonChatLimit    Reason = "output limit 1"      // §1.2 signal 2
	ReasonNonChatSampling Reason = "not a sampled model" // §1.2 signal 3
	ReasonDeprecated      Reason = "deprecated"
	ReasonSuperseded      Reason = "superseded"
	ReasonDatedTwin       Reason = "dated snapshot"
	ReasonLatestAlias     Reason = "latest alias"
	ReasonUserGlob        Reason = "hidden by you"
	ReasonUnhealthy       Reason = "failing"
)

// Hidden is one model Curate removed from the kept catalog, and why.
type Hidden struct {
	Model  Model
	Reason Reason
}

// Curate partitions a snapshot according to r. It never mutates cat (the
// Catalog it returns is a new value built from a filtered copy of
// cat.Models) and never deletes from the cache: hidden is the complete
// audit trail, and every model curate removes from kept is still exactly
// the model it was, just not returned in kept.
//
// Two carve-outs apply to every rule, not just HideDeprecated (principle
// 3): a model with UseCount > 0, or one that came from the user's own
// config (Source.Has(SourceConfig)), is never hidden by an automatic
// rule. An explicit Keep glob overrides everything, including those
// carve-outs being unnecessary in the first place.
func Curate(cat Catalog, r Rules) (kept Catalog, hidden []Hidden) {
	kept = cat
	kept.Models = nil
	kept.index = nil

	if len(cat.Models) == 0 {
		return kept, nil
	}

	// Precompute, per provider, the set of normalized ids present — the
	// relational rules (superseded, dated twins) need to know what else
	// exists in the SAME provider before they can decide anything (§1.3:
	// a global rule cannot be tuned for all three providers' duplicate
	// shapes at once).
	idsByProvider := make(map[string]map[string]bool, 8)
	for _, m := range cat.Models {
		set := idsByProvider[m.Provider]
		if set == nil {
			set = map[string]bool{}
			idsByProvider[m.Provider] = set
		}
		set[strings.ToLower(m.WireID)] = true
	}

	for _, m := range cat.Models {
		reason, drop := decide(m, r, idsByProvider[m.Provider])
		if drop {
			hidden = append(hidden, Hidden{Model: m, Reason: reason})
			continue
		}
		kept.Models = append(kept.Models, m)
	}

	kept.ensureIndex()
	return kept, hidden
}

// decide is Curate's per-model policy, split out so its early-return
// shape (never-hide carve-outs first, then each rule in report order) is
// easy to read against the design doc's own §1.4 breakdown table.
func decide(m Model, r Rules, providerIDs map[string]bool) (Reason, bool) {
	// curation.json's KeepRefs wins over absolutely everything, including
	// the config.toml-level Keep below — it is the strongest layer in
	// design doc §2.2's own precedence table ("what you just pressed
	// always wins"), checked by exact ref rather than a WireID glob.
	if refMatchAny(m.Ref, r.KeepRefs) {
		return "", false
	}

	// Keep always wins, and is checked before anything else — including
	// the "never hide what the user used" carve-out, which would
	// otherwise make Keep redundant for exactly the models a user is
	// most likely to explicitly ask for.
	if globMatchAny(m.WireID, r.Keep) || globMatchAny(m.WireID, r.Providers[m.Provider].Keep) {
		return "", false
	}

	// curation.json's HideRefs is an explicit, one-model-at-a-time human
	// decision (a ctrl+x press, or /model hide) — the opposite case from
	// the automatic rules below, which are heuristics guessing at intent.
	// It is therefore checked NOT gated by usedOrDeclared: principle 3's
	// "never hide what the user actually used" carve-out exists to keep
	// an automatic rule from silently surprising someone, but a model the
	// user just told the picker to hide is not a surprise — it is the
	// most specific instruction this whole system can receive short of
	// Keep itself, and reports the same "hidden by you" reason the
	// glob-based Hide list already uses (they are the same kind of fact:
	// a human, not a heuristic, made this call).
	if refMatchAny(m.Ref, r.HideRefs) {
		return ReasonUserGlob, true
	}

	// Principle 3: never hide what the user has actually used, or what
	// they declared by hand in config.toml. This carve-out applies to
	// every automatic rule below, not just deprecation.
	usedOrDeclared := m.UseCount > 0 || m.Source.Has(SourceConfig)

	if r.HideDeprecated && m.Deprecated() && !usedOrDeclared {
		return ReasonDeprecated, true
	}

	if r.ChatOnly && !usedOrDeclared {
		if reason, drop := nonChat(m); drop {
			return reason, true
		}
	}

	if r.HideSuperseded && !usedOrDeclared && isSuperseded(m, providerIDs) {
		return ReasonSuperseded, true
	}

	if r.HideDatedTwins && !usedOrDeclared && isDatedTwin(m, providerIDs) {
		return ReasonDatedTwin, true
	}

	if r.HideLatest && !usedOrDeclared && isLatestAlias(m) {
		return ReasonLatestAlias, true
	}

	if !usedOrDeclared && (globMatchAny(m.WireID, r.Hide) || globMatchAny(m.WireID, r.Providers[m.Provider].Hide)) {
		return ReasonUserGlob, true
	}

	return "", false
}

// nonChat is §1.2's disjunction of three cheap, independently-justifiable
// signals, each reporting its own reason. Measured against the real
// models.dev payload (§1.2), no single signal is precise or complete
// enough alone; this is why it is three checks and not one.
func nonChat(m Model) (Reason, bool) {
	// 1. Declared OUTPUT modalities that do not include text at all.
	//    Empty modalities = unknown = keep (principle 10). This is the
	//    output side specifically: a model that accepts image/audio and
	//    answers in text is a chat model regardless of what it accepts.
	if len(m.Modalities) > 0 && !containsFold(m.Modalities, "text") {
		return ReasonNonChatModality, true
	}

	// 2. An output limit of exactly 1 token cannot carry a turn. Zero is
	//    NOT evidence: some gateways report a missing limit as 0, so 0
	//    means unknown, not degenerate (§1.2).
	if m.MaxOutput == 1 {
		return ReasonNonChatLimit, true
	}

	// 3. No sampling, no tools, no structured output: a scorer, not a
	//    generator. Requires Temperature to be explicitly false, never
	//    merely absent (principle 10 again).
	if m.Temperature != nil && !*m.Temperature && !m.Caps.Tools && !m.Caps.JSONSchema {
		return ReasonNonChatSampling, true
	}

	return "", false
}

// isSuperseded reports whether m's WireID looks like a "-preview" (or
// "-exp"/"-experimental") twin of a GA id that also exists in the same
// provider. It is deliberately relational, not name-shape alone (§1.3:
// 22 of Google's 41 ids contain "preview" and only 3 are actually
// redundant) — a preview id with no GA counterpart is kept.
func isSuperseded(m Model, providerIDs map[string]bool) bool {
	base, matched := stripSupersededSuffix(m.WireID)
	if !matched {
		return false
	}
	return providerIDs[base]
}

// supersededSuffixes are the name-shape hints that a model MIGHT be a
// preview/experimental twin — never sufficient alone (see isSuperseded's
// own doc comment), only what decides which base id to look for.
var supersededSuffixes = []string{"-preview", "-experimental", "-exp"}

func stripSupersededSuffix(wireID string) (base string, matched bool) {
	lower := strings.ToLower(wireID)
	for _, suf := range supersededSuffixes {
		if strings.HasSuffix(lower, suf) {
			return lower[:len(lower)-len(suf)], true
		}
	}
	return "", false
}

// isDatedTwin reports whether m's WireID is a dated snapshot ("X-20250219")
// of an undated id "X" that also exists in the same provider. Reuses
// NormalizeID's own date-suffix stripping (dateSuffix, modelsdev.go) so
// the two rules never disagree about what counts as a date stamp.
func isDatedTwin(m Model, providerIDs map[string]bool) bool {
	lower := strings.ToLower(m.WireID)
	stripped := dateSuffix.ReplaceAllString(lower, "")
	if stripped == lower {
		return false // no date suffix was actually present
	}
	return providerIDs[stripped]
}

// isLatestAlias reports whether m's WireID is a "-latest"/":latest"/
// "@latest" alias. Off by default (Rules.HideLatest) — see that field's
// own doc comment for why this one is a deliberate, offered choice and
// not a default.
func isLatestAlias(m Model) bool {
	lower := strings.ToLower(m.WireID)
	return strings.HasSuffix(lower, "-latest") ||
		strings.HasSuffix(lower, ":latest") ||
		strings.HasSuffix(lower, "@latest")
}

// containsFold reports whether ss contains s, case-insensitively.
func containsFold(ss []string, s string) bool {
	for _, v := range ss {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}

// globMatchAny reports whether id matches any of patterns, using
// path.Match's shell-glob syntax (the same syntax internal/permissions'
// own matches() documents to users elsewhere in this program, so a hide
// glob and a permissions glob read the same way). A malformed pattern
// (path.ErrBadPattern) is treated as a non-match rather than propagated
// as an error: a typo in a hide list must not turn into a startup
// failure, the same "never abort on bad input" discipline Build's own
// per-record skip already follows.
func globMatchAny(id string, patterns []string) bool {
	if id == "" || len(patterns) == 0 {
		return false
	}
	lower := strings.ToLower(id)
	for _, p := range patterns {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		if ok, err := path.Match(p, lower); err == nil && ok {
			return true
		}
	}
	return false
}

// refMatchAny reports whether ref exactly (case-insensitively) equals any
// of refs — KeepRefs/HideRefs' own matching, deliberately NOT glob syntax
// (see those fields' own doc comment): curation.json stores the exact
// fully-qualified ref a picker key press resolved against, never a
// pattern, so exact comparison is both simpler and more precise than
// reusing globMatchAny would be for this case.
func refMatchAny(ref string, refs []string) bool {
	if ref == "" || len(refs) == 0 {
		return false
	}
	lower := strings.ToLower(ref)
	for _, r := range refs {
		if strings.ToLower(strings.TrimSpace(r)) == lower {
			return true
		}
	}
	return false
}

// curation.go implements the picker half of docs/DESIGN-model-curation.md's
// Layer 2 (F5): ctrl+x hides the model under the cursor, ctrl+h toggles
// whether hidden rows are shown at all (dimmed, tagged with why), and
// ctrl+x on an already-hidden row un-hides it — the "escape hatch that
// makes pressing it a decision you cannot regret" (design doc §2, Layer 2).
//
// CurationStore is the persistence seam, drawn exactly like TrustStore
// (trust.go) and ThemeStore (theme.go): this package never touches a file,
// it calls back through the interface internal/app implements over
// internal/curation.Store/Load/Save. Unlike those two write-only stores,
// CurationStore also has to answer "is this ref hidden right now" — the
// picker's rebuild() needs that on every keystroke, not just at the moment
// a key is pressed — so its shape is Hide/Unhide/IsHidden/Reason rather
// than a single Save(value).
//
// Keep/Hidden/Reset were added alongside /model hide|keep and /models
// hidden|reset (design doc §2.1, the slash-command half of Layer 2 that
// PR #210's picker-only slice deliberately deferred): Keep is /model
// keep's own pin against every automatic rule, stronger than Unhide
// (which only undoes a ctrl+x/hide, leaving the model still subject to
// [catalog.curate]'s heuristics); Hidden enumerates every ref the user has
// hidden, which /models hidden needs and IsHidden alone cannot answer,
// since Root.cat has already had those refs curated OUT by the time a
// running session sees it (internal/app's curationRules/applyCuration);
// Reset is /models reset's own bulk "drop every user hide, keep the
// automatic rules" — it touches only what this store itself tracks, never
// config.toml.
package tui

import (
	"strings"

	"github.com/MichiTrader/ishakat/internal/catalog"
)

// CurationStore is Layer 2's own persistence seam. nil is a supported
// value, the same "session behaves correctly, just does not survive a
// restart" contract TrustStore/ThemeStore already give their own nil case:
// a nil CurationStore still lets ctrl+x hide a row for the rest of this
// session (Picker keeps its own in-memory copy — see Picker.hidden), it
// just never gets asked to persist it. Every slash-command runner that
// reads m.curationStore (slashrun.go's runModelHide/runModelKeep,
// models.go's runModelsHidden/runModelsReset) reports a plain notice
// instead of acting when it is nil, mirroring that same degradation.
type CurationStore interface {
	// IsHidden reports whether ref is in the user's own hide list.
	IsHidden(ref string) bool
	// Hide adds ref to the hide list (moving it out of Kept first).
	Hide(ref string) error
	// Unhide removes ref from the hide list without adding it to Kept —
	// ctrl+x's own toggle-off, not the same thing as Keep (which also
	// pins the model against every automatic rule; that half of Layer 2
	// is /model keep's job, not the picker's ctrl+x).
	Unhide(ref string) error
	// Reason explains why ref is hidden ("hidden by you" for anything
	// reached through this store — the automatic-rule reasons like
	// "deprecated"/"superseded" belong to catalog.Curate, upstream of
	// the picker ever seeing the model at all, so a ref this store
	// tracks is definitionally a human decision, never a heuristic).
	Reason(ref string) string
	// Keep pins ref against every automatic rule — [catalog.curate],
	// per-provider hide, every heuristic in internal/catalog.Curate —
	// not just this store's own hide list the way Unhide does. This is
	// /model keep's own verb (design doc §2.1): the common case is
	// keeping a model [catalog.curate] itself would otherwise hide,
	// which Unhide cannot do since that model was never in this store's
	// hide list to begin with.
	Keep(ref string) error
	// Hidden lists every ref currently in the user's own hide list,
	// sorted by ref. /models hidden's own data source: Root.cat cannot
	// answer this, because internal/app's curationRules/applyCuration
	// already removed every one of these refs from the catalog snapshot
	// before Root ever saw it (the same constraint pickerRow.hidden's
	// own doc comment documents for ctrl+h's "only this session's own
	// hides are revealable" limitation).
	Hidden() []string
	// Reset drops every ref this store's Hidden lists — /models reset's
	// own definition, "drop every user hide, keep the automatic rules"
	// (design doc §2.1): it never touches Kept (a Keep is a deliberate,
	// positive pin, not something a bulk reset should undo) and never
	// touches config.toml's own [catalog.curate]/[[provider]] rules,
	// which live in a different file this package cannot write to (§6.1).
	Reset() error
}

// curationHideReason is CurationStore.Reason's own answer for every ref it
// tracks — mirrors catalog.ReasonUserGlob's wording exactly ("hidden by
// you") so a dimmed row reads the same regardless of whether the hide came
// from a config.toml glob or a ctrl+x press.
const curationHideReason = "hidden by you"

// hiddenByRef looks ref up in m.hidden (internal/app's applyCuration audit
// trail, Options.Hidden's own doc comment) by exact reference,
// case-insensitively — the same comparison catalog.Catalog.Get itself uses,
// so this never disagrees with m.cat about which ref is which. This is
// design doc §2.3's second closing criterion's actual data source for
// automatic-rule hides (ChatOnly, HideDeprecated, HideSuperseded, ...): the
// ones CurationStore.Reason cannot explain, because that store only ever
// tracks curation.json's own user-driven entries (see its own doc comment).
func (m Root) hiddenByRef(ref string) (catalog.Hidden, bool) {
	ref = strings.TrimSpace(ref)
	for _, h := range m.hidden {
		if strings.EqualFold(h.Model.Ref, ref) {
			return h, true
		}
	}
	return catalog.Hidden{}, false
}

// hiddenRuleLabel names the [catalog.curate] setting (or equivalent) behind
// reason, for the one-line "hidden by ..." chat notice runModelCommand's
// hidden-fallback produces. Deliberately a smaller cousin of
// internal/app/models_cmd.go's whyReasonText (which this package cannot
// import, §6.1) rather than a shared helper: `ishakat models --why`'s
// multi-line diagnostic and this package's single notice line have
// different enough shapes that a shared function would need its own
// "which format" branch, and the wording only has to agree in substance,
// not byte-for-byte — both ultimately name the same catalog.Reason.
func hiddenRuleLabel(reason catalog.Reason) string {
	switch reason {
	case catalog.ReasonNonChatModality, catalog.ReasonNonChatLimit, catalog.ReasonNonChatSampling:
		return "catalog.curate.chat_only"
	case catalog.ReasonDeprecated:
		return "catalog.hide_deprecated"
	case catalog.ReasonSuperseded:
		return "catalog.curate.hide_superseded"
	case catalog.ReasonDatedTwin:
		return "catalog.curate.hide_dated_twins"
	case catalog.ReasonLatestAlias:
		return "catalog.curate.hide_latest"
	case catalog.ReasonUserGlob:
		return "your hide list"
	case catalog.ReasonUnhealthy:
		return "health check"
	default:
		return string(reason)
	}
}

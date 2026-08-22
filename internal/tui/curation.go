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
// than a single Save(value). A future Keep method (for /model keep, out of
// scope for this picker-only slice) would belong on this same interface.
package tui

// CurationStore is Layer 2's own persistence seam. nil is a supported
// value, the same "session behaves correctly, just does not survive a
// restart" contract TrustStore/ThemeStore already give their own nil case:
// a nil CurationStore still lets ctrl+x hide a row for the rest of this
// session (Picker keeps its own in-memory copy — see Picker.hidden), it
// just never gets asked to persist it.
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
}

// curationHideReason is CurationStore.Reason's own answer for every ref it
// tracks — mirrors catalog.ReasonUserGlob's wording exactly ("hidden by
// you") so a dimmed row reads the same regardless of whether the hide came
// from a config.toml glob or a ctrl+x press.
const curationHideReason = "hidden by you"

// picker.go implements the §9.4 model selector (Step 10): an overlay drawn
// in place of the normal chat screen, grouped by provider, searched
// incrementally with the same scorer §4.5's /model command uses, and never
// touching the network — it is handed an immutable *catalog.Catalog snapshot
// and reads nothing else.
//
// Picker is a value type, like every other component in this package
// (Root itself included, see chat.go's liveTurn comment for why that rule
// exists): every method below takes a Picker and returns the next one.
// collapsed is a map, which is a reference type — copying the header that
// points at it is fine, since a new Picker replaces the whole map (via
// collapseCurrent) rather than aliasing one across two independent picker
// sessions.
package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/catalog"
	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/theme"
)

// pickerFilter is what ctrl+f cycles through (§9.4: "ctrl+f cicla filtros:
// todos → gratis → con herramientas → con visión → favoritos").
type pickerFilter int

const (
	pickerAll pickerFilter = iota
	pickerFree
	pickerTools
	pickerVision
	pickerFavorites
)

// next wraps back to pickerAll after pickerFavorites.
func (f pickerFilter) next() pickerFilter { return (f + 1) % (pickerFavorites + 1) }

// label is what the footer hint line names the current filter.
func (f pickerFilter) label() string {
	switch f {
	case pickerFree:
		return "free"
	case pickerTools:
		return "tools"
	case pickerVision:
		return "vision"
	case pickerFavorites:
		return "favorites"
	default:
		return "all"
	}
}

// pickerRow is one row of the flattened, filtered, grouped list Update and
// View actually walk. A header row carries no candidate; a model row always
// belongs to the header printed above it in the same provider group.
//
// Headers are real, selectable rows rather than pure decoration on purpose:
// it is what lets left/right (and, on a header, enter) collapse or expand a
// group without a second selection mechanism the rest of the picker would
// have to keep in sync. §9.4 only asks for "colapsables con ←/→"; making the
// header navigable is what makes that reversible once a group is collapsed
// and none of its models are on screen to point at anymore.
type pickerRow struct {
	provider  string
	header    bool
	collapsed bool // header only
	count     int  // header only: how many models this group has, collapsed or not
	cand      catalog.Candidate

	// hidden/hiddenReason are Layer 2's own addition (F5,
	// docs/DESIGN-model-curation.md's "interactive hide/keep"): true when
	// Picker.curationStore.IsHidden reports this ref hidden. A hidden row
	// only ever appears in p.rows at all when Picker.showHidden is true
	// (ctrl+h); with it false, buildPickerRows drops the row entirely —
	// which is what makes ctrl+x on a row still on screen mean "hide it"
	// (never a redundant re-hide) and keeps the footer's "N hidden" count
	// meaning "not currently drawn", the same thing it says.
	//
	// NOTE, a real limitation of this slice: only a ref hidden through
	// THIS picker's own curationStore is ever visible here to un-hide,
	// because a ref already in curation.json's HideRefs before this
	// process started never reaches p.cat at all — internal/app's
	// curationRules folds it into catalog.Rules.HideRefs and catalog.Curate
	// removes it upstream of LoadCatalog/RefreshCatalog ever handing Root a
	// snapshot (docs/PLAN.md's part-8 entry). Revealing THOSE rows too
	// needs the picker to see the pre-curation model list, which is a
	// separate, larger change left for a follow-up slice; ctrl+h's "show
	// hidden" is fully correct for anything hidden after the picker opened.
	hidden       bool
	hiddenReason string
}

// Picker is the §9.4 overlay's state.
type Picker struct {
	cat  *catalog.Catalog
	opts catalog.ResolveOptions

	// active is the model reference currently in use, drawn with the "●"
	// marker (§9.4: "El activo lleva ● en vez de ▸").
	active string
	// favorites is [favorites].list from the configuration, keyed
	// lowercase for a case-insensitive membership test.
	favorites map[string]bool

	query     string
	filter    pickerFilter
	collapsed map[string]bool

	rows []pickerRow
	sel  int

	// curationStore is Layer 2's persistence seam (curation.go), nil for
	// every pre-F5 caller (every test in this package that does not set
	// it explicitly) — see CurationStore's own doc comment for the
	// "session still works, just does not persist" degradation that nil
	// gives.
	curationStore CurationStore
	// showHidden is ctrl+h's own toggle: "all filtering axes stay in
	// effect, but also show me what a hide rule or a ctrl+x removed"
	// (design doc §2's own "hidden-visibility is an orthogonal axis"
	// reasoning for why this is not folded into pickerFilter's cycle).
	showHidden bool
}

// newPicker builds the picker's initial state, seeded with query — the text
// already typed after "/model", or "" for ctrl+p and a bare "/model". store
// is Layer 2's own CurationStore (nil for every caller that predates F5, or
// that simply has none configured — see CurationStore's own doc comment).
func newPicker(cat *catalog.Catalog, opts catalog.ResolveOptions, favorites []string, active, query string, store CurationStore) Picker {
	p := Picker{
		cat:           cat,
		opts:          opts,
		active:        active,
		favorites:     favoriteSet(favorites),
		query:         query,
		collapsed:     map[string]bool{},
		curationStore: store,
	}
	return p.rebuild()
}

func favoriteSet(list []string) map[string]bool {
	out := make(map[string]bool, len(list))
	for _, ref := range list {
		out[strings.ToLower(strings.TrimSpace(ref))] = true
	}
	return out
}

// Active reports whether there is anything to draw. A Picker built without a
// catalog (nil, or the zero value Root starts with before any /model or
// ctrl+p) never becomes the active mode's content, but callers that would
// otherwise have to check m.mode a second time can ask this instead.
func (p Picker) Active() bool { return p.cat != nil }

// rebuild recomputes rows from query/filter/collapsed. It is the single
// place that talks to the catalog, called after every edit to any of those
// three — never incrementally, because a catalog of a few hundred models is
// cheap to re-score entirely and "cheap but wrong on some edits" is a worse
// trade than "always right".
func (p Picker) rebuild() Picker {
	if p.cat == nil {
		p.rows = nil
		p.sel = 0
		return p
	}
	cands := p.cat.Filter(p.query, p.opts)
	cands = filterCandidates(cands, p.filter, p.favorites)
	cands, hiddenRefs := p.splitCurationHidden(cands)
	p.rows = buildPickerRows(groupCandidates(cands), p.collapsed, hiddenRefs)
	if p.sel >= len(p.rows) {
		p.sel = len(p.rows) - 1
	}
	if p.sel < 0 {
		p.sel = 0
	}
	return p
}

// filterCandidates narrows cands to what the current pickerFilter allows.
// pickerAll is the zero value and keeps everything, which is why ctrl+f
// starting there means "no filter" rather than an arbitrary first choice.
func filterCandidates(cands []catalog.Candidate, f pickerFilter, favorites map[string]bool) []catalog.Candidate {
	if f == pickerAll {
		return cands
	}
	out := make([]catalog.Candidate, 0, len(cands))
	for _, c := range cands {
		switch f {
		case pickerFree:
			if !c.Model.Free() {
				continue
			}
		case pickerTools:
			if !c.Model.Caps.Tools {
				continue
			}
		case pickerVision:
			if !c.Model.Caps.Vision {
				continue
			}
		case pickerFavorites:
			if !favorites[strings.ToLower(c.Model.Ref)] {
				continue
			}
		}
		out = append(out, c)
	}
	return out
}

// pickerGroup is candidates sharing a provider, in the rank order Filter
// already produced.
type pickerGroup struct {
	provider string
	models   []catalog.Candidate
}

// groupCandidates buckets cands by provider, preserving first-appearance
// order — which is also rank order, since cands arrives already sorted best
// first. A picker that grouped by, say, alphabetical provider name would
// disagree with `/model son45` about which provider is the likely answer;
// grouping by first appearance keeps the two reading the same story.
func groupCandidates(cands []catalog.Candidate) []pickerGroup {
	idx := map[string]int{}
	var groups []pickerGroup
	for _, c := range cands {
		p := c.Model.Provider
		i, ok := idx[p]
		if !ok {
			i = len(groups)
			idx[p] = i
			groups = append(groups, pickerGroup{provider: p})
		}
		groups[i].models = append(groups[i].models, c)
	}
	return groups
}

// splitCurationHidden separates cands into what still gets drawn and, when
// p.showHidden is set, which refs among them are hidden (so
// buildPickerRows can tag those rows dimmed with their reason). With a nil
// curationStore or showHidden false, curation-hidden refs are dropped from
// cands outright — the ordinary, common case, and the reason ctrl+x's own
// hide takes effect on the very next rebuild with no other code path
// needing to know about it.
func (p Picker) splitCurationHidden(cands []catalog.Candidate) ([]catalog.Candidate, map[string]string) {
	if p.curationStore == nil {
		return cands, nil
	}
	out := make([]catalog.Candidate, 0, len(cands))
	var hiddenRefs map[string]string
	for _, c := range cands {
		if !p.curationStore.IsHidden(c.Model.Ref) {
			out = append(out, c)
			continue
		}
		if !p.showHidden {
			continue
		}
		if hiddenRefs == nil {
			hiddenRefs = map[string]string{}
		}
		hiddenRefs[strings.ToLower(c.Model.Ref)] = p.curationStore.Reason(c.Model.Ref)
		out = append(out, c)
	}
	return out, hiddenRefs
}

// countCurationHidden reports how many of p.rows' underlying candidates the
// curationStore currently hides — the footer's own "+N hidden" count
// (design doc §2's wireframe). It re-derives the count from p.cat.Filter
// rather than counting p.rows directly, since with showHidden false those
// rows were never built at all (splitCurationHidden already dropped them).
func (p Picker) countCurationHidden() int {
	if p.curationStore == nil || p.cat == nil {
		return 0
	}
	cands := p.cat.Filter(p.query, p.opts)
	cands = filterCandidates(cands, p.filter, p.favorites)
	n := 0
	for _, c := range cands {
		if p.curationStore.IsHidden(c.Model.Ref) {
			n++
		}
	}
	return n
}

// buildPickerRows flattens groups into the row list Update/View walk. A
// collapsed group still contributes its header — with the count of models
// it is hiding — so it stays reachable to expand again. hiddenRefs (keyed
// lowercase, from splitCurationHidden) tags a row hidden/dimmed when
// ctrl+h's "show hidden" is on; it is nil the rest of the time, in which
// case every row in groups is already something the user can see normally.
func buildPickerRows(groups []pickerGroup, collapsed map[string]bool, hiddenRefs map[string]string) []pickerRow {
	var rows []pickerRow
	for _, g := range groups {
		coll := collapsed[g.provider]
		rows = append(rows, pickerRow{provider: g.provider, header: true, collapsed: coll, count: len(g.models)})
		if coll {
			continue
		}
		for _, c := range g.models {
			reason, hidden := hiddenRefs[strings.ToLower(c.Model.Ref)]
			rows = append(rows, pickerRow{provider: g.provider, cand: c, hidden: hidden, hiddenReason: reason})
		}
	}
	return rows
}

// typeText appends s (one key press's worth of text — almost always one
// rune) to the query and re-scores.
func (p Picker) typeText(s string) Picker {
	if s == "" {
		return p
	}
	p.query += s
	p.sel = 0
	return p.rebuild()
}

// backspace removes the last rune of the query, rune-safe so an accented
// character disappears as one keystroke's worth of undo, not a mangled
// trailing byte.
func (p Picker) backspace() Picker {
	r := []rune(p.query)
	if len(r) == 0 {
		return p
	}
	p.query = string(r[:len(r)-1])
	p.sel = 0
	return p.rebuild()
}

// cycleFilter advances to the next pickerFilter (ctrl+f).
func (p Picker) cycleFilter() Picker {
	p.filter = p.filter.next()
	p.sel = 0
	return p.rebuild()
}

// moveSel moves the selection by delta rows, wrapping like the slash-command
// dropdown's own moveUp/moveDown.
func (p Picker) moveSel(delta int) Picker {
	if len(p.rows) == 0 {
		return p
	}
	n := len(p.rows)
	p.sel = ((p.sel+delta)%n + n) % n
	return p
}

// selectedProvider is the provider of the row currently under the cursor, or
// "" when there is nothing to select — the guard collapseCurrent and enter
// both need before touching p.rows[p.sel].
func (p Picker) selectedProvider() (string, bool) {
	if len(p.rows) == 0 {
		return "", false
	}
	return p.rows[p.sel].provider, true
}

// collapseCurrent sets the collapsed state of the selected row's provider
// group (left = true, right = false) and keeps the selection on that
// group's header afterwards — the group may have just lost every model row
// it had, and a selection left pointing past the end of a shorter list would
// silently jump to an unrelated provider.
func (p Picker) collapseCurrent(collapse bool) Picker {
	provider, ok := p.selectedProvider()
	if !ok {
		return p
	}
	next := make(map[string]bool, len(p.collapsed)+1)
	for k, v := range p.collapsed {
		next[k] = v
	}
	next[provider] = collapse
	p.collapsed = next
	p = p.rebuild()
	for i, r := range p.rows {
		if r.header && r.provider == provider {
			p.sel = i
			break
		}
	}
	return p
}

// toggleCurrent flips the selected row's provider between collapsed and
// expanded — what enter on a header row means, as opposed to enter on a
// model row (choosing it).
func (p Picker) toggleCurrent() Picker {
	provider, ok := p.selectedProvider()
	if !ok {
		return p
	}
	return p.collapseCurrent(!p.collapsed[provider])
}

// selected is the row under the cursor. Callers must check len(rows) > 0
// first, same contract as slashMenu.Selected — an empty picker has nothing
// to point at, and guessing would hide the caller's own bug.
func (p Picker) selected() pickerRow { return p.rows[p.sel] }

// toggleShowHidden is ctrl+h: flip whether curation-hidden rows are drawn
// (dimmed, tagged with their reason) and rebuild. The selection resets to
// 0 rather than trying to preserve "same row" across a rebuild that can
// insert or remove rows anywhere in the list — the same reset typeText and
// cycleFilter already do on any edit that reshapes p.rows.
func (p Picker) toggleShowHidden() Picker {
	p.showHidden = !p.showHidden
	p.sel = 0
	return p.rebuild()
}

// hideOrUnhideCurrent is ctrl+x: on an ordinary (not-yet-hidden) row it
// hides the model under the cursor; on a row already shown dimmed (only
// reachable with showHidden on) it un-hides instead — "same key, reads as
// a toggle... the escape hatch that makes pressing it a decision you
// cannot regret" (design doc §2). It is a no-op on a header row or an
// empty list, and a no-op entirely when curationStore is nil (Layer 2 has
// nowhere to record the decision, so there is nothing safe to do besides
// leave the row exactly as it was — silently degrading to "cannot hide"
// is judged less surprising here than hiding for one render frame and
// then having it reappear on the very next keystroke's rebuild).
//
// The one-line undo notice the design doc's own wireframe shows riding
// Root.slashNotice ("hid gemini-embedding-2 · ctrl+z undo") is left for a
// follow-up slice: it needs a chord (ctrl+z) not yet wired to anything in
// this package, and this method already gives ctrl+x itself a full,
// symmetrical undo path (press it again on the same row) that does not
// depend on that second chord existing.
func (p Picker) hideOrUnhideCurrent() (Picker, string) {
	if p.curationStore == nil || len(p.rows) == 0 {
		return p, ""
	}
	row := p.selected()
	if row.header {
		return p, ""
	}
	ref := row.cand.Model.Ref
	if row.hidden {
		if err := p.curationStore.Unhide(ref); err != nil {
			return p, "could not un-hide " + ref + ": " + err.Error()
		}
		return p.rebuild(), "shown " + ref
	}
	if err := p.curationStore.Hide(ref); err != nil {
		return p, "could not hide " + ref + ": " + err.Error()
	}
	p.sel = 0
	return p.rebuild(), "hid " + ref
}

// renderPicker draws the full-screen overlay (§9.4). Like renderHelp, it
// replaces the whole live region rather than composing with the input box
// and footer: there is nothing left to type into chat while the picker owns
// the keyboard, and drawing both would waste rows §9.1's narrowest
// breakpoint cannot spare.
func (m Root) renderPicker() string {
	g := m.lay.glyphs()
	width := m.lay.ContentWidth()
	p := m.picker

	// hiddenCount is Layer 2's own footer number (design doc §2's
	// wireframe: "models · 26 shown · 15 hidden"). It is 0 whenever there
	// is no curationStore at all, which is exactly what makes the
	// pre-F5 header line ("models · N") below unchanged for every caller
	// that never wires one — countCurationHidden already returns 0 for a
	// nil store.
	hiddenCount := p.countCurationHidden()
	var b strings.Builder
	if hiddenCount > 0 {
		fmt.Fprintf(&b, " models %s %d shown %s %d hidden\n", g.dot, countModelRows(p.rows), g.dot, hiddenCount)
	} else {
		fmt.Fprintf(&b, " models %s %d\n", g.dot, countModelRows(p.rows))
	}
	if notice := catalogNotice(p.cat); notice != "" {
		b.WriteString(" " + m.styles.Warn.Render(notice) + "\n")
	}
	fmt.Fprintf(&b, " %s %s%s\n", searchGlyph(g), p.query, g.streamCursor)
	b.WriteString(" " + strings.Repeat(g.rule, max(width-2, 1)) + "\n")

	if len(p.rows) == 0 {
		b.WriteString(" " + emptyPickerMessage(p) + "\n")
	}
	visible, offset := visiblePickerRows(p.rows, p.sel, pickerMaxVisibleRows)
	for i, row := range visible {
		idx := offset + i
		for _, line := range renderPickerRow(g, m.styles, width, row, idx == p.sel, row.provider != "" && row.cand.Model.Ref == p.active, p.favorites[strings.ToLower(row.cand.Model.Ref)]) {
			b.WriteString(" " + line + "\n")
		}
	}

	// "+N hidden · ctrl+h show" only when there is something to reveal
	// and it is not on screen already — the design doc's own wireframe
	// line, dropped entirely once showHidden is on (the rows themselves
	// are the proof, no separate count line needed).
	if hiddenCount > 0 && !p.showHidden {
		fmt.Fprintf(&b, " %s\n", m.styles.Dim.Render(fmt.Sprintf("+ %d hidden %s ctrl+h show", hiddenCount, g.dot)))
	}

	b.WriteString(" " + strings.Repeat(g.rule, max(width-2, 1)) + "\n")
	b.WriteString(fmt.Sprintf(" %s move  enter use  %s%s collapse\n", g.scrollHint, g.inputPrefix, g.inputPrefix))
	if p.curationStore != nil {
		fmt.Fprintf(&b, " ctrl+x hide  ctrl+h %s  ctrl+f filter:%s  esc close\n", showHiddenLabel(p.showHidden), p.filter.label())
	} else {
		fmt.Fprintf(&b, " ctrl+f filter:%s  esc close\n", p.filter.label())
	}
	return b.String()
}

// showHiddenLabel is ctrl+h's own footer wording, "show" when hidden rows
// are not currently drawn and "hide" once they are — describing what the
// key does next, the same "name the verb, not the current state" idiom
// ctrl+f's own "filter:all" label already uses for its own cycle.
func showHiddenLabel(showing bool) string {
	if showing {
		return "hide hidden"
	}
	return "show hidden"
}

func emptyPickerMessage(p Picker) string {
	if p.cat == nil || p.cat.Len() == 0 {
		return "no catalog loaded yet"
	}
	return "no models match \"" + p.query + "\""
}

// catalogNotice is the one-line honesty check §4.4 promises ("Stale means
// the data comes from an expired cache, and Seeded means it comes from the
// embedded seed. Both are shown, never hidden" — catalog.Catalog's own
// comment) but that, before this, only ever reached `ishakat models` on the
// command line: the interactive picker drew the exact same 13 rows whether
// they came from a live OmniRoute or from the seed nobody had verified,
// with nothing to tell the two apart short of noticing the count matched
// seed.json's by memory.
//
// Seeded outranks Stale (an unverified placeholder is a stronger claim than
// an old-but-real one), Stale outranks the F11 "catalogs refreshed / N
// pending" case below (a whole cache being old is a stronger claim than
// some of a fresh one's providers having failed), and all three outrank a
// plain "no notes" nil. It reuses resumeAge rather than
// internal/app.humanAge for the same reason resumemenu.go's own copy does:
// internal/app depends on internal/tui, not the other way (§6.1), so a
// three-line helper is duplicated, not shared.
//
// The PendingProviders case (docs/ROADMAP-ux-2026-08-20.md's W4 section)
// is deliberately read straight off the immutable Catalog snapshot rather
// than tracked as separate "just refreshed" transient state on Root/Picker:
// catalog.Build already counts, per provider, exactly the p.Discover &&
// !p.DiscoverOK condition this needs (the same one behind the "could not
// list the models of X" Note and HealthUnreachable), so a snapshot that is
// neither Seeded nor Stale but still carries PendingProviders > 0 IS "a
// refresh just landed and some providers did not answer" — no tea.Tick
// countdown, no extra field on CatalogRefreshedMsg, nothing that has to be
// cleared later (§14's "no always-on ticker" rule, the same reasoning
// applyPhaseWait's own doc comment already gives for PhaseWaitMsg).
func catalogNotice(cat *catalog.Catalog) string {
	if cat == nil {
		return ""
	}
	switch {
	case cat.Seeded:
		return "showing the embedded seed — not verified against any provider; run with a reachable OmniRoute (or `ishakat models --refresh`) to replace it"
	case cat.Stale:
		if !cat.FetchedAt.IsZero() {
			return "stale cache from " + resumeAge(cat.FetchedAt) + " ago — refreshing in the background"
		}
		return "stale cache — refreshing in the background"
	case cat.PendingProviders > 0:
		return "catalogs refreshed — " + pendingProvidersLabel(cat.PendingProviders) + " pending"
	default:
		return ""
	}
}

// pendingProvidersLabel is catalogNotice's own singular/plural wording,
// mirroring internal/provider.plural's exact "%d word" shape (that helper
// lives in internal/provider, unreachable from here for the same §6.1
// reason resumeAge is duplicated rather than shared) — "1 provider
// pending", never "1 providers pending".
func pendingProvidersLabel(n int) string {
	word := "providers"
	if n == 1 {
		word = "provider"
	}
	return strconv.Itoa(n) + " " + word
}

func countModelRows(rows []pickerRow) int {
	n := 0
	for _, r := range rows {
		if !r.header {
			n++
		}
	}
	return n
}

// pickerMaxVisibleRows caps how many picker rows are drawn at once,
// regardless of how many the catalog matched — reported in practice as
// "muestra demasiados modelos" once OmniRoute alone hands back a few
// hundred: with every row drawn unconditionally the frame grew taller than
// most terminals, so moving the cursor past whatever the terminal could
// actually show scrolled the *terminal's own backscroll*, not the picker's
// selection — the top of the list (and the cursor sitting in it) simply
// fell off the top of the screen with nothing left redrawing it back into
// view. 10 matches slashMenuRows' own reasoning in spirit (§9.6 picked 5
// for a single-line dropdown squeezed above the input box; the picker owns
// the full screen and models read as two short lines each, so its budget
// is roughly double).
const pickerMaxVisibleRows = 10

// visiblePickerRows returns the window of rows that keeps sel on screen
// when there are more rows than max can show at once, plus the index the
// window starts at (so the caller can map a visible position back to its
// real row index). It is picker.go's own copy of slashmenu.go's
// visibleSlashRows rather than a shared helper: the two packages' rows are
// different types, and Go's lack of a lightweight way to share four lines
// of index arithmetic across two unrelated slice element types is not
// worth a generic parameter only ever instantiated twice.
func visiblePickerRows(rows []pickerRow, sel, max int) ([]pickerRow, int) {
	if len(rows) <= max {
		return rows, 0
	}
	start := sel - max/2
	if start < 0 {
		start = 0
	}
	if start > len(rows)-max {
		start = len(rows) - max
	}
	return rows[start : start+max], start
}

// searchGlyph is the search field's leading mark. It reuses the model
// footer's own bullet rather than inventing a new repertoire entry — see
// glyphs.go's package comment on why a new decorative character is never
// added lightly.
func searchGlyph(g glyphs) string { return g.modelMark }

// renderPickerRow draws one row: a single line for a provider header, and
// for a model either one line or two depending on width. §9.4's own
// wireframe ("Dos líneas por modelo: identificador arriba, metadatos
// abajo") is drawn at 40 columns, and its prose gives the actual reason:
// "A 40 columnas meterlo todo en una línea obliga a truncar el ID, que es
// justamente el dato que hay que leer" — the two-line layout exists to
// protect the id from truncation, not because a model is entitled to two
// rows on principle. Once the terminal is wide enough that id and metadata
// fit side by side without cutting either, stacking them anyway is exactly
// the wasted space reported in practice — a picker that only shows a
// handful of rows at a time was burning half of them on blank-looking
// metadata lines while the cursor scrolled off screen.
func renderPickerRow(g glyphs, st theme.Styles, width int, row pickerRow, selected, active, favorite bool) []string {
	pointer := " "
	if selected {
		pointer = g.inputPrefix
	}

	if row.header {
		toggle := "v"
		if row.collapsed {
			toggle = ">"
		}
		label := strings.ToUpper(row.provider)
		if row.collapsed && row.count > 0 {
			label += fmt.Sprintf(" (%d)", row.count)
		}
		line := fmt.Sprintf("%s%s %s", pointer, toggle, label)
		line = ansi.Truncate(line, width, "…")
		if selected {
			line = st.Accent.Render(line)
		} else {
			line = st.Dim.Render(line)
		}
		return []string{line}
	}

	mark := " "
	switch {
	case active:
		mark = g.assistantMark
	case favorite:
		mark = g.modelMark
	}

	providerID, wireID, ok := catalog.SplitRef(row.cand.Model.Ref)
	if !ok {
		wireID = row.cand.Model.Ref
	}
	id := fmt.Sprintf("%s %s %s", pointer, mark, wireID)
	// F11 (docs/ROADMAP-ux-2026-08-20.md's DECISION-3): append the
	// provider's short display label — "google", not the raw
	// "gemini-direct" id — dimmed, in brackets. DECISION-3 explicitly
	// keeps `id` as the only thing configs, refs and session files ever
	// store; this label is purely a render-time decoration looked up
	// against the static preset table, never persisted. A provider the
	// user declared entirely by hand (no matching preset) has no known
	// label, so the bracket is simply omitted rather than guessed.
	if label, ok := config.LabelFor(providerID); ok {
		id += " " + st.Dim.Render("["+label+"]")
	}
	meta := pickerMetaLine(g, row.cand.Model)
	// Layer 2 (F5, ctrl+h "show hidden"): tag a hidden row with why, the
	// same reason vocabulary catalog.Reason already uses ("hidden by
	// you", "deprecated", etc. — design doc §2's own three examples).
	if row.hidden && row.hiddenReason != "" {
		meta += " " + g.dot + " " + row.hiddenReason
	}

	styleID := func(s string) string {
		switch {
		// A hidden row stays dimmed even while selected — the cursor's
		// own pointer glyph above is what shows where it is, and giving
		// it the ordinary Accent color would make a row the user just
		// asked NOT to see look identical to any other selection.
		case row.hidden:
			return st.Dim.Render(s)
		case selected:
			return st.Accent.Render(s)
		case favorite:
			return st.Warn.Render(s)
		default:
			return s
		}
	}

	// pickerRowGap separates id from metadata when they share a line — two
	// spaces, the same visual break the two-line layout's leading "   "
	// left in front of the metadata below the id.
	const pickerRowGap = "  "
	if lipglossWidth(id)+lipglossWidth(pickerRowGap)+lipglossWidth(meta) <= width {
		return []string{styleID(id) + pickerRowGap + st.Dim.Render(meta)}
	}

	id = ansi.Truncate(id, width, "…")
	metaLine := ansi.Truncate("   "+meta, width, "…")
	return []string{styleID(id), st.Dim.Render(metaLine)}
}

// pickerMetaLine is the second row of a model entry: context window, cost,
// capability badges and — only when this exact model has actually been used
// before — its measured latency. §9.4 is explicit that the latency comes
// from local statistics and never from a guess.
func pickerMetaLine(g glyphs, m catalog.Model) string {
	parts := []string{contextLabel(m), costLabel(m)}
	if caps := capsLabel(m.Caps); caps != "" {
		parts = append(parts, caps)
	}
	if lat := latencyLabel(m); lat != "" {
		parts = append(parts, lat)
	}
	sep := " " + g.dot + " "
	return strings.Join(parts, sep)
}

func contextLabel(m catalog.Model) string {
	if !m.ContextKnown() {
		return "?"
	}
	return formatContextTokens(m.Context)
}

func formatContextTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return trimFloat(float64(n)/1_000_000, 1) + "M"
	case n >= 1000:
		return strconv.Itoa(n/1000) + "k"
	default:
		return strconv.Itoa(n)
	}
}

// costLabel is "FREE" for a known-free model, "—" for unknown cost (never
// "$0" — §4.2 is explicit that those two must never be drawn the same way),
// and "$in/$out" otherwise.
func costLabel(m catalog.Model) string {
	if m.Free() {
		return "FREE"
	}
	if m.Cost == nil {
		return "—"
	}
	return "$" + trimFloat(m.Cost.In, 3) + "/$" + trimFloat(m.Cost.Out, 3)
}

// capsLabel abbreviates capabilities to one letter each rather than the
// wireframe's emoji (🔧👁): emoji are outside the WGL4 repertoire this
// package restricts itself to (see glyphs.go), and a console that cannot
// draw them would show two boxes instead of information.
func capsLabel(c catalog.Caps) string {
	var b strings.Builder
	if c.Tools {
		b.WriteByte('T')
	}
	if c.Vision {
		b.WriteByte('V')
	}
	if c.Reasoning {
		b.WriteByte('R')
	}
	return b.String()
}

// latencyLabel is "" when the model has never actually been used —
// §9.4's rule against inventing a number — or "0.8s"/"3s" otherwise.
func latencyLabel(m catalog.Model) string {
	if m.P50Latency <= 0 {
		return ""
	}
	s := m.P50Latency.Seconds()
	if s < 10 {
		return trimFloat(s, 1) + "s"
	}
	return strconv.Itoa(int(s+0.5)) + "s"
}

// trimFloat formats f with up to prec decimals, dropping trailing zeros
// (and a trailing '.') so "3.000" reads as "3" and "0.075" keeps every
// meaningful digit.
func trimFloat(f float64, prec int) string {
	s := strconv.FormatFloat(f, 'f', prec, 64)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(s, "0")
		s = strings.TrimRight(s, ".")
	}
	return s
}

// updatePicker handles every key while mode == ModePicker (§9.4). Unlike
// updateSlashMenu it owns the keyboard outright — there is no textarea
// underneath it to fall through to — so every branch below either returns
// handled or does nothing at all, never "not mine, try the next thing".
func (m Root) updatePicker(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	switch keyPressString(key) {
	case m.keys.Cancel:
		m.mode = ModeChat
		return m, nil
	case "up":
		m.picker = m.picker.moveSel(-1)
		return m, nil
	case "down":
		m.picker = m.picker.moveSel(1)
		return m, nil
	case "left":
		m.picker = m.picker.collapseCurrent(true)
		return m, nil
	case "right":
		m.picker = m.picker.collapseCurrent(false)
		return m, nil
	case "backspace":
		m.picker = m.picker.backspace()
		return m, nil
	// ctrl+f is hardcoded rather than read from m.keys: §9.4 names it by
	// this exact chord, and the footer hint printed by renderPicker would
	// have to grow its own remapping display for a key nothing else in the
	// keymap currently makes configurable.
	case "ctrl+f":
		m.picker = m.picker.cycleFilter()
		return m, nil
	// ctrl+x/ctrl+h are Layer 2's own two chords (F5,
	// docs/DESIGN-model-curation.md §2), hardcoded for the identical
	// reason ctrl+f is above: named by this exact chord in the design
	// doc, and neither is in m.keys' remapping table today. Both are
	// no-ops when m.picker.curationStore is nil (see
	// hideOrUnhideCurrent/toggleShowHidden's own doc comments) — a
	// caller that never wires Layer 2 keeps today's behaviour exactly,
	// including "x" still only ever reaching typeText below.
	case "ctrl+x":
		next, notice := m.picker.hideOrUnhideCurrent()
		m.picker = next
		if notice == "" {
			return m, nil
		}
		return m.slashNotice(m.lay.glyphs().assistantMark + " " + notice)
	case "ctrl+h":
		m.picker = m.picker.toggleShowHidden()
		return m, nil
	case m.keys.Submit:
		if len(m.picker.rows) == 0 {
			return m, nil
		}
		row := m.picker.selected()
		if row.header {
			m.picker = m.picker.toggleCurrent()
			return m, nil
		}
		ref := row.cand.Model.Ref
		return m, func() tea.Msg { return modelChosenMsg{Ref: ref} }
	default:
		if key.Text != "" {
			m.picker = m.picker.typeText(key.Text)
		}
		return m, nil
	}
}

// confirmLine is the §4.6 one-liner a direct model switch leaves behind:
// "── now: gpt-5-mini ──". It rides the transcript like any other notice
// (Root.slashNotice) rather than a bespoke tea.Println, so the eviction
// machinery of evictOverflow treats it exactly like every other line.
func confirmLine(g glyphs, name string) string {
	rule := strings.Repeat(g.rule, 2)
	return rule + " now: " + name + " " + rule
}

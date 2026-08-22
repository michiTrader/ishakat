// models.go implements /models (§13, Step 13): a read-only listing of the
// catalog inside the running session, grouped by provider like `ishakat
// models` on the command line — but drawn as a slashNotice against the
// catalog snapshot the session already holds (m.cat), never by shelling out
// to the subcommand or importing internal/app's own writeModelsText.
//
// internal/app is not importable from here: it pulls in net/http
// transitively (provider discovery, models.dev fetch), and TestTUINoImportaHTTP
// (§6.1) forbids that anywhere in this package's dependency closure. So this
// file re-renders the same information with picker.go's own label helpers
// (contextLabel, costLabel, capsLabel, latencyLabel) rather than reusing
// internal/app/models_cmd.go's writeModelsText verbatim — the two are meant
// to agree on what a model row says, not share a call.
package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/catalog"
)

// runModelsCommand renders every model the current catalog snapshot knows
// about, grouped by provider in the catalog's own first-appearance order
// (catalog.Providers), with the same hidden-deprecated/stale/seeded honesty
// the picker's own header line already draws (catalogNotice). An empty or
// missing catalog is reported instead of an empty list with no explanation —
// the same "no models" case models_cmd.go's writeModelsText guards against.
//
// args carries design doc §2.1's two sub-verbs, dispatched before the
// catalog-listing path below since neither reads m.cat at all: "hidden"
// lists every ref the user has hidden and why (runModelsHidden), "reset"
// drops every one of those hides (runModelsReset). Both have to read
// m.curationStore directly rather than m.cat, because internal/app's
// curationRules/applyCuration has already removed every hidden ref from
// the catalog snapshot by the time a running session ever sees it — the
// same constraint pickerRow.hidden's own doc comment documents for ctrl+h.
// Anything else (including "") falls through to the ordinary listing,
// unchanged from before this Kind took an argument at all.
func (m Root) runModelsCommand(args string) (tea.Model, tea.Cmd) {
	switch strings.ToLower(strings.TrimSpace(args)) {
	case "hidden":
		return m.runModelsHidden()
	case "reset":
		return m.runModelsReset()
	}

	g := m.lay.glyphs()
	if m.cat == nil || m.cat.Len() == 0 {
		return m.slashNotice(g.warnMark + " no hay catalogo cargado todavia")
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s models %s %d", g.assistantMark, g.dot, m.cat.Len())
	if notice := catalogNotice(m.cat); notice != "" {
		fmt.Fprintf(&b, "\n  %s %s", g.warnMark, notice)
	}

	favs := favoriteSet(m.favorites)
	byProvider := m.cat.ByProvider()
	for _, provider := range m.cat.Providers() {
		models := byProvider[provider]
		fmt.Fprintf(&b, "\n\n%s (%d)", strings.ToUpper(provider), len(models))
		for _, mdl := range sortedByRef(models) {
			mark := " "
			if mdl.Ref == m.model {
				mark = g.assistantMark
			} else if favs[strings.ToLower(mdl.Ref)] {
				mark = g.modelMark
			}
			_, wireID, ok := catalog.SplitRef(mdl.Ref)
			if !ok {
				wireID = mdl.Ref
			}
			fmt.Fprintf(&b, "\n  %s %-28s  %s", mark, wireID, modelsRowMeta(g, mdl))
		}
	}

	return m.slashNotice(b.String())
}

// runModelsHidden implements "/models hidden" (design doc §2.1): every ref
// the user has hidden — through a ctrl+x press or a "/model hide" — along
// with why (Reason(), always "hidden by you" for this store's own entries,
// per CurationStore.Reason's own doc comment on the difference between a
// human decision and a catalog.curate heuristic). This reads
// m.curationStore.Hidden() rather than walking m.cat, since m.cat has
// already had every one of these refs removed by internal/app's own
// curationRules/applyCuration by the time Root ever sees a snapshot — the
// "listing has to look upstream of the filter it is describing" problem
// pickerRow.hidden's own doc comment names for ctrl+h.
func (m Root) runModelsHidden() (tea.Model, tea.Cmd) {
	g := m.lay.glyphs()
	if m.curationStore == nil {
		return m.slashNotice(g.warnMark + " no hay almacen de curacion configurado; nada que listar")
	}
	refs := m.curationStore.Hidden()
	if len(refs) == 0 {
		return m.slashNotice(g.assistantMark + " no hay modelos escondidos")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s escondidos %s %d", g.assistantMark, g.dot, len(refs))
	for _, ref := range refs {
		fmt.Fprintf(&b, "\n  %s  %s", ref, m.curationStore.Reason(ref))
	}
	return m.slashNotice(b.String())
}

// runModelsReset implements "/models reset" (design doc §2.1): drop every
// user hide, leaving [catalog.curate]'s own automatic rules (and any
// Keep) untouched — CurationStore.Reset's own contract, which this only
// calls and reports on.
func (m Root) runModelsReset() (tea.Model, tea.Cmd) {
	g := m.lay.glyphs()
	if m.curationStore == nil {
		return m.slashNotice(g.warnMark + " no hay almacen de curacion configurado; nada que reiniciar")
	}
	n := len(m.curationStore.Hidden())
	if n == 0 {
		return m.slashNotice(g.assistantMark + " no habia modelos escondidos")
	}
	if err := m.curationStore.Reset(); err != nil {
		return m.slashNotice(g.warnMark + " no se pudo reiniciar: " + err.Error())
	}
	return m.slashNotice(fmt.Sprintf("%s %d modelo(s) ya no escondidos por ti", g.assistantMark, n))
}

// sortedByRef orders a provider's models alphabetically by reference so the
// list reads the same run to run — ByProvider preserves catalog.Models'
// build-time order, which is discovery order and not something a user
// scanning the list should have to depend on.
func sortedByRef(models []catalog.Model) []catalog.Model {
	out := append([]catalog.Model(nil), models...)
	sort.Slice(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	return out
}

// modelsRowMeta is /models' one metadata line per model: context window,
// cost, capability badges and health, reusing picker.go's own label
// functions so this listing and the §9.4 picker can never disagree about
// what a model's price or context window reads as.
func modelsRowMeta(g glyphs, mdl catalog.Model) string {
	parts := []string{contextLabel(mdl), costLabel(mdl)}
	if caps := capsLabel(mdl.Caps); caps != "" {
		parts = append(parts, caps)
	}
	if mdl.Health != catalog.HealthOK {
		parts = append(parts, "["+mdl.Health.String()+"]")
	}
	return strings.Join(parts, " "+g.dot+" ")
}

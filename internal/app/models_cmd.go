package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/MichiTrader/ishakat/internal/catalog"
	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/xdg"
)

// models_cmd.go is the `ishakat models` subcommand of Step 6.
//
// It is the honest window into the catalog, and it is also the fastest way
// to debug the merge: if the picker shows something strange, this command
// says which sources produced each record and what the cache holds.

// ModelsOptions are the flags of the subcommand.
type ModelsOptions struct {
	Version string

	// JSON emits one model per line, so it can be piped into jq like every
	// other machine-readable output in this program.
	JSON bool

	// Refresh forces going to the network before printing. Without it the
	// command answers from the cache, which is the §4.4 rule.
	Refresh bool

	// All bypasses curation entirely (design doc §2.1's CLI table: "existing
	// flag, now bypasses curation") — both Layer 0 (HideDeprecated) and
	// Layer 1/2 (catalog.Curate's automatic rules plus curation.json's own
	// hides). It lists the catalog UncuratedCatalog would build: everything
	// discovery/models.dev/config produced, with nothing removed. It never
	// touches the network on its own; it reuses whatever Cache/Index the
	// normal (possibly --refresh'd) load already produced.
	All bool

	// Hidden lists only what curation removed, and why — `ishakat models
	// --hidden` (design doc §2.1's CLI table). Mutually exclusive with All
	// in intent (one asks "show me everything", the other "show me only
	// what is NOT everything"), but Hidden wins if both are set: asking
	// what is hidden is the more specific question.
	Hidden bool

	// Why is a single ref to explain in full (`ishakat models --why <ref>`,
	// design doc §2.1: "a question that answers itself"). When set, it
	// takes over the whole command — JSON/Refresh still apply (Refresh
	// because a stale answer to "where did my model go" is worse than a
	// slow one; JSON does not, since this is a diagnostic for a human, not
	// a machine-readable stream).
	Why string

	// Filter is an optional substring over the reference and the name.
	Filter string

	ConfigPath string

	// Test seams.
	Config *config.Config
	Stdout io.Writer
	Stderr io.Writer
}

// Models runs the subcommand and returns the process exit code.
func Models(opts ModelsOptions) int {
	out := opts.Stdout
	if out == nil {
		out = os.Stdout
	}
	errw := opts.Stderr
	if errw == nil {
		errw = os.Stderr
	}

	cfg := opts.Config
	if cfg == nil {
		path := opts.ConfigPath
		if path == "" {
			path = xdg.ConfigFile()
		}
		loaded, err := config.Load(config.Options{UserPath: path})
		if err != nil {
			fmt.Fprintf(errw, "✗ configuration error: %v\n", err)
			return ExitError
		}
		cfg = loaded
	}

	snap := LoadCatalog(cfg)

	if opts.Refresh {
		// The only place in the program that blocks on the network before
		// printing, and it does so because the user explicitly asked for it.
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		refreshed, err := RefreshCatalog(ctx, cfg, opts.Version, snap)
		snap = refreshed
		if err != nil {
			fmt.Fprintf(errw, "⚠ incomplete refresh: %v\n", err)
		}
	}

	// --why takes over the whole command (design doc §2.1: a single ref's
	// full explanation, not a table row) — checked before --hidden/--all
	// since it is the most specific request of the three.
	if ref := strings.TrimSpace(opts.Why); ref != "" {
		return writeModelWhy(out, errw, cfg, snap, ref)
	}

	// --hidden lists only applyCuration's own audit trail (snap.Hidden),
	// never snap.Catalog.Models: by the time Models() has a snapshot,
	// LoadCatalog/RefreshCatalog have already removed every one of these
	// refs from Catalog, so listing "what is hidden" from Catalog itself
	// is not just wrong, it is asking the wrong data structure the wrong
	// question (Catalog answers "what to show").
	if opts.Hidden {
		return writeModelsHidden(out, errw, snap)
	}

	models := snap.Catalog.Models
	if opts.All {
		// --all bypasses curation entirely (UncuratedCatalog's own doc
		// comment): rebuilt from the same Cache/Index snap already holds,
		// no new network call, just without applyCuration ever running.
		models = UncuratedCatalog(cfg, snap).Models
	}
	if f := strings.TrimSpace(strings.ToLower(opts.Filter)); f != "" {
		var kept []catalog.Model
		for _, m := range models {
			if strings.Contains(strings.ToLower(m.Ref), f) ||
				strings.Contains(strings.ToLower(m.Name), f) {
				kept = append(kept, m)
			}
		}
		models = kept
	}

	if opts.JSON {
		return writeModelsJSON(out, errw, snap, models)
	}
	return writeModelsText(out, errw, snap, models, cfg)
}

// modelLine is the JSON shape emitted by --json. It is deliberately flatter
// than catalog.Model —no nested caps object, tags already joined— because
// what this output is for is a jq one-liner, and every level of nesting is
// one more thing to type in the filter.
type modelLine struct {
	Ref       string   `json:"ref"`
	Provider  string   `json:"provider"`
	WireID    string   `json:"wire_id"`
	Name      string   `json:"name,omitempty"`
	Family    string   `json:"family,omitempty"`
	Context   int      `json:"context,omitempty"`
	MaxOutput int      `json:"max_output,omitempty"`
	CostIn    *float64 `json:"cost_in,omitempty"`
	CostOut   *float64 `json:"cost_out,omitempty"`
	Free      bool     `json:"free,omitempty"`
	Tools     bool     `json:"tools,omitempty"`
	Vision    bool     `json:"vision,omitempty"`
	Reasoning bool     `json:"reasoning,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	Source    string   `json:"source"`
	Health    string   `json:"health"`
	UseCount  int      `json:"use_count,omitempty"`
}

func writeModelsJSON(out, errw io.Writer, snap CatalogSnapshot, models []catalog.Model) int {
	enc := json.NewEncoder(out)
	for _, m := range models {
		line := modelLine{
			Ref:       m.Ref,
			Provider:  m.Provider,
			WireID:    m.WireID,
			Name:      m.Name,
			Family:    m.Family,
			Context:   m.Context,
			MaxOutput: m.MaxOutput,
			Free:      m.Free(),
			Tools:     m.Caps.Tools,
			Vision:    m.Caps.Vision,
			Reasoning: m.Caps.Reasoning,
			Tags:      m.Tags,
			Source:    m.Source.String(),
			Health:    m.Health.String(),
			UseCount:  m.UseCount,
		}
		if m.Cost != nil {
			in, outc := m.Cost.In, m.Cost.Out
			line.CostIn, line.CostOut = &in, &outc
		}
		if err := enc.Encode(line); err != nil {
			fmt.Fprintf(errw, "✗ %v\n", err)
			return ExitError
		}
	}
	// Notes go to stderr so stdout stays a clean stream of JSON objects.
	for _, n := range snap.Catalog.Notes {
		fmt.Fprintf(errw, "⚠ %s\n", n)
	}
	if len(models) == 0 {
		return ExitError
	}
	return ExitOK
}

func writeModelsText(out, errw io.Writer, snap CatalogSnapshot, models []catalog.Model, cfg *config.Config) int {
	if len(models) == 0 {
		fmt.Fprintln(errw, "no models in the catalog. Run `ishakat models --refresh` "+
			"or check the [[provider]] entries in your configuration.")
		for _, n := range snap.Catalog.Notes {
			fmt.Fprintf(errw, "⚠ %s\n", n)
		}
		printDisabledProviders(errw, cfg)
		return ExitError
	}

	byProvider := map[string][]catalog.Model{}
	var order []string
	for _, m := range models {
		if _, seen := byProvider[m.Provider]; !seen {
			order = append(order, m.Provider)
		}
		byProvider[m.Provider] = append(byProvider[m.Provider], m)
	}

	// Column widths are computed over what is actually going to be printed
	// so the table does not sprawl when every reference is short. It is not
	// aligned to 40 columns on purpose: this output is for a pipe or a wide
	// terminal, and the phone-sized view is the picker of §9.4.
	refW := 0
	for _, m := range models {
		if n := len(m.Ref); n > refW {
			refW = n
		}
	}
	if refW > 48 {
		refW = 48
	}

	for _, p := range order {
		list := byProvider[p]
		fmt.Fprintf(out, "%s (%d)\n", p, len(list))
		for _, m := range list {
			fmt.Fprintf(out, "  %-*s  %9s  %10s  %s\n",
				refW, truncate(m.Ref, refW),
				contextCol(m),
				costCol(m),
				strings.Join(badges(m), " "))
		}
		fmt.Fprintln(out)
	}

	fmt.Fprintf(out, "%d model(s)", len(models))
	if !snap.Catalog.FetchedAt.IsZero() {
		fmt.Fprintf(out, " · updated %s ago", humanAge(time.Since(snap.Catalog.FetchedAt)))
	}
	switch {
	case snap.Catalog.Seeded:
		fmt.Fprint(out, " · embedded seed catalog")
	case snap.Catalog.Stale:
		fmt.Fprint(out, " · stale cache")
	}
	fmt.Fprintln(out)

	for _, n := range snap.Catalog.Notes {
		fmt.Fprintf(errw, "⚠ %s\n", n)
	}
	printDisabledProviders(errw, cfg)
	return ExitOK
}

// printDisabledProviders is the fix for a confusion reported in practice: a
// user adds NVIDIA, Gemini or Aerolink to their configuration, exports the
// matching API key, refreshes the catalog — and still only sees OmniRoute.
// The provider entry was there, but `enabled = false` (the value
// config.example.toml ships for every provider except the first one, so a
// copy-pasted block stays off until edited) filtered it out of
// EnabledProviders silently, with nothing on screen pointing at the one line
// that needed to change.
//
// This prints right after the model table/notes, in every text-mode run
// (including the "0 models" early return), so the missing provider is never
// more than one command away from the reason it is missing.
func printDisabledProviders(errw io.Writer, cfg *config.Config) {
	if cfg == nil {
		return
	}
	var disabled []string
	for _, p := range cfg.Providers {
		if !p.Enabled {
			disabled = append(disabled, p.ID)
		}
	}
	if len(disabled) == 0 {
		return
	}
	fmt.Fprintf(errw, "○ %d provider(s) configured but disabled: %s\n",
		len(disabled), strings.Join(disabled, ", "))
	fmt.Fprintln(errw, "  set enabled = true under its [[provider]] block in "+
		"your config.toml and run `ishakat models --refresh` again.")
}

// contextCol renders the window. An unknown one is "—" and not a guessed
// number: §4.3 forbids inventing 128k.
func contextCol(m catalog.Model) string {
	if !m.ContextKnown() {
		return "—"
	}
	return humanTokens(m.Context)
}

// costCol renders the input price. nil cost prints "—", never "$0" (§4.2).
func costCol(m catalog.Model) string {
	switch {
	case m.Cost == nil:
		return "—"
	case m.Cost.Zero():
		return "free"
	default:
		return "$" + strconv.FormatFloat(m.Cost.In, 'g', 3, 64)
	}
}

func humanTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return strconv.FormatFloat(float64(n)/1_000_000, 'g', 3, 64) + "M"
	case n >= 1000:
		return strconv.Itoa(n/1000) + "k"
	default:
		return strconv.Itoa(n)
	}
}

func badges(m catalog.Model) []string {
	var out []string
	if m.Caps.Tools {
		out = append(out, "tools")
	}
	if m.Caps.Vision {
		out = append(out, "vision")
	}
	if m.Caps.Reasoning {
		out = append(out, "reasoning")
	}
	tags := append([]string(nil), m.Tags...)
	sort.Strings(tags)
	for _, t := range tags {
		if t == catalog.TagFree && m.Cost.Zero() {
			continue // already shown in the cost column
		}
		out = append(out, t)
	}
	if m.Health != catalog.HealthOK {
		out = append(out, "["+m.Health.String()+"]")
	}
	return out
}

// writeModelsHidden implements `ishakat models --hidden` (design doc §2.1):
// only what applyCuration removed from this snapshot, and why — read from
// snap.Hidden, never from snap.Catalog (by the time Models() has a
// snapshot, every one of these refs is already gone from Catalog.Models).
func writeModelsHidden(out, errw io.Writer, snap CatalogSnapshot) int {
	if len(snap.Hidden) == 0 {
		fmt.Fprintln(out, "nothing hidden — every discovered model is shown "+
			"(see `ishakat models --all` if you expected more than that)")
		return ExitOK
	}

	hidden := append([]catalog.Hidden(nil), snap.Hidden...)
	sort.Slice(hidden, func(i, j int) bool { return hidden[i].Model.Ref < hidden[j].Model.Ref })

	refW := 0
	for _, h := range hidden {
		if n := len(h.Model.Ref); n > refW {
			refW = n
		}
	}
	if refW > 48 {
		refW = 48
	}
	for _, h := range hidden {
		fmt.Fprintf(out, "%-*s  %s\n", refW, truncate(h.Model.Ref, refW), h.Reason)
	}
	fmt.Fprintf(out, "%d model(s) hidden · still resolvable by exact ref "+
		"(`/model <ref>`) · see `ishakat models --why <ref>` for the full story\n",
		len(hidden))
	return ExitOK
}

// resolveOptsFor builds the catalog.ResolveOptions writeModelWhy needs from
// the configuration — the same two fields Root.resolveOptions (root.go)
// reads for the TUI's own /model resolver, so a `--why` lookup and a live
// session never disagree about what an alias or prefer_free means.
func resolveOptsFor(cfg *config.Config) catalog.ResolveOptions {
	if cfg == nil {
		return catalog.ResolveOptions{}
	}
	return catalog.ResolveOptions{Alias: cfg.Alias, PreferFree: cfg.Catalog.PreferFree}
}

// writeModelWhy implements `ishakat models --why <ref>` (design doc §2.1's
// worked example): a single model's full diagnostic. It resolves ref
// against UncuratedCatalog(cfg, snap) — not snap.Catalog — precisely
// because the interesting case is a HIDDEN model, which snap.Catalog no
// longer contains at all; resolving against the uncurated list is the only
// way a bare wire_id like "gemini-embedding-2" can still find it via the
// same §4.5 suffix/word/fuzzy stages `/model` itself uses.
func writeModelWhy(out, errw io.Writer, cfg *config.Config, snap CatalogSnapshot, ref string) int {
	full := UncuratedCatalog(cfg, snap)
	res := full.Resolve(ref, resolveOptsFor(cfg))
	if !res.Outcome.Decided() {
		fmt.Fprintf(errw, "no model matches %q\n", ref)
		if len(res.Candidates) > 0 {
			fmt.Fprintln(errw, "closest candidates:")
			for i, c := range res.Candidates {
				if i >= 5 {
					break
				}
				fmt.Fprintf(errw, "  %s\n", c.Model.Ref)
			}
		}
		return ExitError
	}
	m := res.Model

	var hiddenEntry *catalog.Hidden
	for i := range snap.Hidden {
		if strings.EqualFold(snap.Hidden[i].Model.Ref, m.Ref) {
			hiddenEntry = &snap.Hidden[i]
			break
		}
	}

	fmt.Fprintln(out, m.Ref)

	discovered := "no (not listed by the provider)"
	if m.Source.Has(catalog.SourceDiscover) {
		discovered = "yes (provider lists it)"
	}
	fmt.Fprintf(out, "  discovered   %s\n", discovered)

	modelsDev := "not matched"
	if m.Source.Has(catalog.SourceModelsDev) {
		modelsDev = "matched (normalized)"
	}
	fmt.Fprintf(out, "  models.dev   %s\n", modelsDev)

	if hiddenEntry == nil {
		fmt.Fprintln(out, "  hidden by    not hidden — it is shown in `ishakat models`")
		fmt.Fprintln(out, "  still usable yes — it is already visible")
		return ExitOK
	}

	rule, because := whyReasonText(hiddenEntry.Reason, m)
	fmt.Fprintf(out, "  hidden by    %s\n", rule)
	fmt.Fprintf(out, "  because      %s\n", because)
	fmt.Fprintf(out, "  still usable yes — `/model %s` by exact ref\n", m.Ref)
	fmt.Fprintf(out, "  to show it   %s\n", whyReasonRemedy(hiddenEntry.Reason, m))
	return ExitOK
}

// whyReasonText maps a catalog.Reason to the "hidden by"/"because" pair of
// writeModelWhy's diagnostic block, following the design doc §2.1 worked
// example's exact wording where the reason matches (ReasonNonChatLimit,
// including its "modality alone would not have caught it" note).
func whyReasonText(reason catalog.Reason, m catalog.Model) (rule, because string) {
	switch reason {
	case catalog.ReasonNonChatModality:
		return "catalog.curate.chat_only",
			"its output modality does not include text — cannot emit a conversational turn"
	case catalog.ReasonNonChatLimit:
		because = "limit.output = 1 — cannot emit a conversational turn"
		for _, mod := range m.Modalities {
			if strings.EqualFold(mod, "text") {
				because += " (note: its output modality IS text, so the modality " +
					"check alone would not have caught it)"
				break
			}
		}
		return "catalog.curate.chat_only", because
	case catalog.ReasonNonChatSampling:
		return "catalog.curate.chat_only",
			"not a sampled model — temperature is explicitly disabled, with no tools and no structured output"
	case catalog.ReasonDeprecated:
		return "catalog.hide_deprecated", "the provider marked this model deprecated"
	case catalog.ReasonSuperseded:
		return "catalog.curate.hide_superseded",
			"a GA version of this model exists in the same provider without the preview/experimental suffix"
	case catalog.ReasonDatedTwin:
		return "catalog.curate.hide_dated_twins",
			"an undated version of this model exists in the same provider"
	case catalog.ReasonLatestAlias:
		return "catalog.curate.hide_latest", `this is a "-latest" alias`
	case catalog.ReasonUserGlob:
		return "your own hide list",
			"matched a hide glob in config.toml/[[provider]], or was hidden by you (ctrl+x / `/model hide`)"
	case catalog.ReasonUnhealthy:
		return "health check", "the model has been failing and was hidden"
	default:
		return string(reason), string(reason)
	}
}

// whyReasonRemedy is writeModelWhy's "to show it" line: the one edit or
// command that overrides the reason above. ReasonUserGlob gets its own
// remedy (`/model keep`, the exact inverse of what hid it) because
// KeepRefs/HideRefs is a curation.json decision, not a config.toml edit —
// every other reason is a [catalog.curate] rule and Keep (design doc
// §2.2) is what wins over all of them, exactly as the worked example says.
func whyReasonRemedy(reason catalog.Reason, m catalog.Model) string {
	if reason == catalog.ReasonUserGlob {
		return fmt.Sprintf("run `/model keep %s`, or remove it from your hide list", m.Ref)
	}
	_, wire, ok := catalog.SplitRef(m.Ref)
	if !ok {
		wire = m.WireID
	}
	return fmt.Sprintf("add %q to [catalog.curate].keep", wire)
}

func truncate(s string, max int) string {
	r := []rune(s)
	if max <= 1 || len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}

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

	// All disables the display filters (deprecated models stay hidden by
	// [catalog].hide_deprecated otherwise).
	All bool

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

	models := snap.Catalog.Models
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
	return writeModelsText(out, errw, snap, models, opts.All)
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

func writeModelsText(out, errw io.Writer, snap CatalogSnapshot, models []catalog.Model, all bool) int {
	if len(models) == 0 {
		fmt.Fprintln(errw, "no models in the catalog. Run `ishakat models --refresh` "+
			"or check the [[provider]] entries in your configuration.")
		for _, n := range snap.Catalog.Notes {
			fmt.Fprintf(errw, "⚠ %s\n", n)
		}
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
	return ExitOK
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

func truncate(s string, max int) string {
	r := []rune(s)
	if max <= 1 || len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}

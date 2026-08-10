package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/MichiTrader/ishakat/internal/agentsmd"
	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/theme"
	"github.com/MichiTrader/ishakat/internal/tui"
	"github.com/MichiTrader/ishakat/internal/xdg"
)

// reportTerminal prints the terminal half of `doctor`: how the working
// directory will be displayed, which colour depth and which characters the
// interface has decided it may use, why, and a sample of those characters.
//
// This section is the answer to a class of report that is otherwise impossible
// to act on. "The logo is illegible", "the gradient is white in PowerShell but
// coloured in Termux" and "the path shows as ~/D:\projects\ishakat" all have the
// same shape: the program made a decision about the terminal, the decision was
// wrong or the terminal could not honour it, and there was no way to tell which
// from the outside. Now there is, and every line names both the value and its
// cause, so the next report can arrive with the diagnosis already in it.
func reportTerminal(w io.Writer, cfg *config.Config) {
	colorOverride, glyphOverride := "", ""
	if cfg != nil {
		colorOverride, glyphOverride = cfg.UI.Color, cfg.UI.Glyphs
	}

	// The diagnosis is asked about stdout, not about stderr or a guess: whether
	// there is a terminal at all is the one question the environment cannot
	// answer, and a redirected stdout legitimately gets no colour.
	d := theme.Diagnose(colorOverride, glyphOverride, os.Stdout)

	if cwd, err := os.Getwd(); err == nil {
		// The display form, not the raw path: this is the exact string the
		// banner and the footer will show, and it is where "~/ishakat" for a
		// directory that is really ~/projects/ishakat was visible.
		fmt.Fprintf(w, "  cwd          %s\n", xdg.Pretty(cwd))
	}
	fmt.Fprintf(w, "  color        %-14s %s\n", d.Color, d.ColorReason)
	fmt.Fprintf(w, "  glyphs       %-14s %s\n", d.Glyphs, d.GlyphsReason)

	if set := d.Set(); len(set) > 0 {
		pairs := make([]string, 0, len(set))
		for _, s := range set {
			pairs = append(pairs, s.Name+"="+s.Value)
		}
		fmt.Fprintf(w, "  signals      %s\n", strings.Join(pairs, "  "))
	} else {
		// An empty environment is not a boring case: it is the signature of a
		// console host, and the reason the ASCII look is chosen on Windows.
		fmt.Fprintf(w, "  signals      none set\n")
	}

	fmt.Fprintln(w)
	for _, line := range tui.GlyphSample(d.Glyphs) {
		if line == "" {
			// No indent on a blank line: trailing whitespace is invisible
			// here and very visible in a pasted bug report.
			fmt.Fprintln(w)
			continue
		}
		fmt.Fprintf(w, "  %s\n", line)
	}

	if len(d.Advice) > 0 {
		fmt.Fprintln(w)
		for _, line := range d.Advice {
			fmt.Fprintf(w, "  note: %s\n", line)
		}
		fmt.Fprintf(w, "  note: config.toml is at %s\n", xdg.ConfigFile())
	}
}

// reportAgentsMD prints Step 18's three AGENTS.md paths and which of them
// were actually found, so "is my AGENTS.md being read at all" has an answer
// that does not require reading the source. Sources() lists all three
// regardless of existence — the closing criterion this satisfies is being
// able to see the paths even on a project that has none of them yet, not
// just the ones that are present.
func reportAgentsMD(w io.Writer, cfg *config.Config) {
	fmt.Fprintf(w, "  agents.md    %v\n", cfg == nil || cfg.App.AgentsMD)
	if cfg != nil && !cfg.App.AgentsMD {
		return
	}
	for _, src := range agentsmd.Sources(xdg.AgentsFile(), ".") {
		state := "not found"
		if _, err := os.Stat(src.Path); err == nil {
			state = "found"
		}
		fmt.Fprintf(w, "    %-8s %-8s %s\n", src.Layer, state, xdg.Pretty(src.Path))
	}
}

// doctorConfig loads the configuration for `doctor` without ever failing on it.
// A broken config.toml is exactly when a user runs this command, so the report
// has to survive one: the overrides are simply treated as unset and the problem
// is stated rather than raised. `config check` is the command that judges the
// file; this one only reads two fields out of it.
func doctorConfig() (*config.Config, string) {
	cfg, err := config.Load(config.Options{})
	if err != nil {
		return nil, fmt.Sprintf("could not be read (%v) — using defaults", err)
	}
	if len(cfg.Files) == 0 {
		return cfg, "none found — using defaults"
	}
	note := strings.Join(cfg.Files, ", ")
	if n := len(cfg.Warnings); n > 0 {
		note += fmt.Sprintf("  (%d warning(s), run `ishakat config check`)", n)
	}
	return cfg, note
}

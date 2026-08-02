// Package app cablea config → tema → TUI. Es la única pieza autorizada a
// importar tanto internal/config como internal/tui: root.go no sabe que
// existe config.Load, y config no sabe que existe Bubble Tea (§6.1).
package app

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"

	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/theme"
	"github.com/MichiTrader/ishakat/internal/tui"
	"github.com/MichiTrader/ishakat/internal/xdg"
)

// Run carga la configuración, resuelve el tema y arranca el programa de
// Bubble Tea en modo inline. version es la versión compilada de ishakat
// (variable de main, inyectada por -ldflags en builds de release).
func Run(version string) int {
	cfg, err := config.Load(config.Options{UserPath: xdg.ConfigFile()})
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ Error de configuración: %v\n", err)
		return 1
	}

	// The TUI receives the directory already in display form. Deciding what a
	// path looks like to a human needs the home directory and the host's
	// separator, which is filesystem knowledge tui must not have (§6.1); all
	// the TUI does with it is fit it into the columns it has.
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	cwd = xdg.Pretty(cwd)

	noTTY := !term.IsTerminal(os.Stdout.Fd())
	cap := theme.Detect(cfg.UI.Color)
	th := theme.Load(cfg.UI.Theme, xdg.ThemesDir())

	// Colour and repertoire are two independent questions about the same
	// terminal (see theme.GlyphSet), so they are resolved side by side and
	// both handed over. Resolving one and forgetting the other is not a
	// hypothetical mistake: the glyph set existed for a whole step without
	// this line, which meant a cp437 console kept being sent block-drawing
	// characters no matter what [ui] glyphs said.
	glyphs := theme.DetectGlyphs(cfg.UI.Glyphs)

	// The catalog is loaded from disk only (§4.4's non-negotiable budget:
	// no network on the critical path), which is why this is safe to call
	// unconditionally before the interface is drawn — RefreshCatalog, the
	// one that goes to the network, is Step 11's background-refresh
	// concern, not this one's.
	snap := LoadCatalog(cfg)

	// A model/provider that fails to resolve is not fatal here the way it
	// is in Headless (headless.go's own step 4): there is no prompt on the
	// command line that would otherwise have nothing to answer, only an
	// interface the user can still open, read /help in, and fix the
	// configuration from without restarting. tui.Options.Engine already
	// documents nil as a supported value for exactly this reason.
	eng, ref, system, warn, buildErr := BuildEngine(cfg, "", version)
	model := ref.Ref
	if buildErr != nil {
		fmt.Fprintf(os.Stderr, "⚠ %v\n", buildErr)
		eng = nil
	}
	if warn != "" {
		fmt.Fprintf(os.Stderr, "⚠ %s\n", warn)
	}
	for _, w := range cfg.Warnings {
		fmt.Fprintf(os.Stderr, "⚠ [%s] %s\n", w.Where, w.Msg)
	}

	root := tui.NewRoot(tui.Options{
		Version: version,
		CWD:     cwd,
		Cfg:     cfg,
		Theme:   th,
		Cap:     cap,
		Glyphs:  glyphs,
		NoTTY:   noTTY,
		// battery_saver = "auto" (the default) means "6fps on Termux", not "6fps
		// literally everywhere": without this, every desktop session with no
		// override would have read the same false that a phone should, and the
		// key would have had no effect for the one host it names.
		Termux:     xdg.IsTermux(),
		Engine:     eng,
		Model:      model,
		System:     system,
		Catalog:    &snap.Catalog,
		Alias:      cfg.Alias,
		Favorites:  cfg.Favorites.List,
		PreferFree: cfg.Catalog.PreferFree,
	})

	p := tea.NewProgram(root)
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "✗ Error ejecutando la interfaz: %v\n", err)
		return 1
	}
	return 0
}

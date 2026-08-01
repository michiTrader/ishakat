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

	root := tui.NewRoot(tui.Options{
		Version: version,
		CWD:     cwd,
		Cfg:     cfg,
		Theme:   th,
		Cap:     cap,
		Glyphs:  glyphs,
		NoTTY:   noTTY,
	})

	p := tea.NewProgram(root)
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "✗ Error ejecutando la interfaz: %v\n", err)
		return 1
	}
	return 0
}

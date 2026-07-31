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

	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}

	noTTY := !term.IsTerminal(os.Stdout.Fd())
	cap := theme.Detect(cfg.UI.Color)
	th := theme.Load(cfg.UI.Theme, xdg.ThemesDir())

	root := tui.NewRoot(tui.Options{
		Version: version,
		CWD:     cwd,
		Cfg:     cfg,
		Theme:   th,
		Cap:     cap,
		NoTTY:   noTTY,
	})

	p := tea.NewProgram(root)
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "✗ Error ejecutando la interfaz: %v\n", err)
		return 1
	}
	return 0
}

// themestore.go implements tui.ThemeStore (internal/tui/theme.go's own
// interface) over config.SetTheme — the same "only internal/app touches
// internal/config's write path" rule evolvestore.go's fileEvolveStore
// already follows for its own config write (Decay -> config.SetEvolveMode).
package app

import (
	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/tui"
)

// fileThemeStore is the concrete tui.ThemeStore backing every real run.
// Unlike fileEvolveStore it carries no state of its own — config.SetTheme
// already knows the one path (xdg.ConfigFile()) it writes to — so the
// zero value is always ready to use, and app.go wires a bare &fileThemeStore{}.
type fileThemeStore struct{}

var _ tui.ThemeStore = (*fileThemeStore)(nil)

func (fileThemeStore) Save(name string) error {
	return config.SetTheme(name)
}

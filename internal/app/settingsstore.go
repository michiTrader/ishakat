// settingsstore.go implements tui.SettingsStore (internal/tui/settingscmd.go's
// own interface) over config.SetSetting — the same "only internal/app
// touches internal/config's write path" rule themestore.go's fileThemeStore
// already follows for its own config write (Save -> config.SetTheme).
package app

import (
	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/tui"
)

// fileSettingsStore is the concrete tui.SettingsStore backing every real
// run. Like fileThemeStore it carries no state of its own — config.SetSetting
// already knows the one path (xdg.ConfigFile(), via readRawConfigTOML/
// writeRawConfigTOML) it writes to — so the zero value is always ready to
// use, and app.go wires a bare &fileSettingsStore{}.
type fileSettingsStore struct{}

var _ tui.SettingsStore = (*fileSettingsStore)(nil)

func (fileSettingsStore) Set(key, value string) error {
	return config.SetSetting(key, value)
}

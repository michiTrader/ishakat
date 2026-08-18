package tui

import "github.com/MichiTrader/ishakat/internal/config"

// Map es el keymap del TUI, cargado desde config.Keys (§13). Vive como
// strings comparados contra tea.KeyPressMsg.String() en vez de un tipo con
// máscaras de bits: es exactamente lo que la config ya guarda, y comparar
// contra un string es lo que hace el resto del ecosistema Bubble Tea.
type Map struct {
	Submit      string
	Newline     string
	Cancel      string
	Quit        string
	ClearScreen string
	ModelPicker string
	ModelCycle  string
	ThemePicker string
	HistoryPrev string
	HistoryNext string
	CopyLast    string

	// ToggleFold folds/unfolds the fenced code block closest to the cursor
	// (codeblock.go, §17 2026-08-18 "code blocks fill the terminal" entry).
	// It is deliberately not ctrl+o: that chord is already reserved for
	// ModelCycle ("cycle favorites", §4/§9.7 — documented and configurable
	// since Step 10, even though no key handler implements it yet), and
	// claiming it here would collide with that still-pending feature the
	// moment someone finishes it. ctrl+r was free across every file this
	// package's own default keymap, defaults.toml and bubbles/v2's textarea
	// bindings all touch (see this constant's own test).
	ToggleFold string
}

// defaultMap es la red de seguridad si la configuración llega con teclas
// vacías: nunca dejamos al usuario sin forma de salir o de enviar.
var defaultMap = Map{
	Submit:      "enter",
	Newline:     "ctrl+j",
	Cancel:      "esc",
	Quit:        "ctrl+c",
	ClearScreen: "ctrl+l",
	ModelPicker: "ctrl+p",
	ModelCycle:  "ctrl+o",
	ThemePicker: "ctrl+t",
	HistoryPrev: "up",
	HistoryNext: "down",
	CopyLast:    "ctrl+y",
	ToggleFold:  "ctrl+r",
}

// NewMap construye el keymap desde la configuración cargada, rellenando con
// el default cualquier campo que haya quedado vacío.
func NewMap(k config.Keys) Map {
	m := Map{
		Submit:      or(k.Submit, defaultMap.Submit),
		Newline:     or(k.Newline, defaultMap.Newline),
		Cancel:      or(k.Cancel, defaultMap.Cancel),
		Quit:        or(k.Quit, defaultMap.Quit),
		ClearScreen: or(k.ClearScreen, defaultMap.ClearScreen),
		ModelPicker: or(k.ModelPicker, defaultMap.ModelPicker),
		ModelCycle:  or(k.ModelCycle, defaultMap.ModelCycle),
		ThemePicker: or(k.ThemePicker, defaultMap.ThemePicker),
		HistoryPrev: or(k.HistoryPrev, defaultMap.HistoryPrev),
		HistoryNext: or(k.HistoryNext, defaultMap.HistoryNext),
		CopyLast:    or(k.CopyLast, defaultMap.CopyLast),
		ToggleFold:  or(k.ToggleFold, defaultMap.ToggleFold),
	}
	return m
}

func or(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

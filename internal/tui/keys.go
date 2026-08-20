package tui

import (
	"strings"

	"github.com/MichiTrader/ishakat/internal/config"
)

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

	// QuitRepeat is how many times Quit must be pressed inside the grace
	// window to actually exit (§7.4, RC-1). 1 quits on the first press;
	// 2 is the shipped double-press; N counts presses. 0 is treated as
	// unset and filled from the default (2).
	QuitRepeat int
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
	QuitRepeat:  2,
}

// NewMap construye el keymap desde la configuración cargada, rellenando con
// el default cualquier campo que haya quedado vacío.
//
// Quit is special (RC-1): tea.KeyPressMsg.String() is one keystroke, so a
// multi-word value like "ctrl+c ctrl+c" can never match. validateKeys
// already rewrites that form, but NewMap does the same last-resort parse
// so a Map built without going through Load still works.
func NewMap(k config.Keys) Map {
	quit, repeat := normalizeQuitBinding(k.Quit, k.QuitRepeat)
	m := Map{
		Submit:      or(k.Submit, defaultMap.Submit),
		Newline:     or(k.Newline, defaultMap.Newline),
		Cancel:      or(k.Cancel, defaultMap.Cancel),
		Quit:        or(quit, defaultMap.Quit),
		ClearScreen: or(k.ClearScreen, defaultMap.ClearScreen),
		ModelPicker: or(k.ModelPicker, defaultMap.ModelPicker),
		ModelCycle:  or(k.ModelCycle, defaultMap.ModelCycle),
		ThemePicker: or(k.ThemePicker, defaultMap.ThemePicker),
		HistoryPrev: or(k.HistoryPrev, defaultMap.HistoryPrev),
		HistoryNext: or(k.HistoryNext, defaultMap.HistoryNext),
		CopyLast:    or(k.CopyLast, defaultMap.CopyLast),
		ToggleFold:  or(k.ToggleFold, defaultMap.ToggleFold),
		QuitRepeat:  repeat,
	}
	return m
}

// normalizeQuitBinding returns a single chord and a press count. A
// multi-word quit whose tokens are all the same chord is treated as that
// chord pressed N times (the legacy "ctrl+c ctrl+c" form). An unset
// repeat (0) becomes the shipped default of 2; a negative or absurd
// value is clamped to the default rather than disabling quit.
func normalizeQuitBinding(quit string, repeat int) (string, int) {
	parts := strings.Fields(quit)
	if len(parts) >= 2 {
		first := parts[0]
		same := true
		for _, p := range parts[1:] {
			if p != first {
				same = false
				break
			}
		}
		if same {
			quit = first
			if repeat == 0 {
				repeat = len(parts)
			}
		}
		// A mixed sequence is left as-is so handleGlobalKey will not
		// match it; validateKeys is the path that rejects it loudly.
	}
	if repeat <= 0 || repeat > 10 {
		repeat = defaultMap.QuitRepeat
	}
	return quit, repeat
}

func or(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

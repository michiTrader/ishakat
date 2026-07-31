package tui

import (
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
)

// NewInput arma el textarea de entrada con los valores que el resto del PLAN
// da por sentados: una sola línea de altura por defecto (crece con
// DynamicHeight hasta MaxHeight), sin números de línea, prompt vacío porque
// el prefijo "> " lo dibuja la caja de layout.go, no el widget.
func NewInput() textarea.Model {
	ta := textarea.New()
	ta.Placeholder = ""
	ta.ShowLineNumbers = false
	ta.Prompt = ""
	ta.CharLimit = 0
	ta.DynamicHeight = true
	ta.MinHeight = 1
	ta.MaxHeight = 6
	ta.SetHeight(1)
	// Usamos el cursor real de la terminal, no uno virtual dibujado en el
	// texto: root.go lo expone vía tea.View.Cursor (§7.2), que es lo único
	// que funciona bien en modo inline sobre SSH y en Termux.
	ta.SetVirtualCursor(false)
	ta.Focus()
	return ta
}

// SetInputWidth ajusta el ancho del textarea al espacio disponible dentro de
// la caja, descontando el prefijo y los bordes si los hay.
func SetInputWidth(ta *textarea.Model, lay Layout) {
	w := lay.ContentWidth() - len([]rune(lay.InputPrefix()))
	if lay.ShowBoxedInput() {
		w -= 2 // bordes redondeados izquierdo y derecho
	}
	if w < 1 {
		w = 1
	}
	ta.SetWidth(w)
}

// InputBox envuelve el textarea con la caja de §9.2/§9.3: bordes redondeados
// completos en modos normal/ancho/estrecho, un simple prefijo en BPMinimo.
func InputBox(lay Layout, boxStyle stylesBoxLike, prefix, value string) string {
	if !lay.ShowBoxedInput() {
		return lay.InputPrefix() + value
	}
	inner := prefix + value
	return boxStyle.RenderBox(inner, lay.ContentWidth())
}

// stylesBoxLike es lo mínimo de theme.Styles que InputBox necesita.
type stylesBoxLike interface {
	RenderBox(content string, width int) string
}

// keyPressString adapta tea.KeyPressMsg a un string comparable contra el
// keymap; existe para que root.go no repita msg.String() por todas partes y
// para poder testear el despacho de teclas sin levantar un tea.Program.
func keyPressString(msg tea.KeyPressMsg) string { return msg.String() }

package tui

import (
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// NewInput builds the entry textarea with the values the rest of the PLAN
// takes for granted: one line tall by default (it grows with DynamicHeight up
// to MaxHeight) and no line numbers.
//
// The "› " prefix is the widget's own prompt rather than something the box
// glues in front of the value. That matters for the cursor: textarea.Cursor()
// reports a position measured inside the widget and already counts its prompt,
// so as long as the widget draws every column to the left of the text, the
// only correction the view has to apply is the origin of the box (see
// InputOrigin). When the prefix was pasted in by the caller instead, the
// widget's idea of column zero and the drawn column zero were different
// places, which is one half of the reported "cursor sits next to the banner"
// bug.
func NewInput(prefix string) textarea.Model {
	ta := textarea.New()
	ta.Placeholder = ""
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.DynamicHeight = true
	ta.MinHeight = 1
	ta.MaxHeight = 6
	ta.SetHeight(1)

	// The widget's default styles hardcode ANSI colours of their own: a
	// background highlight on the cursor line, a filler for the end of the
	// buffer, and colour 7 for the prompt and the blurred text. Colour 7 is
	// "white" on most terminals — which is where the prompt looking bleached
	// on PowerShell came from — and none of it belongs to the widget anyway:
	// §8 says the theme is the only thing that decides colour. So the input is
	// deliberately flat and the box around it provides the only framing.
	st := ta.Styles()
	flat := lipgloss.NewStyle()
	for _, s := range []*textarea.StyleState{&st.Focused, &st.Blurred} {
		s.CursorLine = flat
		s.EndOfBuffer = flat
		s.Prompt = flat
		s.Text = flat
	}
	ta.SetStyles(st)

	SetInputPrefix(&ta, prefix)

	// We use the terminal's real cursor, not a virtual one drawn into the
	// text: root.go exposes it through tea.View.Cursor (§7.2), which is the
	// only thing that behaves well in inline mode over SSH and on Termux.
	ta.SetVirtualCursor(false)
	ta.Focus()
	return ta
}

// SetInputPrefix makes the widget draw prefix in front of the first line and
// an equally wide blank in front of the continuation lines, so a wrapped or
// multi-line prompt stays aligned under its own text.
//
// It has to be re-applied whenever the breakpoint changes, because the prefix
// itself changes width ("› " normally, "›" under 40 columns).
func SetInputPrefix(ta *textarea.Model, prefix string) {
	w := lipgloss.Width(prefix)
	continuation := strings.Repeat(" ", w)
	ta.SetPromptFunc(w, func(info textarea.PromptInfo) string {
		if info.LineNumber == 0 {
			return prefix
		}
		return continuation
	})
}

// SetInputWidth gives the widget the width it can draw into: the inside of the
// box when there is one, the whole content width otherwise. The prompt is not
// subtracted here — textarea.SetWidth already reserves it.
func SetInputWidth(ta *textarea.Model, lay Layout) {
	w := lay.ContentWidth()
	if lay.ShowBoxedInput() {
		w -= 2 // the two rounded vertical borders
	}
	if w < 1 {
		w = 1
	}
	ta.SetWidth(w)
}

// InputBox wraps the rendered widget in the box of §9.2/§9.3: full rounded
// borders in the narrow/normal/wide breakpoints, no box at all in BPMinimo,
// where every column stolen by a border is a column the text needed.
func InputBox(lay Layout, boxStyle stylesBoxLike, view string) string {
	// textarea.View() ends its last line with a newline; keeping it would add
	// a blank row inside the box and push the footer down by one.
	view = strings.TrimRight(view, "\n")
	if !lay.ShowBoxedInput() {
		return view
	}
	return boxStyle.RenderBox(view, lay.ContentWidth())
}

// InputOrigin is where the widget's own (0,0) ends up once InputBox has drawn
// around it, relative to the first row of the input block. With a box, the
// top and left borders push it one cell down and one cell right; without a
// box the widget starts exactly at the origin.
func InputOrigin(lay Layout) (x, y int) {
	if !lay.ShowBoxedInput() {
		return 0, 0
	}
	return 1, 1
}

// stylesBoxLike is the minimum InputBox needs out of theme.Styles.
type stylesBoxLike interface {
	RenderBox(content string, width int) string
}

// keyPressString adapta tea.KeyPressMsg a un string comparable contra el
// keymap; existe para que root.go no repita msg.String() por todas partes y
// para poder testear el despacho de teclas sin levantar un tea.Program.
func keyPressString(msg tea.KeyPressMsg) string { return msg.String() }

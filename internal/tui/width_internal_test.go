package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/MichiTrader/ishakat/internal/theme"
)

// Step 3's closing criterion, from docs/PLAN.md's bitácora entry for it: "se
// ve correcto a 40, 60 y 120 columnas". The bitácora had that flagged as
// pending because it needs a real terminal to eyeball — but "correct" has one
// part that does not need eyes at all: nothing the package itself lays out
// may draw past the column count the terminal actually has. Bubble Tea's
// inline renderer clips instead of wrapping, so an overflowing line does not
// wrap into a second row, it gets silently cut — which reads as "correct" in a
// screenshot and is actually a line the user cannot read the end of. This
// file is the part of the manual check that a test can do, at all three
// widths, through every screen the package draws.
//
// Prose wrapping (chat.go's renderTranscriptLine/renderLiveTurn) is exercised
// on its own in prose_internal_test.go, with messages built specifically to
// force a wrap; the messages here stay short so this file keeps measuring
// what it is named for — the chrome around the transcript — without the two
// concerns tripping over each other in the same assertion.
func TestNoOverflowAtCriticalWidths(t *testing.T) {
	for _, width := range []int{40, 60, 120} {
		t.Run(fmt.Sprintf("%dcols", width), func(t *testing.T) {
			// A deep path is the one input most likely to force a line past
			// its budget: ShortenPath has to give something up, and giving up
			// the wrong thing is exactly how an overflow would show up here.
			m := NewRoot(Options{
				Version: "0.1.0-test",
				CWD:     "~/projects/muy/profundo/anidado/directorio/de/verdad/largo/ishakat",
				Theme:   theme.Load(""),
				Cap:     theme.CapTruecolor,
			})
			var tm tea.Model = m
			tm, _ = tm.Update(tea.WindowSizeMsg{Width: width, Height: 30})
			assertNoOverflow(t, "banner al arrancar", tm, width)

			// A short submission exercises the live turn's box at every stage
			// — just sent, mid-stream (partial text, elapsed counter, the
			// crush animation), and the moment it finishes and folds into the
			// transcript — without also exercising the prose-wrap gap chat.go
			// documents as deferred (see the package comment above).
			tm = typeInto(tm, "hola, esto es una prueba")
			tm, _ = tm.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			assertNoOverflow(t, "turno recién enviado", tm, width)

			for i := 0; i < 3 && tm.(Root).live.active; i++ {
				tm, _ = tm.Update(streamTickMsg{})
				assertNoOverflow(t, "turno en progreso (streaming parcial)", tm, width)
				tm, _ = tm.Update(animTickMsg{})
				assertNoOverflow(t, "turno en progreso (frame de animación)", tm, width)
			}

			for i := 0; i < 2000 && tm.(Root).live.active; i++ {
				tm, _ = tm.Update(streamTickMsg{})
			}
			if tm.(Root).live.active {
				t.Fatal("el turno no terminó: el resto de la prueba quedaría midiendo el estado equivocado")
			}
			assertNoOverflow(t, "transcript tras terminar el turno", tm, width)

			// /help itself is Step 9's slash-command registry; until then the
			// screen it lands on is reached the same way
			// glyphs_internal_test.go reaches it, by setting the mode
			// directly, which is enough to check the screen's own layout.
			r := tm.(Root)
			r.mode = ModeHelp
			assertNoOverflow(t, "pantalla de ayuda", r, width)
		})
	}
}

// assertNoOverflow renders m and fails for any row wider than width. lipgloss
// is used to measure rather than len(), because a styled row carries escape
// sequences and — once glyphs are involved — runes that occupy more or fewer
// terminal cells than they are runes; len() would report a false overflow on
// exactly the rows this package tries hardest to keep legible.
func assertNoOverflow(t *testing.T, where string, m tea.Model, width int) {
	t.Helper()
	content := m.View().Content
	for i, line := range strings.Split(content, "\n") {
		if got := lipgloss.Width(line); got > width {
			t.Errorf("%s @ %dcols: fila %d se desborda (%d columnas): %q", where, width, i, got, line)
		}
	}
}

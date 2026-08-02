package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// The second half of the report: after sending a long single-line message,
// the transcript showed one row of it and nothing else ("no muestra el texto
// completo en sus lineas de abajo"), and typing the line breaks by hand with
// ctrl+j was the only way to read it back. Nothing was lost from the model —
// only from the screen, which is worse than an error because a truncated
// answer still looks like a complete one.
//
// The cause is that Bubble Tea's inline renderer clips a row wider than the
// terminal instead of letting the terminal wrap it, and chat.go wrote the
// message text verbatim. Both bubbles are wrapped now (see wrap.go); this
// test counts characters rather than inspecting rows, because "how many rows
// it took" is a wrapping detail and "every character I sent is on screen" is
// the property that matters.
func TestALongMessageIsWrappedInsteadOfClipped(t *testing.T) {
	// One unbreakable word longer than any terminal tested, then ordinary
	// words: the first forces a break inside a word, the second must break
	// on spaces instead.
	text := strings.Repeat("z", 200) + " " + strings.Repeat("zz ", 40)
	want := strings.Count(text, "z") * 2 // the dummy engine echoes the prompt back as its answer

	for _, width := range []int{120, 60, 40} {
		t.Run(fmt.Sprintf("%dcols", width), func(t *testing.T) {
			var m tea.Model = newVisibleRoot()
			m, _ = m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
			m = playTurn(m, text)

			content := m.View().Content
			for i, line := range strings.Split(content, "\n") {
				if got := lipgloss.Width(line); got > width {
					t.Fatalf("row %d is %d columns wide in a %d-column terminal: %q", i, got, width, stripANSI(line))
				}
			}
			if got := strings.Count(stripANSI(content), "z"); got != want {
				t.Errorf("the frame shows %d of the %d characters sent and echoed back", got, want)
			}
		})
	}
}

// TestTheLiveTurnWrapsWhileItStreams pins the same property for the turn that
// is still arriving: the answer has to be readable as it comes in, not only
// once it is committed to the transcript.
//
// It cannot compare against pendingEchoPos the way an earlier draft of this
// test did: driveEcho resets pendingEchoPos to 0 the instant the turn
// finishes (root.go's finishTurn), so on the very tick that matters most —
// the last one, where the full answer first has to be on screen — that field
// already reads 0 and the comparison is against the wrong number. Tracking
// "how many characters driveEcho has released so far" independently, from
// echoChunkSize, is the only way to know what the screen owes at each tick
// regardless of which side of finishTurn it lands on.
func TestTheLiveTurnWrapsWhileItStreams(t *testing.T) {
	const width = 40
	const n = 300 // strings.Repeat("z", n) below; keep the two in sync

	var m tea.Model = newVisibleRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
	m = typeInto(m, strings.Repeat("z", n))
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	released := 0 // characters driveEcho has handed to the live turn so far
	for tick := 0; tick < n/echoChunkSize+5; tick++ {
		content := m.View().Content
		for row, line := range strings.Split(content, "\n") {
			if got := lipgloss.Width(line); got > width {
				t.Fatalf("tick %d: row %d is %d columns wide in a %d-column terminal: %q", tick, row, got, width, stripANSI(line))
			}
		}

		echoed := released
		if echoed > n {
			echoed = n
		}
		// The user's own message (n characters) is always fully committed
		// above the live turn; the answer contributes however much of it
		// has streamed (or, once finishTurn has run, all of it) on top.
		want := n + echoed
		if got := strings.Count(stripANSI(content), "z"); got != want {
			t.Fatalf("tick %d: %d characters on screen, want %d (released so far: %d, live active: %v)",
				tick, got, want, released, m.(Root).live.active)
		}

		if !m.(Root).live.active {
			return // finishTurn already ran and the screen above was already checked against it
		}
		m, _ = m.Update(streamTickMsg{})
		released += echoChunkSize
	}
	t.Fatal("the turn never finished: the rest of the test would be measuring a stuck state")
}

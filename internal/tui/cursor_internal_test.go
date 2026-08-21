package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/MichiTrader/ishakat/internal/theme"
)

// The reported symptom was a cursor drawn up near the banner instead of inside
// the input box. The cause is that textarea.Cursor() reports a position
// relative to the widget, and the view returned it untouched, so row 0 of the
// widget became row 0 of the whole frame. These tests assert the only thing
// that actually matters to the user: the terminal cursor sits on the cell where
// the next character will appear.

func TestCursorSitsOnTheCellAfterTheTypedText(t *testing.T) {
	for _, width := range []int{80, 60, 44, 32} {
		t.Run(widthName(width), func(t *testing.T) {
			var m tea.Model = newVisibleRoot()
			m, _ = m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
			m = typeInto(m, "hola")

			v := m.View()
			if v.Cursor == nil {
				t.Fatal("ModeChat must expose a terminal cursor")
			}
			lines := strings.Split(v.Content, "\n")
			y := v.Cursor.Position.Y
			if y < 0 || y >= len(lines) {
				t.Fatalf("cursor row %d is outside the %d rendered rows", y, len(lines))
			}
			line := lines[y]
			at := strings.Index(line, "hola")
			if at < 0 {
				t.Fatalf("cursor row %d is %q, which is not the input line", y, line)
			}
			// The cursor belongs one cell past the last typed rune. The row is
			// measured in terminal cells, not bytes: styled text carries escape
			// sequences that occupy no column.
			want := lipgloss.Width(line[:at]) + lipgloss.Width("hola")
			if got := v.Cursor.Position.X; got != want {
				t.Errorf("cursor column = %d, want %d (row %q)", got, want, line)
			}
		})
	}
}

// TestCursorIsNotAtTheTopOfTheFrame is the regression proper: with the banner
// on screen the buggy version reported row 0 or 1, which is where the logo is
// drawn.
func TestCursorIsNotAtTheTopOfTheFrame(t *testing.T) {
	var m tea.Model = newVisibleRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	m = typeInto(m, "x")

	v := m.View()
	if v.Cursor == nil {
		t.Fatal("ModeChat must expose a terminal cursor")
	}
	if !strings.Contains(v.Content, "ishakat 0.0.0-test") {
		t.Fatal("this test needs the banner on screen to be meaningful")
	}
	rows := strings.Count(v.Content, "\n") + 1
	if v.Cursor.Position.Y < rows/2 {
		t.Errorf("cursor row %d is in the top half of a %d-row frame; it should be down in the input box",
			v.Cursor.Position.Y, rows)
	}
}

// TestCursorFollowsTheTranscriptDown checks the offset is recomputed as the
// conversation grows instead of being a constant tuned for one screen. The
// first turn is played out before measuring because committing it also hides
// the banner, which makes the frame shorter for a reason that has nothing to do
// with the cursor.
func TestCursorFollowsTheTranscriptDown(t *testing.T) {
	var m tea.Model = newVisibleRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	m = playTurn(m, "primera pregunta")

	m = typeInto(m, "segunda")
	before := m.View().Cursor.Position.Y
	assertCursorOnInputLine(t, m, "segunda")

	m = playTurn(m, "") // commit what is typed, then keep going
	m = playTurn(m, "tercera pregunta")
	m = typeInto(m, "cuarta")
	after := m.View().Cursor.Position.Y
	assertCursorOnInputLine(t, m, "cuarta")

	if after <= before {
		t.Errorf("as the transcript grows the cursor row must move down: %d then %d", before, after)
	}
}

// TestManyShortTurnsKeepTheFrameWithinTheTerminalHeight is the reported bug,
// reproduced directly: a long-running chat of short messages ("h", "s", "d"…)
// eventually fills a normal-sized terminal, and the input box's cursor was
// seen sliding further down — and eventually off the visible box — with every
// turn once that happened. It never showed up in TestCursorFollowsTheTranscriptDown
// above because that test's terminal (40 rows) never actually fills up over
// its three exchanges; this one uses a realistic 24-row terminal and enough
// turns to guarantee the live region would have outgrown it under the old
// "redraw the whole history every frame" design.
func TestManyShortTurnsKeepTheFrameWithinTheTerminalHeight(t *testing.T) {
	const height = 24
	var m tea.Model = newVisibleRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: height})

	for i, letter := range "hsdghjkqwertyuiopzxcvbnm" { // 24 one-character turns
		m = playTurn(m, string(letter))

		v := m.View()
		rows := strings.Count(v.Content, "\n") + 1
		if rows > height {
			t.Fatalf("turn %d: frame is %d rows tall in a %d-row terminal — the live region outgrew the screen", i, rows, height)
		}
		if v.Cursor == nil {
			t.Fatalf("turn %d: ModeChat must expose a terminal cursor", i)
		}
		if v.Cursor.Position.Y < 0 || v.Cursor.Position.Y >= height {
			t.Fatalf("turn %d: cursor row %d is outside the %d-row terminal (this is exactly the reported drift)", i, v.Cursor.Position.Y, height)
		}
	}
}

// TestCursorStaysInsideTheInputWhileBusy is RC-2's unit half: ModeBusy still
// draws the input box, so the hardware cursor has to sit in that box rather
// than being left on the bottom border. It does not enable typing — that is
// W2 — and overlays that replace the chat frame still hide the cursor.
func TestCursorStaysInsideTheInputWhileBusy(t *testing.T) {
	var m tea.Model = newVisibleRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeInto(m, "hola")
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.(Root).mode != ModeBusy {
		t.Fatal("enter with text typed must enter ModeBusy")
	}

	v := m.View()
	if v.Cursor == nil {
		t.Fatal("ModeBusy must expose a terminal cursor inside the still-drawn input (RC-2)")
	}
	lines := strings.Split(v.Content, "\n")
	y := v.Cursor.Position.Y
	if y < 0 || y >= len(lines) {
		t.Fatalf("cursor row %d is outside the %d rendered rows", y, len(lines))
	}
	line := lines[y]
	if strings.Contains(line, "hola") {
		t.Errorf("cursor row %d still holds the submitted text %q; submit resets the input", y, line)
	}
	if strings.Contains(line, "pensando") {
		t.Errorf("cursor row %d is the busy line %q, not the input", y, line)
	}
	if strings.ContainsAny(line, "└┘") || strings.HasPrefix(strings.TrimLeft(line, " "), "+-") {
		t.Errorf("cursor is on the box bottom border %q — that is RC-2", line)
	}
	if !strings.ContainsAny(line, "›>") {
		t.Errorf("cursor row %d is %q, which is not the input line", y, line)
	}

	help := m.(Root)
	help.mode = ModeHelp
	if help.View().Cursor != nil {
		t.Error("ModeHelp must not expose a chat-input cursor")
	}

	hotkeys := m.(Root)
	hotkeys.mode = ModeHotkeys
	if hotkeys.View().Cursor != nil {
		t.Error("ModeHotkeys must not expose a chat-input cursor (roadmap F3)")
	}

	// W2 is not this change: printable keys in ModeBusy are still swallowed.
	m, _ = m.Update(tea.KeyPressMsg{Text: "x", Code: 'x'})
	if m.(Root).mode != ModeBusy {
		t.Fatal("a printable key in ModeBusy must stay in ModeBusy")
	}
	if got := m.(Root).input.Value(); got != "" {
		t.Errorf("ModeBusy must not feed keystrokes to the textarea (that is W2), got %q", got)
	}
}

func assertCursorOnInputLine(t *testing.T, m tea.Model, typed string) {
	t.Helper()
	v := m.View()
	if v.Cursor == nil {
		t.Fatal("ModeChat must expose a terminal cursor")
	}
	lines := strings.Split(v.Content, "\n")
	y := v.Cursor.Position.Y
	if y < 0 || y >= len(lines) {
		t.Fatalf("cursor row %d is outside the %d rendered rows", y, len(lines))
	}
	if !strings.Contains(lines[y], typed) {
		t.Errorf("cursor row %d is %q, which is not the input line holding %q", y, lines[y], typed)
	}
}

// playTurn types text (if any), submits it and drains the whole simulated
// stream, leaving the model back in ModeChat with one more exchange committed.
func playTurn(m tea.Model, text string) tea.Model {
	m = typeInto(m, text)
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	for i := 0; i < 5000 && m.(Root).live.active; i++ {
		m, _ = m.Update(streamTickMsg{})
	}
	return m
}

// TestHeadRowsCountsEveryRowAboveTheInput pins the measurement the offset is
// built on. head always terminates its blocks with a newline, so the row count
// is the newline count — if that ever stops being true the cursor drifts by
// exactly one row and nobody notices until it is on screen.
func TestHeadRowsCountsEveryRowAboveTheInput(t *testing.T) {
	cases := []struct {
		head string
		want int
	}{
		{head: "", want: 0},
		{head: "one line\n", want: 1},
		{head: "block\nof three\nlines\n", want: 3},
		{head: "block\n\n", want: 2},
	}
	for _, tc := range cases {
		if got := headRows(tc.head); got != tc.want {
			t.Errorf("headRows(%q) = %d, want %d", tc.head, got, tc.want)
		}
	}
}

// newVisibleRoot is newHeadlessRoot's counterpart: it claims to have a TTY so
// the banner is drawn and the cursor offset has something to be wrong about.
func newVisibleRoot() Root {
	root := NewRoot(Options{
		Version: "0.0.0-test",
		CWD:     "~/projects/ishakat",
		Theme:   theme.Load(""),
		// CapTruecolor, not CapNone: this builder claims a fully capable
		// terminal on purpose, because AnimationsOffFor now treats CapNone as
		// "this terminal asked for NO_COLOR/TERM=dumb, turn animations off
		// too" (anim.go, following ui.animations.mode's own "auto" rule). A
		// helper named "visible" is precisely the one that should not trip
		// that rule by accident — TestBusyDoesArmItsTickers below depends on
		// the animation ticker actually arming.
		Cap: theme.CapTruecolor,
	})
	eng, _ := echoEngine(false)
	return withEngine(root, eng)
}

func typeInto(m tea.Model, s string) tea.Model {
	for _, r := range s {
		m, _ = m.Update(tea.KeyPressMsg{Text: string(r), Code: r})
	}
	return m
}

func widthName(w int) string {
	switch {
	case w >= 100:
		return "wide"
	case w >= 60:
		return "normal"
	case w >= 40:
		return "narrow"
	default:
		return "minimum"
	}
}

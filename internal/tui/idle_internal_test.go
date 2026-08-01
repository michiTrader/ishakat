package tui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// §14 asks for zero CPU activity at idle, and Step 3's closing criterion
// repeats it ("el CPU en reposo es 0%"). That is a property of the command
// graph, not a number to measure with top: a Bubble Tea program only wakes up
// when a command hands it a message, so idle costs nothing exactly when no
// command outstanding at idle is a timer.
//
// It was not true. Init armed a 500 ms ticker that re-armed itself forever and
// flipped a boolean no renderer read, so an ishakat sitting at the prompt woke
// up twice a second for the life of the process. These tests are what stops it
// coming back, and they can say so from a sandbox with no TTY, unlike the
// manual measurement the log had been deferring.

// idleBudget is how long a command is given to produce its message before it
// counts as a timer. Every non-timer command in this package is a closure that
// returns immediately; the shortest real ticker is a stream tick at 50 ms, so
// there is a wide gap to sit in and this is not a flaky threshold.
const idleBudget = 25 * time.Millisecond

func TestIdleArmsNoTimers(t *testing.T) {
	m := newVisibleRoot()
	assertNoTimers(t, "Init", m.Init())

	// The window size is the first thing a real program delivers, and it
	// rebuilds the layout, so it is the most likely place for a stray ticker.
	next, cmd := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	assertNoTimers(t, "WindowSizeMsg", cmd)

	// Typing is idle too: a keystroke redraws, it does not start a clock.
	next, cmd = next.Update(tea.KeyPressMsg{Text: "h", Code: 'h'})
	assertNoTimers(t, "KeyPressMsg", cmd)
}

// TestIdleStaysIdleAfterATurn is the other half: the tickers that do exist are
// legitimate while a turn runs, so what matters is that they stop. Step 8's
// closing criterion ("el CPU vuelve a 0% al terminar el turno") is this test
// with a real engine behind it.
func TestIdleStaysIdleAfterATurn(t *testing.T) {
	var m tea.Model = newVisibleRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = playTurn(m, "una pregunta")

	if got := m.(Root).mode; got != ModeChat {
		t.Fatalf("after the turn the mode is %v, want ModeChat", got)
	}

	// Both tickers are still in flight when a turn ends — the last one each
	// scheduled has not fired yet — so the model has to refuse to re-arm them
	// rather than rely on nobody sending them.
	_, cmd := m.Update(animTickMsg{})
	assertNoTimers(t, "animTickMsg after the turn", cmd)

	_, cmd = m.Update(streamTickMsg{})
	assertNoTimers(t, "streamTickMsg after the turn", cmd)

	// And an idle keystroke after a turn must not restart anything either.
	_, cmd = m.Update(tea.KeyPressMsg{Text: "x", Code: 'x'})
	assertNoTimers(t, "KeyPressMsg after the turn", cmd)
}

// TestBusyDoesArmItsTickers guards the test above from passing for the wrong
// reason. Asserting "no timers" everywhere is satisfied perfectly by an
// interface that never animates and never streams, so the streaming path has to
// be shown to still arm the clocks it needs.
func TestBusyDoesArmItsTickers(t *testing.T) {
	var m tea.Model = newVisibleRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = typeInto(m, "hola")

	m, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.(Root).mode != ModeBusy {
		t.Fatal("enter with text typed must enter ModeBusy")
	}
	if n := countTimers(t, cmd); n != 2 {
		t.Errorf("submitting must arm both the stream and animation tickers, got %d timers", n)
	}
}

// assertNoTimers fails if cmd, or anything it batches, is still thinking after
// idleBudget. A command that has not produced its message by then is waiting on
// a clock, which is the thing idle is not allowed to do.
func assertNoTimers(t *testing.T, where string, cmd tea.Cmd) {
	t.Helper()
	if n := countTimers(t, cmd); n != 0 {
		t.Errorf("%s arms %d timer(s); at idle the process must sleep until the user does something (§14)", where, n)
	}
}

// countTimers walks a command tree and counts the leaves that block. Batches
// are flattened because tea.Batch hides its children behind one Cmd, and a
// ticker smuggled into a batch is exactly the bug this file is about.
func countTimers(t *testing.T, cmd tea.Cmd) int {
	t.Helper()
	if cmd == nil {
		return 0
	}

	msg, ok := runCmd(cmd, idleBudget)
	if !ok {
		return 1
	}

	switch msg := msg.(type) {
	case tea.BatchMsg:
		total := 0
		for _, child := range msg {
			total += countTimers(t, child)
		}
		return total
	case nil:
		return 0
	default:
		return 0
	}
}

// runCmd runs cmd off the test goroutine so a ticker cannot hang the suite: the
// budget expiring is the answer, not a failure to get one. The goroutine is
// left to finish on its own — Tick's timer fires and is collected — which is
// why the channel is buffered.
func runCmd(cmd tea.Cmd, budget time.Duration) (tea.Msg, bool) {
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()

	select {
	case msg := <-done:
		return msg, true
	case <-time.After(budget):
		return nil, false
	}
}

package tui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// Bug report: on a real Termux session, ishakat's wordmark stayed on screen
// underneath the first reply instead of being cleared the way head() means it
// to be (only drawn "while there is nothing in the transcript", see its own
// comment). The identical binary cleared it correctly from PowerShell, and
// from Termux over SSH with a Windows terminal doing the actual drawing —
// which is the terminal emulator misdrawing a shrinking frame, not a mistake
// in what render() asks it to draw (see submit()'s comment on clearBanner for
// the mechanism). submit() now asks for a full tea.ClearScreen() on exactly
// the frame that loses the banner's rows, so the fix does not depend on any
// particular terminal's diffing being correct.

// clearBudget mirrors idle_internal_test.go's idleBudget: long enough that an
// immediate command (clearScreenCmd) always beats it, short enough that the
// real stream/animation tickers (50ms and ~83ms at 12fps) always time out
// instead, so their presence in the same batch cannot make this test flaky.
const clearBudget = 25 * time.Millisecond

func TestSubmitClearsTheScreenWhenTheBannerDisappears(t *testing.T) {
	var m tea.Model = newVisibleRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 40})

	if !m.(Root).lay.ShowBanner(m.(Root).cfgBanner) {
		t.Fatal("this test needs the banner on screen to be meaningful")
	}

	m = typeInto(m, "hola")
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	if !batchHasClearScreen(t, cmd) {
		t.Error("submitting the first message must ask for tea.ClearScreen(), or a terminal whose diff mis-clears a shrinking frame will leave the banner on screen")
	}
}

// TestSubmitDoesNotClearWhenThereIsNoBannerToLose is the guard against the
// naive fix (clear on every submit): once the banner is already gone, a full
// clear on every subsequent message would repaint — and flicker — for no
// reason.
func TestSubmitDoesNotClearWhenThereIsNoBannerToLose(t *testing.T) {
	var m tea.Model = newVisibleRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	m = playTurn(m, "primera pregunta")

	m = typeInto(m, "segunda")
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	if batchHasClearScreen(t, cmd) {
		t.Error("a second message, with the banner already gone, must not ask for another tea.ClearScreen()")
	}
}

// TestSubmitDoesNotClearWithoutABannerToBeginWith covers the headless/short
// path: NoTTY never draws the banner in the first place (Layout.ShowBanner),
// so the first submit has nothing to lose either.
func TestSubmitDoesNotClearWithoutABannerToBeginWith(t *testing.T) {
	var m tea.Model = newHeadlessRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	m = typeInto(m, "hola")
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	if batchHasClearScreen(t, cmd) {
		t.Error("with no banner ever drawn (NoTTY), the first submit must not ask for tea.ClearScreen() either")
	}
}

// batchHasClearScreen walks cmd (flattening tea.Batch, exactly like
// idle_internal_test.go's countTimers) looking for a leaf that resolves to
// tea.ClearScreen(). A real ticker leaf (streamTickMsg/animTickMsg) simply
// times out against clearBudget and is treated as "not it" rather than hung.
func batchHasClearScreen(t *testing.T, cmd tea.Cmd) bool {
	t.Helper()
	if cmd == nil {
		return false
	}
	msg, ok := runClearCmd(cmd)
	if !ok {
		return false
	}
	switch msg := msg.(type) {
	case tea.BatchMsg:
		for _, child := range msg {
			if batchHasClearScreen(t, child) {
				return true
			}
		}
		return false
	default:
		return msg == tea.ClearScreen()
	}
}

func runClearCmd(cmd tea.Cmd) (tea.Msg, bool) {
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()

	select {
	case msg := <-done:
		return msg, true
	case <-time.After(clearBudget):
		return nil, false
	}
}

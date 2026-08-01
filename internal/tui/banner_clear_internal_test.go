package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// Bug report: on a real Termux session, ishakat's wordmark stayed on screen
// underneath the first reply instead of being cleared the way head() means it
// to be (only drawn "while there is nothing in the transcript", see
// bannerText's comment). The identical binary cleared it correctly from
// PowerShell, and from Termux over SSH with a Windows terminal doing the
// actual drawing at first — which read as "the terminal emulator misdrawing a
// shrinking frame, not a mistake in what render() asks it to draw", and the
// first fix (tea.ClearScreen on the losing frame) shipped on that theory.
//
// It did not hold: a second Termux session, and then a PowerShell one, both
// still showed the wordmark surviving under the first reply. Read against
// charm.land/bubbletea's own source, ClearScreen does not emit a literal
// "erase display" escape in inline mode either — it sets a "redraw
// everything" flag that the same diff-and-move-cursor machinery still paints
// through. submit() now prints the banner's exact text via tea.Println
// instead — the same door commitEntryCmd already uses to retire finished
// transcript entries into real scrollback, which has never been reported
// misdrawn on any of the hosts above — so the live region simply never
// includes the banner on a frame it needs to shrink out of; it is retired
// through literal newlines before that frame is asked for.

// clearBudget mirrors idle_internal_test.go's idleBudget: long enough that an
// immediate command (the tea.Println leaf) always beats it, short enough that
// the real stream/animation tickers (50ms and ~83ms at 12fps) always time out
// instead, so their presence in the same batch cannot make this test flaky.
const clearBudget = 25 * time.Millisecond

func TestSubmitPrintsTheBannerToScrollbackWhenItDisappears(t *testing.T) {
	var m tea.Model = newVisibleRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 40})

	root := m.(Root)
	if !root.lay.ShowBanner(root.cfgBanner) {
		t.Fatal("this test needs the banner on screen to be meaningful")
	}
	banner := root.bannerText()
	if banner == "" {
		t.Fatal("bannerText() must return the on-screen banner before submit clears it")
	}

	m = typeInto(m, "hola")
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	if !batchHasPrintedLine(t, cmd, banner) {
		t.Error("submitting the first message must tea.Println() the banner's exact text, or a terminal whose diff mis-clears a shrinking frame will leave the wordmark on screen")
	}
}

// TestSubmitDoesNotPrintWhenThereIsNoBannerToLose is the guard against the
// naive fix (print on every submit): once the banner is already gone,
// printing it again on every subsequent message would duplicate it in
// scrollback for no reason.
func TestSubmitDoesNotPrintWhenThereIsNoBannerToLose(t *testing.T) {
	var m tea.Model = newVisibleRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	m = playTurn(m, "primera pregunta")

	m = typeInto(m, "segunda")
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	if batchHasBannerLikeLine(t, cmd) {
		t.Error("a second message, with the banner already gone, must not print it again")
	}
}

// TestSubmitDoesNotPrintWithoutABannerToBeginWith covers the headless/short
// path: NoTTY never draws the banner in the first place (Layout.ShowBanner),
// so the first submit has nothing to retire either.
func TestSubmitDoesNotPrintWithoutABannerToBeginWith(t *testing.T) {
	var m tea.Model = newHeadlessRoot()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	m = typeInto(m, "hola")
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	if batchHasBannerLikeLine(t, cmd) {
		t.Error("with no banner ever drawn (NoTTY), the first submit must not print one either")
	}
}

// batchHasPrintedLine walks cmd (flattening tea.Batch, exactly like
// idle_internal_test.go's countTimers) looking for a leaf that resolves to a
// tea.Println of exactly want. A real ticker leaf (streamTickMsg/animTickMsg)
// simply times out against clearBudget and is treated as "not it" rather than
// hung.
//
// tea.Println's own message type (printLineMessage) is unexported, so its
// body is read back through fmt's "%s" formatting of the message value
// instead of a direct field access — fmt reaches unexported struct fields via
// reflection even across package boundaries, which a type assertion cannot.
func batchHasPrintedLine(t *testing.T, cmd tea.Cmd, want string) bool {
	t.Helper()
	return walkPrintedLines(t, cmd, func(body string) bool { return body == want+"\n" })
}

// batchHasBannerLikeLine is the looser check the negative tests need: they
// only have to prove *some* banner text did not get printed, not match one
// character for character (there is none to compare against once the banner
// never showed). Any printed line long enough, and shaped enough, to be
// mistaken for the wordmark's block is treated as a match; commitEntryCmd's
// chat lines (short, one role/name/timestamp header) do not fit that shape,
// which is what lets this run safely against a batch that also contains a
// real evictOverflow print.
func batchHasBannerLikeLine(t *testing.T, cmd tea.Cmd) bool {
	t.Helper()
	return walkPrintedLines(t, cmd, func(body string) bool {
		return strings.Contains(body, "Escribe para empezar")
	})
}

func walkPrintedLines(t *testing.T, cmd tea.Cmd, match func(string) bool) bool {
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
			if walkPrintedLines(t, child, match) {
				return true
			}
		}
		return false
	default:
		body, ok := printedLineBody(msg)
		return ok && match(body)
	}
}

// printedLineBody extracts tea.Println's payload from msg if msg is its
// internal message type, without depending on that type being exported (it
// is not). "%s" on the message's reflected value renders exactly the struct's
// one string field and nothing else, which %v would also do but %s makes the
// intent (read a string out) explicit at the call site.
func printedLineBody(msg tea.Msg) (string, bool) {
	if fmt.Sprintf("%T", msg) != "tea.printLineMessage" {
		return "", false
	}
	body := fmt.Sprintf("%v", msg)
	return strings.TrimSuffix(strings.TrimPrefix(body, "{"), "}"), true
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

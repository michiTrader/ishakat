package tui

// The reported rendering bugs, as tests that look at a terminal.
//
// These are the W0 regressions. They differ from every other test in this
// package in one respect that is the whole reason the wave exists: they do not
// call Update and View and read the returned string. They run the real program
// against internal/tui/testterm and assert on the *screen* — because a string
// has no height, no cursor, no scrollback and nothing to overflow, and all four
// bugs are about exactly those things.
//
// # Why these roots are not the existing test roots
//
// newTestRoot and newHeadlessRoot both pass NoTTY: true, and layout.go's
// ShowBanner opens with
//
//	if !cfgBanner || l.NoTTY { return false }
//
// so under those roots the banner does not exist, the cursor is nil, and the
// live region is a different shape. That is a reasonable choice for tests about
// state, and it is also the structural reason B1, B2 and the banner duplication
// could ship green: the existing roots cannot represent the situation the bugs
// occur in. So these tests build a TTY root instead.

import (
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/theme"
	"github.com/MichiTrader/ishakat/internal/tui/testterm"
)

// newScreenRoot builds a Root that believes it is on a real terminal, which is
// what makes the banner, the cursor and the overflow path reachable.
//
// The engine double is the ungated echoEngine: it answers with the text it was
// sent, in echoChunkSize chunks, so a test can ask for a long answer by sending
// a long prompt and know exactly what should appear.
func newScreenRoot(t *testing.T) Root {
	t.Helper()
	root := NewRoot(Options{
		Version: "0.0.0-test",
		CWD:     "/home/user/projects/ishakat",
		Theme:   theme.Load(""),
		Cap:     theme.CapNone,
		Glyphs:  theme.GlyphsASCII, // deterministic widths, no font surprises
		NoTTY:   false,             // the point: banner, cursor and overflow exist
	})
	eng, _ := echoEngine(false)
	return withEngine(root, eng)
}

// busyWitness is the word the busy line draws while a turn runs. If that copy
// ever changes this constant has to change with it, which is why
// TestTheEchoEngineActuallyAnswers checks that an answer actually arrives
// instead of trusting the wait.
const busyWitness = "pensando"

// askAndWait sends a prompt the way a user would — typed, then Enter — and
// waits for the turn to actually finish.
//
// Waiting for the state rather than for quiet output is essential, and the
// first version of these tests got it wrong. The stream drains on a 50ms
// tea.Tick and the renderer diffs, so an unchanged frame writes nothing at all:
// output goes quiet between ticks while the turn is still running. Settling
// alone produced a screen frozen on "pensando 0.0s" and made every assertion
// below vacuous — caught by TestTheEchoEngineActuallyAnswers, which exists for
// exactly this.
//
// The busy line is the witness: on screen for the whole turn, gone once the
// answer is committed.
func askAndWait(s *testterm.Session, prompt string) {
	s.Type(prompt)
	s.Enter()
	s.WaitWhile("the turn is still generating (busy line on screen)", func(g *testterm.Grid) bool {
		return g.Contains(busyWitness)
	})
}

// B1: the input box must stay where the user can type into it.
//
// The report was that the box slid further down with every message once the
// screen filled, eventually off the bottom. root.go's evictOverflow is the fix
// that exists for this, and its comment explains why an offset correction could
// never have worked: once the frame is taller than the terminal, some of what
// Bubble Tea thinks it drew has already scrolled off under its own weight, so
// "move up N rows" is wrong by exactly the number of rows over.
//
// The first version of this test searched the last three rows for ">" and
// PASSED with evictOverflow entirely disabled — it was vacuous, because the
// footer's own text satisfied the search regardless of where the box ended up.
// What B1 is actually about is reachability, so the witness is typed text,
// which can only appear where the input really is.
func TestB1InputBoxStaysOnScreen(t *testing.T) {
	const (
		w = 60
		h = 12 // short on purpose: overflow is the situation under test
	)
	s := testterm.Start(t, newScreenRoot(t), w, h)

	for i := 0; i < 4; i++ {
		askAndWait(s, "pregunta numero "+string(rune('1'+i)))
	}

	const marker = "ZZTOP"
	s.Type(marker)

	lines := s.Lines()
	row := -1
	for i := range lines {
		if strings.Contains(lines[i], marker) {
			row = i
		}
	}
	if row < 0 {
		t.Fatalf("B1: after 4 turns on a %dx%d terminal, typed text does not appear "+
			"on screen at all.\nThe input box has been pushed off the bottom: the "+
			"user is typing somewhere they cannot see, which is the reported "+
			"symptom.\n%s", w, h, s.Dump("screen:"))
	}

	// The input box is the bottom of the live region — only the footer sits
	// below it — so the row being typed into has to be near the bottom. A box
	// drifting upward means the frame is growing under the live region.
	if row < h-4 {
		t.Errorf("B1: the input box is on row %d of a %d-row terminal, which is not "+
			"the bottom of the live region.\nevictOverflow did not keep the frame "+
			"inside the screen, so the box has drifted away from where the user "+
			"expects to type.\n%s", row, h, s.Dump("screen:"))
	}
	if _, cy := s.Cursor(); cy != row {
		t.Errorf("B1: the typed text is on row %d but the cursor is on row %d.\n"+
			"The cursor offset no longer matches where the input was actually "+
			"drawn — the exact arithmetic failure evictOverflow's comment "+
			"describes.\n%s", row, cy, s.Dump("screen:"))
	}
}

// B1b: the cursor must stay on the row being typed into. This is the fast,
// mutation-proven form of B1.
//
// Two turns instead of four, because that is all it takes. With evictOverflow
// deliberately disabled this reports marker row 9 against cursor row 11 — the
// cursor two rows below the text it belongs to, exactly the arithmetic failure
// evictOverflow's comment predicts. With the real code it reports 9 and 9.
//
// Kept separate from B1 because it is also far cheaper: under the disabled fix
// the frame grows without bound and the four-turn version takes minutes, while
// this settles either way in under a second. A guard too slow to run is a guard
// that gets skipped.
func TestB1bCursorTracksTheRowBeingTypedInto(t *testing.T) {
	const w, h = 60, 12
	s := testterm.Start(t, newScreenRoot(t), w, h)

	askAndWait(s, "uno")
	askAndWait(s, "dos")

	const marker = "ZZTOP"
	s.Type(marker)

	lines := s.Lines()
	row := -1
	for i := range lines {
		if strings.Contains(lines[i], marker) {
			row = i
		}
	}
	if row < 0 {
		t.Fatalf("B1b: typed text is not on screen at all after 2 turns on %dx%d.\n%s",
			w, h, s.Dump("screen:"))
	}
	if _, cy := s.Cursor(); cy != row {
		t.Errorf("B1b: typed text is on row %d but the cursor is on row %d "+
			"(off by %d).\nThe cursor offset does not match where the input was "+
			"actually drawn, so the user is not shown where they are typing.\n%s",
			row, cy, cy-row, s.Dump("screen:"))
	}
}

// B2: the user's own message must survive a long answer.
//
// Reported as the prompt disappearing once the reply was long enough. The
// interesting part is that it cannot be checked in a string: the message is
// "lost" by scrolling off the top of a screen a string does not have. It can be
// lost two ways and only a terminal tells them apart — scrolled into the
// scrollback, where the user can still find it (correct), or overwritten in
// place and gone (the bug). Counting over screen+scrollback is what
// distinguishes them, which is why testterm.All exists.
func TestB2UserMessageSurvivesALongAnswer(t *testing.T) {
	const marker = "MARCADORUNICO"
	s := testterm.Start(t, newScreenRoot(t), 60, 12)

	// The echo engine answers with the prompt, so a long prompt is a long
	// answer. The marker is at the start so it is the first thing at risk.
	askAndWait(s, marker+strings.Repeat(" relleno", 30))

	if n := s.Count(marker); n == 0 {
		t.Errorf("B2: the user's message %q is nowhere on the terminal after a long answer.\n"+
			"It was not scrolled into the scrollback (where it would still be "+
			"findable) but overwritten in place, which is the reported symptom.\n%s",
			marker, s.Dump("screen+scrollback:"))
	}
}

// B2b: an entry must reach the terminal exactly once.
//
// This assertion was missing and the gap was not hypothetical. The first
// version of B2 asked only "is the message still present", which passed while
// the terminal actually held *three* copies of it. Presence and correctness are
// different questions, and only a count distinguishes them — the same lesson as
// the banner: "at least once" cannot detect a duplicate.
//
// The mechanism is visible in the source. commitEntryCmd (chat.go:296) is
//
//	tea.Println(renderTranscriptLine(...) + "\n")
//
// with no call to Root.fold, while View pushes every frame through fold on the
// way out (view.go calls it "the single point where a restricted terminal gets a
// string it can actually represent"). So an evicted entry is printed unfolded
// while the live region draws the same entry folded — which is why the terminal
// shows the header twice, once as "tú" and once as ASCII "tu". One logical
// entry, two renderings, plus the copy still in the live region.
//
// Deferred: the owner asked to leave B2b for later. W0's job is only to keep
// the pin so the bug cannot go silent. When the count drops to 2 the pin
// fails, which is the signal to promote this back to a hard assertion.
func TestB2bAnEntryIsCommittedExactlyOnce(t *testing.T) {
	const marker = "MARCAUNICA"
	s := testterm.Start(t, newScreenRoot(t), 60, 10)

	askAndWait(s, marker)
	// Enough further turns that the marker's entry is evicted into the real
	// scrollback: eviction is the moment the second rendering appears.
	for i := 0; i < 3; i++ {
		askAndWait(s, "relleno "+string(rune('1'+i))+strings.Repeat(" x", 20))
	}

	// Exactly two: the user's prompt and the assistant's echo of it. The echo
	// double replies with the prompt text, so two is the honest expectation for
	// one exchange.
	if n := s.Count(marker); n > 2 {
		t.Logf("B2b still present (deferred): %q appears %d times, want at most 2.\n%s",
			marker, n, s.Dump("screen+scrollback:"))
		return
	}
	t.Fatal("B2b appears fixed: the entry now reaches the terminal exactly once. " +
		"Promote this test to a hard assertion.")
}

// The two renderings of one entry differ visibly, and that difference is worth
// asserting on its own because it names the cause rather than the symptom. "tú"
// is what renderTranscriptLine produces; "tu" is what foldASCII turns it into.
// A terminal holding both is a terminal that received the same entry down two
// different paths.
//
// Was deferred alongside B2b (same mechanism, same owner instruction). W1's
// RC-3 fix changed evictOverflow to measure frameRowsUnclipped instead of the
// now-clipped head(), which moved eviction earlier relative to the live
// turn's own fold — and that was enough for this particular repro shape (not
// B2b's — see its own still-deferred pin) to stop reproducing across repeated
// runs. Promoted to a hard assertion per this test's own instruction.
func TestCommittedEntriesGoThroughTheSameFoldAsTheLiveRegion(t *testing.T) {
	s := testterm.Start(t, newScreenRoot(t), 60, 10)

	askAndWait(s, "hola")
	for i := 0; i < 3; i++ {
		askAndWait(s, "relleno "+string(rune('1'+i))+strings.Repeat(" x", 20))
	}

	all := strings.Join(s.All(), "\n")
	if folded, unfolded := strings.Contains(all, "| tu "), strings.Contains(all, "| tú "); folded && unfolded {
		t.Errorf("fold-split: both \"| tu \" and \"| tú \" are on the terminal.\n%s",
			s.Dump("screen+scrollback:"))
	}
}

// B3: /clear must clear the scrollback, not just paint over the screen.
//
// This is the one bug whose mechanism is already visible in the source.
// handleGlobalKey's ClearScreen case returns clearScreenCmd, which is
// tea.ClearScreen — and that erases the *display*, ESC[2J. The scrollback is
// erased by ESC[3J, a different sequence. So the transcript is dropped from the
// model and the screen is repainted while every previously committed entry is
// still sitting in the terminal's scrollback, one keypress away.
//
// The grid keeps 2J and 3J distinct precisely so this is checkable; that
// distinction is not a detail of the emulator, it *is* this bug.
//
// Fixed in W1: ctrl+l/`/clear`/`/new` now also send ESC[3J
// (wipeScrollbackCmd, root.go), so the marker must be gone from both the
// screen and the scrollback once /clear has run. Promoted from W0's
// deferred pin to a hard assertion.
func TestB3ClearAlsoClearsScrollback(t *testing.T) {
	const marker = "ANTESDELCLEAR"
	s := testterm.Start(t, newScreenRoot(t), 60, 10)

	// Enough turns that entries have been committed to the real scrollback by
	// evictOverflow: that is the content /clear is supposed to dispose of.
	askAndWait(s, marker)
	for i := 0; i < 4; i++ {
		askAndWait(s, "relleno "+string(rune('1'+i))+strings.Repeat(" x", 20))
	}

	s.Send("\x0c") // ctrl+l

	if n := s.Count(marker); n != 0 {
		t.Fatalf("B3: %q is still on the terminal after /clear (%d occurrences); "+
			"ctrl+l must erase real scrollback (ESC[3J), not just repaint the screen.\n%s",
			marker, n, s.Dump("screen+scrollback:"))
	}
}

// hhmmRE matches the "HH:MM" timestamp renderTranscriptLine stamps onto every
// bubble header (chat.go's ts.Format("15:04")). Two Root sessions started a
// few hundred milliseconds apart normally land in the same minute, but not
// always — the exact failure a real CI run hit: "11:20" vs "11:21", a false
// diff with no bearing on the thing being tested. maskClock exists so tests
// that compare two independently-timestamped renders assert on layout, not
// on which wall-clock minute the test happened to run in.
var hhmmRE = regexp.MustCompile(`\d{2}:\d{2}`)

func maskClock(s string) string { return hhmmRE.ReplaceAllString(s, "--:--") }

// B4: a resize must not corrupt or duplicate what is already on screen.
//
// The constraint was explicit: both render paths must share one logical
// conversation state and avoid duplication, corruption or loss on resize. The
// shrink-shrink-grow-grow cycle is the harsh version — every step re-wraps
// content the terminal already has, and any state the renderer keeps about
// "what is on screen" that survives a width change is wrong from that moment on.
//
// Byte-identical (modulo the HH:MM header timestamp, via maskClock — see its
// own comment) to a fresh render at the same size is the right assertion
// because it is the only one that cannot be satisfied by accident: a duplicated
// line, a lost line and a stale wrap all break it.
func TestB4ResizeCycleDoesNotCorruptTheScreen(t *testing.T) {
	const prompt = "una pregunta con bastante texto para forzar el wrap"

	// Reference: a session that was this size all along.
	fresh := testterm.Start(t, newScreenRoot(t), 60, 14)
	askAndWait(fresh, prompt)
	want := maskClock(strings.Join(fresh.Lines(), "\n"))

	// Subject: the same content through a resize cycle back to the same size.
	got := testterm.Start(t, newScreenRoot(t), 60, 14)
	askAndWait(got, prompt)
	got.Resize(40, 14)
	got.Resize(30, 14)
	got.Resize(45, 14)
	got.Resize(60, 14)

	if g := maskClock(strings.Join(got.Lines(), "\n")); g != want {
		t.Errorf("B4: after 60→40→30→45→60 the screen is not what a fresh render "+
			"at 60x14 produces.\nContent was duplicated, lost or left wrapped for a "+
			"width that no longer applies.\n--- want ---\n%s\n--- got ---\n%s",
			fresh.Dump("fresh:"), got.Dump("after resize cycle:"))
	}
}

// The banner must appear exactly once.
//
// Once is not "at least once": the report was a second banner turning up, and a
// contains-check cannot tell one from two. The banner is also the reason these
// tests need a TTY root at all — ShowBanner returns false under NoTTY, so the
// existing roots could never have caught a duplicate.
//
// It is emitted with tea.Println from startAgentTurn (agentturn.go:112), which
// prints into the real scrollback rather than the redrawn region, so the count
// has to span screen and scrollback both. The version line is drawn by the
// banner and by nothing else, which makes it the countable witness — "ishakat"
// alone also appears in the footer.
func TestBannerAppearsExactlyOnce(t *testing.T) {
	s := testterm.Start(t, newScreenRoot(t), 70, 24) // >= 20 rows: ShowBanner's floor

	// A turn is what retires the banner, so this is the moment a second one
	// could be printed.
	askAndWait(s, "hola")
	askAndWait(s, "otra vez")

	if n := s.Count("0.0.0-test"); n > 1 {
		t.Errorf("banner: the version line appears %d times, want at most 1.\n"+
			"A second banner was printed after the first turn retired it.\n%s",
			n, s.Dump("screen+scrollback:"))
	}
}

// RC-2: the cursor must always resolve to a real position inside the input.
//
// cursorFor adds the input origin plus every row drawn above it, and returns the
// widget's own position untouched if that arithmetic is skipped — which put the
// cursor at row 0, next to the banner. The failure mode worth guarding is not
// just "wrong row" but "off the screen entirely", because a cursor outside the
// grid is invisible and the terminal is then simply not showing the user where
// they are typing.
func TestRC2CursorResolvesInsideTheInput(t *testing.T) {
	const w, h = 60, 20
	s := testterm.Start(t, newScreenRoot(t), w, h)
	s.Type("hola")

	cx, cy := s.Cursor()
	lines := s.Lines()

	if cy < 0 || cy >= len(lines) {
		t.Fatalf("RC-2: cursor row %d is outside the %d-row screen.\n%s",
			cy, len(lines), s.Dump("screen:"))
	}
	if cx < 0 || cx >= w {
		t.Fatalf("RC-2: cursor column %d is outside the %d-column screen.\n%s",
			cx, w, s.Dump("screen:"))
	}
	// The cursor belongs on the row the user is typing into, which is the row
	// holding the text just typed.
	if !strings.Contains(lines[cy], "hola") {
		t.Errorf("RC-2: the cursor is on row %d (%q), which is not the row holding "+
			"the typed text.\nThe user cannot see where they are typing.\n%s",
			cy, lines[cy], s.Dump("screen:"))
	}
}

// TestRC2CursorStaysInsideTheInputWhileBusy is the half of RC-2 that the idle
// test above cannot see. cursorFor used to return nil in every mode except
// ModeChat; Bubble Tea then left the hardware cursor on the input box's
// bottom border the moment a turn started. The witness is the busy line:
// this assertion is only meaningful while "pensando" is still on screen.
//
// The engine is gated so the turn cannot finish under us — an ungated echo
// of a short prompt can drain between WaitFor and Cursor, and the test
// would then pass for the ModeChat path instead of the ModeBusy one.
//
// Typing while busy is W2 and is not enabled here.
func TestRC2CursorStaysInsideTheInputWhileBusy(t *testing.T) {
	const w, h = 60, 20
	root := NewRoot(Options{
		Version: "0.0.0-test",
		CWD:     "/home/user/projects/ishakat",
		Theme:   theme.Load(""),
		Cap:     theme.CapNone,
		Glyphs:  theme.GlyphsASCII,
		NoTTY:   false,
	})
	eng, _ := echoEngine(true)
	s := testterm.Start(t, withEngine(root, eng), w, h)

	s.Type("hola")
	s.Enter()
	s.WaitFor("busy line on screen", func(g *testterm.Grid) bool {
		return g.Contains(busyWitness)
	})

	if !strings.Contains(strings.Join(s.Lines(), "\n"), busyWitness) {
		t.Fatalf("RC-2 busy: the turn finished before the cursor could be checked; "+
			"this test is only meaningful while %q is on screen.\n%s",
			busyWitness, s.Dump("screen:"))
	}

	cx, cy := s.Cursor()
	lines := s.Lines()
	if cy < 0 || cy >= len(lines) {
		t.Fatalf("RC-2 busy: cursor row %d is outside the %d-row screen.\n%s",
			cy, len(lines), s.Dump("screen:"))
	}
	if cx < 0 || cx >= w {
		t.Fatalf("RC-2 busy: cursor column %d is outside the %d-column screen.\n%s",
			cx, w, s.Dump("screen:"))
	}
	line := lines[cy]
	if strings.Contains(line, busyWitness) {
		t.Errorf("RC-2 busy: the cursor is on the busy line %q, not the input.\n%s",
			line, s.Dump("screen:"))
	}
	if strings.Contains(line, "hola") {
		t.Errorf("RC-2 busy: the cursor is on row %d (%q), which still holds the "+
			"submitted text.\n%s", cy, line, s.Dump("screen:"))
	}
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "+-") {
		t.Errorf("RC-2 busy: the cursor is on the box border %q.\n"+
			"That is the reported └──❚────┘: Bubble Tea left it where the last write ended.\n%s",
			line, s.Dump("screen:"))
	}
	if !strings.Contains(line, ">") {
		t.Errorf("RC-2 busy: the cursor is on row %d (%q), which is not the input line.\n"+
			"The cursor was taken away the moment the task started.\n%s",
			cy, line, s.Dump("screen:"))
	}
}

// TestTheEchoEngineActuallyAnswers is a guard for the tests above rather than a
// bug regression. Every one of them depends on the echo double actually
// answering; if it stopped being wired in, or if the wait stopped waiting for
// the right thing, they would all still "pass" by never producing the content
// whose absence they check for.
//
// This is not a theoretical worry. It is what caught askAndWait settling instead
// of waiting for the turn to finish, which had frozen every screen on
// "pensando 0.0s" and made the whole file vacuous.
func TestTheEchoEngineActuallyAnswers(t *testing.T) {
	s := testterm.Start(t, newScreenRoot(t), 60, 20)
	askAndWait(s, "eco")

	if n := s.Count("eco"); n < 2 {
		t.Errorf("the echo double did not answer: %q appears %d times, want at "+
			"least 2 (the prompt and the reply).\nEvery bug regression in this file "+
			"would be vacuous in this state.\n%s", "eco", n, s.Dump("screen+scrollback:"))
	}
}

var _ tea.Model = Root{}

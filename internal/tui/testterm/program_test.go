package testterm

import (
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// These tests calibrate the *driver*, the same way grid_test.go calibrates the
// grid. They use tiny purpose-built models rather than the real Root, because
// the question here is only "does a real tea.Program reach the grid, and does
// input reach the model" — and answering it against the actual TUI would mean a
// failure could be either side's fault.

// echoModel prints whatever it is told, so a test can put known content on the
// screen at a known place.
type echoModel struct {
	lines   []string
	altFlag bool
}

func (m echoModel) Init() tea.Cmd { return nil }

func (m echoModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "enter":
			m.lines = append(m.lines, "submitted")
		case "ctrl+a":
			m.altFlag = !m.altFlag
		default:
			if t := msg.Text; t != "" {
				if len(m.lines) == 0 {
					m.lines = []string{""}
				}
				m.lines[len(m.lines)-1] += t
			}
		}
	}
	return m, nil
}

// View mirrors how the real Root builds its view (internal/tui/view.go): a zero
// tea.View, SetContent for the frame, and AltScreen as a plain bool. That last
// field is the one DESIGN-tui-mode.md §1 calls load-bearing, and building the
// double the same way production does is what keeps the harness honest about it
// — a double that constructed its view differently could pass while the real
// path was broken.
func (m echoModel) View() tea.View {
	var v tea.View
	v.SetContent(strings.Join(m.lines, "\n"))
	v.AltScreen = m.altFlag
	return v
}

// TestProgramOutputReachesTheGrid is the claim the whole harness rests on: a
// real tea.Program, given an ordinary io.Writer, produces bytes that a parser
// can turn back into a screen. It was verified with a disposable probe before
// DESIGN-tui-mode.md was approved; this is that probe, kept.
//
// If this fails, nothing else in W0 means anything.
func TestProgramOutputReachesTheGrid(t *testing.T) {
	s := Start(t, echoModel{lines: []string{"HOLA", "MUNDO"}}, 20, 6)

	if !containsIn(s.Lines(), "HOLA") {
		t.Errorf("HOLA never reached the grid.\n%s", s.Dump("screen:"))
	}
	if !containsIn(s.Lines(), "MUNDO") {
		t.Errorf("MUNDO never reached the grid.\n%s", s.Dump("screen:"))
	}
}

// TestTheLineDisciplineIsModelledNotAssumed pins a fact that cost real time to
// find and would cost it again. Bubble Tea does not emit a fixed newline
// convention: it chooses, in tea.go, with
//
//	mapNl := runtime.GOOS != "windows" && p.ttyInput == nil
//
// Over pipes ttyInput is nil, so the renderer emits a *bare LF* and expects the
// tty's ONLCR to supply the carriage return. A faithful VT parser moves down a
// row on LF and does not touch the column, so without the discipline modelled
// every line after the first lands at the column the previous line ended on —
// which looks exactly like a wrapping bug in the application under test.
//
// This test exists so that an upgrade of Bubble Tea that changes the convention
// fails loudly here, rather than silently producing staircased screens that get
// blamed on the TUI.
func TestTheLineDisciplineIsModelledNotAssumed(t *testing.T) {
	s := Start(t, echoModel{lines: []string{"uno", "dos"}}, 8, 3)

	lines := s.Lines()
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 rows, got %d", len(lines))
	}
	// The claim is specifically about the column, not about presence: "dos"
	// staircased to column 3 still "contains" dos.
	if !strings.HasPrefix(lines[1], "dos") {
		t.Errorf("second row = %q, want it to start at column 0.\n"+
			"A bare LF was treated as pure line-feed, so the row inherited the\n"+
			"previous row's column. Either Start stopped enabling ONLCR or the\n"+
			"renderer changed its newline convention.\n%s",
			lines[1], s.Dump("screen:"))
	}
}

// TestStrictVTIsStillTheGridDefault guards the other half of the same fact. The
// discipline is a property of the *driven session*, not of terminals in
// general, and the grid's own default must stay strict — because B4's
// assertions distinguish LF from CRLF, and a grid that quietly returned to
// column 0 on every LF could not express that difference at all.
func TestStrictVTIsStillTheGridDefault(t *testing.T) {
	g := New(8, 3)
	if _, err := g.Write([]byte("uno\ndos")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if got := g.Lines()[1]; strings.HasPrefix(got, "dos") {
		t.Errorf("a bare LF returned to column 0 on a default grid (row = %q).\n"+
			"ONLCR leaked into the grid default; the LF/CRLF distinction is gone.", got)
	}
}

// TestNoPtyIsNeeded states the Termux consequence as a test rather than a
// comment. The program above ran with a plain io.Pipe and a plain io.Writer: no
// pty, therefore no CGO, therefore nothing new to install. If somebody later
// "fixes" the harness by reaching for a pty library, this test still passes but
// the dependency audit in DESIGN-tui-mode.md §1.2 stops being true — so the
// guard that actually enforces it is the arch test, and this one documents the
// reason.
func TestNoPtyIsNeeded(t *testing.T) {
	s := Start(t, echoModel{lines: []string{"sin pty"}}, 12, 3)
	if !containsIn(s.Lines(), "sin pty") {
		t.Errorf("a program driven over ordinary buffers did not render.\n%s", s.Dump("screen:"))
	}
}

// TestInputIsDecodedByTheRealDecoder is why Send takes raw bytes. Building a
// tea.KeyPressMsg by hand skips the decoder, and the decoder is where RC-1
// lives: a chord is a string compared against what the decoder produced, so a
// test that fabricates messages cannot detect a chord no keypress can produce.
func TestInputIsDecodedByTheRealDecoder(t *testing.T) {
	s := Start(t, echoModel{}, 20, 4)
	s.Type("abc")
	if !containsIn(s.Lines(), "abc") {
		t.Errorf("typed text did not arrive through the input pipe.\n%s", s.Dump("screen:"))
	}
	s.Enter()
	if !containsIn(s.Lines(), "submitted") {
		t.Errorf("carriage return was not decoded as enter.\n%s", s.Dump("screen:"))
	}
}

// The first version of the resize test asserted only the final width and row
// count, and it *passed* when the ordering was deliberately reversed — it was
// vacuous, because both orderings converge on the same final state once things
// settle. The ordering is only observable from inside the interval, so this
// probe samples the grid at the instant the program is told about the new size.
type resizeProbeModel struct {
	observe func() int // grid width, sampled when WindowSizeMsg arrives
}

func (m resizeProbeModel) Init() tea.Cmd { return nil }

func (m resizeProbeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(tea.WindowSizeMsg); ok {
		m.observe()
	}
	return m, nil
}

func (m resizeProbeModel) View() tea.View { return tea.NewView("probe") }

// TestResizeReshapesTheTerminalBeforeTheProgramHears is the ordering B4 depends
// on. The terminal reshapes its cells first and delivers SIGWINCH after, so for
// one interval the already-printed content is sitting there re-wrapped while
// the application still believes the old width. Telling the program first would
// quietly remove that interval, and with it the bug.
func TestResizeReshapesTheTerminalBeforeTheProgramHears(t *testing.T) {
	var (
		mu   sync.Mutex
		seen []int
		s    *Session
	)
	// Start delivers the initial WindowSizeMsg before it returns, so the probe
	// can fire while s is still nil. Reading s under the same mutex that guards
	// seen makes that window explicit instead of a nil dereference, and the
	// initial sample is discarded anyway.
	record := func() int {
		mu.Lock()
		defer mu.Unlock()
		if s == nil {
			return 0
		}
		w := s.widthUnderLock()
		seen = append(seen, w)
		return w
	}
	sess := Start(t, resizeProbeModel{observe: record}, 10, 4)
	mu.Lock()
	s = sess
	seen = nil // drop the initial sizing, keep only the resize under test
	mu.Unlock()

	s.Resize(5, 4)

	mu.Lock()
	got := append([]int(nil), seen...)
	mu.Unlock()

	if len(got) == 0 {
		t.Fatalf("the program was never told about the resize; the harness cannot "+
			"observe the ordering at all.\n%s", s.Dump("screen:"))
	}
	// Every notification must find the terminal already narrowed. If the program
	// is told first it sees the old width, and B4's interval — where printed
	// content sits re-wrapped while the app still believes the old size — has
	// been silently removed from the harness along with the bug.
	for i, w := range got {
		if w != 5 {
			t.Errorf("on WindowSizeMsg #%d the grid was still %d columns wide, want 5.\n"+
				"The program was notified before the terminal reshaped, so the "+
				"interval B4 lives in does not exist in this harness.", i, w)
		}
	}
}

// TestAltScreenIsObservableThroughTheProgram closes the loop that makes W3
// testable at all: AltScreen is a plain bool on the View, and flipping it must
// show up on the grid as a real alternate-screen switch. This is the fact
// DESIGN-tui-mode.md §1 calls load-bearing — fullscreen is a field, not a
// second renderer — checked against a running program rather than read off the
// upstream source.
func TestAltScreenIsObservableThroughTheProgram(t *testing.T) {
	s := Start(t, echoModel{lines: []string{"inline"}, altFlag: true}, 12, 4)
	if !s.Grid().InAltScreen() {
		t.Errorf("View.AltScreen = true did not put the terminal in the alternate screen.\n%s", s.Dump("screen:"))
	}
}

// TestQuitDrainsBeforeAssertionsRun: whatever the program emits on the way out
// — the alt-screen exit, a restored cursor, DECISION-1b's exit transcript — must
// have reached the grid before a test looks. Otherwise the exit-transcript
// assertion in W3 would be a race.
func TestQuitDrainsBeforeAssertionsRun(t *testing.T) {
	s := Start(t, echoModel{lines: []string{"antes de salir"}, altFlag: true}, 20, 4)
	if !s.Grid().InAltScreen() {
		t.Fatal("setup: expected the alternate screen")
	}
	s.Quit()
	if s.Grid().InAltScreen() {
		t.Errorf("still in the alternate screen after Quit returned: the exit was not drained.\n%s", s.Dump("screen:"))
	}
}

// TestQuitIsIdempotent, because t.Cleanup calls it and tests call it too.
func TestQuitIsIdempotent(t *testing.T) {
	s := Start(t, echoModel{lines: []string{"x"}}, 8, 3)
	s.Quit()
	s.Quit()
}

// TestDumpShowsTheScreenWithARuler: every regression assertion reports through
// Dump, so its output has to be worth reading. A rendering bug described in
// prose is nearly unreadable; the same bug shown as the screen the user saw is
// obvious.
func TestDumpShowsTheScreenWithARuler(t *testing.T) {
	s := Start(t, echoModel{lines: []string{"uno", "dos"}}, 8, 3)
	out := s.Dump("etiqueta:")

	for _, want := range []string{"etiqueta:", "+--------+", "|uno", "|dos"} {
		if !strings.Contains(out, want) {
			t.Errorf("Dump() missing %q:\n%s", want, out)
		}
	}
	// Rows are numbered, so a failure can say "row 3" and be checkable.
	if !strings.Contains(out, "  0 |") {
		t.Errorf("Dump() does not number its rows:\n%s", out)
	}
}

// TestSettleDoesNotFailOnAQuietProgram guards the harness against its own
// timing assumptions: a model that draws once and then does nothing must not be
// reported as a hang.
func TestSettleDoesNotFailOnAQuietProgram(t *testing.T) {
	s := Start(t, echoModel{lines: []string{"quieto"}}, 10, 3)
	for i := 0; i < 3; i++ {
		s.Settle()
	}
	if !containsIn(s.Lines(), "quieto") {
		t.Errorf("repeated Settle lost the frame.\n%s", s.Dump("screen:"))
	}
}

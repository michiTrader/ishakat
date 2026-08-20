package testterm

import (
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// Session drives a real tea.Program against a Grid.
//
// "Real" is the whole point and the reason this file exists. The existing tests
// call Update and View themselves, which means they never exercise the
// renderer: not the diffing, not the cursor arithmetic, not the decision about
// how many rows to erase before redrawing. Those are exactly the parts B1 and
// B2 live in. Here the program runs on its own goroutine with an ordinary pipe
// for input and an ordinary writer for output, and the bytes it produces are
// parsed by the grid — so an assertion is about what a terminal would show, not
// about what the model meant to say.
type Session struct {
	t    *testing.T
	grid *Grid
	prog *tea.Program
	in   io.WriteCloser

	// mu guards the grid and the write counter. The program writes from its own
	// goroutine while tests read from theirs, so every touch of either goes
	// through here.
	mu     sync.Mutex
	writes int

	done chan error
	once sync.Once
}

// sink feeds program output into the grid under the lock, counting writes so
// Settle can tell "still drawing" from "finished".
type sink struct{ s *Session }

func (w sink) Write(p []byte) (int, error) {
	w.s.mu.Lock()
	defer w.s.mu.Unlock()
	w.s.writes++
	return w.s.grid.Write(p)
}

// Start runs model in a real program attached to a fresh w×h terminal.
//
// No pty. Input is an io.Pipe and output is a plain writer, which is what
// DESIGN-tui-mode.md §1.2 committed to: a pty library would mean CGO, and CGO
// is precisely the kind of dependency the Termux constraint rules out. The
// consequence is that the harness needs to model the line discipline itself;
// see the SetONLCR call below.
//
// The environment is fixed rather than inherited. A harness that read the
// developer's own TERM would pass on a laptop and fail in CI for reasons having
// nothing to do with the code, and the colour profile would vary per machine —
// so every session gets the same declared terminal.
func Start(t *testing.T, model tea.Model, w, h int) *Session {
	t.Helper()

	pr, pw := io.Pipe()
	s := &Session{
		t:    t,
		grid: New(w, h),
		in:   pw,
		done: make(chan error, 1),
	}

	// Bubble Tea picks its newline convention from whether its input is a real
	// tty: with a pipe it emits bare LF and expects the tty line discipline to
	// add the CR (tea.go's mapNl). Driving over pipes is forced on us, so the
	// discipline has to be modelled on this side. See Grid.onlcr for why this
	// belongs here and not in the parser.
	s.grid.SetONLCR(true)

	s.prog = tea.NewProgram(model,
		tea.WithInput(pr),
		tea.WithOutput(sink{s}),
		tea.WithWindowSize(w, h),
		tea.WithEnvironment([]string{"TERM=xterm-256color", "COLORTERM=truecolor"}),
		// Without this the program installs a SIGINT handler and a test that
		// sends ctrl+c would race the runtime for it.
		tea.WithoutSignalHandler(),
	)

	go func() {
		_, err := s.prog.Run()
		s.done <- err
	}()

	// The program has not necessarily rendered anything yet; callers that need
	// a frame call Settle. Registering cleanup here means an assertion that
	// fails mid-session still tears the program down instead of leaking a
	// goroutine into the rest of the package's tests.
	t.Cleanup(func() { s.Quit() })
	s.Settle()
	return s
}

// Grid exposes the terminal for assertions. Callers must not write to it.
func (s *Session) Grid() *Grid { return s.grid }

// widthUnderLock reads the terminal width while holding the same lock the grid
// is mutated under. It exists so a test can sample the geometry from the
// program's own goroutine — the only place the resize ordering is observable —
// without racing Resize.
func (s *Session) widthUnderLock() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.grid.W
}

// Settle waits until the program stops producing output.
//
// It waits for a quiet period rather than a fixed sleep, because a fixed sleep
// is either slow or flaky and usually both: too short and the frame is caught
// half-drawn, too long and the suite crawls. Quiet is measured on the write
// counter, so it is the program saying it is finished rather than the test
// hoping so.
func (s *Session) Settle() {
	s.t.Helper()
	const (
		quiet    = 30 * time.Millisecond
		deadline = 3 * time.Second
		tick     = 5 * time.Millisecond
	)

	start := time.Now()
	last := s.count()
	stableSince := time.Now()

	for {
		if time.Since(start) > deadline {
			// Not a fatal: a program that keeps drawing may itself be the bug
			// under test (a redraw loop is a plausible RC-3 symptom), and
			// failing here would hide it behind a harness error.
			s.t.Logf("testterm: output never went quiet in %v (%d writes); continuing", deadline, s.count())
			return
		}
		time.Sleep(tick)
		if n := s.count(); n != last {
			last, stableSince = n, time.Now()
			continue
		}
		if time.Since(stableSince) >= quiet {
			return
		}
	}
}

func (s *Session) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writes
}

// WaitFor blocks until pred is true of the terminal, or fails the test.
//
// Settle is not a substitute for this and cannot be. Settle waits for output to
// go quiet, but a program can be quiet and still unfinished: ishakat drains its
// stream on a 50ms tea.Tick, and the renderer diffs frames, so a repaint that
// changes nothing emits *zero bytes*. Output therefore goes quiet in the gap
// between ticks while the turn is still running, and a test that only settled
// would look at a screen still reading "pensando" and conclude the answer never
// came. That is not hypothetical — it made an entire regression file vacuous
// before the engine-wiring guard caught it.
//
// So anything that depends on the program reaching a *state* waits for that
// state. desc is what the caller was waiting for and appears in the failure
// message: a timeout here usually means the state never arrived, which is the
// bug rather than a slow machine, so the message has to be able to say which
// state it was.
func (s *Session) WaitFor(desc string, pred func(*Grid) bool) {
	s.t.Helper()
	const (
		deadline = 5 * time.Second
		tick     = 10 * time.Millisecond
	)
	start := time.Now()
	for {
		s.mu.Lock()
		ok := pred(s.grid)
		s.mu.Unlock()
		if ok {
			return
		}
		if time.Since(start) > deadline {
			s.t.Fatalf("testterm: timed out after %v waiting for %s\n%s",
				deadline, desc, s.Dump("screen when the wait expired:"))
		}
		time.Sleep(tick)
	}
}

// WaitForText waits until sub appears on screen or in the scrollback.
func (s *Session) WaitForText(sub string) {
	s.t.Helper()
	s.WaitFor("text \""+sub+"\"", func(g *Grid) bool { return g.ContainsAnywhere(sub) })
}

// WaitWhile waits until pred stops being true — for waiting out a transient
// state such as the busy indicator.
func (s *Session) WaitWhile(desc string, pred func(*Grid) bool) {
	s.t.Helper()
	s.WaitFor("NOT "+desc, func(g *Grid) bool { return !pred(g) })
}

// Send writes raw bytes to the program's input, as a terminal would.
//
// Raw bytes rather than tea.KeyPressMsg values, deliberately. Constructing the
// message skips the input decoder, and the decoder is where RC-1 lives: a chord
// like "ctrl+c ctrl+c" is a *string* compared against what the decoder
// produced, so a test that builds messages by hand cannot see a chord that no
// real keypress can ever produce. Going through the pipe means the keys are
// decoded by the same code that decodes a user's.
func (s *Session) Send(raw string) {
	s.t.Helper()
	if _, err := io.WriteString(s.in, raw); err != nil && !errors.Is(err, io.ErrClosedPipe) {
		s.t.Fatalf("testterm: writing input %q: %v", raw, err)
	}
	s.Settle()
}

// Type sends printable text one keystroke at a time, which is how it arrives
// from a keyboard and therefore how the editor should be exercised. A single
// bulk write would be decoded as a paste and can take a different path.
func (s *Session) Type(text string) {
	s.t.Helper()
	for _, r := range text {
		s.Send(string(r))
	}
}

// Enter submits the current input.
func (s *Session) Enter() { s.t.Helper(); s.Send("\r") }

// Resize changes the terminal size and tells the program, in that order.
//
// The order is not incidental; it is the honest model of what happens. The
// terminal reshapes its cells first, then delivers SIGWINCH, so for one moment
// the application's idea of the width is stale while whatever it already
// printed sits there re-wrapped by the terminal. That interval is where B4
// lives. Telling the program first would quietly remove the bug from the
// harness.
func (s *Session) Resize(w, h int) {
	s.t.Helper()
	s.mu.Lock()
	s.grid.Resize(w, h)
	s.mu.Unlock()
	s.prog.Send(tea.WindowSizeMsg{Width: w, Height: h})
	s.Settle()
}

// Quit stops the program and waits for it to finish, so that anything it emits
// on the way out — the alt-screen exit, a restored cursor, DECISION-1b's exit
// transcript — has reached the grid before assertions run. Safe to call twice;
// t.Cleanup already calls it once.
func (s *Session) Quit() {
	s.once.Do(func() {
		s.prog.Quit()
		select {
		case err := <-s.done:
			if err != nil && !errors.Is(err, tea.ErrProgramKilled) {
				s.t.Errorf("testterm: program exited with %v", err)
			}
		case <-time.After(3 * time.Second):
			s.prog.Kill()
			s.t.Error("testterm: program did not exit within 3s; killed")
		}
		_ = s.in.Close()
		// The final bytes were written by the program's goroutine before it
		// returned, so they are already in the grid by the time Run finishes.
		// Settling once more keeps the guarantee even if a renderer flushes
		// asynchronously in a future version.
		s.Settle()
	})
}

// Screen returns the visible screen as one string.
func (s *Session) Screen() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.grid.String()
}

// Lines returns the visible rows, right-trimmed.
func (s *Session) Lines() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.grid.Lines()
}

// All returns scrollback followed by the visible screen, which is what the user
// could scroll back to see. B3 and DECISION-1b are both assertions about this.
func (s *Session) All() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.grid.All()
}

// Count reports how many times sub appears across scrollback and screen. It is
// the shape B2 needs: a duplicated message is not a missing one, and only a
// count can tell them apart.
func (s *Session) Count(sub string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.grid.Count(sub)
}

// Cursor returns the cursor position, which RC-2 is entirely about.
func (s *Session) Cursor() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.grid.Cursor()
}

// Dump renders the screen with a frame, row numbers and the scrollback, for
// failure messages.
//
// Every regression assertion in W0 reports through this, so its output has to
// be worth reading. A rendering bug described in prose is nearly unreadable;
// the same bug shown as the screen the user actually saw is obvious at a
// glance, and the row numbers let a failure message say "row 3" and be checked.
func (s *Session) Dump(label string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	var b strings.Builder
	if label != "" {
		b.WriteString(label)
		b.WriteByte('\n')
	}

	rule := "    +" + strings.Repeat("-", s.grid.W) + "+\n"
	b.WriteString(rule)
	for i, ln := range s.grid.Lines() {
		pad := s.grid.W - ansi.StringWidth(ln)
		if pad < 0 {
			pad = 0
		}
		b.WriteString(pad4(i))
		b.WriteString("|")
		b.WriteString(ln)
		b.WriteString(strings.Repeat(" ", pad))
		b.WriteString("|\n")
	}
	b.WriteString(rule)

	cx, cy := s.grid.Cursor()
	b.WriteString("    cursor: ")
	b.WriteString(itoa(cx))
	b.WriteString(",")
	b.WriteString(itoa(cy))
	if s.grid.InAltScreen() {
		b.WriteString("  [alt screen]")
	}
	b.WriteByte('\n')

	if sb := s.grid.Scrollback(); len(sb) > 0 {
		b.WriteString("    scrollback (")
		b.WriteString(itoa(len(sb)))
		b.WriteString(" rows, oldest first):\n")
		for _, ln := range sb {
			b.WriteString("      | ")
			b.WriteString(ln)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// pad4 right-aligns a small row number in a fixed gutter, so the frame stays
// straight and "row 3" in a message is unambiguous.
func pad4(n int) string {
	s := itoa(n)
	for len(s) < 3 {
		s = " " + s
	}
	return s + " "
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	if neg {
		return "-" + string(d)
	}
	return string(d)
}

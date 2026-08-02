package tui

import (
	"context"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/convo"
	"github.com/MichiTrader/ishakat/internal/engine"
	"github.com/MichiTrader/ishakat/internal/theme"
)

// Mode es la máquina de estados completa del Root (§7.1). Una sola variable
// gobierna teclado y render: la alternativa de banderas booleanas produce
// estados imposibles (picker abierto durante streaming con diálogo encima)
// en cuanto el proyecto crece un poco.
type Mode int

const (
	// ModeChat: input enfocado, se puede escribir.
	ModeChat Mode = iota
	// ModeBusy: generando; solo esc y ctrl+c hacen algo.
	ModeBusy
	// ModePicker: overlay de modelos (Paso 10, todavía no implementado).
	ModePicker
	// ModeConfirm: diálogo de cambio con conflicto (Paso 11).
	ModeConfirm
	// ModeHelp: pantalla de ayuda (§9.7).
	ModeHelp
)

// transcriptEntry es una línea ya comprometida al scrollback, mantenida en
// memoria solo para poder re-renderizar la región viva sin perder el
// historial visible durante un resize (en modo inline no hay buffer alterno
// al que volver).
type transcriptEntry struct {
	role string
	name string
	text string
	ts   time.Time
}

// Root es el modelo raíz de Bubble Tea (§7.1). En el Paso 3 no hay cfg
// completo, catálogo ni engine: viven como valores mínimos para arrancar el
// maniquí, y se amplían en los pasos siguientes sin tocar la forma general.
type Root struct {
	version string
	// cwd arrives already in display form (see xdg.Pretty): the TUI never
	// touches the filesystem, it only decides how many columns the string is
	// allowed to use, which is what ShortenPath does at render time.
	cwd string

	mode Mode
	lay  Layout
	keys Map

	styles theme.Styles

	input textarea.Model
	live  liveTurn

	// eng runs the turn (§7.3). Never nil: NewRoot pushes Options.Engine
	// through engineOr, so a Root built without a provider still has
	// something to call — it just fails the handshake with ErrNoProvider.
	eng *engine.Engine

	// buf is the landing zone for the turn currently in flight: the engine's
	// goroutine writes into it, and streamTickMsg drains it on the repaint
	// clock (that decoupling is StreamBuf's whole purpose). Non-nil exactly
	// while a turn is live, which is also how drainStream tells "the tick
	// arrived after the turn already closed" from a real drain.
	buf *engine.StreamBuf

	// cancel closes the live turn's context. It is the entire implementation
	// of esc/ctrl+c per §7.4: the engine's run loop notices ctx.Err() and
	// calls buf.finish(nil, true), so cancellation travels the same path as a
	// normal ending instead of needing a second one.
	cancel context.CancelFunc

	// conv is the history that travels on every request. Root owns it because
	// it is the only component that knows when a turn actually committed —
	// the engine sees one Request at a time and has no memory between them.
	conv convo.Conversation

	// model is the resolved model reference (§4.2's Ref, the human-facing
	// "provider/model", never the wire ID) and system the effective system
	// prompt (§5.2's file-over-inline rule already applied by internal/app).
	model  string
	system string

	transcript []transcriptEntry

	// printedUpTo is how many of transcript's leading entries have already
	// been handed to commitEntryCmd (tea.Println) and therefore live in the
	// terminal's real scrollback. head() only redraws transcript[printedUpTo:]
	// — see evictOverflow, which is what advances this once the live region
	// grows taller than the terminal.
	printedUpTo int

	footer FooterState

	animOffset int

	// pendingQuit es true entre el primer ctrl+c en ModeBusy/ModeChat y la
	// ventana de gracia: el segundo ctrl+c dentro de ese margen sí cierra.
	pendingQuit bool

	quitting bool

	cfgBanner bool
	fps       int

	// animMode and cap are ui.animations.mode and the terminal's colour
	// capability, kept so a resize can re-resolve Layout.AnimationsOff rather
	// than carry forward whatever it was computed as at 80 columns. "auto"
	// turns animations off under BPMinimo (§ui.animations.mode's own rule),
	// and the breakpoint is a function of width — a session that starts wide
	// and gets narrowed past 40 columns has to lose its spinner without a
	// restart, not keep whatever NewRoot decided when the window was still
	// wide.
	animMode string
	cap      theme.Capability

	// footerItems is ui.footer.items: which footer items to draw and in which
	// order. Empty means the default order of footerItemOrder.
	footerItems []string

	help bool
}

// Options son los parámetros de arranque que cmd/ishakat pasa al construir
// el Root. Todo lo que necesita red o disco ya fue resuelto antes de llegar
// aquí: root.go no sabe qué es un proveedor.
type Options struct {
	Version string
	CWD     string
	Cfg     *config.Config
	Theme   theme.Theme
	Cap     theme.Capability

	// Glyphs is which characters this terminal may be given ([ui] glyphs,
	// resolved by theme.DetectGlyphs). The zero value is GlyphsUnicode, so a
	// caller that says nothing keeps the preferred look.
	Glyphs theme.GlyphSet

	NoTTY bool

	// Engine is the turn runner (§7.3), already built over a concrete
	// provider by internal/app.BuildEngine. nil is a supported value and
	// means "no provider configured": every turn then fails immediately with
	// ErrNoProvider instead of panicking (see engineOr in engine.go). That
	// default is what keeps this package's tests — which care about layout,
	// not about the network — buildable from a bare Options.
	Engine *engine.Engine

	// Model is the model reference to show and to send, in §4.2's Ref form
	// ("provider/model" or a bare alias as the user typed it), never the wire
	// ID: the wire ID is the Streamer's business. Empty falls back to the
	// placeholder the banner and footer have shown since Step 3.
	Model string

	// System is the effective system prompt (§5.2), already resolved by
	// internal/app — file wins over inline, and an unreadable file has
	// already been downgraded to a warning there. Empty means the request
	// carries no system message at all, which is a legitimate configuration.
	System string

	// Termux says the host is Termux on Android, which is what
	// battery_saver = "auto" — the documented default — is asking about (§14,
	// docs/PLAN.md's comment on the key). Like NoTTY and Cap it is resolved by
	// internal/app and handed over already answered: tui does not read
	// /proc or the environment itself (§6.1). The zero value is "not a
	// phone", so a caller that says nothing keeps the desktop frame rate.
	Termux bool
}

// NewRoot construye el modelo inicial.
func NewRoot(o Options) Root {
	styles := theme.NewStyles(o.Theme, o.Cap, o.Glyphs)

	// The [ui.animations] block is resolved in anim.go, one documented rule
	// per function. It used to be resolved here, in three lines that between
	// them honoured fps, half of battery_saver ("on" only, never the
	// documented default "auto") and none of mode: the boolean computed for
	// mode ended up in Layout.AnimationsOff, a field nothing in the package
	// read (see anim.go's package comment).
	anim := animationsCfg(o.Cfg)
	fps := FPSFor(anim.FPS, anim.BatterySaver, o.Termux)
	animOff := AnimationsOffFor(anim.Mode, o.Cap, o.NoTTY, ClassifyBreakpoint(80))

	// The layout comes first: the input prefix depends on the breakpoint, and
	// the widget has to be built already knowing which prefix it draws. The
	// glyph set has to be applied here too, not just handed to the styles:
	// Layout is what every component asks about characters, so a layout built
	// without it leaves the whole repertoire mechanism drawing Unicode.
	lay := NewLayout(80, 24, maxWidthOf(o.Cfg), animOff, o.NoTTY).WithGlyphs(o.Glyphs)

	model := o.Model
	if model == "" {
		// Nothing resolved a model for us (a test, or a caller that only
		// wants the frame). The banner and the footer still have to say
		// something, and this is the placeholder they have shown since Step
		// 3 — an actual turn will fail on ErrNoProvider long before the
		// string is sent anywhere.
		model = "auto/coding"
	}

	r := Root{
		version:   o.Version,
		cwd:       o.CWD,
		mode:      ModeChat,
		lay:       lay,
		styles:    styles,
		input:     NewInput(lay.InputPrefix()),
		fps:       fps,
		cfgBanner: o.Cfg == nil || o.Cfg.UI.Banner,
		animMode:  anim.Mode,
		cap:       o.Cap,
		eng:       engineOr(o.Engine),
		model:     model,
		system:    o.System,
	}
	if o.Cfg != nil {
		r.keys = NewMap(o.Cfg.Keys)
		r.footerItems = o.Cfg.UI.Footer.Items
	} else {
		r.keys = defaultMap
	}
	// CWD is deliberately not stored in the footer state: it depends on the
	// terminal width, so it is computed on every render by Root.footerState.
	r.footer = FooterState{Model: model}
	SetInputWidth(&r.input, r.lay)
	return r
}

func maxWidthOf(cfg *config.Config) int {
	if cfg == nil {
		return 0
	}
	return cfg.UI.MaxWidth
}

// Init satisface tea.Model. No hay nada que arrancar de fondo en el Paso 3:
// sin red, sin engine, solo el foco del input.
//
// Nothing here starts a repeating timer, and that is a requirement rather than
// an accident: §14 asks for zero CPU activity at idle, so an idle ishakat has
// to be a process asleep in a read on the terminal, with no clock of its own.
// Every ticker in this file is armed by an event and stops when that event is
// over (see msgs.go).
func (m Root) Init() tea.Cmd {
	return textareaFocusCmd(&m.input)
}

func textareaFocusCmd(ta *textarea.Model) tea.Cmd { return ta.Focus() }

func tickStream() tea.Cmd {
	return tea.Tick(StreamIntervalMS*time.Millisecond, func(time.Time) tea.Msg { return streamTickMsg{} })
}

func tickAnim(fps int) tea.Cmd {
	if fps <= 0 {
		fps = AnimFPS
	}
	d := time.Second / time.Duration(fps)
	return tea.Tick(d, func(t time.Time) tea.Msg { return animTickMsg{t: t} })
}

// Update satisface tea.Model. Every path through it — every case below, not
// just the ones that fall through to submit/finishTurn — has to pass through
// evictOverflow before returning, because the live turn's own text can push
// the frame past the terminal's height on a plain streamTickMsg with no new
// transcript entry involved at all. Doing that check in one wrapper instead
// of at every return statement in updateDispatch is what keeps it from being
// forgotten the next time a case grows a new early return.
func (m Root) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.updateDispatch(msg)
	root, evictCmd := next.(Root).evictOverflow()
	return root, tea.Batch(cmd, evictCmd)
}

// updateDispatch is Update's actual logic, in two layers in this order
// (§7.1): mensajes/teclas globales, y solo al final el switch de modo.
// Invertir el orden hace que esc deje de cancelar con un overlay abierto.
func (m Root) updateDispatch(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Capa 1: mensajes globales, aplican en cualquier modo.
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// The glyph set is carried over explicitly. NewLayout knows only about
		// the terminal's size, so rebuilding on a resize resets everything it
		// does not take as a parameter — and a repertoire that silently
		// reverts to Unicode the first time the window changes is worse than
		// no repertoire at all.
		animOff := AnimationsOffFor(m.animMode, m.cap, m.lay.NoTTY, ClassifyBreakpoint(msg.Width))
		m.lay = NewLayout(msg.Width, msg.Height, m.lay.MaxWidth, animOff, m.lay.NoTTY).
			WithGlyphs(m.lay.Glyphs)
		// Crossing the 40-column breakpoint changes the prefix ("› " to "›"),
		// and the prefix is part of the widget, so it is re-applied before the
		// width: SetInputWidth reserves whatever the prompt currently costs.
		SetInputPrefix(&m.input, m.lay.InputPrefix())
		SetInputWidth(&m.input, m.lay)
		return m, nil

	case animTickMsg:
		if m.mode != ModeBusy {
			return m, nil
		}
		m.animOffset++
		return m, tickAnim(m.fps)

	case streamTickMsg:
		if !m.live.active {
			return m, nil
		}
		return m.drainStream()

	case quitConfirmMsg:
		m.pendingQuit = false
		return m, nil

	case tea.KeyPressMsg:
		if handled, next, cmd := m.handleGlobalKey(msg); handled {
			return next, cmd
		}
	}

	// Capa 2: delega al componente enfocado según el modo.
	switch m.mode {
	case ModeHelp:
		return m.updateHelp(msg)
	case ModeBusy:
		return m.updateBusy(msg)
	default:
		return m.updateChat(msg)
	}
}

// handleGlobalKey resuelve ctrl+c (con la ventana de doble pulsación de
// §7.4) y ctrl+l, que funcionan en cualquier modo. Devuelve handled=false
// para que el switch de modo procese cualquier otra tecla.
func (m Root) handleGlobalKey(msg tea.KeyPressMsg) (bool, tea.Model, tea.Cmd) {
	key := keyPressString(msg)

	switch key {
	case m.keys.Quit:
		if m.mode == ModeBusy {
			// Un solo ctrl+c en ModeBusy cancela el turno, igual que esc:
			// es demasiado fácil perder una respuesta larga por reflejo si
			// un solo ctrl+c pudiera cerrar la aplicación mientras genera.
			next, cmd := m.cancelTurn()
			return true, next, cmd
		}
		if m.pendingQuit {
			m.quitting = true
			return true, m, tea.Quit
		}
		m.pendingQuit = true
		return true, m, tea.Tick(time.Second, func(time.Time) tea.Msg { return quitConfirmMsg{} })

	case m.keys.ClearScreen:
		m.transcript = nil
		m.printedUpTo = 0
		return true, m, clearScreenCmd
	}
	return false, m, nil
}

// updateChat maneja ModeChat: el input tiene el foco, enter envía (en el
// maniquí, dispara el eco simulado), y ctrl+j inserta salto de línea.
func (m Root) updateChat(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		key := keyPressString(msg)
		switch key {
		case m.keys.Cancel:
			return m, nil // nada que cancelar en ModeChat
		case m.keys.Submit:
			text := strings.TrimSpace(m.input.Value())
			if text == "" {
				return m, nil
			}
			return m.submit(text)
		case m.keys.Newline:
			m.input.InsertRune('\n')
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// updateBusy maneja ModeBusy: solo esc/ctrl+c cancelan (ctrl+c ya se atendió
// en la capa global), el resto de teclas se ignora porque el input no tiene
// sentido mientras el maniquí "genera".
func (m Root) updateBusy(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		if keyPressString(key) == m.keys.Cancel {
			return m.cancelTurn()
		}
		return m, nil
	}
	return m, nil
}

// updateHelp maneja ModeHelp: cualquier tecla vuelve a ModeChat, tal como
// documenta §9.7 ("esc volver"), pero se acepta cualquier tecla por
// comodidad ya que no hay nada más que hacer en esta pantalla.
func (m Root) updateHelp(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(tea.KeyPressMsg); ok {
		m.mode = ModeChat
		m.help = false
	}
	return m, nil
}

// submit opens a real turn against the engine (§7.3). Everything above the
// engine call is unchanged from the Step 3 mannequin on purpose: the point of
// that mannequin was that swapping it for the real runner would touch this
// function and nothing else in Update, and that is what happened — the mode
// change, the tickers, the transcript entry and the banner retirement below
// are all exactly as they were.
func (m Root) submit(text string) (tea.Model, tea.Cmd) {
	// head() only draws the startup banner while transcript is empty and no
	// turn is live (see bannerText's comment), and this call is the one frame
	// where that stops being true for the rest of the session: the moment the
	// first transcriptEntry lands, the banner's rows disappear from head()'s
	// output and the live-managed region gets shorter by however tall the
	// banner was.
	//
	// A first attempt at this fix sent tea.ClearScreen on this transition,
	// reasoning that a shrinking frame relies on the inline renderer's own
	// diff to erase the rows that fall outside the new, shorter one. That
	// reasoning was half right and the fix did not hold: read against
	// charm.land/bubbletea's own source, ClearScreen does not emit a literal
	// "erase display" escape in inline mode either — it only sets a "redraw
	// everything" flag that the *same* diff-and-move-cursor machinery then
	// paints through (cursed_renderer.go's clearScreen, ultraviolet's
	// TerminalRenderer.Erase). A second Termux session, and then a PowerShell
	// one, both still showed the wordmark surviving under the first reply
	// after that fix had already shipped — so the bug was never "the diff
	// runs unprotected on a shrink", it is that the diff itself is not
	// trustworthy for a shrink on some emulators, and no flag that still
	// routes through it closes that gap.
	//
	// bannerText, below, sidesteps the diff instead of asking it to behave:
	// tea.Println/insertAbove is the same mechanism evictOverflow already
	// uses to retire finished transcript entries into real scrollback (see
	// commitEntryCmd's comment), and that path has drawn correctly on every
	// host this project has been run on so far — because it scrolls the
	// terminal with literal newlines and a bare CSI L (insert line), not with
	// the cursor-repositioning-and-selective-erase sequences the diff's shrink
	// path depends on. Printing the banner through the exact same door means
	// the live region never shrinks out from under a banner at all: from the
	// very next frame it was never there to erase.
	bannerText := m.bannerText()

	m.transcript = append(m.transcript, transcriptEntry{
		role: "user", name: "tú", text: text, ts: time.Now(),
	})
	// The new entry is not printed to real scrollback here: evictOverflow
	// (run once after every Update, see the wrapper above) is the only place
	// that decides an entry is old enough to leave the live region, and it
	// always keeps the most recent exchange redrawn inline regardless of
	// height — see its comment for why.
	m.input.Reset()
	m.mode = ModeBusy
	m.live.start(m.footer.Model)

	// The user's turn joins the history before the request is built, because
	// the request is the history: Active() has to already contain what we are
	// asking about. The assistant's side is added by finishTurn, once there
	// is something to add.
	m.conv.Add(convo.User(text))

	// context.Background rather than a parent: the program's lifetime is the
	// terminal's, and there is no ctx to inherit here — Bubble Tea does not
	// hand one to Update. Cancellation flows the other way, from cancelTurn.
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.buf = &engine.StreamBuf{}
	m.eng.Start(ctx, engine.Request{
		Model:    m.model,
		Messages: m.conv.Active(),
		System:   m.system,
	}, m.buf)

	// The stream tick always runs — it is what delivers text. The animation
	// tick is the spinner's clock, and ui.animations.mode = "off" (or its
	// "auto" resolution: no TTY, no colour, or under 40 columns, see anim.go)
	// means exactly "do not run that clock" — not "run it and ignore what it
	// draws", which is what happened before AnimationsOff had a reader.
	cmds := []tea.Cmd{tickStream()}
	if !m.lay.AnimationsOff {
		cmds = append(cmds, tickAnim(m.fps))
	}
	if bannerText != "" {
		// The trailing "\n" is the same blank separator line head() used to
		// leave between the banner and whatever came after it (see the "\n\n"
		// after Banner()'s call in head()); tea.Println already supplies the
		// one line break that ends bannerText's own last line.
		cmds = append(cmds, tea.Println(bannerText+"\n"))
	}
	return m, tea.Batch(cmds...)
}

// clearScreenCmd is tea.ClearScreen wrapped as a tea.Cmd, the shape
// handleGlobalKey's /clear (ctrl+l) needs: unlike the banner-retirement case
// above, a user-requested clear has no "print it to scrollback first" option
// — the whole point of ctrl+l is discarding the transcript, not archiving it.
func clearScreenCmd() tea.Msg { return tea.ClearScreen() }

// drainStream moves whatever the engine's goroutine has produced since the
// last tick into the live turn, and decides whether the turn is still running
// (re-arm the tick) or over (close it). It is the only reader of m.buf.
//
// One Drain per tick, never one per token, is the entire point of StreamBuf:
// a provider can hand over 150 deltas a second and this still repaints at
// StreamIntervalMS.
func (m Root) drainStream() (tea.Model, tea.Cmd) {
	if m.buf == nil {
		// A tick that outlived its turn — cancelTurn and finishTurn both
		// clear buf, and there can be one tick already in flight when they
		// run. Dropping it is correct and deliberately silent.
		return m, nil
	}
	chunk, reasoning, usage, done, aborted, err := m.buf.Drain()
	if reasoning != "" {
		m.live.appendReasoning(reasoning)
	}
	if chunk != "" {
		m.live.append(chunk)
	}
	if usage != nil {
		m.live.usage = usage
	}
	if !done {
		return m, tickStream()
	}
	return m.finishTurn(err, aborted)
}

// finishTurn commits the live turn to the transcript and to the history, then
// returns to ModeChat (§7.5). err is the provider's failure (nothing more is
// coming and it was not the user's doing) and aborted the user's cancellation
// — they are mutually exclusive by StreamBuf's own contract, and the engine
// makes cancellation win when both were racing.
func (m Root) finishTurn(err error, aborted bool) (tea.Model, tea.Cmd) {
	body := m.live.body()

	text := body
	switch {
	case aborted:
		text += " [cancelado]"
	case err != nil:
		// The error is shown in the transcript rather than in a transient
		// banner because it belongs to that turn: scrolling back later has to
		// still explain why the answer stops where it does.
		if text != "" {
			text += "\n"
		}
		text += "⚠ " + err.Error()
	}

	m.transcript = append(m.transcript, transcriptEntry{
		role: "assistant", name: m.live.model, text: text, ts: time.Now(),
	})

	// The history keeps the model's actual words — not the "[cancelado]"
	// suffix or the error line, which are presentation. A cancelled turn is
	// still recorded (with Aborted set) because the user saw it and may well
	// refer to it in the next message; a turn that failed before producing
	// anything at all is not, since an empty assistant message would only
	// confuse the next request.
	if body != "" || aborted {
		msg := convo.NewMessage(convo.RoleAssistant, convo.TextBlock(body))
		if r := m.live.reasoning(); r != "" {
			msg.Blocks = append(msg.Blocks, convo.ReasoningBlock(r))
		}
		msg.Model = m.live.model
		msg.Usage = m.live.usage
		msg.Aborted = aborted
		m.conv.Add(msg)
	}

	m.releaseTurn()
	m.live = liveTurn{}
	m.mode = ModeChat
	m.animOffset = 0
	return m, nil
}

// releaseTurn drops everything tied to the turn that just ended. Calling the
// context's cancel even on a turn that finished by itself is required, not
// merely tidy: a CancelFunc that is never called leaks the context and the
// timer goroutine behind it, and go vet says so.
func (m *Root) releaseTurn() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.buf = nil
}

// cancelTurn implements §7.4: esc (or the first ctrl+c in ModeBusy) closes
// the turn's context. The engine's run loop sees ctx.Err() and calls
// buf.finish(nil, true), so the next streamTickMsg drains whatever had
// already arrived and closes through finishTurn — cancellation takes the
// ordinary ending's path instead of a second one of its own.
//
// The turn is marked aborted here rather than waiting for the drain so the
// frame reflects the keypress immediately: the engine's goroutine may take a
// scheduling slice to notice, and a UI that looks unchanged for a tick after
// esc reads as if the key was missed.
func (m Root) cancelTurn() (tea.Model, tea.Cmd) {
	m.live.aborted = true
	if m.cancel != nil {
		m.cancel()
	}
	return m, tickStream()
}

// keepInline is how many of the transcript's most recent entries evictOverflow
// never touches, even while the live region is over height. Two is "the last
// full exchange": one user entry plus the assistant entry that answered it.
// Below that there is nothing left that is safe to call "old" — evicting the
// only entry on screen would print a message that had not, from the user's
// point of view, gone anywhere yet.
const keepInline = 2

// evictOverflow keeps the live-managed region (banner/live turn/input/footer,
// everything render() draws every frame) from ever growing taller than the
// terminal by handing the oldest still-inline transcript entries to
// commitEntryCmd — tea.Println, which prints once and then belongs to the
// terminal's own scrollback instead of to something this package redraws.
//
// This is the actual fix for the reported bug, not cursorFor's offset
// arithmetic: no offset is correct once the thing it is measuring already
// does not fit on screen. A frame taller than the terminal means some of what
// Bubble Tea thinks it drew last time has already scrolled past the top under
// its own weight, and "move the cursor up N rows" stops matching reality by
// exactly the number of rows over — which is why the input box was reported
// sliding further down with every message once the screen filled: N grew by
// one turn's worth every time, forever.
//
// It runs from Update, not View, because printing is an I/O side effect and
// View has to stay pure — cursorFor and render both call it, and calling it
// twice cannot be allowed to print anything twice.
func (m Root) evictOverflow() (Root, tea.Cmd) {
	if m.mode == ModeHelp || m.lay.Height <= 0 {
		return m, nil
	}
	g := m.lay.glyphs()
	width := m.lay.ContentWidth()
	var cmds []tea.Cmd
	for len(m.transcript)-m.printedUpTo > keepInline {
		if strings.Count(m.renderRaw(), "\n")+1 <= m.lay.Height {
			break
		}
		cmds = append(cmds, commitEntryCmd(g, width, m.transcript[m.printedUpTo]))
		m.printedUpTo++
	}
	return m, tea.Batch(cmds...)
}

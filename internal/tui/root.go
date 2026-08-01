package tui

import (
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/config"
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
	cwd     string

	mode Mode
	lay  Layout
	keys Map

	styles theme.Styles

	input textarea.Model
	live  liveTurn

	// pendingEcho es el texto que el maniquí del Paso 3 "responde": sin
	// engine todavía, el input hace eco de lo escrito para poder ver el
	// streaming simulado y las transiciones de modo sin red real.
	pendingEcho    []rune
	pendingEchoPos int

	transcript []transcriptEntry

	footer FooterState

	animOffset int
	blinkOn    bool

	// pendingQuit es true entre el primer ctrl+c en ModeBusy/ModeChat y la
	// ventana de gracia: el segundo ctrl+c dentro de ese margen sí cierra.
	pendingQuit bool

	quitting bool

	cfgBanner bool
	fps       int

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
	NoTTY   bool
}

// NewRoot construye el modelo inicial.
func NewRoot(o Options) Root {
	styles := theme.NewStyles(o.Theme, o.Cap)
	fps := AnimFPS
	if o.Cfg != nil && o.Cfg.UI.Animations.FPS > 0 {
		fps = o.Cfg.UI.Animations.FPS
	}
	batterySaver := o.Cfg != nil && o.Cfg.UI.Animations.BatterySaver == "on"
	if batterySaver && fps > BatterySaverFPS {
		fps = BatterySaverFPS
	}
	animOff := o.Cfg != nil && o.Cfg.UI.Animations.Mode == "off"

	r := Root{
		version:   o.Version,
		cwd:       o.CWD,
		mode:      ModeChat,
		styles:    styles,
		input:     NewInput(),
		fps:       fps,
		cfgBanner: o.Cfg == nil || o.Cfg.UI.Banner,
	}
	if o.Cfg != nil {
		r.keys = NewMap(o.Cfg.Keys)
	} else {
		r.keys = defaultMap
	}
	r.lay = NewLayout(80, 24, maxWidthOf(o.Cfg), animOff, o.NoTTY)
	r.footer = FooterState{Model: "auto/coding", CWD: shortCWD(o.CWD)}
	SetInputWidth(&r.input, r.lay)
	return r
}

func maxWidthOf(cfg *config.Config) int {
	if cfg == nil {
		return 0
	}
	return cfg.UI.MaxWidth
}

func shortCWD(cwd string) string {
	if cwd == "" {
		return ""
	}
	parts := strings.Split(strings.TrimRight(cwd, "/"), "/")
	if len(parts) == 0 {
		return cwd
	}
	return "~/" + parts[len(parts)-1]
}

// Init satisface tea.Model. No hay nada que arrancar de fondo en el Paso 3:
// sin red, sin engine, solo el foco del input y el parpadeo del cursor.
func (m Root) Init() tea.Cmd {
	return tea.Batch(textareaFocusCmd(&m.input), blinkCmd())
}

func textareaFocusCmd(ta *textarea.Model) tea.Cmd { return ta.Focus() }

func blinkCmd() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(time.Time) tea.Msg { return blinkMsg{} })
}

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

// Update satisface tea.Model. El despacho va en dos capas, en este orden
// (§7.1): mensajes/teclas globales, y solo al final el switch de modo.
// Invertir el orden hace que esc deje de cancelar con un overlay abierto.
func (m Root) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Capa 1: mensajes globales, aplican en cualquier modo.
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.lay = NewLayout(msg.Width, msg.Height, m.lay.MaxWidth, m.lay.AnimationsOff, m.lay.NoTTY)
		SetInputWidth(&m.input, m.lay)
		return m, nil

	case blinkMsg:
		m.blinkOn = !m.blinkOn
		return m, blinkCmd()

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
		return m.driveEcho()

	case echoDoneMsg:
		return m.finishTurn()

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
		return true, m, func() tea.Msg { return tea.ClearScreen() }
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

// submit arranca el turno maniquí: sin red, sin engine, el input hace eco de
// lo que escribiste como si fuera la respuesta (§ Paso 3 del PLAN). Aquí es
// donde el Paso 8 conectará el engine real sin tocar el resto del Update.
func (m Root) submit(text string) (tea.Model, tea.Cmd) {
	m.transcript = append(m.transcript, transcriptEntry{
		role: "user", name: "tú", text: text, ts: time.Now(),
	})
	m.input.Reset()
	m.mode = ModeBusy
	m.live.start(m.footer.Model)
	m.pendingEcho = []rune(text)
	m.pendingEchoPos = 0
	return m, tea.Batch(tickStream(), tickAnim(m.fps))
}

// echoChunkSize es cuántos caracteres del eco se liberan por cada drenado del
// StreamBuf (§7.3): imita la cadencia de un streaming real sin necesitar red.
const echoChunkSize = 3

// driveEcho libera el siguiente trozo del eco pendiente y decide si el turno
// sigue vivo o terminó. Sustituye, en forma, al Drain() de engine.StreamBuf
// que llegará en el Paso 8: mismo punto de entrada, misma decisión de
// re-emitir el tick o cerrar el turno.
func (m Root) driveEcho() (tea.Model, tea.Cmd) {
	if m.pendingEchoPos >= len(m.pendingEcho) {
		return m.finishTurn()
	}
	end := m.pendingEchoPos + echoChunkSize
	if end > len(m.pendingEcho) {
		end = len(m.pendingEcho)
	}
	m.live.append(string(m.pendingEcho[m.pendingEchoPos:end]))
	m.pendingEchoPos = end
	return m, tickStream()
}

// finishTurn comete el turno vivo al scrollback (§7.5) y vuelve a ModeChat.
// Si el usuario canceló a mitad de camino, el mensaje queda marcado
// (liveTurn.aborted); en el Paso 3 esto solo se refleja en el texto, porque
// convo.Message.Aborted se conecta recién en el Paso 8.
func (m Root) finishTurn() (tea.Model, tea.Cmd) {
	text := m.live.body()
	if m.live.aborted {
		text += " [cancelado]"
	}
	m.transcript = append(m.transcript, transcriptEntry{
		role: "assistant", name: m.live.model, text: text, ts: time.Now(),
	})
	m.live = liveTurn{}
	m.pendingEcho = nil
	m.pendingEchoPos = 0
	m.mode = ModeChat
	m.animOffset = 0
	return m, nil
}

// cancelTurn implementa §7.4: esc (o el primer ctrl+c en ModeBusy) marca el
// turno como abortado y corta lo que quedaba por "generar"; el próximo
// streamTickMsg en vuelo drena lo restante (que ya es cero) y cierra con
// finishTurn.
func (m Root) cancelTurn() (tea.Model, tea.Cmd) {
	m.live.aborted = true
	m.pendingEcho = m.pendingEcho[:m.pendingEchoPos] // no sigue "generando"
	return m, nil
}

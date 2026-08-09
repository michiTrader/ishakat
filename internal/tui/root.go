package tui

import (
	"context"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/catalog"
	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/convo"
	"github.com/MichiTrader/ishakat/internal/engine"
	"github.com/MichiTrader/ishakat/internal/slash"
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
	// ModePicker: overlay de modelos (§9.4, Paso 10).
	ModePicker
	// ModeConfirm: diálogo de cambio con conflicto (Paso 11).
	ModeConfirm
	// ModeHelp: pantalla de ayuda (§9.7).
	ModeHelp
	// ModeCompact: /compact resumiendo con compact_model (§9.8, Paso 12).
	ModeCompact
	// ModeResume: the §13 /resume overlay, a flat list of previously saved
	// sessions to reopen. Like ModeConfirm it needs no filtering or
	// grouping — sessions have no provider/tier split the model picker
	// does — so it follows confirmDialog's simpler shape rather than
	// Picker's.
	ModeResume
	// ModeToolApprove: Step 16's tool-approval overlay (toolapprove.go).
	// Opens mid-turn — while the agent loop started by startEngineTurn's
	// tools-enabled branch is blocked waiting for a permissions.Decision —
	// so, unlike every other overlay mode, closing it returns to ModeBusy
	// rather than ModeChat: the turn itself is not over, only the pause is.
	ModeToolApprove
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
	//
	// eng is bound to whichever provider m.model pointed at the last time
	// it (or engineFor) rebuilt it — see engineFor's own comment for why a
	// Ref string alone is not enough to keep this in sync on every switch.
	eng *engine.Engine

	// engineFor rebuilds eng for a newly chosen model Ref, or reports why it
	// could not (a disabled/undeclared provider, a bad API key expansion —
	// the same failures NewProvider already names). nil is a supported
	// value (most of this package's own tests, which never touch a real
	// provider): every switch path falls back to reusing the current eng
	// unchanged, exactly Step 10's original behaviour, so a Root built from
	// a bare Options keeps working with no provider at all.
	//
	// This exists because switching models used to mean only replacing the
	// two display strings, m.model and m.footer.Model: internal/app.Run
	// builds exactly one *engine.Engine, over app.default_model's provider,
	// at startup, and no path that changes m.model afterwards (/model, the
	// picker, /resume, the confirm dialog's remedies, /compact's
	// finishSwitchAfterCompact) ever touched eng. A user who picked, say,
	// gemini-direct/... from the picker saw the footer say "gemini-direct"
	// while every request kept going out over whatever HTTP client
	// default_model had bound at boot — silently the wrong provider's
	// base_url and credentials, not a missing one, which is why the
	// handshake failure this produces reads as "no hay proveedor
	// configurado" or a stray 4xx instead of a clean "wrong key" error.
	// engineFor is the fix: every switch site now asks it for a fresh
	// engine bound to the destination Ref before committing the switch.
	engineFor EngineFactory

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

	// commands is the declarative slash-command table (§9.6/§9.7), resolved
	// once at construction. Every /help line and every dropdown row is
	// generated from it, never hand-duplicated.
	commands slash.Registry

	// menu is the autocomplete dropdown's own state (§9.6), recomputed from
	// the input on every keystroke by slashMenuFor.
	menu slashMenu

	// cat is the model catalog snapshot (§4.2). Never touched over the
	// network from this package (§6.1) — it is handed over once, already
	// built by internal/app, and read by both /model's direct resolution
	// and the picker's incremental search.
	cat *catalog.Catalog

	// alias is [alias] from the configuration, keyed case-insensitively —
	// the same map catalog.Resolve/Filter expect through ResolveOptions.
	alias map[string]string

	// preferFree mirrors [catalog].prefer_free (§4.5's bonusFree).
	preferFree bool

	// favorites is [favorites].list, kept in configuration order (Picker
	// turns it into a set for membership tests, but the order itself is
	// what a future ctrl+o rotation would walk).
	favorites []string

	// picker is the Step 10 overlay's own state (§9.4), live only while
	// mode == ModePicker.
	picker Picker

	// confirm is the Step 11 conflict dialog's own state (§9.5), live only
	// while mode == ModeConfirm.
	confirm confirmDialog

	// compactEng runs compact_model's summarization call (§10, Step 12).
	// nil means [app].compact_model never resolved to a working engine —
	// every compaction then skips straight to the drop-oldest fallback,
	// exactly like [compact].strategy = "drop-oldest" (see startCompact).
	// It is deliberately a second, independent *engine.Engine from m.eng:
	// compact_model can name a different provider than the conversation's
	// own model, and internal/app.NewStreamer binds one Engine to exactly
	// one provider at construction time.
	compactEng *engine.Engine

	// compactModel is compact_model's resolved reference (§4.2's Ref),
	// sent as engine.Request.Model on every Summarize call.
	compactModel string

	// compactAuto, compactTriggerPct, compactKeepLastTurns,
	// compactStrategy and compactOnError mirror [compact] verbatim
	// (config.Compact). This package never stores *config.Config itself
	// (see Options.Cfg's comment on NewRoot) — these are the scalars
	// finishTurn's auto-trigger check and startCompact actually need.
	compactAuto          bool
	compactTriggerPct    int
	compactKeepLastTurns int
	compactStrategy      string
	compactOnError       string

	// compact is the Step 12 overlay's own state (§9.8), live only while
	// mode == ModeCompact.
	compact compactState

	// compactCancel closes the in-flight Summarize call's context — the
	// same role m.cancel plays for an ordinary turn (§7.4, see
	// cancelCompact).
	compactCancel context.CancelFunc

	// inputHistory, historyIdx and historyDraft are the up/down input
	// history of Step 13 (§11), implemented in history.go. inputHistory
	// holds every line submit/runRetry has actually sent, oldest first;
	// historyIdx is where the browse cursor sits (len(inputHistory) means
	// "not browsing, showing the live draft"); historyDraft is what the
	// textarea held right before the first up-arrow of a browse, restored
	// by historyNext once the cursor returns past the newest entry.
	inputHistory []string
	historyIdx   int
	historyDraft string

	// recorder persists completed messages (§10, session.go). nil is the
	// supported "do not save" value — [session] save = false, or a store
	// that failed to open — which is also what keeps this package's tests
	// buildable from a bare Options, the same rule Engine and Catalog
	// already follow.
	recorder Recorder

	// sessionWarned and sessionErr carry the first (and only) persistence
	// failure to the next render. A full disk fails on every message, so
	// warning per message would bury the transcript under identical
	// errors; see session.go's recordMessage for why it is reported
	// exactly once.
	sessionWarned bool
	sessionErr    error

	// sessionLister is where /resume gets its menu rows and its full
	// conversations from (§13). nil is the supported "cannot resume"
	// value — [session] save = false, or a store that failed to open —
	// same rule recorder above already follows: runResumeCommand reports
	// that instead of opening a menu with nothing behind it.
	sessionLister SessionLister

	// resume is the §13 /resume overlay's own state, live only while
	// mode == ModeResume.
	resume resumeMenu

	// toolsEnabled mirrors [tools].enabled (config.Tools.Enabled). false is
	// the pre-Step-16 behaviour: startEngineTurn always takes the plain
	// m.eng.Start streaming path, exactly as it always has, and no turn
	// can ever open ModeToolApprove — matching Options.Tools' own
	// zero-value contract (see its comment).
	toolsEnabled bool

	// agentOpts is engine.AgentOptions, already built by internal/app
	// (buildAgentOptions) with Tools/Runner/the configured caps bound —
	// this package never touches internal/tools directly (§6.1: importing
	// it from here would need internal/tools to stay ignorant of the TUI,
	// which TestToolsNoImportaTUI already guards, but there is simply no
	// reason for tui to know what a tool *is* when engine.AgentOptions
	// already names everything a turn needs). Runner is expected to be
	// built over a *permissions.Guard whose Reviewer is bridge (below);
	// binding that Guard is internal/app's job, done once at construction,
	// the same division agentturn.go's buildAgentOptions already draws for
	// the headless path.
	agentOpts engine.AgentOptions

	// toolApprove is Step 16's overlay state (toolapprove.go), live only
	// while mode == ModeToolApprove.
	toolApprove toolApproveDialog

	// agentTurn is startAgentTurn's own bookkeeping (agentturn.go) for
	// whichever tools-enabled turn is currently running through
	// engine.RunAgentTurn, live only between startAgentTurn and
	// finishAgentTurn. m.cancel (shared with the plain streaming path)
	// carries that turn's cancellation, so there is no separate
	// agentCancel field — cancelAgentTurn closes the very same m.cancel.
	agentTurn agentTurnState
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

	// EngineFor rebuilds Engine for a Ref the user just switched to — see
	// Root.engineFor's own comment for the bug this closes (every switch
	// path used to relabel the model without ever rebinding the HTTP
	// client underneath it). nil is a supported value, same reasoning as
	// Engine above: every test in this package, and any caller with
	// nothing wired, keeps the pre-existing "relabel only" behaviour.
	EngineFor EngineFactory

	// Model is the model reference to show and to send, in §4.2's Ref form
	// ("provider/model" or a bare alias as the user typed it), never the
	// wire ID directly: Root resolves the Ref to its WireID (wireModel, in
	// engine.go) against the catalog right before building each
	// engine.Request, since that resolution has to react to live model
	// switches (/model, /resume) and not just this startup value. Empty
	// falls back to the placeholder the banner and footer have shown since
	// Step 3.
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

	// Catalog is the model catalog snapshot (§4.4), already built on disk by
	// internal/app.LoadCatalog — this package never touches the network
	// (§6.1). nil is a supported value: Picker.Active reports false and
	// /model falls back to its "no catalog loaded yet" message instead of
	// panicking on a nil receiver.
	Catalog *catalog.Catalog

	// Alias is [alias] from the configuration, keyed case-insensitively.
	Alias map[string]string

	// Favorites is [favorites].list, in configuration order.
	Favorites []string

	// PreferFree mirrors [catalog].prefer_free.
	PreferFree bool

	// CompactEngine is compact_model's turn runner (§10, Step 12), already
	// built over its own provider by internal/app.BuildEngine — see
	// Root.compactEng's comment on why it is a separate *engine.Engine
	// from Engine above. nil means compaction never calls a model at all,
	// only convo.DropOldest.
	CompactEngine *engine.Engine

	// CompactModel is compact_model's resolved reference, in the same Ref
	// form as Model above.
	CompactModel string

	// CompactAuto, CompactTriggerPct, CompactKeepLastTurns,
	// CompactStrategy and CompactOnError mirror [compact] from the
	// configuration (config.Compact) — internal/app.Run is where they are
	// read off cfg and handed over already resolved, the same rule Model/
	// System/Alias/Favorites above already follow.
	CompactAuto          bool
	CompactTriggerPct    int
	CompactKeepLastTurns int
	CompactStrategy      string
	CompactOnError       string

	// Recorder persists completed messages (§10). nil means this session is
	// not saved — [session] save = false, or a store internal/app could not
	// open — and is a supported value, not a bug: the interface must work
	// on a read-only filesystem the same way it works with no provider
	// configured (see Engine's own comment above).
	//
	// It is an interface rather than a *convo.Store because this package
	// does not decide where sessions live or open them (see session.go's
	// own comment on why the shortcut would not have been caught by a
	// build error).
	Recorder Recorder

	// History is a previously saved conversation to reopen (--resume,
	// /resume, Step 13). Empty is a fresh session. The caller has already
	// read it off disk; the TUI only puts it in the transcript and in the
	// next request's context.
	History []convo.Message

	// SessionLister is /resume's own read side (§13): the menu's rows and
	// the full conversation behind whichever one is chosen. nil means
	// this session cannot list or reopen others — same supported-nil rule
	// Recorder above already follows for the write side, and for the same
	// reason: a store that never opened, or [session] save = false, must
	// not be a reason to refuse to start.
	SessionLister SessionLister

	// ToolsEnabled mirrors [tools].enabled (config.Tools.Enabled). false —
	// the zero value, and every caller before Step 16 — keeps
	// startEngineTurn on the exact plain-streaming path it always took;
	// AgentOptions is then never even read.
	ToolsEnabled bool

	// AgentOptions is engine.AgentOptions, already built by
	// internal/app.buildAgentOptions (the same call runAgentTurnHeadless
	// uses) with the six core tools, a *permissions.Guard whose Reviewer
	// is bound to this Root's own approval bridge, and [tools]'
	// configured caps/budget. tui builds none of this itself: it has no
	// business knowing what a tool is (§6.1's TestToolsNoImportaTUI), only
	// that engine.AgentOptions is the shape RunAgentTurn needs.
	AgentOptions engine.AgentOptions
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

	// A Root built from a bare Options (most of this package's own tests,
	// and any caller that leaves [compact] unset) must never hand
	// convo.PlanCompact keepLastTurns == 0: that reads as "keep nothing",
	// which is a real configuration this package has no way to ask for
	// today (there is no UI for it, and defaults.toml never ships it),
	// and PlanCompact's own boundary arithmetic assumes at least one turn
	// survives. Falling back to the documented default here is the same
	// "test-friendly, still correct" rule model/keys above already apply.
	compactKeepLastTurns := o.CompactKeepLastTurns
	if compactKeepLastTurns <= 0 {
		compactKeepLastTurns = 4
	}
	compactStrategy := o.CompactStrategy
	if compactStrategy == "" {
		compactStrategy = "summarize"
	}
	compactOnError := o.CompactOnError
	if compactOnError == "" {
		compactOnError = "drop-oldest"
	}

	r := Root{
		version:    o.Version,
		cwd:        o.CWD,
		mode:       ModeChat,
		lay:        lay,
		styles:     styles,
		input:      NewInput(lay.InputPrefix()),
		fps:        fps,
		cfgBanner:  o.Cfg == nil || o.Cfg.UI.Banner,
		animMode:   anim.Mode,
		cap:        o.Cap,
		eng:        engineOr(o.Engine),
		engineFor:  o.EngineFor,
		model:      model,
		system:     o.System,
		commands:   slash.Default(),
		cat:        o.Catalog,
		alias:      o.Alias,
		preferFree: o.PreferFree,
		favorites:  o.Favorites,

		compactEng:           o.CompactEngine,
		compactModel:         o.CompactModel,
		compactAuto:          o.CompactAuto,
		compactTriggerPct:    o.CompactTriggerPct,
		compactKeepLastTurns: compactKeepLastTurns,
		compactStrategy:      compactStrategy,
		compactOnError:       compactOnError,

		// Recorder was documented on Options as the persistence seam (§10,
		// Step 13) but never actually assigned here — every real session
		// silently went unsaved from internal/app.Run while the field's own
		// test double (withRecorder, session_internal_test.go) bypassed
		// NewRoot entirely and kept the tests green. See
		// TestOptionsRecorderIsWiredIntoRoot for the regression test that
		// would have caught it.
		recorder: o.Recorder,

		// sessionLister is /resume's read side (§13), the exact mirror of
		// recorder just above for the write side — see
		// TestOptionsSessionListerIsWiredIntoRoot for the regression test
		// this line exists to satisfy.
		sessionLister: o.SessionLister,

		// toolsEnabled/agentOpts are Step 16's fork in startEngineTurn
		// (root.go) — see Options.ToolsEnabled's own comment for why a
		// bare Options (every test in this package predating this step)
		// keeps taking the plain-streaming path unchanged.
		toolsEnabled: o.ToolsEnabled,
		agentOpts:    o.AgentOptions,

		// History (--resume, resume_last, /resume — §13) has to land in two
		// places, not one: m.conv, because it is what the *next* request's
		// Active() call sends to the provider, and m.transcript, because
		// it is what the user actually sees on reopening. Writing only the
		// first would resume the model's memory while showing a blank
		// screen; writing only the second would show old messages that a
		// reply built on top of would then contradict. historyToTranscript
		// (resume.go) is the same conversion finishTurn/submit apply live,
		// applied here in one pass at construction instead of one entry at
		// a time as the conversation unfolds.
		transcript: historyToTranscript(o.History),
	}
	r.conv.Messages = o.History
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
		// ModeToolApprove is included alongside ModeBusy/ModeCompact so the
		// spinner's clock keeps running (silently — renderToolApprove draws
		// no CrushFrame of its own) rather than breaking the re-arm chain:
		// tickAnim is never re-issued once this case falls through to the
		// stop branch below, and nothing else restarts it when
		// resolveToolApprove returns to ModeBusy, which would otherwise
		// leave the rest of that same turn's spinner frozen after the
		// first approval dialog closes.
		if m.mode != ModeBusy && m.mode != ModeCompact && m.mode != ModeToolApprove {
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

	case modelChosenMsg:
		return m.applyModelChosen(msg.Ref)

	case sessionChosenMsg:
		return m.applySessionChosen(msg.ID)

	case CatalogRefreshedMsg:
		return m.applyCatalogRefreshed(msg.Catalog)

	case compactDoneMsg:
		// A stale result from a compaction cancelCompact already closed —
		// its context is cancelled, but the goroutine already past the
		// point of checking ctx.Err() still sends its answer. Dropping it
		// here is the same "outlived its turn" guard drainStream applies
		// to a tick after cancelTurn/finishTurn (see its own comment).
		if m.mode != ModeCompact {
			return m, nil
		}
		return m.finishCompact(msg.summary, msg.err)

	case ToolApproveRequestMsg:
		// A tools-enabled turn can pause more than once (one dialog per
		// ask-tier tool call), so unlike compactDoneMsg's own stale-result
		// guard there is no "this can only ever be stale" check here: any
		// agent turn currently in ModeBusy is a legitimate turn that may
		// legitimately ask again.
		if m.mode != ModeBusy {
			return m, nil
		}
		return m.openToolApprove(msg)

	case agentTurnDoneMsg:
		// Same "outlived its turn" reasoning as compactDoneMsg's own
		// guard: cancelAgentTurn already moved mode back to ModeBusy (or
		// this could be a message from an entirely unrelated bare
		// engine.Start turn that happens to be running instead), and the
		// only mode a genuine agentTurnCmd result can still usefully land
		// in is ModeBusy — never ModeToolApprove, since that would mean
		// RunAgentTurn returned *and* a dialog it should have been
		// blocked behind is still open, which cancelAgentTurn's own
		// contract does not allow.
		if m.mode != ModeBusy {
			return m, nil
		}
		return m.finishAgentTurn(msg.result, msg.err)

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
	case ModePicker:
		return m.updatePicker(msg)
	case ModeConfirm:
		return m.updateConfirm(msg)
	case ModeCompact:
		return m.updateCompact(msg)
	case ModeResume:
		return m.updateResumeMenu(msg)
	case ModeToolApprove:
		return m.updateToolApprove(msg)
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
		if m.mode == ModeCompact {
			// Same reasoning as ModeBusy above: a compaction is also a
			// model call in flight, and a lone ctrl+c should cancel it
			// rather than risk closing the whole program by reflex.
			next, cmd := m.cancelCompact()
			return true, next, cmd
		}
		if m.mode == ModeToolApprove {
			// Same reasoning again: the agent loop's goroutine is parked
			// behind this exact dialog (see toolapprove.go's own comment
			// on updateToolApprove's esc/Cancel case), so a lone ctrl+c
			// must resolve it — with an explicit deny, never leaving the
			// bridge's channel with no answer coming — rather than risk
			// quitting the whole program while a goroutine is still
			// blocked waiting on it.
			next, cmd := m.cancelAgentTurn()
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

	case m.keys.ModelPicker:
		// Only opens from ModeChat: ModeBusy is generating (§7.4 already
		// reserves esc/ctrl+c there) and ModePicker/ModeHelp own the
		// keyboard outright while active, so a second ctrl+p is swallowed
		// rather than reopening a picker that is already open.
		if m.mode != ModeChat {
			return true, m, nil
		}
		next, cmd := m.openPicker("")
		return true, next, cmd

	case m.keys.CopyLast:
		// Same ModeChat-only gating as ModelPicker above: ModeBusy is
		// generating and every overlay mode owns the keyboard outright, so
		// there is either nothing settled to copy yet or a chord that
		// belongs to whatever is on screen instead.
		if m.mode != ModeChat {
			return true, m, nil
		}
		next, cmd := m.runCopy("")
		return true, next, cmd
	}
	return false, m, nil
}

// openPicker switches to ModePicker with a Picker built from the current
// catalog and prefiltered with query — "" for ctrl+p and a bare /model, or
// whatever text the user typed after /model when it did not resolve
// unambiguously (§4.5's OutcomePicker).
func (m Root) openPicker(query string) (tea.Model, tea.Cmd) {
	m.picker = newPicker(m.cat, m.resolveOptions(), m.favorites, m.model, query)
	m.mode = ModePicker
	return m, nil
}

// resolveOptions is the catalog.ResolveOptions every lookup in this package
// shares: /model's direct resolution and the picker's incremental search
// both have to agree with §4.5's own scorer, or the picker would rank
// results in an order the command line disagreed with.
func (m Root) resolveOptions() catalog.ResolveOptions {
	return catalog.ResolveOptions{Alias: m.alias, PreferFree: m.preferFree}
}

// applyCatalogRefreshed is CatalogRefreshedMsg's only handler. next is nil
// when app.BackgroundRefresh could not improve on the catalog LoadCatalog
// already handed Root at startup (network unreachable, every provider
// timed out) — swapping m.cat for nothing would turn a working picker into
// an empty one over something that was never the user's fault, so that case
// is a no-op.
//
// When the picker is open (ModePicker) at the moment the refresh lands, it
// is rebuilt against the new catalog rather than left stale or closed: the
// user is very possibly looking at exactly the "13" in "models · 13" this
// refresh is about to change, and closing the overlay out from under an
// still-open selection would be a worse surprise than the row list moving
// under their cursor.
func (m Root) applyCatalogRefreshed(next *catalog.Catalog) (tea.Model, tea.Cmd) {
	if next == nil {
		return m, nil
	}
	m.cat = next
	if m.mode == ModePicker {
		m.picker.cat = m.cat
		m.picker = m.picker.rebuild()
	}
	return m, nil
}

// applyModelChosen is modelChosenMsg's only handler, and the single funnel
// every path that can change the active model goes through: the picker's
// enter key and /model's direct-resolution branch both end here (§9.4/§9.6
// and slashrun.go's runModelCommand). It runs the Step 11 checks of §4.6
// before committing anything — if both the current and the destination
// model are known to the catalog and engine.CheckSwap finds a real
// conflict, the switch waits behind the §9.5 dialog instead of happening
// here.
func (m Root) applyModelChosen(ref string) (tea.Model, tea.Cmd) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		m.mode = ModeChat
		return m, nil
	}

	// A model absent from the catalog (either side) leaves CheckSwap with
	// nothing trustworthy to compare — §4.2's own Model zero value would
	// read as "no context, no caps, no cost", which is not the same claim
	// as "this model genuinely has none of those". Falling back to the
	// unconditional switch here is what Step 10 always did, and is still
	// correct when there is no catalog at all (most of this package's own
	// tests, and any session started before the first catalog load).
	to, toOK := m.cat.Get(ref)
	from, fromOK := m.cat.Get(m.model)
	if toOK && fromOK {
		if plan := engine.CheckSwap(&m.conv, from, to); !plan.OK {
			m.mode = ModeConfirm
			m.confirm = newConfirmDialog(from, to, plan)
			return m, nil
		}
	}

	m.mode = ModeChat
	return m.commitModelSwitch(ref)
}

// commitModelSwitch is applyModelChosen's, resolveConfirm's and
// finishSwitchAfterCompact's shared tail: rebind m.eng to ref via
// engineFor (see switchEngine's own comment on why this step exists at
// all) before touching the two display fields, then leave the same §4.6
// confirmation line either way. A rebuild failure — the destination
// provider disabled, undeclared, or missing its API key, the exact set
// NewProvider already names — still switches the label (hiding the
// picker's own choice would be a worse surprise than showing it) but
// replaces the confirmation line with a warning that says plainly the new
// model has no working provider, so the very next turn's failure does not
// arrive as a surprise "no hay proveedor configurado" with no context.
func (m Root) commitModelSwitch(ref string) (tea.Model, tea.Cmd) {
	next, err := switchEngine(m, ref)
	m = next
	m.model = ref
	m.footer.Model = ref
	if err != nil {
		return m.slashNotice(m.lay.glyphs().warnMark + " cambiado a " + ref +
			", pero no se pudo preparar ese proveedor: " + err.Error())
	}
	return m.slashNotice(confirmLine(m.lay.glyphs(), ref))
}

// applySessionChosen is sessionChosenMsg's only handler (§13): the §13
// /resume menu's enter key ends here. Load is only ever called with an ID
// the menu itself produced from a List() row, so a failure here means the
// file disappeared or was corrupted between listing and choosing — rare
// enough (a concurrent process, a manual edit) that a slashNotice is the
// right weight, the same "surface it, keep going" rule finishTurn's own
// provider-error branch already follows.
func (m Root) applySessionChosen(id string) (tea.Model, tea.Cmd) {
	m.mode = ModeChat
	m.resume = resumeMenu{}
	if m.sessionLister == nil {
		return m, nil
	}
	conv, err := m.sessionLister.Load(id)
	if err != nil {
		return m.slashNotice(m.lay.glyphs().warnMark + " " + err.Error())
	}

	// Replacing both m.conv and m.transcript is the same two-place write
	// NewRoot already does for Options.History (see its own comment on
	// why writing only one of them is wrong): m.conv is what the next
	// request sends, m.transcript is what the screen shows, and a resume
	// has to update both or the two would disagree from the very next
	// turn.
	m.conv = *conv
	m.transcript = historyToTranscript(conv.Messages)
	m.printedUpTo = 0

	// Same rebind switchEngine's own comment describes: a resumed session
	// can name a model from a provider that is no longer the one eng was
	// last bound to, and conv.Model is the only place that ever recorded
	// which one it was.
	next, err := switchEngine(m, conv.Model)
	m = next
	m.model = conv.Model
	m.footer.Model = conv.Model
	if err != nil {
		m.transcript = append(m.transcript, transcriptEntry{
			role: "assistant", name: "ishakat", ts: time.Now(),
			text: m.lay.glyphs().warnMark + " sesión reanudada con " + conv.Model +
				", pero no se pudo preparar ese proveedor: " + err.Error(),
		})
	}
	return m, clearScreenCmd
}

// updateChat maneja ModeChat: el input tiene el foco, enter envía (en el
// maniquí, dispara el eco simulado), y ctrl+j inserta salto de línea.
func (m Root) updateChat(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		key := keyPressString(msg)
		// The §9.6 dropdown claims up/down/tab, plus enter/esc repurposed to
		// accept/close it, before they reach the switch below. Any key it
		// does not recognise falls through unchanged.
		if m.menu.Active() {
			if handled, next, cmd := m.updateSlashMenu(key); handled {
				return next, cmd
			}
		}
		switch key {
		case m.keys.Cancel:
			return m, nil // nada que cancelar en ModeChat
		case m.keys.Submit:
			text := strings.TrimSpace(m.input.Value())
			if text == "" {
				return m, nil
			}
			if slash.IsCommand(text) {
				return m.runSlashLine(text)
			}
			return m.submit(text)
		case m.keys.Newline:
			m.input.InsertRune('\n')
			return m, nil
		case m.keys.HistoryPrev:
			// Only claims the key on the textarea's first visual line: on
			// any line below that, up is ordinary cursor movement inside a
			// multi-line draft, exactly like a shell's line editor leaves
			// up/down alone once the cursor is not on the edge line.
			if m.input.Line() == 0 {
				if next, ok := m.historyPrev(); ok {
					next.menu = slashMenuFor(next.input.Value(), next.commands, next.menu)
					return next, nil
				}
			}
		case m.keys.HistoryNext:
			if m.input.Line() == m.input.LineCount()-1 {
				if next, ok := m.historyNext(); ok {
					next.menu = slashMenuFor(next.input.Value(), next.commands, next.menu)
					return next, nil
				}
			}
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	// Recomputed after every keystroke — printable rune, backspace, paste —
	// so the dropdown always reflects what the textarea now holds.
	m.menu = slashMenuFor(m.input.Value(), m.commands, m.menu)
	return m, cmd
}

// updateBusy maneja ModeBusy: solo esc/ctrl+c cancelan (ctrl+c ya se atendió
// en la capa global), el resto de teclas se ignora porque el input no tiene
// sentido mientras el maniquí "genera".
func (m Root) updateBusy(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		if keyPressString(key) == m.keys.Cancel {
			// agentTurn.hist is only non-nil between startAgentTurn and
			// finishAgentTurn (see its own comment) — the same "is a
			// tools-enabled turn actually the one running" test
			// handleGlobalKey's own ModeToolApprove branch relies on
			// indirectly, since that mode can only ever be entered from
			// one of these turns.
			if m.agentTurn.hist != nil {
				return m.cancelAgentTurn()
			}
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
	m = m.recordHistory(text)
	m.input.Reset()

	// The user's turn joins the history before the request is built, because
	// the request is the history: Active() has to already contain what we are
	// asking about. The assistant's side is added by finishTurn, once there
	// is something to add.
	user := convo.User(text)
	m.conv.Add(user)

	// Persisted here rather than at the end of the turn, and that ordering
	// is the point: if the provider hangs and the user kills the process,
	// what they typed still survives. Waiting for the answer would mean the
	// one message guaranteed to be lost is the one the user wrote
	// themselves.
	m = m.recordMessage(user)

	return m.startEngineTurn(bannerText)
}

// startEngineTurn is submit's and runRetry's shared tail: everything past
// "the request's messages are already in m.conv" — switching to ModeBusy,
// opening the cancellable context, and starting the engine and its ticks.
// submit builds bannerText itself (it is the one call site where the
// transcript was still empty a few lines up); runRetry never draws a
// banner, since retrying only happens once the transcript already has
// something in it, so it always passes "".
//
// toolsEnabled ([tools].enabled) is the fork Step 16 adds: with tools off
// (the default, and every path before this step existed) this is exactly
// the plain m.eng.Start streaming below, unchanged. With tools on, there is
// something for a mid-turn tool call to run and — the actual point of this
// step — for an ask-tier one to pause on, so the turn instead runs through
// startAgentTurn's engine.RunAgentTurn path (agentturn.go), which is what
// can open ModeToolApprove at all.
func (m Root) startEngineTurn(bannerText string) (tea.Model, tea.Cmd) {
	if m.toolsEnabled {
		return m.startAgentTurn(bannerText)
	}

	m.mode = ModeBusy
	m.live.start(m.footer.Model)

	// context.Background rather than a parent: the program's lifetime is the
	// terminal's, and there is no ctx to inherit here — Bubble Tea does not
	// hand one to Update. Cancellation flows the other way, from cancelTurn.
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.buf = &engine.StreamBuf{}
	m.eng.Start(ctx, engine.Request{
		Model:    wireModel(m.cat, m.model),
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
		text += m.lay.glyphs().warnMark + " " + err.Error()
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

		// §10: persisted only once the message is complete (including the
		// aborted case, which is complete in the sense that nothing more is
		// coming), never while streaming — one line per finished message,
		// so a kill -9 mid-turn never leaves a JSONL line half-written.
		m = m.recordMessage(msg)
	}

	m.releaseTurn()
	m.live = liveTurn{}
	m.mode = ModeChat
	m.animOffset = 0

	return m.checkAutoCompact()
}

// checkAutoCompact is finishTurn's and finishAgentTurn's shared tail: the
// §10 auto-trigger. Once a turn's own answer has landed (streamed one
// chunk at a time, or produced in full by RunAgentTurn — this check does
// not care which), see whether the conversation just crossed
// [compact].trigger_pct of the active model's window and, if so, compact
// before the next prompt rather than waiting for the user to notice and
// type /compact themselves. m.cat.Get can fail (no catalog, or a model the
// catalog does not know) — same as applyModelChosen's own CheckSwap guard,
// that leaves nothing trustworthy to compare against, so the trigger simply
// does not fire rather than guessing a window.
func (m Root) checkAutoCompact() (tea.Model, tea.Cmd) {
	if m.compactAuto {
		if model, ok := m.cat.Get(m.model); ok {
			window := model.EffectiveContext()
			if convo.NeedsCompact(m.conv.ContextTokens(), window, m.compactTriggerPct) {
				return m.startCompact("")
			}
		}
	}
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

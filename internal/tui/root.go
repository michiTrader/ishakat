package tui

import (
	"context"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MichiTrader/ishakat/internal/catalog"
	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/convo"
	"github.com/MichiTrader/ishakat/internal/engine"
	"github.com/MichiTrader/ishakat/internal/evolve"
	"github.com/MichiTrader/ishakat/internal/mission"
	"github.com/MichiTrader/ishakat/internal/skills"
	"github.com/MichiTrader/ishakat/internal/slash"
	"github.com/MichiTrader/ishakat/internal/termenv"
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
	// ModeHotkeys: the §13/roadmap-F3 dedicated shortcuts overlay
	// (hotkeys.go), reached with /hotkeys or the same key ModeHelp's own
	// screen advertises. It exists as a Mode of its own — not a flag on
	// ModeHelp, and not folded into /help's own screen — because the
	// roadmap's F3 row asks for it as "its own overlay" distinct from
	// /help: two commands, two dropdown rows, two renderers, so a future
	// edit to one can never silently also change the other. Like
	// ModeHelp it always returns to ModeChat on any key (updateHotkeys),
	// never to ModeBusy: unlike ModeToolApprove/ModeAskUser there is no
	// turn underneath it to resume, the same reasoning ModeHelp's own
	// comment on updateHelp already gives.
	ModeHotkeys
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
	// ModeLogin: /login's in-session OAuth device-flow wizard (§13, Step
	// 24, login.go). Follows ModeCompact's own shape — an async network
	// call started by startLogin, its result landing back as a message
	// handled one layer up in updateDispatch — rather than ModeBusy's,
	// because a login has two async legs in sequence (the quick device
	// code request, then the slow poll-for-token wait) instead of
	// ModeCompact's one.
	ModeLogin
	// ModeSuggest: §19.7's "crystallization by observation" suggestion
	// dialog (Step 25, suggest.go) — "[t] crearla  [v] ver el código
	// [n] no, ni ahora ni después". Opens only at the end of a turn
	// (checkSuggest, called alongside checkAutoCompact from finishTurn
	// and finishAgentTurn), never mid-task, per civility rule 1. Unlike
	// every other overlay this one is entirely synchronous to open and
	// to dismiss — evolve.DecideSuggestion is pure, no clock or
	// filesystem call blocks Update — but accepting it ("[t]") starts a
	// real tool_create call through m.agentOpts.Runner, which *is*
	// async, so this mode still closes back to ModeBusy in that one
	// case, the same "the turn is not over, only the pause is" rule
	// ModeToolApprove already follows.
	ModeSuggest
	// ModeThemePicker: the §9.7 ctrl+t overlay (themepicker.go) — /theme
	// [nombre]'s own second access path, a flat list with no grouping
	// (themes have no provider/tier split, unlike ModePicker's catalog),
	// following resumeMenu/confirmDialog's simpler shape. Closes back to
	// ModeChat either way — unlike ModeToolApprove/ModeSuggest's one
	// async branch, applying a theme (switchTheme) is synchronous, so
	// there is no "turn not over" case to preserve here.
	ModeThemePicker
	// ModeTrust: §21.4 layer 2's first-run trust dialog (Step 30,
	// docs/PLAN.md's own "New project" mockup, trust.go). Unlike every
	// other overlay in this list, it never opens from a keybinding or a
	// slash command mid-session — NewRoot itself sets this mode, in
	// place of ModeChat, exactly once, when Options.NeedsTrust is true
	// (internal/app already looked up internal/trust.Store and found no
	// record covering this project's path). Closing it — by choosing an
	// option or by Esc, which defaults to the same safer option §21.4
	// names ("2. Ask before changes") rather than to the recommended
	// one — always returns to ModeChat: there is no turn in flight yet
	// for this to pause, the same reasoning ModeThemePicker's own
	// comment gives for its one-way close.
	ModeTrust
	// ModeMission: §21.6's own constraint-compiler confirmation dialog
	// (Step 31, mission.go), docs/PLAN.md's own worked example ("no
	// Playwright" compiling to a deny rule). Unlike ModeTrust it opens
	// mid-submit, not at construction: checkMission runs inside submit,
	// before startEngineTurn, and only switches here when the goal's
	// mission.Compile result carries at least one negated constraint
	// (mission.Mission.HasDeny()) — "this dialog is not shown for every
	// task" (§21.6). The turn itself has not started yet (unlike
	// ModeToolApprove/ModeSuggest's mid-turn opens, there is no
	// RunAgentTurn goroutine parked underneath this one), so resolving it
	// — by choosing an option or by Esc, which like ModeTrust's own Esc
	// defaults to the safer choice rather than either extreme (see
	// missionDialogDefault's own comment) — is what actually calls
	// m.submit(text) and starts the turn, going to ModeBusy from there
	// the same as an ordinary submit would.
	ModeMission
	// ModeToolScope: §21.6's own second dialog (Step 31 part 6,
	// toolscope.go), "Tools for this mission" — the tool-scope proposal
	// mockup, distinct from ModeMission's own constraint-confirmation
	// mockup. Unlike ModeMission it never opens from checkMission
	// directly: it is chained from resolveMission's own tail, because
	// §21.6's own dialog-opening trigger ("the goal contains a
	// constraint") is exactly the condition that already opened
	// ModeMission in the first place — there is no second, independent
	// check to duplicate. Resolving it — by choosing an option or by
	// Esc, which like the other two dialogs' own Esc defaults to the
	// safer choice (see toolScopeDialogDefault's own comment) — is what
	// actually calls m.submit(text) and starts the turn, the same
	// "opened mid-submit, closing starts the turn" shape ModeMission's
	// own comment describes, one level further chained in.
	ModeToolScope
	// ModeAskUser: Step 32 part 3's own overlay (askuser.go), opened when
	// the model's ask_user tool call reaches this session's ask.Asker
	// (internal/app's tuiAsker) and needs a human answer. Opens mid-turn —
	// the same "the turn is not over, only the pause is" shape
	// ModeToolApprove already follows — so closing it (by answering or by
	// Esc, which sends an empty ask.Answers rather than any allow/deny
	// value, since ask_user has no such axis) returns to ModeBusy, not
	// ModeChat.
	ModeAskUser
	// ModeQueueEdit: W2 item 4's own overlay (F13, DECISION-2 consequence
	// 3, queueedit.go) for alt+up — "re-opens the follow-up queue for
	// editing". Unlike every mid-turn overlay above, this one opens from
	// *either* ModeBusy or ModeChat: a follow-up queued during one turn
	// can still be sitting unsubmitted once the interface is back in
	// ModeChat (checkFollowup, chained from checkEndOfTurn, may have been
	// pre-empted by checkAutoCompact/checkSuggest at the moment that turn
	// ended), and reviewing/trimming the queue should not require
	// starting a new turn first. openQueueEdit records which of the two
	// this dialog was opened from (queueEditDialog.returnMode) so closing
	// it goes back to exactly that, the same way ModeToolApprove always
	// knows to return to ModeBusy without needing to ask.
	ModeQueueEdit
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

	// reasoning is the model's own "thinking" stream for this turn (§17
	// point 6a: "show at least ~2 lines of thinking glued to the response,
	// in grey"), populated only for role == "assistant" — a user's own
	// message never carries one. Empty is the overwhelmingly common case:
	// a model with no reasoning capability, a plain-streaming turn under
	// cfgReasoning == "off", or simply a turn where the provider sent no
	// EventReasoning deltas at all. renderTranscriptLine decides how much
	// of it to actually show (cfgReasoning's own "collapsed" vs "full"
	// distinction), never this struct — a transcriptEntry is a durable
	// record of what happened, not a rendering decision.
	reasoning string
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

	// themesDir and themeStore are /theme's own two dependencies
	// (theme.go): where to look for named themes beyond the embedded
	// default, and where to persist a switch. Both mirror Options'
	// fields of the same name one-for-one — see those comments for why
	// nil/"" are supported values.
	themesDir  string
	themeStore ThemeStore

	// titleStore is /name's own persistence seam (F12, session.go's own
	// SessionTitleStore doc comment) — the exact mirror of themeStore
	// above for a session rename instead of a theme switch. nil is the
	// supported "do not persist" value: /name still renames m.conv for
	// the running session, it just does not survive a restart.
	titleStore SessionTitleStore

	// settingsStore is /settings' own persistence seam (F4,
	// settingscmd.go's own SettingsStore doc comment) — the same §6.1
	// mirror themeStore/titleStore already draw, one level more general
	// since a single Set(key, value) covers every key config.Settings
	// knows about instead of one method per feature. nil is the
	// supported "do not persist" value: a /settings write still applies
	// in memory for the running session, it just does not survive a
	// restart.
	settingsStore SettingsStore

	// reloadFor is /reload's own hot-apply seam (F17, reload.go's own
	// ReloadFactory doc comment) — the same §6.1 boundary
	// catalogRefreshFor already crosses, for the pieces F2 does not
	// already cover (keymap, skills, system prompt). nil is the
	// supported "not wired" value: every test in this package, and any
	// caller with nothing wired, gets runReloadCommand's own notice
	// instead of a silent no-op.
	reloadFor ReloadFactory

	// pathLister is F18's own "@" path-completion seam (atmenu.go/atrun.go,
	// docs/ROADMAP-ux-2026-08-20.md's W5): the same §6.1 boundary
	// reloadFor/catalogRefreshFor already cross, called synchronously
	// (engineFor's own switchEngine convention, not reloadFor's tea.Cmd
	// one) since listing a directory is cheap and local, the same way
	// theme.Available's own os.ReadDir call needs no tea.Cmd wrapping
	// either. nil is the supported "not wired" value: atMenuFor never
	// opens the dropdown when pathLister is nil, so a Root built without
	// one (every test in this package that does not set it) behaves
	// exactly as if the user never typed "@" at all.
	pathLister PathLister

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

	// effortFor is F9's own §6.1 seam (effortcmd.go's own EffortResolver
	// doc comment): turns m.model + m.effort into the
	// engine.Request.Params override startEngineTurn/startAgentTurn
	// attach to the request they are about to send. nil is a supported
	// value, the same discipline engineFor/reloadFor/pathLister already
	// establish: every test in this package, and any caller with
	// nothing wired, simply never attaches an effort override — exactly
	// the behaviour a session had before F9 existed.
	effortFor EffortResolver

	// loginFor drives the §13/Step 24 in-session /login wizard's actual
	// network calls (loginfactory.go) — nil is a supported value
	// (every test in this package, and any caller with nothing wired):
	// startLogin reports that /login has nothing wired to it instead of
	// opening ModeLogin with no way to ever finish, the same nil-factory
	// discipline switchEngine already follows for engineFor above.
	loginFor LoginFactory

	// catalogRefreshFor is F2's own hot-apply seam
	// (docs/ROADMAP-ux-2026-08-20.md's W4, catalogrefresh.go): a
	// successful /login writes a new credential to disk, but the catalog
	// this Root is already showing (m.cat) — and the *config.Config it
	// resolved from (m.cfg) — are both a snapshot taken once at boot, so
	// neither sees the new credential until something re-reads
	// configuration and re-runs discovery. finishLogin (login.go) calls
	// this instead of leaving the freshly-authenticated provider
	// invisible until a separate --refresh/restart. nil is a supported
	// value (every test in this package, and any caller with nothing
	// wired): finishLogin then falls back to its pre-F2 behaviour of
	// closing the wizard with no refresh, the same "no silent panic on
	// an unwired dependency" rule engineFor/loginFor's own nil checks
	// already follow.
	catalogRefreshFor CatalogRefreshFactory

	// buf is the landing zone for the turn currently in flight: the engine's
	// goroutine writes into it, and streamTickMsg drains it on the repaint
	// clock (that decoupling is StreamBuf's whole purpose). Non-nil exactly
	// while a turn is live, which is also how drainStream tells "the tick
	// arrived after the turn already closed" from a real drain.
	buf *engine.StreamBuf

	// agentStream is buf's sibling for a tools-enabled turn (W2 item 2,
	// docs/ROADMAP-ux-2026-08-20.md, agentstream.go): startAgentTurn's own
	// agentTurnCmd goroutine writes into it via the AgentSink it builds
	// (agentStreamBuf.sink), and streamTickMsg drains it the same way it
	// drains buf. Exactly one of buf/agentStream is non-nil while a turn is
	// live — the streamTickMsg dispatch below picks whichever is set — and
	// it is also how drainAgentStream tells "the tick arrived after the
	// turn already closed" from a real drain, mirroring buf's own comment.
	agentStream *agentStreamBuf

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

	// effort is F9's own session-scoped state (docs/ROADMAP-ux-2026-08-20.md
	// W5, effortcmd.go): the effort/thinking-level the user picked via
	// /effort or the EffortCycle chord, exactly as it appears in the
	// active model's own catalog.Model.EffortLevels — never the user's
	// raw typed casing (setEffort/matchEffortLevel already normalize
	// that before this is assigned). "" means "nothing chosen this
	// session, use whatever the provider defaults to" — the same
	// "absence is a legitimate value, not an error" convention system
	// above already follows for "no system prompt configured": every
	// turn-start site reads this fresh (startEngineTurn/startAgentTurn)
	// and simply omits the effort override from engine.Request.Params
	// when it is empty, rather than sending an empty string on the wire.
	//
	// Deliberately not persisted through any *Store: unlike titleStore/
	// themeStore's own session-crossing writes, an effort level is a
	// per-turn request parameter, not saved session state — the same
	// reasoning that already keeps compactModel/system themselves
	// in-memory-only fields on this struct.
	effort string

	transcript []transcriptEntry

	// scrollOffset is fullscreen's own scroll position, in rows counted
	// from the live tail: 0 means "pinned to the bottom, following new
	// content as it arrives" (clipHead's original, only behaviour before
	// this field existed); a positive value is how many rows the visible
	// window has been scrolled back, towards the start of head()'s
	// content. This is the fix for the reported "mouse wheel loads
	// earlier messages instead of scrolling" bug — see emit's own doc
	// comment (view.go) for the xterm mode-1007 mechanism that produced
	// the symptom, and clipHead's own doc comment for how this offset
	// turns into an actual visible window instead of always showing the
	// tail.
	//
	// Clamped on every write by scrollBy (below), against
	// maxScrollOffset() (view.go) — the same ceiling clipHead's own
	// per-frame render clamp computes, via the shared maxScrollOffsetFor
	// helper the two call identically so they can never disagree. This
	// field used to be a raw, never-pre-clamped accumulator, relying
	// entirely on clipHead's clamp to keep the *drawn* window in range on
	// every frame — which it did, but a reported bug showed that was not
	// enough: clipHead's clamp is local to one render call and never
	// written back here, so this field could grow arbitrarily past what
	// was ever visible (scroll up 50 wheel-lines when only ~10 have
	// headroom, and the view stops moving after 10 but this field still
	// reaches 150), which then had to be silently "paid back" before
	// scrolling down did anything at all. Clamping here too, in scrollBy,
	// closes that gap — see its own doc comment for the fix and why
	// clamping at write-time cannot drift from clipHead's read-time one.
	// A resize, an eviction, or a shrinking transcript between one write
	// and the next View still cannot leave this field pointing past the
	// end of what actually exists: clipHead's own clamp remains an
	// unconditional second line of defense on every render regardless of
	// what this field currently holds.
	//
	// Reset to 0 wherever printedUpTo also resets (ClearScreen, /clear,
	// /new): a scroll position measured against a transcript that is
	// about to be wiped has nothing left to mean. Also reset by submit
	// (root.go): sending a new message is the user asking to continue the
	// conversation, so it is reasonable to jump back to the live tail
	// even if they had scrolled up to reread something.
	scrollOffset int

	// printedUpTo is how many of transcript's leading entries have already
	// been handed to commitEntryCmd (tea.Println) and therefore live in the
	// terminal's real scrollback. head() only redraws transcript[printedUpTo:]
	// — see evictOverflow, which is what advances this once the live region
	// grows taller than the terminal.
	printedUpTo int

	footer FooterState

	animOffset int

	// quitPresses is how many times Quit has been pressed inside the current
	// grace window (§7.4, RC-1). Reset to 0 by quitConfirmMsg when the
	// window expires. handleGlobalKey quits when this reaches keys.QuitRepeat.
	quitPresses int

	quitting bool

	// cfg is the effective configuration, held only so /config
	// (runConfigCommand, configcmd.go) has something to call
	// config.Redacted() on at render time — every other Root field this
	// package already derives from Options.Cfg (cfgBanner/cfgSyntax/
	// cfgMarkdown/keys/footerItems, compactModel/fallbackModel/etc.)
	// still reads its own single value out of it instead of reaching
	// through cfg, so none of that has to change. This does not reopen
	// the §6.1 boundary internal/config's own schema draws: cfg is the
	// same *config.Config Options.Cfg already is (a pure value type, no
	// filesystem/network handle attached to it), the same reasoning
	// Catalog/Skills above already rely on for holding a whole struct
	// rather than yet another set of copied-out scalars. nil is a
	// supported value (every test in this package that never sets
	// Options.Cfg): runConfigCommand reports that instead of a nil
	// dereference, the same "nothing wired, nothing happens" default this
	// file's own EvolveStore/EngineFactory fields already establish.
	cfg *config.Config

	cfgBanner bool
	// cfgSyntax is ui.syntax (config/schema.go's UI.Syntax, defaults.toml's
	// syntax = true, unread by anything until codeblock.go): whether fenced
	// code blocks in the transcript get Chroma's tokeniser run over them at
	// all. false still draws the §9.3 rail — that is a layout choice, not a
	// colour one — it only skips the highlighting itself, the same way
	// theme.CapNone already does inside syntaxStyleFor for a terminal that
	// cannot show colour regardless of what the config says.
	cfgSyntax bool

	// cfgMarkdown is ui.markdown (config/schema.go's UI.Markdown, defaults.toml's
	// markdown = true, unread by anything until markdown.go): whether prose
	// outside a fenced code block is handed to Glamour for bold/headers/
	// links/lists, or left as the plain wrapText output every message got
	// before this increment. It is a separate flag from cfgSyntax because
	// the two gate two different renderers over two different kinds of text
	// (see renderMessageBody's own comment).
	cfgMarkdown bool

	// cfgReasoning is ui.reasoning (config/schema.go's UI.Reasoning,
	// defaults.toml's reasoning = "collapsed"): whether/how much of the
	// model's own "thinking" stream (EventReasoning deltas, surfaced live
	// via liveTurn.reason and, since engine.AgentResult gained a Reasoning
	// field, via a tool-enabled turn's result too) is shown alongside its
	// answer. Three values, matching the TOML comment and docs/PLAN.md's
	// own `[ui]` example verbatim: "off" shows nothing (transcriptEntry
	// still records it regardless of mode — finishTurn/finishAgentTurn
	// always populate transcriptEntry.reasoning when the turn produced
	// one, so a later config change to "collapsed"/"full" can still show
	// history recorded while "off" was active — renderTranscriptLine
	// simply never draws it); "collapsed" — the
	// default — shows a short, dim, "~2 lines" preview glued to the top of
	// the answer (renderReasoningPreview, chat.go); "full" shows the whole
	// stream, unclipped.
	//
	// This is a *new* interpretation, not headless's own: internal/app/
	// headless.go's own reasoning := strings.EqualFold(cfg.UI.Reasoning,
	// "full") treats "collapsed" identically to "off" (both hidden) on the
	// text sink, because a pipe has no fold/expand affordance to collapse
	// *into* — headless's own showReasoning doc comment says as much. The
	// TUI has no such excuse: a terminal transcript can show a short
	// preview and still read cleanly, which is the literal shape the user
	// asked for ("show at least ~2 lines... glued to the response"), so
	// "collapsed" here means what its name says instead of copying
	// headless's degenerate case.
	cfgReasoning string
	fps          int

	// foldCode is ctrl+r's own toggle (tui.Map.ToggleFold, §17 2026-08-18
	// "code blocks fill the terminal" fix, part 2): whether every fenced
	// code block still in the live-managed region (head(), view.go) renders
	// as its one-line summary instead of its full body. A single bool
	// rather than a per-block map is a deliberate simplification, not a
	// missing feature: the reported problem was the terminal filling up
	// with code wholesale, and a global toggle solves exactly that without
	// needing a stable per-block identity to key a map by — something
	// Root's own copy-by-value semantics (see liveTurn's doc comment on why
	// its fields must stay plain values) make awkward to thread through
	// finishTurn/commitEntryCmd correctly. Real terminal scrollback already
	// committed via commitEntryCmd cannot be redrawn afterwards (§7.5), so
	// this only ever affects the last keepInline transcript entries plus
	// the live turn — see commitEntryCmd's own comment for that limit.
	//
	// As of F8b (docs/ROADMAP-ux-2026-08-20.md W2 item 5) this field's name
	// undersells what it does: the same bool now also collapses a bubble's
	// reasoning preview (renderReasoningPreview, chat.go) to
	// reasoningFoldSummary's one-line form, replacing the pre-F8b behaviour
	// of "ctrl+r folds code only". It keeps the field's original name
	// rather than gaining a second one, exactly because §5's "deliberately
	// not in any wave" note is explicit that F8b "extends *what* it folds,
	// not *how much state* it keeps" — renaming or duplicating this field
	// would imply new state where the roadmap asks for none. The regular-
	// vs-fullscreen limitation above is unchanged and applies identically
	// to the reasoning half: a committed entry's reasoning preview freezes
	// at whatever foldCode held the moment commitEntryCmd printed it, in
	// `regular` mode only — `fullscreen` never commits at all (see
	// evictOverflow's own fullscreen guard), so there every entry still in
	// m.transcript keeps reacting to ctrl+r for as long as the session
	// runs, which is DECISION-1's own "concrete payoff" the roadmap names
	// for this item.
	foldCode bool

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

	// tuiMode is Options.TUIMode, carried unchanged. As of W3 part 6 this
	// has a second reader beyond /debug: emit (view.go) is the one place
	// that actually acts on it, setting AltScreen true in fullscreen —
	// see emit's own doc comment.
	tuiMode termenv.Mode

	// exitTranscript is Options.FullscreenExitTranscript, carried
	// unchanged. DECISION-1b ([ui] fullscreen_exit_transcript, default
	// true): whether leaving fullscreen should print the whole
	// conversation to the real terminal before handing the screen back.
	// See ExitTranscript's own doc comment (view.go) for why this is read
	// by internal/app.Run *after* p.Run() returns, not by anything inside
	// this package's own Update/View loop.
	exitTranscript bool

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

	// atMenu is F18's own "@" path-completion dropdown state (atmenu.go),
	// the direct structural sibling of menu above: recomputed from the
	// input on every keystroke by atMenuFor, but only ever considers the
	// word at the true end of the whole buffer (currentWordAtEnd) rather
	// than menu's whole-line scope, since an "@" reference can sit
	// anywhere inside an otherwise ordinary chat message.
	atMenu atMenu

	// cat is the model catalog snapshot (§4.2). Never touched over the
	// network from this package (§6.1) — it is handed over once, already
	// built by internal/app, and read by both /model's direct resolution
	// and the picker's incremental search.
	cat *catalog.Catalog

	// skills is the rung-0 prose capability listing (§19.2/§19.4, Step 19),
	// already resolved once at startup by internal/app.SystemPrompt's own
	// skills.Discover call and handed over here unchanged — this package
	// never touches the filesystem to find a SKILL.md itself (§6.1's same
	// "read once, hand over" rule catalog/history/System already follow).
	// The zero value (no Skills, no Warn) is a legitimate "no skills
	// configured", the ordinary case for any session with [tools].enabled
	// = false or an empty/unset skills_dir — /skills reports that instead
	// of an empty list with no explanation, the same "no hay catalogo" rule
	// runModelsCommand already applies to a nil catalog.
	skills skills.Result

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

	// fallbackModel mirrors [app].fallback_model (config.App.FallbackModel,
	// §4.2's Ref form, same as compactModel above), already resolved by
	// internal/app before NewRoot ever runs — this package never reads
	// config itself (§6.1). Empty means "no separate fallback": the
	// documented meaning of an empty fallback_model in defaults.toml, and
	// checkFallback below is a no-op whenever this is "" or already equal
	// to m.model, so a session with nothing configured behaves exactly as
	// it did before this field existed.
	fallbackModel string

	// consecutiveFailures counts turns that ended in finishTurn/
	// finishAgentTurn with err != nil, back to back. A turn that ends
	// without an error (including one aborted by the user, §7.4 — a
	// cancellation is not the model's fault) resets this to zero: the
	// Phase 4 contract this exists for is "the active model failed twice
	// in a row", not "failed twice ever", so a single stray error
	// surrounded by working turns must never trip the switch below.
	consecutiveFailures int

	// compact is the Step 12 overlay's own state (§9.8), live only while
	// mode == ModeCompact.
	compact compactState

	// compactCancel closes the in-flight Summarize call's context — the
	// same role m.cancel plays for an ordinary turn (§7.4, see
	// cancelCompact).
	compactCancel context.CancelFunc

	// login is Step 24's ModeLogin overlay's own state (login.go), live
	// only while mode == ModeLogin.
	login loginState

	// loginCancel closes the in-flight device-flow request/poll's
	// context — the same role compactCancel plays for startCompact.
	loginCancel context.CancelFunc

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

	// missionRecorder persists a confirmed §21.6 mission/tool-scope
	// resolution (§21.16 decision 3, session.go's own MissionRecorder
	// doc comment) — the exact mirror of recorder above for that other
	// event kind. nil is the identical supported "do not save" value.
	missionRecorder MissionRecorder

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

	// toolsLister is /tools' own read side (§13, Step 20, tools.go's own
	// doc comment on why this is an interface rather than a direct
	// internal/tools import). nil means "cannot list layer-2 tools" —
	// [tools].enabled = false, an empty tools.dir, or any test in this
	// package, none of which set it — the same nil-is-safe convention
	// Recorder/SessionLister already establish for their own concern.
	toolsLister ToolsLister

	// permissionsLister is /permissions' own read side (§13, Step 32,
	// permissions.go's own doc comment on why this is an interface rather
	// than a direct internal/permissions import). nil means "no live
	// policy to show" — tools disabled (buildAgentOptions never builds a
	// *permissions.Guard then), or any test in this package, neither of
	// which set it — the same nil-is-safe convention toolsLister above
	// already establishes for its own concern.
	permissionsLister PermissionsLister

	// resume is the §13 /resume overlay's own state, live only while
	// mode == ModeResume.
	resume resumeMenu

	// themePicker is the §9.7 ctrl+t overlay's own state (themepicker.go),
	// live only while mode == ModeThemePicker.
	themePicker themePickerState

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

	// askUser is Step 32 part 3's overlay state (askuser.go), live only
	// while mode == ModeAskUser.
	askUser askUserDialog

	// queueEdit is W2 item 4's own overlay state (queueedit.go), live
	// only while mode == ModeQueueEdit.
	queueEdit queueEditDialog

	// agentTurn is startAgentTurn's own bookkeeping (agentturn.go) for
	// whichever tools-enabled turn is currently running through
	// engine.RunAgentTurn, live only between startAgentTurn and
	// finishAgentTurn. m.cancel (shared with the plain streaming path)
	// carries that turn's cancellation, so there is no separate
	// agentCancel field — cancelAgentTurn closes the very same m.cancel.
	agentTurn agentTurnState

	// steering is W2 item 4's own queue pair (F13, steering.go): ordinary
	// text submitted mid-turn (queueSteering) and alt+enter follow-ups
	// (queueFollowup), both reached through the lazily-initialised
	// steeringQueue() accessor rather than set directly here. Unlike
	// buf/agentStream/agentTurn above, releaseTurn/finishAgentTurn must
	// never nil this out — DECISION-2 consequence 3 is explicit that the
	// follow-up queue survives a turn boundary, and a steering message
	// that missed this iteration's Inject poll must still be there for
	// the next one. See steering.go's own package doc comment for the
	// full reasoning.
	steering *steeringQueue

	// evolveStore is §19.7's own persistence seam (suggest.go's own
	// EvolveStore doc comment) — usage.jsonl's ledger plus
	// suggest-state.json's budget/decay bookkeeping, both resolved
	// through it rather than read from disk here (§6.1). nil is the
	// supported "the suggestion feature is not active" value
	// ([tools.evolve].mode != "suggest", or the store failed to open),
	// same nil-is-safe convention Recorder/SessionLister/EngineFactory
	// already establish for their own concern — checkSuggest simply
	// never fires.
	evolveStore EvolveStore

	// evolveThresholds, suggestPerSession, suggestPerWeek and
	// decayAfterRejects mirror config.Evolve/config.Tools.MaxTools
	// verbatim, already translated by internal/app.evolveThresholds —
	// the exact scalars checkSuggest needs to call evolve.DecideSuggestion,
	// the same "test-friendly, still correct" resolved-value rule
	// compactAuto/compactTriggerPct above already follow for [compact].
	evolveThresholds  evolve.Thresholds
	suggestPerSession int
	suggestPerWeek    int
	decayAfterRejects int

	// suggestSessionCount is §19.7 rule 3's in-memory "1 per session"
	// half (see evolve.SuggestState's own doc comment for why this is
	// not persisted alongside the week counter): reset simply by the
	// process exiting, incremented once per suggestion actually shown
	// (startSuggest), never by one merely detected and not offered.
	suggestSessionCount int

	// suggest is Step 25's ModeSuggest overlay's own state (suggest.go),
	// live only while mode == ModeSuggest.
	suggest suggestState

	// trust is Step 30's ModeTrust overlay's own state (trust.go), live
	// only while mode == ModeTrust.
	trust trustDialog

	// gitInGit, gitClean and gitBranch are Options.GitInGit/GitClean/
	// GitBranch's own persistent copies, kept on Root (unlike the
	// pre-/trust-command era, where they only ever lived transiently
	// inside NewRoot's own local variables long enough to build the
	// first-run trust dialog once, at construction) so that a later
	// /trust command (trustcmd.go, Step 30's own second slice) can
	// reopen the identical dialog with the identical git line — the
	// same "compute once at startup, reuse for the life of the
	// session" reasoning cwd itself already follows two fields above.
	gitInGit  bool
	gitClean  bool
	gitBranch string

	// trustStore persists §21.4 layer 2's own decision (trust.go's own
	// doc comment on the §6.1 seam this draws — the same one ThemeStore
	// already draws for /theme's write). nil is a supported value: the
	// chosen autonomy still applies for the running session, it just
	// does not survive a restart, the same "session-only" degradation
	// ThemeStore's own nil case already documents.
	trustStore TrustStore

	// curationStore is F5/Layer 2's own persistence seam
	// (curation.go's own doc comment on the §6.1 seam this draws — the
	// same shape TrustStore/ThemeStore already draw for their own
	// writes). nil is a supported value: ctrl+x/ctrl+h simply do
	// nothing, the same degradation hideOrUnhideCurrent's own doc
	// comment already documents.
	curationStore CurationStore

	// hidden is Options.Hidden, carried unchanged: internal/app's own
	// applyCuration audit trail, read by runModelCommand's hidden-model
	// fallback (slashrun.go) to answer design doc principle 4 for
	// automatic-rule hides that CurationStore itself never tracked. See
	// Options.Hidden's own doc comment for why this is a plain
	// []catalog.Hidden rather than a richer app-side type.
	hidden []catalog.Hidden

	// mission is §21.6's ModeMission overlay's own state (mission.go),
	// live only while mode == ModeMission.
	mission missionDialog

	// missionText is submit's own text, held here between checkMission
	// opening ModeMission and resolveMission finally calling m.submit —
	// see resolveMission's own comment for why the dialog has to
	// remember what it paused, not just how it was answered.
	missionText string

	// missionGuard is §21.6's own persistence seam (mission.go's own doc
	// comment on the §6.1 seam this draws — the same shape TrustStore
	// draws for its own write). nil (every test in this package, and any
	// caller that never wires Options.MissionGuard) means a confirmed
	// mission's rules apply nowhere — the dialog still closes and the
	// turn still starts, it simply enforces nothing, the same
	// "session-only, or here not even that" degradation TrustStore's own
	// nil case documents for a different failure mode.
	missionGuard MissionGuard

	// missionPolicy is §21.6's own second dialog-opening trigger's
	// bridged config (checkToolPolicy's own doc comment, toolscope.go):
	// the same config.Permissions.Shell/ShellDeny a real caller already
	// has, converted to a mission.Policy value by internal/app the same
	// way denyRulesOf already bridges permissions.MissionRule — see
	// mission.Policy's own doc comment for why internal/mission cannot
	// build this conversion itself.
	//
	// A *mission.Policy, not a plain mission.Policy, precisely so a
	// caller that never wires Options.MissionPolicy (every test in this
	// package that does not set it, and any real caller not yet updated)
	// degrades to "this trigger never fires" rather than to
	// mission.Policy{}'s own zero value: ShellAllowed's own zero value is
	// false, and per OutsidePolicy's own doc comment that would make
	// *every* affirmed keywordRules technology look like a policy
	// collision, the opposite of the "no wiring means no new behaviour"
	// degradation every other optional seam on this struct already
	// follows (missionGuard above, trustStore, evolveStore). nil is
	// checked directly by checkToolPolicy before ever calling
	// OutsidePolicy, the same "check the seam, not its zero value" shape
	// missionGuard's own "!= nil" check already establishes.
	missionPolicy *mission.Policy

	// toolScope is §21.6's second dialog's own state (toolscope.go),
	// "Tools for this mission" — live only while mode == ModeToolScope.
	// Unlike mission, this is never opened directly from checkMission:
	// resolveMission's own tail is its only caller (see ModeToolScope's
	// own doc comment below).
	toolScope toolScopeDialog

	// missionAppliedRules carries, from resolveMission's tail to
	// resolveToolScope's own tail, exactly the rules missionAccept just
	// applied via MissionGuard.AddMissionRules — in convo.MissionRule
	// shape, since that is what the combined event resolveToolScope
	// persists (§21.16 decision 3) is made of. nil in every other case:
	// missionAdjust and missionSoft apply no rule (see their own doc
	// comments in mission.go), and a goal that reaches ModeToolScope via
	// checkToolPolicy's own direct trigger (toolscope.go) never went
	// through resolveMission at all, so there is nothing to carry.
	// resolveToolScope reads this once and always resets it to nil
	// before returning, the same "read once, then clear" shape
	// missionText itself already follows between the two dialogs, so a
	// later, unrelated mission accepted in the very next turn can never
	// see a stale value left over from this one.
	missionAppliedRules []convo.MissionRule
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

	// ThemesDir is xdg.ThemesDir(), where /theme looks for named themes
	// beyond the embedded default (§8, theme.go's own doc comment: "un
	// tema es un archivo de datos"). This is a bare path, not a
	// filesystem read — Root only ever hands it to theme.Load/
	// theme.Available, the same two calls internal/app.Run itself makes
	// at startup — so passing it here does not reopen the §6.1 boundary
	// tui's own package comment on Options.Cfg draws around
	// *config.Config: it is one string, the directory xdg.ThemesDir()
	// already is, not a config field this package would otherwise have
	// to know the shape of. Empty is a supported value: theme.Available
	// and theme.Load both already treat "" as "no such directory,
	// nothing found there" and fall back to the embedded default alone.
	ThemesDir string

	// ThemeStore persists /theme's own choice (theme.go's own doc
	// comment on the §6.1 seam this draws, the same one EvolveStore
	// already draws for its own config write). nil is a supported
	// value: the switch still applies for the running session, it just
	// does not survive a restart.
	ThemeStore ThemeStore

	// TitleStore persists /name's own rename (F12, session.go's own
	// SessionTitleStore doc comment on the §6.1 seam this draws, the
	// same one ThemeStore already draws for its own write). nil is a
	// supported value: the rename still applies for the running
	// session, it just does not survive a restart.
	TitleStore SessionTitleStore

	// SettingsStore persists /settings' own writes (F4,
	// settingscmd.go's own SettingsStore doc comment on the §6.1 seam
	// this draws). nil is a supported value: a settings change still
	// applies for the running session, it just does not survive a
	// restart.
	SettingsStore SettingsStore

	NoTTY bool

	// TUIMode is DECISION-1(d)'s already-resolved render-mode verdict
	// (termenv.Detect, run once by internal/app.Run against [ui] tui_mode
	// and NoTTY above — this package never calls termenv itself, the same
	// §6.1 rule Cap/Glyphs/Termux already follow: tui does not read the
	// environment, it is handed the answer). The zero value is
	// termenv.ModeRegular, which is also termenv.Detect's own "not sure"
	// default, so a bare Options (every test in this package) keeps
	// today's inline behaviour with no caller having to think about it.
	//
	// As of W3 part 6 this is actually acted on: emit (view.go), the one
	// function docs/DESIGN-tui-mode.md §4 Rule 2 allows to be mode-aware,
	// sets v.AltScreen = (mode == termenv.ModeFullscreen). Landing the
	// plumbing in W3 part 1 and the behaviour here, in that order, is what
	// kept this a one-line, reviewable diff confined to emit's own branch
	// instead of a change that also had to introduce the seam it depends
	// on — see emit's own doc comment for the render/emit split, and
	// Options.FullscreenExitTranscript below for the exit-transcript half
	// of what fullscreen needs that AltScreen alone does not give it.
	TUIMode termenv.Mode

	// FullscreenExitTranscript is DECISION-1b ([ui] fullscreen_exit_transcript,
	// config/schema.go, default true): whether leaving fullscreen mode
	// should print the whole conversation to the real terminal — into
	// persistent scrollback — before handing the screen back to whatever
	// was on it before. The zero value is false, which is *not* the
	// configured default (true) — every real caller is expected to pass
	// cfg.UI.FullscreenExitTranscript unchanged, the same "resolved
	// elsewhere, carried here as-is" rule TUIMode above already follows;
	// false is what a bare Options (every test in this package that never
	// sets this) gets, which is the correct "do not print anything extra"
	// behaviour for a test that is not exercising this feature at all.
	//
	// Why this needs its own field, its own method (Root.ExitTranscript,
	// view.go), and — critically — a caller *outside* this package's own
	// tea.Model loop:
	//
	// The naive design is a Cmd that runs tea.Println(transcript) followed
	// by tea.Quit, sequenced with tea.Sequence, fired from whatever
	// Update case is about to leave fullscreen (a quit key, /clear, a mode
	// switch — anywhere AltScreen would flip back to false). That design
	// is not merely inelegant, it is provably racy, for a reason specific
	// to how charm.land/bubbletea/v2 is actually built (read from source,
	// not inferred): cursedRenderer.render(v) (cursed_renderer.go:581-586)
	// does nothing but s.view = v — it performs no I/O at all. The bytes
	// that make up a frame only reach the real terminal on an independent
	// clock: Program.startRenderer (tea.go:1391-1417) starts a
	// time.NewTicker(framerate) loop that calls p.flush() then
	// renderer.flush(false) on every tick, completely decoupled from
	// Update/View's own message-processing cycle. Meanwhile
	// insertAbove — what tea.Println actually calls
	// (cursed_renderer.go:711) — writes synchronously, immediately, into
	// whichever screen buffer the renderer's own bookkeeping currently
	// says is active. There is no message, channel, or other
	// synchronization primitive anywhere in this path that means "the
	// alt-screen-exit ANSI sequence this Update call is about to cause is
	// already on the wire" — so a Println sequenced before a Quit can
	// legitimately land on either side of the real AltScreen-exit write,
	// non-deterministically, depending on ticker timing that this
	// package has no way to observe or control. On the losing order, the
	// transcript is written into the doomed alternate-screen buffer
	// instead of real scrollback, and is lost the instant the terminal
	// switches back — silently defeating this very feature.
	//
	// The one place with no such race is Program.Run itself
	// (tea.go:1144-1171): after eventLoop returns, Run unconditionally
	// calls p.shutdown(killed), which calls p.stopRenderer(kill)
	// (tea.go:1427) — which, unless killed, calls renderer.flush(true)
	// and then unconditionally renderer.close() (cursed_renderer.go:144),
	// the exact function that synchronously writes and flushes
	// ansi.ResetModeAltScreenSaveCursor when the last View had
	// AltScreen == true — followed by p.restoreTerminalState()
	// (tty.go:33), which flushes any remaining queued output. All of that
	// happens strictly before shutdown, and therefore Run itself, returns
	// control to the caller. So: by the time internal/app.Run's own
	// p.Run() call returns, the real terminal is provably already back on
	// the main screen with the alt-screen-exit sequence already on the
	// wire, and it is safe to print the exit transcript there with a
	// plain fmt.Print — no tea.Println, because there is no running
	// Program left to hand a Cmd to.
	//
	// This is why Root.ExitTranscript() is a plain (String, no tea.Cmd,
	// no side effect) method, never called by anything inside this
	// package's own Update/View, and why internal/app.Run — not this
	// package — is the one that calls it, on the final tea.Model p.Run()
	// itself returns, after the error check and after p.Run() has
	// unambiguously returned.
	FullscreenExitTranscript bool

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

	// EffortFor is F9's own §6.1 seam (effortcmd.go's own EffortResolver
	// doc comment) — see Root.effortFor's own comment for what it is
	// called with and why nil is a supported value.
	EffortFor EffortResolver

	// LoginFor drives /login's actual device-flow network calls
	// (loginfactory.go) — see Root.loginFor's own comment for the §6.1
	// boundary this crosses and why nil is a supported value.
	LoginFor LoginFactory

	// CatalogRefreshFor is F2's own hot-apply seam (catalogrefresh.go) —
	// see Root.catalogRefreshFor's own comment for the §6.1 boundary
	// this crosses, why it has to re-read configuration from disk rather
	// than reuse Cfg above, and why nil is a supported value.
	CatalogRefreshFor CatalogRefreshFactory

	// ReloadFor is /reload's own hot-apply seam (F17, reload.go's own
	// ReloadFactory doc comment) — see Root.reloadFor's own comment for
	// the §6.1 boundary this crosses and why nil is a supported value.
	ReloadFor ReloadFactory

	// PathLister is F18's own "@" path-completion seam (atmenu.go) — see
	// Root.pathLister's own comment for the §6.1 boundary this crosses,
	// why it is called synchronously rather than wrapped in a tea.Cmd,
	// and why nil is a supported value.
	PathLister PathLister

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

	// Skills is the rung-0 prose capability listing (§19.2/§19.4, Step 19),
	// already resolved once by internal/app.SystemPrompt's own
	// skills.Discover(cfg.Tools.SkillsDir) call — see Root.skills' own
	// comment for why this package never calls Discover itself. The zero
	// value (skills.Result{}) is a legitimate "nothing configured", the
	// same supported-empty rule Catalog above already follows.
	Skills skills.Result

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

	// FallbackModel mirrors [app].fallback_model (config.App.FallbackModel,
	// §4.2's Ref form, same shape as Model/CompactModel above) — Phase 4's
	// "automatic fallback to fallback_model if the active one fails twice
	// in a row" (docs/PLAN.md §11). Empty is the documented default
	// ("no separate fallback"): checkFallback (root.go) never fires with
	// nothing configured, the same "nothing wired, nothing happens" rule
	// EvolveStore/Recorder/SessionLister above already establish.
	FallbackModel string

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

	// ToolsLister is /tools' own read side (§13, Step 20) — see
	// Root.toolsLister's own comment for the §6.1 boundary this crosses
	// (internal/tools pulls net/http in transitively, so this package
	// never imports it directly) and why nil is a supported value.
	ToolsLister ToolsLister

	// PermissionsLister is /permissions' own read side (§13, Step 32) —
	// see Root.permissionsLister's own comment for the §6.1 boundary this
	// crosses (internal/permissions is a fine import for this package
	// already, via MissionGuard/toolapprove.go's own Request/Decision/
	// Tier vocabulary, but a *permissions.Guard itself still carries a
	// mutex and a config.Permissions this package has no business
	// constructing on its own) and why nil is a supported value.
	PermissionsLister PermissionsLister

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

	// EvolveStore is §19.7's own read/write seam over usage.jsonl and
	// suggest-state.json (suggest.go's own doc comment on why an
	// interface, not two path strings this package would then have to
	// open itself — see EvolveStore's own comment for the §6.1 reasoning).
	// nil means the suggestion feature is inert: checkSuggest never
	// offers anything, the same "nothing wired, nothing happens" default
	// Recorder/SessionLister/EngineFactory above already establish.
	EvolveStore EvolveStore

	// EvolveThresholds, SuggestPerSession, SuggestPerWeek and
	// DecayAfterRejects mirror [tools.evolve]/[tools].max_tools, already
	// translated by internal/app's own evolveThresholds — see
	// Root.evolveThresholds' own comment for why this package takes the
	// plain evolve.Thresholds struct rather than config.Evolve itself
	// (§6.1: tui never imports internal/config's schema beyond the one
	// *config.Config it already carries for [ui]/[keys], and evolve.Evaluate's
	// own doc comment already draws this same line for internal/tools).
	EvolveThresholds  evolve.Thresholds
	SuggestPerSession int
	SuggestPerWeek    int
	DecayAfterRejects int

	// NeedsTrust is true when internal/app looked up internal/trust.Store
	// for this project's path (or any ancestor) and found no saved
	// decision — §21.4 layer 2's own "first run in a directory that has
	// no saved decision" condition. NewRoot opens ModeTrust instead of
	// ModeChat exactly when this is true; false (the zero value, and
	// every test in this package) keeps every existing caller on the
	// pre-Step-30 behaviour of starting directly in ModeChat.
	NeedsTrust bool

	// InitialAutonomy is the status line's own starting word (footer.go's
	// FooterState.Autonomy) for a project that already answered §21.4
	// layer 2 — internal/app's own trust.Store.Lookup hit, or, absent
	// that, [autonomy].default (config.Autonomy.Default) translated
	// through permissions.ParseAutonomy(...).String() the same way
	// internal/app never hands this package a *permissions.Autonomy
	// value directly (§6.1, the exact rule FooterState.Autonomy's own
	// doc comment states). Empty is "not wired" — every test in this
	// package, and any caller from before this field existed — which
	// draws nothing in the footer, the same as an untouched
	// FooterState.Autonomy already does. Ignored when NeedsTrust is
	// true: resolveTrust (trust.go) sets m.footer.Autonomy itself once
	// the dialog closes, so a caller passes at most one of the two.
	InitialAutonomy string

	// GitInGit, GitClean and GitBranch are internal/app.DetectGit's own
	// three fields (internal/app/gitstatus.go), flattened rather than
	// passed through as that package's GitInfo type: internal/tui can
	// never import internal/app (§6.1's own one-way rule — app.go's
	// package comment names app as "the only package authorized to
	// import both internal/config and internal/tui"), so these three
	// plain fields travel the same way FooterState.GitBranch already
	// does for an unrelated concern. All three are zero value ("not a
	// repository") for a caller that never calls DetectGit, which is
	// exactly what an ordinary non-git directory should show in the
	// trust dialog's own "git: yes/no" line.
	GitInGit  bool
	GitClean  bool
	GitBranch string

	// TrustStore persists §21.4 layer 2's own decision (trust.go's own
	// doc comment on the §6.1 seam this draws) — see Root.trustStore's
	// own comment for why nil is a supported value.
	TrustStore TrustStore

	// CurationStore persists F5/Layer 2's own hide/keep decisions
	// (curation.go's own doc comment on the §6.1 seam this draws) — see
	// Root.curationStore's own comment for why nil is a supported value.
	CurationStore CurationStore

	// Hidden is internal/app's own applyCuration audit trail
	// (app.CatalogSnapshot.Hidden's own doc comment): every model Layer
	// 0/1/2 curation removed from Catalog above, and why. It exists so
	// design doc §2.3's second closing criterion — "/model says [a hidden
	// model] is hidden rather than failing" (principle 4) — can be
	// answered for automatic-rule hides too, not just the user's own
	// CurationStore.Reason (which only ever knows about curation.json's
	// own entries, see that interface's own doc comment). A plain
	// []catalog.Hidden, not a richer app-side type, because
	// internal/catalog is one of the two pure packages every boundary may
	// import (§6.1) — unlike CatalogSnapshot itself, which stays on
	// internal/app's side of the line. nil is a supported value (every
	// test in this package, and any session where curation hid nothing):
	// runModelCommand's hidden-fallback lookup simply never finds
	// anything, the same "session behaves correctly, just answers less"
	// degradation CurationStore's own nil case already establishes.
	Hidden []catalog.Hidden

	// MissionGuard is §21.6's own enforcement seam (mission.go's own doc
	// comment) — see Root.missionGuard's own comment for why nil is a
	// supported value. internal/app is expected to pass the same
	// *permissions.Guard already bound to AgentOptions.Runner (buildAgentOptions'
	// own guard variable), since that is the exact Guard whose
	// Authorize calls need to see a confirmed mission's rules.
	MissionGuard MissionGuard

	// MissionPolicy is §21.6's own second dialog-opening trigger's bridged
	// config — see Root.missionPolicy's own comment for why this is a
	// *mission.Policy (nil is the supported "trigger never fires" value)
	// rather than a plain mission.Policy. internal/app is expected to
	// convert the same cfg.Tools.Permissions already threaded through
	// permissions.New elsewhere in that package, mirroring how
	// MissionGuard (above) bridges *permissions.Guard.
	MissionPolicy *mission.Policy

	// MissionRecorder is §21.16 decision 3's own persistence seam — see
	// Root.missionRecorder's own comment and session.go's own
	// MissionRecorder doc comment for why this is a separate interface
	// from Recorder above, even though internal/app is expected to
	// implement both over the same *convo.Store/*convo.Conversation pair
	// Recorder already uses. nil is the supported "do not save" value.
	MissionRecorder MissionRecorder

	// RestoredMissions is every MissionEvent a resumed conversation
	// already carried on disk (§21.16 decision 3) — internal/app reads
	// this straight off resumedConv.Missions (convo.Store.Load's own
	// field) and hands it here unmodified, the same "the caller has
	// already read it off disk" rule History's own doc comment states for
	// messages. Empty for a fresh session, or a resumed one whose goal
	// never carried a recognized constraint — see NewRoot's own comment
	// on why this replays through the exact same code path a live
	// mission resolution already uses, rather than a second, parallel one.
	RestoredMissions []convo.MissionEvent
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

	// startMode is ModeChat for every pre-Step-30 caller (every test in
	// this package, and any real run in an already-trusted project) and
	// ModeTrust exactly when internal/app found no saved §21.4 layer 2
	// decision for this project — see Options.NeedsTrust's own comment.
	startMode := ModeChat
	if o.NeedsTrust {
		startMode = ModeTrust
	}

	r := Root{
		version:           o.Version,
		cwd:               o.CWD,
		mode:              startMode,
		lay:               lay,
		styles:            styles,
		themesDir:         o.ThemesDir,
		themeStore:        o.ThemeStore,
		titleStore:        o.TitleStore,
		settingsStore:     o.SettingsStore,
		reloadFor:         o.ReloadFor,
		pathLister:        o.PathLister,
		trustStore:        o.TrustStore,
		curationStore:     o.CurationStore,
		hidden:            o.Hidden,
		gitInGit:          o.GitInGit,
		gitClean:          o.GitClean,
		gitBranch:         o.GitBranch,
		missionGuard:      o.MissionGuard,
		missionPolicy:     o.MissionPolicy,
		missionRecorder:   o.MissionRecorder,
		input:             NewInput(lay.InputPrefix()),
		fps:               fps,
		cfg:               o.Cfg,
		cfgBanner:         o.Cfg == nil || o.Cfg.UI.Banner,
		cfgSyntax:         o.Cfg == nil || o.Cfg.UI.Syntax,
		cfgMarkdown:       o.Cfg == nil || o.Cfg.UI.Markdown,
		cfgReasoning:      reasoningModeOr(o.Cfg),
		animMode:          anim.Mode,
		cap:               o.Cap,
		tuiMode:           o.TUIMode,
		exitTranscript:    o.FullscreenExitTranscript,
		eng:               engineOr(o.Engine),
		engineFor:         o.EngineFor,
		effortFor:         o.EffortFor,
		loginFor:          o.LoginFor,
		catalogRefreshFor: o.CatalogRefreshFor,
		model:             model,
		system:            o.System,
		commands:          slash.Default(),
		cat:               o.Catalog,
		skills:            o.Skills,
		alias:             o.Alias,
		preferFree:        o.PreferFree,
		favorites:         o.Favorites,

		compactEng:           o.CompactEngine,
		compactModel:         o.CompactModel,
		compactAuto:          o.CompactAuto,
		compactTriggerPct:    o.CompactTriggerPct,
		compactKeepLastTurns: compactKeepLastTurns,
		compactStrategy:      compactStrategy,
		compactOnError:       compactOnError,

		fallbackModel: o.FallbackModel,

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

		// toolsLister is /tools' own read side (§13, Step 20) — see
		// TestOptionsToolsListerIsWiredIntoRoot for the regression test
		// this line exists to satisfy, the same shape
		// TestOptionsSessionListerIsWiredIntoRoot already established for
		// sessionLister above.
		toolsLister: o.ToolsLister,

		// permissionsLister is /permissions' own read side (§13, Step 32)
		// — see TestOptionsPermissionsListerIsWiredIntoRoot for the
		// regression test this line exists to satisfy, the same shape
		// TestOptionsToolsListerIsWiredIntoRoot already established just
		// above.
		permissionsLister: o.PermissionsLister,

		// toolsEnabled/agentOpts are Step 16's fork in startEngineTurn
		// (root.go) — see Options.ToolsEnabled's own comment for why a
		// bare Options (every test in this package predating this step)
		// keeps taking the plain-streaming path unchanged.
		toolsEnabled: o.ToolsEnabled,
		agentOpts:    o.AgentOptions,

		// evolveStore/evolveThresholds/suggestPerSession/suggestPerWeek/
		// decayAfterRejects are Step 25's own resolved-value set (§19.7)
		// — see Root.evolveStore's own comment for why a nil store is a
		// legitimate, silent "suggestions are off" rather than an error.
		evolveStore:       o.EvolveStore,
		evolveThresholds:  o.EvolveThresholds,
		suggestPerSession: o.SuggestPerSession,
		suggestPerWeek:    o.SuggestPerWeek,
		decayAfterRejects: o.DecayAfterRejects,

		// History (--resume, resume_last, /resume — §13) has to land in two
		// places, not one: m.conv, because it is what the *next* request's
		// Active() call sends to the provider, and m.transcript, because
		// it is what the user actually sees on reopening. Writing only the
		// first would resume the model's memory while showing a blank
		// screen; writing only the second would show old messages that a
		// reply built on top of would then contradict. historyToTranscript
		// (resume.go) is the same conversion finishTurn/submit apply live,
		// applied here in one pass at construction instead of one entry at
		// a time as the conversation unfolds. lay is already built above
		// (this composite literal cannot reference r.lay, since r does not
		// exist yet), so historyToTranscript's own glyph lookup (needed to
		// reconstruct each tool-using turn's toolActivityLines summary —
		// see its own doc comment) reads straight off that local instead.
		transcript: historyToTranscript(lay.glyphs(), o.History),
	}
	r.conv.Messages = o.History
	// RestoredMissions (§21.16 decision 3) gets its own notice appended
	// after History's own entries — a resumed session's constraints are
	// shown as the last thing in the reopened transcript, the same
	// position a live mission's own dialogs would have left a fresh
	// notice in had they just resolved, not spliced in among old
	// messages by timestamp.
	if notice := restoredMissionsNotice(lay.glyphs(), o.RestoredMissions); notice != nil {
		r.transcript = append(r.transcript, *notice)
	}
	if o.Cfg != nil {
		r.keys = NewMap(o.Cfg.Keys)
		r.footerItems = o.Cfg.UI.Footer.Items
	} else {
		r.keys = defaultMap
	}
	// CWD is deliberately not stored in the footer state: it depends on the
	// terminal width, so it is computed on every render by Root.footerState.
	// Autonomy is o.InitialAutonomy's own value here (empty unless
	// internal/app already resolved a trust.Store decision, or
	// [autonomy].default, into a word) — a Root that instead opens
	// ModeTrust below overwrites this the moment resolveTrust runs, so
	// the two never actually disagree about what the footer shows.
	r.footer = FooterState{Model: model, Autonomy: o.InitialAutonomy}
	SetInputWidth(&r.input, r.lay)

	// trust is only built when it will actually be shown: every other
	// caller (an already-trusted project, or any test in this package
	// that never sets NeedsTrust) leaves it at trustDialog{}, the same
	// "do not build state nobody asked for" rule themePicker/confirm
	// already follow for their own overlays.
	if startMode == ModeTrust {
		r.trust = newTrustDialog(o.CWD, o.GitInGit, o.GitClean, o.GitBranch)
	}
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
		if m.mode != ModeBusy && m.mode != ModeCompact && m.mode != ModeToolApprove && m.mode != ModeAskUser && m.mode != ModeLogin {
			return m, nil
		}
		m.animOffset++
		return m, tickAnim(m.fps)

	case streamTickMsg:
		// agentStream is set exactly while a tools-enabled turn is live
		// (startAgentTurn) and buf exactly while a plain streamed one is
		// (startEngineTurn's own branch) — never both at once, since
		// toolsEnabled picks one path per turn — so checking agentStream
		// first and falling through to the plain m.live.active/drainStream
		// check otherwise routes every tick to the one drain function that
		// actually owns it, with no ambiguity between the two.
		if m.agentStream != nil {
			return m.drainAgentStream()
		}
		if !m.live.active {
			return m, nil
		}
		return m.drainStream()

	case quitConfirmMsg:
		m.quitPresses = 0
		return m, nil

	case modelChosenMsg:
		return m.applyModelChosen(msg.Ref)

	case sessionChosenMsg:
		return m.applySessionChosen(msg.ID)

	case CatalogRefreshedMsg:
		return m.applyCatalogRefreshed(msg)

	case ReloadedMsg:
		return m.applyReloaded(msg)

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

	case AskUserRequestMsg:
		// Same reasoning as ToolApproveRequestMsg above: a turn may call
		// ask_user more than once, so any agent turn currently in ModeBusy
		// is a legitimate turn that may legitimately ask again.
		if m.mode != ModeBusy {
			return m, nil
		}
		return m.openAskUser(msg)

	case PhaseWaitMsg:
		// Same "outlived its turn" family as ToolApproveRequestMsg/
		// AskUserRequestMsg above: a stale wait notification from a turn
		// cancelAgentTurn already ended has no footer left worth updating,
		// and — unlike those two — there is no dialog state to leave
		// dangling if it is simply dropped, since OnWait never blocks on a
		// reply.
		if m.mode != ModeBusy {
			return m, nil
		}
		return m.applyPhaseWait(msg)

	case loginCodeMsg:
		// Same "outlived its turn" guard compactDoneMsg's own case
		// applies: cancelLogin already moved mode back to ModeChat
		// (esc/ctrl+c during the brief device-code request), and a
		// stale answer from that abandoned call has nothing left to
		// update.
		if m.mode != ModeLogin {
			return m, nil
		}
		return m.finishLoginCode(msg.code, msg.waiter, msg.err)

	case loginDoneMsg:
		if m.mode != ModeLogin {
			return m, nil
		}
		return m.finishLogin(msg.note, msg.err)

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

	case tea.MouseWheelMsg:
		// Bug 1's own event: only ever meaningful in fullscreen, where
		// emit (view.go) claims MouseModeCellMotion specifically so a
		// real tea.MouseWheelMsg reaches here instead of xterm's mode
		// 1007 silently rewriting the tick into an "up"/"down"
		// tea.KeyPressMsg first. Global rather than folded into
		// handleGlobalKey (which only ever sees tea.KeyPressMsg):
		// scrolling to reread something is exactly the same "harmless to
		// do while a turn is running, and simply unreachable from every
		// overlay mode since each one's own updateX claims the keyboard
		// first" case ToggleFold's own handleGlobalKey comment already
		// describes, so it is handled the same way — before the mode
		// switch, not gated to ModeChat.
		if m.tuiMode != termenv.ModeFullscreen {
			return m, nil
		}
		return m.scrollWheel(msg.Button), nil

	case tea.KeyPressMsg:
		if handled, next, cmd := m.handleGlobalKey(msg); handled {
			return next, cmd
		}
	}

	// Capa 2: delega al componente enfocado según el modo.
	switch m.mode {
	case ModeHelp:
		return m.updateHelp(msg)
	case ModeHotkeys:
		return m.updateHotkeys(msg)
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
	case ModeAskUser:
		return m.updateAskUser(msg)
	case ModeQueueEdit:
		return m.updateQueueEdit(msg)
	case ModeLogin:
		return m.updateLogin(msg)
	case ModeSuggest:
		return m.updateSuggest(msg)
	case ModeThemePicker:
		return m.updateThemePicker(msg)
	case ModeTrust:
		return m.updateTrust(msg)
	case ModeMission:
		return m.updateMission(msg)
	case ModeToolScope:
		return m.updateToolScope(msg)
	default:
		return m.updateChat(msg)
	}
}

// handleGlobalKey resuelve Quit (con la ventana de N pulsaciones de
// §7.4 / keys.QuitRepeat) y ctrl+l, que funcionan en cualquier modo.
// Devuelve handled=false para que el switch de modo procese cualquier otra tecla.
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
		if m.mode == ModeAskUser {
			// Same reasoning again: the agent loop's goroutine is parked
			// behind this exact dialog (see askuser.go's own comment on
			// updateAskUser's esc/Cancel case), so a lone ctrl+c must
			// resolve it rather than risk quitting the whole program while
			// a goroutine is still blocked waiting on it.
			next, cmd := m.cancelAgentTurn()
			return true, next, cmd
		}
		need := m.keys.QuitRepeat
		if need < 1 {
			need = 2 // shipped default if a Map was built without NewMap
		}
		m.quitPresses++
		if m.quitPresses >= need {
			m.quitting = true
			return true, m, tea.Quit
		}
		// Arm the grace window on the first press only. Later presses
		// inside it just count; a new timer per press would race the
		// previous one's quitConfirmMsg and reset the count.
		if m.quitPresses == 1 {
			return true, m, tea.Tick(time.Second, func(time.Time) tea.Msg { return quitConfirmMsg{} })
		}
		return true, m, nil

	case m.keys.ClearScreen:
		m.transcript = nil
		m.printedUpTo = 0
		m.scrollOffset = 0
		return true, m, clearAndWipeCmd()

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

	case m.keys.ThemePicker:
		// Same ModeChat-only gating as ModelPicker above: ModeBusy is
		// generating (§7.4 already reserves esc/ctrl+c there) and every
		// overlay mode owns the keyboard outright, so a second ctrl+t is
		// swallowed rather than reopening an overlay already open.
		if m.mode != ModeChat {
			return true, m, nil
		}
		next, cmd := m.openThemePicker()
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

	case m.keys.ToggleFold:
		// Unlike ModelPicker/ThemePicker/CopyLast above, this is not gated
		// to ModeChat: folding is a read-only view toggle over whatever is
		// already on screen, not an action that starts something new, so it
		// stays useful while ModeBusy is still streaming a long code block
		// (arguably the moment it is most wanted) and does nothing harmful
		// in any overlay mode either — it just is not reachable there,
		// since every overlay mode's own updateX claims the keyboard first
		// per handleGlobalKey's own doc comment.
		m.foldCode = !m.foldCode
		return true, m, nil

	case m.keys.EffortCycle:
		// Same "not gated to ModeChat" reasoning as ToggleFold above:
		// cycling the effort level for the *next* turn is harmless to
		// press while the current one is still streaming (ModeBusy), and
		// every overlay mode's own updateX already claims the keyboard
		// first, so this is simply unreachable there rather than needing
		// its own guard. cycleEffort (effortcmd.go) is itself a silent
		// no-op when the active model has no discrete effort levels, so
		// no notice is shown either way — a chord is not a place to
		// explain why it did nothing.
		m = m.cycleEffort()
		return true, m, nil

	case m.keys.ScrollUp:
		// Bug 1's keyboard half, alongside the tea.MouseWheelMsg case in
		// updateDispatch above — handled here, before the mode switch,
		// for the same reason ToggleFold's own comment gives: harmless
		// to press mid-turn, and simply unreachable from any overlay
		// mode since each claims the keyboard first. Handling it here
		// specifically (rather than letting it fall through to
		// updateChat/updateBusy's own tail, m.input.Update) is also what
		// keeps it from ever reaching bubbles/v2's textarea, whose own
		// KeyMap.PageUp binds the exact same "pgup" chord to moving the
		// cursor inside the box — see Root.scrollOffset's own doc
		// comment for why that box's cursor is not what this chord is
		// for in fullscreen.
		//
		// Regular mode deliberately returns handled=false instead of
		// swallowing the key: it has no scrollOffset concept (its
		// scrollback is the terminal's own, per emit's own doc comment),
		// so pgup/pgdown there fall through exactly as before this fix —
		// to bubbles/v2's textarea's own PageUp/PageDown, moving the
		// cursor inside a multi-line draft. Claiming the chord globally
		// even in regular mode would have been a regression: it was
		// never idle before this feature existed, and the config default
		// (defaults.toml) only introduced a *fullscreen* meaning for it.
		if m.tuiMode != termenv.ModeFullscreen {
			return false, m, nil
		}
		return true, m.scrollBy(m.headBudget()), nil

	case m.keys.ScrollDown:
		if m.tuiMode != termenv.ModeFullscreen {
			return false, m, nil
		}
		return true, m.scrollBy(-m.headBudget()), nil
	}
	return false, m, nil
}

// scrollWheel is one mouse-wheel tick's worth of Root.scrollOffset movement
// — 3 rows per tick, the same MouseWheelDelta bubbles/v2's own viewport
// widget defaults to, so a wheel tick here feels like the same amount of
// content bubbles/v2's other scrollable widgets already move per tick,
// rather than inventing a new, unfamiliar step size. Any button other than
// MouseWheelUp/MouseWheelDown (a click, a horizontal wheel push) is a no-op:
// this package has no other mouse feature to route it to yet.
func (m Root) scrollWheel(button tea.MouseButton) Root {
	const wheelStep = 3
	switch button {
	case tea.MouseWheelUp:
		return m.scrollBy(wheelStep)
	case tea.MouseWheelDown:
		return m.scrollBy(-wheelStep)
	default:
		return m
	}
}

// scrollBy moves Root.scrollOffset by delta rows — positive scrolls back
// towards the start of the transcript, negative scrolls forward towards the
// live tail — and clamps both ends: never negative, and never past
// maxScrollOffset() (view.go), the exact same ceiling clipHead's own
// per-frame clamp computes.
//
// This method used to clamp only the floor, deliberately leaving the
// ceiling to clipHead's own per-frame clamp — reasoning that this method
// "has no access to the frame clipHead will actually draw against". That
// was wrong in a way a user actually hit: clipHead's clamp is purely local
// to one render call, so it can hide the overflow *visually* on every frame
// without that clamped value ever being written back to m.scrollOffset
// itself. The field kept accumulating past what was visible — scroll up 50
// wheel-lines' worth when only ~10 lines of headroom exist, and the view
// stops moving after 10 but m.scrollOffset still ends up at 150 rows'
// worth — so scrolling back down had to first "pay back" the whole
// invisible 140-row debt before the view moved at all. Clamping here too
// closes that gap: maxScrollOffset() (view.go) calls m.headContent()/
// m.headBudget() itself, using this Update-time Root's own current state
// exactly the way headBudget/headContent are read fresh on every other call
// in this package (no memoization anywhere here to begin with, so "this
// call's Root" already is "the frame about to be drawn" for every other
// value scrollBy's callers already depend on — m.transcript, m.live, m.lay
// — none of which get any staler by also reading them here). A resize
// riding the same tea.Msg batch still cannot desync the two clamps: both
// this one and clipHead's ultimately call the identical
// maxScrollOffsetFor(n, budget) (view.go), so whichever frame's n/budget
// either one sees, they agree by construction.
func (m Root) scrollBy(delta int) Root {
	m.scrollOffset += delta
	if m.scrollOffset < 0 {
		m.scrollOffset = 0
	}
	if max := m.maxScrollOffset(); m.scrollOffset > max {
		m.scrollOffset = max
	}
	return m
}

// openPicker switches to ModePicker with a Picker built from the current
// catalog and prefiltered with query — "" for ctrl+p and a bare /model, or
// whatever text the user typed after /model when it did not resolve
// unambiguously (§4.5's OutcomePicker).
func (m Root) openPicker(query string) (tea.Model, tea.Cmd) {
	m.picker = newPicker(m.cat, m.resolveOptions(), m.favorites, m.model, query, m.curationStore)
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

// applyCatalogRefreshed is CatalogRefreshedMsg's only handler. msg.Catalog
// is nil when app.BackgroundRefresh could not improve on the catalog
// LoadCatalog already handed Root at startup (network unreachable, every
// provider timed out) — swapping m.cat for nothing would turn a working
// picker into an empty one over something that was never the user's fault,
// so that case is a no-op.
//
// When the picker is open (ModePicker) at the moment the refresh lands, it
// is rebuilt against the new catalog rather than left stale or closed: the
// user is very possibly looking at exactly the "13" in "models · 13" this
// refresh is about to change, and closing the overlay out from under an
// still-open selection would be a worse surprise than the row list moving
// under their cursor.
//
// msg.Cfg is F2's own addition (docs/ROADMAP-ux-2026-08-20.md's W4,
// catalogrefresh.go): nil for the pre-existing §4.4/§11 background refresh
// (its cfg never changed), non-nil for a hot /login apply. When present, it
// is copied into the *config.Config m.cfg already points at — *m.cfg =
// *msg.Cfg, not m.cfg = msg.Cfg — deliberately: engineFor (see
// Root.engineFor's own comment) was built once, at boot, by
// app.NewEngineFactory(cfg, ...), and that closure holds the exact pointer
// value cfg was at construction time, not a way to observe Root.cfg's own
// field being reassigned later. Copying the fresh value's fields into the
// same struct that pointer already refers to is what actually makes a
// freshly-authenticated provider reachable by the very next /model switch,
// with no restart and no rewiring of engineFor itself. This runs on
// Update's own single goroutine (the same one that may read m.cfg via
// /config, /debug), so it never races the tea.Cmd goroutine that produced
// msg.Cfg — see CatalogRefreshFactory's own comment for why that goroutine
// hands back an independent value instead of doing this same mutation
// itself. The nil-m.cfg fallback (every test in this package, and any
// caller that built a Root with no Options.Cfg) simply adopts the pointer
// directly: there is no pre-existing closure for such a Root to keep in
// sync anyway.
func (m Root) applyCatalogRefreshed(msg CatalogRefreshedMsg) (tea.Model, tea.Cmd) {
	if msg.Cfg != nil {
		if m.cfg != nil {
			*m.cfg = *msg.Cfg
		} else {
			m.cfg = msg.Cfg
		}
	}
	if msg.Catalog == nil {
		return m, nil
	}
	m.cat = msg.Catalog
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
//
// The trailing hiddenSuffixFor(ref) is design doc principle 4's "resolving
// one explicitly says so": committing a switch to a model m.hidden still
// remembers (an automatic [catalog.curate] rule, or a curation.json entry
// from before this session — see Options.Hidden's own doc comment) never
// looks like an ordinary switch, whether it was reached through
// runModelCommand's exact-ref fallback, a stale ctrl+p selection, or any
// other caller of this shared tail. An empty suffix (the overwhelmingly
// common, not-hidden case) leaves both notice lines byte-identical to
// before this field existed.
func (m Root) commitModelSwitch(ref string) (tea.Model, tea.Cmd) {
	next, err := switchEngine(m, ref)
	m = next
	m.model = ref
	m.footer.Model = ref
	suffix := m.hiddenSuffixFor(ref)
	if err != nil {
		return m.slashNotice(m.lay.glyphs().warnMark + " cambiado a " + ref +
			", pero no se pudo preparar ese proveedor: " + err.Error() + suffix)
	}
	return m.slashNotice(confirmLine(m.lay.glyphs(), ref) + suffix)
}

// hiddenSuffixFor is commitModelSwitch's own line-continuation: "" when ref
// is not in m.hidden, otherwise a second line naming the rule that hid it —
// hiddenRuleLabel's own wording (curation.go), so a chat notice and
// `ishakat models --why`'s "hidden by" column never disagree about what a
// given catalog.Reason is called.
func (m Root) hiddenSuffixFor(ref string) string {
	h, ok := m.hiddenByRef(ref)
	if !ok {
		return ""
	}
	return "\n" + m.lay.glyphs().warnMark + " este modelo esta escondido (" + hiddenRuleLabel(h.Reason) + ") — sigue siendo utilizable por ref exacto"
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
	m.transcript = historyToTranscript(m.lay.glyphs(), conv.Messages)
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
		// The "@" dropdown (F18, atmenu.go) claims the same keys next,
		// mutually exclusive with m.menu by construction: slashMenuFor
		// only ever opens on a whole-line "/" prefix, atMenuFor only ever
		// opens on a trailing "@" token, so the two conditions never hold
		// at once for the same input value.
		if m.atMenu.Active() {
			if handled, next, cmd := m.updateAtMenu(key); handled {
				return next, cmd
			}
		}
		switch key {
		case m.keys.Cancel:
			return m, nil // nada que cancelar en ModeChat
		case m.keys.EditQueue:
			// alt+up is also reachable here, not only from updateBusy's own
			// case — see ModeQueueEdit's own doc comment for why a
			// follow-up queued during a turn that has already ended (and
			// left mode at ModeChat, unsubmitted because checkFollowup was
			// pre-empted by checkAutoCompact/checkSuggest) still needs a
			// way back into view without starting a new turn first.
			return m.openQueueEdit()
		case m.keys.Submit:
			text := strings.TrimSpace(m.input.Value())
			if text == "" {
				return m, nil
			}
			if slash.IsCommand(text) {
				return m.runSlashLine(text)
			}
			// §21.6: a goal carrying a recognized constraint pauses here
			// on ModeMission instead of starting the turn immediately —
			// see checkMission's own comment for why most goals (no
			// recognized keyword) fall straight through unchanged. The
			// input is cleared either way, the same way submit's own
			// m.input.Reset() would have done in the ordinary case.
			if next, ok := m.checkMission(text); ok {
				next.input.Reset()
				return next, nil
			}
			// §21.6's second trigger, "the mission requests a capability
			// outside current policy" — only reached when checkMission
			// itself found no constraint to confirm: see checkToolPolicy's
			// own comment for why a goal that already opened ModeMission
			// must not be checked twice for the same dialog.
			if next, ok := m.checkToolPolicy(text); ok {
				next.input.Reset()
				return next, nil
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
			//
			// Deliberately does NOT recompute next.menu/next.atMenu from
			// the recalled text (unlike the post-switch fallthrough below,
			// which recomputes on every real keystroke). This is the fix
			// for the reported "browsing history gets permanently stuck
			// once it lands on a line that looks like a slash command"
			// bug: if a recalled entry (e.g. "/model") happened to open
			// m.menu here, the very next up/down press would be consumed
			// by updateSlashMenu's own unconditional up/down cases (this
			// same switch's m.menu.Active() branch, above) instead of
			// ever reaching HistoryPrev/HistoryNext again — with no way
			// back to browsing short of Esc (closes the menu) or Enter
			// (runs the command), which is exactly the "queda estancado"
			// symptom reported. Leaving the dropdown closed here costs
			// nothing browsing-history-wise: a real command needs its
			// argument typed to be useful, and the moment the user does
			// type into a recalled "/..." line, the fallthrough path a few
			// lines down recomputes the menu from that keystroke exactly
			// as it always has.
			if m.input.Line() == 0 {
				if next, ok := m.historyPrev(); ok {
					return next, nil
				}
			}
		case m.keys.HistoryNext:
			if m.input.Line() == m.input.LineCount()-1 {
				if next, ok := m.historyNext(); ok {
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
	m.atMenu = atMenuFor(currentWordAtEnd(m.input), m.pathLister, m.atMenu)
	return m, cmd
}

// updateBusy implements W2 item 3's non-modal ModeBusy (docs/ROADMAP-ux-
// 2026-08-20.md, F7/F3, DECISION-2 consequence 1): the input stays focused
// and editable, the §9.6 dropdown and a conservative allow-list of
// read-only slash commands work exactly as they do in ModeChat, and
// cancellation moves off plain Esc.
//
// Esc's new meaning (DECISION-2 consequence 1, ANSWERED): "keeps meaning
// cancel only while the input is empty — otherwise esc clears the editor".
// With text in the box, Esc clears it and stays in ModeBusy — the same
// "nothing left to lose by pressing it" safety a shell's own line editor
// gives an accidental keypress. With an empty box, Esc still cancels the
// turn exactly as before. ctrl+c is untouched: handleGlobalKey's own
// ModeBusy branch already special-cases it to the identical cancelTurn/
// cancelAgentTurn call, so no new chord had to be invented for "a chord
// that cannot be typed by accident mid-sentence" — ctrl+c already was one.
//
// The §9.6 dropdown and a fixed allow-list of Kinds are wired through the
// same runSlashLine/runSlashCommand/updateSlashMenu machinery updateChat
// already uses. The allow-list is deliberately narrow for this first
// slice: every Kind on it only ever calls slashNotice (theme.go/stats.go/
// configcmd.go/debugcmd.go/models.go/skills.go) and never changes m.mode,
// so none of them need a "return to ModeBusy, not ModeChat" mechanism —
// they simply never leave ModeBusy in the first place. Kinds that mutate
// turn-critical state (KindModel, KindNew, KindClear, KindCompact,
// KindResume, KindLogin, KindTrust, KindTools, KindPermissions, KindRetry,
// KindCopy, KindExit) or open a dedicated overlay Mode (KindHelp,
// KindHotkeys) stay out of this slice on purpose — see busyAllowedSlashKind's
// own doc comment for why each of those is deferred rather than folded in
// here.
//
// Printable keys, backspace, paste and the rest of what updateChat's own
// textarea.Update tail handles are fed into m.input unconditionally,
// mirroring updateChat's own tail. Submit (enter) with non-empty input
// that is not a recognised, allowed slash command reports an honest "not
// wired yet" notice instead of silently discarding what was typed or
// improvising W2 item 4's real steering queue.
func (m Root) updateBusy(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		text := keyPressString(key)

		if m.menu.Active() {
			if handled, next, cmd := m.updateSlashMenu(text); handled {
				return next, cmd
			}
		}
		// Wired the same as updateChat above: harmless while a turn is
		// running since completing a path never submits anything by
		// itself, only ever changes what is sitting in the input box.
		if m.atMenu.Active() {
			if handled, next, cmd := m.updateAtMenu(text); handled {
				return next, cmd
			}
		}

		switch text {
		case m.keys.Cancel:
			if strings.TrimSpace(m.input.Value()) != "" {
				m.input.Reset()
				m.menu = slashMenu{}
				m.atMenu = atMenu{}
				return m, nil
			}
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

		case m.keys.Submit:
			typed := strings.TrimSpace(m.input.Value())
			if typed == "" {
				return m, nil
			}
			if slash.IsCommand(typed) {
				return m.runBusySlashLine(typed)
			}
			// Ordinary chat text submitted mid-turn is W2 item 4's real
			// steering queue (queueSteering, steering.go): the message
			// becomes a real conversation event, shown in the transcript
			// right away, and delivered to the running loop only through
			// engine.AgentSink.Inject's own once-per-iteration poll — see
			// queueSteering's own doc comment for the full reasoning, and
			// this function's own doc comment for why that split (never
			// touching hist directly from here) is the point.
			return m.queueSteering(typed)

		case m.keys.QueueFollowup:
			// alt+enter (F13's second half, DECISION-2 consequence 3):
			// queues typed as a follow-up for *after* this turn, rather
			// than steering it in now — see tui.Map.QueueFollowup's own
			// doc comment (keys.go) for why this needs a distinct chord
			// from Submit at all. Only wired here, not in updateChat: with
			// no turn running there is no "after this turn" for a
			// follow-up to wait for, so alt+enter in ModeChat falls
			// through to the ordinary textarea handling below (which
			// inserts nothing for alt+enter, since bubbles/v2's own
			// textarea does not bind it) rather than silently queuing
			// something that might sit unsubmitted indefinitely.
			typed := strings.TrimSpace(m.input.Value())
			if typed == "" {
				return m, nil
			}
			return m.queueFollowup(typed)

		case m.keys.EditQueue:
			// alt+up (F13's other chord): re-opens the follow-up queue for
			// editing. Unlike QueueFollowup this is also reachable from
			// ModeChat (updateChat's own case below) — DECISION-2
			// consequence 3's "the queue survives a turn boundary" means a
			// follow-up queued during one turn can still be sitting there
			// once the interface is back in ModeChat (checkAutoCompact or
			// checkSuggest may have preempted checkFollowup at the moment
			// this turn ended), and reviewing/trimming it should not
			// require starting a new turn first.
			return m.openQueueEdit()

		case m.keys.Newline:
			m.input.InsertRune('\n')
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.menu = slashMenuFor(m.input.Value(), m.commands, m.menu)
	m.atMenu = atMenuFor(currentWordAtEnd(m.input), m.pathLister, m.atMenu)
	return m, cmd
}

// busyAllowedSlashKind reports whether cmd may run while ModeBusy, for this
// first slice of W2 item 3. Every Kind on the list is a pure slashNotice
// call (theme.go/stats.go/configcmd.go/debugcmd.go/models.go/skills.go):
// none of them ever change m.mode, so allowing them here needs no "return
// to ModeBusy, not ModeChat" mechanism — they simply never leave ModeBusy.
//
// Deliberately NOT on this list, and why:
//   - KindHelp/KindHotkeys: both switch to a dedicated overlay Mode
//     (ModeHelp/ModeHotkeys), whose own updateHelp/updateHotkeys
//     unconditionally return to ModeChat on close — opening either mid-turn
//     would silently drop back to ModeChat under a still-running turn.
//     Deferred to a follow-up slice that gives them their own "was this
//     opened from ModeBusy" fix, not folded in here.
//   - KindModel/KindCompact/KindNew/KindClear/KindResume/KindLogin/
//     KindTrust: each starts a new async flow, opens an overlay, or
//     mutates the conversation/session itself — exactly the turn-critical
//     state a non-modal ModeBusy must not let a second action race against
//     the one already running.
//   - KindTools/KindPermissions: their write halves (create/edit/delete a
//     tool; change autonomy) are governance actions, not read-only
//     reports; kept out entirely for this slice rather than splitting the
//     read/write halves of one Kind onto different allow-lists.
//   - KindRetry/KindCopy: KindRetry drops the assistant response and
//     resubmits — nonsensical while one is still being produced. KindCopy
//     copies "the last response", which is ambiguous mid-turn (the one
//     before this turn, or this turn's still-incomplete draft?).
//   - KindExit: quitting mid-turn is unrelated to this item's scope.
func busyAllowedSlashKind(k slash.Kind) bool {
	switch k {
	case slash.KindStats, slash.KindTheme, slash.KindConfig, slash.KindDebug,
		slash.KindModels, slash.KindSkills:
		return true
	default:
		return false
	}
}

// runBusySlashLine is updateBusy's own counterpart to runSlashLine: it
// parses exactly the same way, but a command that resolves to a Kind
// outside busyAllowedSlashKind's list reports that explicitly rather than
// running it — the same "point at the remedy instead of a silent gap"
// honesty runSlashLine's own unknown-command case already follows.
func (m Root) runBusySlashLine(text string) (tea.Model, tea.Cmd) {
	p := slash.Parse(text, m.commands)
	if !p.Found {
		m = m.recordHistory(text)
		return m.slashNotice(m.lay.glyphs().warnMark + " comando desconocido: /" + p.Raw)
	}
	if !busyAllowedSlashKind(p.Command.Kind) {
		m = m.recordHistory(text)
		g := m.lay.glyphs()
		return m.slashNotice(g.warnMark + " " + p.Command.Usage() + " no esta disponible mientras el turno trabaja")
	}
	return m.runSlashCommand(p.Command, p.Args)
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

// updateHotkeys maneja ModeHotkeys exactamente como updateHelp maneja
// ModeHelp: cualquier tecla cierra el overlay y vuelve a ModeChat. F3's own
// ask ("keep our ESC-dismissable overlay style") is honoured the same way
// ModeHelp already honours it — any key, not only Esc, since there is
// nothing else to do on this screen.
func (m Root) updateHotkeys(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(tea.KeyPressMsg); ok {
		m.mode = ModeChat
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

	// Sending a new message is the user asking to continue the
	// conversation, so it is reasonable to jump back to the live tail even
	// if they had scrolled up to reread something (Root.scrollOffset's own
	// doc comment). Reset before appending: the new entry belongs at the
	// tail scrollOffset is being reset towards, not somewhere a stale
	// offset from before this call would still be treating as "scrolled
	// back".
	m.scrollOffset = 0

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
		Params:   m.effortParams(),
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
	cmds = append(cmds, printBannerCmd(bannerText, m.tuiMode))
	return m, tea.Batch(cmds...)
}

// printBannerCmd is the one place that retires a startup banner to real
// scrollback (§7.5). startEngineTurn and startAgentTurn each independently
// did this inline (tea.Println(bannerText+"\n")) — same guard, same string,
// same trailing separator, duplicated across two call sites. Only one of
// them ever runs for a given turn (the tools-enabled fork in startEngineTurn
// picks exactly one), so no live double-print was reachable and
// TestBannerAppearsExactlyOnce already passed before this change — but the
// duplication in source was exactly the shape the roadmap's RC-5 section
// calls "two producers of the same rows," easy to drift apart the next time
// one call site changes and its twin does not.
//
// Returns nil, not a no-op Cmd, when there is nothing to print: tea.Batch
// already drops nil commands (see its own doc comment), so every caller can
// unconditionally append this without its own bannerText != "" check.
//
// mode == termenv.ModeFullscreen is an early-return, mirroring
// evictOverflow's own fullscreen guard exactly and for the identical
// reason: this function's only mechanism is tea.Println/insertAbove, and
// reading bubbletea's own cursed_renderer.go confirms insertAbove writes
// straight to the terminal (bypassing s.cellbuf/s.lastView, the renderer's
// own diff state) with no AltScreen check anywhere in its body — despite
// renderer.go's doc comment claiming "if the altscreen is active no output
// will be printed" (already flagged as a doc/code discrepancy in this
// project's own docs/PLAN.md, W3 part 6). In fullscreen there is no real
// scrollback for insertAbove to retire the banner into: AltScreen's buffer
// is the alternate screen, and writing into it out of band from the
// renderer's own frame desyncs the renderer's next diff from what is
// actually on screen — this was confirmed to be the mechanism behind the
// reported "sending the first message corrupts the whole interface" bug
// (W5 UI-bugs follow-up), reproduced with TestFirstMessageDoesNotCorruptFullscreen.
// Fullscreen needs no equivalent of this call at all: render() already
// recomputes bannerText() (which returns "" the instant the transcript is
// non-empty) from m.transcript on every frame, so the banner simply stops
// being drawn on the very next redraw — nothing needs to be separately
// "retired" the way regular mode's real terminal scrollback does.
func printBannerCmd(bannerText string, mode termenv.Mode) tea.Cmd {
	if bannerText == "" || mode == termenv.ModeFullscreen {
		return nil
	}
	// The trailing "\n" is the same blank separator line head() used to
	// leave between the banner and whatever came after it (see the "\n\n"
	// after Banner()'s call in head()); tea.Println already supplies the
	// one line break that ends bannerText's own last line.
	return tea.Println(bannerText + "\n")
}

// clearScreenCmd is tea.ClearScreen wrapped as a tea.Cmd, the shape
// handleGlobalKey's /clear (ctrl+l) needs: unlike the banner-retirement case
// above, a user-requested clear has no "print it to scrollback first" option
// — the whole point of ctrl+l is discarding the transcript, not archiving it.
func clearScreenCmd() tea.Msg { return tea.ClearScreen() }

// wipeScrollbackCmd sends ESC[3J directly, in addition to whatever
// tea.ClearScreen() already does. tea.ClearScreen (and the renderer's
// pendingErase it sets) only erases the visible screen buffer — real
// terminal scrollback survives it untouched, which is B3: ctrl+l/`/clear`
// looked like they wiped the pane, but scrolling up still showed the old
// conversation. ansi.EraseEntireDisplay is ESC[3J, the xterm extension that
// also drops scrollback; charmbracelet/x/ansi is already a direct
// dependency (picker.go, slashmenu.go, wrap.go), so this adds nothing new
// to the module graph — a real requirement on Termux, per
// docs/DESIGN-tui-mode.md. tea.Raw is already a tea.Cmd (func() Msg) that
// yields a RawMsg; it is used directly as the Cmd rather than wrapped in
// another function, because wrapping it here would hand the event loop a
// Cmd value where it expects the Msg the Cmd produces, which type-checks
// (Msg is `any`) but never matches `case RawMsg:` — a real mistake made
// and caught while writing this fix. RawMsg hands the bytes straight to
// the program's output stream (p.execute), bypassing the renderer
// entirely, which is correct here: this is not a screen *repaint*, it's a
// side-channel terminal control sequence the renderer has no model for.
var wipeScrollbackCmd tea.Cmd = tea.Raw(ansi.EraseEntireDisplay)

// clearAndWipeCmd is what every user-requested "start this pane over"
// action (ctrl+l, /clear, /new) should return: both the erase-visible-
// screen command bubbletea already understood, and the erase-scrollback
// sequence it didn't. Order does not matter to the terminal (both are
// unconditional erases) but tea.Batch does not guarantee ordering anyway,
// so nothing here should ever come to depend on it.
//
// applySessionChosen (post-/resume redraw) deliberately does NOT use this:
// loading a different session is not the user asking to discard scrollback
// — the previous session's transcript is still information they might
// scroll back to, not clutter to erase — so it keeps using bare
// clearScreenCmd.
func clearAndWipeCmd() tea.Cmd { return tea.Batch(clearScreenCmd, wipeScrollbackCmd) }

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
		reasoning: m.live.reasoning(),
	})

	// checkFallback's own counter (§11 Phase 4): a real provider failure
	// (err != nil) extends the streak; anything else — a clean answer, or
	// the user's own esc/ctrl+c (aborted, never the model's fault) — resets
	// it. See checkFallback's doc comment for why this lives here instead
	// of inside it: the streak has to reflect every turn that ever closed
	// through finishTurn, not just the ones checkEndOfTurn happens to run
	// a fallback check after.
	if err != nil {
		m.consecutiveFailures++
	} else {
		m.consecutiveFailures = 0
	}

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

	return m.checkEndOfTurn()
}

// checkEndOfTurn is finishTurn's and finishAgentTurn's actual shared
// tail: run §11 Phase 4's automatic-fallback check first, then §10's
// auto-compact check, and only offer §19.7's crystallization suggestion
// (checkSuggest, suggest.go) if that second check left the turn fully
// settled in ModeChat — never behind checkAutoCompact's own async
// ModeCompact overlay. Order matters here for the same reason it matters
// in confirmOptionsFor's own priority comment: a suggestion dialog opening
// on top of (or racing) an in-flight compaction would be asking the user
// to read two unrelated things at once, and compaction is the one of the
// two that must not be delayed — an over-full context window breaks the
// very next request, while a crystallization offer can always wait for
// the next turn that ends cleanly.
//
// checkFallback runs first, ahead of both, because it can change m.model:
// checkAutoCompact's own window lookup (m.cat.Get(m.model)) has to see
// whatever model is active by the end of this turn, not the one that just
// failed twice and is about to be abandoned. checkFallback is always
// synchronous (a plain relabel-and-rebuild, exactly like commitModelSwitch)
// and its own tea.Cmd is always nil — there is nothing to wait on, so
// discarding it here rather than batching it with checkAutoCompact's is
// not a bug, only a simplification checkFallback's own doc comment repeats.
func (m Root) checkEndOfTurn() (tea.Model, tea.Cmd) {
	next, _ := m.checkFallback()
	r, ok := next.(Root)
	if !ok {
		return next, nil
	}
	next, cmd := r.checkAutoCompact()
	r2, ok := next.(Root)
	if !ok || r2.mode != ModeChat {
		return next, cmd
	}
	next, cmd = r2.checkSuggest()
	// checkFollowup (W2 item 4, F13, steering.go) is chained in last, on
	// the exact same "nothing else pending" gate checkSuggest's own call
	// above already applies: a follow-up must not preempt a compaction
	// or a crystallization offer that also wants this exact moment, and
	// must not fire at all if either of those instead opened its own
	// dialog (mode would no longer be ModeChat here). Reaching this line
	// with mode == ModeChat means every prior end-of-turn check found
	// nothing to do, which is exactly when DECISION-2 consequence 3 says
	// a queued follow-up should become "the next thing submitted."
	if r3, ok := next.(Root); ok && r3.mode == ModeChat {
		return r3.checkFollowup()
	}
	return next, cmd
}

// checkFallback is checkEndOfTurn's own first half, implementing §11 Phase
// 4's "automatic fallback to fallback_model if the active one fails twice
// in a row — OmniRoute already does this internally, but a user pointing
// directly at a provider needs it." consecutiveFailures is finishTurn's/
// finishAgentTurn's own streak (see their comments on why an aborted or
// successful turn resets it to zero): reaching 2 fires the switch exactly
// once and resets the counter immediately, so a fallback_model that itself
// keeps failing is not retried on every subsequent turn — it only fires
// again after two more failures against whatever model ends up active.
//
// fallbackModel == "" (defaults.toml's documented meaning: "no separate
// fallback") or already equal to m.model (nothing to switch to, e.g. the
// fallback itself is the one that just failed twice) both leave this a
// no-op — the same "nothing configured, nothing happens" rule checkSuggest's
// own evolveStore == nil guard already follows for §19.7.
//
// The switch itself reuses switchEngine (engine.go), the exact seam
// commitModelSwitch already calls for /model and the picker — see its own
// comment for why relabelling m.model without rebuilding m.eng was the
// original bug this whole mechanism has to avoid repeating. This does not
// call commitModelSwitch directly because that function's own confirmLine
// notice ("── now: X ──") reads as a choice the user just made; an
// automatic recovery has to say plainly that it was automatic and why, or
// the switch would look like an unexplained ctrl+p the user never pressed.
func (m Root) checkFallback() (tea.Model, tea.Cmd) {
	if m.fallbackModel == "" || m.fallbackModel == m.model || m.consecutiveFailures < 2 {
		return m, nil
	}
	from := m.model
	m.consecutiveFailures = 0
	next, err := switchEngine(m, m.fallbackModel)
	m = next
	m.model = m.fallbackModel
	m.footer.Model = m.fallbackModel
	notice := m.lay.glyphs().warnMark + " " + from + " falló dos veces seguidas; cambiando automáticamente al fallback " + m.fallbackModel
	if err != nil {
		notice += ", pero no se pudo preparar ese proveedor tampoco: " + err.Error()
	}
	return m.slashNotice(notice)
}

// checkAutoCompact is checkEndOfTurn's own first half: the §10
// auto-trigger. Once a turn's own answer has landed (streamed one
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
	m.agentStream = nil
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

// frameBudget is RC-3/F20's shared height accounting: how many rows render()
// is allowed to fill, out of the terminal's own m.lay.Height.
//
// It reserves exactly one row (F20, "one blank row between the footer and
// the bottom edge"). That is not a separate feature bolted on next to the
// height invariant — measuring it against a real terminal showed the frame
// already fills every row of a short terminal with none left over (a
// 60x15 session with a full live region produced 15 rows of content and 0
// blank rows below them), so "leave one row of breathing room at the
// bottom" and "the budget evictOverflow enforces is one row short of
// lay.Height, not equal to it" are the same fix. Giving it its own name and
// doc comment here, instead of writing "m.lay.Height-1" at each call site,
// is what keeps that connection from being lost the next time either
// number is touched.
func (m Root) frameBudget() int {
	if m.lay.Height <= 0 {
		return 0
	}
	return m.lay.Height - 1
}

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
//
// This alone still has the two holes RC-3 names: it stops at keepInline, and
// it can only evict whole transcript entries — a live turn's own growing
// body is not one. head()'s own clipHead (view.go) is what actually closes
// both, by clipping whatever is left once eviction has done all it can;
// this function's job is to hand off as much as it safely can *before* that
// clip has to draw its "…N rows above" affordance instead of real content.
//
// The overflow check below measures against frameRowsUnclipped, not
// render()'s own (already clipped) output — see frameRowsUnclipped's and
// headContent's doc comments (view.go) for why: this loop has to see the
// overflow head() is about to hide, or it would never run.
func (m Root) evictOverflow() (Root, tea.Cmd) {
	// Fullscreen never evicts to real scrollback. commitEntryCmd's whole
	// mechanism is tea.Println/insertAbove — regular mode's "permanently
	// commit this line to the terminal's own scrollback" — and in
	// fullscreen there is no real scrollback to commit it to: AltScreen's
	// buffer is the alternate screen, which is transient and gets wiped
	// the moment the program exits (or draws over it), so anything
	// printed there today is invisible tomorrow. Evicting in fullscreen
	// would silently lose content instead of relocating it — exactly what
	// §4.1 assertion 3 ("no content loss after resize") exists to catch,
	// and precisely the accepted trade-off docs/DESIGN-tui-mode.md §7's
	// open question 2 already names for the exit transcript itself:
	// keep everything in m.transcript (printedUpTo stays 0 forever in
	// fullscreen) and rely on clipHead's pre-existing *visual* clip
	// (view.go) as fullscreen's sole backstop past frameBudget — unbounded
	// in-memory growth for a very long fullscreen session, revisited only
	// if it is reported, the same call §7's own comment already makes for
	// "print it all" on the way out.
	if m.mode == ModeHelp || m.lay.Height <= 0 || m.tuiMode == termenv.ModeFullscreen {
		return m, nil
	}
	g := m.lay.glyphs()
	width := m.lay.ContentWidth()
	budget := m.frameBudget()
	var cmds []tea.Cmd
	for len(m.transcript)-m.printedUpTo > keepInline {
		if m.frameRowsUnclipped() <= budget {
			break
		}
		cmds = append(cmds, commitEntryCmd(m.styles, g, width, m.transcript[m.printedUpTo], m.cfgSyntax, m.cfgMarkdown, m.foldCode, m.cfgReasoning))
		m.printedUpTo++
	}
	return m, tea.Batch(cmds...)
}

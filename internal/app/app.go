// Package app cablea config → tema → TUI. Es la única pieza autorizada a
// importar tanto internal/config como internal/tui: root.go no sabe que
// existe config.Load, y config no sabe que existe Bubble Tea (§6.1).
package app

import (
	"context"
	"fmt"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"

	"github.com/MichiTrader/ishakat/internal/catalog"
	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/convo"
	"github.com/MichiTrader/ishakat/internal/engine"
	"github.com/MichiTrader/ishakat/internal/mission"
	"github.com/MichiTrader/ishakat/internal/permissions"
	"github.com/MichiTrader/ishakat/internal/theme"
	"github.com/MichiTrader/ishakat/internal/tools"
	"github.com/MichiTrader/ishakat/internal/tui"
	"github.com/MichiTrader/ishakat/internal/xdg"
)

// backgroundRefreshTimeout bounds the one network trip §4.4/§11 allows
// after startup. It is generous compared to fetch.DefaultDiscoverTimeout
// (each provider already races that internally): this is the ceiling on
// the whole background refresh — discovery against every enabled provider
// plus models.dev — not on any single one of them, and a user on a slow
// or flaky connection should still get an answer instead of a goroutine
// that runs until the process exits.
const backgroundRefreshTimeout = 60 * time.Second

// Run carga la configuración, resuelve el tema y arranca el programa de
// Bubble Tea en modo inline. version es la versión compilada de ishakat
// (variable de main, inyectada por -ldflags en builds de release). resume
// is cmd/ishakat's --resume flag; [session] resume_last (config.go) is
// honoured either way, inside ResumeSession, so a caller that never passes
// true here still resumes when the configuration asks for it.
func Run(version string, resume bool) int {
	cfg, err := config.Load(config.Options{UserPath: xdg.ConfigFile()})
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ Configuration error: %v\n", err)
		return 1
	}

	// rawCWD is captured before xdg.Pretty rewrites the display copy below:
	// §21.4 layer 2's own trust.Store keys records by the real absolute
	// path (trust.Set's own doc comment on cleanPath), never by the
	// "~/dev/..." shorthand a human reads in the dialog, so a project
	// under $HOME does not silently key differently from one outside it.
	rawCWD, err := os.Getwd()
	if err != nil {
		rawCWD = "."
	}

	// The TUI receives the directory already in display form. Deciding what a
	// path looks like to a human needs the home directory and the host's
	// separator, which is filesystem knowledge tui must not have (§6.1); all
	// the TUI does with it is fit it into the columns it has.
	cwd := xdg.Pretty(rawCWD)

	noTTY := !term.IsTerminal(os.Stdout.Fd())
	cap := theme.Detect(cfg.UI.Color)
	th := theme.Load(cfg.UI.Theme, xdg.ThemesDir())

	// Colour and repertoire are two independent questions about the same
	// terminal (see theme.GlyphSet), so they are resolved side by side and
	// both handed over. Resolving one and forgetting the other is not a
	// hypothetical mistake: the glyph set existed for a whole step without
	// this line, which meant a cp437 console kept being sent block-drawing
	// characters no matter what [ui] glyphs said.
	glyphs := theme.DetectGlyphs(cfg.UI.Glyphs)

	// The catalog is loaded from disk only (§4.4's non-negotiable budget:
	// no network on the critical path), which is why this is safe to call
	// unconditionally before the interface is drawn — RefreshCatalog, the
	// one that goes to the network, is Step 11's background-refresh
	// concern, not this one's.
	snap := LoadCatalog(cfg)

	// warnp is P3's dedupe fix (warnings.go's own doc comment on
	// WarningPrinter): app.default_model and compact_model both falling
	// back to the exact same provider (P2's ResolveModelForBoot) — or,
	// before P0/P1 existed, both simply naming the same uncredentialed
	// provider — used to print the identical warning line twice. Every
	// ⚠ print in this startup sequence goes through warnp.Warn from here
	// on, so a repeat is silently absorbed instead of shown twice.
	warnp := NewWarningPrinter()

	// A model/provider that fails to resolve is not fatal here the way it
	// is in Headless (headless.go's own step 4): there is no prompt on the
	// command line that would otherwise have nothing to answer, only an
	// interface the user can still open, read /help in, and fix the
	// configuration from without restarting. tui.Options.Engine already
	// documents nil as a supported value for exactly this reason.
	//
	// snap.Catalog and cfg.Tools.Enabled are what let this engine actually
	// offer tools (see CapsFor): passing wantTools = true here — and false
	// for compactEng below — is the fix for the Step 16 bug where every
	// request went out with a zero provider.Caps, so the dialect dropped the
	// `tools` array and the model could never call a tool, never trip the
	// Guard, and never open the approval overlay this step exists to draw.
	eng, ref, system, warn, buildErr := BuildEngine(cfg, &snap.Catalog, "", version, cfg.Tools.Enabled)
	model := ref.Ref
	if buildErr != nil {
		warnp.Warn(os.Stderr, buildErr.Error())
		eng = nil
	}
	warnp.Warn(os.Stderr, warn)

	// compact_model gets its own Engine (§10, Step 12): it can name a
	// different provider than the conversation's own model, and
	// BuildEngine's NewStreamer binds one Engine to exactly one provider
	// at construction time — there is no way to reuse eng above for a
	// second provider. ResolveModel's own empty-string rule ("" falls back
	// to app.default_model) means a configuration that never mentions
	// compact_model still compacts, with whatever model the conversation
	// already uses. A resolution failure here is not fatal any more than
	// eng's own is above: it only means /compact falls back to
	// convo.DropOldest (see tui.Root.startCompact), not that the whole
	// interface refuses to start.
	//
	// wantTools is false: compaction summarizes a conversation, and a
	// summarizer has no business being handed write_file or bash (§10). This
	// is not merely a safety default — RunAgentTurn is never used for
	// compaction (startCompact calls engine.Summarize), so offering tools
	// here would put a `tools` array on the wire that nothing could ever
	// execute.
	compactEng, compactRef, _, compactWarn, compactErr := BuildEngine(cfg, &snap.Catalog, cfg.App.CompactModel, version, false)
	if compactErr != nil {
		warnp.Warn(os.Stderr, compactErr.Error())
		compactEng = nil
	}
	warnp.Warn(os.Stderr, compactWarn)

	// fallback_model (§11 Phase 4, root.go's checkFallback): resolved to
	// its canonical Ref once here, via ResolveFallbackModel (modelref.go —
	// see its own comment for why "" is never passed through ResolveModel).
	// Unlike CompactModel this does NOT go through BuildEngine: checkFallback
	// rebuilds its own engine lazily, only once a switch actually fires, via
	// the exact same EngineFor factory below — building (and immediately
	// discarding) a second *engine.Engine for a fallback that may never be
	// needed this session would be wasted work on every single launch. A
	// resolution failure is a warning, not fatal — exactly like compactErr
	// above, since the interactive session still works with no fallback at
	// all.
	fallbackRef, fallbackErr := ResolveFallbackModel(cfg)
	if fallbackErr != nil {
		warnp.Warn(os.Stderr, fallbackErr.Error())
	}

	// Step 16's tool layer, interactive side: when cfg.Tools.Enabled, every
	// turn runs through engine.RunAgentTurn (tui.Root.startAgentTurn)
	// instead of eng.Start's plain stream drain, and any tool call that
	// lands past Low tier pauses on ModeToolApprove until reviewer.Review
	// answers it. reviewer is built now — buildAgentOptions needs a
	// *permissions.Guard before tui.Options exists at all — but it cannot
	// actually reach the interface until reviewer.SetProgram runs, right
	// after tea.NewProgram below produces the *tea.Program to reach it
	// with; see toolreview.go's own comment for why that two-step
	// construction is unavoidable. Unlike Headless (headless.go's own
	// step 7), there is no --yolo plumbed through to the TUI: an
	// interactive session already has somewhere to ask, so silently
	// skipping the ask would remove the one thing this whole step adds.
	var agentOpts engine.AgentOptions
	var reviewer *toolReviewer
	var asker *tuiAsker
	var waitNotifier *tuiWaitNotifier
	// guard is declared outside the block below (unlike reviewer's own
	// *toolReviewer, which nothing after this needs) because
	// trustStoreFor's own fileTrustStore.Save bridges §21.4 layer 2's
	// dialog choice straight into Guard.SetAutonomy for the running
	// session — see fileTrustStore's own doc comment. nil (tools
	// disabled) is a supported value there too: the chosen autonomy still
	// updates FooterState.Autonomy and trust.json, it just has no live
	// Guard left to narrow in a session with no tools at all.
	var guard *permissions.Guard
	if cfg.Tools.Enabled {
		reviewer = newToolReviewer()
		var modelCost *catalog.Cost
		var modelCaps tools.Caps
		if m, found := snap.Catalog.Get(ref.Ref); found {
			modelCost = m.Cost
			modelCaps = capsForTools(m)
		}
		guard = permissions.New(cfg.Tools.Permissions, false, reviewer)
		var toolsWarn string
		// hasTTY = !noTTY: the TUI is only ever running with a live
		// terminal and a real reviewer bridge to resolve gate 2's approval
		// dialog against (see buildAgentOptions' own doc comment on why
		// runAgentTurnHeadless, the other call site, always passes false
		// instead). noTTY itself is computed once at the top of Run from
		// the same term.IsTerminal(os.Stdout.Fd()) check tui.Options.NoTTY
		// already carries into the interface for unrelated (rendering)
		// reasons — this reuses that one source of truth rather than
		// asking the terminal a second time.
		// Same eng, ref.WireID and system a sub-agent's own turn should
		// answer with -- see newSubAgentRunner's own doc comment (dispatch.go)
		// on why a sub-agent reuses the parent's already-resolved
		// provider/model rather than re-resolving one of its own.
		// asker is built now, same two-step reasoning as reviewer above:
		// buildAgentOptions needs an ask.Asker before tui.Options (and
		// therefore the *tea.Program) exists at all, but tuiAsker cannot
		// actually reach the running program until asker.SetProgram runs,
		// right after tea.NewProgram below produces it -- see askuser.go's
		// own comment (internal/app) for why that two-step construction is
		// unavoidable, the identical shape toolreview.go's reviewer already
		// follows. Built before dispatchRunner below so a sub-agent's own
		// dispatched turn can be given the same asker the parent turn gets
		// (see newSubAgentRunner's own doc comment on why).
		asker = newTUIAsker()
		dispatchRunner := newSubAgentRunner(eng, ref.WireID, system, cfg.Tools, guard, modelCost, modelCaps, !noTTY, asker)
		agentOpts, toolsWarn = buildAgentOptions(cfg.Tools, guard, modelCost, modelCaps, !noTTY, dispatchRunner, asker)
		warnp.Warn(os.Stderr, toolsWarn)

		// waitNotifier is Step 32's own closing bridge (§21.1's "wait"
		// phase, waitphase.go's own doc comment): built now, same two-step
		// reasoning as reviewer/asker above, and wired onto agentOpts here
		// rather than inside buildAgentOptions itself for the identical
		// reason runAgentTurnHeadless sets its own OnWait inline — this is
		// the layer that owns a *tea.Program to report through, and
		// buildAgentOptions itself is shared with dispatch.go's own
		// sub-agent path, which has no *tea.Program of its own to send to.
		// A dispatched sub-agent's own retries are not covered by this —
		// its own inner buildAgentOptions call (newSubAgentRunner,
		// dispatch.go) never sets OnWait, matching the identical gap
		// runAgentTurnHeadless already leaves for the headless path today;
		// closing that for both callers at once is separate follow-up work,
		// not part of wiring the top-level turn's own status line.
		waitNotifier = newTUIWaitNotifier()
		agentOpts.OnWait = waitNotifier.OnWait
	}

	// cfg.Warnings carries one entry per enabled provider missing its
	// credential (expand.go). Printing all of it unconditionally used to
	// warn on every launch about every declared-but-unused provider —
	// noise for a configuration that only actually uses one or two of
	// them. Only warnings about the two providers this session resolved
	// to (the conversation's own model and, separately, compact_model)
	// are shown here; `config check`/`doctor`/`provider list` still print
	// cfg.Warnings unfiltered, on purpose. See warnings.go's doc comment.
	for _, w := range FilterWarningsForProviders(cfg.Warnings, ref.Provider, compactRef.Provider) {
		warnp.Warn(os.Stderr, fmt.Sprintf("[%s] %s", w.Where, w.Msg))
	}

	// --resume / [session] resume_last (§13): load the previous conversation
	// before the recorder is built, so a resumed run appends to that same
	// file instead of starting a new one — see sessionRecorder's own comment
	// on why passing resumedConv in is what makes that true. A failure or
	// "nothing to resume" here is a warning at most, same rule as the
	// recorder below: there is always a fresh session to fall back to.
	resumedConv, resumeStore, resumeWarn := ResumeSession(cfg, resume)
	warnp.Warn(os.Stderr, resumeWarn)
	var history []convo.Message
	if resumedConv != nil {
		history = resumedConv.Messages
	}

	// §10, Step 13: the TUI persists its own conversation the same way
	// headless already did — a failure here is a warning, not a reason to
	// refuse to start, for the same reason engine/compactEng above are not:
	// an interface the user can read and copy from is strictly better than
	// no interface, even with nothing saved to disk.
	//
	// resumeStore is reused instead of letting NewSessionRecorder open a
	// second *convo.Store on the same directory: convo.Store carries no
	// per-conversation state (§10), so this is purely to avoid two
	// redundant os.MkdirAll calls, not a correctness requirement.
	var recorder tui.Recorder
	var sessionWarn string
	if resumedConv != nil && resumeStore != nil {
		recorder = &sessionRecorder{store: resumeStore, conv: resumedConv, model: model, keepLast: cfg.Session.KeepLast}
	} else {
		recorder, sessionWarn = NewSessionRecorder(cfg, model, nil)
	}
	warnp.Warn(os.Stderr, sessionWarn)

	// §13's third item: /resume's own read side. resumeStore is reused when
	// this run itself already opened one (--resume, resume_last) — same
	// reasoning as recorder above — otherwise NewSessionLister opens its
	// own, honouring [session] save exactly like NewSessionRecorder does.
	lister, listerWarn := NewSessionLister(cfg, resumeStore)
	warnp.Warn(os.Stderr, listerWarn)

	// §21.4 layer 2: exactly one question on the first interactive run in
	// a project with no saved decision (resolveProjectTrust's own doc
	// comment). needsTrust/gitInfo/initialAutonomy/trustStore are the
	// five pieces tui.Options below needs; a config with
	// [autonomy].remember = false always reports needsTrust so the
	// question really is asked every run, per that key's own doc comment
	// in internal/config/schema.go.
	needsTrust, gitInfo, initialAutonomy, trustStore := resolveProjectTrust(cfg, rawCWD, guard)

	root := tui.NewRoot(tui.Options{
		Version: version,
		CWD:     cwd,
		Cfg:     cfg,
		Theme:   th,
		Cap:     cap,
		Glyphs:  glyphs,
		// ThemesDir/ThemeStore are /theme's own two dependencies
		// (internal/tui/theme.go's own doc comment on the §6.1 seam
		// this draws): the same xdg.ThemesDir() th above was already
		// resolved against, and a fileThemeStore over config.SetTheme
		// (themestore.go) mirroring NewEvolveStore's own "only
		// internal/app touches internal/config's write path" rule.
		ThemesDir:  xdg.ThemesDir(),
		ThemeStore: &fileThemeStore{},
		NoTTY:      noTTY,
		// battery_saver = "auto" (the default) means "6fps on Termux", not "6fps
		// literally everywhere": without this, every desktop session with no
		// override would have read the same false that a phone should, and the
		// key would have had no effect for the one host it names.
		Termux: xdg.IsTermux(),
		Engine: eng,
		// The factory re-decides Caps per destination model (see its own
		// comment), so switching models with ctrl+p keeps tool calling
		// working — or correctly stops offering tools to a model the
		// catalog says cannot take them — instead of inheriting whatever
		// the boot model happened to support.
		EngineFor: NewEngineFactory(cfg, &snap.Catalog, version, cfg.Tools.Enabled),
		// LoginFor drives /login's actual device-flow network calls
		// (internal/tui/loginfactory.go's own §6.1 boundary comment) —
		// see loginfactory.go's own doc comment for why every built-in
		// preset hits its "no OAuth device flow configured" branch
		// today, and why that is still correct infrastructure to ship.
		LoginFor: NewLoginFactory(cfg),
		Model:    model,
		System:   system,
		Catalog:  &snap.Catalog,
		// DiscoverSkills reuses the exact same gate SystemPrompt (called
		// inside BuildEngine, above) already applied when it built system:
		// a second, disk-only call rather than threading the first result
		// through BuildEngine's return values, because BuildEngine's five
		// already-crowded returns are shared with Headless's own entry
		// point (headless.go's step 4), which has no tui.Options to feed
		// this into and would have to discard it. See DiscoverSkills' own
		// comment for why this is one function, not two copies of the gate.
		Skills:     DiscoverSkills(cfg),
		Alias:      cfg.Alias,
		Favorites:  cfg.Favorites.List,
		PreferFree: cfg.Catalog.PreferFree,

		CompactEngine:        compactEng,
		CompactModel:         compactRef.Ref,
		CompactAuto:          cfg.Compact.Auto,
		CompactTriggerPct:    cfg.Compact.TriggerPct,
		CompactKeepLastTurns: cfg.Compact.KeepLastTurns,
		CompactStrategy:      cfg.Compact.Strategy,
		CompactOnError:       cfg.Compact.OnError,

		FallbackModel: fallbackRef,

		Recorder:      recorder,
		History:       history,
		SessionLister: lister,

		// ToolsLister is /tools' own read (and, since /tools edit/create,
		// write) side (§13, Step 20/21) — see internal/app/toolslister.go's
		// own doc comment for why this package is the one place allowed
		// to import both internal/tools and internal/tui at once. nil
		// (tools.enabled = false, or an empty tools.dir) is the supported
		// "cannot list" value, matching what runToolsCommand already
		// expects. NewToolsListerWithEvolve (not NewToolsListerWithEgress
		// or the plain NewToolsLister) is used here because EditTool
		// needs the same egress allowlist cfgTools.Egress.Allow/AllowAll
		// already threaded into tools.WithMetaTools' own ToolEdit
		// construction in buildAgentOptions, and CreateTool additionally
		// needs the same gate 1 Thresholds already threaded into
		// tools.WithMetaTools' own ToolCreate construction there — see
		// toolsLister's own doc comment.
		ToolsLister: NewToolsListerWithEvolve(cfg.Tools.Dir, cfg.Tools.Enabled, cfg.Tools.Egress.Allow, cfg.Tools.Egress.AllowAll, evolveThresholds(cfg.Tools, cfg.Tools.Evolve)),

		// PermissionsLister is /permissions' own read side (§13, Step 32)
		// — the same guard already bound into agentOpts.Runner above (nil
		// when cfg.Tools.Enabled is false, in which case
		// NewPermissionsLister itself degrades to nil, matching every
		// other Guard-backed seam's own "no live Guard, no live view"
		// rule) plus the same cfg.Tools.Permissions already threaded
		// through permissions.New elsewhere in this function.
		PermissionsLister: NewPermissionsLister(guard, cfg.Tools.Permissions),

		ToolsEnabled: cfg.Tools.Enabled,
		AgentOptions: agentOpts,

		// EvolveStore is §19.7's own suggestion overlay's persistence seam
		// (Step 25) — see NewEvolveStore's own comment for why most runs
		// still get nil here (tools disabled, no TTY, or a configured mode
		// other than "suggest") and EvolveThresholds/the three budget
		// scalars mirror evolveThresholds' own comment above.
		EvolveStore:       NewEvolveStore(cfg.Tools, !noTTY),
		EvolveThresholds:  evolveThresholds(cfg.Tools, cfg.Tools.Evolve),
		SuggestPerSession: cfg.Tools.Evolve.SuggestPerSession,
		SuggestPerWeek:    cfg.Tools.Evolve.SuggestPerWeek,
		DecayAfterRejects: cfg.Tools.Evolve.DecayAfterRejects,

		// §21.4 layer 2 (Step 30) — see resolveProjectTrust's own doc
		// comment for how each of these five is decided.
		NeedsTrust:      needsTrust,
		GitInGit:        gitInfo.InGit,
		GitClean:        gitInfo.Clean,
		GitBranch:       gitInfo.Branch,
		InitialAutonomy: initialAutonomy,
		TrustStore:      trustStore,

		// §21.6 (Step 31, part 2) — the same guard already bound into
		// agentOpts.Runner above, so a mission confirmed through
		// ModeMission is enforced on the very same Guard every tool call
		// this session already passes through. missionGuardOrNil (below)
		// is needed rather than passing guard directly: guard is a
		// *permissions.Guard, nil when cfg.Tools.Enabled is false, and a
		// nil *permissions.Guard boxed directly into the tui.MissionGuard
		// interface would be a non-nil interface value wrapping a nil
		// pointer — Root's own "missionGuard == nil means enforce
		// nowhere" check (root.go's own doc comment) tests the interface,
		// not the pointer underneath it, so that boxed-nil case would
		// panic the first time AddMissionRules dereferenced it instead of
		// being silently skipped the way every other nil-Guard path in
		// this function already is.
		MissionGuard: missionGuardOrNil(guard),

		// §21.6's second dialog-opening trigger (Step 31 part 9) — bridges
		// the same cfg.Tools.Permissions already threaded through
		// permissions.New above into a mission.Policy, mirroring
		// missionGuardOrNil's own bridge for the enforcement seam.
		MissionPolicy: missionPolicyOf(cfg.Tools.Permissions),
	})

	p := tea.NewProgram(root)

	// reviewer (nil unless cfg.Tools.Enabled) has been waiting since it was
	// built above for the one thing it could not have any earlier: a
	// running *tea.Program to send tui.ToolApproveRequestMsg to. See
	// toolreview.go's own comment on why this has to be a second step
	// rather than a constructor argument.
	if reviewer != nil {
		reviewer.SetProgram(p)
	}
	// asker (nil unless cfg.Tools.Enabled) has been waiting since it was
	// built above for the same thing reviewer was: a running *tea.Program
	// to send tui.AskUserRequestMsg to. See internal/app/askuser.go's own
	// comment for why this has to be a second step rather than a
	// constructor argument.
	if asker != nil {
		asker.SetProgram(p)
	}
	// waitNotifier (nil unless cfg.Tools.Enabled) has been waiting since it
	// was built above for the same thing reviewer/asker were: a running
	// *tea.Program to send tui.PhaseWaitMsg to. See
	// internal/app/waitphase.go's own comment for why this has to be a
	// second step rather than a constructor argument.
	if waitNotifier != nil {
		waitNotifier.SetProgram(p)
	}

	// §4.4/§11's background refresh: only worth doing when the cache
	// LoadCatalog already read was expired (or missing) — a fresh cache
	// answers exactly what a refresh would, so firing one anyway would
	// just be a network trip that changes nothing the picker can show.
	// The goroutine talks back to Root the only way anything outside the
	// tea.Program's own Update loop can: p.Send, with the *catalog.Catalog
	// BackgroundRefresh produced (nil is a legitimate answer — see its own
	// comment — and CatalogRefreshedMsg's handler already treats nil as a
	// no-op instead of blanking a catalog the user is looking at).
	if snap.Expired {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), backgroundRefreshTimeout)
			defer cancel()
			next := BackgroundRefresh(ctx, cfg, version, snap)
			p.Send(tui.CatalogRefreshedMsg{Catalog: next})
		}()
	}

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "✗ Error running the interface: %v\n", err)
		return 1
	}
	return 0
}

// missionGuardOrNil returns g boxed as a tui.MissionGuard, or a genuinely
// nil interface value when g itself is nil — see this function's own call
// site (tui.Options.MissionGuard, above) for why a plain `tui.MissionGuard(g)`
// conversion is the wrong shortcut here: boxing a nil *permissions.Guard
// directly into an interface produces a non-nil interface value (its type
// word is set even though its data word is nil), which would defeat
// Root.missionGuard's own "!= nil" nil check the moment a mission ever
// tried to call AddMissionRules on it.
func missionGuardOrNil(g *permissions.Guard) tui.MissionGuard {
	if g == nil {
		return nil
	}
	return g
}

// missionPolicyOf converts a real config.Permissions into a *mission.Policy
// for tui.Options.MissionPolicy — the same field-by-field bridge
// mission.Policy's own doc comment says a caller in this package must build,
// since internal/mission never imports internal/config. Always returns a
// non-nil pointer: unlike missionGuardOrNil, there is no "disabled" case to
// preserve here — cfg.Tools.Permissions always exists, so checkToolPolicy's
// own "nil means never fires" degradation (Root.missionPolicy's own doc
// comment) is only ever exercised by this package's own tests, not by a
// real run.
//
// FetchAllowed is perm.Read != "deny", not perm.Shell — guard.go's own
// mode() already established that fetch shares Read's policy knob, not
// Shell's ("fetch shares Read's policy knob rather than getting its own
// config key"), so this bridge asks the identical question of the
// identical knob Authorize itself consults for a real fetch call.
func missionPolicyOf(perm config.Permissions) *mission.Policy {
	return &mission.Policy{
		ShellAllowed: perm.Shell != "deny",
		ShellDeny:    perm.ShellDeny,
		FetchAllowed: perm.Read != "deny",
	}
}

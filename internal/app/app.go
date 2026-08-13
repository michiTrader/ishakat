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
	"github.com/MichiTrader/ishakat/internal/permissions"
	"github.com/MichiTrader/ishakat/internal/theme"
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

	// The TUI receives the directory already in display form. Deciding what a
	// path looks like to a human needs the home directory and the host's
	// separator, which is filesystem knowledge tui must not have (§6.1); all
	// the TUI does with it is fit it into the columns it has.
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	cwd = xdg.Pretty(cwd)

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
	if cfg.Tools.Enabled {
		reviewer = newToolReviewer()
		var modelCost *catalog.Cost
		if m, found := snap.Catalog.Get(ref.Ref); found {
			modelCost = m.Cost
		}
		guard := permissions.New(cfg.Tools.Permissions, false, reviewer)
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
		dispatchRunner := newSubAgentRunner(eng, ref.WireID, system, cfg.Tools, guard, modelCost, !noTTY)
		agentOpts, toolsWarn = buildAgentOptions(cfg.Tools, guard, modelCost, !noTTY, dispatchRunner)
		warnp.Warn(os.Stderr, toolsWarn)
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

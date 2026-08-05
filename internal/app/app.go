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

	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/convo"
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
		fmt.Fprintf(os.Stderr, "✗ Error de configuración: %v\n", err)
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

	// A model/provider that fails to resolve is not fatal here the way it
	// is in Headless (headless.go's own step 4): there is no prompt on the
	// command line that would otherwise have nothing to answer, only an
	// interface the user can still open, read /help in, and fix the
	// configuration from without restarting. tui.Options.Engine already
	// documents nil as a supported value for exactly this reason.
	eng, ref, system, warn, buildErr := BuildEngine(cfg, "", version)
	model := ref.Ref
	if buildErr != nil {
		fmt.Fprintf(os.Stderr, "⚠ %v\n", buildErr)
		eng = nil
	}
	if warn != "" {
		fmt.Fprintf(os.Stderr, "⚠ %s\n", warn)
	}
	for _, w := range cfg.Warnings {
		fmt.Fprintf(os.Stderr, "⚠ [%s] %s\n", w.Where, w.Msg)
	}

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
	compactEng, compactRef, _, compactWarn, compactErr := BuildEngine(cfg, cfg.App.CompactModel, version)
	if compactErr != nil {
		fmt.Fprintf(os.Stderr, "⚠ %v\n", compactErr)
		compactEng = nil
	}
	if compactWarn != "" {
		fmt.Fprintf(os.Stderr, "⚠ %s\n", compactWarn)
	}

	// --resume / [session] resume_last (§13): load the previous conversation
	// before the recorder is built, so a resumed run appends to that same
	// file instead of starting a new one — see sessionRecorder's own comment
	// on why passing resumedConv in is what makes that true. A failure or
	// "nothing to resume" here is a warning at most, same rule as the
	// recorder below: there is always a fresh session to fall back to.
	resumedConv, resumeStore, resumeWarn := ResumeSession(cfg, resume)
	if resumeWarn != "" {
		fmt.Fprintf(os.Stderr, "⚠ %s\n", resumeWarn)
	}
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
	if sessionWarn != "" {
		fmt.Fprintf(os.Stderr, "⚠ %s\n", sessionWarn)
	}

	// §13's third item: /resume's own read side. resumeStore is reused when
	// this run itself already opened one (--resume, resume_last) — same
	// reasoning as recorder above — otherwise NewSessionLister opens its
	// own, honouring [session] save exactly like NewSessionRecorder does.
	lister, listerWarn := NewSessionLister(cfg, resumeStore)
	if listerWarn != "" {
		fmt.Fprintf(os.Stderr, "⚠ %s\n", listerWarn)
	}

	root := tui.NewRoot(tui.Options{
		Version: version,
		CWD:     cwd,
		Cfg:     cfg,
		Theme:   th,
		Cap:     cap,
		Glyphs:  glyphs,
		NoTTY:   noTTY,
		// battery_saver = "auto" (the default) means "6fps on Termux", not "6fps
		// literally everywhere": without this, every desktop session with no
		// override would have read the same false that a phone should, and the
		// key would have had no effect for the one host it names.
		Termux:     xdg.IsTermux(),
		Engine:     eng,
		EngineFor:  NewEngineFactory(cfg, version),
		Model:      model,
		System:     system,
		Catalog:    &snap.Catalog,
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

		Recorder:      recorder,
		History:       history,
		SessionLister: lister,
	})

	p := tea.NewProgram(root)

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
		fmt.Fprintf(os.Stderr, "✗ Error ejecutando la interfaz: %v\n", err)
		return 1
	}
	return 0
}

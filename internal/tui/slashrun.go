// slashrun.go is the only place that knows what running a slash command
// means (§9.6/§9.7). internal/slash stays deliberately ignorant of engines,
// conversations and terminals — it only classifies a Command by Kind — so
// every branch below is UI/state work Root already owns elsewhere.
package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/convo"
	"github.com/MichiTrader/ishakat/internal/slash"
)

// updateSlashMenu handles the keys the §9.6 dropdown claims while it is
// open: up/down move the selection, tab completes the highlighted command's
// name into the input (so an argument can be typed next) without running
// it, and enter/esc are the same keys chat mode already binds to
// submit/cancel, repurposed here to accept the selection or close the
// dropdown. Any other key returns handled=false so the caller's normal
// dispatch — and the textarea underneath it — still sees it, which is what
// keeps the list narrowing as more of the name is typed.
//
// Submit's own case is the one branch shared with updateBusy (W2 item 3,
// docs/ROADMAP-ux-2026-08-20.md): while ModeBusy, the highlighted command
// still has to pass busyAllowedSlashKind's own allow-list — the dropdown
// itself does not know it is being driven from a running turn, so this is
// the one place that check has to live for the accept-the-selection path,
// mirroring runBusySlashLine's identical check for a fully typed line.
func (m Root) updateSlashMenu(key string) (bool, tea.Model, tea.Cmd) {
	switch key {
	case "up":
		m.menu = m.menu.moveUp()
		return true, m, nil
	case "down":
		m.menu = m.menu.moveDown()
		return true, m, nil
	case "tab":
		sel := m.menu.Selected()
		m.input.SetValue("/" + sel.Name + " ")
		m.menu = slashMenuFor(m.input.Value(), m.commands, m.menu)
		return true, m, nil
	case m.keys.Cancel:
		m.menu = slashMenu{}
		return true, m, nil
	case m.keys.Submit:
		sel := m.menu.Selected()
		if m.mode == ModeBusy && !busyAllowedSlashKind(sel.Kind) {
			m = m.recordHistory(strings.TrimSpace(m.input.Value()))
			m.input.Reset()
			m.menu = slashMenu{}
			g := m.lay.glyphs()
			next, cmd := m.slashNotice(g.warnMark + " " + sel.Usage() + " no esta disponible mientras el turno trabaja")
			return true, next, cmd
		}
		next, cmd := m.runSlashCommand(sel, "")
		return true, next, cmd
	}
	return false, m, nil
}

// runSlashLine parses a full slash-command line and runs it, or reports that
// no such command exists. slash.Parse never returns a bare error — this is
// the one place "/nope" turns into user-facing feedback instead of quietly
// falling through to the engine as chat text.
func (m Root) runSlashLine(text string) (tea.Model, tea.Cmd) {
	p := slash.Parse(text, m.commands)
	if !p.Found {
		// Recorded even on an unknown command — that is exactly the case
		// where recalling the failed line with up-arrow to fix a typo is
		// most useful, and runSlashCommand (the other path in here that
		// records history) never sees an unresolved name at all.
		m = m.recordHistory(text)
		return m.slashNotice(m.lay.glyphs().warnMark + " comando desconocido: /" + p.Raw)
	}
	return m.runSlashCommand(p.Command, p.Args)
}

// runSlashCommand switches on cmd.Kind — the only thing internal/slash
// exposes about behaviour — and does whatever that command means to the
// running interface. args is unused by every Kind implemented so far
// (KindHelp/KindClear/KindNew/KindExit all take none); it is threaded
// through now so Step 10's /model and later /theme, /copy, /compact do not
// need this signature to change again.
//
// It is also the single funnel that records input history for every slash
// command, regardless of whether it arrived through a full typed line
// (runSlashLine, enter with the dropdown closed) or by accepting the
// dropdown's own highlighted row (updateSlashMenu's m.keys.Submit case,
// which calls straight in here with args == "" and never goes through
// runSlashLine) — recording it there instead would miss the second path.
func (m Root) runSlashCommand(cmd slash.Command, args string) (tea.Model, tea.Cmd) {
	if typed := strings.TrimSpace(m.input.Value()); typed != "" {
		m = m.recordHistory(typed)
	}
	m.input.Reset()
	m.menu = slashMenu{}

	switch cmd.Kind {
	case slash.KindHelp:
		m.mode = ModeHelp
		m.help = true
		return m, nil
	case slash.KindHotkeys:
		m.mode = ModeHotkeys
		return m, nil
	case slash.KindClear:
		// Same effect as ctrl+l (handleGlobalKey): only the screen (and, per
		// RC-3/B3, the real scrollback) is wiped — the conversation itself,
		// and what the next turn sends the model, is untouched.
		m.transcript = nil
		m.printedUpTo = 0
		return m, clearAndWipeCmd()
	case slash.KindNew:
		// Unlike /clear, /new also drops the conversation itself: the next
		// turn starts with no history at all.
		m.conv = convo.Conversation{}
		m.transcript = nil
		m.printedUpTo = 0
		return m, clearAndWipeCmd()
	case slash.KindExit:
		m.quitting = true
		return m, tea.Quit
	case slash.KindModel:
		return m.runModelCommand(args)
	case slash.KindCompact:
		return m.startCompact("")
	case slash.KindCopy:
		return m.runCopy(args)
	case slash.KindRetry:
		return m.runRetry()
	case slash.KindStats:
		return m.runStats()
	case slash.KindResume:
		return m.runResumeCommand()
	case slash.KindName:
		return m.runNameCommand(args)
	case slash.KindModels:
		return m.runModelsCommand(args)
	case slash.KindSkills:
		return m.runSkillsCommand()
	case slash.KindLogin:
		return m.startLogin(args)
	case slash.KindTheme:
		return m.runThemeCommand(args)
	case slash.KindConfig:
		return m.runConfigCommand()
	case slash.KindSettings:
		return m.runSettingsCommand(args)
	case slash.KindReload:
		return m.runReloadCommand()
	case slash.KindDebug:
		return m.runDebugCommand()
	case slash.KindTools:
		return m.runToolsCommand(args)
	case slash.KindPermissions:
		return m.runPermissionsCommand(args)
	case slash.KindTrust:
		return m.runTrustCommand()
	default:
		return m.slashNotice(m.lay.glyphs().warnMark + " " + unimplementedNotice(cmd))
	}
}

// unimplementedNotice is what a KindUnimplemented command reports.
// /login (Step 24) has a binary-side equivalent that answers the same
// question today: the OAuth device flow itself is real and tested
// (cmd/ishakat/login.go), just not yet driven from inside a running TUI
// session — internal/tui cannot import net/http (internal/arch_test.go's
// TestTUINoImportaHTTP), so a real in-session wizard needs the
// HTTP-driving half injected via a factory the way EngineFactory already
// is, not written here directly. /theme (Fase 3's first increment),
// /config and /debug (both this-and-the-prior increment, configcmd.go/
// debugcmd.go) have all since moved off this path entirely into their own
// real Kind/runner, so /login is now the only row left here, and it keeps
// the generic message for the same reason: it has no stand-in command to
// point at yet.
func unimplementedNotice(cmd slash.Command) string {
	switch cmd.Name {
	case "login":
		return cmd.Usage() + " todavia no dentro de la TUI: usa `ishakat login <proveedor>` desde la terminal"
	default:
		return cmd.Usage() + " todavia no esta implementado"
	}
}

// runModelCommand implements /model's closing behaviors (§12, Step 10;
// design doc §2.1 for the two sub-verbs added alongside PR #210's picker
// UI): a leading "hide "/"keep " word routes to runModelHide (which
// dispatches on the verb itself) before any of the rest of this runs;
// otherwise, no argument opens the picker unfiltered; an argument that
// §4.5 resolves unambiguously switches straight away with the §4.6
// confirmation line, with no overlay ever drawn; anything else opens the
// picker prefiltered with what the user typed, exactly like an ambiguous
// /model query already does for the command line's own error message
// (there isn't one — §4.5 forbids a bare "model not found").
//
// "hide"/"keep" are recognised as literal leading words, not a new
// slash.Kind: internal/slash's own Command table already routes every
// argument this command takes through Kind == KindModel and hands the rest
// of the line to this function unparsed (the same shape /tools's own
// no-arg-vs-an-arg split already uses without a second Kind) — adding
// KindModelHide/KindModelKeep would just move this same strings.Cut one
// layer up for no benefit, since only this function ever needs to know
// the difference.
func (m Root) runModelCommand(args string) (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(args)
	if text == "" {
		return m.openPicker("")
	}
	if verb, rest, ok := cutModelVerb(text, "hide"); ok {
		return m.runModelHide(verb, rest)
	}
	if verb, rest, ok := cutModelVerb(text, "keep"); ok {
		return m.runModelHide(verb, rest)
	}
	res := m.cat.Resolve(text, m.resolveOptions())
	if res.Outcome.Decided() {
		return m.applyModelChosen(res.Model.Ref)
	}
	// design doc §2.3's second closing criterion / principle 4: a model
	// curation removed from m.cat entirely — an automatic [catalog.curate]
	// rule, or a curation.json hide from before this session started (see
	// Options.Hidden's own doc comment for why catalog.Resolve above can
	// never find these; m.cat simply does not contain them) — is still
	// resolvable by its exact ref rather than opening an empty/wrong
	// picker. applyModelChosen still runs the ordinary switch path;
	// commitModelSwitch's own hiddenNotice check is what makes the
	// resulting confirmation explicitly say the model is hidden, instead
	// of silently succeeding as if nothing curated it out at all.
	if h, ok := m.hiddenByRef(text); ok {
		return m.applyModelChosen(h.Model.Ref)
	}
	return m.openPicker(res.Query)
}

// cutModelVerb reports whether text starts with verb as its own leading
// word (case-insensitive, followed by whitespace or the end of the
// string — "hidex" is a query, not the verb "hide") and, if so, returns
// verb and whatever follows it, trimmed. This is deliberately narrower
// than strings.Cut(text, " "): "hide" alone (no query yet) still has to
// count as the verb, not fall through to catalog.Resolve("hide") and try
// to switch to a model literally named "hide".
func cutModelVerb(text, verb string) (matchedVerb, rest string, ok bool) {
	if !strings.EqualFold(text, verb) && !strings.HasPrefix(strings.ToLower(text), strings.ToLower(verb)+" ") {
		return "", "", false
	}
	rest = strings.TrimSpace(text[len(verb):])
	return verb, rest, true
}

// runModelHide implements "/model hide <query>" and "/model keep <query>"
// (design doc §2.1's first table row and its inverse): resolve query with
// the exact same §4.5 resolver /model's own bare form uses, so the two
// never disagree about which model a given piece of text names. An
// ambiguous query opens the picker prefiltered rather than reporting a
// bare "not found" — §4.5's own rule, which this sub-verb inherits rather
// than re-deciding. verb is "hide" or "keep", read back in the notice so
// the two share one implementation without the message losing which one
// actually ran.
func (m Root) runModelHide(verb, query string) (tea.Model, tea.Cmd) {
	g := m.lay.glyphs()
	if m.curationStore == nil {
		return m.slashNotice(g.warnMark + " no hay almacen de curacion configurado; /model " + verb + " no puede persistir nada esta sesion")
	}
	if query == "" {
		return m.slashNotice(g.warnMark + " uso: /model " + verb + " <texto>")
	}
	res := m.cat.Resolve(query, m.resolveOptions())
	if !res.Outcome.Decided() {
		return m.openPicker(res.Query)
	}
	ref := res.Model.Ref
	var err error
	if verb == "keep" {
		err = m.curationStore.Keep(ref)
	} else {
		err = m.curationStore.Hide(ref)
	}
	if err != nil {
		return m.slashNotice(g.warnMark + " no se pudo " + verb + " " + ref + ": " + err.Error())
	}
	past := "escondido"
	if verb == "keep" {
		past = "mantenido"
	}
	return m.slashNotice(g.assistantMark + " " + past + " " + ref)
}

// slashNotice appends text as a transcript entry without adding anything to
// m.conv: it is feedback about the interface itself, not part of the
// conversation a future turn would send to the model.
func (m Root) slashNotice(text string) (tea.Model, tea.Cmd) {
	m.transcript = append(m.transcript, transcriptEntry{
		role: "assistant", name: "ishakat", text: text, ts: time.Now(),
	})
	return m, nil
}

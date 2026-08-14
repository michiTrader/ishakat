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
		next, cmd := m.runSlashCommand(m.menu.Selected(), "")
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
	case slash.KindClear:
		// Same effect as ctrl+l (handleGlobalKey): only the screen is wiped,
		// the conversation itself — and what the next turn sends the model —
		// is untouched.
		m.transcript = nil
		m.printedUpTo = 0
		return m, clearScreenCmd
	case slash.KindNew:
		// Unlike /clear, /new also drops the conversation itself: the next
		// turn starts with no history at all.
		m.conv = convo.Conversation{}
		m.transcript = nil
		m.printedUpTo = 0
		return m, clearScreenCmd
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
	case slash.KindModels:
		return m.runModelsCommand()
	case slash.KindSkills:
		return m.runSkillsCommand()
	case slash.KindLogin:
		return m.startLogin(args)
	case slash.KindTheme:
		return m.runThemeCommand(args)
	case slash.KindConfig:
		return m.runConfigCommand()
	default:
		return m.slashNotice(m.lay.glyphs().warnMark + " " + unimplementedNotice(cmd))
	}
}

// unimplementedNotice is what a KindUnimplemented command reports. /debug
// already has a binary-side equivalent that answers the same question
// today (ishakat doctor); pointing at it is an honest pending, not a
// silent no-op, until it gets a real in-session screen of its own (§13,
// §17: "un pendiente marcado como hecho es una funcion que nadie va a
// construir" applies just as much to a pending with no remedy attached).
// /login (Step 24) joins it for the same reason: the OAuth device flow
// itself is real and tested (cmd/ishakat/login.go), just not yet driven
// from inside a running TUI session — internal/tui cannot import net/http
// (internal/arch_test.go's TestTUINoImportaHTTP), so a real in-session
// wizard needs the HTTP-driving half injected via a factory the way
// EngineFactory already is, not written here directly. /theme (Fase 3's
// first increment) and /config (this increment, configcmd.go) have both
// since moved off this path entirely into their own real Kind/runner, so
// /debug is now the only row left here, and it keeps the generic message
// for the same reason: it has no stand-in command to point at yet.
func unimplementedNotice(cmd slash.Command) string {
	switch cmd.Name {
	case "debug":
		return cmd.Usage() + " todavia no: usa `ishakat doctor` desde la terminal"
	case "login":
		return cmd.Usage() + " todavia no dentro de la TUI: usa `ishakat login <proveedor>` desde la terminal"
	default:
		return cmd.Usage() + " todavia no esta implementado"
	}
}

// runModelCommand implements /model's three closing behaviors (§12, Step
// 10): no argument opens the picker unfiltered; an argument that §4.5
// resolves unambiguously switches straight away with the §4.6 confirmation
// line, with no overlay ever drawn; anything else opens the picker
// prefiltered with what the user typed, exactly like an ambiguous /model
// query already does for the command line's own error message (there isn't
// one — §4.5 forbids a bare "model not found").
func (m Root) runModelCommand(args string) (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(args)
	if text == "" {
		return m.openPicker("")
	}
	res := m.cat.Resolve(text, m.resolveOptions())
	if res.Outcome.Decided() {
		return m.applyModelChosen(res.Model.Ref)
	}
	return m.openPicker(res.Query)
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

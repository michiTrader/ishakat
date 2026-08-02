// slashrun.go is the only place that knows what running a slash command
// means (§9.6/§9.7). internal/slash stays deliberately ignorant of engines,
// conversations and terminals — it only classifies a Command by Kind — so
// every branch below is UI/state work Root already owns elsewhere.
package tui

import (
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
func (m Root) runSlashCommand(cmd slash.Command, args string) (tea.Model, tea.Cmd) {
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
	default:
		return m.slashNotice(m.lay.glyphs().warnMark + " " + cmd.Usage() + " todavia no esta implementado")
	}
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

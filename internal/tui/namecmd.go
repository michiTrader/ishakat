// namecmd.go implements /name (F12, docs/ROADMAP-ux-2026-08-20.md W5): the
// user's own manual rename of the current session, filling the gap the
// window's own investigation found — convo.Store.SetTitle has existed since
// §10, documented as "the one operation that breaks append... happens once
// per session when autoname names it", but with autoname itself never
// actually built (headless.go's openSession still only ever calls
// titleFrom once, at creation) grep confirmed it had zero real call sites
// anywhere in the program. /name is that missing caller — not autoname, a
// deliberate, user-typed rename instead, which is the more honest reading
// of the roadmap's own "/name [text] names the session" ask.
//
// No argument reports the session's current title, mirroring
// runThemeCommand's own "no argument -> read-only report" shape (theme.go)
// rather than opening a picker: a title is one string, not a list to
// choose from, so there is nothing here for an overlay to add.
package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// runNameCommand implements /name's two behaviours: no argument reports the
// current title, any other text renames the session both in memory
// (m.conv.Title, so /resume's own listing and this session's own next
// report agree immediately) and on disk via titleStore, best-effort —
// switchTheme's own comment gives the identical reasoning for why a
// persistence failure still applies the in-memory change and simply says
// so, rather than discarding a rename the user already sees take effect.
func (m Root) runNameCommand(args string) (tea.Model, tea.Cmd) {
	title := strings.TrimSpace(args)
	if title == "" {
		return m.reportTitle()
	}
	return m.renameSession(title)
}

// reportTitle is /name's no-argument form: the current session's title, or
// a notice that nothing has been named yet — a session created lazily on
// first Append (session.go's own doc comment) has no title at all until
// then, the same "nothing to report" state runStats already treats as
// ordinary rather than an error.
func (m Root) reportTitle() (tea.Model, tea.Cmd) {
	g := m.lay.glyphs()
	if m.conv.Title == "" {
		return m.slashNotice(g.warnMark + " esta sesión todavía no tiene título (aún no se envió ningún mensaje)")
	}
	return m.slashNotice(g.assistantMark + " título: " + m.conv.Title)
}

// renameSession applies title immediately (m.conv.Title, read by
// reportTitle above and by /resume's own listing once this session is
// saved) and persists it through titleStore, the same best-effort order
// switchTheme already follows for /theme's own write: the display change
// is real and already visible, so a persistence failure is reported
// alongside it rather than instead of it.
func (m Root) renameSession(title string) (tea.Model, tea.Cmd) {
	g := m.lay.glyphs()
	m.conv.Title = title

	msg := g.assistantMark + " título: " + title
	if m.titleStore != nil {
		if err := m.titleStore.SetTitle(title); err != nil {
			msg += " (no se pudo guardar: " + err.Error() + ")"
		}
	}
	return m.slashNotice(msg)
}

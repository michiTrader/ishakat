package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// View satisface tea.Model. En v2 View() devuelve tea.View, no un string
// (§7.2). El modo inline es simplemente no activar AltScreen: conservamos el
// scrollback real del terminal en vez de tomar la pantalla completa.
func (m Root) View() tea.View {
	var v tea.View
	v.SetContent(m.render())
	v.AltScreen = false
	v.MouseMode = tea.MouseModeNone
	v.Cursor = m.cursorFor()
	return v
}

// render arma la región viva completa: banner (solo al arranque, cuando no
// hay transcript todavía), transcript comprometido, turno vivo si lo hay, y
// la caja de entrada con el footer.
func (m Root) render() string {
	return m.fold(m.renderRaw())
}

// fold is the single point where a restricted terminal gets a string it can
// actually represent. It sits on the way out of render rather than inside each
// component for the reason the glyph table exists at all: a rule applied at one
// choke point can be checked, and a rule applied in six render functions is a
// rule that will be forgotten in the seventh.
//
// Escape sequences are ASCII, so folding the finished frame does not touch the
// colours the styles put in.
func (m Root) fold(s string) string {
	if !m.lay.ASCII() {
		return s
	}
	return foldASCII(s)
}

func (m Root) renderRaw() string {
	if m.mode == ModeHelp {
		return m.renderHelp()
	}
	if m.mode == ModePicker {
		return m.renderPicker()
	}
	if m.mode == ModeConfirm {
		return m.renderConfirm()
	}
	if m.mode == ModeCompact {
		return m.renderCompact()
	}
	if m.mode == ModeResume {
		return m.renderResumeMenu()
	}
	if m.mode == ModeToolApprove {
		return m.renderToolApprove()
	}
	if m.mode == ModeLogin {
		return m.renderLogin()
	}
	if m.mode == ModeSuggest {
		return m.renderSuggest()
	}
	if m.mode == ModeThemePicker {
		return m.renderThemePicker()
	}
	if m.mode == ModeTrust {
		return m.renderTrust()
	}
	if m.mode == ModeMission {
		return m.renderMission()
	}

	var b strings.Builder
	b.WriteString(m.head())
	if menu := m.slashMenuBlock(); menu != "" {
		b.WriteString(menu)
	}
	b.WriteString(InputBox(m.lay, m.styles, m.input.View()))
	b.WriteString("\n")
	b.WriteString(RenderFooter(m.lay, m.footerState(), m.footerItems))

	return b.String()
}

// slashMenuBlock is the §9.6 dropdown as drawn directly above the input box,
// or "" when it has nothing to show. It is a method of its own — like
// head() — because render and cursorFor must agree on its height down to the
// row.
func (m Root) slashMenuBlock() string {
	if !m.menu.Active() {
		return ""
	}
	return renderSlashMenu(m.lay, m.styles, m.menu) + "\n"
}

// head is everything drawn above the input box: the start-up banner (only
// while there is nothing in the transcript), the committed transcript and the
// live turn.
//
// It is a method of its own because render and cursorFor must agree on its
// height down to the row. Measuring one thing and drawing another is precisely
// how the cursor ended up next to the banner.
func (m Root) head() string {
	g := m.lay.glyphs()
	var b strings.Builder

	if banner := m.bannerText(); banner != "" {
		b.WriteString(banner)
		b.WriteString("\n\n")
	}

	// Only entries not yet handed to commitEntryCmd are redrawn here.
	// Printed ones already live in the terminal's real scrollback (§7.5);
	// keeping them here too would be drawing the same line twice and, past a
	// certain history length, is exactly what grew the live region past the
	// terminal's height (see commitEntryCmd's comment).
	width := m.lay.ContentWidth()
	for _, e := range m.transcript[m.printedUpTo:] {
		b.WriteString(renderTranscriptLine(m.styles, g, width, e.role, e.name, e.text, e.ts, m.cfgSyntax, m.cfgMarkdown))
		b.WriteString("\n\n")
	}

	if m.live.active {
		b.WriteString(renderLiveTurn(m.styles, g, width, m.live, CrushFrame(m.lay, m.animOffset), " esc cancela\n", m.cfgSyntax, m.cfgMarkdown))
		b.WriteString("\n")
	}

	return b.String()
}

// headRows is how many terminal rows head occupies. Every block head writes
// ends with a newline, so the number of rows above the input box is exactly
// the number of newlines. Bubble Tea's inline renderer clips content to the
// terminal width instead of wrapping it, so a long line still costs one row
// and no wrap arithmetic is needed here.
func headRows(head string) int { return strings.Count(head, "\n") }

// bannerText is the startup banner's rendered form, or "" once it should no
// longer be part of the live-managed region — the same condition head() used
// to check inline before submit (see its own comment) also needed this exact
// string, to hand to tea.Println instead of to head() on the one frame the
// condition flips from true to false.
func (m Root) bannerText() string {
	if len(m.transcript) != 0 || m.live.active {
		return ""
	}
	return Banner(m.lay, m.styles, m.version, m.bannerPath(), m.footer.Model, m.cfgBanner, m.animOffset)
}

// bannerPath is the working directory as the banner shows it: the whole path,
// abbreviated only as much as the terminal width forces.
func (m Root) bannerPath() string {
	// "ishakat " + version + " · " is everything the line spends before the
	// path, so that is exactly what the path budget has to give up. The
	// separator is measured rather than assumed: it is a glyph now, so its
	// width is a property of the terminal, and len() would have counted the
	// two bytes of "·" as two columns.
	spent := lipglossWidth("ishakat  "+m.lay.glyphs().dot+" ") + lipglossWidth(m.version)
	return ShortenPath(m.cwd, m.lay.ContentWidth()-spent)
}

// footerCWDShare is the fraction of the footer the path is allowed to take.
// The footer already drops items right to left when it overflows, but a full
// path would starve the model name — the one item nobody wants to lose —
// before the dropping logic ever got a chance to run.
const footerCWDShare = 3

// footerMinCWD is the floor of that share: below six columns the path becomes
// a single letter plus an ellipsis, which is noise rather than information.
const footerMinCWD = 6

// footerState fills in the parts of the footer that depend on the current
// width, leaving everything else as the model holds it.
func (m Root) footerState() FooterState {
	st := m.footer
	budget := m.lay.ContentWidth() / footerCWDShare
	if budget < footerMinCWD {
		budget = footerMinCWD
	}
	st.CWD = ShortenPath(m.cwd, budget)
	return st
}

// cursorFor calcula dónde debe pintarse el cursor real de la terminal: dentro
// del textarea cuando el input tiene foco, o nil cuando no hay nada que
// editar (ModeBusy, ModeHelp).
//
// textarea.Cursor() reports a position relative to the widget's own top-left
// corner, which the widget documents and which is easy to miss: returning it
// untouched puts the cursor at row 0 of the whole view — right next to the
// banner — instead of inside the input box. The offset added here is the box
// origin plus every row drawn above it.
func (m Root) cursorFor() *tea.Cursor {
	if m.mode != ModeChat {
		return nil
	}
	c := m.input.Cursor()
	if c == nil {
		return nil
	}
	dx, dy := InputOrigin(m.lay)
	c.Position.X += dx
	c.Position.Y += dy + headRows(m.head()) + headRows(m.slashMenuBlock())
	return c
}

// renderHelp draws the §9.7 help screen. The command list is generated from
// m.commands (internal/slash.Registry.HelpLines) rather than hand-written:
// Step 9 closes the gap this function's comment used to document — the
// dropdown (slashMenuBlock) reads the very same table.
func (m Root) renderHelp() string {
	g := m.lay.glyphs()
	var b strings.Builder
	b.WriteString(helpHeading(g, "ishakat "+g.dot+" comandos") + "\n\n")
	for _, line := range m.commands.HelpLines() {
		b.WriteString(" " + line + "\n")
	}
	b.WriteString("\n" + helpHeading(g, "atajos") + "\n\n")
	for _, line := range []string{
		"ctrl+p   selector de modelos",
		"ctrl+o   rotar favoritos",
		"ctrl+t   selector de temas",
		"ctrl+j   salto de línea",
		"esc      cancelar generación",
		"ctrl+c×2 salir",
		"ctrl+l   limpiar pantalla",
		"ctrl+y   copiar última respuesta",
	} {
		b.WriteString(" " + line + "\n")
	}
	b.WriteString(fmt.Sprintf("\n %s desplazar %s esc volver", g.scrollHint, g.dot))
	return b.String()
}

// helpWidth is how wide the help screen's rules are drawn. The screen is a
// fixed list of short lines, so it does not follow the terminal width.
const helpWidth = 38

// helpHeading draws a section heading padded out to helpWidth with the rule
// glyph.
//
// The headings used to be literal runs of U+2500 counted out by hand, which had
// two problems: the two of them came out different lengths (visible on screen,
// and impossible to keep aligned when either title is edited), and the box
// drawing character is one more thing a legacy console renders as garbage.
func helpHeading(g glyphs, title string) string {
	const lead = 2
	prefix := strings.Repeat(g.rule, lead) + " " + title + " "
	if fill := helpWidth - lipglossWidth(prefix); fill > 0 {
		return prefix + strings.Repeat(g.rule, fill)
	}
	return prefix
}

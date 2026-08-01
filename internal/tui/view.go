package tui

import (
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
	if m.mode == ModeHelp {
		return m.renderHelp()
	}

	var b strings.Builder

	if len(m.transcript) == 0 && !m.live.active {
		if banner := Banner(m.lay, m.styles, m.version, m.bannerPath(), m.footer.Model, m.cfgBanner, m.animOffset); banner != "" {
			b.WriteString(banner)
			b.WriteString("\n\n")
		}
	}

	for _, e := range m.transcript {
		b.WriteString(renderTranscriptLine(e.role, e.name, e.text, e.ts))
		b.WriteString("\n\n")
	}

	if m.live.active {
		b.WriteString(renderLiveTurn(m.live, CrushFrame(m.animOffset), " esc cancela\n"))
		b.WriteString("\n")
	}

	b.WriteString(InputBox(m.lay, m.styles, m.lay.InputPrefix(), m.input.Value()))
	b.WriteString("\n")
	b.WriteString(RenderFooter(m.lay, m.footerState(), m.footerItems))

	return b.String()
}

// bannerPath is the working directory as the banner shows it: the whole path,
// abbreviated only as much as the terminal width forces.
func (m Root) bannerPath() string {
	// "ishakat " + version + " · " is everything the line spends before the
	// path, so that is exactly what the path budget has to give up.
	spent := len("ishakat ") + len([]rune(m.version)) + len([]rune(" · "))
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
func (m Root) cursorFor() *tea.Cursor {
	if m.mode != ModeChat {
		return nil
	}
	return m.input.Cursor()
}

// renderHelp dibuja la pantalla de ayuda de §9.7. El registro de slash
// commands llega en el Paso 9; hasta entonces esta lista es estática y
// documenta el mismo contrato que consumirá slash.Registry.
func (m Root) renderHelp() string {
	var b strings.Builder
	b.WriteString("── ishakat · comandos ────────────────\n\n")
	for _, line := range []string{
		"/help              esta pantalla",
		"/model [texto]     cambiar modelo",
		"/models            explorar catálogo",
		"/theme [nombre]    cambiar tema",
		"/compact           resumir contexto",
		"/new               conversación nueva",
		"/resume            reabrir una sesión",
		"/clear             limpiar pantalla",
		"/copy [n]          copiar respuesta",
		"/retry             reintentar último",
		"/stats             tokens y costo",
		"/config            config efectiva",
		"/debug             diagnóstico",
		"/exit              salir",
	} {
		b.WriteString(" " + line + "\n")
	}
	b.WriteString("\n── atajos ────────────────────────────\n\n")
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
	b.WriteString("\n ↑↓ desplazar · esc volver")
	return b.String()
}

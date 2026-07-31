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
		if banner := Banner(m.lay, m.styles, m.version, shortCWD(m.cwd), m.footer.Model, m.cfgBanner, m.animOffset); banner != "" {
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
	b.WriteString(RenderFooter(m.lay, m.footer, nil))

	return b.String()
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

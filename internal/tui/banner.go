package tui

import (
	"fmt"
	"strings"
)

// asciiLogo es el logo de arranque (§9.2), medido para caber en 40 columnas.
var asciiLogo = []string{
	`▄▄▖ ▄▄▖  ▄▖  ▄▄▖  ▄▖`,
	`██▌ ██▌ ▐██▌ ▝▀█▖ ▄▖ `,
	`▀▀▘ ▀▘▘ ▀▘▝▘ ▀▀▘  ▀▘ `,
}

// stylesLike es lo mínimo que banner.go necesita de theme.Styles, para no
// acoplar este archivo al tipo concreto y poder probarlo con un doble simple.
type stylesLike interface {
	GradientLines(block string, offset int) string
	DimRender(s string) string
}

// Banner arma el bloque de arranque completo: logo con degradado, línea de
// versión y ruta, línea de proveedor/modelo/contexto, y la sugerencia de
// /help. Devuelve cadena vacía si lay decide que no corresponde mostrarlo.
func Banner(lay Layout, s stylesLike, version, cwd, providerLine string, showBanner bool, offset int) string {
	if !lay.ShowBanner(showBanner) {
		return ""
	}
	logo := s.GradientLines(strings.Join(asciiLogo, "\n"), offset)

	var b strings.Builder
	b.WriteString(logo)
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("ishakat %s · %s\n", version, cwd))
	if providerLine != "" {
		b.WriteString(providerLine + "\n")
	}
	b.WriteString("\n")
	b.WriteString(s.DimRender("Escribe para empezar. /help ayuda."))
	return b.String()
}

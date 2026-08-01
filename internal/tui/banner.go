package tui

import (
	"fmt"
	"strings"
)

// wordmarkLetters spells ISHAKAT in a three-row face, one entry per letter.
//
// The old logo was six quadrant blocks (▖ ▘ ▝ ▗) arranged into shapes that
// spelled nothing in particular; on a console without those glyphs it was a
// grid of boxes, and on a console with them it still did not read as a word.
// Both halves of that are fixed here.
//
// The face is drawn on a 6-pixel-tall grid and encoded two pixel rows per
// character, so the whole alphabet needs exactly three characters: "▀" for the
// top half, "▄" for the bottom half and "█" for both. Those three are in WGL4,
// in cp437, and in every monospace font that has ever shipped on Windows — the
// narrowest set that can still draw letters, which is the point.
//
// Letters are stored per-letter and joined at render time rather than as three
// pre-baked lines: a 28-column string is impossible to review, whereas a
// misaligned letter here is obvious.
var wordmarkLetters = [][3]string{
	{"▀█▀", " █ ", "▄█▄"},    // I
	{"█▀▀", "▀▀█", "▄▄█"},    // S
	{"█ █", "█▀█", "█ █"},    // H
	{"▄▀▄", "█▀█", "█ █"},    // A
	{"█ ▄▀", "██  ", "█ ▀▄"}, // K
	{"▄▀▄", "█▀█", "█ █"},    // A
	{"▀█▀", " █ ", " █ "},    // T
}

// asciiWordmark is the same name for a console that cannot be trusted with a
// single byte above U+007F. A pixel face needs shading to read, and ASCII has
// none worth the name at three rows tall, so it does not try: letter-spaced
// capitals over a rule are legible everywhere and honest about the constraint.
var asciiWordmark = []string{
	"I S H A K A T",
	strings.Repeat("=", 13),
}

// Wordmark is the logo as lines, in whichever repertoire lay allows.
func Wordmark(lay Layout) []string {
	if lay.ASCII() {
		return append([]string(nil), asciiWordmark...)
	}
	rows := make([]string, 3)
	for r := range rows {
		parts := make([]string, 0, len(wordmarkLetters))
		for _, letter := range wordmarkLetters {
			parts = append(parts, letter[r])
		}
		rows[r] = strings.Join(parts, " ")
	}
	return rows
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
	g := lay.glyphs()
	logo := s.GradientLines(strings.Join(Wordmark(lay), "\n"), offset)

	var b strings.Builder
	b.WriteString(logo)
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("ishakat %s %s %s\n", version, g.dot, cwd))
	if providerLine != "" {
		b.WriteString(providerLine + "\n")
	}
	b.WriteString("\n")
	b.WriteString(s.DimRender("Escribe para empezar. /help ayuda."))
	return b.String()
}

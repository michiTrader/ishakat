package tui

import "strings"

// crushWidth is how many columns the animation occupies. It is fixed so the
// text that follows it never shifts sideways between frames.
const crushWidth = 9

// CrushFrame builds the strip of characters for the frame at offset, in
// whichever repertoire lay allows.
//
// The strip used to be a hardcoded run of quadrant blocks (U+259A, U+259E,
// U+2598, U+259D, U+2597) — exactly the family Consolas does not ship, so on
// the console in the report the "thinking" line was nine boxes crawling across
// the screen. The Unicode table uses the shading blocks instead: they are in
// WGL4 and in cp437, and a wave of them reads as motion better than the
// corners did.
//
// It takes the Layout rather than the glyph table because every other renderer
// in the package takes the Layout, and one function with a different convention
// is one function whose callers have to think.
func CrushFrame(lay Layout, offset int) string {
	frames := lay.glyphs().spinner
	n := len(frames)
	if n == 0 {
		return ""
	}
	var b strings.Builder
	b.Grow(crushWidth * 4)
	for i := 0; i < crushWidth; i++ {
		b.WriteRune(frames[modCrush(offset+i, n)])
	}
	return b.String()
}

// modCrush es el mismo módulo no negativo que usa theme.Styles.Gradient; se
// repite aquí (con nombre distinto para no chocar con otros archivos del
// paquete) para no acoplar tui a un símbolo no exportado de otro paquete.
func modCrush(a, n int) int {
	if n <= 0 {
		return 0
	}
	m := a % n
	if m < 0 {
		m += n
	}
	return m
}

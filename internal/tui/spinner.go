package tui

import "strings"

// crushWidth is how many columns the animation occupies. It is fixed so the
// text that follows it never shifts sideways between frames.
//
// F15 (roadmap W5, "prefer a single rotating glyph over the current
// animation") is why this is 1 rather than 9: the strip used to crawl a
// nine-column wave of shading blocks across the screen, which read as a
// loading bar, not as "the model is thinking". One rotating glyph — the
// same shape every terminal spinner uses — says the same thing in one
// column instead of nine, and the fixed width still holds the invariant
// this constant exists for (the "pensando Ns" text after it never shifts).
const crushWidth = 1

// CrushFrame builds the strip of characters for the frame at offset, in
// whichever repertoire lay allows.
//
// The strip used to be a hardcoded run of quadrant blocks (U+259A, U+259E,
// U+2598, U+259D, U+2597) — exactly the family Consolas does not ship — then
// (§17 2026-08-13) a nine-column wave of shading blocks, which are in WGL4
// and in cp437 but read as a progress bar rather than a spinner. F15 (roadmap
// W5) replaced that wave with glyphs.spinner's own now-single-rune-per-frame
// rotation: a classic four-position turning glyph. The Unicode table's frames
// (↑ → ↓ ←, U+2190..U+2195) are in WGL4 and in cp437 — every requirement this
// package's own repertoire rule checks for — which is also why a literal
// braille frame (the "⠴" the report asked for verbatim) was rejected: the
// whole U+2800..U+28FF Braille Patterns block is outside WGL4, exactly the
// class of glyph this file's own convention already excludes on sight.
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

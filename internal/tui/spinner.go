package tui

import "strings"

// crushFrames es el charset de la animación de "pensando" (§9.3): caracteres
// cortos que ciclan con el degradado del tema desplazándose por encima. No es
// bubbles/spinner porque necesitamos que cada carácter reciba un color
// distinto del degradado, cosa que spinner.Model no ofrece.
var crushFrames = []rune("▚▞▘▝▚▗▘▚▞")

const crushWidth = 9

// CrushFrame arma la tira de caracteres de la animación para el fotograma
// dado por offset.
func CrushFrame(offset int) string {
	n := len(crushFrames)
	if n == 0 {
		return ""
	}
	var b strings.Builder
	b.Grow(crushWidth * 3)
	for i := 0; i < crushWidth; i++ {
		b.WriteRune(crushFrames[modCrush(offset+i, n)])
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

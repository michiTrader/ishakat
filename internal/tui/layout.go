// Package tui es el modelo raíz de Bubble Tea (§7 y §9 del PLAN). No importa
// net/http ni ningún paquete de proveedor: la frontera de §6.1 se prueba en
// internal/arch_test.go con `go list -deps`.
package tui

import "github.com/MichiTrader/ishakat/internal/theme"

// Breakpoint es el ancho de terminal categorizado según §9.1 del plan. Se
// recalcula en cada tea.WindowSizeMsg y de él cuelgan todas las decisiones de
// layout: nada de leer m.lay.Width directamente desde los componentes.
type Breakpoint int

const (
	// BPMinimo es bajo 40 columnas: sin cajas, sin banner, sin animaciones,
	// prefijos de un carácter, footer de una línea recortada.
	BPMinimo Breakpoint = iota
	// BPEstrecho es de 40 a 59 columnas: Termux en vertical. El que hay que
	// hacer bien porque es el uso real más común del proyecto.
	BPEstrecho
	// BPNormal es de 60 a 99 columnas: bordes completos, dropdown de
	// autocompletado, footer de dos secciones.
	BPNormal
	// BPAncho es 100 columnas o más: el selector pasa a dos columnas con
	// panel de detalle y el texto se limita a ui.max_width.
	BPAncho
)

// ClassifyBreakpoint mapea un ancho de terminal al breakpoint de §9.1.
func ClassifyBreakpoint(width int) Breakpoint {
	switch {
	case width < 40:
		return BPMinimo
	case width < 60:
		return BPEstrecho
	case width < 100:
		return BPNormal
	default:
		return BPAncho
	}
}

// Layout junta el tamaño de terminal con las decisiones derivadas de él y de
// la configuración (animaciones, max_width). Es lo único que los componentes
// de tui consultan para decidir cómo dibujarse.
type Layout struct {
	Width  int
	Height int

	Breakpoint Breakpoint

	// MaxWidth es ui.max_width; 0 significa sin límite. Solo se aplica en
	// BPAncho, donde una línea de texto sin límite se vuelve ilegible.
	MaxWidth int

	// AnimationsOff refleja ui.animations.mode == "off" o NO_COLOR/dumb: sin
	// esto el spinner seguiría corriendo aunque no haya nada que animar.
	AnimationsOff bool

	// NoTTY indica que la salida no es una terminal real (pipe, redirección).
	// Sin TTY no hay cursor visible ni sentido en animar nada.
	NoTTY bool

	// Glyphs is which characters the components may draw ([ui] glyphs). It sits
	// in Layout for the same reason the breakpoint does: it is a property of the
	// terminal that every component has to respect, and Layout is the only thing
	// components are allowed to consult. The zero value is theme.GlyphsUnicode,
	// which keeps every existing caller drawing what it drew before.
	Glyphs theme.GlyphSet
}

// WithGlyphs returns a copy of the layout restricted to the given glyph set.
//
// It is a method rather than a sixth parameter of NewLayout because a call with
// three trailing booleans and an enum stops telling the reader anything, and
// because the glyph set has to survive a resize: Update rebuilds the layout
// from the new size and then re-applies this.
func (l Layout) WithGlyphs(g theme.GlyphSet) Layout {
	l.Glyphs = g
	return l
}

// ASCII reports whether this layout is restricted to ASCII decorations.
func (l Layout) ASCII() bool { return l.Glyphs.ASCII() }

// NewLayout construye un Layout a partir del tamaño reportado por
// tea.WindowSizeMsg y las opciones fijas de la sesión.
func NewLayout(width, height, maxWidth int, animationsOff, noTTY bool) Layout {
	return Layout{
		Width:         width,
		Height:        height,
		Breakpoint:    ClassifyBreakpoint(width),
		MaxWidth:      maxWidth,
		AnimationsOff: animationsOff,
		NoTTY:         noTTY,
	}
}

// ContentWidth es el ancho útil para texto de prosa: todo el ancho de
// terminal, salvo en BPAncho donde se limita a MaxWidth si está configurado.
func (l Layout) ContentWidth() int {
	w := l.Width
	if l.Breakpoint == BPAncho && l.MaxWidth > 0 && l.MaxWidth < w {
		w = l.MaxWidth
	}
	if w < 1 {
		w = 1
	}
	return w
}

// ShowBanner decide si el banner de arranque (§9.2) cabe y corresponde:
// necesita TTY, animaciones no forzosamente pero sí alto suficiente, y nunca
// aparece en BPMinimo porque a 40 columnas el ASCII art no entra limpio.
func (l Layout) ShowBanner(cfgBanner bool) bool {
	if !cfgBanner || l.NoTTY {
		return false
	}
	if l.Breakpoint == BPMinimo {
		return false
	}
	return l.Height >= 20
}

// ShowBorders indica si los componentes deben dibujar caja completa
// (bordes redondeados) en vez de degradar a un prefijo simple. Bajo
// BPMinimo cualquier borde roba columnas que hacen falta para el texto.
func (l Layout) ShowBorders() bool { return l.Breakpoint != BPMinimo }

// ShowBoxedInput indica si la caja de entrada dibuja su borde propio. En
// BPMinimo el input es una sola línea con "> " de prefijo.
func (l Layout) ShowBoxedInput() bool { return l.Breakpoint != BPMinimo }

// InputPrefix es el prefijo de la línea de entrada: "› " normalmente, o
// solo un prefijo de un carácter (bajo 40).
//
// With GlyphsASCII the guillemet becomes ">", which is the same width and needs
// no font: the prompt is the one glyph the user stares at while typing, so it is
// the last place to gamble on a code page.
func (l Layout) InputPrefix() string {
	mark := l.glyphs().inputPrefix
	if l.Breakpoint == BPMinimo {
		return mark
	}
	return mark + " "
}

// FooterSections indica cuántas secciones debe mostrar el footer: una sola
// línea recortada en BPMinimo, dos en el resto (§9.3, §9.6).
func (l Layout) FooterSections() int {
	if l.Breakpoint == BPMinimo {
		return 1
	}
	return 2
}

// StreamIntervalMS es el intervalo de drenado del StreamBuf (§7.3) en
// milisegundos: 50ms en operación normal. El modo ahorro de batería lo dobla;
// eso lo decide quien arme el Root a partir de ui.animations.battery_saver,
// no Layout.
const StreamIntervalMS = 50

// AnimFPS es el techo de fotogramas por segundo para spinner y degradado en
// movimiento (§14): 12fps en operación normal.
const AnimFPS = 12

// BatterySaverFPS es el techo cuando ui.animations.battery_saver está activo.
const BatterySaverFPS = 6

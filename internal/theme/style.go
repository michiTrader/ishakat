package theme

import (
	"image/color"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
)

// Capability es lo que el terminal puede pintar.
type Capability int

const (
	CapNone Capability = iota // NO_COLOR o TERM=dumb
	Cap16
	Cap256
	CapTruecolor
)

func (c Capability) String() string {
	switch c {
	case CapNone:
		return "sin color"
	case Cap16:
		return "16 colores"
	case Cap256:
		return "256 colores"
	default:
		return "truecolor"
	}
}

// Detect resuelve la capacidad de color leyendo el entorno, con override por
// [ui] color de la configuración ("auto" | "never" | "always" | "16" | "256" |
// "truecolor").
//
// Bubble Tea v2 hace el downsampling solo, así que esto se usa para decidir si
// hay color y para reportarlo en `ishakat doctor`, no para elegir paletas a mano.
func Detect(override string) Capability {
	switch strings.ToLower(strings.TrimSpace(override)) {
	case "never", "none", "off", "0":
		return CapNone
	case "16":
		return Cap16
	case "256":
		return Cap256
	case "always", "truecolor", "24bit":
		return CapTruecolor
	}

	// NO_COLOR gana sobre cualquier detección: es un contrato entre programas
	// de terminal y respetarlo cuesta una línea. Una variable definida pero
	// vacía (como la deja t.Setenv al "limpiarla") no cuenta como activada.
	if v := os.Getenv("NO_COLOR"); v != "" && v != "0" {
		return CapNone
	}

	term := os.Getenv("TERM")
	if term == "" || term == "dumb" {
		return CapNone
	}
	switch strings.ToLower(os.Getenv("COLORTERM")) {
	case "truecolor", "24bit":
		return CapTruecolor
	}
	if strings.Contains(term, "256") {
		return Cap256
	}
	if strings.Contains(term, "kitty") || strings.Contains(term, "alacritty") ||
		strings.Contains(term, "wezterm") || strings.Contains(term, "ghostty") {
		return CapTruecolor
	}
	return Cap16
}

// Styles son los estilos de lipgloss derivados del tema. Se construyen una vez
// y se guardan en el modelo: crear estilos en cada render es el desperdicio
// clásico de los TUIs.
type Styles struct {
	Theme Theme
	Cap   Capability

	FG        lipgloss.Style
	Dim       lipgloss.Style
	Accent    lipgloss.Style
	User      lipgloss.Style
	Assistant lipgloss.Style
	Border    lipgloss.Style
	Success   lipgloss.Style
	Warn      lipgloss.Style
	Error     lipgloss.Style
	Code      lipgloss.Style
	Box       lipgloss.Style
}

// NewStyles construye los estilos. Con CapNone todos quedan sin color, así el
// resto del código no tiene que preguntar nunca si hay color o no.
func NewStyles(t Theme, cap Capability) Styles {
	plain := cap == CapNone
	fg := func(c RGB) lipgloss.Style {
		if plain {
			return lipgloss.NewStyle()
		}
		return lipgloss.NewStyle().Foreground(lipgloss.Color(c.Hex()))
	}

	s := Styles{
		Theme:     t,
		Cap:       cap,
		FG:        fg(t.FG),
		Dim:       fg(t.FGDim),
		Accent:    fg(t.Accent),
		User:      fg(t.User),
		Assistant: fg(t.Assistant),
		Border:    fg(t.Border),
		Success:   fg(t.Success),
		Warn:      fg(t.Warn),
		Error:     fg(t.Error),
		Code:      fg(t.FG),
	}
	s.Box = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor(t, plain))
	return s
}

func borderColor(t Theme, plain bool) color.Color {
	if plain {
		return lipgloss.NoColor{}
	}
	return lipgloss.Color(t.Border.Hex())
}

// Gradient pinta cada carácter visible del texto con el degradado del tema.
// offset desplaza el degradado, que es lo que hace que la animación "corra"
// cuando gradient.scroll está activo.
func (s Styles) Gradient(text string, offset int) string {
	if s.Cap == CapNone || s.Cap == Cap16 {
		// A 16 colores un degradado por carácter se ve peor que un color
		// plano, y sin color no hay nada que hacer.
		return s.Accent.Render(text)
	}
	runes := []rune(text)
	if len(runes) == 0 {
		return ""
	}
	ramp := s.Theme.Gradient(maxInt(len(runes), 2))
	var b strings.Builder
	b.Grow(len(text) * 12)
	for i, r := range runes {
		if r == ' ' || r == '\n' {
			b.WriteRune(r)
			continue
		}
		c := ramp[mod(i+offset, len(ramp))]
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(c.Hex())).Render(string(r)))
	}
	return b.String()
}

// GradientLines aplica el degradado a un bloque multilínea manteniendo la
// posición horizontal, para que el degradado del banner sea vertical-coherente.
func (s Styles) GradientLines(block string, offset int) string {
	lines := strings.Split(block, "\n")
	for i, ln := range lines {
		lines[i] = s.Gradient(ln, offset)
	}
	return strings.Join(lines, "\n")
}

func mod(a, n int) int {
	if n <= 0 {
		return 0
	}
	m := a % n
	if m < 0 {
		m += n
	}
	return m
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

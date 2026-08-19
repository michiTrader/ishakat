// Package theme es el contrato 4 del PLAN (§8): el tema es un archivo de datos,
// no código. Agregar un tema es dejar un TOML en
// $XDG_CONFIG_HOME/ishakat/themes/ y nombrarlo en la configuración.
package theme

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

//go:embed ascua.toml
var embedded embed.FS

// Default es el nombre del tema embebido, el único garantizado.
const Default = "ascua"

// File es el archivo de tema tal como se declara en TOML.
type File struct {
	Name     string                       `toml:"name"`
	Dark     bool                         `toml:"dark"`
	Gradient GradientSpec                 `toml:"gradient"`
	Colors   map[string]string            `toml:"colors"`
	Syntax   map[string]string            `toml:"syntax"`
	Fallback map[string]map[string]string `toml:"fallback"`
}

// GradientSpec es la sección [gradient].
type GradientSpec struct {
	Space  string   `toml:"space"`
	Stops  []string `toml:"stops"`
	Scroll bool     `toml:"scroll"`
}

// Theme es el tema ya resuelto: colores parseados y listos para usar.
type Theme struct {
	Name   string
	Dark   bool
	Space  Space
	Stops  []RGB
	Scroll bool

	FG        RGB
	FGDim     RGB
	Accent    RGB
	User      RGB
	Assistant RGB
	Border    RGB
	Success   RGB
	Warn      RGB
	Error     RGB
	CodeBG    RGB

	// UserBG is the background painted behind a user message's whole body
	// (§17 2026-08-19 "user messages should have a different background
	// color" — a distinct requirement from the same entry's header-
	// foreground fix, already closed in the previous session). It is
	// deliberately its own field rather than a derived tint of User: a
	// theme author picking a background wants a colour dark/desaturated
	// enough to sit behind readable text at every capability level, which
	// is not automatically true of whatever hue looks good as a thin
	// header accent — ascua.toml's own `#7fd1b9` teal, painted solid
	// behind a paragraph of `#e8e6e3` text, would be legible but far
	// brighter than every other surface in the interface. A theme's TOML
	// that predates this field (or simply omits `user_bg`) is not left
	// with a jarring black box: parse (below) starts every field from
	// `base` — builtinFallback's own value — exactly as it already does
	// for every other colour a theme leaves unset, so an old theme file
	// keeps a sensible default rather than RGB{}'s pure black.
	UserBG RGB

	Syntax map[string]RGB

	// Source es de dónde salió: "embebido" o la ruta del archivo.
	Source string
	// Warnings son los problemas no fatales encontrados al cargar. Un color
	// inválido no impide arrancar: se usa el del tema base y se avisa.
	Warnings []string
}

// builtinFallback es el tema base en memoria. Existe para que ningún error de
// archivo pueda dejar la interfaz sin colores.
func builtinFallback() Theme {
	must := func(s string) RGB { c, _ := ParseHex(s); return c }
	return Theme{
		Name:      Default,
		Dark:      true,
		Space:     SpaceOklab,
		Scroll:    true,
		Stops:     []RGB{must("#ff6a3d"), must("#ffa63d"), must("#ffe0a3")},
		FG:        must("#e8e6e3"),
		FGDim:     must("#8a8580"),
		Accent:    must("#ff8a3d"),
		User:      must("#7fd1b9"),
		Assistant: must("#ffb454"),
		Border:    must("#4a443f"),
		Success:   must("#8ec07c"),
		Warn:      must("#e8b25c"),
		Error:     must("#f2635f"),
		CodeBG:    must("#1c1a18"),
		// A dark, desaturated tint of User (#7fd1b9) rather than the teal
		// itself: painted solid behind a whole paragraph of #e8e6e3 text,
		// the bright accent would be louder than every other surface in
		// the interface (see UserBG's own doc comment above) — this is
		// close in hue to CodeBG (#1c1a18) so the two dark surfaces read
		// as part of the same palette, but distinct enough (cooler, a
		// touch of green) to still tell a user bubble's background apart
		// from a code block's at a glance.
		UserBG: must("#182420"),
		Syntax: map[string]RGB{},
		Source: "compilado",
	}
}

// Load resuelve un tema por nombre. Busca primero en dirs (normalmente
// $XDG_CONFIG_HOME/ishakat/themes) y cae al tema embebido.
//
// Nunca devuelve error por un tema roto: la interfaz tiene que arrancar. Los
// problemas viajan en Theme.Warnings y se ven en `ishakat doctor`.
func Load(name string, dirs ...string) Theme {
	if strings.TrimSpace(name) == "" {
		name = Default
	}
	base := builtinFallback()

	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		p := filepath.Join(dir, name+".toml")
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		th, warns := parse(b, base)
		th.Source = p
		th.Warnings = warns
		if th.Name == "" {
			th.Name = name
		}
		return th
	}

	// Tema embebido.
	if b, err := embedded.ReadFile(name + ".toml"); err == nil {
		th, warns := parse(b, base)
		th.Source = "embebido"
		th.Warnings = warns
		if th.Name == "" {
			th.Name = name
		}
		return th
	}

	if name != Default {
		th := Load(Default, dirs...)
		th.Warnings = append(th.Warnings,
			fmt.Sprintf("el tema %q no existe; se usa %q", name, Default))
		return th
	}
	return base
}

// Available lista los nombres de tema disponibles: los archivos de dirs más el
// embebido, sin repetidos.
func Available(dirs ...string) []string {
	seen := map[string]bool{Default: true}
	out := []string{Default}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
				continue
			}
			n := strings.TrimSuffix(e.Name(), ".toml")
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	return out
}

func parse(b []byte, base Theme) (Theme, []string) {
	var f File
	if err := toml.Unmarshal(b, &f); err != nil {
		return base, []string{fmt.Sprintf("tema ilegible: %v", err)}
	}

	th := base
	var warns []string

	th.Name = f.Name
	th.Dark = f.Dark
	if !hasKey(b, "dark") {
		th.Dark = base.Dark
	}

	switch Space(strings.ToLower(f.Gradient.Space)) {
	case SpaceOklab, "":
		th.Space = SpaceOklab
	case SpaceOklch:
		th.Space = SpaceOklch
	case SpaceHSL:
		th.Space = SpaceHSL
	default:
		warns = append(warns, fmt.Sprintf("gradient.space %q desconocido; se usa oklab", f.Gradient.Space))
		th.Space = SpaceOklab
	}
	th.Scroll = f.Gradient.Scroll

	if len(f.Gradient.Stops) > 0 {
		stops := make([]RGB, 0, len(f.Gradient.Stops))
		for _, s := range f.Gradient.Stops {
			c, err := ParseHex(s)
			if err != nil {
				warns = append(warns, fmt.Sprintf("gradient.stops: %v", err))
				continue
			}
			stops = append(stops, c)
		}
		if len(stops) > 0 {
			th.Stops = stops
		}
	}

	targets := map[string]*RGB{
		"fg":        &th.FG,
		"fg_dim":    &th.FGDim,
		"accent":    &th.Accent,
		"user":      &th.User,
		"user_bg":   &th.UserBG,
		"assistant": &th.Assistant,
		"border":    &th.Border,
		"success":   &th.Success,
		"warn":      &th.Warn,
		"error":     &th.Error,
		"code_bg":   &th.CodeBG,
	}
	for k, v := range f.Colors {
		p, ok := targets[k]
		if !ok {
			warns = append(warns, fmt.Sprintf("colors.%s no se usa", k))
			continue
		}
		c, err := ParseHex(v)
		if err != nil {
			warns = append(warns, fmt.Sprintf("colors.%s: %v", k, err))
			continue
		}
		*p = c
	}

	th.Syntax = map[string]RGB{}
	for k, v := range f.Syntax {
		c, err := ParseHex(v)
		if err != nil {
			warns = append(warns, fmt.Sprintf("syntax.%s: %v", k, err))
			continue
		}
		th.Syntax[k] = c
	}

	return th, warns
}

// hasKey mira si una clave de primer nivel aparece en el TOML, para distinguir
// "dark = false" de "dark no declarado".
func hasKey(b []byte, key string) bool {
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, key) {
			rest := strings.TrimSpace(strings.TrimPrefix(line, key))
			if strings.HasPrefix(rest, "=") {
				return true
			}
		}
	}
	return false
}

// Gradient reparte el degradado del tema sobre n posiciones.
func (t Theme) Gradient(n int) []RGB { return Ramp(t.Stops, n, t.Space) }

// GradientAt devuelve el color del degradado en la posición t (0 a 1).
func (t Theme) GradientAt(v float64) RGB {
	r := Ramp(t.Stops, 64, t.Space)
	if len(r) == 0 {
		return t.Accent
	}
	i := int(clamp01(v) * float64(len(r)-1))
	return r[i]
}

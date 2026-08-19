package theme

import (
	"image/color"
	"io"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
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

// Detect resolves the colour capability from the environment, with an override
// from [ui] color ("auto" | "never" | "always" | "16" | "256" | "truecolor").
//
// The detection itself is delegated to charmbracelet/colorprofile, the same
// library Bubble Tea uses to decide what it may write to the terminal. That is
// the whole point: when this function and the renderer disagree, the result is
// the bug reported on Windows — the hand-rolled version below returned "no
// colour" whenever TERM was empty, which is the normal state of a PowerShell or
// cmd.exe console (they do not set TERM at all), so every style was built flat
// and the banner came out white while Termux showed the gradient. colorprofile
// knows to ask the Windows console API instead, and it also handles NO_COLOR,
// CLICOLOR, CLICOLOR_FORCE, WT_SESSION, tmux/screen and terminfo, none of which
// we want to re-implement or keep in sync by hand.
func Detect(override string) Capability {
	return DetectEnv(override, os.Environ())
}

// DetectEnv is Detect against an explicit environment. It exists so the rules
// can be tested without mutating the process' own environment, and so callers
// that already carry an environment around (tests, `doctor`) do not have to
// smuggle it through os.Setenv.
func DetectEnv(override string, env []string) Capability {
	if cap, ok := overrideCapability(override); ok {
		return cap
	}
	if v, ok := lookupEnv(env, "NO_COLOR"); ok && v != "" && v != "0" {
		// no-color.org is a contract between terminal programs; honouring it
		// explicitly also keeps it winning over the console hints below.
		return CapNone
	}
	cap := capabilityOf(colorprofile.Env(env))
	if hint := consoleHint(env); hint > cap {
		cap = hint
	}
	return cap
}

// consoleHint is the capability a Windows console advertises through the
// environment. It only speaks up when TERM is absent, which is the signature of
// a console host: every Unix terminal sets TERM, so if it is missing we are
// either on Windows or talking to something that made no promises at all.
//
// colorprofile already asks the Windows console API when it runs on Windows, and
// that is the primary path; this is the belt to its braces. It costs four
// lookups, it is the same list every terminal library carries, and it means the
// answer no longer depends on which OS the binary happens to be built for —
// which is what makes the behaviour testable at all.
func consoleHint(env []string) Capability {
	if term, ok := lookupEnv(env, "TERM"); ok && term != "" {
		return CapNone
	}
	if v, _ := lookupEnv(env, "WT_SESSION"); v != "" {
		return CapTruecolor // Windows Terminal
	}
	if v, _ := lookupEnv(env, "ConEmuANSI"); strings.EqualFold(v, "on") {
		return CapTruecolor
	}
	switch strings.ToLower(first(env, "TERM_PROGRAM")) {
	case "vscode", "windows_terminal", "hyper":
		return CapTruecolor
	}
	if v, _ := lookupEnv(env, "ANSICON"); v != "" {
		return Cap256
	}
	return CapNone
}

// lookupEnv reads a KEY=VALUE slice the way os.LookupEnv reads the process
// environment. The last assignment wins, matching what the OS does when a
// variable is exported twice.
func lookupEnv(env []string, key string) (string, bool) {
	value, found := "", false
	for _, kv := range env {
		name, v, ok := strings.Cut(kv, "=")
		if ok && name == key {
			value, found = v, true
		}
	}
	return value, found
}

func first(env []string, key string) string {
	v, _ := lookupEnv(env, key)
	return v
}

// DetectWriter is Detect plus the one question the environment cannot answer:
// whether out is a terminal at all. A pipe or a redirected file gets no colour
// even if TERM promises 24 bits, because the escape sequences would end up in
// the file. An explicit override still wins — `color = "always"` is how you ask
// for colour through a pager.
func DetectWriter(override string, out io.Writer) Capability {
	if cap, ok := overrideCapability(override); ok {
		return cap
	}
	if !isTerminalWriter(out) {
		return CapNone
	}
	env := os.Environ()
	if v, ok := lookupEnv(env, "NO_COLOR"); ok && v != "" && v != "0" {
		return CapNone
	}
	cap := capabilityOf(colorprofile.Detect(out, env))
	if hint := consoleHint(env); hint > cap {
		cap = hint
	}
	return cap
}

// isTerminalWriter answers whether out is a real terminal. It is deliberately
// duck-typed on Fd() instead of taking an *os.File: tests hand this an
// in-memory writer, and that must come out as "not a terminal" rather than a
// type assertion panic.
func isTerminalWriter(out io.Writer) bool {
	f, ok := out.(interface{ Fd() uintptr })
	return ok && term.IsTerminal(f.Fd())
}

// overrideCapability reads [ui] color. The second result is false for "auto"
// and for anything unrecognised, meaning "no override, go and detect".
func overrideCapability(override string) (Capability, bool) {
	switch strings.ToLower(strings.TrimSpace(override)) {
	case "never", "none", "off", "0":
		return CapNone, true
	case "16", "ansi":
		return Cap16, true
	case "256", "ansi256":
		return Cap256, true
	case "always", "truecolor", "24bit":
		return CapTruecolor, true
	}
	return CapNone, false
}

// capabilityOf maps a colorprofile profile onto our own three levels. NoTTY,
// ASCII and Unknown all collapse into CapNone: from the theme's point of view
// "there is no colour" is one state, and NewStyles turns it into styles that
// emit no escape sequences at all.
func capabilityOf(p colorprofile.Profile) Capability {
	switch p {
	case colorprofile.TrueColor:
		return CapTruecolor
	case colorprofile.ANSI256:
		return Cap256
	case colorprofile.ANSI:
		return Cap16
	default:
		return CapNone
	}
}

// Styles son los estilos de lipgloss derivados del tema. Se construyen una vez
// y se guardan en el modelo: crear estilos en cada render es el desperdicio
// clásico de los TUIs.
type Styles struct {
	Theme Theme
	Cap   Capability

	// Glyphs is which characters these styles may draw. It lives next to Cap
	// because both answer the same kind of question about the terminal, and
	// because the box border is the one style whose *shape* depends on it.
	Glyphs GlyphSet

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

	// UserBG paints Theme.UserBG as a background, never a foreground — it is
	// built and used separately from User (which stays a header-only
	// foreground accent, §17 2026-08-19 first half) because a user message's
	// *body* needs a background colour behind text that already carries its
	// own foreground/other styling (see PaintBackground's doc comment).
	UserBG lipgloss.Style
}

// NewStyles construye los estilos. Con CapNone todos quedan sin color, así el
// resto del código no tiene que preguntar nunca si hay color o no.
//
// The glyph set is a parameter for the same reason: the border characters of
// the input box depend on it, so no caller has to ask twice whether it may
// draw "╭".
func NewStyles(t Theme, cap Capability, glyphs GlyphSet) Styles {
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
		Glyphs:    glyphs,
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
	if !plain {
		s.UserBG = lipgloss.NewStyle().Background(lipgloss.Color(t.UserBG.Hex()))
	} else {
		s.UserBG = lipgloss.NewStyle()
	}
	s.Box = lipgloss.NewStyle().
		Border(boxBorder(glyphs)).
		BorderForeground(borderColor(t, plain))
	return s
}

// boxBorder picks the border characters for the input box.
//
// Note what is *not* here: lipgloss.RoundedBorder. Its corners are U+256D..
// U+2570, which are not in WGL4 and which Consolas does not ship — so the one
// box the user stares at while typing was framed in four characters a stock
// PowerShell cannot draw. NormalBorder is the same shape in ┌─┐│└┘, all of
// which are WGL4 and all of which are also in cp437, so they survive even a
// mis-guessed code page. ASCIIBorder is +-| for the rest.
func boxBorder(glyphs GlyphSet) lipgloss.Border {
	if glyphs.ASCII() {
		return lipgloss.ASCIIBorder()
	}
	return lipgloss.NormalBorder()
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

// DimRender aplica el estilo atenuado (FGDim) a una cadena. Existe como
// método de conveniencia para que otros paquetes (tui) no tengan que conocer
// el campo Dim directamente, solo la interfaz mínima que necesitan.
func (s Styles) DimRender(text string) string { return s.Dim.Render(text) }

// ansiFullReset is the plain SGR reset every lipgloss-rendered run ends in
// (charmbracelet/x/ansi's own Style.Styled appends exactly this after its
// own content, see ansi.ResetStyle) — the byte sequence PaintBackground has
// to find and patch. It is its own alias rather than every call site below
// spelling out ansi.ResetStyle directly, so a future bump of that dependency
// that renamed or restructured the constant would fail this file's own
// build instead of silently drifting out of sync with lipgloss's real
// output.
const ansiFullReset = ansi.ResetStyle

// PaintBackground wraps an already-rendered, possibly multi-line block of
// text in the UserBG background colour, without losing that background at
// any point *inside* the block where the text carries its own embedded
// style (a header's User foreground, a Chroma token, a Glamour span, ...).
//
// The naive approach — bg.Render(block) — does not work: lipgloss.Style's
// own Render only wraps the *whole* string once, but any already-styled run
// inside that string ends in its own ansiFullReset, and a bare "\x1b[m" also
// zeroes the *background* the outer Render just set. Between any two
// pre-styled runs on the same line the background would visibly "leak" back
// to the terminal's own default. Style.Width(n) forces line padding, which
// fixes that leak but pads every line — including the block's last one —
// with trailing spaces even when nothing asked for a padded box, an
// unwanted side effect for what should read as "text on a coloured strip."
//
// The fix is to ask the style itself for the exact escape codes it would
// wrap around any text (by rendering a single NUL byte as a probe and
// splitting on it), then apply those codes by hand: prefix + suffix around
// each non-empty line, and — critically — the prefix again immediately
// after every embedded ansiFullReset inside that line, so the background is
// re-established the instant an inner style's own reset would have cleared
// it. This mirrors fg()'s own "CapNone renders no escape at all" contract
// for free: under CapNone, s.UserBG is lipgloss.NewStyle() with no
// Background set, so the probe's prefix/suffix both come back empty and the
// whole function is a byte-for-byte no-op — no separate "if plain" branch
// needed here, same as every other Styles method.
func (s Styles) PaintBackground(block string) string {
	probe := s.UserBG.Render("\x00")
	i := strings.IndexByte(probe, 0)
	if i < 0 {
		return block
	}
	prefix, suffix := probe[:i], probe[i+1:]
	if prefix == "" && suffix == "" {
		return block
	}
	lines := strings.Split(block, "\n")
	for idx, ln := range lines {
		if ln == "" {
			continue
		}
		patched := strings.ReplaceAll(ln, ansiFullReset, ansiFullReset+prefix)
		lines[idx] = prefix + patched + suffix
	}
	return strings.Join(lines, "\n")
}

// RenderBox draws content inside the theme's box, occupying exactly width
// terminal columns in total — the two vertical borders included.
//
// The border is *not* subtracted before handing the number to lipgloss, and
// that is the whole point of this comment: in lipgloss v2 Style.Width is the
// width of the finished block, frame and all, so Width(n) renders n columns
// with n-2 of them usable. Subtracting the two borders here as well made the
// box two columns narrower than the caller asked for, which is only cosmetic
// on its own — but it also left the content two columns *wider* than the space
// lipgloss had for it, so lipgloss re-wrapped every line of an already
// laid-out widget. In the input box that turned each full line of typing into
// a blank row plus an unindented row, dropped the continuation prompt from all
// but the last line, and left the terminal cursor pointing at the row the
// textarea had computed before the re-wrap — the reported "the cursor floats
// above the line I am typing on".
func (s Styles) RenderBox(content string, width int) string {
	const minWidth = 3 // two borders plus one usable column
	if width < minWidth {
		width = minWidth
	}
	return s.Box.Width(width).Render(content)
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

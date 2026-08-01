package theme_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichiTrader/ishakat/internal/theme"
)

func TestLoadEmbebido(t *testing.T) {
	th := theme.Load("ascua")
	if th.Name != "ascua" || th.Source != "embebido" {
		t.Fatalf("esperado el tema embebido, obtenido %+v", th.Source)
	}
	if len(th.Warnings) != 0 {
		t.Errorf("el tema embebido no debería avisar de nada: %v", th.Warnings)
	}
	if th.Accent.Hex() != "#ff8a3d" || th.User.Hex() != "#7fd1b9" {
		t.Errorf("colores mal parseados: accent=%s user=%s", th.Accent.Hex(), th.User.Hex())
	}
	if th.Space != theme.SpaceOklab || len(th.Stops) != 3 || !th.Scroll || !th.Dark {
		t.Errorf("sección [gradient] mal leída: %+v", th)
	}
	if th.Syntax["keyword"].Hex() != "#ff8a3d" {
		t.Errorf("syntax mal leído: %v", th.Syntax)
	}
}

func TestLoadDesdeDirectorioDeUsuario(t *testing.T) {
	dir := t.TempDir()
	body := `name = "hielo"
dark = false
[gradient]
space = "oklch"
stops = ["#123456", "#abcdef"]
[colors]
accent = "#00ff00"
`
	if err := os.WriteFile(filepath.Join(dir, "hielo.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	th := theme.Load("hielo", dir)
	if th.Name != "hielo" || th.Dark {
		t.Errorf("el archivo del usuario debe ganar: %+v", th)
	}
	if th.Space != theme.SpaceOklch || len(th.Stops) != 2 {
		t.Errorf("gradient del usuario mal leído: %+v", th)
	}
	if th.Accent.Hex() != "#00ff00" {
		t.Errorf("accent del usuario ignorado: %s", th.Accent.Hex())
	}
	// Los colores no declarados vienen del tema base, no en negro.
	if th.FG.Hex() != "#e8e6e3" {
		t.Errorf("los colores ausentes deben heredarse: fg=%s", th.FG.Hex())
	}
}

func TestTemaRotoNoImpideArrancar(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "roto.toml"),
		[]byte("esto no [es toml valido ==="), 0o600); err != nil {
		t.Fatal(err)
	}
	th := theme.Load("roto", dir)
	if th.Accent.Hex() == "" || len(th.Stops) == 0 {
		t.Fatal("un tema roto debe caer al base usable")
	}
	if len(th.Warnings) == 0 {
		t.Error("un tema roto debe avisar")
	}

	// Un color inválido tampoco puede tumbar nada.
	if err := os.WriteFile(filepath.Join(dir, "medio.toml"),
		[]byte("name=\"medio\"\n[colors]\naccent = \"no-es-un-color\"\ninventado = \"#fff\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	th2 := theme.Load("medio", dir)
	if th2.Accent.Hex() != "#ff8a3d" {
		t.Errorf("un color inválido debe caer al base: %s", th2.Accent.Hex())
	}
	if len(th2.Warnings) < 2 {
		t.Errorf("esperados avisos por color inválido y clave desconocida: %v", th2.Warnings)
	}
}

func TestTemaInexistenteCaeAlDefault(t *testing.T) {
	th := theme.Load("no-existe-este-tema")
	if th.Name != theme.Default {
		t.Errorf("esperado %q, obtenido %q", theme.Default, th.Name)
	}
	if len(th.Warnings) == 0 {
		t.Error("debe avisar que el tema pedido no existe")
	}
}

func TestAvailable(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "hielo.toml"), []byte("name=\"hielo\""), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "notas.txt"), []byte("x"), 0o600)

	got := theme.Available(dir)
	if len(got) != 2 {
		t.Fatalf("esperados 2 temas, %v", got)
	}
	if got[0] != theme.Default {
		t.Errorf("el default debe ir primero: %v", got)
	}
}

func TestParseHex(t *testing.T) {
	for _, in := range []string{"#ff8a3d", "ff8a3d", " #FF8A3D "} {
		c, err := theme.ParseHex(in)
		if err != nil {
			t.Fatalf("ParseHex(%q): %v", in, err)
		}
		if c.Hex() != "#ff8a3d" {
			t.Errorf("ParseHex(%q) = %s", in, c.Hex())
		}
	}
	if c, err := theme.ParseHex("#f0a"); err != nil || c.Hex() != "#ff00aa" {
		t.Errorf("forma corta mal expandida: %v %v", c, err)
	}
	for _, bad := range []string{"", "#12", "#12345", "zzzzzz"} {
		if _, err := theme.ParseHex(bad); err == nil {
			t.Errorf("ParseHex(%q) debería fallar", bad)
		}
	}
}

// Oklab no es un detalle estético: en RGB el punto medio entre ámbar y crema
// se apaga, y en Oklab conserva la luminosidad percibida.
func TestMixOklabMasLuminosoQueRGB(t *testing.T) {
	a, _ := theme.ParseHex("#ff6a3d")
	b, _ := theme.ParseHex("#ffe0a3")

	mid := theme.Mix(a, b, 0.5, theme.SpaceOklab)
	rgbMid := theme.RGB{
		R: uint8((int(a.R) + int(b.R)) / 2),
		G: uint8((int(a.G) + int(b.G)) / 2),
		B: uint8((int(a.B) + int(b.B)) / 2),
	}
	lum := func(c theme.RGB) int { return int(c.R)*299 + int(c.G)*587 + int(c.B)*114 }
	if lum(mid) <= lum(rgbMid) {
		t.Errorf("el punto medio en Oklab (%s) debería ser más luminoso que en RGB (%s)",
			mid.Hex(), rgbMid.Hex())
	}
}

func TestMixExtremosYEspacios(t *testing.T) {
	a, _ := theme.ParseHex("#ff0000")
	b, _ := theme.ParseHex("#0000ff")
	for _, sp := range []theme.Space{theme.SpaceOklab, theme.SpaceOklch, theme.SpaceHSL} {
		if got := theme.Mix(a, b, 0, sp); dist(got, a) > 3 {
			t.Errorf("%s: t=0 debe devolver el primero, dio %s", sp, got.Hex())
		}
		if got := theme.Mix(a, b, 1, sp); dist(got, b) > 3 {
			t.Errorf("%s: t=1 debe devolver el segundo, dio %s", sp, got.Hex())
		}
		// Fuera de rango se recorta, no explota.
		_ = theme.Mix(a, b, -5, sp)
		_ = theme.Mix(a, b, 7, sp)
	}
}

func dist(a, b theme.RGB) int {
	abs := func(n int) int {
		if n < 0 {
			return -n
		}
		return n
	}
	return abs(int(a.R)-int(b.R)) + abs(int(a.G)-int(b.G)) + abs(int(a.B)-int(b.B))
}

func TestRampMonotona(t *testing.T) {
	th := theme.Load("ascua")
	ramp := th.Gradient(40)
	if len(ramp) != 40 {
		t.Fatalf("esperados 40 pasos, %d", len(ramp))
	}
	if ramp[0].Hex() != th.Stops[0].Hex() {
		t.Errorf("el primer paso debe ser la primera parada: %s vs %s", ramp[0].Hex(), th.Stops[0].Hex())
	}
	if ramp[39].Hex() != th.Stops[len(th.Stops)-1].Hex() {
		t.Errorf("el último paso debe ser la última parada: %s", ramp[39].Hex())
	}
	// No debe haber saltos bruscos entre pasos consecutivos.
	for i := 1; i < len(ramp); i++ {
		if d := dist(ramp[i-1], ramp[i]); d > 40 {
			t.Errorf("salto brusco en el paso %d: %d", i, d)
		}
	}
	if got := theme.Ramp(nil, 5, theme.SpaceOklab); got != nil {
		t.Error("Ramp sin paradas debe devolver nil")
	}
	if got := th.Gradient(0); got != nil {
		t.Error("Ramp de 0 pasos debe devolver nil")
	}
}

func TestDetect(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("COLORTERM", "")
	t.Setenv("TERM", "xterm-256color")

	if got := theme.Detect("auto"); got != theme.Cap256 {
		t.Errorf("xterm-256color debería dar 256, dio %v", got)
	}
	t.Setenv("COLORTERM", "truecolor")
	if got := theme.Detect("auto"); got != theme.CapTruecolor {
		t.Errorf("COLORTERM=truecolor debería dar truecolor, dio %v", got)
	}
	if got := theme.Detect("never"); got != theme.CapNone {
		t.Errorf("override never debe ganar, dio %v", got)
	}
	if got := theme.Detect("16"); got != theme.Cap16 {
		t.Errorf("override 16, dio %v", got)
	}
	t.Setenv("NO_COLOR", "1")
	if got := theme.Detect("auto"); got != theme.CapNone {
		t.Errorf("NO_COLOR debe apagar el color, dio %v", got)
	}
	if got := theme.Detect("always"); got != theme.CapTruecolor {
		t.Error("un override explícito debe poder ignorar NO_COLOR")
	}
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "dumb")
	if got := theme.Detect("auto"); got != theme.CapNone {
		t.Errorf("TERM=dumb debe apagar el color, dio %v", got)
	}
}

func TestStylesSinColorNoEmiteEscapes(t *testing.T) {
	th := theme.Load("ascua")
	s := theme.NewStyles(th, theme.CapNone, theme.GlyphsUnicode)
	out := s.Accent.Render("hola") + s.Gradient("mundo", 0)
	if strings.Contains(out, "\x1b[") {
		t.Errorf("con CapNone no debe haber secuencias ANSI: %q", out)
	}
	if !strings.Contains(out, "holamundo") {
		t.Errorf("el texto debe seguir ahí: %q", out)
	}
}

func TestGradientPreservaTexto(t *testing.T) {
	th := theme.Load("ascua")
	s := theme.NewStyles(th, theme.CapTruecolor, theme.GlyphsUnicode)
	out := s.Gradient("ishakat", 0)
	plain := stripANSI(out)
	if plain != "ishakat" {
		t.Errorf("el degradado no debe alterar el texto: %q", plain)
	}
	if !strings.Contains(out, "\x1b[") {
		t.Error("con truecolor debería haber color")
	}
	// Multilínea: cada línea conserva su contenido.
	block := "aa\nbb"
	if got := stripANSI(s.GradientLines(block, 3)); got != block {
		t.Errorf("GradientLines alteró el bloque: %q", got)
	}
	if s.Gradient("", 0) != "" {
		t.Error("degradado de cadena vacía debe ser vacío")
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			i++
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

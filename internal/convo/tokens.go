package convo

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode"
)

// El estimador es heurístico y está bien que lo sea. Un tokenizador real pesa
// megabytes, hay que embarcar uno por familia de modelos, y no cambia ninguna
// decisión del producto: sirve para pintar la barra de contexto y para decidir
// si /compact hace falta, y para eso un ±15% es irrelevante. El número exacto
// llega del proveedor al terminar cada turno, y con él se corrige el ratio.

const (
	// charsPerTokenLatin es el ratio base para prosa en alfabeto latino.
	charsPerTokenLatin = 4.0
	// charsPerTokenCode es el ratio para bloques de código: la puntuación y la
	// indentación se tokenizan más fino.
	charsPerTokenCode = 3.0
	// charsPerTokenCJK: los ideogramas suelen costar cerca de un token cada uno.
	charsPerTokenCJK = 1.2
	// tokensPerMessage es el sobrecosto de estructura por mensaje (rol,
	// separadores del formato de chat).
	tokensPerMessage = 4
	// tokensPerImage es un piso razonable por imagen adjunta; los proveedores
	// varían mucho y el número real llega con el usage.
	tokensPerImage = 800
)

// EstimateText estima los tokens de una cadena distinguiendo prosa, código y
// escritura CJK.
func EstimateText(s string) int {
	if s == "" {
		return 0
	}
	var tokens float64
	for _, seg := range splitCode(s) {
		ratio := charsPerTokenLatin
		if seg.code {
			ratio = charsPerTokenCode
		}
		latin, cjk := countRunes(seg.text)
		tokens += float64(latin) / ratio
		tokens += float64(cjk) / charsPerTokenCJK
	}
	if tokens < 1 && strings.TrimSpace(s) != "" {
		return 1
	}
	return int(tokens + 0.5)
}

type segment struct {
	text string
	code bool
}

// splitCode parte el texto en tramos de prosa y tramos de código delimitados
// por vallas de tres backticks. Una valla sin cerrar deja el resto como código.
func splitCode(s string) []segment {
	const fence = "```"
	if !strings.Contains(s, fence) {
		return []segment{{text: s}}
	}
	var out []segment
	parts := strings.Split(s, fence)
	for i, p := range parts {
		if p == "" {
			continue
		}
		out = append(out, segment{text: p, code: i%2 == 1})
	}
	return out
}

func countRunes(s string) (latin, cjk int) {
	for _, r := range s {
		if isWide(r) {
			cjk++
			continue
		}
		latin++
	}
	return latin, cjk
}

// isWide detecta escrituras donde un carácter cuesta aproximadamente un token.
func isWide(r rune) bool {
	switch {
	case unicode.Is(unicode.Han, r),
		unicode.Is(unicode.Hiragana, r),
		unicode.Is(unicode.Katakana, r),
		unicode.Is(unicode.Hangul, r):
		return true
	}
	return false
}

// EstimateMessage estima los tokens de un mensaje, incluyendo el sobrecosto de
// estructura y los adjuntos.
func EstimateMessage(m Message) int {
	n := tokensPerMessage
	for _, blk := range m.Blocks {
		switch blk.Kind {
		case BlockImage:
			n += tokensPerImage
		case BlockToolCall, BlockToolResult:
			n += EstimateText(blk.Text) + EstimateText(string(blk.Args)) + EstimateText(blk.Name)
		default:
			n += EstimateText(blk.Text)
		}
	}
	return n
}

// Estimate estima los tokens de una lista de mensajes.
func Estimate(msgs []Message) int {
	n := 0
	for _, m := range msgs {
		n += EstimateMessage(m)
	}
	return n
}

// ContextTokens es lo que ocuparía la conversación en la próxima petición:
// solo los mensajes activos, es decir, sin los que un resumen ya reemplazó.
func (c *Conversation) ContextTokens() int {
	// El usage real del proveedor es la mejor fuente para el prompt: si el
	// último mensaje del asistente lo trae, ese número ya midió el historial
	// completo hasta ese punto.
	for i := len(c.Messages) - 1; i >= 0; i-- {
		m := c.Messages[i]
		if m.Role != RoleAssistant || m.Usage == nil || m.Usage.In == 0 {
			continue
		}
		known := m.Usage.In + m.Usage.Out + m.Usage.CacheRead
		// Y a eso se le suma lo que llegó después de ese turno.
		for _, later := range c.Messages[i+1:] {
			known += EstimateMessage(later)
		}
		return known
	}
	return Estimate(c.Active())
}

// ── Corrección del ratio con el usage real ────────────────────────────────

// Ratios guarda el ratio observado de caracteres por token, por modelo, para
// que la estimación mejore con el uso. Es un archivo de caché: si se pierde,
// se vuelve al ratio base y no pasa nada.
type Ratios struct {
	mu   sync.Mutex
	path string
	data map[string]ratio
}

type ratio struct {
	Chars   int     `json:"chars"`
	Tokens  int     `json:"tokens"`
	Current float64 `json:"current"`
}

// LoadRatios lee el archivo de ratios. Nunca falla de forma dura: si no se
// puede leer, devuelve un mapa vacío.
func LoadRatios(path string) *Ratios {
	r := &Ratios{path: path, data: map[string]ratio{}}
	b, err := os.ReadFile(path)
	if err != nil {
		return r
	}
	var m map[string]ratio
	if err := json.Unmarshal(b, &m); err == nil && m != nil {
		r.data = m
	}
	return r
}

// Observe registra una medición real: los caracteres que mandamos y los tokens
// que el proveedor dijo que costaron.
func (r *Ratios) Observe(model string, chars, tokens int) {
	if r == nil || model == "" || chars <= 0 || tokens <= 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.data[model]
	d.Chars += chars
	d.Tokens += tokens
	d.Current = float64(d.Chars) / float64(d.Tokens)
	r.data[model] = d
}

// For devuelve el ratio caracteres/token conocido para un modelo, o el base.
func (r *Ratios) For(model string) float64 {
	if r == nil {
		return charsPerTokenLatin
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if d, ok := r.data[model]; ok && d.Current > 0.5 && d.Current < 12 {
		return d.Current
	}
	return charsPerTokenLatin
}

// Correct ajusta una estimación con el ratio aprendido del modelo.
func (r *Ratios) Correct(model string, estimate int) int {
	f := r.For(model)
	if f <= 0 {
		return estimate
	}
	return int(float64(estimate)*charsPerTokenLatin/f + 0.5)
}

// Save escribe el archivo de ratios de forma atómica. Un error al guardar no
// es fatal para la aplicación: es caché.
func (r *Ratios) Save() error {
	if r == nil || r.path == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(r.path), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(r.data)
	if err != nil {
		return err
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, r.path)
}

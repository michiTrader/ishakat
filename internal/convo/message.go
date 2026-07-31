// Package convo implementa el contrato 1 del PLAN (§4): el modelo de
// conversación agnóstico. El historial nunca se guarda en el formato JSON de
// un proveedor; se guarda en esta estructura propia y se serializa al
// dialecto correspondiente en el momento exacto de la petición
// (internal/provider.FromConvo).
//
// Tipos puros, sin dependencias externas salvo encoding/json y time.
package convo

import (
	"encoding/json"
	"fmt"
	"time"
)

// Role es quién habla en el turno.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// BlockKind distingue los tipos de contenido que puede llevar un mensaje.
type BlockKind int

const (
	BlockText BlockKind = iota
	BlockImage
	BlockToolCall
	BlockToolResult
	BlockReasoning
	BlockSummary // producido por /compact
)

var blockKindNames = map[BlockKind]string{
	BlockText:       "text",
	BlockImage:      "image",
	BlockToolCall:   "tool_call",
	BlockToolResult: "tool_result",
	BlockReasoning:  "reasoning",
	BlockSummary:    "summary",
}

var blockKindValues = map[string]BlockKind{
	"text":        BlockText,
	"image":       BlockImage,
	"tool_call":   BlockToolCall,
	"tool_result": BlockToolResult,
	"reasoning":   BlockReasoning,
	"summary":     BlockSummary,
}

func (k BlockKind) String() string {
	if s, ok := blockKindNames[k]; ok {
		return s
	}
	return "desconocido"
}

// MarshalJSON serializa el tipo como su nombre legible ("text", "image", …)
// para que el JSONL en disco sea auditable a simple vista, no una lista de
// enteros sin significado.
func (k BlockKind) MarshalJSON() ([]byte, error) {
	s, ok := blockKindNames[k]
	if !ok {
		return nil, fmt.Errorf("convo: BlockKind %d fuera de rango", int(k))
	}
	return json.Marshal(s)
}

// UnmarshalJSON acepta tanto el nombre como el entero, para tolerar archivos
// escritos por una versión futura o de depuración que use el número crudo.
func (k *BlockKind) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		v, ok := blockKindValues[s]
		if !ok {
			return fmt.Errorf("convo: BlockKind %q desconocido", s)
		}
		*k = v
		return nil
	}
	var n int
	if err := json.Unmarshal(b, &n); err != nil {
		return fmt.Errorf("convo: BlockKind ilegible: %s", b)
	}
	if _, ok := blockKindNames[BlockKind(n)]; !ok {
		return fmt.Errorf("convo: BlockKind %d fuera de rango", n)
	}
	*k = BlockKind(n)
	return nil
}

// Block es la unidad de contenido dentro de un mensaje. Un mensaje puede
// llevar varios: texto intercalado con razonamiento, o una imagen junto a su
// descripción.
type Block struct {
	Kind BlockKind `json:"kind"`
	Text string    `json:"text,omitempty"`

	// Data y Mime son para adjuntos: imágenes y en el futuro otros binarios.
	Data []byte `json:"data,omitempty"`
	Mime string `json:"mime,omitempty"`
	Name string `json:"name,omitempty"` // nombre de archivo o de herramienta

	// Args es la carga de una llamada a herramienta, cruda porque su forma la
	// define la herramienta, no convo.
	Args json.RawMessage `json:"args,omitempty"`

	// Replaces son los índices de convo.Conversation.Messages que un
	// BlockSummary reemplaza. Solo tiene sentido en bloques de ese tipo.
	Replaces []int `json:"replaces,omitempty"`
}

// TextBlock construye un bloque de texto plano.
func TextBlock(s string) Block { return Block{Kind: BlockText, Text: s} }

// ReasoningBlock construye un bloque de razonamiento (cadena de pensamiento).
func ReasoningBlock(s string) Block { return Block{Kind: BlockReasoning, Text: s} }

// ImageBlock construye un bloque de imagen adjunta.
func ImageBlock(mime string, data []byte, name string) Block {
	return Block{Kind: BlockImage, Mime: mime, Data: data, Name: name}
}

// SummaryBlock construye el resultado de /compact: el texto del resumen y qué
// mensajes reemplaza.
func SummaryBlock(summary string, replaces []int) Block {
	return Block{Kind: BlockSummary, Text: summary, Replaces: replaces}
}

// Usage acumula el consumo de tokens de un turno, tal como lo reporta el
// proveedor al terminar.
type Usage struct {
	In         int `json:"in,omitempty"`
	Out        int `json:"out,omitempty"`
	CacheRead  int `json:"cache_read,omitempty"`
	CacheWrite int `json:"cache_write,omitempty"`
	Reasoning  int `json:"reasoning,omitempty"`
}

// Total suma todos los componentes del uso, para mostrar en el footer y en
// /stats.
func (u *Usage) Total() int {
	if u == nil {
		return 0
	}
	return u.In + u.Out + u.CacheRead + u.CacheWrite + u.Reasoning
}

// Add acumula el uso de otro turno en este, en el lugar. Sirve para /stats
// sobre la sesión completa.
func (u *Usage) Add(o *Usage) {
	if u == nil || o == nil {
		return
	}
	u.In += o.In
	u.Out += o.Out
	u.CacheRead += o.CacheRead
	u.CacheWrite += o.CacheWrite
	u.Reasoning += o.Reasoning
}

// Message es un turno del historial: quién habla, con qué bloques, generado
// por qué modelo y cuándo.
type Message struct {
	ID      string  `json:"id,omitempty"`
	Role    Role    `json:"role"`
	Blocks  []Block `json:"blocks,omitempty"`
	Model   string  `json:"model,omitempty"` // Ref del modelo que generó este mensaje
	Usage   *Usage  `json:"usage,omitempty"`
	Aborted bool    `json:"aborted,omitempty"` // true si el usuario canceló a mitad de streaming

	Ts time.Time `json:"ts"`
}

// NewMessage arma un mensaje con los bloques dados.
func NewMessage(role Role, blocks ...Block) Message {
	return Message{Role: role, Blocks: blocks, Ts: time.Now()}
}

// User es el atajo común: un mensaje de usuario con un único bloque de texto.
func User(text string) Message { return NewMessage(RoleUser, TextBlock(text)) }

// System es el atajo para el mensaje de sistema.
func System(text string) Message { return NewMessage(RoleSystem, TextBlock(text)) }

// Assistant es el atajo para una respuesta ya completa del modelo.
func Assistant(text, model string) Message {
	m := NewMessage(RoleAssistant, TextBlock(text))
	m.Model = model
	return m
}

// Text concatena todos los bloques de texto visibles del mensaje (texto y
// resumen), separados por un salto de línea en blanco. No incluye
// razonamiento: para eso está Reasoning().
func (m Message) Text() string {
	var out string
	for _, b := range m.Blocks {
		if b.Kind != BlockText && b.Kind != BlockSummary {
			continue
		}
		if b.Text == "" {
			continue
		}
		if out != "" {
			out += "\n\n"
		}
		out += b.Text
	}
	return out
}

// Reasoning concatena los bloques de razonamiento del mensaje.
func (m Message) Reasoning() string {
	var out string
	for _, b := range m.Blocks {
		if b.Kind != BlockReasoning || b.Text == "" {
			continue
		}
		if out != "" {
			out += "\n\n"
		}
		out += b.Text
	}
	return out
}

// Has indica si el mensaje tiene al menos un bloque del tipo dado.
func (m Message) Has(kind BlockKind) bool {
	for _, b := range m.Blocks {
		if b.Kind == kind {
			return true
		}
	}
	return false
}

// AppendText agrega texto al mensaje durante el streaming. Si el último
// bloque ya es de texto, coalesce en el mismo bloque en vez de abrir uno
// nuevo por cada delta que llega del proveedor: sin esto, un mensaje típico
// terminaría con cientos de bloques de un carácter.
func (m *Message) AppendText(delta string) {
	m.appendCoalesced(BlockText, delta)
}

// AppendReasoning agrega razonamiento al mensaje durante el streaming, con la
// misma lógica de coalescing que AppendText pero en su propio canal.
func (m *Message) AppendReasoning(delta string) {
	m.appendCoalesced(BlockReasoning, delta)
}

func (m *Message) appendCoalesced(kind BlockKind, delta string) {
	if delta == "" {
		return
	}
	if n := len(m.Blocks); n > 0 && m.Blocks[n-1].Kind == kind {
		m.Blocks[n-1].Text += delta
		return
	}
	m.Blocks = append(m.Blocks, Block{Kind: kind, Text: delta})
}

// Conversation es el historial vivo de una sesión más su cabecera.
type Conversation struct {
	Header
	Messages []Message `json:"messages,omitempty"`

	// Corrupt cuenta las líneas que el almacén no pudo interpretar al cargar.
	// Se reporta en /debug en vez de abortar la carga.
	Corrupt int `json:"-"`
}

// Add anexa un mensaje en memoria y devuelve su índice.
func (c *Conversation) Add(m Message) int {
	c.Messages = append(c.Messages, m)
	return len(c.Messages) - 1
}

// Active devuelve los mensajes que siguen vigentes: los que ningún
// BlockSummary posterior haya reemplazado. Es lo que realmente viaja en la
// próxima petición al proveedor.
func (c *Conversation) Active() []Message {
	replaced := map[int]bool{}
	for _, m := range c.Messages {
		for _, b := range m.Blocks {
			if b.Kind == BlockSummary {
				for _, idx := range b.Replaces {
					replaced[idx] = true
				}
			}
		}
	}
	out := make([]Message, 0, len(c.Messages))
	for i, m := range c.Messages {
		if replaced[i] {
			continue
		}
		out = append(out, m)
	}
	return out
}

// Usage suma el consumo de todos los mensajes de la conversación, para
// /stats.
func (c *Conversation) Usage() *Usage {
	var total Usage
	for _, m := range c.Messages {
		total.Add(m.Usage)
	}
	return &total
}

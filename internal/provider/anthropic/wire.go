// Package anthropic implementa el dialecto nativo de Anthropic (Messages
// API, `POST /v1/messages`), pospuesto a la Fase 4 por §11/§2 (Fase 2 lo
// declaró explícitamente fuera de alcance: "kind = openai contra OmniRoute ya
// te da Claude y Gemini, escribirlos ahora es trabajo sin funcionalidad nueva
// visible"). Antes de este paquete, `kind = "anthropic"` en config.toml no
// tenía ningún adaptador registrado y `validKind` lo aceptaba solo por error
// de diseño (ver internal/config/validate.go, corregido en el mismo cambio
// que introdujo este paquete) — el preset "anthropic" de credentials.go sigue
// hablando a través del shim compatible con OpenAI por defecto; este
// adaptador es la opción nativa para quien la pida a mano con
// kind = "anthropic".
//
// Este archivo contiene solo las formas del cable: las estructuras JSON tal
// como viajan por la Messages API y su streaming SSE. Nada de lógica.
package anthropic

import "encoding/json"

// wireContent es un bloque de contenido, en cualquiera de las dos
// direcciones. Anthropic no tiene una sola forma "mensaje": un turno es un
// array de bloques tipados (`type`), y un mismo turno puede mezclar texto,
// una llamada a herramienta y (cuando el pensamiento extendido está
// activado, que este adaptador no pide todavía) un bloque de razonamiento
// firmado.
//
// Se modela con campos planos y `omitempty`, igual que wireDelta en el
// dialecto OpenAI, en vez de una interfaz o un tipo por variante: barato de
// serializar y los tests pueden mirar el mapa resultante directamente.
type wireContent struct {
	Type string `json:"type"` // text | tool_use | tool_result | thinking

	// type = "text"
	Text string `json:"text,omitempty"`

	// type = "tool_use" (saliente: el turno assistant que reenvía una
	// llamada que ya hizo). Input es un objeto JSON, nunca una cadena — a
	// diferencia del dialecto OpenAI, cuyo `arguments` es texto.
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// type = "tool_result" (saliente: el turno user que lleva la salida de
	// una herramienta). Content aquí es la forma más simple documentada
	// (una cadena); Anthropic también acepta una lista de bloques anidados,
	// que este adaptador no necesita producir.
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
}

// wireMessage es un turno del array `messages`. Role es "user" o
// "assistant" — Anthropic no tiene rol "system" ni "tool": el system prompt
// va en el campo `system` de nivel superior (wireRequest.System) y el
// resultado de una herramienta es contenido de un turno "user" (BlockKind
// convo.BlockToolResult se traduce a un wireContent tipo tool_result, no a
// un mensaje con rol propio).
type wireMessage struct {
	Role    string        `json:"role"`
	Content []wireContent `json:"content"`
}

// wireToolDef es una entrada del array `tools` del cuerpo del request. A
// diferencia del dialecto OpenAI (que envuelve function/parameters en un
// wireToolFunc anidado), Anthropic pone name/description/input_schema al
// mismo nivel que el propio elemento del array.
type wireToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// wireRequest es el cuerpo de POST /v1/messages.
//
// MaxTokens no lleva `omitempty`: la Messages API lo exige (400 sin él), a
// diferencia de chat/completions donde es opcional. buildBody siempre le
// pone un valor, nunca lo deja en cero.
type wireRequest struct {
	Model       string        `json:"model"`
	Messages    []wireMessage `json:"messages"`
	System      string        `json:"system,omitempty"`
	MaxTokens   int           `json:"max_tokens"`
	Stream      bool          `json:"stream,omitempty"`
	Temperature *float64      `json:"temperature,omitempty"`
	Tools       []wireToolDef `json:"tools,omitempty"`
}

// wireUsage es el bloque de consumo, en cualquiera de sus dos apariciones:
// dentro de message_start.message.usage (fija el input_tokens del turno) o
// dentro de message_delta.usage (el output_tokens *acumulado* hasta ese
// punto del streaming, no un incremento — ver mergeUsage en stream.go).
type wireUsage struct {
	InputTokens              int `json:"input_tokens,omitempty"`
	OutputTokens             int `json:"output_tokens,omitempty"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
}

// wireError es el objeto `error` que tanto una respuesta HTTP no-200 como un
// evento SSE de tipo "error" llevan.
type wireError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// wireErrorEnvelope es el sobre de nivel superior de un error HTTP:
// {"type":"error","error":{...}}.
type wireErrorEnvelope struct {
	Type  string     `json:"type"`
	Error *wireError `json:"error,omitempty"`
}

// wireMessageResponse es el cuerpo completo cuando stream = false: un solo
// objeto Message, sin envoltura de streaming.
type wireMessageResponse struct {
	ID         string        `json:"id"`
	Type       string        `json:"type"`
	Role       string        `json:"role"`
	Content    []wireContent `json:"content"`
	Model      string        `json:"model,omitempty"`
	StopReason string        `json:"stop_reason,omitempty"`
	Usage      *wireUsage    `json:"usage,omitempty"`
}

// ─────────────────────────────────────────────────────────────
// streaming (SSE)
// ─────────────────────────────────────────────────────────────

// wireMessageStart es el payload del evento message_start: un Message con
// content vacío, cuyo usage.input_tokens es el único dato de consumo fijo
// del turno completo.
type wireMessageStart struct {
	ID    string     `json:"id"`
	Role  string     `json:"role"`
	Model string     `json:"model,omitempty"`
	Usage *wireUsage `json:"usage,omitempty"`
}

// wireContentBlockStart anuncia el tipo de un bloque nuevo. Para tool_use,
// ID y Name llegan aquí; Input suele llegar vacío (`{}`) y se rellena por
// los content_block_delta de tipo input_json_delta que siguen.
type wireContentBlockStart struct {
	Type  string          `json:"type"` // text | tool_use | thinking
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

// wireStreamDelta es el delta de un content_block_delta (texto o fragmento
// de JSON de una tool_use) — no confundir con el delta de nivel superior de
// message_delta, que wireStreamEvent.Delta también usa pero para
// stop_reason, no para contenido.
type wireStreamDelta struct {
	Type string `json:"type"` // text_delta | input_json_delta | thinking_delta | signature_delta

	Text        string `json:"text,omitempty"`         // text_delta
	PartialJSON string `json:"partial_json,omitempty"` // input_json_delta
	Thinking    string `json:"thinking,omitempty"`     // thinking_delta (no pedido todavía, ver stream.go)
	Signature   string `json:"signature,omitempty"`    // signature_delta (idem)

	StopReason string `json:"stop_reason,omitempty"` // message_delta's own delta
}

// wireStreamEvent es un evento SSE ya decodificado de su campo `data`. Todos
// los tipos de evento comparten un solo struct, con `omitempty` en cada
// campo que no aplica a un tipo dado — el mismo enfoque que wireChunk usa en
// el dialecto OpenAI para chat.completion.chunk.
type wireStreamEvent struct {
	Type string `json:"type"`

	Message      *wireMessageStart      `json:"message,omitempty"`       // message_start
	Index        int                    `json:"index"`                   // content_block_*
	ContentBlock *wireContentBlockStart `json:"content_block,omitempty"` // content_block_start
	Delta        *wireStreamDelta       `json:"delta,omitempty"`         // content_block_delta / message_delta
	Usage        *wireUsage             `json:"usage,omitempty"`         // message_delta
	Error        *wireError             `json:"error,omitempty"`         // error
}

// ─────────────────────────────────────────────────────────────
// GET /v1/models
// ─────────────────────────────────────────────────────────────

// wireModel es una entrada de la lista de modelos. A diferencia del dialecto
// OpenAI, esta API no reporta ventana de contexto ni límite de salida: esos
// datos los aporta la fusión con models.dev que ya hace el catálogo
// (internal/catalog, Paso 6), no este adaptador.
type wireModel struct {
	ID          string `json:"id"`
	Type        string `json:"type,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
}

// wireModelList es la respuesta de GET /v1/models.
//
// HasMore/paginación: esta primera versión lee solo la primera página. El
// catálogo real de Anthropic tiene un puñado de modelos (decenas, no
// cientos), así que una sola página cubre el caso real; paginar es trabajo
// para el día que ese deje de ser cierto.
type wireModelList struct {
	Data    []wireModel `json:"data"`
	HasMore bool        `json:"has_more,omitempty"`
}

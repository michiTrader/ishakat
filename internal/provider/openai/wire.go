// Package openai implementa el dialecto de chat de OpenAI, que es el que
// hablan OmniRoute, OpenAI, Groq, Together, OpenRouter, DeepSeek, Ollama y
// LM Studio (§5.4). Un solo adaptador cubre ocho servicios; los de Anthropic
// y Google directos se posponen a la Fase 4 a propósito.
//
// Este archivo contiene solo las formas del cable: las estructuras JSON tal
// como viajan. Nada de lógica.
package openai

import "encoding/json"

// ChatMessage es un mensaje en el dialecto OpenAI. Content va como string
// porque este paso solo manda texto; cuando entren las imágenes de verdad
// (Fase 3) pasará a ser una lista de partes y este es el único tipo que
// cambia.
//
// ToolCalls y ToolCallID cubren el bucle de herramientas del Paso 14
// (§12bis #5): un mensaje assistant lleva ToolCalls cuando pidió invocar
// herramientas, y un mensaje role:"tool" lleva ToolCallID para que el
// servicio pueda correlacionar el resultado con la llamada que lo originó.
// Content puede ir vacío en un assistant que solo hizo tool_calls (algunos
// servicios exigen content:"" y otros lo rechazan; marshalTools se encarga
// de eso).
type ChatMessage struct {
	Role       string            `json:"role"`
	Content    string            `json:"content"`
	Name       string            `json:"name,omitempty"`
	ToolCalls  []wireToolCallOut `json:"tool_calls,omitempty"`
	ToolCallID string            `json:"tool_call_id,omitempty"`
}

// wireToolDef es una entrada del array `tools` del cuerpo del request
// (§12bis #5). Parameters es un JSON Schema; se manda crudo porque su forma
// la define la herramienta, no el dialecto.
type wireToolDef struct {
	Type     string       `json:"type"`
	Function wireToolFunc `json:"function"`
}

// wireToolFunc es la mitad function de una tool definition.
type wireToolFunc struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// wireUsage es el bloque de consumo. Los campos anidados son la forma en que
// OpenAI reporta razonamiento y caché; los gateways los copian tal cual
// cuando los tienen.
type wireUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`

	PromptTokensDetails *struct {
		CachedTokens int `json:"cached_tokens"`
		AudioTokens  int `json:"audio_tokens"`
	} `json:"prompt_tokens_details,omitempty"`

	CompletionTokensDetails *struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details,omitempty"`

	// Variantes de gateway: OmniRoute y OpenRouter han usado estos nombres.
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
}

// wireToolFuncCall is the function half of a tool call, shared by both
// directions because its shape is identical in each.
type wireToolFuncCall struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// wireExtraContent es el sobre por el que Google transporta datos propios
// dentro de una respuesta que por lo demás es del dialecto OpenAI. Va anidado
// bajo la clave del proveedor ("google") para que cada uno pueda añadir lo
// suyo sin colisionar.
//
// Se modela con tipos en vez de con json.RawMessage porque el campo se lee y
// se vuelve a escribir: un RawMessage haría que la firma viajara pegada a la
// forma exacta que mandó el servicio, y el punto de este arreglo es
// justamente separar el dato opaco (la firma) de la forma del cable.
type wireExtraContent struct {
	Google *wireGoogleExtra `json:"google,omitempty"`
}

// wireGoogleExtra lleva la firma de pensamiento de Gemini: una instantánea
// cifrada del razonamiento interno del modelo. Es opaca por diseño —no se
// puede leer, decodificar ni generar— y su único propósito es volver tal cual
// en la petición siguiente.
type wireGoogleExtra struct {
	ThoughtSignature string `json:"thought_signature,omitempty"`
}

// signature saca la firma sin obligar a quien pregunta a comprobar dos
// punteros. Devuelve "" cuando no hay ninguna, que es el caso de todos los
// proveedores salvo Gemini.
func (e *wireExtraContent) signature() string {
	if e == nil || e.Google == nil {
		return ""
	}
	return e.Google.ThoughtSignature
}

// googleExtra construye el sobre para una firma, o nil si no hay firma que
// mandar. Devolver nil importa: con `omitempty` es lo que hace que la clave
// `extra_content` no aparezca en el cuerpo cuando el proveedor no firma nada,
// y así el request que se manda a OpenAI o a OmniRoute no cambia ni un byte.
func googleExtra(sig string) *wireExtraContent {
	if sig == "" {
		return nil
	}
	return &wireExtraContent{Google: &wireGoogleExtra{ThoughtSignature: sig}}
}

// wireToolCall es una llamada a herramienta tal como *llega* en streaming,
// troceada. Index se reensambla por él (ver toolAccumulator.add) cuando el
// servicio lo manda.
//
// Index es un puntero, y eso es una decisión medida contra la API real: Gemini
// NO manda `index` en streaming, ni siquiera con llamadas en paralelo. Con un
// int a secas, ausente e índice 0 son indistinguibles, así que dos llamadas
// paralelas de Gemini caían las dos en la ranura 0 del acumulador y sus
// argumentos se concatenaban en un JSON inválido. Un puntero distingue "no
// vino" de "vino 0" y permite al acumulador recurrir al id cuando falta.
//
// Deliberadamente NO se reutiliza para construir un request: ver
// wireToolCallOut y el comentario que lo acompaña.
type wireToolCall struct {
	Index    *int              `json:"index,omitempty"`
	ID       string            `json:"id,omitempty"`
	Type     string            `json:"type,omitempty"`
	Function wireToolFuncCall  `json:"function"`
	Extra    *wireExtraContent `json:"extra_content,omitempty"`
}

// wireToolCallOut es una llamada a herramienta tal como se *envía* dentro de
// un mensaje assistant del historial. Es un tipo aparte de wireToolCall por
// una razón concreta y no por simetría: `index` pertenece al formato de
// respuesta en streaming, no al de request, y enviarlo es lo que hacía que
// Gemini rechazara con HTTP 400 cualquier turno posterior a una llamada a
// herramienta.
//
// Un solo struct para las dos direcciones parece economía y es una trampa:
// obliga a que el campo exista en la dirección donde sobra, y como `index`
// no llevaba `omitempty` (correctamente — en streaming un índice 0 es real y
// no debe desaparecer), el request salía siempre con `"index": 0`. OpenAI
// ignora los campos que no conoce y el error quedó invisible durante todo el
// desarrollo; la capa de compatibilidad de Gemini los valida y devuelve 400.
// El formato de entrada y el de salida son dos contratos distintos que
// coinciden en parte, así que se modelan por separado.
//
// Extra es la excepción que confirma la regla: es el único campo que sí viaja
// en las dos direcciones, porque Google exige que vuelva idéntico. Lleva
// `omitempty` y se rellena solo si la llamada trajo firma, así que para
// cualquier otro proveedor el cuerpo sale exactamente igual que antes.
type wireToolCallOut struct {
	ID       string            `json:"id,omitempty"`
	Type     string            `json:"type,omitempty"`
	Function wireToolFuncCall  `json:"function"`
	Extra    *wireExtraContent `json:"extra_content,omitempty"`
}

// wireDelta es el incremento de un chunk de streaming.
//
// Los tres nombres de razonamiento no son paranoia: DeepSeek manda
// reasoning_content, OpenRouter manda reasoning, y algunos gateways lo
// envuelven en un objeto con campo text. Aceptar los tres cuesta seis líneas
// y evita que el razonamiento desaparezca sin explicación.
type wireDelta struct {
	Role             string          `json:"role,omitempty"`
	Content          string          `json:"content,omitempty"`
	ReasoningContent string          `json:"reasoning_content,omitempty"`
	Reasoning        json.RawMessage `json:"reasoning,omitempty"`
	ToolCalls        []wireToolCall  `json:"tool_calls,omitempty"`
	Refusal          string          `json:"refusal,omitempty"`

	// Extra es el mismo sobre de wireToolCall, aquí en el nivel del mensaje:
	// cuando la respuesta NO trae llamadas a herramientas, Gemini pone la
	// firma en el propio delta. Se acepta para no perderla, aunque devolverla
	// en ese caso es recomendado y no obligatorio: Google solo valida de forma
	// estricta las firmas de las llamadas a herramientas.
	Extra *wireExtraContent `json:"extra_content,omitempty"`
}

// wireChoice es una alternativa de la respuesta. ishakat siempre pide una
// (n = 1) y usa la de índice 0.
type wireChoice struct {
	Index        int        `json:"index"`
	Delta        wireDelta  `json:"delta"`
	Message      *wireDelta `json:"message,omitempty"` // respuesta no-streaming
	FinishReason *string    `json:"finish_reason,omitempty"`
}

// wireError es el error que algunos servicios mandan dentro del cuerpo, con
// estado 200, en medio del stream. Ignorarlo produce el peor síntoma posible:
// un turno que termina en silencio sin texto y sin explicación.
type wireError struct {
	Message string          `json:"message"`
	Type    string          `json:"type,omitempty"`
	Code    json.RawMessage `json:"code,omitempty"`
	Param   string          `json:"param,omitempty"`
}

// wireChunk es un evento de streaming (`chat.completion.chunk`) y también
// sirve para la respuesta completa cuando stream = false.
type wireChunk struct {
	ID      string       `json:"id"`
	Object  string       `json:"object,omitempty"`
	Model   string       `json:"model,omitempty"`
	Choices []wireChoice `json:"choices"`
	Usage   *wireUsage   `json:"usage,omitempty"`
	Error   *wireError   `json:"error,omitempty"`
}

// wireModelList es la respuesta de GET /models. Los campos de contexto tienen
// cuatro nombres según el servicio; se leen todos y gana el primero no nulo.
type wireModelList struct {
	Object string            `json:"object,omitempty"`
	Data   []json.RawMessage `json:"data"`
	Error  *wireError        `json:"error,omitempty"`
}

type wireModel struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`

	ContextLength    int `json:"context_length,omitempty"`
	ContextWindow    int `json:"context_window,omitempty"`
	MaxContextTokens int `json:"max_context_tokens,omitempty"`
	MaxInputTokens   int `json:"max_input_tokens,omitempty"`

	MaxOutputTokens     int `json:"max_output_tokens,omitempty"`
	MaxCompletionTokens int `json:"max_completion_tokens,omitempty"`

	OwnedBy string   `json:"owned_by,omitempty"`
	Tags    []string `json:"tags,omitempty"`
}

// context devuelve la ventana de contexto según el primer campo que el
// servicio haya rellenado.
func (m wireModel) context() int {
	for _, v := range []int{m.ContextLength, m.ContextWindow, m.MaxContextTokens, m.MaxInputTokens} {
		if v > 0 {
			return v
		}
	}
	return 0
}

// output devuelve el límite de salida con el mismo criterio.
func (m wireModel) output() int {
	for _, v := range []int{m.MaxOutputTokens, m.MaxCompletionTokens} {
		if v > 0 {
			return v
		}
	}
	return 0
}

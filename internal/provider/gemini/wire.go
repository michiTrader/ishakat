// Package gemini implementa el dialecto nativo de Google Gemini
// (`POST /v1beta/{model}:generateContent` y `:streamGenerateContent`), el
// último ítem grande de la Fase 4 (§11: "el adaptador nativo de Gemini —
// falta empezarlo"). Antes de este paquete, `kind = "gemini"` en config.toml
// no tenía ningún adaptador registrado y `validKind` lo aceptaba solo por el
// mismo error de diseño que tenía "anthropic" antes de su propio adaptador
// (ver internal/config/validate.go, corregido en el mismo cambio que
// introduce este paquete) — el preset "gemini" de credentials.go sigue
// hablando a través del shim compatible con OpenAI por defecto; este
// adaptador es la opción nativa para quien la pida a mano con
// kind = "gemini".
//
// La ambigüedad que retrasó este paquete: la documentación pública de Google
// en 2026 promueve por defecto una "Interactions API" nueva
// (`client.interactions.create`, `POST /v1beta/interactions`,
// stateful/steps-based) en casi todos sus ejemplos de código, incluida
// text-generation.md y function-calling.md. Pero el documento canónico y sin
// ambigüedad — el Discovery Document que Google publica en
// https://generativelanguage.googleapis.com/$discovery/rest?version=v1beta,
// leído directamente para escribir este archivo — confirma que
// `models.generateContent` y `models.streamGenerateContent` siguen siendo
// métodos de primera clase, sin "deprecated": true, con el formato
// Content{role, parts[]} / Part{text|inlineData|functionCall|
// functionResponse|fileData|executableCode|codeExecutionResult|thought|
// thoughtSignature} de siempre — el mismo formato que ya asumen los
// comentarios de internal/provider/openai sobre las peculiaridades reales de
// Gemini (thought_signature, tool calls sin índice en streaming). Ese
// Discovery Document, no una guía de marketing con ejemplos actualizados
// para promover el nuevo SDK, es la fuente que este adaptador sigue.
//
// Este archivo contiene solo las formas del cable: las estructuras JSON tal
// como viajan por generateContent/streamGenerateContent y su streaming SSE.
// Nada de lógica.
package gemini

import "encoding/json"

// wireContent es un turno del array `contents`, en cualquiera de las dos
// direcciones. Role es "user" o "model" — Gemini no tiene rol "system" (va
// en systemInstruction, un Content aparte) ni rol "tool" propio (el
// resultado de una función es un Part functionResponse dentro de un
// Content role:"user", igual que documenta la guía de function-calling de
// Google: "the next conversation turn may contain a FunctionResponse with
// the Content.role \"user\"").
type wireContent struct {
	Role  string     `json:"role,omitempty"`
	Parts []wirePart `json:"parts"`
}

// wirePart es un bloque dentro de Content.parts. El Discovery Document lo
// modela como union type (como máximo un campo de datos presente); aquí se
// modela con campos planos y `omitempty`, igual que wireContent hace en el
// dialecto de Anthropic — barato de serializar y los tests pueden mirar el
// mapa resultante directamente.
type wirePart struct {
	// Text: bloque de texto plano.
	Text string `json:"text,omitempty"`

	// FunctionCall: una llamada a herramienta que el modelo predijo
	// (entrante) o que se reenvía como parte del historial (saliente).
	FunctionCall *wireFunctionCall `json:"functionCall,omitempty"`

	// FunctionResponse: el resultado de una función, en un Content
	// role:"user" (saliente) — Gemini no distingue functionResponse
	// entrante de saliente, a diferencia de Anthropic con tool_use/
	// tool_result, porque solo el cliente produce este campo.
	FunctionResponse *wireFunctionResponse `json:"functionResponse,omitempty"`

	// InlineData: bytes de un adjunto (imagen, audio, …) codificados en
	// base64 por el propio encoding/json vía []byte. No se manda todavía
	// (ver serialize.go); el campo existe para cuando la Fase 3 de envío
	// real de imágenes llegue a este dialecto también.
	InlineData *wireBlob `json:"inlineData,omitempty"`

	// Thought marca un Part de razonamiento (resumen del pensamiento
	// interno). Este adaptador nunca lo pide (generationConfig.thinkingConfig
	// no se manda, ver buildBody) así que en la práctica nunca debería
	// llegar, pero se modela para no fallar si algún día llega de todos
	// modos.
	Thought bool `json:"thought,omitempty"`

	// ThoughtSignature es la firma opaca que Gemini exige de vuelta,
	// byte a byte, en el próximo turno cuando acompañó una llamada a
	// herramienta (o, con pensamiento activado, un bloque de texto).
	// convo.Block.Signature ya documenta el porqué en detalle: sin ella
	// el turno siguiente falla con HTTP 400 "Function call is missing a
	// thought_signature in functionCall parts" — el mismo bug que
	// internal/provider/openai/wire.go's wireGoogleExtra ya resuelve para
	// el shim compatible con OpenAI. Aquí no hace falta un sobre
	// "extra_content.google" como en ese dialecto: en la API nativa el
	// campo va directo en el Part.
	ThoughtSignature string `json:"thoughtSignature,omitempty"`
}

// wireFunctionCall es la llamada a función tal como Gemini la representa,
// en cualquiera de las dos direcciones. Args es un objeto JSON, nunca una
// cadena — a diferencia del dialecto OpenAI, cuyo `arguments` es texto.
type wireFunctionCall struct {
	ID   string          `json:"id,omitempty"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

// wireFunctionResponse es el resultado de una función que se reenvía al
// modelo. Response es un objeto JSON libre — el Discovery Document dice
// "Callers can use any keys of their choice ... e.g. \"output\", \"result\"" —
// así que se envuelve siempre bajo una clave fija ("result") en vez de
// intentar adivinar la forma que el modelo espera.
type wireFunctionResponse struct {
	ID       string          `json:"id,omitempty"`
	Name     string          `json:"name"`
	Response json.RawMessage `json:"response"`
}

// wireBlob es el Part inlineData: bytes crudos más su tipo MIME.
type wireBlob struct {
	MimeType string `json:"mimeType"`
	Data     []byte `json:"data"` // encoding/json lo serializa en base64 él solo
}

// wireFunctionDeclaration es una entrada del array
// tools[].functionDeclarations del cuerpo del request. A diferencia de
// Anthropic (name/description/input_schema al mismo nivel del elemento) y de
// OpenAI (function/parameters anidado bajo un wrapper "function"), Gemini
// anida las declaraciones un nivel más: tools es un array de a lo sumo un
// objeto "Tool" con functionDeclarations, no un array de una declaración por
// elemento.
type wireFunctionDeclaration struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// wireTool es un elemento del array `tools` del cuerpo. Este adaptador solo
// llena FunctionDeclarations — codeExecution, googleSearch, urlContext, etc.
// (el resto del union type Tool que el Discovery Document lista) quedan
// fuera de alcance: son herramientas que el propio servicio ejecuta, no las
// que ishakat declara desde su capa de agente (§19).
type wireTool struct {
	FunctionDeclarations []wireFunctionDeclaration `json:"functionDeclarations,omitempty"`
}

// wireRequest es el cuerpo de POST /v1beta/{model}:generateContent (y su
// gemela :streamGenerateContent, mismo cuerpo). A diferencia de Anthropic,
// no hay `stream` en el cuerpo: Gemini elige streaming por el endpoint que
// se llama, no por un campo — el mismo punto que
// openarmature.org/capabilities/llm-provider ya documenta al describir el
// dialecto de Gemini ("Gemini selects streaming by a distinct endpoint, not
// a request-body flag").
type wireRequest struct {
	Contents          []wireContent  `json:"contents"`
	SystemInstruction *wireContent   `json:"systemInstruction,omitempty"`
	Tools             []wireTool     `json:"tools,omitempty"`
	GenerationConfig  *wireGenConfig `json:"generationConfig,omitempty"`
}

// wireGenConfig es generationConfig. Solo lleva los campos que este
// adaptador de verdad pone; el resto del objeto real (topK, seed,
// responseModalities, …) no tiene equivalente en provider.Request y se deja
// fuera hasta que algo lo necesite — la vía de escape de [provider.params]
// puede añadir cualquier otro campo sin recompilar (ver buildBody).
type wireGenConfig struct {
	Temperature     *float64 `json:"temperature,omitempty"`
	MaxOutputTokens *int     `json:"maxOutputTokens,omitempty"`
}

// wireUsageMetadata es el bloque de consumo de GenerateContentResponse.
type wireUsageMetadata struct {
	PromptTokenCount        int `json:"promptTokenCount,omitempty"`
	CachedContentTokenCount int `json:"cachedContentTokenCount,omitempty"`
	CandidatesTokenCount    int `json:"candidatesTokenCount,omitempty"`
	ThoughtsTokenCount      int `json:"thoughtsTokenCount,omitempty"`
	TotalTokenCount         int `json:"totalTokenCount,omitempty"`
}

// wireCandidate es una entrada de GenerateContentResponse.candidates.
type wireCandidate struct {
	Content      wireContent `json:"content"`
	FinishReason string      `json:"finishReason,omitempty"`
	Index        int         `json:"index"`
}

// wirePromptFeedback es GenerateContentResponse.promptFeedback: se rellena
// cuando el prompt en sí (no la respuesta) fue bloqueado, en cuyo caso
// candidates viene vacío — el propio Discovery Document lo dice: "Returns no
// candidates at all only if there was something wrong with the prompt
// (check promptFeedback)".
type wirePromptFeedback struct {
	BlockReason string `json:"blockReason,omitempty"`
}

// wireGenerateContentResponse es el cuerpo completo de una respuesta, tanto
// para generateContent (una sola vez) como para cada evento del streaming de
// streamGenerateContent (el mismo objeto se repite completo, no un delta con
// forma distinta — ver el comentario de pumpSSE en stream.go sobre qué
// significa esto para el streaming de texto).
type wireGenerateContentResponse struct {
	Candidates     []wireCandidate     `json:"candidates,omitempty"`
	PromptFeedback *wirePromptFeedback `json:"promptFeedback,omitempty"`
	UsageMetadata  *wireUsageMetadata  `json:"usageMetadata,omitempty"`
	ModelVersion   string              `json:"modelVersion,omitempty"`
}

// wireError es el objeto `error` del sobre estándar de error de Google APIs:
// {"error":{"code":...,"message":"...","status":"..."}}. A diferencia del
// shim compatible con OpenAI de Gemini (que openai/openai.go documenta como
// "wraps errors in a JSON array unlike a plain object"), la API nativa usa
// siempre un objeto — el mismo formato de error estándar de toda
// generativelanguage.googleapis.com, confirmado contra la guía pública de
// errores (ai.google.dev/gemini-api/docs/generate-content/api-errors) y
// contra decenas de reportes de error reales in situ (foros, GitHub issues)
// que citan literalmente esta forma.
type wireError struct {
	Code    int    `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	Status  string `json:"status,omitempty"`
}

// wireErrorEnvelope es el sobre de nivel superior de un error HTTP.
type wireErrorEnvelope struct {
	Error *wireError `json:"error,omitempty"`
}

// ─────────────────────────────────────────────────────────────
// GET /v1beta/models
// ─────────────────────────────────────────────────────────────

// wireModel es una entrada de la lista de modelos, con los campos del
// Discovery Document que interesan al catálogo (§4.3, Paso 6): ventana de
// contexto y límite de salida, algo que ni el dialecto de Anthropic ni el
// listado nativo de OpenAI reportan de forma tan directa.
type wireModel struct {
	Name                       string   `json:"name"`
	BaseModelID                string   `json:"baseModelId,omitempty"`
	DisplayName                string   `json:"displayName,omitempty"`
	Description                string   `json:"description,omitempty"`
	InputTokenLimit            int      `json:"inputTokenLimit,omitempty"`
	OutputTokenLimit           int      `json:"outputTokenLimit,omitempty"`
	SupportedGenerationMethods []string `json:"supportedGenerationMethods,omitempty"`
}

// wireModelList es la respuesta de GET /v1beta/models.
//
// NextPageToken/paginación: esta primera versión lee solo la primera
// página, igual que el dialecto de Anthropic razona para su propio
// GET /v1/models — pageSize por defecto es 50 y el catálogo real de Gemini
// no se acerca a eso en modelos con generateContent habilitado.
type wireModelList struct {
	Models        []wireModel `json:"models"`
	NextPageToken string      `json:"nextPageToken,omitempty"`
}

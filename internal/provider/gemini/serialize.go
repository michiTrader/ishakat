package gemini

import (
	"encoding/json"
	"strings"

	"github.com/MichiTrader/ishakat/internal/convo"
	"github.com/MichiTrader/ishakat/internal/provider"
)

// Aquí vive la traducción del modelo agnóstico de convo al dialecto de
// Gemini — el equivalente de anthropic/serialize.go y openai/serialize.go
// para este proveedor. Vive en su propio paquete por la misma razón que las
// otras dos: []wireContent es una forma de cable propia de este dialecto,
// no un concepto agnóstico.
//
// La diferencia estructural más grande frente a los otros dos dialectos:
// Gemini no tiene mensaje de rol "system" dentro de `contents`, igual que
// Anthropic — el prompt de sistema es un campo de nivel superior del
// request (wireRequest.SystemInstruction). Pero a diferencia de Anthropic
// (donde el resultado de una herramienta es un bloque "tool_result" dentro
// de un mensaje "user", con role:"tool" inexistente) Gemini tampoco tiene
// rol "tool": el resultado de una función es un Part functionResponse
// dentro de un Content con role:"user" — el mismo rol de un turno de
// usuario normal, no uno propio. FromConvo devuelve el system extraído por
// separado en vez de dejarlo como un Content más, precisamente porque en
// este dialecto tampoco hay forma correcta de serializarlo como tal.

// FromConvo traduce el historial al dialecto Gemini, extrayendo el prompt de
// sistema aparte y aplanando los bloques a texto donde corresponda,
// reportando qué se degradó.
//
// El camino de las herramientas depende de Caps.Tools (§4.6), igual que en
// los otros dos dialectos: un BlockToolCall con Caps.Tools se serializa
// como un Part functionCall dentro de un Content role:"model"; un
// BlockToolResult correspondiente se serializa como un Part
// functionResponse dentro de un Content role:"user" (nunca un Content con
// su propio rol "tool": ese rol no existe aquí, igual que en Anthropic).
// Cuando Caps.Tools es false, ambos se aplanan a texto descriptivo y se
// cuentan en Degradation.ToolsFlattened.
//
// El razonamiento se omite siempre, con la misma razón que los otros dos
// dialectos: reenviarlo confunde al modelo y se paga dos veces. Esto también
// evita el requisito de thoughtSignature de Gemini para bloques de
// pensamiento (que sí se exige de vuelta si el pensamiento se pidió) porque
// este adaptador nunca pide pensamiento (wireGenConfig no manda
// thinkingConfig) y por tanto nunca lo recibe como bloque de razonamiento
// propio — la firma que SÍ puede llegar y viajar de vuelta es la de un
// BlockToolCall (ver el caso de abajo), que es un requisito distinto y
// mucho más común: Gemini 3 la adjunta a la primera llamada a función de
// cada paso incluso sin pensamiento extendido activado explícitamente.
func FromConvo(msgs []convo.Message, caps provider.Caps) ([]wireContent, string, provider.Degradation) {
	var system strings.Builder
	out := make([]wireContent, 0, len(msgs))
	var deg provider.Degradation

	for _, m := range msgs {
		if m.Role == convo.RoleSystem {
			appendPara(&system, m.Text())
			continue
		}

		var b strings.Builder
		var parts []wirePart

		for _, blk := range m.Blocks {
			switch blk.Kind {
			case convo.BlockText:
				appendPara(&b, blk.Text)

			case convo.BlockSummary:
				appendPara(&b, "[resumen de la conversación anterior]\n"+blk.Text)

			case convo.BlockReasoning:
				// No se reenvía nunca: ver el comentario de FromConvo.
				deg.ReasoningDropped++

			case convo.BlockImage:
				deg.ImagesDropped++
				if caps.Images {
					// El envío real de imágenes llega en la Fase 3; hasta
					// entonces se declara la pérdida en vez de fingir,
					// igual que en los otros dos dialectos.
					appendPara(&b, "[imagen adjunta: "+nameOr(blk.Name, blk.Mime)+"]")
				} else {
					appendPara(&b, "[imagen adjunta no soportada por este modelo: "+nameOr(blk.Name, blk.Mime)+"]")
				}

			case convo.BlockToolCall:
				if caps.Tools {
					args := blk.Args
					if len(args) == 0 {
						args = json.RawMessage("{}")
					}
					parts = append(parts, wirePart{
						FunctionCall: &wireFunctionCall{
							ID:   blk.ToolCallID,
							Name: blk.Name,
							Args: args,
						},
						// La firma vuelve tal como llegó. Para Gemini 3
						// esto no es una mejora de calidad sino el
						// requisito que decide si la petición se acepta:
						// sin ella responde HTTP 400 "Function call is
						// missing a thought_signature in functionCall
						// parts" y el bucle de herramientas no pasa del
						// primer paso — el mismo bug documentado en
						// internal/provider/openai/wire.go para el shim
						// compatible con OpenAI, aquí sin necesidad de un
						// sobre "extra_content" porque el campo va directo
						// en el Part.
						ThoughtSignature: blk.Signature,
					})
				} else {
					deg.ToolsFlattened++
					appendPara(&b, "[llamada a herramienta "+blk.Name+"]\n"+string(blk.Args))
				}

			case convo.BlockToolResult:
				if caps.Tools {
					parts = append(parts, wirePart{
						FunctionResponse: &wireFunctionResponse{
							ID:       blk.ToolCallID,
							Name:     blk.Name,
							Response: functionResponsePayload(blk.Text, blk.IsError),
						},
					})
				} else {
					deg.ToolsFlattened++
					if blk.IsError {
						appendPara(&b, "[error de "+blk.Name+"]\n"+blk.Text)
					} else {
						appendPara(&b, "[resultado de "+blk.Name+"]\n"+blk.Text)
					}
				}
			}
		}

		text := b.String()
		if m.Aborted {
			appendPara(&b, "(respuesta interrumpida por el usuario)")
			text = b.String()
		}

		// El texto acumulado va primero, como Part de texto, seguido de
		// las llamadas/resultados de herramienta en el orden en que
		// aparecieron — el mismo orden que los otros dos dialectos
		// mantienen.
		var content []wirePart
		if strings.TrimSpace(text) != "" {
			content = append(content, wirePart{Text: text})
		}
		content = append(content, parts...)
		if len(content) == 0 {
			// Un Content sin ningún Part (por ejemplo: solo llevaba
			// razonamiento, que siempre se descarta) no se puede mandar:
			// Gemini exige al menos un Part por Content, igual que
			// Anthropic exige al menos un bloque por mensaje.
			continue
		}

		// role:"tool" no existe en este dialecto (tampoco en Anthropic):
		// el resultado de una herramienta —serializado o aplanado— viaja
		// en un Content role:"user". role:"assistant" tampoco existe
		// aquí: Gemini llama a su propio turno "model", no "assistant".
		role := "user"
		if m.Role == convo.RoleAssistant {
			role = "model"
		}

		out = append(out, wireContent{Role: role, Parts: content})
	}
	return out, strings.TrimSpace(system.String()), deg
}

// functionResponsePayload envuelve la salida de una herramienta bajo una
// clave fija ("result"), o ("error") si IsError, en vez de intentar
// adivinar la forma que el modelo espera. El Discovery Document deja la
// forma de FunctionResponse.response abierta a elección del llamador
// ("Callers can use any keys of their choice ... e.g. \"output\",
// \"result\""); una clave fija y estable es más predecible para el modelo
// que variar el nombre según el contenido.
func functionResponsePayload(text string, isError bool) json.RawMessage {
	key := "result"
	if isError {
		key = "error"
	}
	out, err := json.Marshal(map[string]string{key: text})
	if err != nil {
		// json.Marshal de un map[string]string con una clave constante
		// nunca falla en la práctica; el fallback existe para que la
		// función nunca devuelva un json.RawMessage inválido.
		return json.RawMessage(`{}`)
	}
	return out
}

// MarshalTools serializa una lista de provider.ToolDef al array `tools` del
// dialecto de Gemini. A diferencia de Anthropic y OpenAI (una entrada del
// array por herramienta), Gemini anida todas las declaraciones bajo un
// único elemento Tool: el array `tools` en la práctica real casi siempre
// tiene como mucho una entrada con function calling activado, y el
// Discovery Document modela functionDeclarations como el array repetido,
// no Tool.
//
// Devuelve nil para una lista vacía, por la misma razón que en los otros
// dos dialectos: permite distinguir "sin herramientas" (campo omitido del
// cuerpo) de "una herramienta sin parámetros" (el campo se manda con esa
// entrada).
func MarshalTools(defs []provider.ToolDef) []wireTool {
	if len(defs) == 0 {
		return nil
	}
	decls := make([]wireFunctionDeclaration, 0, len(defs))
	for _, d := range defs {
		schema := d.Parameters
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"OBJECT","properties":{}}`)
		}
		decls = append(decls, wireFunctionDeclaration{
			Name:        d.Name,
			Description: d.Description,
			Parameters:  schema,
		})
	}
	return []wireTool{{FunctionDeclarations: decls}}
}

func appendPara(b *strings.Builder, s string) {
	if s == "" {
		return
	}
	if b.Len() > 0 {
		b.WriteString("\n\n")
	}
	b.WriteString(s)
}

func nameOr(name, fallback string) string {
	if name != "" {
		return name
	}
	if fallback != "" {
		return fallback
	}
	return "sin nombre"
}

// toUsage traduce el usageMetadata del cable al tipo agnóstico.
//
// thoughtsTokenCount se reporta aparte como Reasoning, igual que
// convo.Usage ya modela para cualquier proveedor que separe esa cifra —
// Anthropic no la tiene porque nunca pide pensamiento extendido en este
// adaptador, pero Gemini SÍ puede reportarla incluso sin que este
// adaptador pida thinkingConfig, en modelos que piensan por defecto.
func toUsage(u *wireUsageMetadata) *convo.Usage {
	if u == nil {
		return nil
	}
	return &convo.Usage{
		In:        u.PromptTokenCount,
		Out:       u.CandidatesTokenCount,
		CacheRead: u.CachedContentTokenCount,
		Reasoning: u.ThoughtsTokenCount,
	}
}

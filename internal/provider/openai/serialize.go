package openai

import (
	"encoding/json"
	"strings"

	"github.com/MichiTrader/ishakat/internal/convo"
	"github.com/MichiTrader/ishakat/internal/provider"
)

// Aquí vive la traducción del modelo agnóstico de convo al dialecto de este
// proveedor. Es el único lugar donde el historial cambia de forma, y por eso
// también es el único lugar donde se decide qué degradar cuando el modelo de
// destino no sabe representar algo (§4.6).
//
// Vivía en internal/provider hasta el Paso 4. Se movió aquí porque
// []ChatMessage es una forma de cable, no un concepto agnóstico: dejarla en el
// paquete raíz obligaba a que el futuro adaptador de Anthropic conviviera con
// tipos de OpenAI. provider.Caps y provider.Degradation sí se quedaron arriba,
// porque son vocabulario del catálogo y de la interfaz.

// FromConvo traduce el historial al dialecto OpenAI de chat, aplanando los
// bloques a texto y reportando qué se degradó.
//
// El camino de las herramientas depende de Caps.Tools (§4.6): cuando es
// true, un BlockToolCall se serializa como el campo `tool_calls` de un
// mensaje assistant (con el id que el servicio asignó, para que el
// BlockToolResult correspondiente pueda correlacionarlo vía
// `tool_call_id`), y un BlockToolResult se serializa como un mensaje
// role:"tool". Cuando Caps.Tools es false, ambos se aplanan a texto
// descriptivo y se cuentan en Degradation.ToolsFlattened — el modelo
// sigue viendo qué pasó, pero no puede pedir más herramientas.
//
// El razonamiento se omite siempre: reenviarlo confunde al modelo y se
// paga dos veces. Las imágenes se anuncian pero no se envían hasta la
// Fase 3.
func FromConvo(msgs []convo.Message, caps provider.Caps) ([]ChatMessage, provider.Degradation) {
	out := make([]ChatMessage, 0, len(msgs))
	var deg provider.Degradation

	for _, m := range msgs {
		var b strings.Builder
		var toolCalls []wireToolCallOut
		var toolResults []ChatMessage

		for _, blk := range m.Blocks {
			switch blk.Kind {
			case convo.BlockText:
				appendPara(&b, blk.Text)

			case convo.BlockSummary:
				appendPara(&b, "[resumen de la conversación anterior]\n"+blk.Text)

			case convo.BlockReasoning:
				// No se reenvía nunca: el razonamiento es del turno en que se
				// produjo, y arrastrarlo ensucia el contexto.
				deg.ReasoningDropped++

			case convo.BlockImage:
				if caps.Images {
					// El envío real de imágenes llega en la Fase 3, cuando
					// ChatMessage.Content pase a ser una lista de partes;
					// hasta entonces se declara la pérdida en vez de fingir.
					deg.ImagesDropped++
					appendPara(&b, "[imagen adjunta: "+nameOr(blk.Name, blk.Mime)+"]")
					continue
				}
				deg.ImagesDropped++
				appendPara(&b, "[imagen adjunta no soportada por este modelo: "+nameOr(blk.Name, blk.Mime)+"]")

			case convo.BlockToolCall:
				if caps.Tools {
					toolCalls = append(toolCalls, wireToolCallOut{
						ID:   blk.ToolCallID,
						Type: "function",
						Function: wireToolFuncCall{
							Name:      blk.Name,
							Arguments: string(blk.Args),
						},
					})
				} else {
					deg.ToolsFlattened++
					appendPara(&b, "[llamada a herramienta "+blk.Name+"]\n"+string(blk.Args))
				}

			case convo.BlockToolResult:
				if caps.Tools {
					toolResults = append(toolResults, ChatMessage{
						Role:       "tool",
						ToolCallID: blk.ToolCallID,
						Content:    blk.Text,
					})
				} else {
					deg.ToolsFlattened++
					// Un fallo se marca como fallo. Aplanarlo igual que una salida
					// normal deja al modelo adivinando si "permission denied" es lo
					// que el comando imprimió o lo que le pasó al comando, y de esa
					// distinción depende que reaccione (§3: el error es dato).
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
			// Sin esta nota el modelo cree que se expresó completo y sigue
			// como si nada; con ella entiende que lo cortaron (§4).
			appendPara(&b, "(respuesta interrumpida por el usuario)")
			text = b.String()
		}

		// Un mensaje assistant con tool_calls puede tener content vacío:
		// algunos servicios exigen content:"" y otros lo rechazan si está
		// ausente. TrimSpace distingue "no hay texto" de "solo espacios".
		if strings.TrimSpace(text) != "" || len(toolCalls) > 0 {
			role := string(m.Role)
			// When Caps.Tools is false, a role:"tool" message has been
			// flattened to descriptive text. Sending role:"tool" without a
			// tool_call_id is invalid in the OpenAI dialect (the service
			// rejects it), so remap it to role:"user" — the flattened result
			// is information the model needs to see, and user is the only
			// role that carries free-form text without a correlation id.
			if role == string(convo.RoleTool) && !caps.Tools {
				role = string(convo.RoleUser)
			}
			out = append(out, ChatMessage{
				Role:      role,
				Content:   text,
				ToolCalls: toolCalls,
			})
		}
		// Los resultados de herramientas son mensajes propios role:"tool",
		// uno por llamada, no bloques del mensaje que los originó.
		out = append(out, toolResults...)
	}
	return out, deg
}

// MarshalTools serializa una lista de provider.ToolDef al array `tools` del
// dialecto (§12bis #5). Devuelve nil para una lista vacía, lo que permite al
// llamador distinguir "sin herramientas" de "una herramienta sin
// parámetros": el primero omite el campo del cuerpo, el segundo lo incluye.
func MarshalTools(defs []provider.ToolDef) []wireToolDef {
	if len(defs) == 0 {
		return nil
	}
	out := make([]wireToolDef, 0, len(defs))
	for _, d := range defs {
		params := d.Parameters
		if len(params) == 0 {
			// Un schema vacío explícito es más compatible que ausente:
			// algunos servicios rechazan una tool sin `parameters`.
			params = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		out = append(out, wireToolDef{
			Type: "function",
			Function: wireToolFunc{
				Name:        d.Name,
				Description: d.Description,
				Parameters:  params,
			},
		})
	}
	return out
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

// toUsage traduce el usage del cable al tipo agnóstico, aceptando las tres
// formas en que los servicios reportan caché y razonamiento.
func toUsage(u *wireUsage) *convo.Usage {
	if u == nil {
		return nil
	}
	out := &convo.Usage{In: u.PromptTokens, Out: u.CompletionTokens}

	if d := u.PromptTokensDetails; d != nil {
		out.CacheRead = d.CachedTokens
	}
	if u.CacheReadInputTokens > 0 {
		out.CacheRead = u.CacheReadInputTokens
	}
	if u.CacheCreationInputTokens > 0 {
		out.CacheWrite = u.CacheCreationInputTokens
	}
	if d := u.CompletionTokensDetails; d != nil {
		out.Reasoning = d.ReasoningTokens
	}

	// OpenAI cuenta los tokens de razonamiento dentro de completion_tokens.
	// Duplicarlos inflaría el total del footer, así que se descuentan.
	if out.Reasoning > 0 && out.Out >= out.Reasoning {
		out.Out -= out.Reasoning
	}

	// Los tokens de caché también van dentro de prompt_tokens en OpenAI.
	if out.CacheRead > 0 && out.In >= out.CacheRead {
		out.In -= out.CacheRead
	}
	return out
}

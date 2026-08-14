package anthropic

import (
	"encoding/json"
	"strings"

	"github.com/MichiTrader/ishakat/internal/convo"
	"github.com/MichiTrader/ishakat/internal/provider"
)

// Aquí vive la traducción del modelo agnóstico de convo al dialecto de
// Anthropic — el equivalente de openai/serialize.go para este proveedor.
// Vive en su propio paquete por la misma razón que la de OpenAI: []wireMessage
// es una forma de cable propia de este dialecto, no un concepto agnóstico.
//
// La diferencia estructural más grande frente al dialecto OpenAI: Anthropic
// no tiene mensaje de rol "system" dentro de `messages`, ni rol "tool". El
// prompt de sistema es un campo de nivel superior del request
// (wireRequest.System), y el resultado de una herramienta es un bloque de
// contenido tipo "tool_result" dentro de un mensaje "user", no un mensaje con
// su propio rol. FromConvo devuelve el system extraído por separado en vez de
// dejarlo como un mensaje más, precisamente porque en este dialecto no hay
// forma correcta de serializarlo como tal.

// FromConvo traduce el historial al dialecto Anthropic, extrayendo el prompt
// de sistema aparte y aplanando los bloques a texto donde corresponda,
// reportando qué se degradó.
//
// El camino de las herramientas depende de Caps.Tools (§4.6), igual que en el
// dialecto OpenAI: un BlockToolCall con Caps.Tools se serializa como un
// bloque de contenido "tool_use" dentro de un mensaje "assistant"; un
// BlockToolResult correspondiente se serializa como un bloque "tool_result"
// dentro de un mensaje "user" (nunca un mensaje "tool": ese rol no existe
// aquí). Cuando Caps.Tools es false, ambos se aplanan a texto descriptivo y
// se cuentan en Degradation.ToolsFlattened.
//
// El razonamiento se omite siempre, con la misma razón que el dialecto
// OpenAI: reenviarlo confunde al modelo y se paga dos veces. Esto también
// evita el requisito de firma del pensamiento extendido de Anthropic (un
// wireContent tipo "thinking" tiene que volver con su Signature intacta o el
// turno falla) porque este adaptador nunca pide pensamiento extendido
// (wireRequest no manda el campo `thinking`) y por tanto nunca lo recibe.
func FromConvo(msgs []convo.Message, caps provider.Caps) ([]wireMessage, string, provider.Degradation) {
	var system strings.Builder
	out := make([]wireMessage, 0, len(msgs))
	var deg provider.Degradation

	for _, m := range msgs {
		// El prompt de sistema no es un mensaje en este dialecto: es un
		// campo aparte del request. Varios mensajes de sistema en el
		// historial (poco común, pero convo lo permite) se concatenan en
		// el orden en que aparecen, igual que appendPara hace para el
		// resto de bloques.
		if m.Role == convo.RoleSystem {
			appendPara(&system, m.Text())
			continue
		}

		var b strings.Builder
		var blocks []wireContent

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
					// igual que en el dialecto OpenAI.
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
					blocks = append(blocks, wireContent{
						Type:  "tool_use",
						ID:    blk.ToolCallID,
						Name:  blk.Name,
						Input: args,
					})
				} else {
					deg.ToolsFlattened++
					appendPara(&b, "[llamada a herramienta "+blk.Name+"]\n"+string(blk.Args))
				}

			case convo.BlockToolResult:
				if caps.Tools {
					blocks = append(blocks, wireContent{
						Type:      "tool_result",
						ToolUseID: blk.ToolCallID,
						Content:   blk.Text,
						IsError:   blk.IsError,
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

		// El texto acumulado va primero, como bloque de contenido "text",
		// seguido de las llamadas/resultados de herramienta en el orden en
		// que aparecieron. TrimSpace distingue "no hay texto" de "solo
		// espacios", igual que en el dialecto OpenAI.
		var content []wireContent
		if strings.TrimSpace(text) != "" {
			content = append(content, wireContent{Type: "text", Text: text})
		}
		content = append(content, blocks...)
		if len(content) == 0 {
			// Un mensaje sin ningún bloque (por ejemplo: solo llevaba
			// razonamiento, que siempre se descarta) no se puede mandar:
			// Anthropic exige al menos un bloque de contenido por mensaje.
			continue
		}

		// role:"tool" no existe en este dialecto: el resultado de una
		// herramienta —serializado o aplanado— viaja en un mensaje
		// "user", el único rol que puede llevar un tool_result o texto
		// libre de vuelta al modelo.
		role := string(m.Role)
		if role == string(convo.RoleTool) {
			role = string(convo.RoleUser)
		}

		out = append(out, wireMessage{Role: role, Content: content})
	}
	return out, strings.TrimSpace(system.String()), deg
}

// MarshalTools serializa una lista de provider.ToolDef al array `tools` del
// dialecto de Anthropic. A diferencia de OpenAI (que envuelve
// name/description/parameters en un objeto "function" anidado), Anthropic
// los pone al mismo nivel que el propio elemento, bajo la clave
// `input_schema` en vez de `parameters`.
//
// Devuelve nil para una lista vacía, por la misma razón que la versión de
// OpenAI: permite distinguir "sin herramientas" (campo omitido del cuerpo)
// de "una herramienta sin parámetros" (el campo se manda con esa entrada).
func MarshalTools(defs []provider.ToolDef) []wireToolDef {
	if len(defs) == 0 {
		return nil
	}
	out := make([]wireToolDef, 0, len(defs))
	for _, d := range defs {
		schema := d.Parameters
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		out = append(out, wireToolDef{
			Name:        d.Name,
			Description: d.Description,
			InputSchema: schema,
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

// toUsage traduce el usage del cable al tipo agnóstico.
//
// A diferencia del dialecto OpenAI, Anthropic nunca cuenta los tokens de
// caché ni de razonamiento dentro de input/output_tokens — los reporta
// aparte desde el principio — así que no hace falta el descuento que
// openai.toUsage aplica.
func toUsage(u *wireUsage) *convo.Usage {
	if u == nil {
		return nil
	}
	return &convo.Usage{
		In:         u.InputTokens,
		Out:        u.OutputTokens,
		CacheRead:  u.CacheReadInputTokens,
		CacheWrite: u.CacheCreationInputTokens,
	}
}

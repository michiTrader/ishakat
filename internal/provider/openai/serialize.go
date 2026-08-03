package openai

import (
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
// El dialecto de este paso solo entiende texto: las imágenes se anuncian pero
// no se envían, las llamadas a herramientas se aplanan a texto legible, y el
// razonamiento se omite porque reenviarlo confunde al modelo y se paga dos
// veces. Cuando el Paso 6 traiga las capacidades reales del catálogo, esta
// misma función decide con datos en vez de por defecto.
func FromConvo(msgs []convo.Message, caps provider.Caps) ([]ChatMessage, provider.Degradation) {
	out := make([]ChatMessage, 0, len(msgs))
	var deg provider.Degradation

	for _, m := range msgs {
		var b strings.Builder
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
				deg.ToolsFlattened++
				appendPara(&b, "[llamada a herramienta "+blk.Name+"]\n"+string(blk.Args))

			case convo.BlockToolResult:
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

		text := b.String()
		if m.Aborted {
			// Sin esta nota el modelo cree que se expresó completo y sigue
			// como si nada; con ella entiende que lo cortaron (§4).
			appendPara(&b, "(respuesta interrumpida por el usuario)")
			text = b.String()
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		out = append(out, ChatMessage{Role: string(m.Role), Content: text})
	}
	return out, deg
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

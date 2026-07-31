package provider

import (
	"strings"

	"github.com/MichiTrader/ishakat/internal/convo"
)

// Aquí vive la traducción del modelo agnóstico de convo al dialecto del
// proveedor. Es el único lugar donde el historial cambia de forma, y por eso
// también es el único lugar donde se decide qué degradar cuando el modelo de
// destino no sabe representar algo (§4.6).

// Caps describe qué sabe hacer el modelo de destino. El catálogo (Paso 6) las
// rellena; hasta entonces el cero-valor significa "solo texto", que es el
// mínimo común denominador y siempre funciona.
type Caps struct {
	Images    bool
	Tools     bool
	Reasoning bool
}

// Degradation describe qué se perdió al serializar. La UI lo usa para avisar
// con honestidad en vez de mandar una petición que el modelo va a rechazar.
type Degradation struct {
	ImagesDropped    int
	ToolsFlattened   int
	ReasoningDropped int
}

// Any indica si hubo alguna pérdida.
func (d Degradation) Any() bool {
	return d.ImagesDropped > 0 || d.ToolsFlattened > 0 || d.ReasoningDropped > 0
}

// Reason devuelve una explicación en una línea, para la línea de aviso de la UI.
func (d Degradation) Reason() string {
	var parts []string
	if d.ImagesDropped > 0 {
		parts = append(parts, plural(d.ImagesDropped, "imagen", "imágenes")+" sin enviar")
	}
	if d.ToolsFlattened > 0 {
		parts = append(parts, plural(d.ToolsFlattened, "llamada a herramienta", "llamadas a herramientas")+" convertidas a texto")
	}
	if d.ReasoningDropped > 0 {
		parts = append(parts, plural(d.ReasoningDropped, "bloque de razonamiento", "bloques de razonamiento")+" omitidos")
	}
	return strings.Join(parts, ", ")
}

func plural(n int, one, many string) string {
	w := many
	if n == 1 {
		w = one
	}
	return itoa(n) + " " + w
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// FromConvo traduce el historial al dialecto OpenAI de chat, aplanando los
// bloques a texto y reportando qué se degradó.
//
// El dialecto OpenAI de este paso solo entiende texto: las imágenes se
// anuncian pero no se envían, las llamadas a herramientas se aplanan a texto
// legible, y el razonamiento se omite porque reenviarlo confunde al modelo y
// se paga dos veces. Cuando el Paso 6 traiga las capacidades reales del
// catálogo, esta misma función decide con datos en vez de por defecto.
func FromConvo(msgs []convo.Message, caps Caps) ([]ChatMessage, Degradation) {
	out := make([]ChatMessage, 0, len(msgs))
	var deg Degradation

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
					// El envío real de imágenes llega con el Paso 4; hasta
					// entonces se declara la pérdida en vez de fingir.
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
				appendPara(&b, "[resultado de "+blk.Name+"]\n"+blk.Text)
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

// ToUsage traduce el usage del proveedor al tipo agnóstico.
func ToUsage(u *Usage) *convo.Usage {
	if u == nil {
		return nil
	}
	return &convo.Usage{In: u.PromptTokens, Out: u.CompletionTokens}
}

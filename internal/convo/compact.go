package convo

// Aquí vive solo la parte pura de /compact: decidir qué se resume. El resumen
// en sí necesita hablar con un modelo, y eso es del engine (Paso 12). Separado
// así, la decisión se prueba con una tabla y sin red.

// Plan describe una compactación antes de ejecutarla.
type Plan struct {
	// Replace son los índices de los mensajes que el resumen sustituye.
	Replace []int
	// Keep son los índices que se conservan íntegros.
	Keep []int
	// Tokens es lo que ocupan hoy los mensajes a reemplazar.
	Tokens int
	// System son los índices de mensajes de sistema, que nunca se compactan.
	System []int
}

// Empty indica que no hay nada que compactar.
func (p Plan) Empty() bool { return len(p.Replace) == 0 }

// PlanCompact decide qué compactar conservando keepLastTurns turnos completos.
//
// Un "turno" es un mensaje de usuario y todo lo que vino después hasta el
// siguiente mensaje de usuario. Contar turnos y no mensajes es lo que importa:
// cortar entre una llamada a herramienta y su resultado deja el historial
// incoherente para el modelo.
//
// Los mensajes de sistema nunca se resumen, y los que un resumen anterior ya
// reemplazó tampoco se vuelven a tocar.
func PlanCompact(msgs []Message, keepLastTurns int) Plan {
	var p Plan
	if keepLastTurns < 0 {
		keepLastTurns = 0
	}

	replaced := map[int]bool{}
	for i := range msgs {
		for _, blk := range msgs[i].Blocks {
			if blk.Kind == BlockSummary {
				for _, idx := range blk.Replaces {
					replaced[idx] = true
				}
			}
		}
	}

	// Índices donde empieza cada turno de usuario.
	var starts []int
	for i, m := range msgs {
		if m.Role == RoleSystem {
			p.System = append(p.System, i)
			continue
		}
		if m.Role == RoleUser && !replaced[i] {
			starts = append(starts, i)
		}
	}

	// Frontera: primer índice que se conserva íntegro.
	boundary := len(msgs)
	if len(starts) > keepLastTurns {
		boundary = starts[len(starts)-keepLastTurns]
	} else if keepLastTurns > 0 {
		boundary = 0 // hay menos turnos que el mínimo a conservar: nada que hacer
	}

	for i, m := range msgs {
		switch {
		case m.Role == RoleSystem:
			// ya contabilizado en p.System; se conserva siempre
		case replaced[i]:
			// ya resumido antes
		case i < boundary:
			p.Replace = append(p.Replace, i)
			p.Tokens += EstimateMessage(m)
		default:
			p.Keep = append(p.Keep, i)
		}
	}
	return p
}

// ApplySummary anexa a la conversación el mensaje de resumen que reemplaza los
// índices del plan. No borra nada: el JSONL conserva el historial completo y
// el resumen declara qué rangos sustituye, de modo que compactar es auditable
// y reversible (§10).
func (c *Conversation) ApplySummary(p Plan, summary, model string) int {
	if p.Empty() || summary == "" {
		return -1
	}
	m := NewMessage(RoleAssistant, SummaryBlock(summary, p.Replace))
	m.Model = model
	return c.Add(m)
}

// NeedsCompact indica si el contexto cruzó el umbral configurado
// (compact.trigger_pct sobre la ventana del modelo activo).
func NeedsCompact(contextTokens, window, triggerPct int) bool {
	if window <= 0 || triggerPct <= 0 {
		return false
	}
	return contextTokens*100 >= window*triggerPct
}

// DropOldest es el plan de emergencia cuando el resumen falla: se descartan los
// mensajes más viejos hasta que el contexto cabe en target tokens.
func DropOldest(msgs []Message, target int) []int {
	total := Estimate(msgs)
	var drop []int
	for i, m := range msgs {
		if total <= target {
			break
		}
		if m.Role == RoleSystem {
			continue
		}
		drop = append(drop, i)
		total -= EstimateMessage(m)
	}
	return drop
}

// mission.go implementa la parte de persistencia de §21.16 decisión 3: un
// mission/tool-scope de §21.6 confirmado no vive solo en la memoria del
// Guard (internal/permissions), sino que se anexa al mismo JSONL de la
// sesión como su propio tipo de línea -- "a new event kind, not a sidecar
// file", en las palabras exactas de la decisión. §10 reafirma lo mismo por
// su cuenta: "the mission is appended to this same file as its own event
// kind -- not to a sidecar file".
//
// Tipo puro, sin dependencias externas salvo time, igual que el resto de
// este paquete (ver el comentario de cabecera de message.go).
package convo

import "time"

// MissionRule refleja permissions.MissionRule campo a campo -- capability y
// pattern, nada más -- para que convo nunca tenga que importar
// internal/permissions (§6.1: convo no importa permissions, ver
// arch_test.go's TestConvoEsPuro). Es el mismo patrón de "tipo espejo
// pequeño, convertido por un paquete puente de confianza" que ya usa
// mission.Rule -> permissions.MissionRule vía internal/tui's denyRulesOf:
// aquí el puente es internal/app, que traduce entre este tipo y
// permissions.MissionRule al restaurar una sesión (--resume) o al grabar
// una nueva resolución.
type MissionRule struct {
	Capability string `json:"capability"`
	Pattern    string `json:"pattern"`
}

// MissionEvent es una resolución completa de los dos diálogos de §21.6 (la
// misión y, encadenado siempre después, el alcance de herramientas) --
// persistida como su propio tipo de línea en el JSONL (recMission, en
// store.go).
//
// Goal se guarda solo para mostrarlo al restaurar la sesión (§21.16
// decisión 3: "on resume, the restored constraints are displayed, not
// merely reloaded") -- nunca se vuelve a compilar con mission.Compile al
// cargar, porque el texto del objetivo podría dar un resultado distinto si
// las reglas del compilador cambiaron entre versiones, y lo que hay que
// restaurar es exactamente lo que el usuario aceptó, no una reinterpretación
// de una versión nueva del compilador.
//
// Rules son las reglas de denegación que sí se aplicaron con
// Guard.AddMissionRules -- vacío si el usuario ajustó o suavizó el objetivo
// (missionAdjust/missionSoft) en vez de aceptarlo tal cual, o si este evento
// llegó por el camino de checkToolPolicy sin pasar nunca por el diálogo de
// misión. Al restaurar, se vuelven a pasar a AddMissionRules -- que
// acumula, nunca reemplaza -- así que cada MissionEvent de una sesión debe
// reproducirse en orden, no solo el último.
//
// BashScope es el alcance de bash resuelto por el segundo diálogo --
// siempre presente cuando una interacción real pasó por los dos diálogos
// encadenados de §21.6, porque resolveToolScope es quien graba el evento
// final y siempre resuelve a algún valor de scope (incluida la opción
// "todo instalado", que se representa como nil). Al restaurar, reemplaza
// -- no acumula -- igual que Guard.SetBashScope: solo el BashScope del
// último MissionEvent de la sesión importa.
type MissionEvent struct {
	Goal      string        `json:"goal,omitempty"`
	Rules     []MissionRule `json:"rules,omitempty"`
	BashScope []string      `json:"bash_scope,omitempty"`
	Ts        time.Time     `json:"ts"`
}

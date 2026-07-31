# ISHAKAT — Documento maestro del proyecto

**Versión:** 1.1 · **Última actualización:** 2026-07-30
**Estado:** Fase 1 cerrada · Fase 2 en curso · Paso 0 completado
**Naturaleza de este archivo:** fuente única de verdad. Contiene todo lo concebido y nada de lo descartado. Quien lo lea —persona o IA— puede ejecutar el proyecto completo sin necesitar contexto previo ni conversaciones anteriores.

---

## 0. Instrucciones para el agente que lee esto

Si eres una IA trabajando en este repositorio, lee este documento entero antes de escribir código y respeta estas reglas:

- Las decisiones marcadas como **CERRADA** no se rediscuten, se implementan. Si crees que una está equivocada, dilo explícitamente antes de cambiar nada, no la cambies por iniciativa propia.
- Cuando el documento dice "fuera de alcance en esta fase", eso es una restricción deliberada, no un olvido. El mayor riesgo del proyecto es la expansión de alcance. Un chat impecable vale más que un agente a medias.
- Implementa un paso a la vez, en el orden dado. Cada paso tiene un criterio de cierre verificable. No empieces el siguiente hasta que el actual pase su criterio. Al terminar un paso, actualiza la bitácora de la §17 de este mismo archivo y haz commit.
- No agregues dependencias sin justificarlo contra el presupuesto de la §6.4. El presupuesto es parte del producto, no una sugerencia.
- Escribe los tests indicados en cada paso antes o junto con el código, especialmente el del matcher difuso (Paso 7), que es el contrato con el requisito central del producto.
- Todo el código, comentarios y mensajes de error van en español. Los identificadores de Go van en inglés cuando es idiomático (`Config`, `Provider`, `Stream`), en español cuando es dominio propio del producto.

---

## 1. Qué es ishakat

Una interfaz de línea de comandos para conversar con modelos de inteligencia artificial desde el terminal. En vez de abrir un navegador, escribes en la terminal donde ya estás trabajando y el modelo responde ahí mismo, con el texto apareciendo palabra por palabra.

Ese tipo de herramienta ya existe —gemini-cli de Google, opencode, y una docena más— pero todas comparten dos defectos que ishakat existe para resolver.

El primero es que cambiar de modelo es doloroso. La mayoría eligen el modelo al arrancar y lo amarran al proceso: para pasar de un modelo caro y potente a uno barato y rápido a mitad de conversación hay que cerrar el programa, cambiar una variable de entorno, reabrirlo y perder el hilo. Y para elegir hay que escribir el identificador exacto, cosa de teclear `anthropic/claude-sonnet-4-5` sin fallar un carácter, entre quinientas opciones.

El segundo es que casi ninguna funciona bien en el teléfono. Termux es un emulador de terminal para Android que mucha gente usa como computador de bolsillo. La mayoría de estos CLIs se instalan con dificultad o no se instalan, porque arrastran dependencias que hay que compilar en el dispositivo o binarios que asumen un Linux de escritorio.

Ishakat es un solo archivo ejecutable, sin nada que instalar alrededor, que arranca en menos de 150 milisegundos, se ve bonito, y en el que cambiar de modelo es escribir `/model son45` y presionar Enter — con la conversación intacta.

### 1.1 La oportunidad

El acceso a modelos de IA se está fragmentando y abaratando a la vez. Un usuario típico tiene hoy acceso a media docena de proveedores distintos, cada uno bueno para algo diferente: uno razona mejor, otro es diez veces más barato, otro corre local y sin internet. La herramienta que gana no es la que se casa con un proveedor, sino la que hace trivial saltar entre todos.

Al mismo tiempo existe una capa nueva de infraestructura que resuelve el problema del lado del servidor: los gateways locales. OmniRoute es uno de ellos —código abierto, licencia MIT— que corre en tu propia máquina en `http://localhost:20128/v1` y expone cientos de proveedores tras una sola interfaz compatible con OpenAI. Ishakat no tiene que implementar 290 integraciones: implementa bien un dialecto y habla con todo.

El hueco de mercado es el cliente de terminal que aprovecha esa capa, cabe en un teléfono, y hace del cambio de modelo su función principal en vez de una configuración escondida.

### 1.2 Los cinco diferenciadores

En orden de importancia:

1. Instalación de un solo binario sin runtime, que en Termux es la diferencia entre "funciona" y "no lo instalo".
2. Cambio de modelo en caliente conservando el contexto, con verificación automática de que la conversación cabe en la ventana del modelo nuevo.
3. Selector de modelos con búsqueda difusa y etiquetas de gratis, costo y latencia leídas del catálogo, para elegir viendo información en vez de adivinar entre cientos de identificadores.
4. Layout responsivo real diseñado para 40 columnas, que es un teléfono en vertical, algo que ninguno de los dos referentes hace bien.
5. Animaciones conscientes de batería, que se apagan solas cuando no aportan.

---

## 2. Hallazgos de investigación que fundamentan el diseño

Resultado de la Fase 1. Explican por qué cada decisión posterior es como es.

**Por qué gemini-cli corre bien en Termux.** No es magia: su árbol de dependencias es JavaScript puro, sin módulos nativos. Lo que rompe en Termux son las dependencias que compilan C/C++ con node-gyp (`better-sqlite3`, `node-pty`, `sharp`, `keytar`) o los binarios precompilados contra glibc, porque Android usa Bionic libc. La lección transferible: la portabilidad se gana eliminando compilación en el dispositivo, no parcheándola.

**Por qué opencode cambia de modelo tan fácil.** Tres decisiones combinadas: el catálogo de modelos vive fuera del código, consumido de models.dev; los identificadores son uniformes con la forma `proveedor/modelo`; y el modelo activo vive en el estado de la sesión, no en la inicialización del proceso. Las tres se adoptan en ishakat.

models.dev publica tres endpoints, no uno. `api.json` (combinación proveedor+modelo, la que usa opencode), `models.json` (metadatos del modelo independientes del proveedor) y `catalog.json` (ambas). Esa distinción es crítica para el emparejamiento de metadatos descrito en §4.3.

**OmniRoute resuelve medio proyecto.** Endpoint OpenAI-compatible en `localhost:20128/v1`, con `GET /v1/models` que devuelve todo el catálogo, modelos virtuales (`auto`, `auto/coding`, `auto/fast`, `auto/cheap`, `auto/smart`, `auto/offline`), fallback automático entre proveedores, y funciona en Termux.

**La estética objetivo ya está construida en Go.** Crush, de Charm, usa Bubble Tea (arquitectura Elm), Lip Gloss (estilos y degradados), Bubbles (componentes) y Harmonica (animación con física). Bubble Tea v2 —estable desde el 23 de febrero de 2026— trae además dos regalos: el Cursed Renderer, que hace diffing de celdas al estilo ncurses, y downsampling de color automático, que degrada cualquier estilo ANSI al perfil real del terminal sin código nuestro.

---

## 3. Decisiones de arquitectura CERRADAS

**Stack:** Go 1.24+ con Bubble Tea v2 / Lip Gloss v2 / Bubbles v2. Produce un binario único de 15–25 MB, arranca en decenas de milisegundos, no necesita runtime, y el ecosistema Charm da exactamente la estética objetivo. Rutas de importación: `charm.land/bubbletea/v2`, `charm.land/lipgloss/v2`, `charm.land/bubbles/v2` (dominio vanity nuevo de v2, verificado).

**Compilación por plataforma.** `CGO_ENABLED=0` para linux y darwin. Para android/arm64, `CGO_ENABLED=1` con el NDK, apuntando CC a `aarch64-linux-android24-clang`. Esto es obligatorio y no negociable: un binario Go sin CGO usa el resolver DNS puro de Go, que lee `/etc/resolv.conf`, archivo que Android no tiene. El binario arranca, imprime `--version`, se ve perfecto, y muere en la primera petición HTTP con `lookup api.example.com on [::1]:53: connection refused`. El síntoma se esconde durante semanas porque el camino por defecto es `localhost:20128`, que no pasa por DNS. Como red de seguridad se implementa `internal/netfix` (§6.5).

**Modo inline, nunca alt-screen.** En alt-screen se pierde el scrollback del terminal y hay que reimplementar el scroll —que con dedos en un teléfono es peor que el nativo— y se rompe la selección de texto para copiar. En modo inline, lo que ya terminó se imprime una vez con `tea.Printf` y jamás se repinta; lo vivo ocupa las últimas líneas. Es el equivalente del `<Static>` de Ink que usa gemini-cli. Contrapartida aceptada: las líneas ya impresas no se re-envuelven al cambiar el ancho del terminal.

**Persistencia en JSONL, jamás SQLite.** Un archivo por sesión, append-only. Sin base de datos, sin índice, sin CGO. Sobrevive a un `kill -9` y se inspecciona con `tail`.

**No se inventa ningún protocolo nuevo.** Tres adaptadores de dialecto cubren el mercado: OpenAI (`chat/completions`), Anthropic (`messages`) y Google (`generateContent`). El 95% de los proveedores hablan OpenAI. Lo que sí se construye es un adaptador declarativo por configuración: agregar un proveedor es pegar cinco líneas de TOML, no escribir código.

Cuatro contratos internos gobiernan todo el sistema: el modelo de conversación agnóstico (§4.0), el catálogo de modelos (§4), el esquema de configuración (§5) y el tema como datos (§8).

---

## 4. Contrato 1: modelo de conversación agnóstico

Es la pieza que hace posible todo lo demás. El historial nunca se guarda en el formato JSON de un proveedor; se guarda en estructura propia y se serializa al dialecto correspondiente en el momento exacto de la petición.

```go
// internal/convo/message.go — tipos puros, cero dependencias externas
type Role string // "system" | "user" | "assistant" | "tool"

type BlockKind int
const (
    BlockText BlockKind = iota
    BlockImage
    BlockToolCall
    BlockToolResult
    BlockReasoning
    BlockSummary   // producido por /compact
)

type Block struct {
    Kind     BlockKind
    Text     string
    Data     []byte          // imágenes y adjuntos
    Mime     string
    Name     string          // nombre de herramienta
    Args     json.RawMessage
    Replaces []int           // solo BlockSummary: índices de mensajes resumidos
}

type Message struct {
    Role    Role
    Blocks  []Block
    Model   string     // Ref del modelo que generó este mensaje
    Usage   *Usage
    Aborted bool       // true si el usuario canceló a mitad de streaming
    Ts      time.Time
}

type Usage struct{ In, Out, CacheRead, CacheWrite, Reasoning int }
```

Dos campos que parecen menores y no lo son. `Model` guarda qué modelo generó cada mensaje, lo que permite pintar el transcript con atribución correcta cuando cambiaste tres veces de modelo en la misma sesión, y sirve de auditoría de costos. `Aborted` marca las respuestas cortadas: si botas el parcial, el usuario pierde tokens que ya pagó; si lo guardas sin marcar, el modelo cree que se expresó completo. Marcado, `/retry` sabe qué hacer y el serializador puede añadir "(respuesta interrumpida por el usuario)" al convertir el historial.

---

## 4bis. Contrato 2: el catálogo de modelos

### 4.1 El problema en una frase

Tres fuentes dicen cosas distintas y ninguna sola alcanza: el proveedor sabe qué se puede llamar ahora mismo, models.dev sabe cuánto cuesta y qué ventana tiene, y el usuario sabe qué quiere corregir. El catálogo las funde en un registro único y garantiza que el arranque nunca dependa de la red.

### 4.2 Registro normalizado

```go
// internal/catalog/model.go
type Model struct {
    Ref       string    // "omniroute/anthropic/claude-sonnet-4-5" ← clave única, lo que ve el usuario
    Provider  string    // "omniroute"
    WireID    string    // "anthropic/claude-sonnet-4-5" ← lo que va en el JSON de la petición
    Name      string    // "Claude Sonnet 4.5"
    Family    string    // "claude" — para agrupar y para fallback de metadatos

    Context   int       // 0 = desconocido
    MaxOutput int

    Cost      *Cost     // nil = DESCONOCIDO, que no es lo mismo que gratis
    Caps      Caps      // Tools, Vision, Reasoning, Streaming, JSONSchema, Attachments
    Tags      []string  // free | virtual | local | deprecated | beta

    Source    Source    // bitmask: Discover | ModelsDev | Config
    Health    Health    // ok | cooling | unauthenticated | unreachable

    // estadísticas locales; viven en el caché y alimentan el ranking difuso
    UseCount   int
    LastUsed   time.Time
    P50Latency time.Duration
    FailStreak int
}

type Cost struct{ In, Out, CacheRead, CacheWrite float64 } // USD por millón de tokens
```

Dos decisiones no obvias. `Ref` y `WireID` son campos separados porque el identificador que el usuario escribe lleva prefijo de proveedor y el que va al cable no. Y como OmniRoute sirve modelos cuyo propio ID ya contiene barras, `strings.Split(ref, "/")` es un bug: hay que partir únicamente en la primera barra, con `strings.Cut`. Segundo, `Cost == nil` significa "no sé", y el selector muestra `—` en vez de `$0`, porque marcar como gratis algo que cobra es la peor mentira que puede decir esa pantalla.

### 4.3 Fusión de las tres fuentes

La fusión es campo por campo, no registro por registro. La existencia de un modelo la define discovery: si el proveedor no lo lista, no se puede llamar. Los metadatos los aporta models.dev cuando discovery no los trae. La config del usuario gana siempre. Un modelo declarado a mano que discovery no reporta se mantiene visible pero marcado, que es exactamente el caso de los virtuales de OmniRoute.

El emparejamiento con models.dev se intenta en cascada: primero `provider/wire_id` exacto contra `api.json`; si falla, se normaliza el `wire_id` (minúsculas, quitar `-latest`, sufijos de fecha tipo `-20250219`, prefijos de vendor duplicados) y se reintenta; si falla, se busca por familia en `models.json` —la base agnóstica del proveedor, que existe justo para esto— resolviendo el caso del gateway que sirve Claude bajo otro nombre. Si nada empata, el modelo queda con metadatos desconocidos y la interfaz lo dice en vez de inventar.

Cuando falta la ventana de contexto tras toda esa cascada, no se adivina 128k. Se marca desconocida, se asume un piso conservador de 32k solo para las alertas de compactación, y la primera respuesta con usage real corrige el dato en el caché.

### 4.4 Caché y secuencia de arranque

Un solo archivo JSON en `$XDG_CACHE_HOME/ishakat/catalog.json`, escrito de forma atómica (temporal + rename) para que un Ctrl+C a media escritura no lo corrompa.

```json
{
  "v": 1,
  "fetched_at": "2026-07-30T14:02:11Z",
  "modelsdev": { "etag": "W/\"3f8a\"", "fetched_at": "...", "models": 1843 },
  "providers": {
    "omniroute": {
      "fetched_at": "2026-07-30T14:02:09Z",
      "ok": true,
      "models": [ { "wire_id": "auto/coding", "name": "Auto · Coding", "context": 200000 } ]
    }
  },
  "stats": {
    "omniroute/auto/coding": { "use_count": 41, "last_used": "...", "p50_ms": 820 }
  }
}
```

La secuencia es lo que hace que se sienta rápido. Se lee el caché y se pinta la interfaz de inmediato, sin tocar la red ni una vez, incluso con el caché vencido. En paralelo, si el TTL expiró, una goroutine hace discovery contra cada proveedor habilitado con timeout de 2 segundos y refresca models.dev con `If-None-Match`. Al terminar, el catálogo se reemplaza en caliente y, si el selector está abierto, la lista se reordena sin cerrarse. Sin red no pasa nada visible salvo una franja `⚠ catálogo de hace 3 días`. En primera ejecución sin caché y sin red se usa un catálogo semilla embebido en el binario con los virtuales de OmniRoute y los diez modelos más comunes.

**Presupuesto no negociable:** el arranque no toca la red en el camino crítico.

### 4.5 Resolución de referencias y búsqueda difusa

El corazón del requisito "no tener que escribir el ID exacto". La resolución pasa por cuatro etapas en orden y se detiene en la primera que produce un ganador claro: coincidencia exacta con un `Ref`; coincidencia exacta con un alias de la config; coincidencia única de sufijo (escribir `claude-sonnet-4-5` y que resuelva porque solo un proveedor lo sirve); y por último puntaje difuso.

El puntaje difuso es una subsecuencia con penalización por hueco, sobre ambos strings normalizados a minúsculas y sin los separadores `- _ / . :`. Sobre el puntaje base se aplican bonificaciones por coincidencia al inicio de palabra, por prefijo de proveedor, por dígitos en el mismo orden —esto es lo que hace que `son45` gane contra `sonnet-4-0`— y por uso reciente y frecuencia leídos de las estadísticas locales. Se penaliza `deprecated`. Si `prefer_free = true`, se bonifica `free`.

Traza del caso `/model son45`: normaliza a `son45`; contra `omniroute/anthropic/claude-sonnet-4-5` → `omniroutanthropicclaudesonnet45` encuentra `son` contiguo dentro de una palabra y `4,5` en orden al final, puntaje alto; contra `claude-sonnet-4-0` los dígitos no empatan y baja; contra `gpt-5-nano` no hay `son` y queda fuera.

**Regla de desempate:** si el mejor supera al segundo por más del 20%, cambia directo e imprime una línea de confirmación. Si no, abre el selector prefiltrado con `son45` ya escrito. Nunca, bajo ninguna circunstancia, un "modelo no encontrado" a secas.

### 4.6 Cambio en caliente: las tres verificaciones

Cambiar de modelo a mitad de conversación no es reasignar una variable. Antes de aceptar se corren tres chequeos, implementados como función pura testeable sin terminal:

```go
// internal/engine/hotswap.go
type ConflictKind int
const (
    ContextTooSmall ConflictKind = iota
    MissingCaps
    NoAuth
)

type Plan struct {
    OK        bool
    Conflicts []Conflict
    Suggested Action  // Compact | DropOldest | Cancel
    EstAfter  int     // tokens estimados si se compacta
}

func CheckSwap(c *convo.Conversation, from, to catalog.Model) Plan
```

El chequeo de contexto compara los tokens estimados contra `Context` del modelo destino y, si no caben, ofrece compactar. El de capacidades detecta bloques que el modelo nuevo no soporta —imágenes hacia un modelo sin visión, resultados de herramientas hacia uno sin tool calling— y advierte que se degradarán a texto descriptivo en vez de romper la petición. El de autenticación verifica que el proveedor destino tenga credencial resuelta antes de dejarte cambiar, no cuando mandes el mensaje.

Si `Plan.OK`, el cambio es instantáneo y lo único visible es una línea `── ahora: gpt-5-mini ──` en el transcript. El diálogo de conflicto aparece solo cuando hay una decisión real que tomar.

Ctrl+O (rotar favoritos) corre exactamente el mismo `CheckSwap`. Sin atajos por debajo.

---

## 5. Contrato 3: configuración

### 5.1 Ubicación y precedencia

Un archivo de usuario en `$XDG_CONFIG_HOME/ishakat/config.toml` (en Termux resuelve a `~/.config/ishakat/config.toml`). Opcionalmente `./.ishakat.toml` por proyecto, que se fusiona encima, pensado para fijar modelo y prompt de sistema por repositorio.

Precedencia de menor a mayor: valores compilados (`defaults.toml` embebido), config de usuario, config de proyecto, variables de entorno con prefijo `ISHAKAT_`, flags de línea de comandos. La fusión es profunda para tablas y de reemplazo total para arrays, con una excepción: los `[[provider]]` se fusionan por `id`, para que un proyecto pueda sobrescribir el `base_url` de omniroute sin redeclarar todo el bloque.

Cualquier string admite `$VAR` o `${VAR}`, expandido contra el entorno al cargar. Si la variable no existe, el proveedor no desaparece: se marca `unauthenticated` y sus modelos salen en gris con la nota "falta $X". El archivo se crea con permisos 0600 y el directorio con 0700; si al arrancar tiene permisos más laxos y contiene claves literales, se advierte una sola vez.

### 5.2 Archivo completo comentado (config.example.toml)

```toml
# ~/.config/ishakat/config.toml
schema = 1                       # versión del esquema; habilita migraciones automáticas

# ─────────────────────────────────────────────────────────────
[app]
default_model      = "omniroute/auto/coding"
compact_model      = "omniroute/auto/cheap"   # modelo barato para /compact y títulos
fallback_model     = "omniroute/auto"         # si el activo falla 2 veces seguidas
stream             = true
system_prompt      = ""
system_prompt_file = ""                       # el archivo gana si ambos existen
timeout_s          = 120
connect_timeout_s  = 10
max_retries        = 3
locale             = "auto"                   # auto | es | en

# ─────────────────────────────────────────────────────────────
[session]
save        = true
dir         = "$XDG_DATA_HOME/ishakat/sessions"
autoname    = true          # titula la sesión con compact_model tras el primer turno
keep_last   = 50
resume_last = false

# ─────────────────────────────────────────────────────────────
[ui]
theme      = "ascua"        # tema embebido o archivo en themes/
banner     = true
markdown   = true
syntax     = true
reasoning  = "collapsed"    # off | collapsed | full
timestamps = false
mouse      = false          # off por defecto: en Termux estorba al seleccionar texto
layout     = "auto"         # auto | minimal | narrow | wide
max_width  = 100
color      = "auto"         # auto | truecolor | 256 | 16 | off

[ui.animations]
mode            = "auto"    # auto = off si !TTY, TERM=dumb, NO_COLOR o ancho<40
fps             = 12        # techo duro de repintado
spinner         = "charm"   # charm | dots | line | none
face            = true      # carita con ojos que siguen el cursor
gradient_scroll = true
battery_saver   = "auto"    # auto = baja a 6 fps al detectar Android/Termux

[ui.footer]
items = ["model", "context", "tokens", "cost", "git", "cwd"]  # se recortan de derecha a izquierda

# ─────────────────────────────────────────────────────────────
[keys]
submit       = "enter"
newline      = "ctrl+j"     # y shift+enter donde el terminal lo distinga
cancel       = "esc"
quit         = "ctrl+c ctrl+c"
clear_screen = "ctrl+l"
model_picker = "ctrl+p"
model_cycle  = "ctrl+o"
theme_picker = "ctrl+t"
history_prev = "up"
history_next = "down"
copy_last    = "ctrl+y"

# ─────────────────────────────────────────────────────────────
[catalog]
sources            = ["provider", "modelsdev", "config"]
modelsdev_url      = "https://models.dev/api.json"
modelsdev_meta_url = "https://models.dev/models.json"
cache_file         = "$XDG_CACHE_HOME/ishakat/catalog.json"
ttl_h              = 24
refresh            = "background"   # background | startup | manual
offline_ok         = true
hide_deprecated    = true
prefer_free        = false

# ─────────────────────────────────────────────────────────────
[compact]
auto            = true
trigger_pct     = 80
keep_last_turns = 4
summary_tokens  = 800
strategy        = "summarize"   # summarize | drop-oldest
on_error        = "drop-oldest"

# ─────────────────────────────────────────────────────────────
[favorites]
list = ["omniroute/auto/coding", "omniroute/auto/fast", "omniroute/auto/cheap"]

[alias]
smart = "omniroute/auto/coding"
fast  = "omniroute/auto/fast"
cheap = "omniroute/auto/cheap"
local = "ollama/qwen2.5-coder:7b"

# ─────────────────────────────────────────────────────────────
# PROVEEDORES. Agregar uno = pegar 5 líneas, sin tocar código.
# ─────────────────────────────────────────────────────────────
[[provider]]
id        = "omniroute"
name      = "OmniRoute"
kind      = "openai"                    # openai | anthropic | gemini
base_url  = "http://localhost:20128/v1"
api_key   = "$OMNIROUTE_API_KEY"        # vacío = se envía sin Authorization
discover  = true                        # llena el catálogo con GET /models
enabled   = true
timeout_s = 180                         # los combos tardan más

  [provider.headers]
  "X-Title" = "ishakat"

  [provider.params]
  temperature = 0.7

  # Declarados a mano: se suman a los descubiertos y ganan sobre ellos.
  [[provider.model]]
  id      = "auto/coding"
  name    = "Auto · Coding"
  context = 200_000
  output  = 32_000
  tags    = ["virtual", "free"]

  [[provider.model]]
  id      = "auto/fast"
  name    = "Auto · Fast"
  context = 128_000
  tags    = ["virtual", "free"]

[[provider]]
id       = "openai"
kind     = "openai"
base_url = "https://api.openai.com/v1"
api_key  = "$OPENAI_API_KEY"
discover = true
enabled  = false          # declarado pero apagado

[[provider]]
id       = "anthropic"
kind     = "anthropic"
base_url = "https://api.anthropic.com/v1"
api_key  = "$ANTHROPIC_API_KEY"
discover = true
enabled  = false
  [provider.headers]
  "anthropic-version" = "2023-06-01"

[[provider]]
id       = "ollama"
kind     = "openai"
base_url = "http://127.0.0.1:11434/v1"
api_key  = ""
discover = true
enabled  = false
```

`internal/config/defaults.toml` es esta misma estructura sin comentarios, con un solo `[[provider]]` (omniroute). Los demás son sugerencias que viven únicamente en el ejemplo.

### 5.3 Validación

El cargador falla en arranque solo por tres cosas: schema desconocido, TOML sintácticamente inválido, o un `[[provider]]` sin `id`/`kind`/`base_url`. Todo lo demás degrada con advertencia visible en `/config`: un `kind` no soportado desactiva el proveedor; un `default_model` que no resuelve cae al primer proveedor habilitado; un tema inexistente cae a `ascua`; y las claves desconocidas se reportan como "clave ignorada" en vez de reventar, lo cual es esencial para no romper configs viejas al agregar funciones.

Los mensajes de error nombran el proveedor por su `id`, no por su índice, y traen el ejemplo de la línea que falta.

### 5.4 Contrato del adaptador de proveedor

Para que ese TOML sea suficiente, el código expone una sola interfaz:

```go
// internal/provider/provider.go
type Provider interface {
    ID() string
    Discover(ctx context.Context) ([]RawModel, error)
    Stream(ctx context.Context, req Request) (<-chan Event, error)
}

type EventKind int
const (
    EventDelta EventKind = iota
    EventToolCall
    EventUsage
    EventDone
    EventError
)

type Event struct {
    Kind  EventKind
    Text  string
    Usage *convo.Usage
    Err   error
}
```

`kind = "openai"` cubre OmniRoute, OpenAI, Groq, Together, OpenRouter, Ollama, LM Studio y DeepSeek. Los otros dos adaptadores existen únicamente para hablar directo con Anthropic y Google sin pasar por gateway, y por eso se posponen a la Fase 4.

---

## 6. Estructura del repositorio

### 6.1 La regla que ordena todo

El TUI no sabe qué es HTTP y el proveedor no sabe qué es un color. Todo lo que cruza esa frontera pasa por `convo` y `catalog`, que son tipos puros sin dependencias externas.

Esa frontera se prueba, no se promete: un test de CI que corra `go list -deps ./internal/tui` y falle si aparece `net/http`, y el simétrico para `provider` contra `lipgloss`, cuesta veinte líneas y evita el acoplamiento que después hace imposible testear.

### 6.2 Árbol

```
ishakat/
├── cmd/ishakat/main.go        # flags, subcomandos, elige TUI o headless
├── internal/
│   ├── app/
│   │   ├── app.go             # cableado: config → catálogo → engine → programa
│   │   └── headless.go        # ishakat -p "..."  (pipeline completo sin TUI)
│   ├── config/
│   │   ├── config.go  schema.go  merge.go  load.go
│   │   ├── expand.go  validate.go  redact.go
│   │   └── defaults.toml      # go:embed
│   ├── catalog/
│   │   ├── model.go           # Model, Ref/WireID, Cost, Caps
│   │   ├── store.go           # caché JSON atómico + TTL
│   │   ├── merge.go           # discovery ∪ models.dev ∪ config
│   │   ├── resolve.go         # exacto → alias → sufijo → difuso
│   │   ├── seed.go            # catálogo semilla embebido (go:embed)
│   │   └── modelsdev.go       # cliente con If-None-Match
│   ├── provider/
│   │   ├── provider.go        # interface Provider + Event + Request
│   │   ├── registry.go        # kind → constructor
│   │   ├── openai/            # dialecto OpenAI + parser SSE
│   │   └── fake/              # httptest.Server y proveedor de pruebas
│   ├── convo/
│   │   ├── message.go         # Message, Block, Role, Usage (tipos puros)
│   │   ├── store.go           # JSONL append-only, listar, resume
│   │   ├── tokens.go          # estimador + corrección con usage real
│   │   └── compact.go         # summarize / drop-oldest
│   ├── engine/
│   │   ├── engine.go  turn.go  retry.go  hotswap.go  streambuf.go
│   ├── tui/
│   │   ├── root.go            # modelo raíz de Bubble Tea
│   │   ├── msgs.go            # TODOS los tea.Msg propios, en un solo archivo
│   │   ├── keys.go            # keymap desde config
│   │   ├── chat.go            # transcript vivo + commit a scrollback
│   │   ├── input.go           # textarea + dropdown de slash commands
│   │   ├── footer.go
│   │   ├── picker.go          # selector de modelos (overlay)
│   │   ├── confirm.go         # diálogo de cambio con conflicto
│   │   ├── spinner.go         # animación tipo Crush + carita
│   │   ├── banner.go          # logo ASCII con degradado
│   │   └── layout.go          # breakpoints, ancho, recorte
│   ├── theme/                 # Fase 2: un tema embebido y la interfaz
│   ├── slash/                 # registro de comandos, parseo, autocompletado
│   ├── netfix/                # shim de DNS para Android
│   └── xdg/                   # rutas config/cache/data/state
├── testdata/                  # fixtures: /v1/models real, recorte api.json, SSE grabado
├── themes/ascua.toml
├── docs/PLAN.md               # este archivo
├── docs/ARCHITECTURE.md       # números del spike + decisiones fechadas
├── config.example.toml
├── AGENTS.md
├── Makefile
└── .github/workflows/
```

### 6.3 Comandos de arranque

```bash
mkdir ishakat && cd ishakat && git init
go mod init github.com/TU_USUARIO/ishakat

go get github.com/BurntSushi/toml

mkdir -p cmd/ishakat
mkdir -p internal/{app,config,catalog,convo,engine,theme,slash,netfix,xdg}
mkdir -p internal/provider/{openai,fake}
mkdir -p internal/tui testdata themes docs .github/workflows

printf 'bin/\ndist/\n*.jsonl\n' > .gitignore
```

Las dependencias de Charm entran en el Paso 3, no antes.

### 6.4 Presupuesto de dependencias (Fase 2: seis, máximo)

Bubble Tea v2, Lip Gloss v2, Bubbles v2, un parser TOML (`BurntSushi/toml`), `sahilm/fuzzy` solo como referencia de scoring —lo más probable es terminar con matcher propio porque se necesitan las bonificaciones por dígitos y por uso reciente— y `charmbracelet/x/exp/teatest` solo en tests. Glamour (Markdown) y Chroma (resaltado) se quedan afuera hasta la Fase 3: pesan varios MB y no aportan a "que funcione". Nada de cobra: flag de la stdlib y despacho manual.

### 6.5 El shim de DNS

```go
// internal/netfix/android.go
// Instala un resolver propio cuando detecta Android sin /etc/resolv.conf.
// Lee getprop net.dns1 / net.dns2, cae a 1.1.1.1 y 8.8.8.8 como último recurso.
func Install() (active string, err error)
```

`ishakat doctor` debe reportar qué resolver está activo, porque diagnosticar esto a ciegas es horrible. Verificación en el dispositivo: `GODEBUG=netdns=go+1 ./ishakat doctor`.

---

## 7. El loop de Bubble Tea v2

### 7.1 Modelo raíz y máquina de estados

```go
type Mode int
const (
    ModeChat Mode = iota // input enfocado, se puede escribir
    ModeBusy             // generando; solo esc y ctrl+c
    ModePicker           // overlay de modelos
    ModeConfirm          // diálogo de cambio con conflicto
    ModeHelp
)

type Root struct {
    cfg  *config.Config
    cat  *catalog.Catalog   // snapshot inmutable; se reemplaza entero al refrescar
    eng  *engine.Engine
    conv *convo.Conversation

    mode Mode
    lay  layout.Layout      // ancho, alto, breakpoint, animaciones on/off
    keys keys.Map

    input  textarea.Model
    live   liveTurn         // turno en curso: texto parcial, tokens, inicio
    picker picker.Model
    footer footer.Model
    spin   spinner.Model

    buf    *engine.StreamBuf
    cancel context.CancelFunc // no-nil solo en ModeBusy
    err    *uierr.Item
}
```

`Mode` es una sola variable y todas las decisiones de teclado y render cuelgan de ella. La alternativa —booleanos `showPicker`, `isStreaming`, `confirmOpen`— produce en dos semanas estados imposibles como picker abierto durante streaming con diálogo encima. Un enum, un switch, y se acabó.

El despacho en `Update` va en tres capas y en este orden: mensajes globales que aplican en cualquier modo (`tea.WindowSizeMsg`, ticks, eventos de stream, refresco de catálogo); teclas globales (`ctrl+c`, `ctrl+l`); y solo al final el switch `m.mode` que delega al componente enfocado. Invertir el orden hace que `esc` deje de cancelar cuando hay un overlay abierto.

### 7.2 La vista declarativa de v2

En v2 `View()` devuelve `tea.View`, no `string`. El modo inline es simplemente no activar `AltScreen`:

```go
func (m Root) View() tea.View {
    var v tea.View
    v.SetContent(m.render())        // solo la región viva
    v.AltScreen = false             // inline: conservamos el scrollback del terminal
    v.MouseMode = m.mouseMode()     // tea.MouseModeCellMotion solo si cfg.ui.mouse
    v.Cursor = m.cursorFor()        // posición real del cursor dentro del textarea
    return v
}
```

Las teclas se capturan con `tea.KeyPressMsg` (no `tea.KeyMsg`, que ahora es la interfaz que agrupa press y release), y `msg.String()` sigue siendo la forma cómoda de hacer match. Tres funciones nativas de v2 que se aprovechan directo: `tea.SetClipboard` implementa `/copy` y `ctrl+y` vía OSC52, funcionando incluso sobre SSH; el downsampling de color es automático, así que los bloques `[fallback.256]` del tema pasan de obligatorios a overrides opcionales; y `tea.EnvMsg` entrega el entorno real del cliente, útil para detectar Termux.

### 7.3 El puente de streaming: coalescing, no un mensaje por token

Este es el punto donde la mayoría de los TUIs de IA se ponen lentos en el celular. El patrón canónico —un `Cmd` que lee un evento del canal, lo devuelve como `Msg` y se re-emite— significa un ciclo Update+View completo por token, o sea 80–150 repintados por segundo. La solución es desacoplar la llegada de datos del repintado:

```go
// internal/engine/streambuf.go — vive fuera de Bubble Tea
type StreamBuf struct {
    mu    sync.Mutex
    text  strings.Builder
    usage *convo.Usage
    done  bool
    err   error
}

func (s *StreamBuf) push(delta string) {
    s.mu.Lock(); s.text.WriteString(delta); s.mu.Unlock()
}

func (s *StreamBuf) Drain() (chunk string, usage *convo.Usage, done bool, err error) {
    s.mu.Lock(); defer s.mu.Unlock()
    chunk = s.text.String()
    s.text.Reset()
    return chunk, s.usage, s.done, s.err
}
```

```go
// internal/tui/root.go
type streamTickMsg struct{}

func tickStream(d time.Duration) tea.Cmd {
    return tea.Tick(d, func(time.Time) tea.Msg { return streamTickMsg{} })
}

func (m Root) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    switch msg := msg.(type) {

    case streamTickMsg:
        chunk, usage, done, err := m.buf.Drain()
        if chunk != "" {
            m.live.Append(chunk)   // re-envuelve solo el bloque vivo
        }
        if usage != nil {
            m.live.Usage = usage
        }
        if !done {
            return m, tickStream(m.lay.StreamInterval) // 50ms normal, 100ms battery saver
        }
        return m.finishTurn(err)   // commit a scrollback + volver a ModeChat

    case tea.KeyPressMsg:
        if m.mode == ModeBusy && msg.String() == m.keys.Cancel {
            m.cancel()
            return m, nil
        }
        // ...
    }
    return m, nil
}
```

Veinte repintados por segundo se leen perfectamente fluidos y cuestan una fracción del CPU. Y como el tick solo se re-emite mientras hay un turno vivo, la aplicación en reposo consume exactamente cero: no hay ticker global de fondo, que es el pecado clásico de los TUIs con animaciones. El spinner y la carita corren en un tick independiente a `ui.animations.fps`, también activo solo en `ModeBusy`. Dos relojes, dos presupuestos.

### 7.4 Cancelación y cierre del turno

`esc` llama al `context.CancelFunc`. El engine ve el contexto muerto, cierra el cuerpo de la respuesta, escribe `done = true` en el buffer y termina la goroutine. El siguiente `streamTickMsg` drena lo que quedó y llama a `finishTurn`, que guarda el parcial como mensaje del asistente con `Aborted: true`.

`ctrl+c` una vez en `ModeBusy` equivale a `esc`. Dos veces en menos de un segundo, sale. Nunca salir con un solo `ctrl+c` durante generación: es demasiado fácil perder una respuesta larga por reflejo.

### 7.5 Commit al scrollback

Mientras hace streaming, el turno vive en el modelo. Al terminar se renderiza a texto definitivo, se emite con `tea.Printf` —que escribe por encima de la región dinámica sin pelearse con el renderer— y se borra del estado vivo. Ese es el equivalente exacto del `<Static>` de Ink.

---

## 8. Contrato 4: el tema como datos

```toml
# ~/.config/ishakat/themes/ascua.toml
name = "ascua"
dark = true

[gradient]
space  = "oklab"                          # oklab | oklch | hsl — nunca rgb lineal
stops  = ["#ff6a3d", "#ffa63d", "#ffe0a3"]
scroll = true

[colors]
fg        = "#e8e6e3"
fg_dim    = "#8a8580"
accent    = "#ff8a3d"
user      = "#7fd1b9"
assistant = "#ffb454"
border    = "#4a443f"
success   = "#8ec07c"
warn      = "#e8b25c"
error     = "#f2635f"
code_bg   = "#1c1a18"

[syntax]
keyword = "#ff8a3d"
string  = "#8ec07c"
comment = "#6b655f"
number  = "#d3869b"

# Overrides opcionales. Bubble Tea v2 hace downsampling automático;
# esto solo se declara cuando el resultado automático no convence.
[fallback.256]
accent = 209
[fallback.16]
accent = "yellow"
```

Los degradados se interpolan en espacio perceptual (Oklab) y no en RGB lineal, porque en RGB los pasos intermedios se ven sucios y grisáceos. La detección de capacidades lee `COLORTERM`, `TERM` y `NO_COLOR`, con override por `[ui] color`. Termux reporta truecolor correctamente.

---

## 9. Interfaz: wireframes a 40 columnas

### 9.1 Breakpoints

Cuatro modos, recalculados en cada `WindowSizeMsg`. Bajo 40 columnas es mínimo: sin cajas, sin banner, sin animaciones, prefijos de un carácter, footer de una línea recortada. De 40 a 59 es estrecho, que es Termux en vertical y es el que hay que hacer bien. De 60 a 99 es normal: bordes completos, dropdown de autocompletado, footer de dos secciones. De 100 en adelante es ancho: el selector pasa a dos columnas con panel de detalle y el texto se limita a `max_width`.

Todos los wireframes siguientes miden exactamente 40 columnas.

### 9.2 Arranque

```
1...5....0....5....0....5....0....5....0

  ▄▄▖ ▄▄▖  ▄▖  ▄▄▖  ▄▖
  ██▌ ██▌ ▐██▌ ▝▀█▖ ▄▖   ← degradado
  ▀▀▘ ▀▘▘ ▀▘▝▘ ▀▀▘  ▀▘     ámbar→ceniza

  ishakat 0.1.0 · ~/proyectos/api
  omniroute · auto/coding · 200k ctx

  Escribe para empezar. /help ayuda.

╭──────────────────────────────────────╮
│ >                                    │
╰──────────────────────────────────────╯
 ◍ auto/coding   ▏░░ 0%   0 tok  $0.00
```

El banner aparece solo si `ui.banner`, hay TTY y el alto es de al menos 20 líneas.

### 9.3 Conversación en streaming

```
1...5....0....5....0....5....0....5....0
 ▌ tú                            14:02
 ¿cómo optimizo esta consulta que
 hace full scan sobre events?

 ◆ claude-sonnet-4-5              14:02
 El problema es que el filtro por
 fecha no puede usar el índice
 existente. Un índice compuesto:

 │ sql
 │ CREATE INDEX idx_events_user
 │   ON events (user_id, created_at);

 Con eso el planner hace index scan▊

 ▚▞▘▝▚▗▘▚▞ pensando 3.4s · 412 tok
 esc cancela
╭──────────────────────────────────────╮
│ >                                    │
╰──────────────────────────────────────╯
 ◍ sonnet-4-5  ▍▓░ 18%  36k  $0.04  ⎇ma
```

Detalles que no son decorativos. Los bloques de código usan riel izquierdo `│` en vez de caja completa: a 40 columnas una caja roba 4 columnas útiles y hace que el código se envuelva feo, y además el riel deja el código copiable de un tirón, mientras que con caja se copian los bordes pegados. La línea `▚▞▘▝▚▗▘▚▞` es la animación tipo Crush: caracteres de un charset ciclando con el degradado desplazándose, a 12 fps máximo, solo en la línea de estado, nunca sobre el texto ya emitido. El `▊` es el cursor de streaming. El footer recorta el nombre del modelo de izquierda a derecha (`claude-sonnet-4-5` → `sonnet-4-5`) y elimina ítems de derecha a izquierda según `ui.footer.items`.

### 9.4 Selector de modelos (`/model` sin argumentos, o Ctrl+P)

```
1...5....0....5....0....5....0....5....0
╭─ modelos ──────────────────── 517 ─╮
│ 🔍 son45▊                          │
├────────────────────────────────────┤
│ OMNIROUTE                          │
│ ▸ anthropic/claude-sonnet-4-5      │
│   200k · $3/$15 · 🔧👁 · 0.8s      │
│   anthropic/claude-sonnet-4-0      │
│   200k · $3/$15 · 🔧👁             │
│                                    │
│ OPENROUTER                         │
│   anthropic/claude-sonnet-4.5      │
│   200k · $3.3/$16.5 · 🔧👁         │
├────────────────────────────────────┤
│ ↑↓ mover  ⏎ usar  tab detalle      │
│ ctrl+f solo gratis   esc salir     │
╰────────────────────────────────────╯
```

Dos líneas por modelo: identificador arriba, metadatos abajo. A 40 columnas meterlo todo en una línea obliga a truncar el ID, que es justamente el dato que hay que leer. Grupos por proveedor, colapsables con `←/→`. El contador de arriba baja mientras filtras. El activo lleva `●` en vez de `▸`, los favoritos llevan `★`, los gratis van en verde con la etiqueta `FREE` reemplazando al precio. La latencia sale de `P50Latency` local y solo aparece si has usado ese modelo antes: nada de números inventados. `ctrl+f` cicla filtros: todos → gratis → con herramientas → con visión → favoritos. Con catálogo vencido y sin red aparece la franja `⚠ catálogo de hace 3 días` bajo el buscador.

### 9.5 Confirmación de cambio con contexto insuficiente

```
1...5....0....5....0....5....0....5....0
╭─ cambiar modelo ───────────────────╮
│                                    │
│  de  claude-sonnet-4-5   200k      │
│  a   gpt-5-mini          128k      │
│                                    │
│  ⚠ la conversación usa 142k tok    │
│    y no cabe en 128k.              │
│                                    │
│  ▸ compactar y cambiar  (~38k)     │
│    cambiar y recortar los turnos   │
│      más viejos                    │
│    cancelar                        │
│                                    │
╰────────────────────────────────────╯
```

Aparece solo cuando hay conflicto real. El 95% de las veces el cambio es instantáneo y lo único visible es `── ahora: gpt-5-mini ──` con degradado tenue. Ese contraste es el punto: la fricción aparece únicamente cuando hay una decisión que tomar.

### 9.6 Autocompletado de slash commands

```
1...5....0....5....0....5....0....5....0
 ┌────────────────────────────────────┐
 │ /model    cambiar de modelo        │
 │ /models   listar catálogo          │
 │ /compact  resumir la conversación  │
 │ /config   ver configuración        │
 │ /copy     copiar última respuesta  │
 └────────────────────────────────────┘
╭──────────────────────────────────────╮
│ > /co▊                               │
╰──────────────────────────────────────╯
 ◍ auto/coding  ▍▓░ 18%  36k  $0.04
```

El dropdown se dibuja encima de la caja de input, no debajo, porque abajo está el footer y en una terminal corta no hay espacio. Cinco filas visibles con scroll, activado con `/` en la primera columna.

### 9.7 Ayuda

```
1...5....0....5....0....5....0....5....0
 ── ishakat · comandos ────────────────

 /help              esta pantalla
 /model [texto]     cambiar modelo
 /models            explorar catálogo
 /theme [nombre]    cambiar tema
 /compact           resumir contexto
 /new               conversación nueva
 /resume            reabrir una sesión
 /clear             limpiar pantalla
 /copy [n]          copiar respuesta
 /retry             reintentar último
 /stats             tokens y costo
 /config            config efectiva
 /debug             diagnóstico
 /exit              salir

 ── atajos ────────────────────────────

 ctrl+p   selector de modelos
 ctrl+o   rotar favoritos
 ctrl+t   selector de temas
 ctrl+j   salto de línea
 esc      cancelar generación
 ctrl+c×2 salir
 ctrl+l   limpiar pantalla
 ctrl+y   copiar última respuesta

 ↑↓ desplazar · esc volver
```

El registro de comandos es una tabla de datos, no un switch, para que esta pantalla y el autocompletado se generen solos.

### 9.8 Errores y compactación

```
1...5....0....5....0....5....0....5....0
 ◆ auto/coding
 ⚠ límite de tasa (429). Reintento 2
   de 3 en 4s…  esc cancela

 ⚠ omniroute no responde en :20128.
   ¿está corriendo? `omniroute start`
   ▸ reintentar   cambiar modelo

 ⟳ compactando 18 turnos con
   auto/cheap…  ▚▞▘▝▚

 ✓ compactado: 142k → 38k tokens
   (18 turnos → 1 resumen + 4 turnos)
```

Ningún error muestra JSON crudo en la superficie. El volcado completo queda en `/debug` y en `$XDG_STATE_HOME/ishakat/last-error.json`, siempre con claves redactadas.

---

## 10. Persistencia

Un archivo por sesión en `$XDG_DATA_HOME/ishakat/sessions/2026-07-30T14-02-11-a3f9.jsonl`. Primera línea: objeto de cabecera con `id`, `título`, `timestamps`, `modelo inicial` y `versión de esquema`. Después, un `convo.Message` serializado por línea, anexado cuando el mensaje se completa, nunca durante el streaming. `/resume` lista los archivos, lee solo la primera línea de cada uno para armar el menú, y carga el archivo completo únicamente al elegir.

`/compact` no reescribe el archivo: anexa un mensaje con un `BlockSummary` que declara qué rangos reemplaza, de modo que el historial completo queda auditable y compactar es reversible.

---

## 11. Las cinco fases

### Fase 1 — Investigación y arquitectura · CERRADA

Entregada en este documento: los cuatro contratos, el esquema de configuración completo, el diseño del catálogo, los wireframes a 40 columnas.

### Fase 2 — Prototipo · EN CURSO

Un chat que funciona de verdad, feo pero sólido: streaming SSE token por token, input multilínea, historial navegable con flechas, comandos mínimos, persistencia JSONL, y el selector de modelos, que es el diferenciador principal.

Orden concreto de implementación. Cada paso deja algo funcionando y probable, y el riesgo grande —Termux, red, DNS— se toca primero. El detalle de cada paso está en §12.

| # | Paso | Estado |
|---|------|--------|
| 0 | Spike medido en teléfono real | ✅ hecho |
| 1 | Esqueleto y configuración | ⬜ siguiente |
| 2 | Tipos de conversación y almacén JSONL | ⬜ |
| 3 | Esqueleto de TUI sin red (banner, degradado) | ⬜ |
| 4 | Adaptador OpenAI con SSE | ⬜ |
| 5 | Modo headless `ishakat -p` | ⬜ |
| 6 | Catálogo (discovery, caché, merge) | ⬜ |
| 7 | Resolución y matcher difuso | ⬜ |
| 8 | Conectar engine y TUI (coalescing) | ⬜ |
| 9 | Registro de slash commands | ⬜ |
| 10 | Picker de modelos | ⬜ |
| 11 | Cambio en caliente (CheckSwap) | ⬜ |
| 12 | `/compact` del lado del cliente | ⬜ |
| 13 | Cierre: historial, `/copy`, `/retry`, `/stats`, `doctor` | ⬜ |

**Aceptación de la Fase 2.** En un teléfono limpio: un `curl \| sh` instala el binario en menos de dos minutos; se conversa con OmniRoute con streaming visible; se cambia de modelo tres veces en la misma conversación, al menos una hacia un modelo con ventana más chica, sin perder el hilo; `esc` cancela sin romper nada; se cierra y `ishakat --resume` recupera la sesión completa; todo a 40 columnas en vertical sin una línea rota. Números: arranque bajo 150 ms con catálogo cacheado, RSS bajo 60 MB con 50 turnos, y cero repintados en reposo (verificable con `top` mostrando 0% de CPU).

**Fuera de alcance en Fase 2**, por más que dé comidilla: tool calling, MCP, temas en archivo (basta uno embebido), Markdown con Glamour, resaltado de sintaxis, mouse, imágenes, y los adaptadores de Anthropic y Gemini. Los últimos son trampa pura: `kind = "openai"` contra OmniRoute ya te da Claude y Gemini, así que escribirlos ahora es trabajo sin funcionalidad nueva visible.

### Fase 3 — Mejoras internas y estéticas

Ahora lo bonito, con disciplina de rendimiento. Temas en archivos con `/theme` en vivo; degradados interpolados en Oklab; degradación de color verificada contra terminales pobres (Bubble Tea v2 la hace automática, pero hay que comprobarla). Caja de input con bordes, footer completo, dropdown de autocompletado, Markdown renderizado (Glamour entra aquí) y bloques de código resaltados (Chroma entra aquí).

Aquí van las dos ideas visuales del producto. La carita con ojos que siguen el cursor mapea la columna del cursor sobre el ancho del input a una posición de pupila en el rango −1 a 1, con temporizador de parpadeo y repintado solo cuando el input cambia. La animación tipo Crush es un ciclo de caracteres de un charset con el degradado desplazándose. Ambas con dos reglas no negociables: máximo 10–15 fps, y apagado automático sin TTY, con `TERM=dumb`, con `--no-anim`, o bajo 40 columnas. En un celular esas animaciones son exactamente lo que se come la batería.

### Fase 4 — Solución (robustez)

La fase menos divertida y la que decide si la gente lo usa. Reintentos con backoff exponencial respetando `Retry-After` en los 429, timeouts configurables, cancelación limpia, y mensajes de error legibles en vez de volcados de JSON. Modo offline real: sin red, el catálogo cacheado sirve y el CLI arranca igual. Fallback automático a `fallback_model` si el activo falla dos veces seguidas —OmniRoute ya lo hace por dentro, pero un usuario apuntando a un proveedor directo lo necesita.

Aquí entran también los adaptadores de Anthropic y Gemini, las pruebas de los tres dialectos contra servidores simulados, el perfilado con presupuestos explícitos, y la revisión de seguridad: claves nunca en logs, permisos 600, `/debug` que redacta secretos.

### Fase 5 — Creación (distribución)

Binarios cross-compilados para android/arm64 (con NDK y CGO, obligatorio), linux/amd64, linux/arm64 y darwin/arm64, publicados en GitHub Releases vía GitHub Actions, más un `install.sh` que detecte Termux y ponga el binario en `$PREFIX/bin`. Opcionalmente un paquete npm que solo descargue el binario correcto, para quien prefiera `npm i -g`.

README con GIF grabado en un celular, documentación de la config, guía de "cómo agregar un proveedor" y un tema de ejemplo.

---

## 12. Detalle de los pasos de la Fase 2

### Paso 0 · Spike — ✅ COMPLETADO

Hola mundo con Bubble Tea v2 compilado con `GOOS=android GOARCH=arm64 CGO_ENABLED=1 CC=$NDK/.../aarch64-linux-android24-clang`, corriendo en el teléfono, haciendo un GET real a `https://models.dev/api.json` y otro a `localhost:20128/v1/models`.

Higiene pendiente si no se hizo: anotar en `docs/ARCHITECTURE.md` los números reales (arranque en ms, RSS, si el DNS resolvió con CGO o hubo que tocar algo) y mover el hola-mundo a una rama `spike/` o borrarlo. No dejarlo flotando en `main`.

### Paso 1 · Esqueleto y configuración — ⬜ SIGUIENTE

**Objetivo.** Un binario `ishakat` que todavía no chatea, pero que carga, fusiona, expande y valida la configuración completa, y responde a `config init`, `config path`, `config check` y `doctor`.

**Criterio de cierre.** `ishakat config check` acepta `config.example.toml` sin errores y rechaza con mensaje legible un `[[provider]]` sin `base_url`.

Es el paso menos vistoso del proyecto y el que más deuda evita, porque todo lo demás (catálogo, engine, TUI) lee de aquí. Una sola dependencia: `BurntSushi/toml`.

#### 1.1 `internal/xdg` — rutas y detección de Termux

Empieza por aquí porque todo lo demás lo importa.

```go
// internal/xdg/xdg.go
package xdg

import (
	"os"
	"path/filepath"
	"strings"
)

const App = "ishakat"

func home() string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h
	}
	return "."
}

func base(env string, def ...string) string {
	if v := os.Getenv(env); v != "" {
		return v
	}
	return filepath.Join(append([]string{home()}, def...)...)
}

func ConfigDir() string { return filepath.Join(base("XDG_CONFIG_HOME", ".config"), App) }
func CacheDir() string  { return filepath.Join(base("XDG_CACHE_HOME", ".cache"), App) }
func DataDir() string   { return filepath.Join(base("XDG_DATA_HOME", ".local", "share"), App) }
func StateDir() string  { return filepath.Join(base("XDG_STATE_HOME", ".local", "state"), App) }

func ConfigFile() string  { return filepath.Join(ConfigDir(), "config.toml") }
func ThemesDir() string   { return filepath.Join(ConfigDir(), "themes") }
func CatalogFile() string { return filepath.Join(CacheDir(), "catalog.json") }
func SessionsDir() string { return filepath.Join(DataDir(), "sessions") }
func ErrorFile() string   { return filepath.Join(StateDir(), "last-error.json") }

// EnsureDir crea con 0700, como exige §5.1.
func EnsureDir(p string) error { return os.MkdirAll(p, 0o700) }

// IsTermux alimenta battery_saver = "auto" y el instalador de la Fase 5.
func IsTermux() bool {
	if strings.Contains(os.Getenv("PREFIX"), "com.termux") {
		return true
	}
	_, err := os.Stat("/data/data/com.termux/files/usr")
	return err == nil
}
```

En Termux `HOME` es `/data/data/com.termux/files/home` y `XDG_CONFIG_HOME` normalmente no está definido, así que la config cae en `~/.config/ishakat/config.toml`. Verifícalo con `ishakat config path` en el teléfono al final del paso.

#### 1.2 La estrategia de fusión (léela antes de codificar)

Es la única decisión técnica no obvia del paso, y equivocarse cuesta reescribir el paquete.

El impulso natural es decodificar cada capa directamente sobre el mismo `struct Config`. No sirve: cuando el archivo de proyecto trae un `[[provider]]`, el slice completo se reemplaza y pierdes los defaults, y no hay forma de saber si `enabled = false` fue escrito por el usuario o es el cero de Go.

La estrategia correcta es decodificar cada capa a `map[string]any`, fusionar los mapas con reglas explícitas, y recién al final decodificar el mapa fusionado al struct. Con eso obtienes semántica exacta de "solo las claves presentes ganan", el merge por `id` de los proveedores sale natural, y `md.Undecoded()` del decode final te da gratis la lista de claves desconocidas para reportarlas como advertencia.

Que los defaults sean un TOML embebido y no una función `Defaults()` en Go es deliberado: una sola fuente de verdad, la capa 0 se fusiona con el mismo código que las demás, y ishakat funciona sin ningún archivo de config porque el proveedor `omniroute` ya viene declarado ahí.

#### 1.3 `internal/config/schema.go`

```go
package config

const Schema = 1

type Config struct {
	Schema    int               `toml:"schema"`
	App       App               `toml:"app"`
	Session   Session           `toml:"session"`
	UI        UI                `toml:"ui"`
	Keys      Keys              `toml:"keys"`
	Catalog   Catalog           `toml:"catalog"`
	Compact   Compact           `toml:"compact"`
	Favorites Favorites         `toml:"favorites"`
	Alias     map[string]string `toml:"alias"`
	Providers []Provider        `toml:"provider"`

	// No se serializa: diagnóstico de la carga.
	Files    []string          `toml:"-"` // capas efectivamente leídas, en orden
	Warnings []Warning         `toml:"-"`
	EnvUsed  map[string]string `toml:"-"` // "$OMNIROUTE_API_KEY" -> "sk-…9f2"
}

type App struct {
	DefaultModel     string `toml:"default_model"`
	CompactModel     string `toml:"compact_model"`
	FallbackModel    string `toml:"fallback_model"`
	Stream           bool   `toml:"stream"`
	SystemPrompt     string `toml:"system_prompt"`
	SystemPromptFile string `toml:"system_prompt_file"`
	TimeoutS         int    `toml:"timeout_s"`
	ConnectTimeoutS  int    `toml:"connect_timeout_s"`
	MaxRetries       int    `toml:"max_retries"`
	Locale           string `toml:"locale"`
}

type UI struct {
	Theme      string     `toml:"theme"`
	Banner     bool       `toml:"banner"`
	Markdown   bool       `toml:"markdown"`
	Syntax     bool       `toml:"syntax"`
	Reasoning  string     `toml:"reasoning"`
	Timestamps bool       `toml:"timestamps"`
	Mouse      bool       `toml:"mouse"`
	Layout     string     `toml:"layout"`
	MaxWidth   int        `toml:"max_width"`
	Color      string     `toml:"color"`
	Animations Animations `toml:"animations"`
	Footer     Footer     `toml:"footer"`
}

type Animations struct {
	Mode           string `toml:"mode"`
	FPS            int    `toml:"fps"`
	Spinner        string `toml:"spinner"`
	Face           bool   `toml:"face"`
	GradientScroll bool   `toml:"gradient_scroll"`
	BatterySaver   string `toml:"battery_saver"`
}

type Footer struct {
	Items []string `toml:"items"`
}

type Provider struct {
	ID       string            `toml:"id"`
	Name     string            `toml:"name"`
	Kind     string            `toml:"kind"` // openai | anthropic | gemini
	BaseURL  string            `toml:"base_url"`
	APIKey   string            `toml:"api_key"`
	Discover bool              `toml:"discover"`
	Enabled  bool              `toml:"enabled"`
	TimeoutS int               `toml:"timeout_s"`
	Headers  map[string]string `toml:"headers"`
	Params   map[string]any    `toml:"params"`
	Models   []ProviderModel   `toml:"model"`

	// Derivados de la carga, no del archivo.
	AuthOK     bool   `toml:"-"`
	MissingEnv string `toml:"-"` // "OMNIROUTE_API_KEY" si la variable no existe
}

type ProviderModel struct {
	ID      string   `toml:"id"`
	Name    string   `toml:"name"`
	Context int      `toml:"context"`
	Output  int      `toml:"output"`
	Tags    []string `toml:"tags"`
}

type Warning struct {
	Where string // "config.toml", "provider[2]"
	Msg   string
}

// Session, Keys, Catalog, Compact, Favorites: igual de mecánicos, cópialos de §5.2.
```

`Enabled` y `Discover` pueden ser `bool` normal y no `*bool` solo porque los defaults llegan por TOML. Para un `[[provider]]` nuevo que el usuario declare sin esas claves, el merge rellena `enabled = true` y `discover = true` desde la plantilla de 1.4. Si algún día quitas la capa de defaults en TOML, tendrás que volver a punteros.

#### 1.4 `internal/config/merge.go`

```go
package config

func mergeRoot(dst, src map[string]any) map[string]any {
	if dst == nil {
		dst = map[string]any{}
	}
	for k, v := range src {
		if k == "provider" {
			dst[k] = mergeProviders(dst[k], v)
			continue
		}
		dst[k] = mergeValue(dst[k], v)
	}
	return dst
}

func mergeValue(dst, src any) any {
	sm, sok := src.(map[string]any)
	dm, dok := dst.(map[string]any)
	if sok && dok {
		for k, v := range sm {
			dm[k] = mergeValue(dm[k], v)
		}
		return dm
	}
	return src // escalares y arrays: reemplazo total (§5.1)
}

// Plantilla para un [[provider]] que aparece por primera vez.
var providerTemplate = map[string]any{
	"kind":     "openai",
	"discover": true,
	"enabled":  true,
	"api_key":  "",
}

func mergeProviders(dstAny, srcAny any) any {
	dst, src := toTables(dstAny), toTables(srcAny)
	out := make([]map[string]any, 0, len(dst)+len(src))
	idx := map[string]int{}
	for _, p := range dst {
		id, _ := p["id"].(string)
		idx[id] = len(out)
		out = append(out, p)
	}
	for _, p := range src {
		id, _ := p["id"].(string)
		if i, ok := idx[id]; ok {
			out[i] = mergeRoot(out[i], p) // fusión por id: §5.1
			continue
		}
		idx[id] = len(out)
		out = append(out, mergeRoot(cloneMap(providerTemplate), p))
	}
	return out
}

func toTables(v any) []map[string]any {
	switch t := v.(type) {
	case []map[string]any:
		return t
	case []any:
		out := make([]map[string]any, 0, len(t))
		for _, e := range t {
			if m, ok := e.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	}
	return nil
}
```

`cloneMap` es una copia superficial de una línea; la plantilla no tiene submapas.

#### 1.5 `internal/config/load.go`

```go
package config

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/BurntSushi/toml"
	"github.com/TU_USUARIO/ishakat/internal/xdg"
)

//go:embed defaults.toml
var defaultsTOML string

type Options struct {
	UserPath    string // "" = xdg.ConfigFile()
	ProjectPath string // "" = "./.ishakat.toml"
	SkipProject bool
	Overrides   map[string]any // flags ya traducidos a rutas punteadas
}

func Load(o Options) (*Config, error) {
	if o.UserPath == "" {
		o.UserPath = xdg.ConfigFile()
	}
	if o.ProjectPath == "" {
		o.ProjectPath = ".ishakat.toml"
	}

	raw := map[string]any{}
	var files []string
	var warns []Warning

	if err := decodeInto(raw, defaultsTOML); err != nil {
		return nil, fmt.Errorf("defaults embebidos corruptos: %w", err) // bug nuestro
	}

	layers := []string{o.UserPath}
	if !o.SkipProject {
		layers = append(layers, o.ProjectPath)
	}
	for _, p := range layers {
		b, err := os.ReadFile(p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		var m map[string]any
		if _, err := toml.Decode(string(b), &m); err != nil {
			return nil, fmt.Errorf("%s: TOML inválido: %w", p, err) // fatal (§5.3)
		}
		raw = mergeRoot(raw, m)
		files = append(files, p)
		warns = append(warns, checkPerms(p)...)
	}

	applyEnv(raw)
	for path, v := range o.Overrides {
		setPath(raw, path, v)
	}

	// Re-serializar y decodificar al struct: así md.Undecoded() reporta
	// claves desconocidas sobre el resultado final ya fusionado.
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(raw); err != nil {
		return nil, fmt.Errorf("no se pudo normalizar la configuración: %w", err)
	}
	cfg := &Config{EnvUsed: map[string]string{}}
	md, err := toml.Decode(buf.String(), cfg)
	if err != nil {
		return nil, err
	}
	for _, k := range md.Undecoded() {
		warns = append(warns, Warning{Where: "config", Msg: "clave ignorada: " + k.String()})
	}

	cfg.Files = files
	warns = append(warns, expandVars(cfg)...)
	cfg.Warnings = append(cfg.Warnings, warns...)

	if err := Validate(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
```

`applyEnv` no adivina rutas a partir del nombre de la variable — `ISHAKAT_APP_DEFAULT_MODEL` es ambiguo porque no sabes dónde parte el guion bajo. Usa una tabla explícita y corta:

```go
var envMap = map[string]string{
	"ISHAKAT_MODEL":   "app.default_model",
	"ISHAKAT_THEME":   "ui.theme",
	"ISHAKAT_COLOR":   "ui.color",
	"ISHAKAT_NO_ANIM": "ui.animations.mode", // "1" => "off"
}
```

Las claves de API no se pasan por aquí: viajan como `$VAR` dentro del TOML y se expanden en `expandVars`. Un solo mecanismo para secretos.

#### 1.6 `internal/config/expand.go`

Recorrido con `reflect` sobre todos los `string` del struct (campos, valores de mapas y elementos de slices), reemplazando `$VAR` y `${VAR}`. Los proveedores se procesan primero para poder marcar el estado de autenticación:

```go
var varRe = regexp.MustCompile(`\$\{?([A-Za-z_][A-Za-z0-9_]*)\}?`)

func expandVars(c *Config) []Warning {
	var warns []Warning
	for i := range c.Providers {
		p := &c.Providers[i]
		raw := p.APIKey
		val, missing := expandString(raw, c.EnvUsed)
		p.APIKey = val
		switch {
		case raw == "":
			p.AuthOK = true // proveedor local sin clave: legítimo
		case missing != "":
			p.AuthOK, p.MissingEnv = false, missing
			warns = append(warns, Warning{
				Where: "provider[" + p.ID + "]",
				Msg:   "falta $" + missing + "; el proveedor queda sin autenticar",
			})
		default:
			p.AuthOK = true
		}
	}
	walkStrings(c, func(s string) string { v, _ := expandString(s, c.EnvUsed); return v })
	return warns
}
```

El detalle que importa: una variable ausente no elimina el proveedor. Lo deja `AuthOK = false` para que el selector lo muestre en gris con la nota. Si lo borras aquí, el usuario ve modelos desaparecidos sin explicación y no hay forma de diagnosticarlo.

También expande `$XDG_DATA_HOME` y `$XDG_CACHE_HOME`, que aparecen en los defaults. En Termux esas variables no existen, así que `expandString` debe consultar primero a `xdg` para esos tres nombres concretos antes de caer a `os.LookupEnv`. Si no lo haces, `session.dir` queda como `/ishakat/sessions` y escribes en la raíz.

#### 1.7 `validate.go` y `redact.go`

```go
func Validate(c *Config) error {
	if c.Schema != Schema {
		return fmt.Errorf("schema = %d no soportado (esta versión entiende %d); "+
			"actualiza ishakat o corrige la primera línea de config.toml", c.Schema, Schema)
	}
	seen := map[string]bool{}
	for i := range c.Providers {
		p := &c.Providers[i]
		where := fmt.Sprintf("provider[%d]", i)
		if p.ID == "" {
			return fmt.Errorf("%s: falta id. Cada [[provider]] necesita un id único", where)
		}
		if seen[p.ID] {
			return fmt.Errorf("provider %q está declarado dos veces", p.ID)
		}
		seen[p.ID] = true
		if p.Kind == "" {
			return fmt.Errorf("provider %q: falta kind. Usa openai, anthropic o gemini", p.ID)
		}
		if p.BaseURL == "" {
			return fmt.Errorf("provider %q: falta base_url.\n  Ejemplo: base_url = \"https://api.openai.com/v1\"", p.ID)
		}
		if !validKind(p.Kind) {
			p.Enabled = false
			c.Warnings = append(c.Warnings, Warning{where,
				fmt.Sprintf("kind %q no soportado; el proveedor queda desactivado", p.Kind)})
		}
	}
	// no fatales: theme inexistente -> "ascua"; default_model que no resuelve
	// se corrige en el Paso 6 contra el catálogo; fps fuera de [1,30] se recorta.
	return nil
}

func Mask(s string) string {
	if s == "" { return "" }
	if len(s) <= 8 { return "…" }
	return "…" + s[len(s)-4:]
}

// Redacted devuelve una copia profunda con todo secreto enmascarado.
// Ninguna ruta de logging a disco debe usar el Config sin pasar por aquí.
func (c *Config) Redacted() *Config
```

#### 1.8 `cmd/ishakat/main.go`

Sin cobra: flag de la stdlib y despacho manual.

```go
var version = "dev" // -X main.version

func main() {
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") {
		switch os.Args[1] {
		case "config":
			os.Exit(cmdConfig(os.Args[2:]))
		case "doctor":
			os.Exit(cmdDoctor())
		case "version":
			fmt.Println("ishakat", version)
			return
		case "models":
			fmt.Fprintln(os.Stderr, "aún no: paso 6")
			os.Exit(1)
		}
	}
	// TUI: paso 3. Por ahora carga la config y la imprime.
}
```

`cmdConfig` implementa los tres verbos. `init` crea `~/.config/ishakat/` con 0700, escribe `config.example.toml` embebido como `config.toml` con 0600, y se niega a sobrescribir si ya existe salvo `--force`. `path` imprime la ruta y nada más, para que sirva en `$(ishakat config path)`. `check` carga, imprime las capas leídas, las advertencias, y sale con código 0 o 1; con `--strict` las advertencias también fallan, que es lo que correrá CI.

`doctor` va ahora aunque esté a medias: imprime versión, plataforma, si detecta Termux, las cuatro rutas XDG, el resolver DNS activo y el resultado de un `net.LookupHost` de prueba. Cuesta cuarenta líneas y es la única herramienta de diagnóstico remoto cuando alguien escriba "no me funciona en mi teléfono".

#### 1.9 Tests que cierran el paso

Cinco en `internal/config/config_test.go`, ninguno necesita red.

El primero carga `config.example.toml` como capa de usuario y exige cero advertencias. Es el que mata la deriva entre `defaults.toml` y el ejemplo.

El segundo prueba el merge por `id`: una capa de proyecto con solo `[[provider]] id = "omniroute"` y `base_url = "http://otro:9999/v1"` debe producir un proveedor con el `base_url` nuevo pero conservando `kind`, `timeout_s` y los `[[provider.model]]` de la capa inferior.

El tercero es una tabla de errores fatales: `[[provider]]` sin `base_url`, sin `id`, con `id` duplicado, `schema = 99`, y TOML roto. Verifica que el mensaje contenga el `id` del proveedor y la palabra `base_url` — no compares el string exacto o el test se rompe cada vez que mejores la redacción.

El cuarto usa `t.Setenv` para probar expansión: variable presente, ausente (proveedor con `AuthOK == false` y `MissingEnv` correcto, pero presente en la lista), y `${LLAVES}`.

El quinto verifica que `Redacted()` no deje ninguna clave completa: recorre el struct redactado buscando el valor original y falla si lo encuentra.

Y el test de frontera arquitectónica en `internal/arch_test.go`, que ya se puede escribir aunque `tui` esté vacío:

```go
func TestTUINoImportaHTTP(t *testing.T) {
	out, _ := exec.Command("go", "list", "-deps", "./internal/tui").Output()
	if bytes.Contains(out, []byte("net/http")) {
		t.Fatal("internal/tui importa net/http: la frontera de §6.1 está rota")
	}
}
```

#### 1.10 Makefile

```makefile
BIN     := ishakat
PKG     := ./cmd/ishakat
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BIN) $(PKG)

test:
	go test ./...

check: test
	go vet ./...
	./bin/$(BIN) config check --strict

android:
	CGO_ENABLED=1 GOOS=android GOARCH=arm64 \
	CC=$(NDK)/toolchains/llvm/prebuilt/linux-x86_64/bin/aarch64-linux-android24-clang \
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BIN)-android-arm64 $(PKG)
```

El target `android` no se usa hasta la Fase 5, pero se deja escrito ahora mientras está fresco el comando que funcionó en el spike.

**Definición de terminado.** `go test ./...` en verde; `ishakat config init` crea el archivo con permisos 0600 en un `$HOME` limpio; `ishakat config check` acepta el ejemplo; borrar la línea `base_url` de un proveedor produce un mensaje que nombra el proveedor y dice qué falta; `ishakat doctor` corre en Termux mostrando rutas correctas. Commit: `feat(config): esquema, carga por capas, expansión y validación`

### Paso 2 · Tipos de conversación y almacén JSONL

Implementar `internal/convo`: `message.go` con los tipos de §4 (puros, sin imports externos salvo `encoding/json` y `time`), `store.go` con el JSONL append-only descrito en §10, y `tokens.go` con el estimador.

El estimador de tokens es heurístico y está bien que lo sea: aproximadamente `len(texto)/4` para latinos, con ajuste por bloques de código, y corrección con el usage real que devuelve el proveedor al terminar cada turno. Guarda el ratio observado por modelo en el caché para que la estimación mejore con el uso. Nunca embarcar un tokenizador real: pesa megas y no cambia ninguna decisión del producto.

`store.go` expone `Append(msg)`, `List()` (lee solo la primera línea de cada archivo), `Load(id)`, `New(title)` y la rotación por `session.keep_last`.

**Cierre:** un test escribe veinte mensajes, los relee y obtiene lo mismo, incluyendo un bloque de imagen y uno de resumen. Un test extra trunca el archivo a la mitad de una línea y verifica que `Load` devuelva los mensajes completos anteriores sin error fatal. Commit: `feat(convo): tipos agnósticos y almacén JSONL`

### Paso 3 · Esqueleto de TUI sin red — la recompensa temprana

Entran las dependencias de Charm. Modo inline, `Root` con los cinco modos de §7.1, textarea de Bubbles, footer, `WindowSizeMsg` con los breakpoints de §9.1, banner con degradado Oklab, `ctrl+c` doble para salir.

Nota de método: este paso está adelantado deliberadamente respecto del orden puramente técnico (lo lógico sería ir tras el catálogo). Va aquí como recompensa visual, porque en un proyecto personal mantener el impulso es un requisito de ingeniería tan real como el rendimiento. Cuesta algo de retrabajo menor y vale la pena.

Sin red, sin engine: el input hace eco de lo que escribes como si fuera respuesta. Es un maniquí, pero un maniquí con la estética final.

**Cierre:** se ve correcto a 40, 60 y 120 columnas, sin parpadeo al redimensionar, y el CPU en reposo es 0%. Commit: `feat(tui): esqueleto inline, breakpoints y banner`

### Paso 4 · Adaptador OpenAI con SSE

`internal/provider/openai`: construcción del request desde `convo.Message`, parser de Server-Sent Events, y traducción a `provider.Event`.

El parser de SSE es donde se esconden los bugs. Trátalo como un `bufio.Scanner` con split function propia que respete líneas `data:`, líneas vacías como separador de evento, y comentarios `:`. Nunca asumas que un `Read` del socket trae un evento completo.

**Cierre:** el test contra un `httptest.Server` que reproduce un stream grabado en `testdata/` cubre cinco casos: stream normal, `[DONE]`, corte a mitad de evento, chunk partido en dos lecturas del socket, y 429 con `Retry-After`. Commit: `feat(provider): dialecto OpenAI con streaming SSE`

### Paso 5 · Modo headless `ishakat -p "hola"`

Pipeline completo —config, proveedor, streaming, persistencia— escupiendo texto a stdout sin una línea de TUI.

Es el paso más subestimado de la lista: da el 60% del sistema probado en CI, sirve para scripting y pipes, y cuando algo falle en la interfaz sabrás de inmediato de qué lado está el bug.

Detalles: si stdin no es TTY, lee el prompt de stdin y lo concatena. Si stdout no es TTY, desactiva todo color. `--json` emite un evento por línea para que se pueda encadenar con `jq`.

**Cierre:** `ishakat -p "di hola" | cat` funciona en Termux. Commit: `feat(app): modo headless`

### Paso 6 · Catálogo

Discovery contra proveedores habilitados, caché atómico con TTL, merge de las tres fuentes según §4.3, cliente de models.dev con `If-None-Match`, catálogo semilla embebido, y el subcomando `ishakat models [--json]`.

**Cierre:** el fixture real de OmniRoute en `testdata/` produce el catálogo esperado; el arranque en frío con la red apagada devuelve el caché sin bloquearse; y con caché ausente y sin red arranca con la semilla. Commit: `feat(catalog): descubrimiento, caché y fusión de tres fuentes`

### Paso 7 · Resolución y matcher difuso

`catalog.Resolve(texto)` con las cuatro etapas de §4.5 y el scoring completo.

Escribe este test antes que la UI del picker. Es el contrato con el requisito central del producto. Tabla mínima de casos: `son45` → `omniroute/anthropic/claude-sonnet-4-5`; `gpt5` → el `gpt-5` correcto y no `gpt-5-nano` si hay ambos; `haiku` → único match por sufijo; `smart` → resuelve por alias; un sufijo ambiguo que debe abrir el picker en vez de adivinar; y una cadena sin ningún match razonable que también abre el picker prefiltrado, nunca un error.

**Cierre:** la tabla completa pasa. Commit: `feat(catalog): resolución por alias, sufijo y difusa`

### Paso 8 · Conectar engine y TUI

`internal/engine` con el `StreamBuf` de §7.3, el turno, los reintentos básicos y la cancelación. El puente con coalescing a 50 ms, el commit a scrollback con `tea.Printf`, el spinner con tiempo transcurrido y contador de tokens, y `esc` que cancela dejando el parcial marcado como `Aborted`.

**Cierre:** cancelas a mitad de una respuesta larga y la app sigue perfectamente usable; el parcial queda en el historial marcado; el CPU vuelve a 0% al terminar el turno. Commit: `feat(engine): turno con streaming coalescido y cancelación`

### Paso 9 · Registro de slash commands

`internal/slash` con el registro como tabla de datos: nombre, alias, descripción, si acepta argumento, y la función. El parser, el dropdown de autocompletado dibujado encima del input (§9.6), y los comandos `/help`, `/clear`, `/new`, `/exit`.

`/help` y el autocompletado se generan de la tabla. Si tienes que tocar dos sitios para agregar un comando, el diseño está mal.

Commit: `feat(slash): registro declarativo y autocompletado`

### Paso 10 · Picker de modelos

Overlay según §9.4: filas de dos líneas, agrupación por proveedor, búsqueda incremental sobre el matcher del Paso 7, `ctrl+f` para ciclar filtros, `ctrl+O` para rotar favoritos, badges de gratis/costo/latencia. Recibe un snapshot del catálogo y no toca la red jamás. Devuelve un único mensaje `modelChosenMsg{Ref string}`.

**Cierre:** `/model` sin argumentos abre; `/model son45` cambia directo con línea de confirmación; `/model son` abre prefiltrado. Commit: `feat(tui): selector de modelos con búsqueda difusa`

### Paso 11 · Cambio en caliente

`engine.CheckSwap` como función pura (§4.6) y el diálogo de conflicto de §9.5.

**Cierre:** el test unitario de "142k tokens hacia una ventana de 128k" ofrece compactar y, aceptando, el siguiente mensaje llega bien al modelo nuevo. Los tres tipos de conflicto tienen test. Commit: `feat(engine): verificación de cambio de modelo en caliente`

### Paso 12 · `/compact` del lado del cliente

Resumen de los turnos antiguos con `compact_model`, conservando `keep_last_turns` íntegros, reemplazando el bloque por un `BlockSummary`, con fallback a `drop-oldest` si el resumen falla. Disparo automático al cruzar `trigger_pct`.

Se hace del lado del cliente a propósito, sin delegar en la compresión del gateway, para que funcione igual contra OmniRoute, contra OpenAI directo o contra lo que sea.

**Cierre:** compactar y seguir conversando mantiene la coherencia; el footer refleja el contexto nuevo; el JSONL conserva el historial completo y el resumen declara qué rangos reemplaza. Commit: `feat(convo): compactación con resumen y fallback`

### Paso 13 · Cierre de la Fase 2

Historial de input navegable con flechas, `/copy` y `ctrl+y` vía `tea.SetClipboard` (OSC52), `/retry`, `/stats`, `ishakat doctor` completo, `ishakat --resume`. Y la pasada de aceptación en Termux desde cero contra la lista de la §11.

Commit: `feat: cierre de fase 2 + tag v0.1.0`

---

## 13. Comandos y atajos definitivos

**Comandos:** `/help`, `/model`, `/models`, `/theme`, `/clear`, `/compact`, `/new`, `/resume`, `/copy`, `/retry`, `/stats`, `/config`, `/debug`, `/exit`.

**Atajos:** `Tab` autocompletar, `Ctrl+P` selector de modelos, `Ctrl+O` rotar favoritos, `Ctrl+T` selector de temas, `Ctrl+J` salto de línea, `Esc` cancelar generación, `Ctrl+C` dos veces para salir, `Ctrl+L` limpiar pantalla, `Ctrl+Y` copiar última respuesta.

**Subcomandos del binario:** `ishakat` (TUI), `ishakat -p "texto"` (headless), `ishakat --resume`, `ishakat models [--json]`, `ishakat config init|path|check`, `ishakat doctor`, `ishakat version`.

---

## 14. Presupuestos de rendimiento

Arranque por debajo de 150 ms con catálogo cacheado. RSS por debajo de 60 MB con una conversación de 50 turnos. Repintado de streaming a 20 fps (intervalo de 50 ms), animaciones a 12 fps o 6 en battery saver. Cero actividad de CPU en reposo. Binario final entre 15 y 25 MB. Cero peticiones de red en el camino crítico del arranque. Cero dependencias que requieran compilación en el dispositivo.

Cada uno de estos números es un test o una verificación manual documentada, no una aspiración.

---

## 15. Riesgos y mitigaciones

El alcance es el enemigo número uno. La tentación de meter herramientas y agentes en la Fase 2 va a ser fuerte, y un chat impecable vale más que un agente a medias. Mitigación: la lista explícita de "fuera de alcance" de cada fase se trata como contrato.

Atarse demasiado a OmniRoute. Se usa como proveedor por defecto, pero desde el primer día se prueba también contra al menos un endpoint OpenAI directo, para que el acoplamiento no se cuele sin que nadie lo note.

La curva de Go si quien implementa no lo conoce: súmense dos semanas al cronograma. Es tiempo bien invertido, pero mejor planeado que sorpresivo.

El DNS de Android, ya descrito, que tiene la propiedad venenosa de esconderse durante semanas. Mitigado forzándolo a la superficie en el Paso 0 y con `ishakat doctor`.

---

## 16. Decisiones abiertas a revisión

`mouse = false` por defecto y el selector con dos líneas por modelo están optimizados para pantalla de celular en vertical. Si el uso principal termina siendo escritorio con terminal ancha, el selector de dos líneas se sentirá desperdiciado y conviene invertir el default. Es fácil de cambiar ahora y molesto después.

---

## 17. Bitácora

Actualizar al cerrar cada paso. Una línea por entrada: fecha, paso, resultado, número medido si aplica.

| Fecha | Paso | Resultado |
|-------|------|-----------|
| 2026-07-30 | Fase 1 | Cerrada. Cuatro contratos definidos. |
| — | Paso 0 · Spike | Completado. Pendiente anotar aquí arranque en ms, RSS y estado del DNS. |
| 2026-07-30 | Paso 1 · Config | Verificado en este entorno: `go build ./...` y `go test ./...` en verde tras instalar Go 1.24 (descarga automática del toolchain 1.26.5 declarado en `go.mod`). Se corrigió `TestLoadExampleNoWarnings`, que dependía de que `config.example.toml` tuviera permisos 0600 en disco; git no preserva el modo completo al clonar, así que el test ahora copia el fixture a un temporal con 0600 explícito antes de cargarlo. |
| 2026-07-30 | Nota de arquitectura | **Divergencia detectada, no corregida por iniciativa propia:** el contrato §4 (modelo de conversación agnóstico `internal/convo`, con `Message.Blocks []Block`) no se implementó. En su lugar existe `internal/session` con `Message.Content string` plano (sin `BlockKind`, sin `Aborted`, sin `Usage` con reasoning/cache). Es funcional para lo hecho hasta ahora (JSONL append-only, paso 2), pero si se construye el engine/TUI sobre esta forma plana, migrar después a bloques (necesario para `/compact` con `BlockSummary`, adjuntos e imágenes, y degradar tool-calls al hacer hotswap) sale más caro. Pendiente decisión explícita: migrar `session`→`convo` con bloques antes del Paso 8, o aceptar formalmente la simplificación y enmendar el contrato de §4. |
| 2026-07-31 | Nota de arquitectura · resuelta (Opción A) | Se migró `internal/session` → `internal/convo` siguiendo el contrato §4 al pie de la letra: `Message.Blocks []Block` con `BlockKind` (text/image/tool_call/tool_result/reasoning/summary), `AppendText`/`AppendReasoning` con coalescing de deltas, `Usage` con reasoning/cache, `Aborted`. `internal/session` fue eliminado. `internal/provider/serialize.go` traduce `convo.Message` al dialecto OpenAI reportando degradaciones en vez de fallar en silencio. También se implementó `internal/theme` (contrato §8): tema TOML embebido (`ascua.toml`), conversión sRGB↔Oklab para degradados perceptuales, `Detect()` de capacidad de color con override por config. |
| 2026-07-31 | Paso 3 · Esqueleto de TUI | Cerrado. `internal/tui` completo sobre Bubble Tea v2 + Lipgloss v2 + Bubbles v2: `Root` con los cinco `Mode` de §7.1 y despacho en dos capas (mensajes/teclas globales → switch de modo); `View()` devuelve `tea.View` con `AltScreen=false` (inline) y cursor real vía `textarea.Cursor()`; breakpoints de §9.1 (`Layout`/`ClassifyBreakpoint`) recalculados en cada `WindowSizeMsg`; banner con degradado Oklab (`theme.Styles.GradientLines`) que solo aparece con TTY, `ui.banner` y alto ≥20; footer de una o dos secciones que se recorta de derecha a izquierda según `ui.footer.items`; caja de input con `textarea.Model` y prefijo de un carácter en BPMinimo; animación tipo Crush (`▚▞▘▝▚▗▘▚▞`) y contador de "pensando" en `ModeBusy`; `esc` y un solo `ctrl+c` cancelan el turno sin salir; doble `ctrl+c` dentro de 1s sí sale (`tea.Quit`); `ctrl+l` limpia el transcript. Sin red y sin engine: el input hace eco de lo escrito, simulando streaming a trozos de 3 runas por `streamTickMsg` para poder ver las transiciones de modo. Frontera de §6.1 verificada: `TestTUINoImportaHTTP` sigue en verde. `go build ./...`, `go vet ./...` y `go test ./...` en verde, incluidos los tests nuevos de `internal/tui` (breakpoints, footer, keymap, banner y transiciones de `Root` sin levantar un `tea.Program`). Pendiente para el cierre visual completo del paso: verificación manual a 40/60/120 columnas en una terminal real y medición de CPU en reposo (verificaciones de §14 que requieren TTY real, no cubiertas en este entorno sandbox). Commit: `feat(tui): root.go + view.go`. |
| 2026-07-31 | Language policy | From this entry onward, all new code, comments, identifiers, commit messages and documentation additions are written in English (see `AGENTS.md`). Pre-existing Spanish content, including the rest of this document, is left as-is and will be migrated later — it is not being retroactively translated as a side effect of unrelated changes. |
| 2026-07-31 | Step 5 · Headless mode | Closed. `internal/app/wiring.go` translates `config.Provider` into `provider.Settings` (`Settings`, `NewProvider`, `FindProvider`, `EnabledProviders`, `SystemPrompt`, `Dialects`) without `internal/app` needing to know the HTTP dialect details. `internal/app/modelref.go` adds `ResolveModel`, a deliberately partial resolver (exact match, config alias with cycle guard, provider/wire_id split on the *first* slash only per §4.2) — the full four-stage §4.5 resolver needs the catalog and is Step 6/7. `internal/app/sink.go` + `internal/app/headless.go` implement the full pipeline: config load → sink selection (plain text vs `--json` one-event-per-line) → prompt assembly (flag + stdin, §Step 5 order rule) → model/provider resolution → session persistence via `convo.Store` (never blocks the response on a save failure) → turn execution with handshake-only retry on `provider.Error.Retryable` (429/5xx honoring `Retry-After`, exponential backoff otherwise) → exit codes 0/1/2/130. `cmd/ishakat/main.go` gained the CLI surface: `-p/--prompt`, `-m/--model`, `--system`, `--json`, `--stream`/`--no-stream`, `--no-save`, `-q/--quiet`, `--config`, proper `-v/--version` (previously dead code, since it lived behind a switch branch only reachable for args *not* starting with `-`), and headless mode auto-activates whenever stdin/stdout isn't a TTY so pipes never try to draw the TUI. Also fixed, as a prerequisite bug found while wiring `session.dir`: `$XDG_DATA_HOME` (and the other three XDG vars) were expanding to `xdg.DataDir()` etc., which already appends the `ishakat` suffix, producing `~/.local/share/ishakat/ishakat/sessions` instead of `~/.local/share/ishakat/sessions`; added `xdg.*Home()` (base, no suffix) and pointed `config/expand.go` at those instead. Covered by `internal/app/modelref_test.go` (alias/cycle/disabled-provider/timeout-override table tests) and `internal/app/headless_test.go` (13 cases against `provider/fake`'s `httptest.Server`: clean stdout, no duplicated trailing newline, `--json` well-formed one-per-line stream, stdin+flag concatenation order, stdin-only prompt, missing-prompt usage error, HTTP error not leaking into stdout, 429 handshake retry, truncated mid-stream keeps the partial response, session JSONL contents, `--no-save`, `--no-stream`, system-prompt precedence, reasoning visibility per `ui.reasoning`). Manually smoke-tested end to end against a local fake SSE server in text mode, `--json` mode, and `doctor`/`-v`. `go build ./...`, `go vet ./...` and `go test ./...` all green. |

---

## 18. Roadmap post-1.0

Llamadas a herramientas, MCP, sesiones con `/resume` enriquecido, sistema de plugins.

---

*Fin del documento.*

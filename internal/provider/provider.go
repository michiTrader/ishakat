// Package provider define el contrato 3bis del PLAN (§5.4): la única
// interfaz por la que ishakat habla con un servicio de inferencia.
//
// La regla de §6.1 se cumple en las dos direcciones:
//
//   - provider no sabe qué es un color: no importa lipgloss ni el tema.
//   - provider no sabe qué es la configuración: recibe Settings, que
//     internal/app rellena a partir de config.Provider.
//
// Lo único que cruza la frontera hacia arriba son convo.Message (entrada) y
// provider.Event (salida). Los dialectos concretos viven en subpaquetes
// (provider/openai) y se registran solos con Register.
package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/MichiTrader/ishakat/internal/convo"
)

// Provider es el contrato de §5.4. Tres métodos, ni uno más: identidad,
// descubrimiento de modelos y streaming de un turno.
type Provider interface {
	// ID es el identificador corto del proveedor tal como aparece en la
	// configuración y en las referencias de modelo ("omniroute/…").
	ID() string

	// Discover lista los modelos que el servicio declara. El catálogo
	// (Paso 6) normaliza y fusiona este resultado; aquí se devuelve casi
	// crudo a propósito.
	Discover(ctx context.Context) ([]RawModel, error)

	// Stream ejecuta un turno. Devuelve error solo si falla el handshake
	// (construcción del request, red, o estado HTTP distinto de 200): eso
	// permite que el engine reintente antes de haber mostrado nada. Una vez
	// devuelto el canal, todo lo demás —incluidos los errores de mitad de
	// stream— llega como Event.
	Stream(ctx context.Context, req Request) (<-chan Event, error)
}

// EventKind clasifica lo que sale del stream.
//
// Nota de contrato: §5.4 enumera Delta, ToolCall, Usage, Done y Error. Se
// añaden dos, documentadas aquí porque el resto del sistema ya las necesita:
//
//   - EventReasoning: convo.Message distingue BlockReasoning de BlockText y
//     ui.reasoning decide si se muestra; aplanar el razonamiento dentro de
//     EventDelta obligaría a la TUI a adivinar dónde empieza la respuesta.
//   - EventWarning: la degradación de §4.6 (imágenes o herramientas que el
//     modelo de destino no soporta) tiene que llegar a la interfaz como
//     aviso honesto, no como texto del asistente.
type EventKind int

const (
	EventDelta EventKind = iota
	EventReasoning
	EventToolCall
	EventUsage
	EventWarning
	EventDone
	EventError
)

var eventKindNames = map[EventKind]string{
	EventDelta:     "delta",
	EventReasoning: "reasoning",
	EventToolCall:  "tool_call",
	EventUsage:     "usage",
	EventWarning:   "warning",
	EventDone:      "done",
	EventError:     "error",
}

func (k EventKind) String() string {
	if s, ok := eventKindNames[k]; ok {
		return s
	}
	return "desconocido"
}

// Event es la unidad que viaja por el canal del stream.
//
// Garantías del canal, iguales para todos los dialectos:
//
//  1. EventDone es siempre el último evento y llega exactamente una vez.
//  2. Si hubo un fallo a mitad de stream, EventError llega justo antes de
//     EventDone; nunca reemplaza al terminador.
//  3. El canal se cierra después de EventDone.
//
// Esas tres reglas son lo que permite que el engine (§7.3) tenga un único
// camino de cierre de turno.
type Event struct {
	Kind EventKind

	// Text lleva el delta de EventDelta y EventReasoning, y el mensaje
	// legible de EventWarning.
	Text string

	// Name y Args describen una llamada a herramienta (EventToolCall). Args
	// va cruda porque su forma la define la herramienta, no provider.
	Name string
	Args json.RawMessage

	// Usage viene en EventUsage y, si el servicio lo manda tarde, también
	// puede venir adosado a EventDone.
	Usage *convo.Usage

	// Err solo se rellena en EventError.
	Err error
}

// Request es un turno a punto de enviarse. Messages es el historial completo
// en el formato agnóstico de convo: traducirlo al dialecto del servicio es
// responsabilidad del adaptador, y es el único lugar del programa donde el
// historial cambia de forma (§4).
type Request struct {
	// Model es el identificador de cable (wire ID) del modelo, ya resuelto
	// por el catálogo. Nunca un alias ni una referencia con proveedor.
	Model string

	// Messages es el historial. El prompt de sistema puede venir aquí como
	// primer mensaje o aparte en System; si viene en los dos, System gana y
	// se inserta al principio.
	Messages []convo.Message
	System   string

	// Caps son las capacidades del modelo de destino, según el catálogo.
	// El cero-valor significa "solo texto", que siempre funciona.
	Caps Caps

	// Stream en falso pide una respuesta única en vez de SSE (app.stream =
	// false). El canal de eventos es el mismo en los dos casos: un delta con
	// todo el texto, el usage y done.
	Stream bool

	Temperature *float64
	MaxTokens   *int

	// Params son overrides crudos que se mezclan en el cuerpo JSON justo
	// antes de enviarlo. Vienen de [provider.params] y de [[provider.model]]:
	// es la vía para hablar con un servicio que pide un campo que ishakat no
	// conoce, sin tocar código (§5.2).
	Params map[string]any
}

// RawModel es lo que un servicio dice de un modelo, antes de que el catálogo
// lo normalice. Raw se conserva porque los gateways meten campos propios que
// el Paso 6 sí sabe leer (precio, familia, límites).
type RawModel struct {
	WireID  string
	Name    string
	Context int
	Output  int
	Tags    []string
	Raw     json.RawMessage
}

// Settings es la configuración de un proveedor traducida a lo que el
// adaptador necesita. internal/app la construye desde config.Provider; así
// provider no importa config y se puede instanciar en un test con tres
// líneas.
type Settings struct {
	ID      string
	Name    string
	Kind    string
	BaseURL string
	WireAPI string

	// APIKey ya expandida (config resuelve ${env:VAR}). Vacía significa
	// "servicio local sin autenticación", como Ollama o LM Studio.
	APIKey string

	Headers map[string]string
	Params  map[string]any

	Timeout        time.Duration
	ConnectTimeout time.Duration

	// HTTPClient permite inyectar un cliente ya configurado —o el de un
	// httptest.Server— en vez del que arma el adaptador.
	HTTPClient *http.Client

	// UserAgent identifica a ishakat ante el servicio.
	UserAgent string
}

// Caps describe qué sabe hacer el modelo de destino. El catálogo (Paso 6) las
// rellena; el cero-valor significa "solo texto", que es el mínimo común
// denominador y siempre funciona.
type Caps struct {
	Images    bool
	Tools     bool
	Reasoning bool
}

// Degradation describe qué se perdió al serializar el historial. La UI lo usa
// para avisar con honestidad en vez de mandar una petición que el modelo va a
// rechazar (§4.6).
type Degradation struct {
	ImagesDropped    int
	ToolsFlattened   int
	ReasoningDropped int
}

// Any indica si hubo alguna pérdida.
func (d Degradation) Any() bool {
	return d.ImagesDropped > 0 || d.ToolsFlattened > 0 || d.ReasoningDropped > 0
}

// Reason devuelve una explicación en una línea, para la línea de aviso de la
// UI. El razonamiento omitido no se reporta: no se reenvía nunca y contarlo
// como pérdida solo asustaría al usuario sin motivo.
func (d Degradation) Reason() string {
	var parts []string
	if d.ImagesDropped > 0 {
		parts = append(parts, plural(d.ImagesDropped, "imagen", "imágenes")+" sin enviar")
	}
	if d.ToolsFlattened > 0 {
		parts = append(parts, plural(d.ToolsFlattened, "llamada a herramienta", "llamadas a herramientas")+" convertidas a texto")
	}
	if len(parts) == 0 {
		return ""
	}
	return join(parts, ", ")
}

func plural(n int, one, many string) string {
	w := many
	if n == 1 {
		w = one
	}
	return fmt.Sprintf("%d %s", n, w)
}

func join(parts []string, sep string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += sep
		}
		out += p
	}
	return out
}

// Errores centinela del paquete. El engine los distingue para decidir entre
// reintentar, avisar o guardar el parcial como turno abortado (§7.4).
var (
	// ErrStreamTruncated es un stream que se cortó sin [DONE]: se conserva
	// lo recibido y se avisa. No es lo mismo que una cancelación del
	// usuario, que no produce error.
	ErrStreamTruncated = errors.New("provider: el stream se cortó antes de terminar")

	// ErrNoAPIKey is a provider that requires a key and doesn't have one.
	ErrNoAPIKey = errors.New("provider: missing API key")

	// ErrUnknownKind es un kind que no tiene adaptador registrado.
	ErrUnknownKind = errors.New("provider: kind desconocido")
)

// Error es un fallo con la respuesta HTTP del servicio ya interpretada. Lo
// importante no es el texto sino Retryable y RetryAfter: de eso depende la
// política de reintentos del engine (retry.go, Paso 8).
type Error struct {
	Provider   string
	Status     int
	Code       string
	Message    string
	RetryAfter time.Duration
	Retryable  bool
	Err        error
}

func (e *Error) Error() string {
	switch {
	case e == nil:
		return "<nil>"
	case e.Status == 0 && e.Err != nil:
		return fmt.Sprintf("%s: %v", e.Provider, e.Err)
	case e.Message != "":
		return fmt.Sprintf("%s: HTTP %d: %s", e.Provider, e.Status, e.Message)
	default:
		return fmt.Sprintf("%s: HTTP %d", e.Provider, e.Status)
	}
}

func (e *Error) Unwrap() error { return e.Err }

// Retry es el engranaje estructural del Paso 8: internal/engine define una
// interfaz retryHint con esta misma firma y la detecta vía errors.As, sin
// importar nunca este paquete (que trae net/http, prohibido en la frontera
// de internal/tui). El método se llama Retry, no Retryable/RetryAfter,
// porque un método no puede compartir nombre con un campo del mismo tipo.
func (e *Error) Retry() (wait time.Duration, retryable bool) {
	if e == nil {
		return 0, false
	}
	return e.RetryAfter, e.Retryable
}

// Temporary conserva la convención de net.Error para que el engine pueda
// tratar de la misma forma un 503 y un socket caído.
func (e *Error) Temporary() bool { return e != nil && e.Retryable }

// RetryableStatus decide si vale la pena reintentar un estado HTTP.
//
// 429 y 5xx sí. 408, 409 y 425 son cortesías del protocolo que algunos
// gateways usan. 401, 403 y 404 no: reintentar una clave inválida solo gasta
// batería y puede bloquear la cuenta.
func RetryableStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusConflict, http.StatusTooEarly,
		http.StatusTooManyRequests:
		return true
	}
	return status >= 500 && status <= 599
}

// ParseRetryAfter interpreta la cabecera Retry-After en sus dos formas
// legales: segundos ("2") o fecha HTTP. Devuelve 0 si no se entiende, nunca
// un error: una cabecera rara no debe tumbar un turno.
func ParseRetryAfter(v string, now time.Time) time.Duration {
	if v == "" {
		return 0
	}
	var secs float64
	if _, err := fmt.Sscanf(v, "%f", &secs); err == nil && secs >= 0 {
		// Sscanf acepta "2abc"; se descarta ese caso comprobando que la
		// cadena sea solo número.
		if isNumeric(v) {
			return time.Duration(secs * float64(time.Second))
		}
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := t.Sub(now); d > 0 {
			return d
		}
		return 0
	}
	return 0
}

func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	dot := false
	for i, r := range s {
		switch {
		case r >= '0' && r <= '9':
		case r == '.' && !dot && i > 0:
			dot = true
		default:
			return false
		}
	}
	return true
}

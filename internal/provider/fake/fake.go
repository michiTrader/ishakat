// Package fake es el proveedor de pruebas del árbol de §6.2: un
// provider.Provider que no toca la red y un httptest.Server que sirve un
// stream SSE grabado.
//
// Existe para que los pasos siguientes se puedan probar sin inventar cada vez
// el mismo andamio: el engine (Paso 8) necesita un proveedor que emita eventos
// controlados, y el modo headless (Paso 5) necesita un servidor que hable el
// dialecto de verdad. Uno cubre la lógica de arriba, el otro el cable.
//
// No se importa desde el binario: solo desde tests.
package fake

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	"github.com/MichiTrader/ishakat/internal/convo"
	"github.com/MichiTrader/ishakat/internal/provider"
)

// Provider implementa provider.Provider emitiendo una lista de eventos ya
// decidida. Es determinista a propósito: un test que depende del ritmo real de
// un modelo no es un test.
type Provider struct {
	id string

	// Script son los eventos que emite Stream, en orden. No hace falta incluir
	// EventDone: se añade solo, para que el canal cumpla la misma garantía que
	// los proveedores de verdad.
	Script []provider.Event

	// Delay se espera entre evento y evento. Con 0 el turno entero llega antes
	// del primer tick de la TUI, que es justo lo que hace falta para probar el
	// coalescing de §7.3.
	Delay time.Duration

	// HandshakeErr, si no es nil, es lo que devuelve Stream en vez de un canal.
	// Sirve para probar la política de reintentos con un 429 sintético.
	HandshakeErr error

	// Models es lo que devuelve Discover, y DiscoverErr tiene prioridad.
	Models      []provider.RawModel
	DiscoverErr error

	mu    sync.Mutex
	turns []provider.Request
}

// New construye un proveedor falso con los eventos dados.
func New(id string, script ...provider.Event) *Provider {
	return &Provider{id: id, Script: script}
}

// Text es el atajo más usado: un proveedor que responde con estos trozos de
// texto y termina.
func Text(id string, chunks ...string) *Provider {
	p := &Provider{id: id}
	for _, c := range chunks {
		p.Script = append(p.Script, provider.Event{Kind: provider.EventDelta, Text: c})
	}
	return p
}

// ID implementa provider.Provider.
func (p *Provider) ID() string { return p.id }

// Discover implementa provider.Provider.
func (p *Provider) Discover(ctx context.Context) ([]provider.RawModel, error) {
	if p.DiscoverErr != nil {
		return nil, p.DiscoverErr
	}
	return p.Models, nil
}

// Stream emite el guion y cierra, respetando las tres garantías del canal:
// EventDone al final, una sola vez, y canal cerrado después.
func (p *Provider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	if p.HandshakeErr != nil {
		return nil, p.HandshakeErr
	}

	p.mu.Lock()
	p.turns = append(p.turns, req)
	p.mu.Unlock()

	ch := make(chan provider.Event, len(p.Script)+1)
	go func() {
		defer close(ch)
		for _, ev := range p.Script {
			if p.Delay > 0 {
				select {
				case <-time.After(p.Delay):
				case <-ctx.Done():
					return
				}
			}
			select {
			case ch <- ev:
			case <-ctx.Done():
				return
			}
		}
		select {
		case ch <- provider.Event{Kind: provider.EventDone}:
		case <-ctx.Done():
		}
	}()
	return ch, nil
}

// Turns devuelve los turnos recibidos. Un test de hotswap (Paso 11) necesita
// comprobar que el historial que llegó al proveedor nuevo es el completo.
func (p *Provider) Turns() []provider.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]provider.Request, len(p.turns))
	copy(out, p.turns)
	return out
}

// LastTurn devuelve el último turno recibido, o el cero-valor si no hubo.
func (p *Provider) LastTurn() provider.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.turns) == 0 {
		return provider.Request{}
	}
	return p.turns[len(p.turns)-1]
}

// Usage construye un evento de consumo, para no repetir el literal en cada
// test.
func Usage(in, out int) provider.Event {
	return provider.Event{Kind: provider.EventUsage, Usage: &convo.Usage{In: in, Out: out}}
}

// ─────────────────────────────────────────────────────────────
// servidor SSE
// ─────────────────────────────────────────────────────────────

// SSEOptions configura el servidor de streaming falso.
type SSEOptions struct {
	// Chunks son los trozos de cuerpo tal como se escriben en el socket, con
	// un Flush entre cada uno. Pueden cortar un evento por la mitad: es la
	// forma de reproducir lo que hace la red de verdad.
	Chunks []string

	// Status distinto de 0 y de 200 hace que el servidor responda ese estado
	// con Body como cuerpo, sin stream.
	Status int
	Body   string

	// RetryAfter se manda como cabecera cuando Status lo justifica.
	RetryAfter string

	// Pause se espera entre trozo y trozo, para simular un modelo lento.
	Pause time.Duration

	// Models es la respuesta de GET /models.
	Models string

	// OnRequest recibe cada petición para poder inspeccionarla.
	OnRequest func(*http.Request, []byte)
}

// SSEServer levanta un httptest.Server que habla el dialecto OpenAI. El
// llamador debe cerrarlo con Close, o pasarlo por t.Cleanup.
func SSEServer(opt SSEOptions) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body []byte
		if r.Body != nil {
			buf := make([]byte, 0, 1024)
			tmp := make([]byte, 512)
			for {
				n, err := r.Body.Read(tmp)
				buf = append(buf, tmp[:n]...)
				if err != nil {
					break
				}
			}
			body = buf
			_ = r.Body.Close()
		}
		if opt.OnRequest != nil {
			opt.OnRequest(r, body)
		}

		if r.URL.Path == "/models" {
			w.Header().Set("Content-Type", "application/json")
			if opt.Models == "" {
				_, _ = fmt.Fprint(w, `{"object":"list","data":[]}`)
				return
			}
			_, _ = fmt.Fprint(w, opt.Models)
			return
		}

		if opt.Status != 0 && opt.Status != http.StatusOK {
			if opt.RetryAfter != "" {
				w.Header().Set("Retry-After", opt.RetryAfter)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(opt.Status)
			_, _ = fmt.Fprint(w, opt.Body)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		for _, c := range opt.Chunks {
			if _, err := w.Write([]byte(c)); err != nil {
				return
			}
			if fl != nil {
				fl.Flush()
			}
			if opt.Pause > 0 {
				select {
				case <-time.After(opt.Pause):
				case <-r.Context().Done():
					return
				}
			}
		}
	}))
}

// SSEChunk envuelve un JSON en un evento SSE completo, con la línea vacía
// final. Escribir eso a mano en cada test es la fuente número uno de fixtures
// mal formados.
func SSEChunk(json string) string { return "data: " + json + "\n\n" }

// SSEDelta es un chunk de streaming con un delta de texto.
func SSEDelta(text string) string {
	return SSEChunk(fmt.Sprintf(`{"id":"fake","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":%s}}]}`, quote(text)))
}

// SSEDone es el terminador del protocolo.
func SSEDone() string { return "data: [DONE]\n\n" }

// quote escapa una cadena como literal JSON.
func quote(s string) string {
	out := make([]byte, 0, len(s)+2)
	out = append(out, '"')
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '"', '\\':
			out = append(out, '\\', c)
		case '\n':
			out = append(out, '\\', 'n')
		case '\r':
			out = append(out, '\\', 'r')
		case '\t':
			out = append(out, '\\', 't')
		default:
			if c < 0x20 {
				out = append(out, []byte(fmt.Sprintf(`\u%04x`, c))...)
				continue
			}
			out = append(out, c)
		}
	}
	return string(append(out, '"'))
}

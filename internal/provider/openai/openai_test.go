package openai_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichiTrader/ishakat/internal/convo"
	"github.com/MichiTrader/ishakat/internal/provider"
	"github.com/MichiTrader/ishakat/internal/provider/openai"
)

// Los cinco casos que cierran el Paso 4 son, en este archivo:
//
//	1. stream normal ................ TestStreamNormal
//	2. [DONE] ....................... TestStreamDoneCierraEIgnoraElResto
//	3. corte a mitad de evento ...... TestStreamCorteAMitadDeEvento
//	4. chunk partido en dos lecturas  TestStreamChunkPartidoEnDosLecturas
//	5. 429 con Retry-After .......... TestStream429ConRetryAfter
//
// El resto son los que aparecieron al escribirlos: cancelación, error con
// estado 200, modo no-streaming y descubrimiento de modelos.

// ─────────────────────────────────────────────────────────────
// utilidades
// ─────────────────────────────────────────────────────────────

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("no se pudo leer el fixture %s: %v", name, err)
	}
	return string(b)
}

// sseServer sirve los trozos dados en el orden dado, haciendo Flush entre
// cada uno. Los trozos NO tienen que coincidir con los límites de los eventos:
// ahí está la gracia.
func sseServer(t *testing.T, chunks []string, inspect func(*http.Request, []byte)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if inspect != nil {
			body := readAll(t, r)
			inspect(r, body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		for _, c := range chunks {
			if _, err := w.Write([]byte(c)); err != nil {
				return
			}
			if fl != nil {
				fl.Flush()
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func readAll(t *testing.T, r *http.Request) []byte {
	t.Helper()
	defer r.Body.Close()
	b := make([]byte, 0, 1024)
	buf := make([]byte, 512)
	for {
		n, err := r.Body.Read(buf)
		b = append(b, buf[:n]...)
		if err != nil {
			return b
		}
	}
}

func newProvider(t *testing.T, url string, mut ...func(*provider.Settings)) provider.Provider {
	t.Helper()
	s := provider.Settings{
		ID:      "omniroute",
		Kind:    "openai",
		BaseURL: url,
		APIKey:  "sk-test",
		Headers: map[string]string{"X-Title": "ishakat"},
	}
	for _, m := range mut {
		m(&s)
	}
	p, err := provider.New(s) // pasa por el registro: prueba también el init()
	if err != nil {
		t.Fatalf("no se pudo construir el proveedor: %v", err)
	}
	return p
}

func hola() provider.Request {
	return provider.Request{
		Model:    "anthropic/claude-sonnet-4-5",
		Messages: []convo.Message{convo.User("hola")},
		Stream:   true,
	}
}

// drained es el resultado de consumir un canal de eventos completo.
type drained struct {
	text      string
	reasoning string
	warnings  []string
	tools     []provider.Event
	usage     *convo.Usage
	err       error
	dones     int
	kinds     []provider.EventKind
}

func drain(t *testing.T, ch <-chan provider.Event) drained {
	t.Helper()
	var d drained
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return d
			}
			d.kinds = append(d.kinds, ev.Kind)
			switch ev.Kind {
			case provider.EventDelta:
				d.text += ev.Text
			case provider.EventReasoning:
				d.reasoning += ev.Text
			case provider.EventWarning:
				d.warnings = append(d.warnings, ev.Text)
			case provider.EventToolCall:
				d.tools = append(d.tools, ev)
			case provider.EventUsage:
				d.usage = ev.Usage
			case provider.EventError:
				d.err = ev.Err
			case provider.EventDone:
				d.dones++
			}
		case <-deadline:
			t.Fatal("el canal de eventos no terminó en 5 s: hay una goroutine colgada")
		}
	}
}

// ─────────────────────────────────────────────────────────────
// caso 1 · stream normal
// ─────────────────────────────────────────────────────────────

func TestStreamNormal(t *testing.T) {
	var gotPath, gotAuth, gotAccept, gotTitle string
	var gotBody map[string]any

	srv := sseServer(t, []string{fixture(t, "stream_normal.sse")}, func(r *http.Request, body []byte) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		gotTitle = r.Header.Get("X-Title")
		_ = json.Unmarshal(body, &gotBody)
	})

	p := newProvider(t, srv.URL)
	ch, err := p.Stream(context.Background(), hola())
	if err != nil {
		t.Fatalf("el handshake debía funcionar: %v", err)
	}
	d := drain(t, ch)

	// Petición
	if gotPath != "/chat/completions" {
		t.Errorf("path equivocado: %q", gotPath)
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("Authorization equivocado: %q", gotAuth)
	}
	if gotAccept != "text/event-stream" {
		t.Errorf("Accept equivocado: %q", gotAccept)
	}
	if gotTitle != "ishakat" {
		t.Errorf("las cabeceras de la config deben viajar: %q", gotTitle)
	}
	if gotBody["model"] != "anthropic/claude-sonnet-4-5" || gotBody["stream"] != true {
		t.Errorf("cuerpo equivocado: %+v", gotBody)
	}
	if so, ok := gotBody["stream_options"].(map[string]any); !ok || so["include_usage"] != true {
		t.Errorf("hay que pedir el usage en el último chunk: %+v", gotBody["stream_options"])
	}

	// Respuesta
	if d.text != "Hola, ishakat en línea." {
		t.Errorf("texto mal ensamblado: %q", d.text)
	}
	if d.reasoning != "El usuario saluda. Respondo corto." {
		t.Errorf("razonamiento mal ensamblado: %q", d.reasoning)
	}
	if d.err != nil {
		t.Errorf("un stream normal no produce error: %v", d.err)
	}
	if d.dones != 1 {
		t.Errorf("EventDone debe llegar exactamente una vez, llegó %d", d.dones)
	}
	if d.kinds[len(d.kinds)-1] != provider.EventDone {
		t.Errorf("EventDone debe ser el último evento: %v", d.kinds)
	}

	// Usage: 1240 prompt con 1024 cacheados y 68 de salida con 12 de
	// razonamiento. In y Out se descuentan para que el total no se infle.
	if d.usage == nil {
		t.Fatal("el usage del último chunk se perdió")
	}
	if got := *d.usage; got.In != 216 || got.Out != 56 || got.CacheRead != 1024 || got.Reasoning != 12 {
		t.Errorf("usage mal traducido: %+v", got)
	}
	if d.usage.Total() != 1308 {
		t.Errorf("el total debe coincidir con total_tokens: %d", d.usage.Total())
	}
}

// ─────────────────────────────────────────────────────────────
// caso 2 · [DONE]
// ─────────────────────────────────────────────────────────────

func TestStreamDoneCierraEIgnoraElResto(t *testing.T) {
	chunks := []string{
		`data: {"choices":[{"delta":{"content":"visible"}}]}` + "\n\n",
		"data: [DONE]\n\n",
		`data: {"choices":[{"delta":{"content":" INVISIBLE"}}]}` + "\n\n",
	}
	srv := sseServer(t, chunks, nil)

	p := newProvider(t, srv.URL)
	ch, err := p.Stream(context.Background(), hola())
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	d := drain(t, ch)

	if d.text != "visible" {
		t.Errorf("todo lo posterior a [DONE] se ignora: %q", d.text)
	}
	if d.err != nil {
		t.Errorf("[DONE] es un cierre limpio, no un error: %v", d.err)
	}
	if d.dones != 1 {
		t.Errorf("un solo EventDone, hubo %d", d.dones)
	}
}

// ─────────────────────────────────────────────────────────────
// caso 3 · corte a mitad de evento
// ─────────────────────────────────────────────────────────────

func TestStreamCorteAMitadDeEvento(t *testing.T) {
	srv := sseServer(t, []string{fixture(t, "stream_truncado.sse")}, nil)

	p := newProvider(t, srv.URL)
	ch, err := p.Stream(context.Background(), hola())
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	d := drain(t, ch)

	// Lo recibido se conserva: es lo que el usuario ya vio en pantalla y lo
	// que se guardará como turno parcial.
	if d.text != "Voy a explicarlo en tres pasos" {
		t.Errorf("el parcial completo debe conservarse: %q", d.text)
	}
	if !errors.Is(d.err, provider.ErrStreamTruncated) {
		t.Errorf("un corte debe reportar ErrStreamTruncated, dio %v", d.err)
	}
	if d.dones != 1 {
		t.Errorf("EventDone llega igual tras el error, hubo %d", d.dones)
	}
	if d.kinds[len(d.kinds)-1] != provider.EventDone || d.kinds[len(d.kinds)-2] != provider.EventError {
		t.Errorf("el orden debe ser …EventError, EventDone: %v", d.kinds)
	}
}

func TestStreamSinDoneFinalTambienEsCorte(t *testing.T) {
	// Eventos completos pero sin [DONE]: la conexión se cayó justo después de
	// un chunk bien formado. Es un corte igual, y el parcial se conserva.
	chunks := []string{`data: {"choices":[{"delta":{"content":"medio"}}]}` + "\n\n"}
	srv := sseServer(t, chunks, nil)

	p := newProvider(t, srv.URL)
	ch, err := p.Stream(context.Background(), hola())
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	d := drain(t, ch)

	if d.text != "medio" {
		t.Errorf("parcial perdido: %q", d.text)
	}
	if !errors.Is(d.err, provider.ErrStreamTruncated) {
		t.Errorf("se esperaba ErrStreamTruncated, dio %v", d.err)
	}
}

// ─────────────────────────────────────────────────────────────
// caso 4 · chunk partido en dos lecturas del socket
// ─────────────────────────────────────────────────────────────

func TestStreamChunkPartidoEnDosLecturas(t *testing.T) {
	// El fixture entero, pero cortado en dos escrituras con Flush en medio, en
	// un punto que cae dentro del JSON de un evento. Si el parser asumiera que
	// cada lectura trae eventos completos, aquí se perdería texto o se
	// generaría un JSON inválido.
	whole := fixture(t, "stream_normal.sse")
	cut := strings.Index(whole, `"content":"ishakat"`) + 8
	if cut <= 8 {
		t.Fatal("el fixture cambió: no se encontró el punto de corte")
	}
	srv := sseServer(t, []string{whole[:cut], whole[cut:]}, nil)

	p := newProvider(t, srv.URL)
	ch, err := p.Stream(context.Background(), hola())
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	d := drain(t, ch)

	if d.text != "Hola, ishakat en línea." {
		t.Errorf("el corte a mitad de JSON rompió el texto: %q", d.text)
	}
	if d.err != nil {
		t.Errorf("partir el flujo no es un error: %v", d.err)
	}
	if d.usage == nil || d.usage.CacheRead != 1024 {
		t.Errorf("el usage debe llegar igual: %+v", d.usage)
	}
}

func TestStreamByteAByte(t *testing.T) {
	// El extremo del caso anterior: una lectura por byte. Lento pero
	// definitivo.
	whole := fixture(t, "stream_normal.sse")
	chunks := make([]string, 0, len(whole))
	for i := 0; i < len(whole); i++ {
		chunks = append(chunks, whole[i:i+1])
	}
	srv := sseServer(t, chunks, nil)

	p := newProvider(t, srv.URL)
	ch, err := p.Stream(context.Background(), hola())
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	d := drain(t, ch)
	if d.text != "Hola, ishakat en línea." {
		t.Errorf("texto mal ensamblado byte a byte: %q", d.text)
	}
}

// ─────────────────────────────────────────────────────────────
// caso 5 · 429 con Retry-After
// ─────────────────────────────────────────────────────────────

func TestStream429ConRetryAfter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "2")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limit exceeded","type":"rate_limit_error","code":"rate_limited"}}`))
	}))
	defer srv.Close()

	p := newProvider(t, srv.URL)
	ch, err := p.Stream(context.Background(), hola())
	if ch != nil {
		t.Error("un 429 no debe devolver canal: el turno no empezó")
	}
	if err == nil {
		t.Fatal("un 429 debe ser error del handshake")
	}

	var pe *provider.Error
	if !errors.As(err, &pe) {
		t.Fatalf("se esperaba *provider.Error, dio %T: %v", err, err)
	}
	if pe.Status != http.StatusTooManyRequests {
		t.Errorf("estado mal propagado: %d", pe.Status)
	}
	if !pe.Retryable || !pe.Temporary() {
		t.Error("un 429 es reintentable")
	}
	if pe.RetryAfter != 2*time.Second {
		t.Errorf("Retry-After mal interpretado: %v", pe.RetryAfter)
	}
	if pe.Code != "rate_limited" || !strings.Contains(pe.Message, "rate limit") {
		t.Errorf("el error del cuerpo debe llegar legible: %+v", pe)
	}
	if !strings.Contains(pe.Error(), "omniroute") {
		t.Errorf("el mensaje debe decir de qué proveedor es: %q", pe.Error())
	}
}

func TestStream401NoEsReintentable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	}))
	defer srv.Close()

	p := newProvider(t, srv.URL)
	_, err := p.Stream(context.Background(), hola())
	var pe *provider.Error
	if !errors.As(err, &pe) {
		t.Fatalf("se esperaba *provider.Error: %v", err)
	}
	if pe.Retryable {
		t.Error("reintentar una clave inválida solo gasta batería")
	}
}

func TestStream401SinClaveExplicaQueFaltaLaClave(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	p := newProvider(t, srv.URL, func(s *provider.Settings) { s.APIKey = "" })
	_, err := p.Stream(context.Background(), hola())
	if !errors.Is(err, provider.ErrNoAPIKey) {
		t.Fatalf("se esperaba ErrNoAPIKey, dio %v", err)
	}
	if !strings.Contains(err.Error(), "api_key") {
		t.Errorf("el mensaje debe decir qué falta configurar: %q", err.Error())
	}
}

func TestStreamRetryAfterConFecha(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", time.Now().Add(30*time.Second).UTC().Format(http.TimeFormat))
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	p := newProvider(t, srv.URL)
	_, err := p.Stream(context.Background(), hola())
	var pe *provider.Error
	if !errors.As(err, &pe) {
		t.Fatalf("se esperaba *provider.Error: %v", err)
	}
	if pe.RetryAfter < 25*time.Second || pe.RetryAfter > 31*time.Second {
		t.Errorf("Retry-After con fecha HTTP mal interpretado: %v", pe.RetryAfter)
	}
	if !pe.Retryable {
		t.Error("un 503 es reintentable")
	}
}

// ─────────────────────────────────────────────────────────────
// los que aparecieron al escribir los cinco
// ─────────────────────────────────────────────────────────────

func TestStreamCancelacionNoEsError(t *testing.T) {
	// El servidor manda un chunk y se queda callado hasta que lo cancelen:
	// reproduce a alguien pulsando esc a mitad de una respuesta larga (§7.4).
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"content":"escribiendo"}}]}` + "\n\n"))
		if fl != nil {
			fl.Flush()
		}
		select {
		case <-release:
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	defer srv.Close()
	defer close(release)

	ctx, cancel := context.WithCancel(context.Background())
	p := newProvider(t, srv.URL)
	ch, err := p.Stream(ctx, hola())
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}

	// Esperar el primer delta y cancelar.
	ev := <-ch
	if ev.Kind != provider.EventDelta || ev.Text != "escribiendo" {
		t.Fatalf("primer evento inesperado: %+v", ev)
	}
	cancel()

	// La garantía que importa: el canal se cierra, sin colgarse y sin
	// convertir la cancelación del usuario en un error rojo.
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if ev.Kind == provider.EventError {
				t.Fatalf("cancelar no es fallar: %v", ev.Err)
			}
		case <-deadline:
			t.Fatal("el canal no se cerró tras cancelar: goroutine colgada")
		}
	}
}

func TestStreamErrorConEstado200(t *testing.T) {
	// El formato más traicionero: 200 OK y el error dentro del stream. Sin
	// tratarlo, el usuario ve un turno vacío y ninguna explicación.
	chunks := []string{
		`data: {"choices":[{"delta":{"content":"empiezo"}}]}` + "\n\n",
		`data: {"error":{"message":"upstream se cayó","code":"upstream_error"}}` + "\n\n",
	}
	srv := sseServer(t, chunks, nil)

	p := newProvider(t, srv.URL)
	ch, err := p.Stream(context.Background(), hola())
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	d := drain(t, ch)

	if d.text != "empiezo" {
		t.Errorf("lo recibido antes del error se conserva: %q", d.text)
	}
	var pe *provider.Error
	if !errors.As(d.err, &pe) || !strings.Contains(pe.Message, "upstream") {
		t.Fatalf("el error del cuerpo debe llegar como *provider.Error: %v", d.err)
	}
	if d.dones != 1 {
		t.Errorf("EventDone llega igual, hubo %d", d.dones)
	}
}

func TestStreamAvisaDeLaDegradacion(t *testing.T) {
	srv := sseServer(t, []string{"data: [DONE]\n\n"}, nil)
	p := newProvider(t, srv.URL)

	req := hola()
	req.Messages = []convo.Message{
		convo.NewMessage(convo.RoleUser,
			convo.TextBlock("mirá esto"),
			convo.ImageBlock("image/png", []byte("x"), "captura.png"),
		),
	}
	ch, err := p.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	d := drain(t, ch)

	if len(d.warnings) != 1 || !strings.Contains(d.warnings[0], "imagen") {
		t.Fatalf("la degradación de §4.6 debe avisarse: %+v", d.warnings)
	}
	if d.kinds[0] != provider.EventWarning {
		t.Errorf("el aviso va antes que el texto: %v", d.kinds)
	}
}

func TestStreamHerramientasSeRearman(t *testing.T) {
	chunks := []string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","function":{"name":"grep","arguments":"{\"q\":"}}]}}]}` + "\n\n",
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"x\"}"}}]}}]}` + "\n\n",
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n",
		"data: [DONE]\n\n",
	}
	srv := sseServer(t, chunks, nil)

	p := newProvider(t, srv.URL)
	ch, err := p.Stream(context.Background(), hola())
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	d := drain(t, ch)

	if len(d.tools) != 1 {
		t.Fatalf("esperada 1 llamada rearmada, hubo %d", len(d.tools))
	}
	if d.tools[0].Name != "grep" || string(d.tools[0].Args) != `{"q":"x"}` {
		t.Errorf("argumentos mal rearmados: %s %s", d.tools[0].Name, d.tools[0].Args)
	}
}

func TestStreamNoStreamingMismoCanal(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := readAll(t, r)
		_ = json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","choices":[{"index":0,"message":{"role":"assistant","content":"respuesta entera"}}],"usage":{"prompt_tokens":10,"completion_tokens":3,"total_tokens":13}}`))
	}))
	defer srv.Close()

	p := newProvider(t, srv.URL)
	req := hola()
	req.Stream = false
	ch, err := p.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	d := drain(t, ch)

	if gotBody["stream"] != false {
		t.Errorf("stream debe ir en falso: %+v", gotBody["stream"])
	}
	if _, hay := gotBody["stream_options"]; hay {
		t.Error("sin streaming no se piden stream_options")
	}
	if d.text != "respuesta entera" {
		t.Errorf("texto perdido: %q", d.text)
	}
	if d.usage == nil || d.usage.In != 10 || d.usage.Out != 3 {
		t.Errorf("usage perdido: %+v", d.usage)
	}
	if d.dones != 1 {
		t.Errorf("el canal es el mismo en los dos modos: %d dones", d.dones)
	}
}

func TestStreamParamsSobrescribenElCuerpo(t *testing.T) {
	var gotBody map[string]any
	srv := sseServer(t, []string{"data: [DONE]\n\n"}, func(_ *http.Request, body []byte) {
		_ = json.Unmarshal(body, &gotBody)
	})

	p := newProvider(t, srv.URL, func(s *provider.Settings) {
		s.Params = map[string]any{
			"stream_options":   nil, // un nil borra la clave
			"reasoning_effort": "high",
		}
	})
	req := hola()
	req.Params = map[string]any{"top_p": 0.5}
	temp := 0.2
	req.Temperature = &temp

	ch, err := p.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	drain(t, ch)

	if _, hay := gotBody["stream_options"]; hay {
		t.Error("params con nil debe poder borrar una clave")
	}
	if gotBody["reasoning_effort"] != "high" {
		t.Errorf("los params del proveedor deben viajar: %+v", gotBody)
	}
	if gotBody["top_p"] != 0.5 {
		t.Errorf("los params del turno deben viajar: %+v", gotBody)
	}
	if gotBody["temperature"] != 0.2 {
		t.Errorf("temperature mal enviada: %+v", gotBody["temperature"])
	}
}

// TestStreamParamsConClaveAnidadaCreaElObjetoIntermedio is F9's regression
// guard for applyParam's dotted-key extension: a key like
// "extra_body.google.thinking_config.include_thoughts" must walk/create the
// intermediate objects and set only the innermost field, without disturbing
// a sibling flat key applied in the same params map. This dialect's own
// buildBody never pre-populates a nested struct at "extra_body" (unlike
// gemini's typed generationConfig), so this exercises descend's `nil` branch,
// not its JSON-round-trip branch — that branch is pinned separately in
// gemini's own test for this same extension.
func TestStreamParamsConClaveAnidadaCreaElObjetoIntermedio(t *testing.T) {
	var gotBody map[string]any
	srv := sseServer(t, []string{"data: [DONE]\n\n"}, func(_ *http.Request, body []byte) {
		_ = json.Unmarshal(body, &gotBody)
	})

	p := newProvider(t, srv.URL)
	req := hola()
	req.Params = map[string]any{
		"extra_body.google.thinking_config.include_thoughts": true,
		"top_p": 0.5, // sibling flat key: must survive alongside the nested one
	}

	ch, err := p.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	drain(t, ch)

	if gotBody["top_p"] != 0.5 {
		t.Errorf("una clave plana junto a una anidada debe seguir llegando: %+v", gotBody)
	}
	eb, ok := gotBody["extra_body"].(map[string]any)
	if !ok {
		t.Fatalf("extra_body no se creó como objeto: %+v", gotBody)
	}
	g, ok := eb["google"].(map[string]any)
	if !ok {
		t.Fatalf("extra_body.google no se creó como objeto: %+v", eb)
	}
	tc, ok := g["thinking_config"].(map[string]any)
	if !ok {
		t.Fatalf("extra_body.google.thinking_config no se creó como objeto: %+v", g)
	}
	if tc["include_thoughts"] != true {
		t.Errorf("include_thoughts = %v, want true", tc["include_thoughts"])
	}
}

// TestStreamParamsAnidadosNilBorraSoloLaHoja pins the delete side of the
// dotted-key extension: a nil at the leaf must delete only that innermost
// key, leaving sibling fields at the same nesting level untouched.
func TestStreamParamsAnidadosNilBorraSoloLaHoja(t *testing.T) {
	var gotBody map[string]any
	srv := sseServer(t, []string{"data: [DONE]\n\n"}, func(_ *http.Request, body []byte) {
		_ = json.Unmarshal(body, &gotBody)
	})

	p := newProvider(t, srv.URL, func(s *provider.Settings) {
		s.Params = map[string]any{
			"extra_body.foo": "bar",
			"extra_body.baz": "qux",
		}
	})
	req := hola()
	req.Params = map[string]any{"extra_body.foo": nil}

	ch, err := p.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	drain(t, ch)

	eb, ok := gotBody["extra_body"].(map[string]any)
	if !ok {
		t.Fatalf("extra_body no se creó como objeto: %+v", gotBody)
	}
	if _, hay := eb["foo"]; hay {
		t.Errorf("un nil en una clave anidada debe borrar solo esa hoja: %+v", eb)
	}
	if eb["baz"] != "qux" {
		t.Errorf("una hoja hermana no debe verse afectada por el borrado de otra: %+v", eb)
	}
}

func TestStreamSystemVaPrimero(t *testing.T) {
	var gotBody struct {
		Messages []openai.ChatMessage `json:"messages"`
	}
	srv := sseServer(t, []string{"data: [DONE]\n\n"}, func(_ *http.Request, body []byte) {
		_ = json.Unmarshal(body, &gotBody)
	})

	p := newProvider(t, srv.URL)
	req := hola()
	req.System = "sos conciso"
	ch, err := p.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	drain(t, ch)

	if len(gotBody.Messages) != 2 {
		t.Fatalf("esperados 2 mensajes, %d: %+v", len(gotBody.Messages), gotBody.Messages)
	}
	if gotBody.Messages[0].Role != "system" || gotBody.Messages[0].Content != "sos conciso" {
		t.Errorf("el prompt de sistema debe ir primero: %+v", gotBody.Messages[0])
	}
}

func TestStreamRedCaidaEsReintentable(t *testing.T) {
	// Un servidor que ya no existe: el fallo de red debe llegar clasificado
	// como reintentable, que es lo que el engine necesita saber.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	p := newProvider(t, url)
	_, err := p.Stream(context.Background(), hola())
	var pe *provider.Error
	if !errors.As(err, &pe) {
		t.Fatalf("se esperaba *provider.Error, dio %T: %v", err, err)
	}
	if !pe.Retryable {
		t.Error("una conexión caída es reintentable")
	}
}

func TestStreamSinMensajesNoSaleALaRed(t *testing.T) {
	llamado := false
	srv := sseServer(t, []string{"data: [DONE]\n\n"}, func(*http.Request, []byte) { llamado = true })

	p := newProvider(t, srv.URL)
	req := hola()
	req.Messages = []convo.Message{convo.User("   ")}
	if _, err := p.Stream(context.Background(), req); err == nil {
		t.Fatal("un turno sin contenido debe fallar antes de gastar red")
	}
	if llamado {
		t.Error("no debió llegar ninguna petición al servidor")
	}
}

// ─────────────────────────────────────────────────────────────
// descubrimiento
// ─────────────────────────────────────────────────────────────

func TestDiscover(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Errorf("path equivocado: %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("método equivocado: %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fixture(t, "models_omniroute.json")))
	}))
	defer srv.Close()

	p := newProvider(t, srv.URL)
	models, err := p.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover falló: %v", err)
	}

	// Cinco entradas en el fixture, una sin id: esa se descarta sin invalidar
	// el resto.
	if len(models) != 4 {
		t.Fatalf("esperados 4 modelos, %d: %+v", len(models), models)
	}
	byID := map[string]provider.RawModel{}
	for _, m := range models {
		byID[m.WireID] = m
	}

	son := byID["anthropic/claude-sonnet-4-5"]
	if son.Name != "Claude Sonnet 4.5" || son.Context != 200000 || son.Output != 64000 {
		t.Errorf("sonnet mal normalizado: %+v", son)
	}
	if len(son.Raw) == 0 || !strings.Contains(string(son.Raw), "pricing") {
		t.Error("el JSON crudo debe conservarse para el Paso 6")
	}
	if g := byID["openai/gpt-5"]; g.Context != 400000 || g.Output != 128000 {
		t.Errorf("gpt-5 mal normalizado (context_window/max_completion_tokens): %+v", g)
	}
	if n := byID["openai/gpt-5-nano"]; n.Name != "openai/gpt-5-nano" {
		t.Errorf("sin name, el nombre es el id: %+v", n)
	}
	if l := byID["meta/llama-3.3-70b"]; l.Context != 131072 {
		t.Errorf("max_context_tokens no se leyó: %+v", l)
	}
}

func TestDiscoverErrorHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("upstream caído"))
	}))
	defer srv.Close()

	p := newProvider(t, srv.URL)
	if _, err := p.Discover(context.Background()); err == nil {
		t.Fatal("un 500 debe ser error")
	} else {
		var pe *provider.Error
		if !errors.As(err, &pe) || !pe.Retryable {
			t.Errorf("se esperaba *provider.Error reintentable: %v", err)
		}
	}
}

// ─────────────────────────────────────────────────────────────
// registro y construcción
// ─────────────────────────────────────────────────────────────

func TestRegistroConoceElKindOpenAI(t *testing.T) {
	if !provider.Registered("openai") {
		t.Fatal("el init() del paquete debe registrar el kind openai")
	}
	if _, err := provider.New(provider.Settings{ID: "x", Kind: "anthropic"}); !errors.Is(err, provider.ErrUnknownKind) {
		t.Errorf("un kind sin adaptador debe decirlo claro: %v", err)
	}
	if _, err := provider.New(provider.Settings{Kind: "openai"}); err == nil {
		t.Error("un proveedor sin id es un error de configuración")
	}
	// Sin kind se asume openai, que es el dialecto por defecto de §5.2.
	if _, err := provider.New(provider.Settings{ID: "local", BaseURL: "http://127.0.0.1:11434/v1"}); err != nil {
		t.Errorf("kind vacío debe asumir openai: %v", err)
	}
	if _, err := provider.New(provider.Settings{ID: "malo", BaseURL: "ftp://nope"}); err == nil {
		t.Error("una base_url sin http debe rechazarse al construir, no al primer turno")
	}
}

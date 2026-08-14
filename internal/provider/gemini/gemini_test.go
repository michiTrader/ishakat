package gemini_test

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
	"github.com/MichiTrader/ishakat/internal/provider/gemini"
)

// Los casos de este archivo espejan los de los otros dos dialectos
// (anthropic/anthropic_test.go, openai/openai_test.go), adaptados a las
// peculiaridades propias de Gemini: streaming elegido por endpoint (no por
// campo del cuerpo), cabecera x-goog-api-key, sobre de error siempre-objeto,
// ausencia de una señal de cierre de streaming propia (finishReason en vez
// de message_stop/[DONE]), functionCall con args ya-objeto y
// thoughtSignature, y promptFeedback.blockReason como camino de error propio
// sin equivalente en los otros dos dialectos.

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("no se pudo leer el fixture %s: %v", name, err)
	}
	return string(b)
}

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
		ID:      "gemini",
		Kind:    "gemini",
		BaseURL: url,
		APIKey:  "AIza-test",
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
		Model:    "gemini-2.5-flash",
		Messages: []convo.Message{convo.User("hola")},
		Stream:   true,
	}
}

type drained struct {
	text  string
	tools []provider.Event
	usage *convo.Usage
	err   error
	dones int
	kinds []provider.EventKind
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
// stream normal
// ─────────────────────────────────────────────────────────────

func TestStreamNormal(t *testing.T) {
	var gotPath, gotKey, gotAccept, gotRawQuery string
	var gotBody map[string]any

	srv := sseServer(t, []string{fixture(t, "stream_normal.sse")}, func(r *http.Request, body []byte) {
		gotPath = r.URL.Path
		gotRawQuery = r.URL.RawQuery
		gotKey = r.Header.Get("x-goog-api-key")
		gotAccept = r.Header.Get("Accept")
		_ = json.Unmarshal(body, &gotBody)
	})

	p := newProvider(t, srv.URL)
	ch, err := p.Stream(context.Background(), hola())
	if err != nil {
		t.Fatalf("el handshake debía funcionar: %v", err)
	}
	d := drain(t, ch)

	if gotPath != "/models/gemini-2.5-flash:streamGenerateContent" {
		t.Errorf("path equivocado: %q", gotPath)
	}
	if gotRawQuery != "alt=sse" {
		t.Errorf("alt=sse es obligatorio para streaming, dio %q", gotRawQuery)
	}
	if gotKey != "AIza-test" {
		t.Errorf("x-goog-api-key equivocado: %q (Authorization: Bearer no es este dialecto)", gotKey)
	}
	if gotAccept != "text/event-stream" {
		t.Errorf("Accept equivocado: %q", gotAccept)
	}
	if _, ok := gotBody["stream"]; ok {
		t.Errorf("Gemini no tiene campo \"stream\" en el cuerpo: %+v", gotBody)
	}
	if _, ok := gotBody["model"]; ok {
		t.Errorf("Gemini no lleva el modelo en el cuerpo, va en el path: %+v", gotBody)
	}
	if gotBody["contents"] == nil {
		t.Errorf("el cuerpo debe llevar \"contents\": %+v", gotBody)
	}

	if d.text != "Hola, ishakat en línea." {
		t.Errorf("texto mal ensamblado: %q", d.text)
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
	if d.usage == nil {
		t.Fatal("el usage se perdió")
	}
	if d.usage.In != 24 || d.usage.Out != 12 {
		t.Errorf("usage mal traducido: %+v", *d.usage)
	}
}

// ─────────────────────────────────────────────────────────────
// corte sin finishReason
// ─────────────────────────────────────────────────────────────

func TestStreamSinFinishReasonEsCorte(t *testing.T) {
	srv := sseServer(t, []string{fixture(t, "stream_truncado.sse")}, nil)

	p := newProvider(t, srv.URL)
	ch, err := p.Stream(context.Background(), hola())
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	d := drain(t, ch)

	if d.text != "Voy a explicarlo en tres pasos" {
		t.Errorf("el parcial completo debe conservarse: %q", d.text)
	}
	if !errors.Is(d.err, provider.ErrStreamTruncated) {
		t.Errorf("un corte sin candidate.finishReason debe reportar ErrStreamTruncated, dio %v", d.err)
	}
	if d.kinds[len(d.kinds)-1] != provider.EventDone || d.kinds[len(d.kinds)-2] != provider.EventError {
		t.Errorf("el orden debe ser …EventError, EventDone: %v", d.kinds)
	}
}

// ─────────────────────────────────────────────────────────────
// prompt bloqueado
// ─────────────────────────────────────────────────────────────

// TestStreamPromptBloqueado fija el camino de error propio de Gemini sin
// equivalente en Anthropic/OpenAI: promptFeedback.blockReason con
// candidates vacío significa que el PROMPT (no la respuesta) fue rechazado,
// y eso se traduce a un provider.Error explícito en vez de ErrStreamTruncated.
func TestStreamPromptBloqueado(t *testing.T) {
	srv := sseServer(t, []string{fixture(t, "stream_bloqueado.sse")}, nil)

	p := newProvider(t, srv.URL)
	ch, err := p.Stream(context.Background(), hola())
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	d := drain(t, ch)

	if d.err == nil || !strings.Contains(d.err.Error(), "SAFETY") {
		t.Errorf("el bloqueo del prompt debe reportarse con su razón: %v", d.err)
	}
	var pe *provider.Error
	if !errors.As(d.err, &pe) {
		t.Fatalf("se esperaba *provider.Error, dio %T: %v", d.err, d.err)
	}
	if pe.Code != "PROMPT_BLOCKED" {
		t.Errorf("code equivocado: %q", pe.Code)
	}
}

// ─────────────────────────────────────────────────────────────
// errores HTTP
// ─────────────────────────────────────────────────────────────

func TestStream429ConRetryAfter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "2")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":429,"message":"rate limit exceeded","status":"RESOURCE_EXHAUSTED"}}`))
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
	if pe.Code != "RESOURCE_EXHAUSTED" || !strings.Contains(pe.Message, "rate limit") {
		t.Errorf("el error del cuerpo debe llegar legible: %+v", pe)
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

func TestHTTPErrorEsSiempreUnObjetoNuncaUnArray(t *testing.T) {
	// A diferencia del shim compatible con OpenAI de Gemini
	// (openai.httpError, que sí envuelve el error en un array porque hay un
	// gateway de terceros de por medio), la API nativa usa siempre un
	// objeto {"error": {...}} — este test fija que ese sobre se lee bien.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":400,"message":"Request contains an invalid argument","status":"INVALID_ARGUMENT"}}`))
	}))
	defer srv.Close()

	p := newProvider(t, srv.URL)
	_, err := p.Stream(context.Background(), hola())
	if err == nil || !strings.Contains(err.Error(), "invalid argument") {
		t.Errorf("el mensaje del servicio debe llegar legible: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────
// herramientas
// ─────────────────────────────────────────────────────────────

// TestStreamHerramientaConThoughtSignature fija dos cosas propias de este
// dialecto y sin equivalente exacto en Anthropic: los args de functionCall
// llegan como un objeto JSON completo de una sola vez (no fragmentos de
// partial_json que haya que rearmar), y el thoughtSignature adjunto al Part
// tiene que propagarse a Event.Signature — sin eso el próximo turno con esa
// llamada de vuelta falla con HTTP 400 (ver el comentario de
// wirePart.ThoughtSignature en wire.go).
func TestStreamHerramientaConThoughtSignature(t *testing.T) {
	srv := sseServer(t, []string{fixture(t, "stream_herramienta.sse")}, nil)

	p := newProvider(t, srv.URL)
	req := hola()
	req.Caps.Tools = true
	req.Tools = []provider.ToolDef{{Name: "clima", Description: "consulta el clima"}}
	ch, err := p.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	d := drain(t, ch)

	if len(d.tools) != 1 {
		t.Fatalf("se esperaba una llamada a herramienta, hubo %d", len(d.tools))
	}
	tc := d.tools[0]
	if tc.Name != "clima" || tc.ID != "call_1" {
		t.Errorf("nombre/id mal propagados: %+v", tc)
	}
	if tc.Signature != "sig_abc123" {
		t.Errorf("thoughtSignature debe propagarse a Event.Signature: %q", tc.Signature)
	}
	var args map[string]any
	if err := json.Unmarshal(tc.Args, &args); err != nil {
		t.Fatalf("args ya viene como objeto JSON completo: %v (%s)", err, tc.Args)
	}
	if args["ciudad"] != "Madrid" {
		t.Errorf("argumentos mal propagados: %+v", args)
	}
}

func TestFromConvoAplanaHerramientasSinCapsTools(t *testing.T) {
	msgs := []convo.Message{
		convo.NewMessage(convo.RoleAssistant, convo.ToolCallBlock("id1", "clima", json.RawMessage(`{"ciudad":"Madrid"}`))),
		convo.NewMessage(convo.RoleTool, convo.ToolResultBlock("id1", "clima", "20 grados")),
	}
	out, _, deg := gemini.FromConvo(msgs, provider.Caps{Tools: false})
	if deg.ToolsFlattened != 2 {
		t.Errorf("las dos llamadas deben contarse como aplanadas: %+v", deg)
	}
	for _, m := range out {
		if m.Role == "tool" {
			t.Errorf("role:\"tool\" no existe en este dialecto, debe remapear a user: %+v", m)
		}
		if m.Role == "assistant" {
			t.Errorf("role:\"assistant\" no existe en este dialecto, debe remapear a model: %+v", m)
		}
	}
}

// ─────────────────────────────────────────────────────────────
// no-streaming
// ─────────────────────────────────────────────────────────────

func TestStreamNoStreamingMismoCanal(t *testing.T) {
	body := `{"candidates":[{"content":{"role":"model","parts":[{"text":"Hola sin streaming"}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":3,"totalTokenCount":8}}`
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
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

	if gotPath != "/models/gemini-2.5-flash:generateContent" {
		t.Errorf("sin streaming el endpoint debe ser :generateContent, dio %q", gotPath)
	}
	if d.text != "Hola sin streaming" {
		t.Errorf("texto mal leído en modo no-streaming: %q", d.text)
	}
	if d.usage == nil || d.usage.In != 5 || d.usage.Out != 3 {
		t.Errorf("usage mal leído: %+v", d.usage)
	}
}

// ─────────────────────────────────────────────────────────────
// descubrimiento
// ─────────────────────────────────────────────────────────────

func TestDiscover(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Errorf("path equivocado: %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fixture(t, "models.json")))
	}))
	defer srv.Close()

	p := newProvider(t, srv.URL)
	models, err := p.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("se esperaban 2 modelos (embedding-001 debe filtrarse), hubo %d", len(models))
	}
	if models[0].WireID != "gemini-2.5-pro" || models[0].Name != "Gemini 2.5 Pro" {
		t.Errorf("modelo mal traducido: %+v", models[0])
	}
	if models[0].Context != 1048576 || models[0].Output != 65536 {
		t.Errorf("Context/Output mal rellenados desde inputTokenLimit/outputTokenLimit: %+v", models[0])
	}
	for _, m := range models {
		if m.WireID == "embedding-001" {
			t.Errorf("embedding-001 no soporta generateContent y no debe aparecer: %+v", m)
		}
	}
}

func TestDiscoverErrorHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"code":500,"message":"boom","status":"INTERNAL"}}`))
	}))
	defer srv.Close()

	p := newProvider(t, srv.URL)
	_, err := p.Discover(context.Background())
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("el error del catálogo debe llegar legible: %v", err)
	}
}

func TestRegistroConoceElKindGemini(t *testing.T) {
	if !provider.Registered("gemini") {
		t.Error("el kind \"gemini\" debe quedar registrado por el init() de este paquete")
	}
}

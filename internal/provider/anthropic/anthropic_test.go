package anthropic_test

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
	"github.com/MichiTrader/ishakat/internal/provider/anthropic"
)

// Los casos de este archivo espejan los del dialecto OpenAI
// (openai/openai_test.go), adaptados al flujo de eventos propio de la
// Messages API: stream normal, corte sin message_stop, 429 con
// Retry-After, 401 sin clave, herramientas (tool_use/tool_result),
// modo no-streaming y descubrimiento de modelos.

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
		ID:      "anthropic",
		Kind:    "anthropic",
		BaseURL: url,
		APIKey:  "sk-ant-test",
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
		Model:    "claude-sonnet-4-5",
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
	var gotPath, gotKey, gotVersion, gotAccept string
	var gotBody map[string]any

	srv := sseServer(t, []string{fixture(t, "stream_normal.sse")}, func(r *http.Request, body []byte) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		gotAccept = r.Header.Get("Accept")
		_ = json.Unmarshal(body, &gotBody)
	})

	p := newProvider(t, srv.URL)
	ch, err := p.Stream(context.Background(), hola())
	if err != nil {
		t.Fatalf("el handshake debía funcionar: %v", err)
	}
	d := drain(t, ch)

	if gotPath != "/messages" {
		t.Errorf("path equivocado: %q", gotPath)
	}
	if gotKey != "sk-ant-test" {
		t.Errorf("x-api-key equivocado: %q (Authorization: Bearer no es este dialecto)", gotKey)
	}
	if gotVersion != "2023-06-01" {
		t.Errorf("anthropic-version equivocado: %q", gotVersion)
	}
	if gotAccept != "text/event-stream" {
		t.Errorf("Accept equivocado: %q", gotAccept)
	}
	if gotBody["model"] != "claude-sonnet-4-5" || gotBody["stream"] != true {
		t.Errorf("cuerpo equivocado: %+v", gotBody)
	}
	if mt, ok := gotBody["max_tokens"].(float64); !ok || mt <= 0 {
		t.Errorf("max_tokens es obligatorio en este dialecto y debe ir siempre: %+v", gotBody["max_tokens"])
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
// corte sin message_stop
// ─────────────────────────────────────────────────────────────

func TestStreamSinMessageStopEsCorte(t *testing.T) {
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
		t.Errorf("un corte sin message_stop debe reportar ErrStreamTruncated, dio %v", d.err)
	}
	if d.kinds[len(d.kinds)-1] != provider.EventDone || d.kinds[len(d.kinds)-2] != provider.EventError {
		t.Errorf("el orden debe ser …EventError, EventDone: %v", d.kinds)
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
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"rate limit exceeded"}}`))
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
	if pe.Code != "rate_limit_error" || !strings.Contains(pe.Message, "rate limit") {
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
	// A diferencia del shim de Gemini (openai.httpError), la API nativa de
	// Anthropic nunca envuelve el error en un array: no hay ningún tercero
	// de por medio. Este test fija que el sobre de objeto se lee bien.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"max_tokens: field required"}}`))
	}))
	defer srv.Close()

	p := newProvider(t, srv.URL)
	_, err := p.Stream(context.Background(), hola())
	if err == nil || !strings.Contains(err.Error(), "max_tokens") {
		t.Errorf("el mensaje del servicio debe llegar legible: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────
// herramientas
// ─────────────────────────────────────────────────────────────

func TestStreamHerramientaSeRearmaDesdeFragmentosDeJSON(t *testing.T) {
	chunks := []string{
		`event: message_start` + "\n" +
			`data: {"type":"message_start","message":{"id":"m1","type":"message","role":"assistant","usage":{"input_tokens":5}}}` + "\n\n",
		`event: content_block_start` + "\n" +
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"clima"}}` + "\n\n",
		`event: content_block_delta` + "\n" +
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"ciudad\""}}` + "\n\n",
		`event: content_block_delta` + "\n" +
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":":\"Madrid\"}"}}` + "\n\n",
		`event: content_block_stop` + "\n" +
			`data: {"type":"content_block_stop","index":0}` + "\n\n",
		`event: message_delta` + "\n" +
			`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":8}}` + "\n\n",
		`event: message_stop` + "\n" +
			`data: {"type":"message_stop"}` + "\n\n",
	}
	srv := sseServer(t, chunks, nil)

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
	if tc.Name != "clima" || tc.ID != "toolu_1" {
		t.Errorf("nombre/id mal propagados: %+v", tc)
	}
	var args map[string]any
	if err := json.Unmarshal(tc.Args, &args); err != nil {
		t.Fatalf("los fragmentos de partial_json deben rearmar un JSON válido: %v (%s)", err, tc.Args)
	}
	if args["ciudad"] != "Madrid" {
		t.Errorf("argumentos mal rearmados: %+v", args)
	}
}

func TestFromConvoAplanaHerramientasSinCapsTools(t *testing.T) {
	msgs := []convo.Message{
		convo.NewMessage(convo.RoleAssistant, convo.ToolCallBlock("id1", "clima", json.RawMessage(`{"ciudad":"Madrid"}`))),
		convo.NewMessage(convo.RoleTool, convo.ToolResultBlock("id1", "clima", "20 grados")),
	}
	out, _, deg := anthropic.FromConvo(msgs, provider.Caps{Tools: false})
	if deg.ToolsFlattened != 2 {
		t.Errorf("las dos llamadas deben contarse como aplanadas: %+v", deg)
	}
	for _, m := range out {
		if m.Role == "tool" {
			t.Errorf("role:\"tool\" no existe en este dialecto, debe remapear a user: %+v", m)
		}
	}
}

// ─────────────────────────────────────────────────────────────
// no-streaming
// ─────────────────────────────────────────────────────────────

func TestStreamNoStreamingMismoCanal(t *testing.T) {
	body := `{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"Hola sin streaming"}],"usage":{"input_tokens":5,"output_tokens":3}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		t.Fatalf("se esperaban 2 modelos, hubo %d", len(models))
	}
	if models[0].WireID != "claude-sonnet-4-5" || models[0].Name != "Claude Sonnet 4.5" {
		t.Errorf("modelo mal traducido: %+v", models[0])
	}
}

func TestDiscoverErrorHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"api_error","message":"boom"}}`))
	}))
	defer srv.Close()

	p := newProvider(t, srv.URL)
	_, err := p.Discover(context.Background())
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("el error del catálogo debe llegar legible: %v", err)
	}
}

func TestRegistroConoceElKindAnthropic(t *testing.T) {
	if !provider.Registered("anthropic") {
		t.Error("el kind \"anthropic\" debe quedar registrado por el init() de este paquete")
	}
}

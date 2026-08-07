package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/MichiTrader/ishakat/internal/provider"
)

// El auto-registro de §5.4: con este init(), un `kind = "openai"` en el TOML
// basta para hablar con OmniRoute, OpenAI, Groq, Together, OpenRouter,
// DeepSeek, Ollama o LM Studio sin tocar una línea de código.
func init() {
	provider.Register("openai", New)
	// Aerolink and Codex-compatible gateways use the OpenAI Responses wire API
	// rather than chat/completions, but share the same authentication, model
	// discovery and transport settings.
	provider.Register("responses", New)
}

// defaultBaseURL es el de OpenAI. En la práctica siempre viene de la
// configuración; está aquí para que un Settings mínimo funcione en un test.
const defaultBaseURL = "https://api.openai.com/v1"

// Provider implementa provider.Provider para el dialecto OpenAI.
type Provider struct {
	set  provider.Settings
	base string
	hc   *http.Client
}

// New construye el adaptador. Verifica lo que se puede verificar sin red:
// que haya id y que la URL base sea usable.
func New(s provider.Settings) (provider.Provider, error) {
	base := strings.TrimSuffix(strings.TrimSpace(s.BaseURL), "/")
	if base == "" {
		base = defaultBaseURL
	}
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		return nil, fmt.Errorf("openai: base_url de %q debe empezar con http:// o https://, es %q", s.ID, base)
	}

	p := &Provider{set: s, base: base, hc: s.HTTPClient}
	if p.hc == nil {
		p.hc = newHTTPClient(s)
	}
	return p, nil
}

// ID devuelve el identificador del proveedor tal como está en la config.
func (p *Provider) ID() string { return p.set.ID }

// newHTTPClient arma el cliente con la única política de timeouts que sirve
// para streaming.
//
// http.Client.Timeout cuenta el cuerpo de la respuesta, así que ponerlo mata
// cualquier turno que dure más que el timeout, y un turno largo puede durar
// minutos legítimamente. Lo que hay que acotar es el silencio: el tiempo hasta
// la conexión y el tiempo hasta las cabeceras. Si el modelo ya empezó a
// escribir, no hay reloj que lo interrumpa salvo el usuario.
func newHTTPClient(s provider.Settings) *http.Client {
	connect := s.ConnectTimeout
	if connect <= 0 {
		connect = 10 * time.Second
	}
	header := s.Timeout
	if header <= 0 {
		header = 60 * time.Second
	}

	tr := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   connect,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          8,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   connect,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: header,

		// Sin esto, Go puede pedir gzip y bufferizar: el streaming llegaría a
		// tirones de varios kilobytes en vez de token a token.
		DisableCompression: true,
	}
	return &http.Client{Transport: tr}
}

// newRequest construye una petición con las cabeceras comunes ya puestas.
func (p *Provider) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	url := p.base + path
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("openai: no se pudo crear la petición a %s: %w", url, err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if p.set.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.set.APIKey)
	}
	ua := p.set.UserAgent
	if ua == "" {
		ua = "ishakat"
	}
	req.Header.Set("User-Agent", ua)

	// Las cabeceras de la config van al final para que puedan sobrescribir
	// cualquiera de las anteriores: es la vía de escape para un gateway que
	// pide HTTP-Referer, X-Title o una autenticación no estándar (§5.2).
	for k, v := range p.set.Headers {
		req.Header.Set(k, v)
	}
	return req, nil
}

// buildBody arma el cuerpo JSON del turno.
//
// Se construye como mapa, no como struct, por una razón concreta: los
// overrides de [provider.params] tienen que poder añadir campos que ishakat no
// conoce y también reemplazar los que sí. Con un struct habría que declarar de
// antemano cada campo de cada gateway, que es exactamente lo que §5.2 promete
// evitar.
func (p *Provider) buildBody(req provider.Request, msgs []ChatMessage) ([]byte, error) {
	body := map[string]any{
		"model":    req.Model,
		"messages": msgs,
		"stream":   req.Stream,
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	if req.MaxTokens != nil {
		// Los modelos nuevos de OpenAI prefieren max_completion_tokens, pero
		// todos los gateways del catálogo siguen aceptando max_tokens. Si
		// alguno deja de hacerlo, se arregla con params sin recompilar.
		body["max_tokens"] = *req.MaxTokens
	}

	// Pedir el usage en el último chunk. Va antes de los overrides para que un
	// servicio que no lo soporte pueda desactivarlo con
	// params.stream_options = {}.
	if req.Stream {
		body["stream_options"] = map[string]any{"include_usage": true}
	}

	// El array `tools` del dialecto (§12bis #5). Se manda solo cuando el
	// modelo de destino declara soporte (Caps.Tools) — provider.Request.Tools
	// documenta exactamente esto: "cuando Caps.Tools es falso, el adaptador
	// lo deja vacío". Sin esta comprobación, un modelo sin soporte de tools
	// recibía el array igual y el servicio respondía 400. Vacío/omitido
	// significa sin herramientas: se omite el campo en vez de mandar [],
	// porque algunos gateways rechazan un array vacío. Va antes de los
	// overrides para que [provider.params] pueda reemplazarlo o quitarlo sin
	// recompilar.
	if req.Caps.Tools {
		if tools := MarshalTools(req.Tools); tools != nil {
			body["tools"] = tools
		}
	}

	for k, v := range p.set.Params {
		applyParam(body, k, v)
	}
	for k, v := range req.Params {
		applyParam(body, k, v)
	}

	out, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("openai: no se pudo serializar el cuerpo del turno: %w", err)
	}
	return out, nil
}

// applyParam aplica un override. Un valor nil borra la clave: así se puede
// quitar stream_options desde el TOML.
func applyParam(body map[string]any, k string, v any) {
	if k == "" {
		return
	}
	if v == nil {
		delete(body, k)
		return
	}
	body[k] = v
}

// httpError interpreta una respuesta con estado distinto de 200 y la convierte
// en el *provider.Error que el engine necesita para decidir si reintenta.
//
// El cuerpo se lee acotado: un servicio caído puede devolver una página HTML
// de error de varios megabytes, y no hay ninguna razón para cargarla completa
// en un teléfono.
func (p *Provider) httpError(resp *http.Response) *provider.Error {
	const maxErrBody = 8 << 10
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrBody))

	e := &provider.Error{
		Provider:   p.set.ID,
		Status:     resp.StatusCode,
		Retryable:  provider.RetryableStatus(resp.StatusCode),
		RetryAfter: provider.ParseRetryAfter(resp.Header.Get("Retry-After"), time.Now()),
	}

	var env struct {
		Error *wireError `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err == nil && env.Error != nil {
		e.Message = env.Error.Message
		e.Code = codeString(env.Error)
	}
	if e.Message == "" {
		e.Message = firstLine(string(raw), 200)
	}
	if e.Message == "" {
		e.Message = http.StatusText(resp.StatusCode)
	}

	// A 401 with a key set and a 401 with no key are different problems,
	// and the message has to say so, because this is the most common error
	// when configuring the program for the first time.
	if resp.StatusCode == http.StatusUnauthorized && p.set.APIKey == "" {
		e.Err = provider.ErrNoAPIKey
		e.Message = "the service requires authentication and no api_key is configured for " + p.set.ID
	}
	return e
}

// codeString devuelve el código de error como texto, venga como cadena o como
// número (los dos se ven en la práctica).
func codeString(we *wireError) string {
	if we == nil || len(we.Code) == 0 {
		if we != nil {
			return we.Type
		}
		return ""
	}
	var s string
	if err := json.Unmarshal(we.Code, &s); err == nil {
		return s
	}
	var n json.Number
	if err := json.Unmarshal(we.Code, &n); err == nil {
		return n.String()
	}
	return we.Type
}

func firstLine(s string, max int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}

// netError envuelve un fallo de red como error reintentable. Un DNS que falla
// en Termux al salir del túnel o un socket que se cae en el metro son
// exactamente los casos que el reintento del Paso 8 tiene que cubrir.
func (p *Provider) netError(err error) *provider.Error {
	return &provider.Error{
		Provider:  p.set.ID,
		Retryable: true,
		Message:   err.Error(),
		Err:       err,
	}
}

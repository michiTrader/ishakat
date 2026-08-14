package anthropic

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

// El auto-registro de §5.4: con este init(), un `kind = "anthropic"` en el
// TOML habla directamente con la Messages API nativa, sin pasar por el shim
// compatible con OpenAI que el preset "anthropic" de credentials.go usa por
// defecto.
func init() {
	provider.Register("anthropic", New)
}

// defaultBaseURL es el de Anthropic. En la práctica siempre viene de la
// configuración; está aquí para que un Settings mínimo funcione en un test,
// igual que en el dialecto OpenAI.
const defaultBaseURL = "https://api.anthropic.com/v1"

// defaultAnthropicVersion es la versión de API que se manda cuando la
// configuración no fija una propia en [provider.headers]. "2023-06-01" es la
// única versión que existe a la fecha de este adaptador y la que toda la
// documentación pública usa como ejemplo; si Anthropic publica una nueva
// versión, [provider.headers] "anthropic-version" ya es la vía de escape sin
// recompilar (§5.2), la misma que config.example.toml's propio bloque
// `[[provider]] id = "anthropic"` ya documenta.
const defaultAnthropicVersion = "2023-06-01"

// defaultMaxTokens es lo que se manda cuando ni Request.MaxTokens ni
// [provider.params] max_tokens fijan un valor. La Messages API, a diferencia
// de chat/completions, EXIGE este campo — un request sin él es un 400, no un
// valor por defecto del servicio — así que buildBody siempre lo pone. El
// número es deliberadamente generoso (cubre una respuesta larga sin cortar)
// y no un límite de seguridad: quien necesite otra cosa lo fija con
// [provider.params] max_tokens = N.
const defaultMaxTokens = 8192

// Provider implementa provider.Provider para el dialecto nativo de
// Anthropic.
type Provider struct {
	set  provider.Settings
	base string
	hc   *http.Client
}

// New construye el adaptador. Verifica lo que se puede verificar sin red:
// que la URL base sea usable — igual que openai.New.
func New(s provider.Settings) (provider.Provider, error) {
	base := strings.TrimSuffix(strings.TrimSpace(s.BaseURL), "/")
	if base == "" {
		base = defaultBaseURL
	}
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		return nil, fmt.Errorf("anthropic: base_url de %q debe empezar con http:// o https://, es %q", s.ID, base)
	}

	p := &Provider{set: s, base: base, hc: s.HTTPClient}
	if p.hc == nil {
		p.hc = newHTTPClient(s)
	}
	return p, nil
}

// ID devuelve el identificador del proveedor tal como está en la config.
func (p *Provider) ID() string { return p.set.ID }

// newHTTPClient es una copia deliberada de openai.newHTTPClient, no una
// función compartida: el mismo razonamiento sobre timeouts para streaming
// aplica byte a byte a este dialecto, pero atarlos con una función común
// obligaría a mover esa lógica a un tercer paquete del que ninguno de los
// dos depende hoy, por una duplicación de treinta líneas que cambia junta
// con tanta frecuencia como cambia nunca.
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
		DisableCompression:    true,
	}
	return &http.Client{Transport: tr}
}

// newRequest construye una petición con las cabeceras propias del dialecto
// ya puestas: x-api-key (no "Authorization: Bearer") y anthropic-version,
// que la API exige en toda petición o responde 400.
func (p *Provider) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	url := p.base + path
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("anthropic: no se pudo crear la petición a %s: %w", url, err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if p.set.APIKey != "" {
		req.Header.Set("x-api-key", p.set.APIKey)
	}
	req.Header.Set("anthropic-version", defaultAnthropicVersion)
	ua := p.set.UserAgent
	if ua == "" {
		ua = "ishakat"
	}
	req.Header.Set("User-Agent", ua)

	// Las cabeceras de la config van al final para que puedan sobrescribir
	// cualquiera de las anteriores — es la vía de escape de §5.2, y en
	// concreto la que deja fijar una anthropic-version distinta sin
	// recompilar (ver el comentario de defaultAnthropicVersion).
	for k, v := range p.set.Headers {
		req.Header.Set(k, v)
	}
	return req, nil
}

// buildBody arma el cuerpo JSON del turno. Se construye como mapa, no como
// struct, por la misma razón que openai.buildBody: los overrides de
// [provider.params] tienen que poder añadir o reemplazar campos sin que el
// adaptador conozca de antemano cada uno.
func (p *Provider) buildBody(req provider.Request, msgs []wireMessage, system string) ([]byte, error) {
	maxTokens := defaultMaxTokens
	if req.MaxTokens != nil && *req.MaxTokens > 0 {
		maxTokens = *req.MaxTokens
	}

	body := map[string]any{
		"model":      req.Model,
		"messages":   msgs,
		"max_tokens": maxTokens,
		"stream":     req.Stream,
	}
	if system != "" {
		body["system"] = system
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
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
		return nil, fmt.Errorf("anthropic: no se pudo serializar el cuerpo del turno: %w", err)
	}
	return out, nil
}

// applyParam es una copia de openai.applyParam: un valor nil borra la clave,
// para poder quitar un campo desde el TOML.
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

// httpError interpreta una respuesta con estado distinto de 200 y la
// convierte en el *provider.Error que el engine necesita para decidir si
// reintenta.
//
// El sobre de error de Anthropic es siempre un objeto, nunca el array que el
// shim de Gemini usa (openai.httpError's segunda rama no tiene equivalente
// aquí porque no hay ningún gateway de terceros de por medio: esta es la API
// nativa, verificada directamente contra su propia documentación pública de
// errores — §11/§errors: {"type":"error","error":{"type":"...",
// "message":"..."}}).
func (p *Provider) httpError(resp *http.Response) *provider.Error {
	const maxErrBody = 8 << 10
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrBody))

	e := &provider.Error{
		Provider:   p.set.ID,
		Status:     resp.StatusCode,
		Retryable:  provider.RetryableStatus(resp.StatusCode),
		RetryAfter: provider.ParseRetryAfter(resp.Header.Get("Retry-After"), time.Now()),
	}

	var env wireErrorEnvelope
	if err := json.Unmarshal(raw, &env); err == nil && env.Error != nil {
		e.Message = env.Error.Message
		e.Code = env.Error.Type
	}
	if e.Message == "" {
		e.Message = collapseJSON(string(raw), 300)
	}
	if e.Message == "" {
		e.Message = http.StatusText(resp.StatusCode)
	}

	// Un 401 con clave puesta y un 401 sin clave son problemas distintos,
	// igual que en el dialecto OpenAI: el mensaje tiene que decir cuál es,
	// porque es el error más común al configurar el programa por primera
	// vez.
	if resp.StatusCode == http.StatusUnauthorized && p.set.APIKey == "" {
		e.Err = provider.ErrNoAPIKey
		e.Message = "the service requires authentication and no api_key is configured for " + p.set.ID
	}
	return e
}

// collapseJSON es una copia de openai.collapseJSON: aplana un cuerpo de
// varias líneas en una sola para que truncarlo conserve información en vez
// de puntuación.
func collapseJSON(s string, max int) string {
	var b strings.Builder
	b.Grow(len(s))
	space := true
	for _, r := range s {
		if r == '\n' || r == '\r' || r == '\t' || r == ' ' {
			if !space {
				b.WriteByte(' ')
				space = true
			}
			continue
		}
		b.WriteRune(r)
		space = false
	}
	out := strings.TrimSpace(b.String())
	if r := []rune(out); len(r) > max {
		out = string(r[:max]) + "…"
	}
	return out
}

// netError envuelve un fallo de red como error reintentable, igual que
// openai.netError.
func (p *Provider) netError(err error) *provider.Error {
	return &provider.Error{
		Provider:  p.set.ID,
		Retryable: true,
		Message:   err.Error(),
		Err:       err,
	}
}

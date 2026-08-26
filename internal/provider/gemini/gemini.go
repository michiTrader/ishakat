package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/MichiTrader/ishakat/internal/provider"
)

// El auto-registro de §5.4: con este init(), un `kind = "gemini"` en el TOML
// habla directamente con generateContent/streamGenerateContent, sin pasar
// por el shim compatible con OpenAI que el preset "gemini" de
// credentials.go usa por defecto.
func init() {
	provider.Register("gemini", New)
}

// defaultBaseURL es el de la API nativa de Google, confirmado contra el
// Discovery Document (ver wire.go): todo método vive bajo /v1beta.
const defaultBaseURL = "https://generativelanguage.googleapis.com/v1beta"

// Provider implementa provider.Provider para el dialecto nativo de Gemini.
type Provider struct {
	set  provider.Settings
	base string
	hc   *http.Client
}

// New construye el adaptador. Verifica lo que se puede verificar sin red:
// que la URL base sea usable — igual que anthropic.New y openai.New.
func New(s provider.Settings) (provider.Provider, error) {
	base := strings.TrimSuffix(strings.TrimSpace(s.BaseURL), "/")
	if base == "" {
		base = defaultBaseURL
	}
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		return nil, fmt.Errorf("gemini: base_url de %q debe empezar con http:// o https://, es %q", s.ID, base)
	}

	p := &Provider{set: s, base: base, hc: s.HTTPClient}
	if p.hc == nil {
		p.hc = newHTTPClient(s)
	}
	return p, nil
}

// ID devuelve el identificador del proveedor tal como está en la config.
func (p *Provider) ID() string { return p.set.ID }

// newHTTPClient es una copia deliberada de anthropic.newHTTPClient/
// openai.newHTTPClient, no una función compartida — mismo razonamiento
// documentado en esos dos archivos.
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

// newRequest construye una petición con la cabecera propia del dialecto ya
// puesta: x-goog-api-key, no "Authorization: Bearer" ni "x-api-key" — la
// guía pública de autenticación de Google lo dice de forma literal: "All
// requests to the Gemini API must include a x-goog-api-key header with
// your API key."
func (p *Provider) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	url := p.base + path
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("gemini: no se pudo crear la petición a %s: %w", url, err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if p.set.APIKey != "" {
		req.Header.Set("x-goog-api-key", p.set.APIKey)
	}
	ua := p.set.UserAgent
	if ua == "" {
		ua = "ishakat"
	}
	req.Header.Set("User-Agent", ua)

	// Las cabeceras de la config van al final para que puedan sobrescribir
	// cualquiera de las anteriores — la vía de escape de §5.2.
	for k, v := range p.set.Headers {
		req.Header.Set(k, v)
	}
	return req, nil
}

// buildBody arma el cuerpo JSON del turno. Se construye como mapa, no como
// struct, por la misma razón que los otros dos dialectos: los overrides de
// [provider.params] tienen que poder añadir o reemplazar campos sin que el
// adaptador conozca de antemano cada uno.
func (p *Provider) buildBody(req provider.Request, contents []wireContent, system string) ([]byte, error) {
	body := map[string]any{
		"contents": contents,
	}
	if system != "" {
		body["systemInstruction"] = wireContent{Parts: []wirePart{{Text: system}}}
	}
	if req.Caps.Tools {
		if tools := MarshalTools(req.Tools); tools != nil {
			body["tools"] = tools
		}
	}

	var gc wireGenConfig
	haveGC := false
	if req.Temperature != nil {
		gc.Temperature = req.Temperature
		haveGC = true
	}
	if req.MaxTokens != nil && *req.MaxTokens > 0 {
		gc.MaxOutputTokens = req.MaxTokens
		haveGC = true
	}
	// Thought summaries are opt-in on Google's side: without this the
	// response carries no `thought` Part at all, emitPart's own
	// `case part.Thought` never fires, and the interface shows an empty
	// reasoning preview no matter what ui.reasoning says. Sent only when
	// asked (§4.2: reasoning tokens are billed) and, like every other field
	// here, before the params overrides, so [provider.params] can still
	// replace or delete generationConfig wholesale.
	if req.IncludeReasoning {
		gc.ThinkingConfig = &wireThinkingConfig{IncludeThoughts: true}
		haveGC = true
	}
	if haveGC {
		body["generationConfig"] = gc
	}

	for k, v := range p.set.Params {
		applyParam(body, k, v)
	}
	for k, v := range req.Params {
		applyParam(body, k, v)
	}

	out, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("gemini: no se pudo serializar el cuerpo del turno: %w", err)
	}
	return out, nil
}

// applyParam es una copia de anthropic.applyParam/openai.applyParam: un
// valor nil borra la clave, para poder quitar un campo desde el TOML.
//
// F9 extends this with one level (or more) of dotted-key nesting: a key
// like "generationConfig.thinkingConfig.thinkingLevel" walks/creates the
// intermediate objects and applies nil-deletes/sets-otherwise only to the
// innermost one, instead of only ever touching a flat top-level field. This
// matters concretely for this dialect: buildBody sets body["generationConfig"]
// as a typed wireGenConfig struct (not a map[string]any) whenever haveGC is
// true — descend's own comment explains why a JSON round-trip, not a naive
// overwrite, is what lets a dotted params override reach inside that struct
// (or inside its own nested *wireThinkingConfig) without discarding whatever
// buildBody already put there. A flat key (every existing caller) behaves
// exactly as before: this is purely additive.
func applyParam(body map[string]any, k string, v any) {
	if k == "" {
		return
	}
	segs := strings.Split(k, ".")
	target := body
	for _, seg := range segs[:len(segs)-1] {
		if seg == "" {
			return // malformed key ("a..b", leading/trailing dot): no-op, safer than guessing
		}
		target = descend(target, seg)
	}
	leaf := segs[len(segs)-1]
	if leaf == "" {
		return
	}
	if v == nil {
		delete(target, leaf)
		return
	}
	target[leaf] = v
}

// descend es una copia de openai.descend: devuelve el mapa anidado en
// body[seg], creando uno vacío si no existe. Si body[seg] ya contiene otra
// cosa — aquí, típicamente, el struct tipado wireGenConfig que buildBody puso
// antes de que corriera el bucle de params — se pasa primero por
// encoding/json (Marshal/Unmarshal) para que sus campos existentes
// sobrevivan como mapa en vez de perderse con un simple
// "target[seg] = map[string]any{}". Esto funciona porque wireGenConfig y
// wireThinkingConfig (wire.go) llevan sus propias etiquetas json:"..." en
// cada campo. Un Marshal fallido (no debería pasar con nada que buildBody
// construya) cae a un mapa vacío en vez de entrar en pánico o devolver un
// error que nada aquí podría reportar de forma útil.
func descend(body map[string]any, seg string) map[string]any {
	switch existing := body[seg].(type) {
	case map[string]any:
		return existing
	case nil:
		m := map[string]any{}
		body[seg] = m
		return m
	default:
		m := map[string]any{}
		if raw, err := json.Marshal(existing); err == nil {
			_ = json.Unmarshal(raw, &m)
		}
		body[seg] = m
		return m
	}
}

// httpError interpreta una respuesta con estado distinto de 200 y la
// convierte en el *provider.Error que el engine necesita para decidir si
// reintenta.
//
// El sobre de error de la API nativa es siempre un objeto — {"error":
// {"code":...,"message":"...","status":"..."}} — nunca el array que el shim
// compatible con OpenAI de Gemini usa (openai/openai.go's httpError
// documenta esa peculiaridad; aquí no hay ningún gateway de terceros de por
// medio, así que no tiene equivalente).
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
		switch {
		case env.Error.Status != "":
			e.Code = env.Error.Status
		case env.Error.Code != 0:
			e.Code = strconv.Itoa(env.Error.Code)
		}
	}
	if e.Message == "" {
		e.Message = collapseJSON(string(raw), 300)
	}
	if e.Message == "" {
		e.Message = http.StatusText(resp.StatusCode)
	}

	// Un 401/403 con clave puesta y uno sin clave son problemas distintos,
	// igual que en los otros dos dialectos: el mensaje tiene que decir
	// cuál es. Gemini responde 400 "API key not valid" en algunos casos y
	// 403 en otros según el motivo exacto; ninguno de los dos códigos es
	// tan inequívoco como el 401 de Anthropic/OpenAI, así que la
	// comprobación se limita a la ausencia de clave, que sí es inequívoca.
	if p.set.APIKey == "" && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) {
		e.Err = provider.ErrNoAPIKey
		e.Message = "the service requires authentication and no api_key is configured for " + p.set.ID
	}
	return e
}

// collapseJSON es una copia de anthropic.collapseJSON/openai.collapseJSON:
// aplana un cuerpo de varias líneas en una sola para que truncarlo conserve
// información en vez de puntuación.
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

// netError envuelve un fallo de red como error reintentable, igual que en
// los otros dos dialectos.
func (p *Provider) netError(err error) *provider.Error {
	return &provider.Error{
		Provider:  p.set.ID,
		Retryable: true,
		Message:   err.Error(),
		Err:       err,
	}
}

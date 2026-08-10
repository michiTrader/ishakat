// declarative.go implements §19.2's rung 1 of the crystallization ladder: a
// declarative tool is an HTTP request template on disk (`tool.toml`), not
// code — the model fills in arguments, it never writes executable logic, so
// there is no possibility of a generated `rm -rf` hiding in one. This is
// deliberately the primary path (§19.2: "rung 1 is the primary path and the
// one nobody else builds") and, per docs/PLAN.md's own step ordering, the
// piece that must exist and be provably correct *before* Step 21 lets a
// model write into it at all — "building the factory before the factory".
//
// Parsing uses the TOML library already in go.mod (github.com/BurntSushi/toml)
// and nothing else: §6.4's budget for rung 1 is
// encoding/json + crypto/hmac + crypto/sha256 + text/template, all standard
// library, so this file adds zero new dependencies.
//
// Directory discovery mirrors internal/skills.Discover on purpose — same
// shape, same leniency (a missing directory is not an error, a directory
// without the expected file is silently skipped, only the first read/parse
// failure is reported) — because §20.11 item 5 asks that a tool's on-disk
// layout already be a valid "package" (id-named directory, manifest at the
// root, no absolute paths, no machine-specific state in the manifest
// itself), and skills already settled what that discovery contract should
// look like for the sibling rung-0 case.
package tools

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/BurntSushi/toml"
)

// defaultDeclarativeTimeout bounds how long a single declarative call may
// wait for a response. Slightly more generous than Fetch's
// defaultFetchTimeout (10s): a declarative tool's endpoint is a specific,
// known API rather than an arbitrary fetched page, and some real services
// (order books, account summaries) are legitimately slower than a static
// document.
const defaultDeclarativeTimeout = 15 * time.Second

// maxDeclarativeBodyBytes and maxDeclarativeOutputBytes mirror Fetch's own
// ceilings (fetch.go) for the same reason: a response has to be bounded
// before it can be read at all, and the final text handed to the model has
// its own, usually much smaller, ceiling on top of that — extraction
// narrows a response but must not be trusted to always narrow it enough on
// its own (a manifest with no [response].extract at all returns the whole
// body).
const maxDeclarativeBodyBytes = 2 << 20
const maxDeclarativeOutputBytes = 32 << 10

// ManifestFileName is the file every declarative tool directory must
// contain at its root. §20.11 item 5's "manifest at the root" requirement
// is what makes this a fixed, well-known name rather than something
// Discover has to search for.
const ManifestFileName = "tool.toml"

// ParamSpec describes one parameter a declarative tool accepts, the
// `[params.<name>]` table of a manifest. It maps onto the same JSON-schema
// `prop` shape every native tool's Parameters() already builds (schema.go),
// so a model sees a declarative tool's arguments in exactly the same form
// as a native one's — progressive disclosure and tool-calling do not need to
// know which rung produced a given ToolDef.
type ParamSpec struct {
	Type        string   `toml:"type"`
	Required    bool     `toml:"required"`
	Description string   `toml:"description"`
	Enum        []string `toml:"enum"`
	// Default is used to fill the template when the model omits an optional
	// parameter, so `{{.coin}}` still resolves to something rather than the
	// empty string silently changing the request's shape.
	Default string `toml:"default"`
}

// AuthSpec names a signing scheme by identifier, never by code: §19.2 is
// explicit that "signing is a named scheme implemented in Go, NEVER
// model-generated code" — a manifest can select "bearer" or "hmac_sha256",
// it cannot embed the bytes that perform either. Scheme is looked up
// against a small, fixed switch in buildAuth below; an unknown scheme name
// is a manifest error at run time (ErrorResult, not a Go error — the model
// can see and react to a typo here the same way it reacts to any other
// tool failure).
type AuthSpec struct {
	Scheme string `toml:"scheme"`
	// KeyEnv and SecretEnv name environment variables holding credential
	// material — never the credential itself, so a manifest committed to a
	// dotfiles repo or shared between machines carries no secret (§20.11
	// item 5's "no machine-specific state" extended to the more dangerous
	// case of no *secret* state).
	KeyEnv    string `toml:"key_env"`
	SecretEnv string `toml:"secret_env"`
	// Header names the HTTP header a "header" or "hmac_sha256" scheme
	// writes its credential/signature into. Defaults are scheme-specific;
	// see buildAuth.
	Header string `toml:"header"`
	// Param names the query parameter a "query" scheme writes its
	// credential into.
	Param string `toml:"param"`
}

// RequestSpec is the `[request]` table: the HTTP call a declarative tool
// makes, with `{{.param}}`-templated pieces filled in from the model's
// arguments at call time (text/template, stdlib — §6.4). Every value here
// is a template source string, evaluated fresh on every Run so the same
// manifest can be reused with different arguments across calls.
type RequestSpec struct {
	Method  string            `toml:"method"`
	URL     string            `toml:"url"`
	Query   map[string]string `toml:"query"`
	Headers map[string]string `toml:"headers"`
	Body    string            `toml:"body"`
	Auth    AuthSpec          `toml:"auth"`
}

// ResponseSpec is the `[response]` table: how to turn the HTTP response
// body into the (much shorter) text a model actually needs to see. Extract
// is a small dotted-path expression (see extractPath's own doc comment for
// exactly what subset it supports and why); empty means "return the whole
// body", which is always a safe default for a manifest that has not
// bothered to narrow the response yet.
type ResponseSpec struct {
	Extract string `toml:"extract"`
}

// OriginSpec is the `[origin]` table §19.6/§19.8 make mandatory provenance:
// who or what created this tool, and why. Step 20 only has to read and
// preserve it — the governance that requires it to be present and accurate
// (gate 1, tainted-context marking) is Step 21's job — but recording it now
// means a hand-written tool.toml already exercises the field a
// model-written one will need to populate later.
type OriginSpec struct {
	CreatedBy   string   `toml:"created_by"`
	Reason      string   `toml:"reason"`
	Repetitions int      `toml:"repetitions"`
	SessionID   string   `toml:"session_id"`
	Sources     []string `toml:"sources"`
}

// Manifest is one parsed `tool.toml`. Note what is deliberately *not* a
// field here: `[package]` (§20.11 item 1) and `[selftest]` (§19.2's
// illustrative example, whose actual quarantine mechanism is Step 21's
// `tool_probe`). Both are accepted and silently ignored simply by not
// decoding them into anything — see parseManifest's own doc comment for why
// that is enough, and why it is the *correct* way to satisfy "reserved
// table, no 'ignored key' warning" rather than an accident of omission.
type Manifest struct {
	Name        string `toml:"name"`
	Description string `toml:"description"`
	Version     int    `toml:"version"`
	// Danger is the manifest author's own claim. §19.5 rule 2 is explicit
	// that danger is *inferred*, never self-declared: this field is read
	// but a DeclarativeTool's actual Danger() never simply returns it
	// unchanged — see inferDanger's doc comment for the one-way ratchet
	// that applies to it (a manifest may only ever raise its own risk
	// tier, never lower what inference already decided).
	Danger string `toml:"danger"`

	Origin   OriginSpec           `toml:"origin"`
	Params   map[string]ParamSpec `toml:"params"`
	Request  RequestSpec          `toml:"request"`
	Response ResponseSpec         `toml:"response"`

	// RequiresCaps and MinContext are §20.11 item 4's forward-compat pair:
	// a declarative tool may name capabilities (from the fixed vocabulary
	// capNames below) and a minimum context window the *active model* must
	// have for this tool to make sense to offer at all. See Caps and
	// Manifest.Unsatisfied for how a caller (internal/app, which already
	// knows the resolved model's real catalog.Caps) checks this.
	RequiresCaps []string `toml:"requires_caps"`
	MinContext   int      `toml:"min_context"`

	// Dir is the tool's own directory, set by Discover — never decoded
	// from the TOML itself (`toml:"-"`), matching skills.Skill.Dir's
	// identical role: what a future `tool_edit`/`tool_delete` (Step 21)
	// or an audit view needs to find the manifest again on disk.
	Dir string `toml:"-"`
}

// DeclarativeResult is what Discover found, mirroring skills.Result's own
// shape field for field (Skills/Warn there, Tools/Warn here) so a caller
// already familiar with one reads the other for free.
type DeclarativeResult struct {
	// Tools are every subdirectory of the scanned directory that contained
	// a readable, parseable tool.toml, sorted by Name for a stable listing
	// run to run.
	Tools []Manifest
	// Warn names the first subdirectory whose tool.toml exists but could
	// not be read or parsed. A missing tool.toml, or the directory itself
	// not existing, is not a warning — the ordinary case for most
	// installs, which have created no tools of their own yet.
	Warn string
}

// DiscoverDeclarative reads every immediate subdirectory of dir for a
// tool.toml and parses it. dir not existing at all is not an error, exactly
// like skills.Discover's identical contract for the sibling rung-0 case.
func DiscoverDeclarative(dir string) DeclarativeResult {
	var res DeclarativeResult
	if dir == "" {
		return res
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return res
		}
		res.Warn = "could not read tools directory (" + dir + "): " + err.Error()
		return res
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		toolDir := filepath.Join(dir, entry.Name())
		file := filepath.Join(toolDir, ManifestFileName)

		body, err := os.ReadFile(file)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			if res.Warn == "" {
				res.Warn = "could not read " + file + ": " + err.Error()
			}
			continue
		}

		m, err := parseManifest(body)
		if err != nil {
			if res.Warn == "" {
				res.Warn = "could not parse " + file + ": " + err.Error()
			}
			continue
		}

		if m.Name == "" {
			// A manifest without its own name falls back to the directory
			// name, the same leniency skills.Discover applies — a tool
			// author who forgot the field still gets a usable, stable
			// identifier rather than every unnamed tool colliding on "".
			m.Name = entry.Name()
		}
		m.Dir = toolDir
		res.Tools = append(res.Tools, m)
	}

	sort.Slice(res.Tools, func(i, j int) bool { return res.Tools[i].Name < res.Tools[j].Name })
	return res
}

// parseManifest decodes raw TOML into a Manifest.
//
// Deliberately not called: toml.MetaData.Undecoded(). internal/config/load.go
// calls it and turns every result into an "ignored key: …" warning, because
// config.toml's schema is meant to be exhaustive — an unrecognized key there
// is very likely a typo. A tool.toml manifest is the opposite case on
// purpose: §20.11 item 1 asks that `[package]` be "accepted-and-ignored...
// (no 'ignored key' warning)" so that a future community-layer field can be
// added to the format without every existing hand-written manifest suddenly
// producing warnings, and §19.2's own illustrative manifest already has a
// `[selftest]` table this package does not need to understand yet (that is
// Step 21's `tool_probe`, not Step 20's job). Simply not checking Undecoded
// gives both of those exactly the silence they ask for, for free — no
// special-cased "reserved" field needed for either.
func parseManifest(body []byte) (Manifest, error) {
	var m Manifest
	if _, err := toml.Decode(string(body), &m); err != nil {
		return Manifest{}, fmt.Errorf("invalid TOML: %w", err)
	}
	if strings.TrimSpace(m.Request.URL) == "" {
		return Manifest{}, fmt.Errorf("missing [request].url")
	}
	if strings.TrimSpace(m.Request.Method) == "" {
		m.Request.Method = http.MethodGet
	}
	return m, nil
}

// capNames is the fixed vocabulary a manifest's requires_caps may name,
// deliberately the same field names catalog.Caps already uses (lower-cased,
// snake_case for the two-word one) so a caller translating a resolved
// model's real capabilities into this package's Caps type (below) never has
// to guess at a mapping.
var capNames = map[string]func(Caps) bool{
	"tools":       func(c Caps) bool { return c.Tools },
	"vision":      func(c Caps) bool { return c.Vision },
	"reasoning":   func(c Caps) bool { return c.Reasoning },
	"streaming":   func(c Caps) bool { return c.Streaming },
	"json_schema": func(c Caps) bool { return c.JSONSchema },
	"attachments": func(c Caps) bool { return c.Attachments },
}

// Caps is the minimal capability/context vector a declarative tool's
// requires_caps/min_context can be checked against (§20.11 item 4).
// Deliberately independent of catalog.Caps rather than importing it:
// internal/tools must never import internal/catalog any more than it
// imports internal/config (see Fetch's own doc comment on why a
// cross-cutting concern here takes the minimal, purpose-built argument it
// needs rather than a whole external type) — the caller (internal/app,
// which already resolves a model and therefore already has its real
// catalog.Caps and catalog.Model.Context in hand) is what translates one
// into the other, field by field, exactly like it already does for
// config.Egress -> Fetch.Allow/AllowAll.
type Caps struct {
	Tools       bool
	Vision      bool
	Reasoning   bool
	Streaming   bool
	JSONSchema  bool
	Attachments bool
	// Context is the active model's context window in tokens, 0 meaning
	// unknown. An unknown context never fails MinContext — see Unsatisfied.
	Context int
}

// Unsatisfied reports which of m's requires_caps/min_context requirements
// against are not met, or nil if all of them are (including the common case
// of a manifest that declares none at all). A non-nil, non-empty result
// names each missing requirement in a form suitable for a warning or a
// "why was this tool hidden" explanation — never a Go error, since an
// incompatible tool being temporarily unavailable for the active model is
// an ordinary, expected outcome (§20.11 item 4 mirrors engine.CheckSwap's
// MissingCaps conflict, which reports rather than fails for the same
// reason).
//
// An unrecognized name in RequiresCaps is itself reported as unsatisfied,
// rather than silently ignored — a manifest requiring a capability this
// build does not know how to check is exactly the kind of typo Step 21's
// gate 2 (human review) exists to catch before the tool is ever used.
func (m Manifest) Unsatisfied(against Caps) []string {
	var missing []string
	for _, name := range m.RequiresCaps {
		key := strings.ToLower(strings.TrimSpace(name))
		check, ok := capNames[key]
		if !ok {
			missing = append(missing, fmt.Sprintf("unknown requires_caps entry %q", name))
			continue
		}
		if !check(against) {
			missing = append(missing, fmt.Sprintf("requires capability %q", key))
		}
	}
	if m.MinContext > 0 && against.Context > 0 && against.Context < m.MinContext {
		missing = append(missing, fmt.Sprintf("requires min_context %d, active model has %d", m.MinContext, against.Context))
	}
	return missing
}

// inferDanger implements §19.5 rule 2, verbatim: "danger is inferred, never
// self-declared. If the manifest uses a non-GET method, or touches a host
// on the finance list, ishakat assigns danger: high itself, overriding
// whatever the model wrote. A model may not lower its own permissions."
//
// That last sentence is the ratchet this function encodes: a manifest's own
// Danger field may only ever raise the tier inference would have assigned,
// never lower it. A GET request to an ordinary host defaults to Low
// (matching Fetch's own DangerLow for the identical "read-only network
// call" shape) unless the manifest itself claims Medium or High, in which
// case that higher claim is honoured; a non-GET method or a finance-list
// host is unconditionally High, and no manifest value can talk it down from
// that.
func inferDanger(m Manifest) Danger {
	forcedHigh := !strings.EqualFold(m.Request.Method, http.MethodGet) || touchesFinanceHost(m.Request.URL)

	declared := DangerLow
	switch strings.ToLower(strings.TrimSpace(m.Danger)) {
	case "medium":
		declared = DangerMedium
	case "high":
		declared = DangerHigh
	}

	if forcedHigh {
		return DangerHigh
	}
	return declared
}

// financeHosts is a deliberately small, illustrative set of hostname
// substrings §19.5 rule 2's "finance list" checks against. It exists so the
// mechanism — a model cannot write a manifest that quietly downgrades a
// money-touching tool to danger: low by pointing it at a GET endpoint on a
// known exchange — is real and testable in Step 20, not so that this list
// is exhaustive; broadening it (or making it configurable) is exactly the
// kind of refinement Step 21's governance work is expected to make, once
// there are real, model-written manifests to learn from rather than one
// illustrative example (§19.2's own bybit_balance).
var financeHosts = []string{
	"bybit.com", "binance.com", "coinbase.com", "kraken.com", "okx.com",
	"kucoin.com", "bitfinex.com", "bitstamp.net", "gemini.com", "gate.io",
}

func touchesFinanceHost(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	for _, fh := range financeHosts {
		if host == fh || strings.HasSuffix(host, "."+fh) {
			return true
		}
	}
	return false
}

// DeclarativeTool is the runnable Tool built from one Manifest. It composes
// with the exact same egress-allowlist shape Fetch already uses
// (Allow/AllowAll — §19.8 mitigation 4: "a tool.toml's url host must be in
// [tools.egress].allow"), so a declarative tool and fetch are governed by
// the same one list in config.toml rather than two.
type DeclarativeTool struct {
	Manifest Manifest

	Allow    []string
	AllowAll bool

	// HTTPClient is the client used to perform the request, nil meaning
	// http.DefaultClient — identical contract to Fetch.HTTPClient, and for
	// the same reason: tests need to point this at an httptest.Server.
	HTTPClient *http.Client

	// Now, when set, replaces time.Now for the hmac_sha256 auth scheme's
	// timestamp — nil meaning the real clock. Exists purely so
	// declarative_test.go can assert on an exact signature instead of a
	// moving target.
	Now func() time.Time
}

var _ Tool = DeclarativeTool{}

func (d DeclarativeTool) Name() string        { return d.Manifest.Name }
func (d DeclarativeTool) Description() string { return d.Manifest.Description }
func (d DeclarativeTool) Danger() Danger      { return inferDanger(d.Manifest) }

// Parameters builds the JSON-schema object every Tool must expose, from the
// manifest's own [params] table — the same objectSchema/prop machinery
// every native tool's Parameters() already uses (schema.go), so a
// declarative tool's arguments look identical to a native one's from the
// model's point of view.
func (d DeclarativeTool) Parameters() json.RawMessage {
	props := make(map[string]prop, len(d.Manifest.Params))
	var required []string
	// Sorted iteration: map order is not stable, and an unstable schema
	// would make identical manifests produce byte-different Parameters()
	// output across runs — cheap to avoid, matching registry.go's own
	// stated preference for a system prompt that "doesn't reshuffle
	// between runs".
	names := make([]string, 0, len(d.Manifest.Params))
	for name := range d.Manifest.Params {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		spec := d.Manifest.Params[name]
		t := spec.Type
		if t == "" {
			t = "string"
		}
		props[name] = prop{Type: t, Description: spec.Description, Enum: spec.Enum}
		if spec.Required {
			required = append(required, name)
		}
	}
	return objectSchema(props, required...)
}

func (d DeclarativeTool) clock() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// templateData builds the string map a request's `{{.name}}` templates
// execute against, from args (the model's JSON call arguments) filled in
// over the manifest's own declared parameter defaults. Every declared
// parameter is present in the returned map even when the model omitted it
// — an optional parameter's template still executes to something (its
// Default, or "") rather than text/template's own "<no value>" filler for
// a genuinely absent map key.
func (d DeclarativeTool) templateData(args json.RawMessage) (map[string]string, error) {
	var raw map[string]json.RawMessage
	if len(args) > 0 {
		if err := json.Unmarshal(args, &raw); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
	}

	data := make(map[string]string, len(d.Manifest.Params))
	for name, spec := range d.Manifest.Params {
		data[name] = spec.Default
	}
	for name, v := range raw {
		var s string
		// Scalars unmarshal into a Go string cleanly whether the model
		// sent a JSON string, number or bool; anything structured
		// (object/array) is passed through as its own compact JSON text
		// instead of failing the whole call, on the theory that a
		// template author who wrote {{.filters}} expecting a raw JSON
		// fragment should get one rather than an error.
		var asString string
		if err := json.Unmarshal(v, &asString); err == nil {
			s = asString
		} else {
			s = strings.TrimSpace(string(v))
		}
		data[name] = s
	}
	return data, nil
}

// renderTemplate executes one manifest template source against data.
// text/template's own escaping and control-flow features are all available
// here on purpose — {{if .coin}}...{{end}} inside a body template is a
// deliberate and useful pattern, not a security concern, since a template's
// *source* only ever comes from a manifest a human already approved (Step
// 21's gate 2), never from the model's per-call arguments.
func renderTemplate(name, src string, data map[string]string) (string, error) {
	if src == "" {
		return "", nil
	}
	tmpl, err := template.New(name).Parse(src)
	if err != nil {
		return "", fmt.Errorf("template %q: %w", name, err)
	}
	var b strings.Builder
	if err := tmpl.Execute(&b, data); err != nil {
		return "", fmt.Errorf("template %q: %w", name, err)
	}
	return b.String(), nil
}

// buildAuth applies the manifest's [request.auth] scheme to req, mutating
// its headers/query in place. Every scheme here is a fixed, named,
// Go-implemented behaviour (§19.2: "signing is a named scheme implemented
// in Go, NEVER model-generated code") — a manifest selects one by name, it
// cannot supply its own signing logic.
func buildAuth(req *http.Request, spec AuthSpec, now time.Time) error {
	switch strings.ToLower(strings.TrimSpace(spec.Scheme)) {
	case "", "none":
		return nil
	case "bearer":
		secret, err := lookupEnv(spec.SecretEnv, spec.KeyEnv)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+secret)
		return nil
	case "header":
		secret, err := lookupEnv(spec.SecretEnv, spec.KeyEnv)
		if err != nil {
			return err
		}
		header := spec.Header
		if header == "" {
			header = "Authorization"
		}
		req.Header.Set(header, secret)
		return nil
	case "query":
		secret, err := lookupEnv(spec.SecretEnv, spec.KeyEnv)
		if err != nil {
			return err
		}
		param := spec.Param
		if param == "" {
			param = "api_key"
		}
		q := req.URL.Query()
		q.Set(param, secret)
		req.URL.RawQuery = q.Encode()
		return nil
	case "hmac_sha256":
		return buildHMACAuth(req, spec, now)
	default:
		return fmt.Errorf("unknown auth scheme %q", spec.Scheme)
	}
}

// buildHMACAuth is the one non-trivial named scheme §19.2 budgets
// crypto/hmac + crypto/sha256 for. It is deliberately generic — not tied to
// any one exchange's exact signing convention, since §16.1 forbids a
// runnable, finance-specific example living in this repository at all — and
// signs a canonical string of "timestamp\nmethod\npath\nquery\nbody" with
// the secret from spec.SecretEnv, writing three headers: the API key
// (spec.Header or "X-API-KEY"), the hex-encoded signature
// ("X-API-SIGNATURE"), and the timestamp itself ("X-API-TIMESTAMP") so the
// receiving service can verify it. A real integration's manifest may need a
// different canonical form; that is exactly the kind of narrow,
// service-specific variation §19.3 reserves for a rung-2 script tool
// instead, once rung 1's fixed shape stops being enough.
func buildHMACAuth(req *http.Request, spec AuthSpec, now time.Time) error {
	key, err := lookupEnv(spec.KeyEnv, "")
	if err != nil {
		return fmt.Errorf("hmac_sha256 requires key_env: %w", err)
	}
	secret, err := lookupEnv(spec.SecretEnv, "")
	if err != nil {
		return fmt.Errorf("hmac_sha256 requires secret_env: %w", err)
	}

	timestamp := strconv.FormatInt(now.UnixMilli(), 10)
	var body string
	if req.GetBody != nil {
		if rc, err := req.GetBody(); err == nil {
			b := make([]byte, 0)
			buf := make([]byte, 4096)
			for {
				n, rerr := rc.Read(buf)
				if n > 0 {
					b = append(b, buf[:n]...)
				}
				if rerr != nil {
					break
				}
			}
			rc.Close()
			body = string(b)
		}
	}
	canonical := strings.Join([]string{
		timestamp,
		strings.ToUpper(req.Method),
		req.URL.Path,
		req.URL.RawQuery,
		body,
	}, "\n")

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(canonical))
	signature := hex.EncodeToString(mac.Sum(nil))

	header := spec.Header
	if header == "" {
		header = "X-API-KEY"
	}
	req.Header.Set(header, key)
	req.Header.Set("X-API-SIGNATURE", signature)
	req.Header.Set("X-API-TIMESTAMP", timestamp)
	return nil
}

func lookupEnv(primary, fallback string) (string, error) {
	name := primary
	if name == "" {
		name = fallback
	}
	if name == "" {
		return "", fmt.Errorf("no environment variable configured for this credential")
	}
	v, ok := os.LookupEnv(name)
	if !ok || v == "" {
		return "", fmt.Errorf("missing $%s", name)
	}
	return v, nil
}

// Run executes the declarative tool's HTTP request: templates fill in the
// URL, query, headers and body from args; the configured auth scheme
// signs the request; the egress allowlist is checked exactly like Fetch's
// own (§19.8 mitigation 4 — the same list governs both); the response is
// read, bounded, and narrowed by [response].extract when the manifest sets
// one.
//
// A returned Go error means the call could not even be attempted (bad
// arguments JSON, a template that failed to parse, an unregistered auth
// scheme — all of these are manifest/argument problems the caller already
// treats as tool-error data one level up, per tool.go's own doc comment).
// An HTTP-level failure (host unreachable, non-2xx status) is a Result with
// IsError set instead, since that is an operation that was attempted and
// failed, which the model should see and can react to.
func (d DeclarativeTool) Run(ctx context.Context, rawArgs json.RawMessage) (Result, error) {
	data, err := d.templateData(rawArgs)
	if err != nil {
		return Result{}, fmt.Errorf("declarative(%s): %w", d.Manifest.Name, err)
	}

	rawURL, err := renderTemplate("url", d.Manifest.Request.URL, data)
	if err != nil {
		return Result{}, fmt.Errorf("declarative(%s): %w", d.Manifest.Name, err)
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ErrorResult(fmt.Sprintf("could not parse %q as a URL: %v", rawURL, err)), nil
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return ErrorResult(fmt.Sprintf("refused to call %q: only http and https are supported, got scheme %q", rawURL, parsed.Scheme)), nil
	}
	if parsed.Hostname() == "" {
		return ErrorResult(fmt.Sprintf("refused to call %q: no host in URL", rawURL)), nil
	}
	if !d.AllowAll && !hostAllowed(parsed.Hostname(), d.Allow) {
		return ErrorResult(fmt.Sprintf(
			"refused to call %s: host %q is not on the egress allowlist. A new host is its own separate decision (see [tools.egress].allow in config.toml) — this is not a per-call approval.",
			rawURL, parsed.Hostname())), nil
	}

	query := parsed.Query()
	// Sorted iteration for the same determinism reason Parameters() sorts
	// its own map keys: two calls with identical arguments should build
	// byte-identical requests, which matters for both testability and for
	// a future audit trail comparing requests across runs.
	queryKeys := make([]string, 0, len(d.Manifest.Request.Query))
	for k := range d.Manifest.Request.Query {
		queryKeys = append(queryKeys, k)
	}
	sort.Strings(queryKeys)
	for _, k := range queryKeys {
		v, err := renderTemplate("query."+k, d.Manifest.Request.Query[k], data)
		if err != nil {
			return Result{}, fmt.Errorf("declarative(%s): %w", d.Manifest.Name, err)
		}
		// A template that rendered to empty (an omitted optional
		// parameter with no default) is dropped from the query string
		// entirely, rather than sent as "coin=" — the same reasoning
		// §19.2's own bybit_balance manifest relies on for its optional
		// `coin` filter to mean "no filter" when unset.
		if v != "" {
			query.Set(k, v)
		}
	}
	parsed.RawQuery = query.Encode()

	body, err := renderTemplate("body", d.Manifest.Request.Body, data)
	if err != nil {
		return Result{}, fmt.Errorf("declarative(%s): %w", d.Manifest.Name, err)
	}

	method := d.Manifest.Request.Method
	if method == "" {
		method = http.MethodGet
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, defaultDeclarativeTimeout)
	defer cancel()

	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(timeoutCtx, method, parsed.String(), bodyReader)
	if err != nil {
		return Result{}, fmt.Errorf("declarative(%s): could not build request: %w", d.Manifest.Name, err)
	}
	if body != "" {
		// GetBody lets buildHMACAuth read the body to sign it without
		// consuming the reader Run itself needs to send — the same
		// pattern http.NewRequest already sets up automatically for a
		// *bytes.Reader/*strings.Reader body on the standard library
		// side; set explicitly here since NewRequestWithContext's own
		// auto-detection only covers a few concrete reader types and a
		// caller that later swaps bodyReader's construction should not
		// silently lose this.
		b := body
		req.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(b)), nil
		}
	}

	headerKeys := make([]string, 0, len(d.Manifest.Request.Headers))
	for k := range d.Manifest.Request.Headers {
		headerKeys = append(headerKeys, k)
	}
	sort.Strings(headerKeys)
	for _, k := range headerKeys {
		v, err := renderTemplate("header."+k, d.Manifest.Request.Headers[k], data)
		if err != nil {
			return Result{}, fmt.Errorf("declarative(%s): %w", d.Manifest.Name, err)
		}
		req.Header.Set(k, v)
	}
	if body != "" && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("User-Agent", "ishakat/declarative (+https://github.com/MichiTrader/ishakat)")

	if err := buildAuth(req, d.Manifest.Request.Auth, d.clock()); err != nil {
		return Result{}, fmt.Errorf("declarative(%s): auth: %w", d.Manifest.Name, err)
	}

	client := d.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			// Same distinction Fetch.Run already draws: the caller's own
			// context was cancelled, not just this call's own timeout, so
			// this surfaces as a Go error for the agent loop's
			// cancellation path rather than as tool-error data.
			return Result{}, ctx.Err()
		}
		return ErrorResult(fmt.Sprintf("could not call %s: %v", rawURL, err)), nil
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxDeclarativeBodyBytes+1))
	if err != nil {
		return ErrorResult(fmt.Sprintf("could not read response body from %s: %v", rawURL, err)), nil
	}
	truncated := len(respBody) > maxDeclarativeBodyBytes
	if truncated {
		respBody = respBody[:maxDeclarativeBodyBytes]
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ErrorResult(fmt.Sprintf("%s responded with status %d: %s", rawURL, resp.StatusCode, truncateForError(respBody))), nil
	}

	out := string(respBody)
	if extract := strings.TrimSpace(d.Manifest.Response.Extract); extract != "" {
		extracted, err := extractJSON(respBody, extract)
		if err != nil {
			// An extract expression that fails against a real response is
			// reported as tool-error data, not silently swallowed into the
			// raw body — a manifest author (human, per Step 21's gate 2)
			// needs to see that the path they wrote does not match what
			// the service actually returns.
			return ErrorResult(fmt.Sprintf("[response].extract %q failed: %v", extract, err)), nil
		}
		out = extracted
	}

	if len(out) > maxDeclarativeOutputBytes {
		out = out[:maxDeclarativeOutputBytes] + "\n\n…[truncated: response was larger than this tool's ceiling]"
	} else if truncated {
		out += fmt.Sprintf("\n\n…[truncated: %s's response body was larger than this tool's ceiling]", rawURL)
	}
	return OKResult(out), nil
}

// truncateForError bounds how much of a non-2xx response body an error
// message quotes, so a service that answers a 500 with a full HTML error
// page does not itself blow past the tool's own output ceiling.
func truncateForError(body []byte) string {
	const max = 500
	s := strings.TrimSpace(string(body))
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

// extractJSON evaluates a small, deliberately restricted dotted-path
// expression against a JSON response body and returns the result
// re-encoded as compact JSON text.
//
// §19.2's own illustrative manifest uses
// "result.list[0].coin[*].{coin, walletBalance, usdValue}" as its
// [response].extract value — a JMESPath expression. Adding a JMESPath
// library would violate §6.4's zero-new-dependencies budget for the whole
// of Phase 2.5, and a *general* JMESPath evaluator is a project on its own.
// The decision made here, for Step 20's first cut, is option (a) from the
// three considered (hand-roll a minimal subset vs. skip extraction
// entirely vs. named strategies): implement exactly the subset that one
// illustrative expression exercises, since it already covers what §19.2's
// own economics table prices rung 1's value on (turning ~1.200 tok of raw
// JSON into ~80 tok of filtered extract) — a wildcard array projection into
// a small object shape. The supported grammar, in full:
//
//	path       := segment ("." segment)*
//	segment    := key ( "[" index "]" )? | "{" field ("," field)* "}"
//	index      := digits | "*"
//	key, field := unquoted identifier (letters, digits, underscore)
//
// That is: dotted field access, a numeric or "*" array index, and a
// trailing "{a, b, c}" projection that narrows a matched object (or every
// object in a matched array) down to just those fields. It deliberately
// does not support filters, slices, functions, or a projection anywhere but
// at the end of the path — exactly what would need to grow if a
// model-written manifest (Step 21) ever needs more, at which point the
// right move is very likely still "extend this evaluator's small grammar
// by one production", not "add a dependency", since the whole point of
// rung 1 is that its interpreter stays small enough to read in one sitting.
func extractJSON(body []byte, expr string) (string, error) {
	var data any
	if err := json.Unmarshal(body, &data); err != nil {
		return "", fmt.Errorf("response is not valid JSON: %w", err)
	}

	segments, err := parseExtractPath(expr)
	if err != nil {
		return "", err
	}

	result, err := evalExtractPath(data, segments)
	if err != nil {
		return "", err
	}

	out, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("could not encode extracted result: %w", err)
	}
	return string(out), nil
}

// extractSegment is one step of a parsed extract path: a field name, an
// optional array index ("" for none, a non-negative integer, or "*" for
// every element), and — only ever on the final segment — a projection list
// narrowing a matched object down to specific fields.
type extractSegment struct {
	Field      string
	Index      string // "" | "*" | a decimal integer
	Projection []string
}

// parseExtractPath parses extractJSON's small grammar (see its own doc
// comment) into a sequence of extractSegment. Kept entirely separate from
// evaluation so a caller could validate a manifest's extract expression at
// authoring time (a natural fit for Step 21's tool_probe self-test) without
// needing a real response body to test it against.
func parseExtractPath(expr string) ([]extractSegment, error) {
	var segments []extractSegment
	for _, part := range strings.Split(expr, ".") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("empty path segment in %q", expr)
		}

		seg := extractSegment{}
		// A "{a, b, c}" projection, if present, must be the trailing
		// suffix of this segment (either the whole segment, as in
		// ".{coin, x}", or attached right after an index, as the
		// illustrative manifest's own "coin[*].{coin, walletBalance,
		// usdValue}" does across two segments — the projection there is
		// its own final segment with no Field/Index at all).
		if strings.HasPrefix(part, "{") {
			if !strings.HasSuffix(part, "}") {
				return nil, fmt.Errorf("unterminated projection in segment %q", part)
			}
			inner := part[1 : len(part)-1]
			for _, f := range strings.Split(inner, ",") {
				f = strings.TrimSpace(f)
				if f == "" {
					return nil, fmt.Errorf("empty field name in projection %q", part)
				}
				seg.Projection = append(seg.Projection, f)
			}
			segments = append(segments, seg)
			continue
		}

		if br := strings.IndexByte(part, '['); br >= 0 {
			if !strings.HasSuffix(part, "]") {
				return nil, fmt.Errorf("unterminated index in segment %q", part)
			}
			seg.Field = part[:br]
			seg.Index = part[br+1 : len(part)-1]
			if seg.Index != "*" {
				if _, err := strconv.Atoi(seg.Index); err != nil {
					return nil, fmt.Errorf("invalid array index %q in segment %q", seg.Index, part)
				}
			}
		} else {
			seg.Field = part
		}
		if seg.Field == "" {
			return nil, fmt.Errorf("empty field name in segment %q", part)
		}
		segments = append(segments, seg)
	}
	return segments, nil
}

// evalExtractPath walks data (already-unmarshalled JSON: map[string]any,
// []any, or a scalar) according to segments, returning the matched value —
// a plain value, or, once a "*" index has been consumed, a []any of one
// result per matched element (JMESPath's own "projection" semantics for
// exactly this shape, which is why the grammar comment above calls it
// that).
func evalExtractPath(data any, segments []extractSegment) (any, error) {
	current := data
	for i, seg := range segments {
		if len(seg.Projection) > 0 {
			return applyProjection(current, seg.Projection)
		}

		if seg.Field != "" {
			obj, ok := current.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("segment %q: expected an object, got %T", seg.Field, current)
			}
			v, ok := obj[seg.Field]
			if !ok {
				return nil, fmt.Errorf("segment %q: field not found", seg.Field)
			}
			current = v
		}

		if seg.Index == "" {
			continue
		}
		arr, ok := current.([]any)
		if !ok {
			return nil, fmt.Errorf("segment %q[%s]: expected an array, got %T", seg.Field, seg.Index, current)
		}
		if seg.Index == "*" {
			// The remaining segments apply to every element; recurse once
			// per element and collect. This only makes sense as the
			// second-to-last production before a trailing projection
			// (exactly the illustrative manifest's own shape), so a "*"
			// followed by more field/index segments (rather than a
			// projection) walks each element the same way.
			rest := segments[i+1:]
			out := make([]any, 0, len(arr))
			for _, elem := range arr {
				v, err := evalExtractPath(elem, rest)
				if err != nil {
					return nil, err
				}
				out = append(out, v)
			}
			return out, nil
		}
		idx, _ := strconv.Atoi(seg.Index) // already validated by parseExtractPath
		if idx < 0 || idx >= len(arr) {
			return nil, fmt.Errorf("segment %q[%d]: index out of range (array has %d elements)", seg.Field, idx, len(arr))
		}
		current = arr[idx]
	}
	return current, nil
}

// applyProjection narrows value down to fields: value itself when it is a
// single object, or the same narrowing applied to every element when value
// is an array of objects (the "coin[*].{...}" case, where evalExtractPath's
// own "*" handling has already produced a []any by the time the trailing
// projection segment is reached).
func applyProjection(value any, fields []string) (any, error) {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(fields))
		for _, f := range fields {
			out[f] = v[f] // absent field becomes nil/omitted-on-marshal via omitempty-less map access -> nil value
		}
		return out, nil
	case []any:
		out := make([]any, 0, len(v))
		for _, elem := range v {
			projected, err := applyProjection(elem, fields)
			if err != nil {
				return nil, err
			}
			out = append(out, projected)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("projection %v: expected an object or array, got %T", fields, value)
	}
}

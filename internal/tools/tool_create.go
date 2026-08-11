// tool_create.go implements §19.5/§19.6's gate 1 + write path: "writes
// tool.toml (+ script) and its own self-test, state unverified". This is
// the one meta-tool of the five (tool_list/tool_create/tool_probe/
// tool_edit/tool_delete) that both creates new on-disk capability *and*
// must clear a deterministic, Go-only admission check before that write is
// even attempted — §19.6's own framing: "ask an LLM does this deserve a
// tool? and it says yes — it is agreeable... So gate 1 is Go code the model
// cannot talk its way past." internal/evolve.Evaluate *is* that code; this
// file is its first real caller.
//
// Only rung 1 (declarative tool.toml, no run.py sidecar) is written here,
// matching tool_probe.go's own current scope — a future rung-2 script-tool
// path needs its own sidecar file and its own hash-inputs list
// (ComputeHash's doc comment on caller-owned ordering) but the write here
// (Manifest -> TOML -> writeStringAtomic) and the gate-1 call are already
// rung-agnostic.
//
// §19.8's threat model ("self-extension makes prompt injection permanent")
// names five mitigations that "ship in the same step as the feature, never
// later". This file is where three of them live in code (the other two —
// mandatory `/tools audit` and tainted-context marking — are a listing/UI
// concern for a later slice, not this write path itself):
//
//  1. tool_create is always danger: high (Danger() below, unconditionally
//     — never inferDanger's ratchet, which exists for a manifest a human
//     already wrote and is reading danger *out of*, not for the act of
//     writing one at all).
//  2. Mandatory provenance: Origin.CreatedBy/Reason/Repetitions/SessionID/
//     Sources are always written from the model's own arguments, never
//     left as a zero-value Manifest.Origin — see buildManifest.
//  4. Egress allowlist: a new tool's own [request].url host must already
//     be on the same allowlist DeclarativeTools/Fetch enforce (t.Allow/
//     t.AllowAll) — a manifest naming an un-allowlisted host is refused at
//     creation time, not just deferred to its first real call.
//  5. Structural exfiltration detection: detectExfiltration below hard-
//     blocks the two shapes §19.8 names by construction (reading a
//     credential-shaped local path, or a non-GET request whose target
//     host is not the one place a GET already had to be allowlisted for).
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/MichiTrader/ishakat/internal/evolve"
)

// toolCreateParamArg is one `[params.<name>]` entry, exactly the shape
// ParamSpec already has but with json tags of its own rather than adding
// json tags to ParamSpec itself — Manifest and its sub-structs are decoded
// from TOML only elsewhere in this package (declarative.go's own doc
// comment: "Manifest is one parsed tool.toml"), and giving them a second,
// independent JSON-facing shape here keeps that contract untouched rather
// than quietly growing it a second consumer.
type toolCreateParamArg struct {
	Type        string   `json:"type"`
	Required    bool     `json:"required,omitempty"`
	Description string   `json:"description,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Default     string   `json:"default,omitempty"`
}

// toolCreateAuthArg mirrors AuthSpec — see toolCreateParamArg's doc comment
// for why this is a sibling type rather than a reused one.
type toolCreateAuthArg struct {
	Scheme    string `json:"scheme,omitempty"`
	KeyEnv    string `json:"key_env,omitempty"`
	SecretEnv string `json:"secret_env,omitempty"`
	Header    string `json:"header,omitempty"`
	Param     string `json:"param,omitempty"`
}

// toolCreateArgs is tool_create's full argument shape: everything needed to
// build a Manifest, plus the origin/evidence fields gate 1 (evolve.Evaluate)
// consumes and never persists into the manifest itself.
type toolCreateArgs struct {
	Name        string `json:"name"`
	Description string `json:"description"`

	Params map[string]toolCreateParamArg `json:"params,omitempty"`

	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Query   map[string]string `json:"query,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
	Auth    toolCreateAuthArg `json:"auth,omitempty"`

	Extract string `json:"extract,omitempty"`

	SelftestEnv    map[string]string `json:"selftest_env,omitempty"`
	SelftestArgs   map[string]string `json:"selftest_args,omitempty"`
	SelftestExpect string            `json:"selftest_expect,omitempty"`

	// Origin, mandatory provenance (§19.8 mitigation 2). Reason and
	// Sources are always required (see Run's own validation) regardless
	// of Origin — a tool with no recorded justification and no recorded
	// sources is exactly the "innocent description, unknown provenance"
	// shape §19.8's own scenario describes, and this is the one place
	// that can still refuse to let that happen.
	Origin string `json:"origin"` // "agent", "user_declared", or "user_forced"
	Reason string `json:"reason"`
	// Sources deliberately has no `,omitempty`: an empty-but-present []
	// must round-trip as a non-nil, zero-length slice (mandatory
	// provenance the caller explicitly declared has none), distinct from
	// omitting the field entirely (nil, "did not even address
	// provenance") -- see Run's own nil check.
	Sources     []string `json:"sources"`
	SessionID   string   `json:"session_id,omitempty"`
	Repetitions int      `json:"repetitions,omitempty"`
	VaryingArgs int      `json:"varying_args,omitempty"`

	// Profitability evidence, all optional (evolve.Evaluate's own
	// contract: ExpectedUses == 0 means "not estimated", skipped rather
	// than failed).
	CreationCostTokens int `json:"creation_cost_tokens,omitempty"`
	PerUseSavingTokens int `json:"per_use_saving_tokens,omitempty"`
	ExpectedUses       int `json:"expected_uses,omitempty"`
}

// ToolCreate is the tool_create meta-tool. Dir is the same layer-2 tools
// directory ToolList/ToolProbe/DeclarativeTools take; Allow/AllowAll are
// the same egress allowlist a real call by the newly created tool will be
// held to (§19.8 mitigation 4 — checked here too, at creation time, not
// only deferred to the tool's first real invocation). Thresholds is gate
// 1's own configuration (see evolve.Thresholds's doc comment for why this
// package takes the plain struct rather than importing config itself); a
// zero-value Thresholds is filled from evolve.DefaultThresholds by
// evolve.Evaluate's own normalization, so an install that has not touched
// these knobs still gets §19.6's stated defaults.
type ToolCreate struct {
	Dir        string
	Allow      []string
	AllowAll   bool
	Thresholds evolve.Thresholds
}

var _ Tool = ToolCreate{}

func (ToolCreate) Name() string { return "tool_create" }

// Danger is unconditionally DangerHigh — §19.8 mitigation 1, verbatim:
// "tool_create is always danger: high. Never 'allow for session', never
// under --yolo." This is not inferDanger's ratchet (which exists to read
// danger *out of* a manifest a human already wrote); the act of writing a
// brand new capability to disk is itself the high-risk operation, before
// any request the resulting manifest might make is even considered.
func (ToolCreate) Danger() Danger { return DangerHigh }

func (ToolCreate) Description() string {
	return "Propose and, if gate 1 (evolve.Evaluate: repetition, no-duplicate, stability, budget, profitability) allows it, write a new layer-2 declarative tool (tool.toml). The new tool starts unverified and unusable until tool_probe passes its self-test. Always danger: high and requires mandatory provenance (reason + sources)."
}

func (ToolCreate) Parameters() json.RawMessage {
	return objectSchema(map[string]prop{
		"name": {
			Type:        "string",
			Description: "The new tool's name (also its directory name under the tools directory). Must not already exist.",
		},
		"description": {
			Type:        "string",
			Description: "One-sentence, model-facing description of what the tool does.",
		},
		"params": {
			Type:        "object",
			Description: "Optional: map of parameter name to {type, required, description, enum, default}, mirroring tool.toml's [params.<name>] tables.",
		},
		"method": {
			Type:        "string",
			Description: "HTTP method for the tool's request. Defaults to GET.",
		},
		"url": {
			Type:        "string",
			Description: "The request URL. May contain {{.param}} template placeholders for any name declared in params. Its host must already be on the egress allowlist.",
		},
		"query": {
			Type:        "object",
			Description: "Optional: map of query parameter name to a (possibly templated) value.",
		},
		"headers": {
			Type:        "object",
			Description: "Optional: map of header name to a (possibly templated) value.",
		},
		"body": {
			Type:        "string",
			Description: "Optional: request body template.",
		},
		"auth": {
			Type:        "object",
			Description: "Optional: {scheme, key_env, secret_env, header, param}. scheme is one of none/bearer/header/query/hmac_sha256.",
		},
		"extract": {
			Type:        "string",
			Description: "Optional: [response].extract dotted-path expression to narrow the response body before the model sees it.",
		},
		"selftest_env": {
			Type:        "object",
			Description: "Optional: environment variables tool_probe applies for the duration of the self-test call only (e.g. a testnet flag).",
		},
		"selftest_args": {
			Type:        "object",
			Description: "Optional: the arguments tool_probe's self-test call uses.",
		},
		"selftest_expect": {
			Type:        "string",
			Description: "Optional: a substring the self-test's output must contain to pass.",
		},
		"origin": {
			Type:        "string",
			Description: "How this proposal claims gate 1 should evaluate it.",
			Enum:        []string{"agent", "user_declared", "user_forced"},
		},
		"reason": {
			Type:        "string",
			Description: "Mandatory provenance: why this tool is being created.",
		},
		"sources": {
			Type:        "array",
			Description: "Mandatory provenance: URLs read to build this tool (may be empty for a purely user-directed creation, but the field itself must be present).",
			Items:       &prop{Type: "string"},
		},
		"session_id": {
			Type:        "string",
			Description: "Optional: the session this tool was created in.",
		},
		"repetitions": {
			Type:        "integer",
			Description: "For origin=agent: how many times this exact pattern has been observed repeating. Ignored for other origins.",
		},
		"varying_args": {
			Type:        "integer",
			Description: "How many call arguments varied across the observed repetitions (stability evidence). 0 means not measured.",
		},
		"creation_cost_tokens": {
			Type:        "integer",
			Description: "Optional profitability evidence: estimated one-time token cost of creating this tool.",
		},
		"per_use_saving_tokens": {
			Type:        "integer",
			Description: "Optional profitability evidence: estimated token saving per use versus not having the tool.",
		},
		"expected_uses": {
			Type:        "integer",
			Description: "Optional profitability evidence: estimated number of future uses. 0 means not estimated (profitability is then skipped, not failed).",
		},
	}, "name", "description", "url", "origin", "reason", "sources")
}

// nativeToolCatalog is the fixed name/description pair for every tool
// Core() registers, used only so gate 1's dedup/budget checks (evolve.
// Evaluate's []ExistingTool) see the *whole* catalogue a model could
// collide with or be capped by, not just the layer-2 subset DiscoverDeclarative
// finds on disk. Hardcoded here rather than accepting a *Registry: a
// *Registry argument would let a caller (or a future test) forget to
// include the native seven, silently under-counting the budget, where a
// fixed list can only ever go stale in the one place registry.go's own
// TestCoreRegistersAllSevenToolsByName already pins and would flag first.
var nativeToolCatalog = []evolve.ExistingTool{
	{Name: ReadFile{}.Name(), Description: ReadFile{}.Description()},
	{Name: WriteFile{}.Name(), Description: WriteFile{}.Description()},
	{Name: EditFile{}.Name(), Description: EditFile{}.Description()},
	{Name: Bash{}.Name(), Description: Bash{}.Description()},
	{Name: Glob{}.Name(), Description: Glob{}.Description()},
	{Name: Grep{}.Name(), Description: Grep{}.Description()},
	{Name: Fetch{}.Name(), Description: Fetch{}.Description()},
}

// credentialLikePaths is §19.8 mitigation 5's fixed, illustrative list of
// path shapes a declarative tool must never read — the same "small,
// illustrative set, not exhaustive" caveat financeHosts already documents
// for a different mechanism in this package applies here too. A tool
// reading one of these is refused outright (ErrorResult, "hard block ...
// not a confirmation prompt" per §19.8), never merely warned about,
// because the whole point of a structural check is that no amount of
// plausible-sounding justification in Reason/Sources talks it past this.
var credentialLikePaths = []string{
	".ssh", ".aws", ".gnupg", "id_rsa", ".env", "config.toml",
}

// touchesCredentialPath reports whether any of URL/body/query/headers
// template sources mention one of credentialLikePaths — checked against
// the *unrendered* template text (Manifest fields are template sources,
// not yet-executed requests, at creation time), which is deliberately
// stricter than necessary: a manifest author who genuinely needs the
// literal string ".env" for an unrelated reason should rename the
// parameter or restructure the request, not have this check taught to
// special-case them.
func touchesCredentialPath(m Manifest) bool {
	haystack := strings.ToLower(m.Request.URL + " " + m.Request.Body)
	for _, v := range m.Request.Query {
		haystack += " " + strings.ToLower(v)
	}
	for _, v := range m.Request.Headers {
		haystack += " " + strings.ToLower(v)
	}
	for _, p := range credentialLikePaths {
		if strings.Contains(haystack, p) {
			return true
		}
	}
	return false
}

// Run validates arguments, runs gate 1 (evolve.Evaluate) against the live
// catalogue (native seven plus every layer-2 tool DiscoverDeclarative(t.Dir)
// finds right now), runs the two structural §19.8 checks that a passing
// gate 1 verdict does not itself cover, and only then writes tool.toml.
//
// A Go error means the proposal could not even be attempted: bad
// arguments JSON, a missing required field, or a name that already exists
// as a directory (a caller should use tool_edit for that, not overwrite
// silently) — matching tool_probe.go's own error-vs-Result split. A gate 1
// rejection, an un-allowlisted host, or a structural exfiltration match are
// all ErrorResult, not a Go error: the model attempted a creation and it
// was refused for a substantive reason it can see, react to, and revise —
// exactly the "attempted and failed" shape every other manifest problem in
// this package already gets.
func (t ToolCreate) Run(ctx context.Context, rawArgs json.RawMessage) (Result, error) {
	var args toolCreateArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return Result{}, fmt.Errorf("tool_create: invalid arguments: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(t.Dir) == "" {
		return ErrorResult("tool_create: no tools directory is configured"), nil
	}
	if strings.TrimSpace(args.Name) == "" {
		return Result{}, fmt.Errorf("tool_create: name is required")
	}
	if strings.ContainsAny(args.Name, "/\\") || args.Name == "." || args.Name == ".." {
		return Result{}, fmt.Errorf("tool_create: name %q must be a plain directory name, not a path", args.Name)
	}
	if strings.TrimSpace(args.Description) == "" {
		return Result{}, fmt.Errorf("tool_create: description is required")
	}
	if strings.TrimSpace(args.URL) == "" {
		return Result{}, fmt.Errorf("tool_create: url is required")
	}
	if strings.TrimSpace(args.Reason) == "" {
		return Result{}, fmt.Errorf("tool_create: reason is required (§19.8 mandatory provenance)")
	}
	if args.Sources == nil {
		return Result{}, fmt.Errorf("tool_create: sources is required (§19.8 mandatory provenance) -- pass an empty array if none apply")
	}

	origin, err := parseOrigin(args.Origin)
	if err != nil {
		return Result{}, fmt.Errorf("tool_create: %w", err)
	}

	toolDir := filepath.Join(t.Dir, args.Name)
	if _, statErr := os.Stat(toolDir); statErr == nil {
		return ErrorResult(fmt.Sprintf("a tool named %q already exists -- use tool_edit to change it, not tool_create", args.Name)), nil
	} else if !os.IsNotExist(statErr) {
		return Result{}, fmt.Errorf("tool_create: could not check %s: %w", toolDir, statErr)
	}

	existing := append([]evolve.ExistingTool(nil), nativeToolCatalog...)
	discovered := DiscoverDeclarative(t.Dir)
	for _, m := range discovered.Tools {
		existing = append(existing, evolve.ExistingTool{Name: m.Name, Description: m.Description})
	}

	candidate := evolve.Candidate{
		Name:               args.Name,
		Description:        args.Description,
		Origin:             origin,
		Repetitions:        args.Repetitions,
		VaryingArgs:        args.VaryingArgs,
		CreationCostTokens: args.CreationCostTokens,
		PerUseSavingTokens: args.PerUseSavingTokens,
		ExpectedUses:       args.ExpectedUses,
	}
	verdict := evolve.Evaluate(t.Thresholds, candidate, existing)
	if !verdict.Allowed {
		return ErrorResult(fmt.Sprintf("gate 1 refused %q:\n- %s", args.Name, strings.Join(verdict.Reasons, "\n- "))), nil
	}

	m := buildManifest(args)

	if res, blocked := checkManifestSafety(m, t.Allow, t.AllowAll, "create"); blocked {
		return res, nil
	}

	body, err := toml.Marshal(m)
	if err != nil {
		return Result{}, fmt.Errorf("tool_create: could not encode manifest for %q: %w", args.Name, err)
	}
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("tool_create: could not create %s: %w", toolDir, err)
	}
	if err := writeStringAtomic(ctx, filepath.Join(toolDir, ManifestFileName), string(body), 0o644); err != nil {
		return Result{}, fmt.Errorf("tool_create: could not write manifest for %q: %w", args.Name, err)
	}

	// state.json is deliberately not written here: LoadState's own
	// "missing file -> StateUnverified" default already gives the
	// correct starting lifecycle state (§19.5 rule 1) for a tool that
	// has never yet been probed, with no separate write needed.
	return OKResult(fmt.Sprintf("created %q (danger: %s, state: unverified). Run tool_probe to verify it before it can be used.", args.Name, inferDanger(m))), nil
}

// parseOrigin translates toolCreateArgs.Origin's string identifier into
// evolve.Origin, the same translation-at-the-boundary pattern this
// package's own callers already apply for config.Egress -> Fetch.Allow —
// evolve.Origin's three named constants are Go-internal, not meant to be
// re-derived from a wire string more than once.
func parseOrigin(s string) (evolve.Origin, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "agent":
		return evolve.OriginAgent, nil
	case "user_declared":
		return evolve.OriginUserDeclared, nil
	case "user_forced":
		return evolve.OriginUserForced, nil
	default:
		return 0, fmt.Errorf("unknown origin %q, want one of agent/user_declared/user_forced", s)
	}
}

// requestHost extracts the hostname a manifest's (unrendered) request URL
// names, for the egress-allowlist check at creation time. A template
// placeholder in the host itself (e.g. "https://{{.host}}/x") is not
// resolvable before a real call supplies arguments; such a URL parses to
// an empty or malformed hostname and is refused by the allowlist check
// this feeds (an empty host is never on any allowlist), which is the
// correct, conservative outcome -- a manifest whose own host is not fixed
// at creation time cannot be pre-approved against a fixed allowlist.
func requestHost(rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	return parsed.Hostname(), nil
}

// checkManifestSafety runs the two §19.8 structural checks a manifest must
// pass before it may be written to disk, shared verbatim between
// tool_create (a brand new manifest) and tool_edit (an edited one) --
// §19.8's own mitigations 4 and 5 apply to *any* manifest content ending
// up on disk, not only to the moment of first creation, since an edit is
// just as capable of introducing an un-allowlisted host or a credential-
// shaped path as a creation is. verb names the caller's own action
// ("create"/"edit") for the returned message's wording only. blocked=false
// means the manifest passed both checks and the (zero-value) Result should
// be ignored.
func checkManifestSafety(m Manifest, allow []string, allowAll bool, verb string) (Result, bool) {
	parsedHost, err := requestHost(m.Request.URL)
	if err != nil {
		return ErrorResult(fmt.Sprintf("could not parse url %q: %v", m.Request.URL, err)), true
	}
	if !allowAll && !hostAllowed(parsedHost, allow) {
		return ErrorResult(fmt.Sprintf(
			"refused to %s %q: host %q is not on the egress allowlist. A new host is its own separate decision (see [tools.egress].allow in config.toml) -- this is not a per-call approval.",
			verb, m.Name, parsedHost)), true
	}
	if touchesCredentialPath(m) {
		return ErrorResult(fmt.Sprintf(
			"refused to %s %q: its request touches a credential-shaped path (.ssh, .aws, .gnupg, id_rsa, .env, config.toml). This is a hard block, not a confirmation -- some shapes are simply not allowed.",
			verb, m.Name)), true
	}
	return Result{}, false
}

// buildManifest translates toolCreateArgs into a Manifest ready for
// toml.Marshal. Dir is left empty -- Discover is what ever sets it, per
// Manifest.Dir's own doc comment ("never decoded from the TOML itself"),
// and a freshly created tool is written to disk, not discovered from it,
// in this same call.
func buildManifest(args toolCreateArgs) Manifest {
	var params map[string]ParamSpec
	if len(args.Params) > 0 {
		params = make(map[string]ParamSpec, len(args.Params))
		for name, p := range args.Params {
			params[name] = ParamSpec{
				Type:        p.Type,
				Required:    p.Required,
				Description: p.Description,
				Enum:        p.Enum,
				Default:     p.Default,
			}
		}
	}

	method := strings.TrimSpace(args.Method)
	if method == "" {
		method = "GET"
	}

	m := Manifest{
		Name:        args.Name,
		Description: args.Description,
		Version:     1,
		Origin: OriginSpec{
			CreatedBy:   "agent",
			Reason:      args.Reason,
			Repetitions: args.Repetitions,
			SessionID:   args.SessionID,
			Sources:     args.Sources,
		},
		Params: params,
		Request: RequestSpec{
			Method:  method,
			URL:     args.URL,
			Query:   args.Query,
			Headers: args.Headers,
			Body:    args.Body,
			Auth: AuthSpec{
				Scheme:    args.Auth.Scheme,
				KeyEnv:    args.Auth.KeyEnv,
				SecretEnv: args.Auth.SecretEnv,
				Header:    args.Auth.Header,
				Param:     args.Auth.Param,
			},
		},
		Response: ResponseSpec{
			Extract: args.Extract,
		},
		Selftest: SelftestSpec{
			Env:    args.SelftestEnv,
			Args:   args.SelftestArgs,
			Expect: args.SelftestExpect,
		},
	}
	return m
}

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// maxFetchOutputBytes bounds a single fetch call's returned text, same
// figure as maxReadFileBytes/maxBashOutputBytes (§12bis's own
// max_tool_output_bytes default): a large page must not blow up the
// context, so the converted text is capped here rather than relying solely
// on the truncation the agent loop applies later.
const maxFetchOutputBytes = 32 << 10

// maxFetchBodyBytes bounds how much of the HTTP response body this tool
// will even read before giving up on the page, independent of
// maxFetchOutputBytes above: an HTML document is denser than its extracted
// text (tags, scripts, styles all get stripped), so the raw body has to be
// allowed several times larger than the final text ceiling or a page that
// would have produced a perfectly reasonable ~30 KiB of text gets cut off
// mid-tag first. 2 MiB is generous for anything §19.8 already says this
// tool is good for (docs, blogs, APIs, GitHub) and small enough that a
// pathological response cannot exhaust memory.
const maxFetchBodyBytes = 2 << 20

// defaultFetchTimeout bounds how long a single fetch call may wait for a
// response before it is reported as failed. Ten seconds matches the
// per-provider budget internal/catalog/fetch.DefaultDiscoverTimeout already
// uses for the same kind of "a remote host might just never answer"
// failure mode.
const defaultFetchTimeout = 10 * time.Second

// scriptStyleRe strips a <script>...</script> or <style>...</style> block
// whole, tags and content together — the content of neither is ever text a
// reader would want, and leaving it in would dump raw JavaScript/CSS into
// the model's context instead of the page's actual prose.
var scriptStyleRe = regexp.MustCompile(`(?is)<(script|style)\b[^>]*>.*?</(script|style)>`)

// tagRe matches any remaining HTML tag once script/style blocks are gone,
// so what is left is stripped down to text and whitespace.
var tagRe = regexp.MustCompile(`(?s)<[^>]*>`)

// blockTagRe finds the *opening* tag of an element that should force a line
// break where a browser would render one — a block-level element or <br> —
// applied before tagRe removes the tags themselves. Run against the same
// pass as commentRe/scriptStyleRe (before tag stripping) so <p>a</p><p>b</p>
// does not collapse into "ab" once every tag disappears.
var blockTagRe = regexp.MustCompile(`(?i)</?(p|div|br|li|tr|h[1-6]|blockquote|pre)\b[^>]*>`)

// commentRe strips an HTML comment, <!-- ... -->, including the multi-line
// case some pages use for large commented-out blocks.
var commentRe = regexp.MustCompile(`(?s)<!--.*?-->`)

// blankLinesRe collapses three or more consecutive newlines (left behind
// once tags and their surrounding whitespace are stripped) down to a single
// blank line, so the output reads like the prose it is meant to convert to
// rather than a page riddled with empty lines from a deeply nested layout.
var blankLinesRe = regexp.MustCompile(`\n{3,}`)

// spacesRe collapses runs of horizontal whitespace (but not newlines,
// which blockTagRe deliberately introduced) down to a single space.
var spacesRe = regexp.MustCompile(`[ \t]+`)

// fetchArgs is fetch's argument shape.
type fetchArgs struct {
	URL string `json:"url"`
}

// Fetch is the fetch core tool (§19.1, §19.8): retrieve a URL over HTTP(S)
// and return its content as plain text, converting HTML to text along the
// way with a hand-rolled tag stripper rather than an HTML-parsing
// dependency — §6.4's budget is zero new dependencies for the whole of
// Phase 2.5, and net/http is already present transitively (internal/catalog/
// fetch, internal/provider/openai both use it), so this tool adds nothing to
// go.mod.
//
// Danger: low. fetch is read-only in the same sense read_file/glob/grep
// are (§19.1's own table lists it as "read-only or otherwise reversible")
// — it cannot change anything on the machine it runs on, and its own
// egress allowlist is what stands between it and the exfiltration risk a
// naive reading of "it makes network requests" would otherwise put on it.
// That allowlist is enforced here, once, in-tool — deliberately not folded
// into internal/permissions' hardDeny path — because it names a set of
// *destinations*, not a Tier the human approval flow needs to reason
// about; the same separation §19.8's own table draws between "danger" and
// "which host" for declarative tools.
type Fetch struct {
	// Allow is the allowlist of hostnames this call may reach, mirroring
	// config.Egress.Allow (§19.8, mitigation 4: "A tool.toml's url host
	// must be in [tools.egress].allow. A new host is its own separate
	// confirmation."). Matching is exact against the URL's Host with any
	// port stripped, plus a leading "*." wildcard for subdomains — the
	// same shape config.example.toml's own comment implies ("a qué hosts
	// puede llegar una herramienta").
	//
	// This is a plain []string rather than config.Egress: internal/tools
	// must never import internal/config (tool.go's own doc comment: a
	// tool's only constructor arguments are what a cross-cutting concern
	// like this needs injected, not a whole configuration type), so
	// internal/app's wiring is what translates cfg.Tools.Egress.Allow
	// into this field, exactly the same shape Guard.New already takes
	// config.Permissions by value rather than the whole *config.Config.
	Allow []string
	// AllowAll disables the allowlist entirely, mirroring
	// config.Egress.AllowAll. Kept as its own field rather than a nil/empty
	// Allow meaning "allow everything", because an empty allowlist is far
	// more likely to be a configuration mistake than a deliberate "let
	// this reach nothing" — the same reasoning validate.go's own comment
	// on an empty deny list already applies to defaults, applied here to
	// the opposite direction (empty allow means deny, not allow).
	AllowAll bool

	// HTTPClient is the client used to perform the request. Nil means
	// http.DefaultClient with defaultFetchTimeout applied per-request via
	// context, matching http.Client's own documented pattern for a
	// per-call rather than per-client timeout (a shared *http.Client with
	// no Timeout set is what net/http's own docs recommend when the
	// caller already manages cancellation through context, which
	// Run's ctx parameter already does). Exposed for tests, which need to
	// point this at an httptest.Server instead of the real network — the
	// same shape internal/catalog/fetch.Client's own Client field takes.
	HTTPClient *http.Client
}

var _ Tool = Fetch{}

func (Fetch) Name() string   { return "fetch" }
func (Fetch) Danger() Danger { return DangerLow }
func (Fetch) Description() string {
	return "Fetch a URL over HTTP(S) and return its content as plain text. HTML is converted to readable text (tags, scripts and styles stripped); useful for documentation, blogs, APIs and GitHub, useless for JavaScript-only sites, logins or anything that needs clicking. Only hosts on the configured egress allowlist may be reached."
}

func (Fetch) Parameters() json.RawMessage {
	return objectSchema(map[string]prop{
		"url": {
			Type:        "string",
			Description: "The absolute http:// or https:// URL to fetch.",
		},
	}, "url")
}

func (f Fetch) Run(ctx context.Context, rawArgs json.RawMessage) (Result, error) {
	var args fetchArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return Result{}, fmt.Errorf("fetch: invalid arguments: %w", err)
	}
	if args.URL == "" {
		return Result{}, fmt.Errorf("fetch: url is required")
	}

	parsed, err := url.Parse(args.URL)
	if err != nil {
		return ErrorResult(fmt.Sprintf("could not parse %q as a URL: %v", args.URL, err)), nil
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return ErrorResult(fmt.Sprintf("refused to fetch %q: only http and https are supported, got scheme %q", args.URL, parsed.Scheme)), nil
	}
	if parsed.Hostname() == "" {
		return ErrorResult(fmt.Sprintf("refused to fetch %q: no host in URL", args.URL)), nil
	}

	if !f.AllowAll && !hostAllowed(parsed.Hostname(), f.Allow) {
		return ErrorResult(fmt.Sprintf(
			"refused to fetch %s: host %q is not on the egress allowlist. A new host is its own separate decision (see [tools.egress].allow in config.toml) — this is not a per-call approval.",
			args.URL, parsed.Hostname())), nil
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, defaultFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(timeoutCtx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return Result{}, fmt.Errorf("fetch: could not build request: %w", err)
	}
	req.Header.Set("User-Agent", "ishakat/fetch (+https://github.com/MichiTrader/ishakat)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain,application/json;q=0.9,*/*;q=0.5")

	client := f.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			// The caller's own context was cancelled, not just this
			// call's timeout — surface it as a Go error so the agent
			// loop's cancellation path handles it (§12bis: cancellation
			// is not "the tool failed"), matching bash.go's identical
			// distinction between the two context sources.
			return Result{}, ctx.Err()
		}
		return ErrorResult(fmt.Sprintf("could not fetch %s: %v", args.URL, err)), nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchBodyBytes+1))
	if err != nil {
		return ErrorResult(fmt.Sprintf("could not read response body from %s: %v", args.URL, err)), nil
	}
	bodyTruncated := len(body) > maxFetchBodyBytes
	if bodyTruncated {
		body = body[:maxFetchBodyBytes]
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ErrorResult(fmt.Sprintf("%s responded with status %d", args.URL, resp.StatusCode)), nil
	}

	contentType := resp.Header.Get("Content-Type")
	text := toText(string(body), contentType)

	if text == "" {
		return OKResult(fmt.Sprintf("%s returned no readable text (content-type: %s)", args.URL, contentType)), nil
	}

	out := text
	truncated := bodyTruncated
	if len(out) > maxFetchOutputBytes {
		out = out[:maxFetchOutputBytes]
		truncated = true
	}
	if truncated {
		out += fmt.Sprintf("\n\n…[truncated: %s was larger than this tool's ceiling]", args.URL)
	}
	return OKResult(out), nil
}

// hostAllowed reports whether host matches one of allow, exactly or via a
// leading "*." wildcard entry (e.g. "*.github.com" matches
// "raw.githubusercontent.com"'s sibling pattern, "api.github.com" matches
// the literal entry). Matching is case-insensitive, since hostnames are
// not case-sensitive per RFC 4343 and a config file's casing should not be
// load-bearing.
func hostAllowed(host string, allow []string) bool {
	host = strings.ToLower(host)
	for _, entry := range allow {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if entry == "" {
			continue
		}
		if strings.HasPrefix(entry, "*.") {
			suffix := entry[1:] // ".example.com"
			if strings.HasSuffix(host, suffix) || host == entry[2:] {
				return true
			}
			continue
		}
		if host == entry {
			return true
		}
	}
	return false
}

// toText converts body to plain text. HTML (detected from contentType, or
// assumed when contentType is empty and the body itself looks like markup —
// some misconfigured servers omit the header) is run through the
// stdlib-only tag stripper below; anything else (JSON, plain text, XML)
// passes through unchanged, since forcing non-HTML content through an
// HTML-shaped cleanup would mangle a JSON API response's own brackets and
// quotes for no benefit.
func toText(body, contentType string) string {
	if !looksLikeHTML(contentType, body) {
		return strings.TrimSpace(body)
	}
	s := body
	s = commentRe.ReplaceAllString(s, "")
	s = scriptStyleRe.ReplaceAllString(s, "")
	s = blockTagRe.ReplaceAllString(s, "\n")
	s = tagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = spacesRe.ReplaceAllString(s, " ")
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(line)
	}
	s = strings.Join(lines, "\n")
	s = blankLinesRe.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

// looksLikeHTML decides whether toText should run its tag-stripping pass.
// A well-behaved server sets Content-Type; some do not, so an empty
// content-type falls back to sniffing the first non-whitespace bytes for a
// "<" that starts a tag-like token, the same low-tech heuristic
// net/http.DetectContentType uses internally for its own HTML case.
func looksLikeHTML(contentType, body string) bool {
	ct := strings.ToLower(contentType)
	if strings.Contains(ct, "html") || strings.Contains(ct, "xhtml") {
		return true
	}
	if ct != "" {
		return false
	}
	trimmed := strings.TrimSpace(body)
	return strings.HasPrefix(trimmed, "<")
}

package fetch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/MichiTrader/ishakat/internal/catalog"
)

// The models.dev client. Two documents, one conditional request each:
//
//	api.json    ~3 MB   provider → model → metadata
//	models.json ~200 KB provider-agnostic base, for the gateway case
//
// If-None-Match is what makes the daily refresh cheap: models.dev serves a
// strong ETag and answers 304 with an empty body when nothing changed, so
// the usual cost of a refresh is two round trips and no payload. On a phone
// that is the difference between a refresh you can afford every day and one
// you cannot.

// Defaults for the two endpoints, matching [catalog] in §5.2.
const (
	DefaultAPIURL  = "https://models.dev/api.json"
	DefaultMetaURL = "https://models.dev/models.json"

	// maxPayload caps what will be read from the network. api.json is
	// around 3 MB today; 32 MB leaves room for years of growth and still
	// protects against a redirect to something enormous.
	maxPayload = 32 << 20

	// DefaultTimeout is the whole models.dev refresh, both documents.
	DefaultTimeout = 20 * time.Second
)

// ModelsDev fetches and digests the metadata source.
type ModelsDev struct {
	APIURL    string
	MetaURL   string
	Client    *http.Client
	UserAgent string
}

// RefreshResult says what happened, in enough detail for the cache receipt
// of §4.4 and for an honest message to the user.
type RefreshResult struct {
	Index    *catalog.Index
	Changed  bool
	NotMod   bool
	Err      error
	Duration time.Duration
}

// Refresh fetches both documents conditionally and returns an updated index.
//
// prev may be nil or empty. The rules:
//
//   - 304 on both documents → the previous index is returned untouched and
//     Changed is false. This is the common case and it costs no payload.
//   - 200 on api.json → the index is rebuilt from it. models.json is only
//     re-fetched when its own ETag also changed.
//   - any error → prev is returned unchanged with Err set. Stale metadata is
//     strictly better than none, and §4.4 says a failed refresh must be
//     invisible beyond a staleness strip.
func (c *ModelsDev) Refresh(ctx context.Context, prev *catalog.Index) RefreshResult {
	start := time.Now()
	res := RefreshResult{Index: prev}
	if res.Index == nil {
		res.Index = catalog.NewIndex()
	}

	apiURL := c.APIURL
	if apiURL == "" {
		apiURL = DefaultAPIURL
	}
	metaURL := c.MetaURL
	if metaURL == "" {
		metaURL = DefaultMetaURL
	}

	// api.json first: it is the one that decides whether the index changes.
	apiBody, apiETag, apiMod, err := c.get(ctx, apiURL, res.Index.ETag)
	res.Duration = time.Since(start)
	if err != nil {
		res.Err = err
		return res
	}

	next := catalog.NewIndex()
	next.ETag = res.Index.ETag
	next.MetaETag = res.Index.MetaETag
	next.ByProvider = res.Index.ByProvider
	next.Agnostic = res.Index.Agnostic
	next.FetchedAt = time.Now()

	switch {
	case apiMod:
		fresh := catalog.NewIndex()
		if err := fresh.ParseAPI(apiBody); err != nil {
			res.Err = err
			return res
		}
		fresh.ETag = apiETag
		fresh.MetaETag = res.Index.MetaETag
		fresh.Agnostic = res.Index.Agnostic
		fresh.FetchedAt = time.Now()
		next = fresh
		res.Changed = true
	default:
		res.NotMod = true
	}

	// models.json second. A failure here is not fatal: the agnostic base is
	// the fallback rung of the cascade, not the main one.
	metaBody, metaETag, metaMod, err := c.get(ctx, metaURL, res.Index.MetaETag)
	if err == nil && metaMod {
		if perr := next.ParseMeta(metaBody); perr == nil {
			next.MetaETag = metaETag
			res.Changed = true
			res.NotMod = false
		}
	}

	res.Index = next
	res.Duration = time.Since(start)
	return res
}

// get performs one conditional request. Returns the body (empty on 304),
// the new validator, and whether the document was actually modified.
func (c *ModelsDev) get(ctx context.Context, url, etag string) (body []byte, newETag string, modified bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", false, fmt.Errorf("models.dev: malformed request for %s: %w", url, err)
	}
	req.Header.Set("Accept", "application/json")
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}

	hc := c.Client
	if hc == nil {
		hc = &http.Client{Timeout: DefaultTimeout}
	}

	resp, err := hc.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, "", false, ctxErr
		}
		return nil, "", false, fmt.Errorf("models.dev: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNotModified:
		// Nothing changed. The validator is kept: some CDNs omit the ETag
		// on a 304 and dropping it would force a full download next time.
		next := resp.Header.Get("ETag")
		if next == "" {
			next = etag
		}
		return nil, next, false, nil
	case http.StatusOK:
		raw, err := io.ReadAll(io.LimitReader(resp.Body, maxPayload))
		if err != nil {
			return nil, "", false, fmt.Errorf("models.dev: error reading %s: %w", url, err)
		}
		return raw, resp.Header.Get("ETag"), true, nil
	default:
		return nil, "", false, fmt.Errorf("models.dev: HTTP %d from %s", resp.StatusCode, url)
	}
}

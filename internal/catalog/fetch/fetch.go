// Package fetch is the only part of the catalog that touches the network:
// provider discovery and the models.dev client with If-None-Match.
//
// It exists as a separate package to keep the promise of §6.1 — "the TUI
// does not know what HTTP is" — testable. internal/catalog is imported by
// the model picker; if the models.dev client lived there, net/http would
// appear in the transitive closure of internal/tui and the boundary test
// would fail. The deviation from the §6.2 tree is deliberate and recorded in
// the §17 changelog.
//
// The budget of §4.4 rules everything here: startup does not touch the
// network on the critical path. Nothing in this package is ever called
// before the interface is drawn.
package fetch

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/MichiTrader/ishakat/internal/catalog"
	"github.com/MichiTrader/ishakat/internal/provider"
)

// DefaultDiscoverTimeout is the per-provider budget of §4.4. Two seconds is
// not generous by accident: a provider that cannot list its models in two
// seconds is not going to make the refresh feel live, and the cached list is
// already on screen.
const DefaultDiscoverTimeout = 2 * time.Second

// Target is one provider to interrogate.
type Target struct {
	ID       string
	Provider provider.Provider
}

// Result is what one provider answered.
type Result struct {
	ID        string
	Models    []catalog.DiscoveredModel
	Err       error
	FetchedAt time.Time
	Elapsed   time.Duration
}

// OK reports whether the discovery succeeded.
func (r Result) OK() bool { return r.Err == nil }

// Discover interrogates every provider in parallel with an individual
// timeout, and returns the results in the same order it was given.
//
// It never returns an error of its own: a provider that fails is a Result
// with Err set, and the merge keeps the models already in the cache. Failing
// the whole refresh because one of four providers is down would throw away
// three good answers.
func Discover(ctx context.Context, targets []Target, timeout time.Duration) []Result {
	if timeout <= 0 {
		timeout = DefaultDiscoverTimeout
	}
	out := make([]Result, len(targets))
	var wg sync.WaitGroup

	for i, t := range targets {
		if t.Provider == nil {
			out[i] = Result{ID: t.ID, Err: errors.New("no adapter built for this provider")}
			continue
		}
		wg.Add(1)
		go func(i int, t Target) {
			defer wg.Done()
			out[i] = discoverOne(ctx, t, timeout)
		}(i, t)
	}
	wg.Wait()
	return out
}

func discoverOne(ctx context.Context, t Target, timeout time.Duration) Result {
	start := time.Now()
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	res := Result{ID: t.ID, FetchedAt: start}
	raw, err := t.Provider.Discover(cctx)
	res.Elapsed = time.Since(start)
	if err != nil {
		res.Err = err
		return res
	}

	res.Models = make([]catalog.DiscoveredModel, 0, len(raw))
	for _, rm := range raw {
		res.Models = append(res.Models, catalog.DiscoveredModel{
			WireID:  rm.WireID,
			Name:    rm.Name,
			Context: rm.Context,
			Output:  rm.Output,
			Tags:    rm.Tags,
			Raw:     rm.Raw,
		})
	}
	res.FetchedAt = time.Now()
	return res
}

// Apply writes the results into the cache: successes replace the list,
// failures keep the previous one and record the reason (§4.4).
func Apply(c *catalog.Cache, results []Result, now time.Time) {
	if c == nil {
		return
	}
	for _, r := range results {
		if r.Err != nil {
			c.SetProviderError(r.ID, r.Err, now)
			continue
		}
		c.SetProvider(r.ID, r.Models, r.FetchedAt)
	}
	c.FetchedAt = now
}

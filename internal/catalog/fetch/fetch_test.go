package fetch

import (
	"context"
	"testing"
	"time"

	"github.com/MichiTrader/ishakat/internal/provider"
)

type delayedProvider struct {
	delay time.Duration
}

func (p delayedProvider) ID() string { return "delayed" }

func (p delayedProvider) Discover(ctx context.Context) ([]provider.RawModel, error) {
	timer := time.NewTimer(p.delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return []provider.RawModel{{WireID: "slow-model", Name: "Slow model"}}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p delayedProvider) Stream(context.Context, provider.Request) (<-chan provider.Event, error) {
	return nil, nil
}

func TestDiscoverUsesTargetTimeout(t *testing.T) {
	results := Discover(context.Background(), []Target{{
		ID:       "delayed",
		Provider: delayedProvider{delay: 30 * time.Millisecond},
		Timeout:  60 * time.Millisecond,
	}}, 5*time.Millisecond)

	if len(results) != 1 {
		t.Fatalf("Discover returned %d results, want 1", len(results))
	}
	if err := results[0].Err; err != nil {
		t.Fatalf("provider discovery failed despite target timeout: %v", err)
	}
	if len(results[0].Models) != 1 || results[0].Models[0].WireID != "slow-model" {
		t.Fatalf("unexpected discovered models: %#v", results[0].Models)
	}
}

func TestDiscoverUsesFallbackTimeoutWhenTargetTimeoutIsUnset(t *testing.T) {
	results := Discover(context.Background(), []Target{{
		ID:       "delayed",
		Provider: delayedProvider{delay: 30 * time.Millisecond},
	}}, 5*time.Millisecond)

	if len(results) != 1 {
		t.Fatalf("Discover returned %d results, want 1", len(results))
	}
	if results[0].Err == nil {
		t.Fatal("discovery succeeded beyond the fallback timeout")
	}
	if results[0].Err != context.DeadlineExceeded {
		t.Fatalf("discovery error = %v, want context deadline exceeded", results[0].Err)
	}
}

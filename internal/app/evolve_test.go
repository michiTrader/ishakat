package app

import (
	"testing"

	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/evolve"
	"github.com/MichiTrader/ishakat/internal/tools"
)

func TestEvolveThresholdsMapsConfiguredFields(t *testing.T) {
	got := evolveThresholds(
		config.Tools{MaxTools: 25},
		config.Evolve{MinRepeats: 5, DedupThreshold: 0.9},
	)
	want := evolve.Thresholds{MinRepeats: 5, DedupThreshold: 0.9, MaxTools: 25}
	if got != want {
		t.Fatalf("evolveThresholds() = %+v, want %+v", got, want)
	}
}

func TestEvolveThresholdsZeroValueNormalizesInEvaluate(t *testing.T) {
	// A never-configured install (every config.Evolve/config.Tools field at
	// its Go zero value) must still see §19.6's real defaults once passed
	// through evolve.Evaluate -- this function itself does not normalize
	// (that is evolve.Thresholds.normalized()'s job), it only has to avoid
	// inventing a non-zero value that would shadow the real default.
	got := evolveThresholds(config.Tools{}, config.Evolve{})
	if got != (evolve.Thresholds{}) {
		t.Fatalf("expected an all-zero-config translation to stay all-zero (letting evolve.Evaluate normalize it), got %+v", got)
	}
	v := evolve.Evaluate(got, evolve.Candidate{
		Name: "x", Description: "y", Origin: evolve.OriginAgent, Repetitions: 3,
	}, nil)
	if !v.Allowed {
		t.Fatalf("expected the zero-value translation to still pass gate 1 at the default MinRepeats=3, got: %v", v.Reasons)
	}
}

func TestExistingToolsFromNilRegistryReturnsNil(t *testing.T) {
	if got := existingToolsFrom(nil); got != nil {
		t.Fatalf("existingToolsFrom(nil) = %v, want nil", got)
	}
}

func TestExistingToolsFromListsEveryRegisteredTool(t *testing.T) {
	reg := tools.Core(nil, false)
	got := existingToolsFrom(reg)
	want := reg.Tools()
	if len(got) != len(want) {
		t.Fatalf("existingToolsFrom returned %d tools, registry has %d", len(got), len(want))
	}
	for i, tool := range want {
		if got[i].Name != tool.Name() || got[i].Description != tool.Description() {
			t.Errorf("index %d: got %+v, want name=%q description=%q", i, got[i], tool.Name(), tool.Description())
		}
	}
}

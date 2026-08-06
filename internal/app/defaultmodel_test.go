package app

import (
	"testing"

	"github.com/MichiTrader/ishakat/internal/config"
)

func baseCfgForDefaultModel() *config.Config {
	return &config.Config{
		Schema: config.Schema,
		App:    config.App{DefaultModel: "omniroute/auto/coding"},
	}
}

func TestNeedsDefaultModelTrueWhenProviderNotDeclared(t *testing.T) {
	cfg := baseCfgForDefaultModel() // no providers at all
	if !NeedsDefaultModel(cfg) {
		t.Error("want true: default_model's provider isn't declared")
	}
}

func TestNeedsDefaultModelTrueWhenProviderDisabled(t *testing.T) {
	cfg := baseCfgForDefaultModel()
	cfg.Providers = []config.Provider{
		{ID: "omniroute", Enabled: false, AuthOK: true},
	}
	if !NeedsDefaultModel(cfg) {
		t.Error("want true: the default's provider is disabled")
	}
}

func TestNeedsDefaultModelTrueWhenProviderHasNoCredential(t *testing.T) {
	cfg := baseCfgForDefaultModel()
	cfg.Providers = []config.Provider{
		{ID: "omniroute", Enabled: true, AuthOK: false, MissingEnv: "OMNIROUTE_API_KEY"},
	}
	if !NeedsDefaultModel(cfg) {
		t.Error("want true: the default's provider has no working credential")
	}
}

func TestNeedsDefaultModelFalseWhenDefaultAlreadyWorks(t *testing.T) {
	cfg := baseCfgForDefaultModel()
	cfg.Providers = []config.Provider{
		{ID: "omniroute", Enabled: true, AuthOK: true},
	}
	if NeedsDefaultModel(cfg) {
		t.Error("want false: the configured default already resolves to a usable provider")
	}
}

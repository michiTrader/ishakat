package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichiTrader/ishakat/internal/config"
)

func TestLoadExampleNoWarnings(t *testing.T) {
	t.Setenv("OMNIROUTE_API_KEY", "dummy-key")
	t.Setenv("OPENAI_API_KEY", "dummy-key")
	t.Setenv("ANTHROPIC_API_KEY", "dummy-key")

	cfg, err := config.Load(config.Options{
		UserPath:    "../../config.example.toml",
		SkipProject: true,
	})
	if err != nil {
		t.Fatalf("error al cargar config.example.toml: %v", err)
	}
	if len(cfg.Warnings) > 0 {
		t.Errorf("se esperaban 0 advertencias, pero se obtuvieron %d:", len(cfg.Warnings))
		for _, w := range cfg.Warnings {
			t.Logf("  [%s] %s", w.Where, w.Msg)
		}
	}
}

func TestMergeProvidersByID(t *testing.T) {
	tmpDir := t.TempDir()
	userCfgPath := filepath.Join(tmpDir, "user.toml")
	projCfgPath := filepath.Join(tmpDir, "proj.toml")

	_ = os.WriteFile(userCfgPath, []byte(`
schema = 1
[[provider]]
id = "omniroute"
base_url = "http://localhost:20128/v1"
kind = "openai"
timeout_s = 180
`), 0o600)

	_ = os.WriteFile(projCfgPath, []byte(`
schema = 1
[[provider]]
id = "omniroute"
base_url = "http://otro-servidor:9999/v1"
`), 0o600)

	cfg, err := config.Load(config.Options{
		UserPath:    userCfgPath,
		ProjectPath: projCfgPath,
	})
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}

	var omni *config.Provider
	for i := range cfg.Providers {
		if cfg.Providers[i].ID == "omniroute" {
			omni = &cfg.Providers[i]
			break
		}
	}
	if omni == nil {
		t.Fatal("proveedor omniroute no encontrado")
	}

	if omni.BaseURL != "http://otro-servidor:9999/v1" {
		t.Errorf("esperado 'http://otro-servidor:9999/v1', obtenido '%s'", omni.BaseURL)
	}
	if omni.Kind != "openai" {
		t.Errorf("se debió conservar kind='openai', obtenido '%s'", omni.Kind)
	}
	if omni.TimeoutS != 180 {
		t.Errorf("se debió conservar timeout_s=180, obtenido %d", omni.TimeoutS)
	}
}

func TestFatalErrorsTable(t *testing.T) {
	tests := []struct {
		name      string
		toml      string
		wantMatch string
	}{
		{
			name:      "sin_base_url",
			toml:      `schema=1` + "\n" + `[[provider]]` + "\n" + `id="test"` + "\n" + `kind="openai"`,
			wantMatch: "base_url",
		},
		{
			name:      "sin_id",
			toml:      `schema=1` + "\n" + `[[provider]]` + "\n" + `kind="openai"` + "\n" + `base_url="http://x"`,
			wantMatch: "falta id",
		},
		{
			name:      "id_duplicado",
			toml:      `schema=1` + "\n" + `[[provider]]` + "\n" + `id="a"` + "\n" + `kind="openai"` + "\n" + `base_url="http://x"` + "\n" + `[[provider]]` + "\n" + `id="a"` + "\n" + `kind="openai"` + "\n" + `base_url="http://y"`,
			wantMatch: "dos veces",
		},
		{
			name:      "schema_invalido",
			toml:      `schema=99`,
			wantMatch: "schema = 99 no soportado",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			p := filepath.Join(tmpDir, "invalid.toml")
			_ = os.WriteFile(p, []byte(tt.toml), 0o600)

			_, err := config.Load(config.Options{UserPath: p, SkipProject: true})
			if err == nil {
				t.Fatal("se esperaba error, pero la carga fue exitosa")
			}
			if !strings.Contains(err.Error(), tt.wantMatch) {
				t.Errorf("el mensaje de error %q no contiene %q", err.Error(), tt.wantMatch)
			}
		})
	}
}

func TestEnvExpansion(t *testing.T) {
	t.Setenv("TEST_API_KEY", "sk-secret-123456")

	tmpDir := t.TempDir()
	p := filepath.Join(tmpDir, "env.toml")
	_ = os.WriteFile(p, []byte(`
schema = 1
[[provider]]
id = "presente"
kind = "openai"
base_url = "http://x"
api_key = "$TEST_API_KEY"

[[provider]]
id = "ausente"
kind = "openai"
base_url = "http://x"
api_key = "${MISSING_KEY_VAR}"
`), 0o600)

	cfg, err := config.Load(config.Options{UserPath: p, SkipProject: true})
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}

	var p1, p2 *config.Provider
	for i := range cfg.Providers {
		if cfg.Providers[i].ID == "presente" {
			p1 = &cfg.Providers[i]
		}
		if cfg.Providers[i].ID == "ausente" {
			p2 = &cfg.Providers[i]
		}
	}

	if p1 == nil || p1.APIKey != "sk-secret-123456" || !p1.AuthOK {
		t.Errorf("p1 falló expansión o AuthOK: %+v", p1)
	}

	if p2 == nil || p2.AuthOK || p2.MissingEnv != "MISSING_KEY_VAR" {
		t.Errorf("p2 debió quedar desautenticado con MissingEnv='MISSING_KEY_VAR': %+v", p2)
	}
}

func TestRedacted(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.Provider{
			{ID: "p1", APIKey: "secret-1234567890"},
		},
	}
	redacted := cfg.Redacted()

	if redacted.Providers[0].APIKey == cfg.Providers[0].APIKey {
		t.Fatal("Redacted() no enmascaró la APIKey")
	}
	if strings.Contains(redacted.Providers[0].APIKey, "secret-1234") {
		t.Errorf("la clave redactada contiene partes sensibles: %s", redacted.Providers[0].APIKey)
	}
}

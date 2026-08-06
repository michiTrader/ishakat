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

	// Se copia el fixture a un archivo temporal con permisos 0600 explícitos:
	// git no preserva el modo completo (solo el bit ejecutable), así que tras
	// un clon el archivo puede quedar en 0644 según el umask del checkout, lo
	// que dispararía una advertencia de permisos ajena a lo que este test
	// quiere verificar (que el ejemplo no tenga advertencias de contenido).
	original, err := os.ReadFile("../../config.example.toml")
	if err != nil {
		t.Fatalf("no se pudo leer config.example.toml: %v", err)
	}
	tmpDir := t.TempDir()
	tmpPath := filepath.Join(tmpDir, "config.example.toml")
	if err := os.WriteFile(tmpPath, original, 0o600); err != nil {
		t.Fatalf("no se pudo escribir copia temporal: %v", err)
	}

	cfg, err := config.Load(config.Options{
		UserPath:    tmpPath,
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

// TestConfigTOMLMode0644DoesNotWarn is the regression test for the audit's
// second P0 finding: `provider add` deliberately writes config.toml at
// 0644 (SaveProviderConnection's own comment: "config.toml is not a secrets
// file"), and checkPerms used to run against every loaded layer including
// this one — so the very next `config check` (or any config.Load) warned
// "insecure permissions 0644 (0600 recommended)" about a mode the program
// itself had just chosen on purpose, recommending a mode
// (SaveProviderConnection) explicitly rejected. Only credentials.toml
// should ever trigger this warning.
func TestConfigTOMLMode0644DoesNotWarn(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte("schema = 1\n"), 0o644); err != nil {
		t.Fatalf("could not write fixture: %v", err)
	}

	cfg, err := config.Load(config.Options{UserPath: cfgPath, SkipProject: true})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	for _, w := range cfg.Warnings {
		if strings.Contains(w.Msg, "permisos inseguros") || strings.Contains(w.Msg, "insecure permissions") {
			t.Errorf("config.toml at 0644 must not produce a permissions warning, got [%s] %s", w.Where, w.Msg)
		}
	}
}

// TestCredentialsTOMLMode0644DoesWarn is the other half: credentials.toml
// (the actual secrets file, always written 0600 by atomicWritePrivate) must
// still be flagged if something leaves it group/world readable — the fix
// for the P0 above narrows checkPerms' scope, it does not remove the check.
func TestCredentialsTOMLMode0644DoesWarn(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	credPath := filepath.Join(tmpDir, "ishakat", "credentials.toml")
	if err := os.MkdirAll(filepath.Dir(credPath), 0o755); err != nil {
		t.Fatalf("could not create credentials dir: %v", err)
	}
	if err := os.WriteFile(credPath, []byte("schema = 1\n"), 0o644); err != nil {
		t.Fatalf("could not write fixture: %v", err)
	}

	cfgPath := filepath.Join(tmpDir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte("schema = 1\n"), 0o600); err != nil {
		t.Fatalf("could not write fixture: %v", err)
	}

	cfg, err := config.Load(config.Options{UserPath: cfgPath, SkipProject: true})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	found := false
	for _, w := range cfg.Warnings {
		if strings.Contains(w.Where, "credentials.toml") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a permissions warning for credentials.toml at 0644, got warnings: %+v", cfg.Warnings)
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

// TestDisabledProviderMissingEnvIsSilent cubre el caso que motivó el fix: un
// usuario que solo usa OmniRoute ve, en cada arranque, dos advertencias por
// $OPENAI_API_KEY y $ANTHROPIC_API_KEY faltantes — variables de proveedores
// que ni siquiera tiene activados (config.example.toml los trae con
// `enabled = false`). El estado interno (AuthOK/MissingEnv) debe seguir
// siendo correcto para quien lo consulte más adelante; lo único que cambia
// es que un proveedor deshabilitado no genera ruido visible de arranque.
func TestDisabledProviderMissingEnvIsSilent(t *testing.T) {
	tmpDir := t.TempDir()
	p := filepath.Join(tmpDir, "disabled.toml")
	_ = os.WriteFile(p, []byte(`
schema = 1
[[provider]]
id = "activo"
kind = "openai"
base_url = "http://x"
api_key = "${MISSING_ACTIVO}"
enabled = true

[[provider]]
id = "inactivo"
kind = "openai"
base_url = "http://x"
api_key = "${MISSING_INACTIVO}"
enabled = false
`), 0o600)

	cfg, err := config.Load(config.Options{UserPath: p, SkipProject: true})
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}

	var activo, inactivo *config.Provider
	for i := range cfg.Providers {
		switch cfg.Providers[i].ID {
		case "activo":
			activo = &cfg.Providers[i]
		case "inactivo":
			inactivo = &cfg.Providers[i]
		}
	}

	if activo == nil || activo.AuthOK || activo.MissingEnv != "MISSING_ACTIVO" {
		t.Fatalf("activo debió quedar desautenticado con MissingEnv='MISSING_ACTIVO': %+v", activo)
	}
	if inactivo == nil || inactivo.AuthOK || inactivo.MissingEnv != "MISSING_INACTIVO" {
		t.Fatalf("inactivo también debe registrar el estado internamente: %+v", inactivo)
	}

	var sawActivo, sawInactivo bool
	for _, w := range cfg.Warnings {
		if strings.Contains(w.Msg, "MISSING_ACTIVO") {
			sawActivo = true
		}
		if strings.Contains(w.Msg, "MISSING_INACTIVO") {
			sawInactivo = true
		}
	}
	if !sawActivo {
		t.Error("se esperaba la advertencia de MISSING_ACTIVO (proveedor enabled=true)")
	}
	if sawInactivo {
		t.Error("no se esperaba advertencia de MISSING_INACTIVO (proveedor enabled=false): el ruido de arranque debe callarse para proveedores deshabilitados")
	}
}

// TestExampleTOMLInSync catches a bug this test was written in response to:
// `ishakat config init` writes the *embedded* internal/config/example.toml,
// while the file everyone reads and edits is config.example.toml at the repo
// root. Nothing tied the two together, and they had already drifted — the
// embedded copy had lost the `color` and `glyphs` documentation, so the option
// a Windows user most needs was missing from the very file ishakat hands them.
// Two copies with no check between them will always drift; this is the check.
func TestExampleTOMLInSync(t *testing.T) {
	root, err := os.ReadFile("../../config.example.toml")
	if err != nil {
		t.Fatalf("no se pudo leer config.example.toml: %v", err)
	}
	if config.ExampleTOML != string(root) {
		t.Error("internal/config/example.toml y config.example.toml han divergido.\n" +
			"  `ishakat config init` entrega el embebido, así que el usuario recibiría\n" +
			"  algo distinto de lo que documenta el repo. Copia uno sobre el otro:\n" +
			"    cp config.example.toml internal/config/example.toml")
	}
}

// TestExampleTOMLLoads is the other half: being in sync is worthless if what
// they agree on is invalid. `config init` must never leave a user with a file
// that ishakat itself refuses to start with.
func TestExampleTOMLLoads(t *testing.T) {
	tmpDir := t.TempDir()
	p := filepath.Join(tmpDir, "config.toml")
	if err := os.WriteFile(p, []byte(config.ExampleTOML), 0o600); err != nil {
		t.Fatalf("no se pudo escribir el config temporal: %v", err)
	}
	if _, err := config.Load(config.Options{UserPath: p, SkipProject: true}); err != nil {
		t.Fatalf("el archivo que escribe `config init` no carga: %v", err)
	}
}

// TestMinimalTOMLLoads is TestExampleTOMLLoads's counterpart for the
// skeleton `config init` writes by default (P2 of the 2026-08-06 audit):
// it has no [[provider]] blocks and an empty [app], and must still load
// and validate cleanly on its own — a schema-only file with nothing else
// declared is exactly what a first run looks like before `provider add`.
func TestMinimalTOMLLoads(t *testing.T) {
	tmpDir := t.TempDir()
	p := filepath.Join(tmpDir, "config.toml")
	if err := os.WriteFile(p, []byte(config.MinimalTOML), 0o600); err != nil {
		t.Fatalf("could not write the temp config: %v", err)
	}
	cfg, err := config.Load(config.Options{UserPath: p, SkipProject: true})
	if err != nil {
		t.Fatalf("the file `config init` writes by default does not load: %v", err)
	}
	// The omniroute default from defaults.toml is still expected to be
	// present — the minimal file only skips *documenting* it, it never
	// disables the built-in provider.
	if len(cfg.Providers) == 0 {
		t.Error("expected at least the built-in omniroute provider from defaults.toml, got none")
	}
}

// TestMinimalTOMLIsActuallyMinimal guards the point of this file: it must
// stay far shorter than the fully annotated example, or `--full` stops
// being a meaningfully different choice. Not a byte-exact check (unlike
// TestExampleTOMLInSync) — minimal.toml is free to gain a line or two —
// just a guard against it silently regrowing into a second copy of the
// annotated example.
func TestMinimalTOMLIsActuallyMinimal(t *testing.T) {
	minimalLines := strings.Count(config.MinimalTOML, "\n")
	exampleLines := strings.Count(config.ExampleTOML, "\n")
	if minimalLines >= exampleLines/4 {
		t.Errorf("MinimalTOML has %d lines, ExampleTOML has %d; expected the minimal skeleton to stay well under a quarter of the full example's size", minimalLines, exampleLines)
	}
}

// TestToolsDefaultsLoad guards against the failure mode where validateTools
// exists but never actually sees the embedded defaults: if [tools] in
// defaults.toml drifted out of sync with the schema, Load would emit
// "ignored key" warnings instead of populating these fields, and every
// limit would silently read as its zero value. Asserting on concrete numbers
// (rather than "not zero") is what makes this test able to fail.
func TestToolsDefaultsLoad(t *testing.T) {
	tmpDir := t.TempDir()
	p := filepath.Join(tmpDir, "minimal.toml")
	if err := os.WriteFile(p, []byte("schema = 1\n"), 0o600); err != nil {
		t.Fatalf("no se pudo escribir el config temporal: %v", err)
	}

	cfg, err := config.Load(config.Options{UserPath: p, SkipProject: true})
	if err != nil {
		t.Fatalf("los defaults embebidos no pasan Validate: %v", err)
	}

	for _, w := range cfg.Warnings {
		if strings.Contains(w.Msg, "tools") || strings.Contains(w.Where, "tools") {
			t.Errorf("advertencia inesperada sobre [tools]: [%s] %s", w.Where, w.Msg)
		}
	}

	if !cfg.Tools.Enabled {
		t.Error("tools.enabled debió ser true por defecto")
	}
	if cfg.Tools.MaxTools != 40 {
		t.Errorf("max_tools = %d, esperado 40 (§19.5 entropy cap)", cfg.Tools.MaxTools)
	}
	if cfg.Tools.MaxCallsPerTurn != 25 {
		t.Errorf("max_calls_per_turn = %d, esperado 25", cfg.Tools.MaxCallsPerTurn)
	}
	if cfg.Tools.MaxOutputBytes != 32768 {
		t.Errorf("max_output_bytes = %d, esperado 32768", cfg.Tools.MaxOutputBytes)
	}
	if cfg.Tools.ArchiveDays != 90 {
		t.Errorf("archive_days = %d, esperado 90", cfg.Tools.ArchiveDays)
	}

	// §19.6 gate 2: the human must stay in the loop by default.
	if cfg.Tools.Permissions.Read != "allow" {
		t.Errorf("permissions.read = %q, esperado \"allow\"", cfg.Tools.Permissions.Read)
	}
	if cfg.Tools.Permissions.Write != "ask" {
		t.Errorf("permissions.write = %q, esperado \"ask\"", cfg.Tools.Permissions.Write)
	}
	if cfg.Tools.Permissions.Shell != "ask" {
		t.Errorf("permissions.shell = %q, esperado \"ask\"", cfg.Tools.Permissions.Shell)
	}

	// An empty deny list is the dangerous failure: it looks like a working
	// config and blocks nothing at all.
	if len(cfg.Tools.Permissions.ShellDeny) == 0 {
		t.Error("shell_deny está vacío: los bloqueos duros de §19.8 no se cargaron")
	}
	if len(cfg.Tools.Permissions.WriteDeny) == 0 {
		t.Error("write_deny está vacío: los bloqueos duros de §19.8 no se cargaron")
	}
	for _, want := range []string{"~/.ssh/**", "**/.env"} {
		found := false
		for _, got := range cfg.Tools.Permissions.WriteDeny {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("write_deny no contiene %q", want)
		}
	}

	if cfg.Tools.Evolve.Mode != "suggest" {
		t.Errorf("evolve.mode = %q, esperado \"suggest\" (§19.7)", cfg.Tools.Evolve.Mode)
	}
	if cfg.Tools.Evolve.MinRepeats != 3 {
		t.Errorf("evolve.min_repeats = %d, esperado 3", cfg.Tools.Evolve.MinRepeats)
	}
	if cfg.Tools.Evolve.DedupThreshold != 0.8 {
		t.Errorf("evolve.dedup_threshold = %g, esperado 0.8", cfg.Tools.Evolve.DedupThreshold)
	}
	if !cfg.Tools.Evolve.RequireSelftest {
		t.Error("evolve.require_selftest debió ser true: gate 3 de §19.6")
	}
	if cfg.Tools.Evolve.AllowWithoutTTY {
		t.Error("evolve.allow_without_tty debió ser false: sin TTY no hay quién autorice")
	}

	if cfg.Tools.Egress.AllowAll {
		t.Error("egress.allow_all debió ser false por defecto")
	}
	if len(cfg.Tools.Egress.Allow) == 0 {
		t.Error("egress.allow está vacío: la lista blanca inicial no se cargó")
	}
}

// TestToolsFatalErrors proves validateTools actually rejects. Each case is a
// value that has no safe interpretation, so starting up with it would be worse
// than refusing to start.
func TestToolsFatalErrors(t *testing.T) {
	tests := []struct {
		name      string
		toml      string
		wantMatch string
	}{
		{
			name:      "permiso_mal_escrito",
			toml:      "schema=1\n[tools.permissions]\nwrite=\"alow\"\n",
			wantMatch: "no reconocido",
		},
		{
			name:      "permiso_inventado",
			toml:      "schema=1\n[tools.permissions]\nshell=\"maybe\"\n",
			wantMatch: "\"ask\", \"allow\" o \"deny\"",
		},
		{
			name:      "modo_evolve_invalido",
			toml:      "schema=1\n[tools.evolve]\nmode=\"aggressive\"\n",
			wantMatch: "on_request",
		},
		{
			name:      "dedup_threshold_cero",
			toml:      "schema=1\n[tools.evolve]\ndedup_threshold=0.0\n",
			wantMatch: "fuera de rango",
		},
		{
			name:      "dedup_threshold_mayor_que_uno",
			toml:      "schema=1\n[tools.evolve]\ndedup_threshold=1.5\n",
			wantMatch: "fuera de rango",
		},
		{
			name:      "limite_negativo",
			toml:      "schema=1\n[tools]\nmax_tools=-1\n",
			wantMatch: "no puede ser negativo",
		},
		{
			name:      "presupuesto_negativo",
			toml:      "schema=1\n[tools]\nbudget_usd=-5.0\n",
			wantMatch: "budget_usd",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			p := filepath.Join(tmpDir, "invalid.toml")
			if err := os.WriteFile(p, []byte(tt.toml), 0o600); err != nil {
				t.Fatalf("no se pudo escribir el config temporal: %v", err)
			}
			_, err := config.Load(config.Options{UserPath: p, SkipProject: true})
			if err == nil {
				t.Fatal("se esperaba error, pero la carga fue exitosa")
			}
			if !strings.Contains(err.Error(), tt.wantMatch) {
				t.Errorf("el mensaje %q no contiene %q", err.Error(), tt.wantMatch)
			}
		})
	}
}

// TestToolsWarnings covers the settings that are legal but unsafe. These must
// warn and still load: refusing to start would take away a choice the user is
// entitled to make, but making it silently would be worse.
func TestToolsWarnings(t *testing.T) {
	tests := []struct {
		name      string
		toml      string
		wantMatch string
	}{
		{
			name:      "sin_llamadas_por_turno",
			toml:      "schema=1\n[tools]\nenabled=true\nmax_calls_per_turn=0\n",
			wantMatch: "ninguna herramienta podrá ejecutarse",
		},
		{
			name:      "sin_tty",
			toml:      "schema=1\n[tools.evolve]\nallow_without_tty=true\n",
			wantMatch: "sin que nadie autorice",
		},
		{
			name:      "sin_selftest",
			toml:      "schema=1\n[tools.evolve]\nrequire_selftest=false\n",
			wantMatch: "sin haberse probado",
		},
		{
			name:      "egress_abierto",
			toml:      "schema=1\n[tools.egress]\nallow_all=true\n",
			wantMatch: "cualquier host",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			p := filepath.Join(tmpDir, "warn.toml")
			if err := os.WriteFile(p, []byte(tt.toml), 0o600); err != nil {
				t.Fatalf("no se pudo escribir el config temporal: %v", err)
			}
			cfg, err := config.Load(config.Options{UserPath: p, SkipProject: true})
			if err != nil {
				t.Fatalf("debió cargar con advertencia, no fallar: %v", err)
			}
			found := false
			for _, w := range cfg.Warnings {
				if strings.Contains(w.Msg, tt.wantMatch) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("no se emitió una advertencia que contenga %q; advertencias: %+v",
					tt.wantMatch, cfg.Warnings)
			}
		})
	}
}

// TestEvolveOffSkipsSelftestWarning documents a deliberate asymmetry: with
// self-extension disabled, require_selftest is dead configuration and warning
// about it would be noise. Warnings only earn their place when they describe a
// risk that is actually reachable.
func TestEvolveOffSkipsSelftestWarning(t *testing.T) {
	tmpDir := t.TempDir()
	p := filepath.Join(tmpDir, "off.toml")
	if err := os.WriteFile(p, []byte("schema=1\n[tools.evolve]\nmode=\"off\"\nrequire_selftest=false\n"), 0o600); err != nil {
		t.Fatalf("no se pudo escribir el config temporal: %v", err)
	}
	cfg, err := config.Load(config.Options{UserPath: p, SkipProject: true})
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	for _, w := range cfg.Warnings {
		if strings.Contains(w.Msg, "sin haberse probado") {
			t.Errorf("con mode=\"off\" no debió advertir sobre require_selftest: %s", w.Msg)
		}
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

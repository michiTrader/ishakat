package config

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/BurntSushi/toml"
	"github.com/MichiTrader/ishakat/internal/xdg"
)

//go:embed defaults.toml
var defaultsTOML string

type Options struct {
	UserPath    string
	ProjectPath string
	SkipProject bool
	Overrides   map[string]any
}

func Load(o Options) (*Config, error) {
	if o.UserPath == "" {
		o.UserPath = xdg.ConfigFile()
	}
	if o.ProjectPath == "" {
		o.ProjectPath = ".ishakat.toml"
	}

	raw := map[string]any{}
	var files []string
	var warns []Warning

	if _, err := toml.Decode(defaultsTOML, &raw); err != nil {
		return nil, fmt.Errorf("defaults embebidos corruptos: %w", err)
	}

	layers := []string{o.UserPath}
	if !o.SkipProject {
		layers = append(layers, o.ProjectPath)
	}
	// Credentials are a final user-owned layer: they override provider keys and
	// activation without changing shareable project configuration.
	layers = append(layers, xdg.CredentialsFile())
	for _, p := range layers {
		b, err := os.ReadFile(p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		var m map[string]any
		if _, err := toml.Decode(string(b), &m); err != nil {
			return nil, fmt.Errorf("%s: TOML inválido: %w", p, err)
		}
		raw = mergeRoot(raw, m)
		files = append(files, p)
		warns = append(warns, checkPerms(p)...)
	}

	applyEnv(raw)
	for path, v := range o.Overrides {
		setPath(raw, path, v)
	}

	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(raw); err != nil {
		return nil, fmt.Errorf("no se pudo normalizar la configuración: %w", err)
	}
	cfg := &Config{EnvUsed: map[string]string{}}
	md, err := toml.Decode(buf.String(), cfg)
	if err != nil {
		return nil, err
	}
	for _, k := range md.Undecoded() {
		warns = append(warns, Warning{Where: "config", Msg: "clave ignorada: " + k.String()})
	}

	cfg.Files = files
	warns = append(warns, expandVars(cfg)...)
	cfg.Warnings = append(cfg.Warnings, warns...)

	if err := Validate(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

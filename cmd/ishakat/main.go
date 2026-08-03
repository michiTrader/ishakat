package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"runtime"
	"strings"

	"github.com/charmbracelet/x/term"

	"github.com/MichiTrader/ishakat/internal/app"
	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/netfix"
	"github.com/MichiTrader/ishakat/internal/xdg"
)

var version = "dev"

// usage documents the headless-mode flags added on top of the existing
// subcommands. New user-facing text goes in English per the project's
// language policy (AGENTS.md); the pre-existing Spanish subcommand output
// below is left as-is for now.
const usage = `ishakat — talk to AI models from the terminal

USAGE
  ishakat                         opens the interactive interface
  ishakat --resume                reopens the last saved conversation
  ishakat -p "question"           answers and exits (headless mode)
  cat log | ishakat -p "explain"  reads stdin and appends it to the prompt
  ishakat <subcommand>

SUBCOMMANDS
  config init|path|check   creates, locates or validates the configuration
  doctor                   network, path and dialect diagnostics
  models [--json] [--refresh] [--all] [filter]   the model catalog
  version                  prints the version

FLAGS
  -p, --prompt <text>    question to answer without opening the interface
  -m, --model <ref>      model to use (ref, alias or wire_id)
      --system <text>    system prompt for this turn
      --json             one JSON event per line (for jq)
      --stream           force streaming
      --no-stream        request the full response at once
      --no-save          do not write the session file
      --resume           reopen the last saved conversation (interactive mode only)
  -q, --quiet            no warnings on stderr
      --config <path>    use a different config.toml
  -h, --help             this help text
  -v, --version          version

EXIT CODES
  0 ok · 1 error · 2 bad usage · 130 cancelled with Ctrl+C
`

func main() {
	_ = netfix.Install()

	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") {
		switch os.Args[1] {
		case "config":
			os.Exit(cmdConfig(os.Args[2:]))
		case "doctor":
			os.Exit(cmdDoctor())
		case "version":
			fmt.Println("ishakat", version)
			return
		case "models":
			os.Exit(cmdModels(os.Args[2:]))
		case "help":
			fmt.Print(usage)
			return
		}
	}

	fs := flag.NewFlagSet("ishakat", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }

	var (
		prompt     = fs.String("p", "", "question to answer without opening the interface")
		promptLong = fs.String("prompt", "", "same as -p")
		model      = fs.String("m", "", "model to use")
		modelLong  = fs.String("model", "", "same as -m")
		system     = fs.String("system", "", "system prompt for this turn")
		jsonOut    = fs.Bool("json", false, "one JSON event per line")
		stream     = fs.Bool("stream", false, "force streaming")
		noStream   = fs.Bool("no-stream", false, "request the full response at once")
		noSave     = fs.Bool("no-save", false, "do not write the session file")
		resume     = fs.Bool("resume", false, "reopen the last saved conversation (interactive mode only)")
		quiet      = fs.Bool("q", false, "no warnings on stderr")
		quietLong  = fs.Bool("quiet", false, "same as -q")
		cfgPath    = fs.String("config", "", "alternate config.toml path")
		showVer    = fs.Bool("v", false, "version")
		showVerL   = fs.Bool("version", false, "version")
	)

	if err := fs.Parse(os.Args[1:]); err != nil {
		// flag already printed the error and the usage text.
		os.Exit(app.ExitUsage)
	}

	if *showVer || *showVerL {
		fmt.Println("ishakat", version)
		return
	}

	p := firstNonEmpty(*prompt, *promptLong)
	// Bare positional arguments are treated as part of the prompt:
	// `ishakat -p say hi` is what people actually type, and failing on that
	// would be pedantic.
	if rest := fs.Args(); len(rest) > 0 {
		p = strings.TrimSpace(p + " " + strings.Join(rest, " "))
	}

	var streamPtr *bool
	switch {
	case *noStream:
		f := false
		streamPtr = &f
	case *stream:
		t := true
		streamPtr = &t
	}
	var savePtr *bool
	if *noSave {
		f := false
		savePtr = &f
	}

	// The TUI/headless decision. Headless is chosen when the user asks for
	// it explicitly (-p, --json) and also when the environment doesn't give
	// a terminal: `echo hi | ishakat` and `ishakat > out.txt` should do
	// something sensible instead of trying to draw an interface over a
	// pipe.
	stdinTTY := term.IsTerminal(os.Stdin.Fd())
	stdoutTTY := term.IsTerminal(os.Stdout.Fd())
	headless := p != "" || *jsonOut || !stdinTTY || !stdoutTTY

	if headless {
		os.Exit(app.Headless(app.HeadlessOptions{
			Version:    version,
			Prompt:     p,
			Model:      firstNonEmpty(*model, *modelLong),
			System:     *system,
			JSON:       *jsonOut,
			Stream:     streamPtr,
			Save:       savePtr,
			Quiet:      *quiet || *quietLong,
			ConfigPath: *cfgPath,
		}))
	}

	os.Exit(app.Run(version, *resume))
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func cmdConfig(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "uso: ishakat config <init|path|check> [flags]")
		return 2
	}

	switch args[0] {
	case "path":
		fmt.Println(xdg.ConfigFile())
		return 0

	case "init":
		fs := flag.NewFlagSet("config init", flag.ExitOnError)
		force := fs.Bool("force", false, "sobrescribe la configuración si ya existe")
		_ = fs.Parse(args[1:])

		path := xdg.ConfigFile()
		if err := xdg.EnsureDir(xdg.ConfigDir()); err != nil {
			fmt.Fprintf(os.Stderr, "✗ Error creando directorio de configuración: %v\n", err)
			return 1
		}

		if _, err := os.Stat(path); err == nil && !*force {
			fmt.Fprintf(os.Stderr, "✗ El archivo %s ya existe. Usa --force para sobrescribirlo.\n", path)
			return 1
		}

		content := config.ExampleTOML
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "✗ Error al escribir %s: %v\n", path, err)
			return 1
		}
		fmt.Printf("✓ Configuración inicial creada en: %s (0600)\n", path)
		return 0

	case "check":
		fs := flag.NewFlagSet("config check", flag.ExitOnError)
		strict := fs.Bool("strict", false, "trata las advertencias como errores")
		_ = fs.Parse(args[1:])

		path := xdg.ConfigFile()
		if len(fs.Args()) > 0 {
			path = fs.Arg(0)
		}

		cfg, err := config.Load(config.Options{UserPath: path, SkipProject: true})
		if err != nil {
			fmt.Fprintf(os.Stderr, "✗ Error de configuración: %v\n", err)
			return 1
		}

		fmt.Printf("✓ Configuración válida (%d proveedor(es) cargado(s))\n", len(cfg.Providers))
		if len(cfg.Files) > 0 {
			fmt.Println("  Capas leídas:", strings.Join(cfg.Files, ", "))
		}

		if len(cfg.Warnings) > 0 {
			fmt.Printf("  %d advertencia(s):\n", len(cfg.Warnings))
			for _, w := range cfg.Warnings {
				fmt.Printf("    - [%s] %s\n", w.Where, w.Msg)
			}
			if *strict {
				fmt.Fprintln(os.Stderr, "✗ Fallo por flag --strict")
				return 1
			}
		}
		return 0

	default:
		fmt.Fprintf(os.Stderr, "subcomando desconocido: ishakat config %s\n", args[0])
		return 2
	}
}

// cmdModels is the `ishakat models` subcommand of Step 6: a text table or
// one-JSON-per-line dump of the catalog snapshot, entirely offline unless
// --refresh is passed.
func cmdModels(args []string) int {
	fs := flag.NewFlagSet("models", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOut := fs.Bool("json", false, "one JSON object per line, for jq")
	refresh := fs.Bool("refresh", false, "go to the network before printing")
	all := fs.Bool("all", false, "also show deprecated models hidden by catalog.hide_deprecated")
	filter := fs.String("filter", "", "keep only refs/names containing this substring")
	cfgPath := fs.String("config", "", "alternate config.toml path")
	if err := fs.Parse(args); err != nil {
		return app.ExitUsage
	}
	if rest := fs.Args(); len(rest) > 0 && *filter == "" {
		*filter = strings.Join(rest, " ")
	}

	return app.Models(app.ModelsOptions{
		Version:    version,
		JSON:       *jsonOut,
		Refresh:    *refresh,
		All:        *all,
		Filter:     *filter,
		ConfigPath: *cfgPath,
	})
}

func cmdDoctor() int {
	rep := netfix.Install()

	fmt.Printf("ishakat %s · doctor\n\n", version)
	fmt.Printf("  go           %s\n", runtime.Version())
	fmt.Printf("  plataforma   %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Printf("  cgo          %v\n", netfix.CGOEnabled)
	fmt.Printf("  android      %v\n", rep.Android)
	fmt.Printf("  termux       %v\n", xdg.IsTermux())
	fmt.Printf("  resolv.conf  %v\n", rep.ResolvConf)
	fmt.Printf("  resolver     %s\n", rep.Resolver())
	if len(rep.Servers) > 0 {
		fmt.Printf("  dns          %s  (%s)\n", strings.Join(rep.Servers, ", "), rep.Source)
	}
	cfg, cfgNote := doctorConfig()

	fmt.Println()
	fmt.Printf("  config path  %s\n", xdg.ConfigFile())
	fmt.Printf("  config read  %s\n", cfgNote)
	fmt.Printf("  cache dir    %s\n", xdg.CacheDir())
	fmt.Printf("  data dir     %s\n", xdg.DataDir())
	fmt.Printf("  state dir    %s\n", xdg.StateDir())
	fmt.Println()

	// The terminal section: how the working directory renders, what the
	// interface decided about colour and characters, and a sample of those
	// characters so the guess can be checked by eye.
	reportTerminal(os.Stdout, cfg)
	fmt.Println()

	// Registered dialects come from app: importing the package is what
	// triggers provider/openai's init(), and doctor must report what the
	// binary can actually speak, not a hand-written list.
	fmt.Printf("  dialects     %s\n", strings.Join(app.Dialects(), ", "))
	fmt.Println()

	fmt.Print("  probando DNS (models.dev)... ")
	ips, err := net.LookupHost("models.dev")
	if err != nil {
		fmt.Printf("FALLÓ: %v\n", err)
	} else {
		fmt.Printf("OK (%s)\n", strings.Join(ips, ", "))
	}

	return 0
}

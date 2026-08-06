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
  config init [--full]     creates the configuration (minimal by default; --full for the annotated example)
  config path|check        locates or validates the configuration
  provider add|list|remove configure API credentials without editing TOML
  doctor                   network, path and dialect diagnostics
  models [--json] [--refresh] [--all] [filter]   the model catalog
  models clean             delete the cached catalog (catalog.json) on disk
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

// knownSubcommands lists every first-word dispatch target the switch in
// main() recognizes (mirrored here, rather than derived from the switch via
// reflection, because there's no cheap way to introspect a switch
// statement). cmdUnknownSubcommand's "did you mean" suggestion walks this
// list; keep it in sync with the switch below when a subcommand is added.
var knownSubcommands = []string{"config", "provider", "doctor", "version", "models", "help"}

func main() {
	_ = netfix.Install()

	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") {
		switch os.Args[1] {
		case "config":
			os.Exit(cmdConfig(os.Args[2:]))
		case "provider", "providers":
			os.Exit(cmdProvider(os.Args[2:]))
		case "doctor":
			os.Exit(cmdDoctor())
		case "version":
			fmt.Println("ishakat", version)
			return
		case "models":
			if len(os.Args) > 2 && os.Args[2] == "clean" {
				os.Exit(cmdModelsClean(os.Args[3:]))
			}
			os.Exit(cmdModels(os.Args[2:]))
		case "help":
			fmt.Print(usage)
			return
		default:
			// A bare first word that isn't one of the subcommands above used
			// to fall straight through to the flag.FlagSet below, whose
			// "bare positionals join the prompt" rule (see the comment on
			// `rest := fs.Args()` further down) then silently turned a
			// mistyped subcommand into a chat prompt: `ishakat add provider
			// nvidia --no-verify` (the words of `ishakat provider add
			// nvidia --no-verify` reversed) parsed as prompt text "add
			// provider nvidia --no-verify" sent to app.default_model, with
			// no usage error at all — the flag package also stops parsing
			// flags at the first non-flag argument, so --no-verify was never
			// recognized either; it just became more prompt text. There is
			// no supported way to send a prompt without -p/--prompt or a
			// pipe, so a lone unrecognized word can never legitimately mean
			// "answer this" — it is always a usage mistake now.
			os.Exit(cmdUnknownSubcommand(os.Args[1]))
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

// cmdUnknownSubcommand reports a mistyped subcommand as a usage error
// (exit 2) instead of letting it silently become a chat prompt — see the
// comment on the `default` case in main() for the bug this replaces.
func cmdUnknownSubcommand(word string) int {
	fmt.Fprintf(os.Stderr, "ishakat: unknown subcommand %q\n", word)
	if guess := closestSubcommand(word); guess != "" {
		fmt.Fprintf(os.Stderr, "did you mean %q?\n\n", guess)
	} else {
		fmt.Fprintln(os.Stderr)
	}
	fmt.Fprint(os.Stderr, usage)
	return app.ExitUsage
}

// closestSubcommand returns the known subcommand closest to word by edit
// distance, capped so an unrelated word ("frobnicate") gets no suggestion
// rather than a misleading one. Ties keep the first (alphabetical) match.
func closestSubcommand(word string) string {
	word = strings.ToLower(word)
	best := ""
	bestDist := -1
	for _, cand := range knownSubcommands {
		d := levenshtein(word, cand)
		if bestDist == -1 || d < bestDist {
			bestDist, best = d, cand
		}
	}
	// A distance larger than the candidate's own length means "no
	// resemblance at all" (e.g. comparing against "help" is worthless once
	// the edit count exceeds 4); 3 is a generous cap that still catches
	// single-typo and transposed-word mistakes like "add provider".
	if bestDist < 0 || bestDist > 3 {
		return ""
	}
	return best
}

func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	curr := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		curr[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			m := del
			if ins < m {
				m = ins
			}
			if sub < m {
				m = sub
			}
			curr[j] = m
		}
		prev, curr = curr, prev
	}
	return prev[len(rb)]
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
		fmt.Fprintln(os.Stderr, "usage: ishakat config <init|path|check> [flags]")
		return 2
	}

	switch args[0] {
	case "path":
		fmt.Println(xdg.ConfigFile())
		return 0

	case "init":
		fs := flag.NewFlagSet("config init", flag.ExitOnError)
		force := fs.Bool("force", false, "overwrite the configuration if it already exists")
		full := fs.Bool("full", false, "write the fully annotated example instead of the minimal skeleton")
		_ = fs.Parse(args[1:])

		path := xdg.ConfigFile()
		if err := xdg.EnsureDir(xdg.ConfigDir()); err != nil {
			fmt.Fprintf(os.Stderr, "✗ Error creating configuration directory: %v\n", err)
			return 1
		}

		if _, err := os.Stat(path); err == nil && !*force {
			fmt.Fprintf(os.Stderr, "✗ File %s already exists. Use --force to overwrite it.\n", path)
			return 1
		}

		// Default to a minimal skeleton — schema, an empty [app], and a
		// comment pointing at `provider add` — rather than the fully
		// annotated example: a brand-new user's first encounter with the
		// file used to be ~200 lines documenting every knob, most of them
		// already fine at their built-in default. --full opts back into
		// that for anyone who wants to read every option inline.
		content := config.MinimalTOML
		kind := "minimal"
		if *full {
			content = config.ExampleTOML
			kind = "full example"
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "✗ Error writing %s: %v\n", path, err)
			return 1
		}
		fmt.Printf("✓ Initial configuration (%s) created at: %s (0600)\n", kind, path)
		return 0

	case "check":
		fs := flag.NewFlagSet("config check", flag.ExitOnError)
		strict := fs.Bool("strict", false, "treat warnings as errors")
		_ = fs.Parse(args[1:])

		path := xdg.ConfigFile()
		if len(fs.Args()) > 0 {
			path = fs.Arg(0)
		}

		cfg, err := config.Load(config.Options{UserPath: path, SkipProject: true})
		if err != nil {
			fmt.Fprintf(os.Stderr, "✗ Configuration error: %v\n", err)
			return 1
		}

		fmt.Printf("✓ Valid configuration (%d provider(s) loaded)\n", len(cfg.Providers))
		if len(cfg.Files) > 0 {
			fmt.Println("  Layers read:", strings.Join(cfg.Files, ", "))
		}

		if len(cfg.Warnings) > 0 {
			fmt.Printf("  %d warning(s):\n", len(cfg.Warnings))
			for _, w := range cfg.Warnings {
				fmt.Printf("    - [%s] %s\n", w.Where, w.Msg)
			}
			if *strict {
				fmt.Fprintln(os.Stderr, "✗ Failing due to --strict")
				return 1
			}
		}
		return 0

	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: ishakat config %s\n", args[0])
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

// cmdModelsClean implements `ishakat models clean`: it deletes the on-disk
// catalog cache (catalog.json and its models.dev digest sibling), which
// lives under $XDG_CACHE_HOME/ishakat and therefore survives deleting
// $XDG_CONFIG_HOME/ishakat (config.toml, credentials.toml) — a distinction
// that is not obvious from the outside and has caused real confusion: a
// provider removed from config.toml, or a gateway that stopped answering,
// leaves its last successful discovery sitting in this cache and showing up
// in `ishakat models` until either a fresh discovery overwrites it or the
// file is deleted by hand. This subcommand is that "by hand" step, without
// requiring the user to know the path.
func cmdModelsClean(args []string) int {
	fs := flag.NewFlagSet("models clean", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	cfgPath := fs.String("config", "", "alternate config.toml path")
	if err := fs.Parse(args); err != nil {
		return app.ExitUsage
	}

	path := *cfgPath
	if path == "" {
		path = xdg.ConfigFile()
	}
	cfg, err := config.Load(config.Options{UserPath: path})
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ configuration error: %v\n", err)
		return app.ExitError
	}

	res, err := app.CleanCatalogCache(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ could not clear the catalog cache: %v\n", err)
		return app.ExitError
	}

	switch {
	case res.CacheRemoved || res.DigestRemoved:
		fmt.Printf("Removed %s\n", res.CachePath)
		if res.DigestRemoved {
			fmt.Printf("Removed %s\n", res.DigestPath)
		}
		fmt.Println("The catalog cache is empty; the next `ishakat models --refresh` " +
			"starts from a clean discovery for every enabled provider.")
	default:
		fmt.Printf("Nothing to remove: %s did not exist.\n", res.CachePath)
	}
	return app.ExitOK
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

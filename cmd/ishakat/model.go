package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/MichiTrader/ishakat/internal/config"
)

// cmdModel is P3c: the ergonomic layer the original bug report asked for
// on top of `ishakat model set/alias/favorite` config.toml mutators
// (internal/config/connection.go) — "a command instead of hand-editing
// TOML" for the three things a user actually wants to change after
// `provider add`: which model is the default, what an alias points at, and
// which refs show up as favorites in the picker.
//
// This is deliberately a SEPARATE subcommand from `provider`
// (cmd/ishakat/provider.go): `provider add/remove` is about credentials and
// which provider is active at all; `model set/alias/favorite` is about
// which already-configured model is used where. Mixing the two would make
// `ishakat provider set gemini-2.0-flash` and `ishakat model set gemini`
// both plausible-looking typos for the same command.
func cmdModel(args []string) int {
	if len(args) == 0 {
		printModelUsage(os.Stderr)
		return 2
	}
	switch args[0] {
	case "set":
		return cmdModelSet(args[1:])
	case "alias":
		return cmdModelAlias(args[1:])
	case "favorite", "favorites", "fav":
		return cmdModelFavorite(args[1:])
	case "help", "--help", "-h":
		printModelUsage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown model command %q\n", args[0])
		printModelUsage(os.Stderr)
		return 2
	}
}

func printModelUsage(w io.Writer) {
	fmt.Fprintln(w, `ishakat model — point app.default_model/compact_model/fallback_model,
aliases and favorites at a model without hand-editing config.toml

USAGE
  ishakat model set <ref>                  sets app.default_model (the ordinary case)
  ishakat model set <ref> --compact        sets app.compact_model instead
  ishakat model set <ref> --fallback       sets app.fallback_model instead
  ishakat model set <ref> --all            sets default_model, compact_model and fallback_model together
  ishakat model set "" --compact           resets compact_model to "follow default_model"

  ishakat model alias set <name> <ref>     ishakat model alias set smart gemini-direct/gemini-2.0-pro
  ishakat model alias remove <name>

  ishakat model favorite add <ref>         ishakat model favorite add gemini-direct/gemini-2.0-flash
  ishakat model favorite remove <ref>

<ref> is a model reference: "provider/wire_id" (e.g. "gemini-direct/gemini-2.0-flash"),
a bare wire_id (uses the first enabled provider), or an existing alias.
None of these subcommands verify the reference against a live provider —
use `+"`ishakat models`"+` to see what is actually discovered, or `+"`ishakat provider add`"+`
first if the provider itself isn't configured yet.`)
}

// cmdModelSet implements `ishakat model set <ref> [--default|--compact|
// --fallback|--all]`. With no role flag, --default is assumed: that is the
// single most common edit ("point ishakat at a different default model"),
// and it mirrors what `provider add`'s own offerDefaultModel prompt already
// does non-interactively.
func cmdModelSet(args []string) int {
	fs := flag.NewFlagSet("model set", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	def := fs.Bool("default", false, "set app.default_model (the default if no role flag is given)")
	defShort := fs.Bool("d", false, "same as --default")
	compact := fs.Bool("compact", false, "set app.compact_model")
	compactShort := fs.Bool("c", false, "same as --compact")
	fallback := fs.Bool("fallback", false, "set app.fallback_model")
	fallbackShort := fs.Bool("f", false, "same as --fallback")
	all := fs.Bool("all", false, "set default_model, compact_model and fallback_model together")
	allShort := fs.Bool("a", false, "same as --all")

	// flag.Parse stops at the FIRST non-flag argument and treats
	// everything after it (flags included) as positional — see Go's own
	// FlagSet.Parse doc comment. Every usage example above puts <ref>
	// BEFORE the role flag ("model set <ref> --compact"), which is
	// exactly the ordering that trips this: fs.Parse([]string{ref,
	// "--compact"}) would leave --compact unparsed in fs.Args() and this
	// function would reject it as an "extra argument" instead of setting
	// compact_model. positionals/flags are partitioned by hand here so
	// role flags are recognized regardless of where they appear.
	positionals, flags := splitFlagsFromPositionals(args)
	if err := fs.Parse(flags); err != nil {
		return 2
	}

	rest := positionals
	if len(rest) == 0 {
		fmt.Fprintln(os.Stderr, "usage: ishakat model set <ref> [--default|--compact|--fallback|--all]")
		return 2
	}
	// The reference itself may be "" (`ishakat model set "" --compact`,
	// the documented way to reset compact_model/fallback_model back to
	// "follow default_model" — see SetAppModel's own doc comment), so it is
	// taken as rest[0] exactly, not joined/trimmed the way a chat prompt's
	// bare positionals are in main().
	ref := rest[0]
	if len(rest) > 1 {
		fmt.Fprintf(os.Stderr, "usage: ishakat model set <ref> [--default|--compact|--fallback|--all] "+
			"(got extra arguments: %s)\n", strings.Join(rest[1:], " "))
		return 2
	}

	roleDefault := *def || *defShort
	roleCompact := *compact || *compactShort
	roleFallback := *fallback || *fallbackShort
	roleAll := *all || *allShort
	rolesGiven := 0
	for _, v := range []bool{roleDefault, roleCompact, roleFallback, roleAll} {
		if v {
			rolesGiven++
		}
	}
	if rolesGiven > 1 {
		fmt.Fprintln(os.Stderr, "use only one of --default, --compact, --fallback, --all")
		return 2
	}

	var keys []config.AppModelKey
	switch {
	case roleAll:
		keys = []config.AppModelKey{config.AppModelDefault, config.AppModelCompact, config.AppModelFallback}
	case roleCompact:
		keys = []config.AppModelKey{config.AppModelCompact}
	case roleFallback:
		keys = []config.AppModelKey{config.AppModelFallback}
	default:
		// roleDefault, or no flag at all: --default is the assumed role.
		keys = []config.AppModelKey{config.AppModelDefault}
	}

	for _, key := range keys {
		if err := config.SetAppModel(key, ref); err != nil {
			fmt.Fprintf(os.Stderr, "could not set app.%s: %v\n", key, err)
			return 1
		}
	}

	if ref == "" {
		fmt.Printf("app.%s reset to \"\" (follows app.default_model).\n", joinKeys(keys))
	} else {
		fmt.Printf("app.%s set to %s.\n", joinKeys(keys), ref)
	}
	return 0
}

// splitFlagsFromPositionals partitions args into positional arguments and
// flag arguments, preserving each group's relative order. It exists solely
// to work around flag.Parse's "stop at first positional" rule (see
// cmdModelSet's own comment) for a flag set made entirely of boolean
// switches (--default/-d, --compact/-c, --fallback/-f, --all/-a): none of
// them take a value, so "does this token start with '-'" is a complete and
// correct test here. It is not a general-purpose flag parser and must not
// be reused for a flag set that has value-taking flags (e.g. "--foo bar"),
// where the separate value token would be misclassified as positional.
func splitFlagsFromPositionals(args []string) (positionals, flags []string) {
	for _, a := range args {
		if strings.HasPrefix(a, "-") && a != "-" {
			flags = append(flags, a)
		} else {
			positionals = append(positionals, a)
		}
	}
	return positionals, flags
}

func joinKeys(keys []config.AppModelKey) string {
	strs := make([]string, len(keys))
	for i, k := range keys {
		strs[i] = string(k)
	}
	return strings.Join(strs, ", ")
}

// cmdModelAlias implements `ishakat model alias set|remove`.
func cmdModelAlias(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: ishakat model alias <set|remove> ...")
		return 2
	}
	switch args[0] {
	case "set":
		rest := args[1:]
		if len(rest) != 2 {
			fmt.Fprintln(os.Stderr, "usage: ishakat model alias set <name> <ref>")
			return 2
		}
		if err := config.SetAlias(rest[0], rest[1]); err != nil {
			fmt.Fprintf(os.Stderr, "could not set alias: %v\n", err)
			return 1
		}
		fmt.Printf("Alias %q now points to %s.\n", rest[0], rest[1])
		return 0
	case "remove", "delete":
		rest := args[1:]
		if len(rest) != 1 {
			fmt.Fprintln(os.Stderr, "usage: ishakat model alias remove <name>")
			return 2
		}
		if err := config.RemoveAlias(rest[0]); err != nil {
			fmt.Fprintf(os.Stderr, "could not remove alias: %v\n", err)
			return 1
		}
		fmt.Printf("Alias %q removed (no-op if it didn't exist).\n", rest[0])
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown model alias command %q\n", args[0])
		fmt.Fprintln(os.Stderr, "usage: ishakat model alias <set|remove> ...")
		return 2
	}
}

// cmdModelFavorite implements `ishakat model favorite add|remove`.
func cmdModelFavorite(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: ishakat model favorite <add|remove> <ref>")
		return 2
	}
	switch args[0] {
	case "add":
		rest := args[1:]
		if len(rest) != 1 {
			fmt.Fprintln(os.Stderr, "usage: ishakat model favorite add <ref>")
			return 2
		}
		if err := config.AddFavorite(rest[0]); err != nil {
			fmt.Fprintf(os.Stderr, "could not add favorite: %v\n", err)
			return 1
		}
		fmt.Printf("%s added to favorites.\n", rest[0])
		return 0
	case "remove", "delete":
		rest := args[1:]
		if len(rest) != 1 {
			fmt.Fprintln(os.Stderr, "usage: ishakat model favorite remove <ref>")
			return 2
		}
		if err := config.RemoveFavorite(rest[0]); err != nil {
			fmt.Fprintf(os.Stderr, "could not remove favorite: %v\n", err)
			return 1
		}
		fmt.Printf("%s removed from favorites (no-op if it wasn't one).\n", rest[0])
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown model favorite command %q\n", args[0])
		fmt.Fprintln(os.Stderr, "usage: ishakat model favorite <add|remove> <ref>")
		return 2
	}
}

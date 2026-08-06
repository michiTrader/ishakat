package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/x/term"

	"github.com/MichiTrader/ishakat/internal/app"
	"github.com/MichiTrader/ishakat/internal/config"
)

// cmdPurge implements P3's `ishakat purge` / `ishakat purge --sessions`:
// see internal/app/purge.go's own doc comment for why this needs to be a
// dedicated command rather than "just rm -rf it yourself" — the four
// separate XDG base directories this program writes to are not obvious
// from the outside, and none of them are touched by reinstalling the
// binary.
//
// A TTY gets an interactive [y/N] confirmation before anything is deleted
// (this permanently removes config, credentials and every saved
// conversation); --force/-f skips it for scripts and CI, where there is
// nobody to answer a prompt that would otherwise hang forever.
func cmdPurge(args []string) int {
	fs := flag.NewFlagSet("purge", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	sessionsOnly := fs.Bool("sessions", false, "delete only session transcripts, leaving config/credentials/cache untouched")
	force := fs.Bool("force", false, "skip the interactive confirmation")
	forceShort := fs.Bool("f", false, "same as --force")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	force2 := *force || *forceShort

	// Loading config.toml is what lets PurgeTargets honour a customized
	// [session] dir (see its own doc comment) instead of always assuming
	// the XDG default. A config that fails to load is not fatal here —
	// purge's whole purpose covers exactly the case where config.toml is
	// broken beyond repair and the user just wants a clean slate — so cfg
	// stays nil and PurgeTargets falls back to xdg.SessionsDir().
	cfg, _ := config.Load(config.Options{SkipProject: true})

	targets := app.PurgeTargets(cfg, *sessionsOnly)

	fmt.Println(purgeDescription(*sessionsOnly))
	for _, t := range targets {
		fmt.Printf("  %s\n", t)
	}

	if !force2 {
		if !term.IsTerminal(os.Stdin.Fd()) {
			fmt.Fprintln(os.Stderr, "no terminal attached to confirm; re-run with --force to proceed without asking")
			return 2
		}
		fmt.Print("This permanently deletes the paths above. Continue? [y/N] ")
		yes, err := readPurgeConfirm(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "could not read your answer, nothing was deleted: %v\n", err)
			return 1
		}
		if !yes {
			fmt.Println("Nothing was deleted.")
			return 0
		}
	}

	res, err := app.Purge(targets)
	for _, d := range res.Removed {
		fmt.Printf("Removed %s\n", d)
	}
	for _, d := range res.Missing {
		fmt.Printf("Nothing to remove: %s did not exist.\n", d)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ purge stopped early: %v\n", err)
		return 1
	}
	if *sessionsOnly {
		fmt.Println("Session history cleared. Configuration and credentials were left untouched.")
	} else {
		fmt.Println("Everything ishakat had stored on disk is gone. `ishakat provider add` starts fresh.")
	}
	return 0
}

func purgeDescription(sessionsOnly bool) string {
	if sessionsOnly {
		return "This will delete ishakat's saved session transcripts:"
	}
	return "This will delete every file ishakat has stored on disk — configuration, " +
		"credentials, the model catalog cache and all session transcripts:"
}

// readPurgeConfirm mirrors readYesNo's own convention (provider.go) but
// defaults to NO on a bare Enter — "[y/N]", not "[Y/n]" — since purge is
// destructive and irreversible where offerDefaultModel's own prompt is not.
func readPurgeConfirm(r io.Reader) (bool, error) {
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes", nil
}

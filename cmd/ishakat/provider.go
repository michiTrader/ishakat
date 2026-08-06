package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/term"

	"github.com/MichiTrader/ishakat/internal/app"
	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/xdg"
)

func cmdProvider(args []string) int {
	if len(args) == 0 {
		printProviderUsage(os.Stderr)
		return 2
	}
	switch args[0] {
	case "add", "set", "configure":
		return cmdProviderAdd(args[1:])
	case "list":
		return cmdProviderList()
	case "remove", "delete":
		return cmdProviderRemove(args[1:])
	case "help", "--help", "-h":
		printProviderUsage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown provider command %q\n", args[0])
		printProviderUsage(os.Stderr)
		return 2
	}
}

func printProviderUsage(w io.Writer) {
	fmt.Fprintln(w, `ishakat provider — manage provider credentials

USAGE
  ishakat provider add                       (interactive: pick a provider from a short list)
  ishakat provider add <provider> [--api-key-unsafe <key>]
  ishakat provider add <provider> --api-key-stdin
  ishakat provider add <provider> --force   (overwrite a customized base_url)
  ishakat provider add <provider> --no-verify  (skip the live authentication check)
  ishakat provider list
  ishakat provider remove <provider>

PROVIDERS
  omniroute, openai, anthropic, nvidia, gemini

Before writing anything, `+"`add`"+` makes one real authenticated request to the
service (a one-token chat completion) to confirm the key actually works.
GET /models is not used for this: some services (NVIDIA) answer it without
any credential at all, which would report success for a bad key.

The key is stored in a separate, owner-only credentials file
(~/.config/ishakat/credentials.toml). Connection details (base_url, kind,
discover) go in config.toml instead, so a key rotation never silently
reverts a base_url you customized. Adding a provider also enables it once
verified; no manual config.toml edit is required.`)
}

func cmdProviderAdd(args []string) int {
	if len(args) == 0 {
		// No provider named: on a script/CI invocation (no TTY) there is
		// nobody to ask, so this stays the same usage error it always
		// was. With a TTY attached, `ishakat provider add` alone should
		// complete the "download and just add my key" flow the audit
		// asked for, instead of forcing a second invocation with a
		// memorized preset id — offer the short list interactively.
		if !term.IsTerminal(os.Stdin.Fd()) {
			fmt.Fprintln(os.Stderr, "usage: ishakat provider add <provider> [flags]")
			return 2
		}
		name, ok := pickProviderInteractively(os.Stdin, os.Stderr)
		if !ok {
			return 2
		}
		args = []string{name}
	}
	providerName := args[0]
	fs := flag.NewFlagSet("provider add", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	// --api-key (plain) was renamed to --api-key-unsafe: a key passed on the
	// command line lands in shell history and, on Linux, in
	// /proc/<pid>/cmdline, readable by any other user on the same machine.
	// The interactive prompt and --api-key-stdin do not have this problem
	// and are what the usage text leads with; this flag stays only for
	// scripts that already accept the risk, and the name says so up front.
	apiKey := fs.String("api-key-unsafe", "", "API key on the command line (visible in shell history and ps); prefer the interactive prompt or --api-key-stdin")
	fromStdin := fs.Bool("api-key-stdin", false, "read the API key from stdin without echoing it")
	force := fs.Bool("force", false, "overwrite a base_url this provider id already has in config.toml")
	noVerify := fs.Bool("no-verify", false, "skip the live authentication probe (not recommended)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if *apiKey != "" && *fromStdin {
		fmt.Fprintln(os.Stderr, "use only one of --api-key-unsafe and --api-key-stdin")
		return 2
	}

	preset, err := config.ResolveProviderPreset(providerName)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	key := strings.TrimSpace(*apiKey)
	switch {
	case *fromStdin:
		key, err = readAPIKeyStdin(os.Stdin)
	case key == "" && term.IsTerminal(os.Stdin.Fd()):
		key, err = readAPIKeyPrompt(preset.Name)
	case key == "":
		err = errors.New("no API key supplied; use --api-key-stdin or --api-key-unsafe")
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "provider setup failed: %v\n", err)
		return 1
	}

	if *noVerify {
		fmt.Fprintln(os.Stderr, "warning: skipping the live authentication check (--no-verify); "+
			"the provider will be marked enabled even if the key is invalid")
	} else {
		fmt.Fprintf(os.Stderr, "Verifying %s credentials…\n", preset.Name)
		if err := verifyCredential(preset, key); err != nil {
			// Nothing is written on failure — the whole point of verifying
			// first. This is the fix for the audit's central finding: a
			// command whose only purpose is configuring a provider must
			// not declare success (or, worse, silently accept a key for a
			// service like NVIDIA that answers GET /models unauthenticated)
			// without ever having checked it against the real API.
			fmt.Fprintf(os.Stderr, "provider setup failed: %v\n", err)
			fmt.Fprintln(os.Stderr, "Nothing was written. Re-run with --no-verify to save anyway.")
			return 1
		}
		fmt.Fprintln(os.Stderr, "✓ Key verified.")
	}

	overwrote, err := config.SaveProviderConnection(preset, *force)
	if err != nil {
		fmt.Fprintf(os.Stderr, "provider setup failed: %v\n", err)
		return 1
	}
	if !overwrote {
		fmt.Fprintf(os.Stderr, "note: %s already has a different base_url in config.toml; "+
			"left it untouched. Re-run with --force to overwrite it with the preset default.\n", preset.ID)
	}

	if err := config.SaveCredential(preset.ID, key); err != nil {
		fmt.Fprintf(os.Stderr, "provider setup failed: %v\n", err)
		return 1
	}

	fmt.Printf("Configured %s (%s).\n", preset.Name, preset.ID)
	if runtime.GOOS == "windows" {
		// os.Chmod on Windows only toggles the read-only attribute; there
		// is no POSIX owner-only mode. Claiming "0600" here would be a
		// false statement about protection the file does not have — the
		// key inherits whatever ACL the parent directory (normally the
		// user's own profile) already had.
		fmt.Printf("Credentials stored in %s.\n", xdg.CredentialsFile())
		fmt.Println("Note: Windows has no POSIX file-permission equivalent to the 0600 " +
			"this build uses on Linux/macOS; the file relies on your user profile's " +
			"own access control instead.")
	} else {
		fmt.Printf("Credentials stored in %s with mode 0600.\n", xdg.CredentialsFile())
	}
	if preset.Notes != "" {
		fmt.Printf("Note: %s\n", preset.Notes)
	}
	fmt.Println("The provider is enabled. Run `ishakat models --refresh` to update its model list.")

	// Offer to set app.default_model to the model this run just proved
	// works, but only when the *current* default doesn't already resolve
	// to a usable provider — see app.NeedsDefaultModel's own comment for
	// why this predicate exists: leaving app.default_model pointed at a
	// provider with no credential (the stock "omniroute/auto/coding" on a
	// fresh install being the most common case) is the single most common
	// failure mode the audit that added this found, and the fix documented
	// there (SetDefaultModel) had never actually been wired to a caller.
	//
	// Skipped for --no-verify: that path has no proof the key is valid at
	// all, so nudging the user toward making it the default would be
	// promoting an unconfirmed credential instead of a confirmed one.
	if !*noVerify {
		offerDefaultModel(preset)
	} else {
		fmt.Printf("If you want this provider's models as your default, edit app.default_model in %s\n", xdg.ConfigFile())
	}
	return 0
}

// offerDefaultModel checks whether app.default_model currently resolves to
// a usable provider and, if not, offers to point it at the provider that
// was just verified and configured — using preset.VerifyModel, the exact
// model id this run already proved answers with this key, rather than
// guessing at one discovery hasn't found yet (`ishakat models --refresh`
// runs separately, after this command returns).
//
// With no TTY on stdin, the offer degrades to the same "edit it yourself"
// pointer `provider add` always printed, rather than blocking a
// script/CI run waiting on input that will never arrive.
func offerDefaultModel(preset config.ProviderPreset) {
	cfg, err := config.Load(config.Options{})
	if err != nil || !app.NeedsDefaultModel(cfg) {
		// Either the config failed to reload (nothing more this command
		// can safely act on) or the existing default already resolves —
		// in both cases, silently proceed rather than second-guess a
		// setup that isn't this provider's problem to fix.
		return
	}

	ref := preset.ID + "/" + preset.VerifyModel
	if !term.IsTerminal(os.Stdin.Fd()) {
		fmt.Printf("app.default_model in %s does not resolve to a usable provider yet; "+
			"set it to %q (or run `ishakat provider add` again with a terminal attached "+
			"to be asked interactively).\n", xdg.ConfigFile(), ref)
		return
	}

	fmt.Printf("Use %s as your default model? [Y/n] ", ref)
	yes, err := readYesNo(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "could not read your answer, leaving app.default_model untouched: %v\n", err)
		return
	}
	if !yes {
		fmt.Printf("Not changed. Edit app.default_model in %s whenever you're ready.\n", xdg.ConfigFile())
		return
	}
	if err := config.SetDefaultModel(ref); err != nil {
		fmt.Fprintf(os.Stderr, "could not set app.default_model: %v\n", err)
		return
	}
	fmt.Printf("app.default_model is now %s.\n", ref)
}

// readYesNo reads one line and interprets it the way a "[Y/n]" prompt
// promises: empty input (a bare Enter) means yes, since Y is the
// capitalized (default) option; anything starting with 'n' or 'N' means
// no; everything else is treated as an affirmative answer rather than
// silently doing nothing, since the prompt already told the user what
// pressing Enter does.
func readYesNo(r io.Reader) (bool, error) {
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	line = strings.ToLower(strings.TrimSpace(line))
	if line == "" {
		return true, nil
	}
	return line[0] != 'n', nil
}

// pickProviderInteractively prints the short list of provider presets and
// reads a single-line choice: either the list's 1-based number or the
// preset's id/name typed directly (so someone who already knows they want
// "gemini" doesn't have to count list entries). Returns false if the input
// couldn't be read, was empty, or didn't match any preset — the caller
// falls back to the usual usage error in all of those cases rather than
// guessing.
func pickProviderInteractively(r io.Reader, w io.Writer) (string, bool) {
	presets := config.ProviderPresets()
	fmt.Fprintln(w, "Which provider?")
	for i, p := range presets {
		fmt.Fprintf(w, "  %d. %s (%s)\n", i+1, p.Name, p.ID)
	}
	fmt.Fprint(w, "Enter a number or name: ")

	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		fmt.Fprintf(w, "could not read your choice: %v\n", err)
		return "", false
	}
	choice := strings.TrimSpace(line)
	if choice == "" {
		return "", false
	}

	if n, convErr := strconv.Atoi(choice); convErr == nil {
		if n < 1 || n > len(presets) {
			fmt.Fprintf(w, "%d is not one of the options above.\n", n)
			return "", false
		}
		return presets[n-1].ID, true
	}

	if _, err := config.ResolveProviderPreset(choice); err != nil {
		fmt.Fprintln(w, err)
		return "", false
	}
	return choice, true
}

func readAPIKeyPrompt(provider string) (string, error) {
	fmt.Fprintf(os.Stderr, "%s API key (input hidden): ", provider)
	key, err := term.ReadPassword(os.Stdin.Fd())
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("read API key: %w", err)
	}
	if value := strings.TrimSpace(string(key)); value != "" {
		return value, nil
	}
	return "", errors.New("API key cannot be empty")
}

func readAPIKeyStdin(r io.Reader) (string, error) {
	value, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read API key: %w", err)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("API key cannot be empty")
	}
	return value, nil
}

func cmdProviderList() int {
	cfg, err := config.Load(config.Options{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot load configuration: %v\n", err)
		return 1
	}
	fmt.Println("Configured providers:")
	for _, p := range cfg.Providers {
		status := "disabled"
		if p.Enabled {
			status = "enabled"
		}
		credential := "missing credential"
		if p.AuthOK {
			credential = "credential available"
		}
		fmt.Printf("  %-16s %-8s %s\n", p.ID, status, credential)
	}
	return 0
}

func cmdProviderRemove(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: ishakat provider remove <provider>")
		return 2
	}
	preset, err := config.ResolveProviderPreset(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if err := config.RemoveCredential(preset.ID); err != nil {
		fmt.Fprintf(os.Stderr, "provider removal failed: %v\n", err)
		return 1
	}
	fmt.Printf("Removed stored credentials for %s.\n", preset.Name)
	return 0
}

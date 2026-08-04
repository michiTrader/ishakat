package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

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
  ishakat provider add <provider> [--api-key <key>]
  ishakat provider add <provider> --api-key-stdin
  ishakat provider list
  ishakat provider remove <provider>

PROVIDERS
  omniroute, openai, anthropic, nvidia, gemini

The key is stored in a separate owner-only credentials file. Adding a provider
also enables it; no manual config.toml edit is required.`)
}

func cmdProviderAdd(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: ishakat provider add <provider> [flags]")
		return 2
	}
	providerName := args[0]
	fs := flag.NewFlagSet("provider add", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiKey := fs.String("api-key", "", "API key (prefer an interactive prompt or --api-key-stdin)")
	fromStdin := fs.Bool("api-key-stdin", false, "read the API key from stdin without echoing it")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if *apiKey != "" && *fromStdin {
		fmt.Fprintln(os.Stderr, "use only one of --api-key and --api-key-stdin")
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
		err = errors.New("no API key supplied; use --api-key-stdin or --api-key")
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "provider setup failed: %v\n", err)
		return 1
	}
	if err := config.SaveCredential(preset.ID, key); err != nil {
		fmt.Fprintf(os.Stderr, "provider setup failed: %v\n", err)
		return 1
	}
	fmt.Printf("Configured %s (%s).\n", preset.Name, preset.ID)
	fmt.Printf("Credentials stored in %s with mode 0600.\n", xdg.CredentialsFile())
	fmt.Println("The provider is enabled automatically. Run `ishakat models --refresh` to update its model list.")
	return 0
}

func readAPIKeyPrompt(provider string) (string, error) {
	fmt.Fprintf(os.Stderr, "%s API key (input hidden): ", provider)
	key, err := term.ReadPassword(int(os.Stdin.Fd()))
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

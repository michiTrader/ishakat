// login.go is `ishakat login` (docs/PLAN.md §11 Step 24's second half): a
// thin CLI wrapper over internal/oauth's RFC 8628 device-flow client,
// reusing `provider add`'s own verify-then-write pipeline (verify.go,
// internal/config/credentials.go, internal/config/connection.go) so a
// token obtained through a browser lands in exactly the same
// credentials.toml/config.toml shape a pasted API key does — the wire
// dialect (internal/provider/openai) sends "Authorization: Bearer
// <api_key>" either way, so nothing downstream needs to know which path a
// given provider's credential took.
//
// Why none of the five presets in config.ProviderPresets() actually uses
// this: a real, ToS-clean OAuth device flow in front of a chat-completion
// API is rarer than it sounds. OmniRoute/OpenAI/Anthropic/NVIDIA/Gemini all
// issue long-lived API keys, not device-flow tokens — there is no
// "device_code" endpoint to poll for any of them. GitHub Copilot does have
// a widely-documented device flow (github.com/login/device/code), but the
// only working path from that token to a chat completion goes through
// api.github.com/copilot_internal/v2/token and api.githubcopilot.com — both
// undocumented, unversioned endpoints reverse-engineered from the VS Code
// extension, using a hardcoded client_id (Iv1.b507a08c87ecfe98) that is not
// registered to ishakat and that GitHub's own Copilot Terms of Service do
// not authorize third-party tools to call this way. Baking that into a
// shipped binary is a real legal/ToS liability, not a shortcut worth
// taking to make this step's demo prettier — see the Bitácora entry for
// the research trail.
//
// So `ishakat login` is provider-agnostic instead: it drives RFC 8628
// against whatever device_code_url/token_url a *preset* declares (via
// config.ProviderPreset.SupportsDeviceFlow — none do today, on purpose) or
// whatever a caller names directly with --client-id/--device-code-url/
// --token-url, for a self-hosted or future gateway that documents its own
// legitimate device flow. The mechanism is real, tested end-to-end against
// httptest servers (internal/oauth/device_test.go), and ready the day a
// provider preset can honestly set those four fields; it is not wired to
// a service that would put a user's GitHub account or ishakat itself at
// risk to demonstrate.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/oauth"
	"github.com/MichiTrader/ishakat/internal/xdg"
)

// loginPollTimeout bounds the whole device-flow wait beyond whatever the
// server's own expires_in says, as a last-resort ceiling if a provider's
// device code response omits expires_in and RequestDeviceCode's own
// 900-second default is still too generous for a CLI invocation nobody is
// scripting to wait fifteen minutes unattended for.
const loginPollTimeout = 15 * time.Minute

func cmdLogin(args []string) int {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	clientID := fs.String("client-id", "", "OAuth client_id for a custom device flow (overrides the preset's own, if any)")
	scope := fs.String("scope", "", "OAuth scope for a custom device flow")
	deviceCodeURL := fs.String("device-code-url", "", "device authorization endpoint for a custom device flow")
	tokenURL := fs.String("token-url", "", "token endpoint for a custom device flow")
	forceFlag := fs.Bool("force", false, "overwrite a base_url this provider id already has in config.toml")
	noVerify := fs.Bool("no-verify", false, "skip the live authentication probe after obtaining the token (not recommended)")
	fs.Usage = func() {
		printLoginUsage(os.Stderr)
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) == 0 {
		printLoginUsage(os.Stderr)
		return 2
	}
	providerName := rest[0]

	preset, err := config.ResolveProviderPreset(providerName)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	oauthCfg := oauth.Config{
		ClientID:      firstNonEmpty(*clientID, preset.OAuthClientID),
		Scope:         firstNonEmpty(*scope, preset.OAuthScope),
		DeviceCodeURL: firstNonEmpty(*deviceCodeURL, preset.OAuthDeviceCodeURL),
		TokenURL:      firstNonEmpty(*tokenURL, preset.OAuthTokenURL),
	}
	if oauthCfg.DeviceCodeURL == "" || oauthCfg.TokenURL == "" {
		fmt.Fprintf(os.Stderr, "%s has no OAuth device flow configured.\n", preset.Name)
		fmt.Fprintln(os.Stderr, "Use `ishakat provider add "+preset.ID+"` for the API-key wizard instead, "+
			"or pass --device-code-url and --token-url to drive a device flow this build does not know "+
			"about by default (see login.go's own package comment for why none of the built-in "+
			"presets enables this today).")
		return 2
	}

	return runLogin(os.Stdout, os.Stderr, preset, oauthCfg, *forceFlag, *noVerify)
}

// runLogin is cmdLogin's own body, split out so tests can point it at a
// fake oauth.Config (an httptest server, exactly like
// internal/oauth/device_test.go's own discipline) without going through
// os.Args/flag parsing.
func runLogin(stdout, stderr io.Writer, preset config.ProviderPreset, oauthCfg oauth.Config, force, noVerify bool) int {
	fmt.Fprintf(stderr, "Requesting a device code from %s…\n", preset.Name)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dc, err := oauth.RequestDeviceCode(ctx, oauthCfg)
	if err != nil {
		fmt.Fprintf(stderr, "login failed: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "\nOpen %s in a browser and enter this code:\n\n    %s\n\n", dc.VerificationURI, dc.UserCode)
	fmt.Fprintf(stderr, "Waiting for authorization (code expires in %s)…\n", oauth.FormatWait(dc))

	pollCtx, cancel := context.WithTimeout(ctx, loginPollTimeout)
	defer cancel()

	tok, err := oauth.PollForToken(pollCtx, oauthCfg, dc)
	if err != nil {
		switch {
		case errors.Is(err, oauth.ErrAccessDenied):
			fmt.Fprintln(stderr, "login failed: the authorization request was denied.")
		case errors.Is(err, oauth.ErrExpired):
			fmt.Fprintln(stderr, "login failed: the device code expired before authorization completed. Try again.")
		case errors.Is(err, context.Canceled):
			fmt.Fprintln(stderr, "login cancelled.")
		default:
			fmt.Fprintf(stderr, "login failed: %v\n", err)
		}
		return 1
	}
	fmt.Fprintln(stderr, "✓ Authorized.")

	if !noVerify {
		fmt.Fprintf(stderr, "Verifying %s credentials…\n", preset.Name)
		if err := verifyCredential(preset, tok.AccessToken); err != nil {
			fmt.Fprintf(stderr, "login failed: %v\n", err)
			fmt.Fprintln(stderr, "Nothing was written. Re-run with --no-verify to save anyway.")
			return 1
		}
		fmt.Fprintln(stderr, "✓ Token verified.")
	} else {
		fmt.Fprintln(stderr, "warning: skipping the live authentication check (--no-verify); "+
			"the provider will be marked enabled even if the token is invalid")
	}

	overwrote, err := config.SaveProviderConnection(preset, force)
	if err != nil {
		fmt.Fprintf(stderr, "login failed: %v\n", err)
		return 1
	}
	if !overwrote {
		fmt.Fprintf(stderr, "note: %s already has a different base_url in config.toml; "+
			"left it untouched. Re-run with --force to overwrite it with the preset default.\n", preset.ID)
	}

	if err := config.SaveCredential(preset.ID, tok.AccessToken); err != nil {
		fmt.Fprintf(stderr, "login failed: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "Configured %s (%s) via OAuth device flow.\n", preset.Name, preset.ID)
	fmt.Fprintf(stdout, "Token stored in %s.\n", xdg.CredentialsFile())
	if preset.Notes != "" {
		fmt.Fprintf(stdout, "Note: %s\n", preset.Notes)
	}
	fmt.Fprintln(stdout, "The provider is enabled. Run `ishakat models --refresh` to update its model list.")
	return 0
}

func printLoginUsage(w io.Writer) {
	fmt.Fprintln(w, `ishakat login — authenticate a provider via OAuth device flow (RFC 8628)

USAGE
  ishakat login <provider>
  ishakat login <provider> --client-id <id> --device-code-url <url> --token-url <url> [--scope <scope>]
  ishakat login <provider> --force       (overwrite a customized base_url)
  ishakat login <provider> --no-verify   (skip the live authentication check)

None of the built-in provider presets (omniroute, openai, anthropic, nvidia,
gemini) ships an OAuth device flow — they all issue long-lived API keys, for
which `+"`ishakat provider add`"+` is the right command. This command exists for a
self-hosted or third-party gateway that documents its own legitimate RFC
8628 device authorization endpoints; pass them with --client-id/
--device-code-url/--token-url.

The obtained token is stored exactly like a pasted API key: in the
owner-only credentials file (~/.config/ishakat/credentials.toml), verified
with a real one-token chat completion before anything is written, unless
--no-verify is given.`)
}

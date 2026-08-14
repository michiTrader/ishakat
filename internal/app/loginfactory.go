// loginfactory.go is internal/app's half of Step 24's in-session /login
// wizard (§13): the real, HTTP-driving tui.LoginFactory implementation
// tui.Root calls through the seam internal/tui/loginfactory.go's own doc
// comment names — internal/tui cannot import net/http
// (internal/arch_test.go's TestTUINoImportaHTTP), so the actual
// RequestDeviceCode/PollForToken calls against internal/oauth have to live
// here, on the far side of that boundary, exactly like NewEngineFactory
// (engine.go) already does for switching models.
//
// NewLoginFactory deliberately reuses the exact same
// request→display→poll→verify→save sequence cmd/ishakat/login.go's
// runLogin already established for the CLI door: RequestDeviceCode once
// (before the wizard has anything to show), then a LoginWaiter whose Wait
// method polls (bounded by loginPollTimeout, the same 15-minute ceiling
// runLogin's own constant documents), verifies the token with a real
// one-token chat completion (verifyLoginCredential, below — a deliberate,
// small duplication of cmd/ishakat/verify.go's verifyCredential, not a
// shared helper: that function lives in package main, which internal/app
// cannot import at all, and every other "door" in this codebase already
// reimplements what it needs rather than forcing a premature shared
// abstraction — see docs/PLAN.md's own notes on the three front doors
// sharing the engine but not every helper), and finally writes the
// connection/credential the same two calls runLogin makes, in the same
// order.
//
// Why every one of today's five built-in presets hits the "no OAuth
// device flow configured" branch on the very first call: none of them
// (omniroute, openai, anthropic, nvidia, gemini) sets OAuthDeviceCodeURL/
// OAuthTokenURL — see cmd/ishakat/login.go's own package comment for why.
// This factory is still real, tested infrastructure, ready the day a
// preset (or a self-hosted gateway config) honestly sets those fields;
// it is simply unreachable today for the same reason the CLI's own
// `ishakat login` is.
package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/convo"
	"github.com/MichiTrader/ishakat/internal/oauth"
	"github.com/MichiTrader/ishakat/internal/provider"

	// The openai dialect is what every preset in credentials.go declares
	// as its kind (including Anthropic and Gemini, both reached through
	// their respective OpenAI-compatible shims). internal/app already
	// imports this transitively via wiring.go's own blank import for
	// NewProvider's sake, but importing it again here (harmless, Go
	// dedupes) keeps this file legible on its own, the same reasoning
	// verify.go's own copy of this import states.
	_ "github.com/MichiTrader/ishakat/internal/provider/openai"

	// Anthropic's own native Messages API adapter (Fase 4), for the same
	// reason: a hand-written kind = "anthropic" connection must be able to
	// run this same login wizard, not just `provider add`/the real binary.
	_ "github.com/MichiTrader/ishakat/internal/provider/anthropic"
	_ "github.com/MichiTrader/ishakat/internal/provider/gemini"

	"github.com/MichiTrader/ishakat/internal/tui"
)

// loginPollTimeout mirrors cmd/ishakat/login.go's own constant of the same
// name: a last-resort ceiling on the whole device-flow wait, in case a
// provider's device code response omits expires_in and
// oauth.RequestDeviceCode's own 900-second default is still too generous
// for an in-session wizard nobody is scripting to wait fifteen minutes
// unattended for. Named separately (not exported from cmd/ishakat, which
// this package cannot import anyway) so the two doors can tune this
// independently if they ever need to; today they intentionally agree.
const loginPollTimeout = 15 * time.Minute

// verifyLoginTimeout mirrors cmd/ishakat/verify.go's own verifyTimeout for
// the same reason loginPollTimeout mirrors its own constant.
const verifyLoginTimeout = 20 * time.Second

// NewLoginFactory returns a tui.LoginFactory closed over cfg: tui.Root
// calls it once per /login attempt (startLogin, internal/tui/login.go),
// handing back only the resolved preset's ID string — never the full
// config.ProviderPreset — which is why the closure below re-resolves the
// preset itself via config.ResolveProviderPreset before it can look at any
// of its OAuth* fields.
//
// cfg is accepted (rather than nothing) purely for parity with
// NewEngineFactory's own shape and because a future preset override living
// in cfg (the way [providers] entries already can override BaseURL) is a
// plausible next step for a self-hosted gateway's device flow — nothing
// in cfg is actually consulted today, since config.ResolveProviderPreset
// itself does not take a *config.Config either.
func NewLoginFactory(cfg *config.Config) tui.LoginFactory {
	_ = cfg // reserved for a future per-cfg OAuth override; see doc comment above.

	return func(ctx context.Context, providerID string) (tui.LoginDeviceCode, tui.LoginWaiter, error) {
		preset, err := config.ResolveProviderPreset(providerID)
		if err != nil {
			return tui.LoginDeviceCode{}, nil, err
		}
		return beginLoginAttempt(ctx, preset)
	}
}

// beginLoginAttempt is NewLoginFactory's closure body, split out so tests
// can drive it directly with a fake config.ProviderPreset (OAuth URLs and
// BaseURL pointed at httptest servers) without needing a real preset in
// config.ResolveProviderPreset's own fixed registry to declare a device
// flow — exactly why cmd/ishakat/login.go's own runLogin is split out from
// cmdLogin's flag/preset resolution.
func beginLoginAttempt(ctx context.Context, preset config.ProviderPreset) (tui.LoginDeviceCode, tui.LoginWaiter, error) {
	if !preset.SupportsDeviceFlow() {
		return tui.LoginDeviceCode{}, nil, fmt.Errorf(
			"%s has no OAuth device flow configured; use `ishakat provider add %s` "+
				"or the API-key wizard instead", preset.Name, preset.ID)
	}

	oauthCfg := oauth.Config{
		ClientID:      preset.OAuthClientID,
		Scope:         preset.OAuthScope,
		DeviceCodeURL: preset.OAuthDeviceCodeURL,
		TokenURL:      preset.OAuthTokenURL,
	}

	dc, err := oauth.RequestDeviceCode(ctx, oauthCfg)
	if err != nil {
		return tui.LoginDeviceCode{}, nil, err
	}

	code := tui.LoginDeviceCode{
		UserCode:        dc.UserCode,
		VerificationURI: dc.VerificationURI,
	}
	waiter := &loginWaiter{preset: preset, oauthCfg: oauthCfg, dc: dc}
	return code, waiter, nil
}

// loginWaiter is NewLoginFactory's concrete tui.LoginWaiter: it holds
// exactly what Wait needs to finish the flow a later, independent call
// (Bubble Tea's own waitLoginCmd, run as a separate tea.Cmd from the one
// that produced this value) started.
type loginWaiter struct {
	preset   config.ProviderPreset
	oauthCfg oauth.Config
	dc       oauth.DeviceCodeResponse
}

// Wait implements tui.LoginWaiter. It reproduces runLogin's own
// poll→verify→save sequence (cmd/ishakat/login.go) exactly: a bounded
// PollForToken, a mandatory verify step (the TUI wizard exposes no
// --no-verify equivalent, so a token that cannot authenticate is never
// saved), then SaveProviderConnection followed by SaveCredential, in that
// order — the same order a pasted API key already lands in via `provider
// add`, so nothing downstream needs to know which path a given provider's
// credential took.
func (w *loginWaiter) Wait(ctx context.Context) (string, error) {
	pollCtx, cancel := context.WithTimeout(ctx, loginPollTimeout)
	defer cancel()

	tok, err := oauth.PollForToken(pollCtx, w.oauthCfg, w.dc)
	if err != nil {
		switch {
		case errors.Is(err, oauth.ErrAccessDenied):
			return "", errors.New("the authorization request was denied")
		case errors.Is(err, oauth.ErrExpired):
			return "", errors.New("the device code expired before authorization completed; try /login again")
		case errors.Is(err, context.Canceled):
			return "", context.Canceled
		default:
			return "", err
		}
	}

	if err := verifyLoginCredential(w.preset, tok.AccessToken); err != nil {
		return "", fmt.Errorf("%w; nothing was written", err)
	}

	if _, err := config.SaveProviderConnection(w.preset, false); err != nil {
		return "", err
	}
	if err := config.SaveCredential(w.preset.ID, tok.AccessToken); err != nil {
		return "", err
	}

	return fmt.Sprintf("Configured %s (%s) via OAuth device flow.", w.preset.Name, w.preset.ID), nil
}

// verifyLoginCredential is a deliberate, small duplicate of
// cmd/ishakat/verify.go's verifyCredential — see this file's own package
// comment for why duplicating roughly thirty lines here beats either
// reaching into package main (impossible: Go forbids importing a main
// package at all) or a larger refactor moving verifyCredential out of
// cmd/ishakat for a change whose only beneficiary today is this wizard.
//
// Like verifyCredential, this performs a real authenticated request — a
// one-token chat completion, never GET /models — before Wait writes
// anything to disk. See verifyCredential's own comment for why GET
// /models cannot serve as the check (NVIDIA answers 200 for any key;
// Gemini's OpenAI shim answers 404 for a missing model, not 401 for a
// missing key).
func verifyLoginCredential(preset config.ProviderPreset, apiKey string) error {
	p, err := provider.New(provider.Settings{
		ID:      preset.ID,
		Kind:    preset.Kind,
		BaseURL: preset.BaseURL,
		APIKey:  apiKey,
		Timeout: verifyLoginTimeout,
	})
	if err != nil {
		return fmt.Errorf("build provider adapter: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), verifyLoginTimeout)
	defer cancel()

	maxTokens := 1
	events, err := p.Stream(ctx, provider.Request{
		Model:     preset.VerifyModel,
		Messages:  []convo.Message{convo.User("hi")},
		Stream:    false,
		MaxTokens: &maxTokens,
	})
	if err != nil {
		return interpretLoginVerifyError(preset, err)
	}
	for ev := range events {
		if ev.Kind == provider.EventError && ev.Err != nil {
			return interpretLoginVerifyError(preset, ev.Err)
		}
	}
	return nil
}

// interpretLoginVerifyError mirrors cmd/ishakat/verify.go's own
// interpretVerifyError — same status-code reasoning, same reason it is a
// separate small copy rather than a cross-package call.
func interpretLoginVerifyError(preset config.ProviderPreset, err error) error {
	var perr *provider.Error
	if errors.As(err, &perr) && perr != nil {
		switch perr.Status {
		case 401, 403:
			return fmt.Errorf("the token was rejected (HTTP %d): %s", perr.Status, shortLoginMsg(perr.Message))
		case 404:
			return fmt.Errorf("HTTP 404 from %s: either the token is invalid or "+
				"the model id %q this build probes with is no longer served. "+
				"Raw message: %s", preset.Name, preset.VerifyModel, shortLoginMsg(perr.Message))
		case 429:
			return fmt.Errorf("the service rate-limited the verification request (HTTP 429); "+
				"the token may still be valid. Raw message: %s", shortLoginMsg(perr.Message))
		default:
			return fmt.Errorf("HTTP %d from %s: %s", perr.Status, preset.Name, shortLoginMsg(perr.Message))
		}
	}
	return fmt.Errorf("could not reach %s to verify the token: %w", preset.Name, err)
}

func shortLoginMsg(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "(no message body)"
	}
	const max = 200
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

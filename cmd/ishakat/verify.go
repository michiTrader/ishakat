package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/convo"
	"github.com/MichiTrader/ishakat/internal/provider"

	// The openai dialect is what every preset in credentials.go declares as
	// its kind (including Anthropic and Gemini, both reached through their
	// respective OpenAI-compatible shims — see the presets' Notes fields).
	// Importing it here, rather than through internal/app, keeps `provider
	// add` from depending on the whole application wiring just to place one
	// HTTP call.
	_ "github.com/MichiTrader/ishakat/internal/provider/openai"

	// Anthropic's own native Messages API adapter (Fase 4): needed so
	// `provider add` can verify a hand-written kind = "anthropic"
	// connection too, not just the openai-shim default the built-in preset
	// still uses.
	_ "github.com/MichiTrader/ishakat/internal/provider/anthropic"
)

// verifyTimeout bounds the one probe request `provider add` makes. Long
// enough for a cold TLS handshake to a first-party API, short enough that a
// stalled or firewalled endpoint doesn't leave the command hanging.
const verifyTimeout = 20 * time.Second

// verifyCredential performs a real authenticated request against the
// service — a one-token chat completion, never GET /models — before
// `provider add` writes anything to disk.
//
// GET /models cannot serve as the authentication check: NVIDIA's endpoint
// answers 200 with its full model catalog for any request, credentialed or
// not (empirically confirmed against the live API), and Gemini's OpenAI
// shim answers 404 for a missing model rather than 401 for a missing key.
// A verification step that can be satisfied by an invalid key is worse than
// no verification step, because it reports success while lying about it —
// exactly the false-positive this function exists to close.
//
// A chat completion is not free of the same trap in the other direction: an
// invalid *model* id can produce the same 4xx family as an invalid *key* on
// some gateways, which is why every preset carries a VerifyModel that is
// checked to actually exist on that service at the time these presets were
// written, not "whatever the user asked for".
func verifyCredential(preset config.ProviderPreset, apiKey string) error {
	p, err := provider.New(provider.Settings{
		ID:      preset.ID,
		Kind:    preset.Kind,
		BaseURL: preset.BaseURL,
		APIKey:  apiKey,
		Timeout: verifyTimeout,
	})
	if err != nil {
		return fmt.Errorf("build provider adapter: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), verifyTimeout)
	defer cancel()

	maxTokens := 1
	events, err := p.Stream(ctx, provider.Request{
		Model:     preset.VerifyModel,
		Messages:  []convo.Message{convo.User("hi")},
		Stream:    false,
		MaxTokens: &maxTokens,
	})
	if err != nil {
		return interpretVerifyError(preset, err)
	}
	// The handshake already succeeded by the time Stream returns without
	// error (see stream.go: the HTTP status is checked before the channel
	// is handed back). Draining is only to free the connection cleanly;
	// a mid-stream error at this point would be surprising but is still
	// reported, since a key that fails immediately after authenticating
	// (e.g. a billing block) is not one `provider add` should call good.
	for ev := range events {
		if ev.Kind == provider.EventError && ev.Err != nil {
			return interpretVerifyError(preset, ev.Err)
		}
	}
	return nil
}

func interpretVerifyError(preset config.ProviderPreset, err error) error {
	var perr *provider.Error
	if errors.As(err, &perr) && perr != nil {
		switch perr.Status {
		case 401, 403:
			return fmt.Errorf("the key was rejected (HTTP %d): %s", perr.Status, shortMsg(perr.Message))
		case 404:
			// Gemini's OpenAI shim answers 404 for a model it doesn't
			// recognise, not for a missing key — and 404 without auth
			// versus 404 with a bad key both look the same on the wire.
			// Naming both possibilities beats a bare "not found", which
			// audits of this exact preset flagged as diagnostic noise
			// that points the user at the wrong problem.
			return fmt.Errorf("HTTP 404 from %s: either the key is invalid or "+
				"the model id %q this build probes with is no longer served. "+
				"Raw message: %s", preset.Name, preset.VerifyModel, shortMsg(perr.Message))
		case 429:
			return fmt.Errorf("the service rate-limited the verification request (HTTP 429); "+
				"the key may still be valid. Raw message: %s", shortMsg(perr.Message))
		default:
			return fmt.Errorf("HTTP %d from %s: %s", perr.Status, preset.Name, shortMsg(perr.Message))
		}
	}
	return fmt.Errorf("could not reach %s to verify the key: %w", preset.Name, err)
}

func shortMsg(s string) string {
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

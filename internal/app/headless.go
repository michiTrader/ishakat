// headless.go is PLAN.md Step 5: the full pipeline —configuration, provider,
// streaming, persistence— printing text to stdout without a single line of
// TUI.
//
// It's the most underrated step on the list for three reasons worth writing
// down: it exercises 60% of the system without needing a terminal, it's
// useful for scripting and pipelines, and when something breaks in the
// interface it lets you know immediately which side the bug is on. If
// `ishakat -p "hi"` works and the TUI doesn't, the problem is in the TUI; if
// it doesn't work here either, the problem is in the provider or the
// configuration.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/x/term"

	"github.com/MichiTrader/ishakat/internal/catalog"
	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/convo"
	"github.com/MichiTrader/ishakat/internal/permissions"
	"github.com/MichiTrader/ishakat/internal/provider"
	"github.com/MichiTrader/ishakat/internal/tools"
	"github.com/MichiTrader/ishakat/internal/xdg"
)

// Exit codes. 2 is a usage error (bad flags), 130 is the POSIX convention
// for "terminated by SIGINT", which is what a script needs to tell a Ctrl+C
// apart from a model failure.
const (
	ExitOK      = 0
	ExitError   = 1
	ExitUsage   = 2
	ExitAborted = 130
)

// HeadlessOptions are the parameters of headless mode. The Config, Stdin,
// Stdout, Stderr, StdinTTY, StdoutTTY fields below exist for tests: the
// whole pipeline runs against an httptest.Server without touching the
// user's disk or needing a terminal.
type HeadlessOptions struct {
	Version string

	Prompt string // -p / --prompt
	Model  string // -m / --model
	System string // --system (wins over app.system_prompt)

	JSON   bool  // --json: one event per line
	Stream *bool // --stream / --no-stream; nil = app.stream
	Save   *bool // --no-save; nil = session.save
	Quiet  bool  // --quiet: no warnings on stderr
	Yolo   bool  // --yolo: approve write and shell tools without prompting

	// AllowToolCreate is --allow-tool-create (§13, §19.7): the deliberate,
	// separate escape hatch that lets `tool_create` appear in headless
	// mode's registry even though headless has no reviewer channel to
	// resolve gate 2 against. --yolo does NOT imply this -- granting
	// self-evolution must be its own explicit flag a human typed knowingly
	// into a specific script, never a side effect of "stop asking me so
	// much". A `tool_create` call that reaches gate 2 with this flag set
	// still has no reviewer (permissions.New's third argument stays nil in
	// Headless, matching every existing headless call), so it fails with
	// permissions.ErrDenied at call time rather than ever silently
	// succeeding unattended -- the same "no human, no self-extension" rule
	// docs/PLAN.md §19.7 states for `serve`, just surfaced as a normal
	// tool-call error instead of the tool being entirely absent from the
	// catalogue. What this flag actually buys a script: the model can see
	// tool_create exists and *propose* it, and a human reviewing that
	// script's transcript afterward can see the proposal was made and
	// denied -- visibility, not unattended approval.
	AllowToolCreate bool

	// ConfigPath points at a different config.toml (--config).
	ConfigPath string

	// Test seams.
	Config     *config.Config
	Stdin      io.Reader
	Stdout     io.Writer
	Stderr     io.Writer
	StdinTTY   *bool
	StdoutTTY  *bool
	StderrTTY  *bool
	SessionDir string
}

// Headless runs one turn and returns the process exit code.
func Headless(opts HeadlessOptions) int {
	in := opts.Stdin
	if in == nil {
		in = os.Stdin
	}
	out := opts.Stdout
	if out == nil {
		out = os.Stdout
	}
	errw := opts.Stderr
	if errw == nil {
		errw = os.Stderr
	}

	stdinTTY := boolOr(opts.StdinTTY, func() bool { return term.IsTerminal(os.Stdin.Fd()) })
	stderrTTY := boolOr(opts.StderrTTY, func() bool { return term.IsTerminal(os.Stderr.Fd()) })

	// 1. Configuration.
	cfg := opts.Config
	if cfg == nil {
		path := opts.ConfigPath
		if path == "" {
			path = xdg.ConfigFile()
		}
		loaded, err := config.Load(config.Options{UserPath: path})
		if err != nil {
			fmt.Fprintf(errw, "✗ Configuration error: %v\n", err)
			return ExitError
		}
		cfg = loaded
	}

	// 2. Output. Decided before anything else so even configuration errors
	// come out in the requested format.
	var s sink
	if opts.JSON {
		s = newJSONSink(out)
	} else {
		reasoning := strings.EqualFold(cfg.UI.Reasoning, "full")
		s = &textSink{
			out:           out,
			err:           errw,
			showReasoning: reasoning,
			quiet:         opts.Quiet,
			// Color turns off if stderr isn't a terminal or if the config
			// disables it. stdout never carries color in headless mode.
			color: stderrTTY && !strings.EqualFold(cfg.UI.Color, "off"),
		}
	}

	// 3. Prompt: the flag's value plus whatever comes from stdin.
	prompt, err := buildPrompt(opts.Prompt, in, stdinTTY)
	if err != nil {
		s.fail(fmt.Errorf("could not read stdin: %w", err))
		return ExitError
	}
	if strings.TrimSpace(prompt) == "" {
		s.fail(errors.New(`nothing to ask: use -p "your question" or pipe it through stdin`))
		return ExitUsage
	}

	// 4. Model and provider. ResolveModelForBoot is P2: with opts.Model
	// empty (no explicit -m/--model), an app.default_model that fails to
	// resolve to a usable provider — or that was never set at all — is
	// routed to another configured, credentialed provider instead of
	// failing outright, and reported once via fb.Describe(). An explicit
	// -m/--model always goes through ResolveModel's ordinary path unchanged
	// (a typo must fail loudly, not land somewhere else silently).
	//
	// The catalog snapshot is loaded first because the resolver now consults
	// it: given a choice, a boot fallback should pick a model the provider
	// was last seen actually serving rather than this build's compiled-in
	// preset id. LoadCatalog reads local cache only (§4.4 keeps the network
	// off this path), so hoisting it above the resolution costs nothing.
	catalogSnapshot := LoadCatalog(cfg)
	ref, fb, err := ResolveModelForBoot(cfg, &catalogSnapshot.Catalog, opts.Model)
	if err != nil {
		s.fail(err)
		return ExitError
	}
	if line := fb.Describe(); line != "" {
		s.warn(line)
	}
	// Read pricing from the local catalog only; unknown prices remain unknown.
	var modelCost *catalog.Cost
	var modelCaps tools.Caps
	if model, found := catalogSnapshot.Catalog.Get(ref.Ref); found {
		modelCost = model.Cost
		modelCaps = capsForTools(model)
	}
	// [tools].budget_usd only works when the catalog actually knows this
	// model's price: buildAgentOptions leaves every *CostUSD field at zero
	// when cost is nil, so engine.estimateCost prices every token at zero
	// and the accumulated CostUSD can never reach a positive budget no
	// matter how many tool calls run. That silently disables the one guard
	// §15/§16 exist for — the runaway-cost stop — on exactly the models
	// most likely to need it (new/undiscovered models, or a stale local
	// catalog, are the ones without a price yet). Warn once, loudly,
	// instead of letting the user believe a ceiling is still in effect.
	//
	// modelCost == nil is checked explicitly rather than relying on
	// Cost.Zero() alone: Zero() has a nil-safe receiver that returns false
	// for a nil *Cost on purpose (nil means "unknown", a distinct case from
	// the genuinely-free model Zero() documents), so this condition would
	// silently miss the nil case if it only called modelCost.Zero().
	if cfg.Tools.Enabled && cfg.Tools.BudgetUSD > 0 && (modelCost == nil || modelCost.Zero()) {
		s.warn(fmt.Sprintf(
			"[tools] budget_usd = %.4f is set, but the catalog has no price for %q; "+
				"the cost budget cannot be enforced for this model and will not stop the turn",
			cfg.Tools.BudgetUSD, ref.Ref))
	}

	// Startup warnings are printed only now that ref.Provider is known.
	// cfg.Warnings carries one entry per enabled provider missing its
	// credential (expand.go), and printing all of it unconditionally used
	// to warn about every declared-but-unused provider on every single
	// turn — noise about providers this run never touches.
	// `config check`/`doctor`/`provider list` still read cfg.Warnings
	// directly and print all of it, on purpose: those are audit commands,
	// not a turn. See warnings.go's own doc comment.
	for _, w := range FilterWarningsForProviders(cfg.Warnings, ref.Provider) {
		s.warn(fmt.Sprintf("[%s] %s", w.Where, w.Msg))
	}

	pc, ok := FindProvider(cfg, ref.Provider)
	if !ok {
		s.fail(fmt.Errorf("provider %q for %q is not declared in %s",
			ref.Provider, ref.Ref, configOrigin(cfg)))
		return ExitError
	}
	prov, err := NewProvider(cfg, pc, opts.Version)
	if err != nil {
		s.fail(err)
		return ExitError
	}

	system := opts.System
	if system == "" {
		sp, warn := SystemPrompt(cfg)
		if warn != "" {
			s.warn(warn)
		}
		system = sp
	}

	stream := cfg.App.Stream
	if opts.Stream != nil {
		stream = *opts.Stream
	}

	// 5. Session. A failure to persist must not prevent the response from
	// being printed: warn and keep going without saving. Losing the file is
	// annoying; losing the response the user already paid for is not
	// acceptable.
	save := cfg.Session.Save
	if opts.Save != nil {
		save = *opts.Save
	}
	var store *convo.Store
	var conv *convo.Conversation
	if save {
		dir := opts.SessionDir
		if dir == "" {
			dir = cfg.Session.Dir
		}
		if dir == "" {
			dir = xdg.SessionsDir()
		}
		store, conv, err = openSession(dir, prompt, ref.Ref)
		if err != nil {
			s.warn(fmt.Sprintf("the session will not be saved: %v", err))
			store, conv = nil, nil
		}
	}
	sessionID := ""
	if conv != nil {
		sessionID = conv.ID
	}

	s.meta(ref, sessionID, stream)

	// 6. History. In headless mode the history is a single message: there's
	// no previous conversation to load until --resume exists (Step 13).
	user := convo.User(prompt)
	if store != nil {
		if err := store.Append(conv.ID, user); err != nil {
			s.warn(fmt.Sprintf("could not save the user's message: %v", err))
		}
	}

	req := provider.Request{
		Model:    ref.WireID,
		Messages: []convo.Message{user},
		System:   system,
		Stream:   stream,
	}

	// 7. The turn. SIGINT and SIGTERM cancel the context instead of killing
	// the process: this way the partial response is saved marked as Aborted
	// (§7.4) and the session file stays consistent.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	started := time.Now()
	var msg convo.Message
	var turnErr error
	if cfg.Tools.Enabled {
		// The agent-loop path (Step 14/§12bis): RunAgentTurn owns history
		// itself, appending every message it produces (tool calls, tool
		// results, the final answer) to hist as it goes and — via
		// runAgentTurnHeadless — persisting each one individually as soon
		// as it lands, exactly the same one-message-at-a-time contract
		// convo's own doc comment describes for Append (§10). hist is conv
		// when the turn is being saved, or a throwaway Conversation when
		// it isn't (save=false, or the session file failed to open above):
		// either way the model still needs *some* history object to append
		// tool calls and results to across iterations.
		hist := conv
		if hist == nil {
			hist = &convo.Conversation{}
		}
		guard := permissions.New(cfg.Tools.Permissions, opts.Yolo, nil)
		// asker is nil: §21.7's own door table gives `-p` headless "no" for
		// "ask available?" -- there is no human on the other end of a
		// headless process the way there is for the TUI or a live `serve`
		// connection, so ask_user always degrades to reporting "no human
		// is present to ask" as tool-error data here, exactly as it always
		// has (this is the final, documented answer for this door, not
		// merely an unwired seam -- see runAgentTurnHeadless's own doc
		// comment on its asker parameter).
		msg, turnErr = runAgentTurnHeadless(ctx, prov, cfg.Tools, guard, modelCost, modelCaps, cfg.App.MaxRetries, req, user, s, store, conv, hist, opts.AllowToolCreate, nil)
	} else {
		msg, turnErr = runTurn(ctx, prov, req, s, cfg.App.MaxRetries)
	}
	msg.Model = ref.Ref
	elapsed := time.Since(started)

	if ctx.Err() != nil {
		msg.Aborted = true
	}

	// 8. Persisting the turn. Saved even if it failed or was cancelled: a
	// marked partial is worth more than a gap in the history.
	//
	// The tools path skips this: runAgentTurnHeadless already persisted
	// every message the loop produced as it produced it, message by
	// message. Persisting msg here too — a summary built from
	// engine.AgentResult, not one of those real messages — would duplicate
	// the final answer in the session file.
	if !cfg.Tools.Enabled && store != nil && (len(msg.Blocks) > 0 || msg.Aborted) {
		if err := store.Append(conv.ID, msg); err != nil {
			s.warn(fmt.Sprintf("could not save the response: %v", err))
		}
	}
	if store != nil {
		if n := cfg.Session.KeepLast; n > 0 {
			_, _ = store.Rotate(n)
		}
	}

	if turnErr != nil {
		s.fail(turnErr)
	}
	s.done(msg, elapsed)

	switch {
	case msg.Aborted:
		return ExitAborted
	case turnErr != nil:
		// A truncated stream that left text behind is not a total failure:
		// the user has a partial response on stdout and the warning on
		// stderr. Still returning 1 is correct for a script checking $?.
		return ExitError
	default:
		return ExitOK
	}
}

// runTurn runs the turn and translates provider.Event into sink calls,
// accumulating the assistant message in convo's shape.
//
// The retry here only covers the handshake —the error Stream returns before
// handing back the channel—, which is exactly the case provider.Error marks
// as Retryable: a 429 with Retry-After, or a socket that never opened. A cut
// mid-stream is never retried, because resending the turn would duplicate
// the text already printed. The full policy with exponential backoff and
// jitter is Step 8 (engine/retry.go); this is the honest minimum so a `-p`
// doesn't die on a courtesy 429.
func runTurn(ctx context.Context, p provider.Provider, req provider.Request, s sink, maxRetries int) (convo.Message, error) {
	msg := convo.NewMessage(convo.RoleAssistant)

	var ch <-chan provider.Event
	for attempt := 0; ; attempt++ {
		var err error
		ch, err = p.Stream(ctx, req)
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			return msg, nil // cancellation: not something to report as an error
		}

		var pe *provider.Error
		if attempt < maxRetries && errors.As(err, &pe) && pe.Retryable {
			d := pe.RetryAfter
			if d <= 0 {
				d = backoff(attempt)
			}
			if d > 30*time.Second {
				d = 30 * time.Second
			}
			s.warn(fmt.Sprintf("%v · retry %d/%d in %s", err, attempt+1, maxRetries, d.Round(time.Millisecond)))
			select {
			case <-time.After(d):
				continue
			case <-ctx.Done():
				return msg, nil
			}
		}
		return msg, err
	}

	var turnErr error
	for ev := range ch {
		switch ev.Kind {
		case provider.EventDelta:
			msg.AppendText(ev.Text)
			s.delta(ev.Text)
		case provider.EventReasoning:
			msg.AppendReasoning(ev.Text)
			s.reasoning(ev.Text)
		case provider.EventToolCall:
			msg.Blocks = append(msg.Blocks, convo.Block{
				Kind: convo.BlockToolCall, Name: ev.Name, Args: ev.Args,
				// ToolCallID and Signature were both dropped here. This path
				// only prints the turn, but the message it builds is what a
				// later --resume replays, and a replayed Gemini tool call
				// without its signature is an HTTP 400.
				ToolCallID: ev.ID, Signature: ev.Signature,
			})
			s.tool(ev.Name, ev.Args)
		case provider.EventUsage:
			if ev.Usage != nil {
				if msg.Usage == nil {
					msg.Usage = &convo.Usage{}
				}
				*msg.Usage = *ev.Usage
				s.usage(ev.Usage)
			}
		case provider.EventWarning:
			s.warn(ev.Text)
		case provider.EventError:
			// The loop isn't broken here: the provider.Event contract
			// guarantees EventDone comes afterwards, and draining the
			// channel to the end is what leaves the socket goroutine closed
			// instead of hanging.
			if turnErr == nil {
				turnErr = ev.Err
			}
		case provider.EventDone:
			if ev.Usage != nil && msg.Usage == nil {
				u := *ev.Usage
				msg.Usage = &u
				s.usage(&u)
			}
		}
	}
	if ctx.Err() != nil {
		// A cancellation wins over any stream error: what happened is the
		// user hit Ctrl+C, not that the provider failed.
		return msg, nil
	}
	return msg, turnErr
}

// backoff is the fallback when the service doesn't send Retry-After: 500ms,
// 1s, 2s, 4s. No jitter, which arrives with the full Step 8 policy.
func backoff(attempt int) time.Duration {
	d := 500 * time.Millisecond << attempt
	if d > 8*time.Second {
		d = 8 * time.Second
	}
	return d
}

// buildPrompt combines the -p argument with whatever arrives on stdin.
//
// The order matters: instruction first, material after. So
// `cat error.log | ishakat -p "explain this error"` produces a prompt that
// reads top to bottom, the same way models read it too.
//
// stdin is only read when it isn't a terminal. If it were, reading would
// block the process waiting for an EOF the user doesn't know they have to
// give.
func buildPrompt(flagPrompt string, in io.Reader, stdinTTY bool) (string, error) {
	flagPrompt = strings.TrimSpace(flagPrompt)
	if stdinTTY || in == nil {
		return flagPrompt, nil
	}

	// 8 MiB cap: an accidental `cat` of a binary must not fill up a phone's
	// RAM nor get sent to a service that charges per token.
	const maxStdin = 8 << 20
	raw, err := io.ReadAll(io.LimitReader(in, maxStdin))
	if err != nil {
		return "", err
	}
	piped := strings.TrimRight(string(raw), "\n")
	if strings.TrimSpace(piped) == "" {
		return flagPrompt, nil
	}
	if flagPrompt == "" {
		return piped, nil
	}
	return flagPrompt + "\n\n" + piped, nil
}

// openSession creates the turn's JSONL file. The provisional title is the
// first line of the prompt, trimmed; autoname (§5.2) will replace it using
// compact_model once Step 12 exists.
func openSession(dir, prompt, model string) (*convo.Store, *convo.Conversation, error) {
	store, err := convo.NewStore(dir)
	if err != nil {
		return nil, nil, err
	}
	conv, err := store.New(titleFrom(prompt), model)
	if err != nil {
		return nil, nil, err
	}
	return store, conv, nil
}

func titleFrom(prompt string) string {
	s := strings.TrimSpace(prompt)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	const max = 60
	if len([]rune(s)) > max {
		s = string([]rune(s)[:max]) + "…"
	}
	if s == "" {
		return "new conversation"
	}
	return s
}

func boolOr(v *bool, f func() bool) bool {
	if v != nil {
		return *v
	}
	return f()
}

// agentturn.go wires headless.go (Step 5) to engine.RunAgentTurn (Step 14):
// the path Headless takes when cfg.Tools.Enabled is true, in place of
// runTurn's direct provider.Stream drain. It is the piece §12bis's own plan
// names as the closing criterion: `ishakat -p "…"` with a real tool
// (read_file, glob, …) producing a correct answer in headless mode.
//
// The trade-off this file makes explicit, on purpose: RunAgentTurn blocks
// until the whole loop (every iteration, every tool call) finishes — it has
// no per-token callback. So the tools-enabled path in headless mode loses
// live token-by-token streaming to stdout; the text still reaches stdout
// (and --json still emits its "delta" line) but only once the model's final
// answer is in hand, not as it is generated. runTurn's direct
// provider.Stream drain is untouched and keeps streaming exactly as before
// when cfg.Tools.Enabled is false — the common case, and every existing
// test's path.
package app

import (
	"context"
	"fmt"

	"github.com/MichiTrader/ishakat/internal/catalog"
	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/convo"
	"github.com/MichiTrader/ishakat/internal/engine"
	"github.com/MichiTrader/ishakat/internal/permissions"
	"github.com/MichiTrader/ishakat/internal/provider"
	"github.com/MichiTrader/ishakat/internal/tools"
)

// buildAgentOptions translates config.Tools into engine.AgentOptions,
// binding tools.Core() — the six native tools Step 15 shipped — as the
// catalogue and runner. cfgTools.MaxCallsPerTurn/MaxOutputBytes pass through
// as-is: when they are the zero value (unset in config), AgentOptions'
// own doc comment says zero means its built-in default, and because
// defaults.toml's own values (25, 32768) equal RunAgentTurn's built-in
// defaults exactly, a stock configuration and a caller that skipped config
// entirely (a zero config.Tools) both mean the same thing.
func buildAgentOptions(cfgTools config.Tools, guard *permissions.Guard, cost *catalog.Cost) engine.AgentOptions {
	reg := tools.Core()
	opts := engine.AgentOptions{
		Tools:          ToolDefsFrom(reg),
		Runner:         ToolRunnerWithGuard(reg, guard),
		MaxToolCalls:   cfgTools.MaxCallsPerTurn,
		MaxOutputBytes: cfgTools.MaxOutputBytes,
		BudgetUSD:      cfgTools.BudgetUSD,
	}
	if cost != nil {
		opts.InputCostUSD = cost.In
		opts.OutputCostUSD = cost.Out
		opts.CacheReadCostUSD = cost.CacheRead
		opts.CacheWriteCostUSD = cost.CacheWrite
	}
	return opts
}

// runAgentTurnHeadless runs one turn through RunAgentTurn instead of
// runTurn's direct stream drain, translating the result into the same sink
// calls (s.delta, s.reasoning, s.tool, s.usage) headless.go already prints
// through, and persisting every message the loop produced — not just a
// single final one, since a tool-using turn can add several (an assistant
// turn with tool calls, the tool results, a second assistant turn, …).
//
// hist is the in-memory conversation the loop reads from and appends to
// (conv when the caller is saving the session, an ephemeral one otherwise —
// see Headless's own step 6). user must not already be in hist.Messages:
// this function appends it before calling RunAgentTurn, exactly once, so
// the model's first iteration sees it in context. store/conv persist each
// new message as it is produced, mirroring convo's own contract that a
// message is only ever appended once it is complete (§10); conv may be nil
// when the caller only wants to read hist.Active(), and store nil disables
// persistence entirely — both match Headless's own save=false path.
//
// The returned convo.Message is the turn's final assistant text, in the
// same shape runTurn returns for the streaming path, so Headless's own
// step 8 (Model, Aborted, s.done, the exit-code switch) needs no branch of
// its own to handle either path — except skipping the second, redundant
// persistence of that same summary message, which the caller must do since
// this function already persisted the loop's real messages individually.
func runAgentTurnHeadless(
	ctx context.Context,
	prov provider.Provider,
	cfgTools config.Tools,
	guard *permissions.Guard,
	cost *catalog.Cost,
	maxRetries int,
	req provider.Request,
	user convo.Message,
	s sink,
	store *convo.Store,
	conv *convo.Conversation,
	hist *convo.Conversation,
) (convo.Message, error) {
	stream := NewStreamer(prov, provider.Caps{Tools: true})
	eng := engine.New(stream, maxRetries)
	opts := buildAgentOptions(cfgTools, guard, cost)
	// Usage.CostUSD is persisted on assistant messages. Reusing that durable
	// total means a resumed conversation starts at the amount already spent,
	// rather than resetting the budget at every process launch.
	if prior := hist.Usage(); prior != nil {
		opts.SpentUSD = prior.CostUSD
	}

	engReq := engine.Request{
		Model:  req.Model,
		System: req.System,
		// Messages is rebuilt every iteration inside RunAgentTurn from
		// hist.Active() — see agentloop.go's own comment on iterReq — so
		// what's set here never actually reaches the wire; Model and
		// System are the only fields RunAgentTurn does not overwrite.
	}

	hist.Add(user)
	before := len(hist.Messages)

	result, turnErr := eng.RunAgentTurn(ctx, engReq, opts, hist)

	// Cancellation is not a failure — matching runTurn's own contract for
	// the exact same event (its ctx.Err() branches return (msg, nil), never
	// a wrapped context.Canceled). Without this, a Ctrl+C mid-loop would
	// make Headless's s.fail print "context canceled" on stderr even though
	// its exit-code switch already keys off msg.Aborted, not turnErr, for
	// that case.
	if ctx.Err() != nil {
		turnErr = nil
	}

	// Walk every message the loop added — in order, so a --json consumer
	// sees tool calls before the text that used their results — translating
	// each block into the sink call runTurn already makes for the
	// equivalent provider.Event, and persisting the message once translated.
	for i := before; i < len(hist.Messages); i++ {
		m := hist.Messages[i]
		for _, b := range m.Blocks {
			switch b.Kind {
			case convo.BlockText:
				s.delta(b.Text)
			case convo.BlockReasoning:
				s.reasoning(b.Text)
			case convo.BlockToolCall:
				s.tool(b.Name, b.Args)
			case convo.BlockToolResult:
				s.toolResult(b.Name, b.IsError, b.Text)
			}
		}
		if store != nil && conv != nil {
			if err := store.Append(conv.ID, m); err != nil {
				s.warn(fmt.Sprintf("could not save a message: %v", err))
			}
		}
	}

	if result.Usage != nil {
		s.usage(result.Usage)
	}
	if result.Stopped != "" {
		s.warn(result.Stopped)
	}

	msg := convo.NewMessage(convo.RoleAssistant)
	if result.Text != "" {
		msg.Blocks = append(msg.Blocks, convo.TextBlock(result.Text))
	}
	if result.Usage != nil {
		u := *result.Usage
		msg.Usage = &u
	}
	if result.Aborted {
		msg.Aborted = true
	}

	return msg, turnErr
}

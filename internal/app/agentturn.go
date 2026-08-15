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
	"strings"
	"time"

	"github.com/MichiTrader/ishakat/internal/catalog"
	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/convo"
	"github.com/MichiTrader/ishakat/internal/engine"
	"github.com/MichiTrader/ishakat/internal/permissions"
	"github.com/MichiTrader/ishakat/internal/provider"
	"github.com/MichiTrader/ishakat/internal/tools"
	"github.com/MichiTrader/ishakat/internal/xdg"
)

// buildAgentOptions translates config.Tools into engine.AgentOptions,
// binding tools.WithMetaTools — layer 1's seven native tools (the six from
// Step 15 plus fetch from Step 19), dispatch (Step 22, gated on
// dispatchRunner below rather than on cfgTools at all), every layer-2
// declarative tool (rung 1, Step 20) found under cfgTools.Dir, and
// whichever of §19.5's five meta-tools cfgTools.Dir/Evolve/hasTTY currently
// allow (Step 21) — as the catalogue and runner. fetch's egress allowlist
// comes straight from
// cfgTools.Egress, the same config.Tools already threaded through this
// function; no new parameter is needed, and the same allowlist governs
// every declarative tool's own [origin] check (see
// tools.DeclarativeTools' doc comment) and tool_probe/tool_create/tool_edit's
// identical check on a manifest they would write or invoke (§19.8
// mitigation 4). A discovery problem (an unreadable directory, or the
// first unparseable tool.toml) does not stop the turn — the second return
// value is a warn string the caller surfaces the same way SystemPrompt's
// own skills.Discover warning already does, never a fatal error, matching
// DiscoverDeclarative's own "an install with no tools of its own yet is
// not a warning" contract. cfgTools.MaxCallsPerTurn/MaxOutputBytes pass
// through as-is: when they are the zero value (unset in config),
// AgentOptions' own doc comment says zero means its built-in default, and
// because defaults.toml's own values (25, 32768) equal RunAgentTurn's
// built-in defaults exactly, a stock configuration and a caller that
// skipped config entirely (a zero config.Tools) both mean the same thing.
//
// hasTTY is §19.6's own gate on tool_create specifically (docs/PLAN.md
// §19.7, quoted verbatim in tools.MetaToolsOptions' own doc comment: "With
// no TTY, tool_create is denied. Full stop.") — the caller passes what it
// already knows (app.go's own term.IsTerminal(os.Stdout.Fd()) for the TUI;
// always false from runAgentTurnHeadless below, since headless mode has no
// reviewer channel for gate 2 to resolve against regardless of what fd
// term.IsTerminal would report for a script piping through a real
// terminal's stdin/stdout — see runAgentTurnHeadless's own comment on why
// that is deliberate, not merely unwired yet).
//
// dispatchRunner is Step 22's own addition: tools.MetaToolsOptions.
// DispatchRunner, passed straight through. nil (every call site before
// Step 22, and every call this function's own tests still make) means
// dispatch is absent from the returned registry, exactly as before this
// parameter existed. The two real call sites (below, and app.go's Run)
// build it via newSubAgentRunner (dispatch.go) closed over their own
// already-resolved *engine.Engine/model/system — this function itself
// stays the one place that turns cfgTools into a Registry, so it is also
// the one place dispatch's own capability slots in, rather than a third
// copy of "call WithMetaTools" appearing wherever a caller wants dispatch
// too.
//
// caps is §20.11 item 4's own addition: the active model's real
// tools.Caps (built by capsForTools, evolve.go), passed straight through
// to tools.MetaToolsOptions.ActiveCaps — see that field's own doc comment
// for what it gates. The zero value (every call site and every test
// before this parameter existed) satisfies every manifest that declares
// no requires_caps/min_context of its own, so this is purely additive.
func buildAgentOptions(cfgTools config.Tools, guard *permissions.Guard, cost *catalog.Cost, caps tools.Caps, hasTTY bool, dispatchRunner tools.SubAgentRunner) (engine.AgentOptions, string) {
	reg, warn := tools.WithMetaTools(tools.MetaToolsOptions{
		Dir:             cfgTools.Dir,
		Allow:           cfgTools.Egress.Allow,
		AllowAll:        cfgTools.Egress.AllowAll,
		EvolveMode:      cfgTools.Evolve.Mode,
		AllowWithoutTTY: cfgTools.Evolve.AllowWithoutTTY,
		HasTTY:          hasTTY,
		Thresholds:      evolveThresholds(cfgTools, cfgTools.Evolve),
		LedgerPath:      xdg.UsageFile(),
		DispatchRunner:  dispatchRunner,
		ActiveCaps:      caps,
	})
	if guard != nil {
		// Every tool beyond the native seven (declarative tools chief
		// among them) gets its real Tool.Danger()-inferred Tier registered
		// here, so permissions.Guard's own tierFor/mode default (safe but
		// blind: Critical/"ask" for any name it does not recognize) becomes
		// aware of what declarative.go's inferDanger actually computed for
		// each manifest, without permissions ever importing tools (see
		// Guard.SetToolTiers' own doc comment on why the translation lives
		// on this side of the boundary). tools.Danger is still a 3-value
		// enum (§19.5 rule #2); this mapping can never produce Controlled,
		// since no manifest declares that property yet -- only bash's own
		// per-argument classifier (guard.go's bashTier) currently does.
		tiers := make(map[string]permissions.Tier)
		for _, t := range reg.Tools() {
			switch t.Danger() {
			case tools.DangerLow:
				tiers[t.Name()] = permissions.Safe
			case tools.DangerMedium:
				tiers[t.Name()] = permissions.Sensitive
			default:
				tiers[t.Name()] = permissions.Critical
			}
		}
		guard.SetToolTiers(tiers)
	}
	runner := ToolRunnerWithGuard(reg, guard)
	// §19.7's ledger only ever feeds gate 1's Repetition criterion, which
	// only matters while tool_create could ever be offered again -- under
	// Mode == "off" nothing will read it before the next config change, so
	// observing every bash/fetch call would be pure write-amplification
	// for no consumer (see WithMetaTools' own "off means absent, not
	// merely denied" framing for the identical reasoning applied to the
	// meta-tools themselves).
	if !strings.EqualFold(strings.TrimSpace(cfgTools.Evolve.Mode), "off") {
		runner = ledgerObservingRunner(runner, xdg.UsageFile(), nil)
	}
	opts := engine.AgentOptions{
		Tools:          ToolDefsFrom(reg),
		Runner:         runner,
		MaxToolCalls:   cfgTools.MaxCallsPerTurn,
		MaxOutputBytes: cfgTools.MaxOutputBytes,
		BudgetUSD:      cfgTools.BudgetUSD,
		MinInterval:    time.Duration(cfgTools.MinIntervalMS) * time.Millisecond,
	}
	if cost != nil {
		opts.InputCostUSD = cost.In
		opts.OutputCostUSD = cost.Out
		opts.CacheReadCostUSD = cost.CacheRead
		opts.CacheWriteCostUSD = cost.CacheWrite
	}
	return opts, warn
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
//
// buildAgentOptions is called with hasTTY = allowToolCreate here, regardless
// of whether opts.StdinTTY/StdoutTTY (Headless's own test seams) happen to
// report a real terminal on the other end of a pipe: §19.6's own rule is
// about a human being present to resolve gate 2's approval dialog, and
// headless mode has no reviewer at all wired to Guard for a High-tier
// request (see headless.go's own `permissions.New(cfg.Tools.Permissions,
// opts.Yolo, nil)` — the third argument, reviewer, is always nil on this
// path) — a `tool_create` call that reached gate 2 here would hit
// Guard.Authorize's own "g.reviewer == nil" branch and simply fail with
// ErrDenied regardless of allowToolCreate. That is deliberate, not a bug:
// --allow-tool-create (§13/§19.7, HeadlessOptions.AllowToolCreate's own doc
// comment) only grants *visibility* — tool_create appears in the registry
// and the model may propose it — never unattended approval, since headless
// still has no human to resolve the approval dialog against. With
// allowToolCreate == false (the default, and every call site before this
// parameter existed), the catalogue omits tool_create entirely, matching
// docs/PLAN.md §19.7's own instruction verbatim: "With no TTY, tool_create
// is denied. Full stop."
//
// caps is §20.11 item 4's own addition, threaded through to both
// buildAgentOptions and newSubAgentRunner below — see buildAgentOptions'
// own doc comment for what it gates.
func runAgentTurnHeadless(
	ctx context.Context,
	prov provider.Provider,
	cfgTools config.Tools,
	guard *permissions.Guard,
	cost *catalog.Cost,
	caps tools.Caps,
	maxRetries int,
	req provider.Request,
	user convo.Message,
	s sink,
	store *convo.Store,
	conv *convo.Conversation,
	hist *convo.Conversation,
	allowToolCreate bool,
) (convo.Message, error) {
	stream := NewStreamer(prov, provider.Caps{Tools: true})
	eng := engine.New(stream, maxRetries)
	// Same eng, req.Model and req.System a sub-agent's own turn should
	// answer with -- see newSubAgentRunner's own doc comment on why a
	// sub-agent reuses the parent's already-resolved provider/model rather
	// than re-resolving one of its own. hasTTY (allowToolCreate here, same
	// value this call site already passes to buildAgentOptions for its own
	// registry) is threaded through identically, so a sub-agent sees the
	// exact same tool_create visibility rule the parent turn does.
	dispatchRunner := newSubAgentRunner(eng, req.Model, req.System, cfgTools, guard, cost, caps, allowToolCreate)
	opts, toolsWarn := buildAgentOptions(cfgTools, guard, cost, caps, allowToolCreate, dispatchRunner)
	if toolsWarn != "" {
		s.warn(toolsWarn)
	}
	// Usage.CostUSD is persisted on assistant messages. Reusing that durable
	// total means a resumed conversation starts at the amount already spent,
	// rather than resetting the budget at every process launch.
	if prior := hist.Usage(); prior != nil {
		opts.SpentUSD = prior.CostUSD
	}
	// A rate-limited retry can legitimately pause for tens of seconds. The
	// loop already waits out the provider's Retry-After window (step 26,
	// fix 2); saying so is what keeps that from looking like a hang to
	// someone watching a phone screen. Set here rather than in
	// buildAgentOptions because this is the layer that owns a sink.
	opts.OnWait = func(wait time.Duration, attempt int) {
		s.warn(fmt.Sprintf("rate limited, waiting %s before retrying", roundWait(wait)))
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

// roundWait renders a retry wait at a granularity a person can read. A raw
// time.Duration prints as "22.317849213s", which is nine digits of noise on
// a 40-column phone screen (§2). Sub-second waits keep millisecond
// resolution because "0s" would be a lie about why the agent paused.
func roundWait(d time.Duration) time.Duration {
	if d < time.Second {
		return d.Round(time.Millisecond)
	}
	return d.Round(time.Second)
}

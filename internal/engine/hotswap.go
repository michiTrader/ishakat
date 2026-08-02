// hotswap.go implements the §4.6 checks that run before a model switch is
// accepted mid-conversation: comparing the destination window against the
// conversation's own estimate, detecting history that leans on a capability
// the destination does not have, and refusing a provider with no resolved
// credential. All three are pure — no terminal, no network — which is what
// lets internal/tui's confirmation dialog (Step 11, §9.5) stay a thin
// renderer over whatever this file decides.
package engine

import (
	"github.com/MichiTrader/ishakat/internal/catalog"
	"github.com/MichiTrader/ishakat/internal/convo"
)

// ConflictKind names one of the three checks that can block an instant swap.
type ConflictKind int

const (
	// ContextTooSmall means the conversation's own estimated context does
	// not fit in the destination model's window.
	ContextTooSmall ConflictKind = iota
	// MissingCaps means the conversation's history already contains a block
	// (an image, a tool call/result) the destination model cannot serve.
	MissingCaps
	// NoAuth means the destination's provider has no resolved credential
	// yet (catalog.Health.Usable() is false).
	NoAuth
)

var conflictKindNames = map[ConflictKind]string{
	ContextTooSmall: "context_too_small",
	MissingCaps:     "missing_caps",
	NoAuth:          "no_auth",
}

func (k ConflictKind) String() string {
	if s, ok := conflictKindNames[k]; ok {
		return s
	}
	return "unknown"
}

// Conflict is one reason CheckSwap refused an instant switch. The fields are
// deliberately raw data (token counts, a Caps bitmask) rather than
// pre-rendered prose: this package has no business deciding how "142k"
// should be abbreviated or which language the dialog speaks — that is
// internal/tui's job, same separation §4.2 already draws between catalog.Cost
// and the picker's costLabel.
type Conflict struct {
	Kind ConflictKind

	// Tokens and Window are set only when Kind == ContextTooSmall: the
	// conversation's current context estimate and the destination model's
	// effective window (catalog.Model.EffectiveContext), both in tokens.
	Tokens int
	Window int

	// Missing is set only when Kind == MissingCaps: the subset of
	// catalog.Caps the conversation's history actually uses (Vision for an
	// image block, Tools for a tool call/result) that the destination model
	// does not advertise. Only those two bits are ever set here — §4.6 only
	// names those two examples, and every other capability either has no
	// corresponding convo.BlockKind yet or does not change what gets sent
	// on the wire.
	Missing catalog.Caps
}

// Action is one of the remedies the confirmation dialog can offer. Cancel is
// the zero value on purpose: a Plan nobody finishes wiring up defaults to
// "do nothing" rather than silently compacting a conversation.
type Action int

const (
	ActionCancel Action = iota
	ActionCompact
	ActionDropOldest
)

var actionNames = map[Action]string{
	ActionCancel:     "cancel",
	ActionCompact:    "compact",
	ActionDropOldest: "drop_oldest",
}

func (a Action) String() string {
	if s, ok := actionNames[a]; ok {
		return s
	}
	return "unknown"
}

// Plan is CheckSwap's verdict (§4.6). OK means the switch can happen this
// instant with nothing more than the §4.6 confirmation line; a non-OK Plan
// is what the ModeConfirm dialog of §9.5 renders.
type Plan struct {
	OK        bool
	Conflicts []Conflict
	// Suggested is which remedy is pre-selected in the dialog. It is only
	// meaningful when Has(ContextTooSmall): compacting or dropping the
	// oldest turns are the only two mechanical remedies this package can
	// apply on its own, so a Plan whose only conflicts are MissingCaps
	// and/or NoAuth leaves Suggested at ActionCancel — there is nothing
	// else to offer, since neither compacting nor dropping messages fixes a
	// missing capability or a missing credential.
	Suggested Action
	// EstAfter is the estimated token count once a compaction runs,
	// populated only alongside Suggested == ActionCompact. It is an
	// estimate for the same reason convo.EstimateText is: the real number
	// depends on compact_model's actual summary, which does not exist until
	// Step 12 wires /compact's client-side compaction to a model call.
	EstAfter int
}

// Has reports whether the plan carries a conflict of the given kind.
func (p Plan) Has(kind ConflictKind) bool {
	for _, c := range p.Conflicts {
		if c.Kind == kind {
			return true
		}
	}
	return false
}

// defaultCompactKeepTurns mirrors [compact].keep_last_turns' documented
// default (§5.2's config.example.toml). CheckSwap only uses it to *estimate*
// what a compaction would leave behind for the dialog's "(~38k)" label —
// the value the user actually configured is read and applied by Step 12's
// /compact, not by this pure check.
const defaultCompactKeepTurns = 4

// summaryBudget is a placeholder token cost for the summary block a real
// compaction would produce. The real number does not exist until Step 12
// asks compact_model to write one; this constant only keeps CheckSwap's
// EstAfter from claiming compacting is free, and gets corrected the moment
// an actual compaction runs and its usage comes back from the provider.
const summaryBudget = 500

// CheckSwap runs the three checks of §4.6 and reports whether the switch
// from `from` to `to` can happen instantly. c may be nil (no conversation
// yet, e.g. before the first turn): every check below treats that as "the
// history is empty", which never conflicts with anything.
func CheckSwap(c *convo.Conversation, from, to catalog.Model) Plan {
	_ = from // not consulted today: see missingCaps's own comment on why.

	var conflicts []Conflict

	tokens := contextTokens(c)
	window := to.EffectiveContext()
	if tokens > window {
		conflicts = append(conflicts, Conflict{Kind: ContextTooSmall, Tokens: tokens, Window: window})
	}

	if missing := missingCaps(c, to); missing.Any() {
		conflicts = append(conflicts, Conflict{Kind: MissingCaps, Missing: missing})
	}

	if !to.Health.Usable() {
		conflicts = append(conflicts, Conflict{Kind: NoAuth})
	}

	plan := Plan{Conflicts: conflicts, OK: len(conflicts) == 0}
	switch {
	case plan.Has(ContextTooSmall):
		plan.Suggested = ActionCompact
		plan.EstAfter = estimateAfterCompact(c)
	case len(conflicts) > 0:
		plan.Suggested = ActionCancel
	}
	return plan
}

func contextTokens(c *convo.Conversation) int {
	if c == nil {
		return 0
	}
	return c.ContextTokens()
}

// missingCaps walks the conversation's active history and reports which
// capabilities it actually uses that `to` cannot serve: images towards a
// model with no vision, tool calls/results towards one with no tool calling
// (§4.6's own two examples). `from` is deliberately not consulted — what
// matters is what the history already contains, not which model produced
// it, since that content has to travel in the very next request regardless
// of who wrote it.
func missingCaps(c *convo.Conversation, to catalog.Model) catalog.Caps {
	var used catalog.Caps
	if c != nil {
		for _, m := range c.Active() {
			for _, b := range m.Blocks {
				switch b.Kind {
				case convo.BlockImage:
					used.Vision = true
				case convo.BlockToolCall, convo.BlockToolResult:
					used.Tools = true
				}
			}
		}
	}
	var missing catalog.Caps
	if used.Vision && !to.Caps.Vision {
		missing.Vision = true
	}
	if used.Tools && !to.Caps.Tools {
		missing.Tools = true
	}
	return missing
}

// estimateAfterCompact estimates the context tokens a compaction using
// defaultCompactKeepTurns would leave behind: the kept turns and the system
// messages (compact.go's PlanCompact never touches either) plus a flat
// budget for the summary block that would replace everything else.
func estimateAfterCompact(c *convo.Conversation) int {
	if c == nil {
		return 0
	}
	p := convo.PlanCompact(c.Messages, defaultCompactKeepTurns)
	kept := 0
	for _, i := range p.Keep {
		kept += convo.EstimateMessage(c.Messages[i])
	}
	for _, i := range p.System {
		kept += convo.EstimateMessage(c.Messages[i])
	}
	return kept + summaryBudget
}

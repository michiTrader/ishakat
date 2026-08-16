// ask_user.go is §19.1's one documented exception (settled 2026-08-15,
// docs/PLAN.md §21.16 decision 1, §19.1 itself): the ninth core tool,
// added to the fixed eight rather than as a mode of dispatch. The
// exception is granted on a property no other tool has -- ask_user
// executes nothing. It reads no file, runs no command, opens no socket;
// its entire effect is to hand control back to a human and wait for an
// answer. That is also why it is always present and never denyable
// (permissions.Guard's own Authorize bypasses every gate for this one
// name -- see that method's own doc comment for exactly where and why),
// while every other tool in this package still goes through the ordinary
// permission machinery.
//
// This is the model-initiated producer §21.7's own diagram names ("the
// model asks ask_user (a tool)"), the sibling of the runtime's own
// Guard-raised question -- the two stay two producers by design (§21.16
// decision 1's own closing paragraph), so this file never reaches into
// internal/permissions and internal/permissions never reaches into this
// file beyond the one bypass.
//
// The actual capability to collect a human's answer is injected as an
// ask.Asker, exactly like dispatch.go's SubAgentRunner is injected rather
// than built here: this package cannot import internal/tui/internal/app
// (TestToolsNoImportaTUI, internal/arch_test.go), so the door that can
// actually render a form -- the TUI today, serve's WebSocket and a real
// headless policy in the future -- is wired in by internal/app, the one
// package trusted to bridge every seam like this one. Unlike Dispatch's
// Runner, which is nil until a caller opts in, AskUser is registered
// unconditionally by WithMetaTools regardless of whether an Asker has
// been wired yet (see that function's own doc comment) -- "always
// present" is §19.1's own contract for this tool, not a capability a
// caller can choose to omit from the model's own tool list the way
// dispatch's absence is a deliberate, visible choice.
//
// internal/ask is safe to import here: TestToolsNoImportaTUI's own
// forbidden list (internal/tui, lipgloss, bubbletea, bubbles) does not
// name it, and internal/ask's own TestAskStaysPresentationFree keeps it
// presentation-free for exactly this reason -- a primitive usable from
// every door, including this one.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MichiTrader/ishakat/internal/ask"
)

// askUserArgs is ask_user's argument shape: one question, an optional
// fixed menu of choices, and whether a free-text answer is also accepted.
// This deliberately mirrors a single ask.Question rather than a whole
// ask.Form of several -- the smallest shape that lets the model ask one
// genuinely blocking question, matching this tool's own §21.16 framing
// ("the model may call ask_user when it is genuinely blocked", not for a
// multi-step survey, which is the runtime's own §21.6 mission dialog's
// job, not this tool's). Extending to several questions later is a
// backward-compatible addition (a new array field), not a breaking change
// to this one.
type askUserArgs struct {
	Question      string   `json:"question"`
	Options       []string `json:"options,omitempty"`
	AllowFreeText bool     `json:"allow_free_text,omitempty"`
}

// askUserQuestionID is the fixed ask.Question.ID this tool always uses --
// there is only ever one question per call (askUserArgs' own doc comment),
// so a caller reading back ask.Answers never needs to discover an ID the
// model chose; it is always this constant.
const askUserQuestionID = "answer"

// AskUser is the ask_user core tool (§19.1's ninth, §21.16 decision 1):
// ask the human a question and wait for the answer.
//
// Danger: low, matching read_file/glob/grep/fetch (permissions.Guard maps
// tools.DangerLow to Safe -- see internal/app/agentturn.go's own
// buildAgentOptions), even though ask_user is not merely low-risk the way
// a read is: it is risk-free by construction, since it executes nothing
// at all. Guard.Authorize does not even consult this value for ask_user
// in practice (its own bypass runs first, see that method's doc comment)
// but the tier is still declared here rather than left at the zero value
// by omission, matching Dispatch's own "explicit for legibility" reasoning
// for a case its own gate structurally cannot reach either.
type AskUser struct {
	// Asker is the injected capability that actually collects a human's
	// answer -- nil means no door has wired one yet (every caller before
	// this tool's TUI/serve bridge lands, and any session with no human
	// attached, e.g. headless). Run below reports that as tool-error
	// data, the identical "reports the reason, lets the model react"
	// contract Dispatch.Run already follows for its own nil Runner,
	// rather than a panic or a Go error a caller would have to
	// specifically guard against.
	Asker ask.Asker
}

var _ Tool = AskUser{}

func (AskUser) Name() string   { return "ask_user" }
func (AskUser) Danger() Danger { return DangerLow }
func (AskUser) Description() string {
	return "Ask the human a question and wait for their answer. Use this only when genuinely blocked on a decision only a human can make -- not for routine confirmations, which the runtime already handles on its own. Optionally offer a fixed set of choices; the human can always answer with free text instead."
}

func (AskUser) Parameters() json.RawMessage {
	return objectSchema(map[string]prop{
		"question": {
			Type:        "string",
			Description: "The question to ask the human.",
		},
		"options": {
			Type:        "array",
			Description: "Optional fixed choices to offer. If omitted, the human answers with free text.",
			Items:       &prop{Type: "string"},
		},
		"allow_free_text": {
			Type:        "boolean",
			Description: "Offer a free-text answer in addition to options. Ignored (always true) when options is empty, since a question with no options must still be answerable.",
		},
	}, "question")
}

func (a AskUser) Run(ctx context.Context, rawArgs json.RawMessage) (Result, error) {
	var args askUserArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return Result{}, fmt.Errorf("ask_user: invalid arguments: %w", err)
	}
	if strings.TrimSpace(args.Question) == "" {
		return Result{}, fmt.Errorf("ask_user: question is required")
	}

	if a.Asker == nil {
		return ErrorResult("ask_user is not available in this session: no human is present to ask"), nil
	}

	opts := make([]ask.Option, 0, len(args.Options))
	for _, o := range args.Options {
		opts = append(opts, ask.Option{Label: o, Value: o})
	}
	// A question with no fixed options has nothing else to answer with,
	// so free text is forced on regardless of what the model passed --
	// the same "cannot be sure, do not guess" shape bashTier already
	// applies to an unparseable command, applied here to an unanswerable
	// question instead of silently producing a Form no door can resolve.
	allowFreeText := args.AllowFreeText || len(opts) == 0

	form := ask.Form{
		Title: "ask_user",
		Questions: []ask.Question{
			{
				ID:            askUserQuestionID,
				Prompt:        args.Question,
				Options:       opts,
				AllowFreeText: allowFreeText,
			},
		},
	}

	answers, err := a.Asker.Ask(ctx, form)
	if err != nil {
		if ctx.Err() != nil {
			// The caller's own context was cancelled, not merely a door
			// failing to answer -- surface it as a Go error so the
			// parent agent loop's cancellation path handles it, the
			// identical distinction dispatch.go's Run already draws
			// between ctx.Err() and an ordinary operational failure
			// (§12bis: cancellation is not "the tool failed").
			return Result{}, ctx.Err()
		}
		return ErrorResult(fmt.Sprintf("ask_user: %v", err)), nil
	}

	answer, ok := answers[askUserQuestionID]
	if !ok || !answer.Answered() {
		return OKResult("(the human gave no answer)"), nil
	}
	if answer.FreeText != "" {
		return OKResult(answer.FreeText), nil
	}
	return OKResult(answer.Value), nil
}

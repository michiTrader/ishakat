// Package ask is the §21.7 primitive: "internal/ask is the primitive. Tool
// approval is one of its cases." Two producers exist and stay two producers
// (§21.16 decision 1) — the model calling its own ask_user tool, and the
// runtime's Guard needing a decision before a tool call can proceed — and
// both eventually hand an ask.Form to an ask.Asker:
//
//	            ask.Question / ask.Form
//	                      |
//	      +---------------+---------------+
//	      |                               |
//	the model asks                  the runtime asks
//	ask_user (a tool)               Guard needs a decision
//	      |                               |
//	      +---------------+---------------+
//	                      v
//	                  ask.Asker
//	                      |
//	      +---------------+---------------+
//	      v               v               v
//	     TUI          serve (WS)       headless
//	  a dialog      permission_request  policy decides
//
// This file defines the vocabulary (Option, Question, Form, Answer,
// Answers, Asker) and the one round trip every synchronous door in this
// codebase used to hand-roll separately. Step 27 (docs/PLAN.md §21.14)
// scopes this package to the primitive plus the tab-bar form (form.go) and
// the deduplicated round trip (AwaitReply, below); the deeper convergence
// of permissions.Reviewer onto ask.Asker itself, and the Guard rewrite
// that would require, is deliberately left to steps 28-29 (§21.14's own
// note: "the Guard is rewritten once, behind an already-migrated dialog,
// rather than twice").
//
// This package must stay presentation-free, the same rule internal/engine
// and internal/tools already hold (§6.1): no lipgloss, no bubbletea, no
// theme. A door renders a Form however it wants — form.go's Render is a
// plain-text reference rendering, not a mandate — which is what lets the
// exact same Form reach a TUI overlay, a WebSocket event, and (when
// headless has nothing to ask) simply never render at all.
package ask

import "context"

// Option is one fixed choice offered by a Question. Value is opaque to
// this package — it is whatever the producer (the model's ask_user
// arguments, or the runtime code building a permission question) chooses
// to key its own answer-handling switch on — ask itself never inspects or
// compares it beyond passing it back unchanged inside an Answer.
type Option struct {
	Label string
	Value string
}

// Question is one prompt a Form asks. Options is the fixed menu, if any;
// AllowFreeText additionally offers a free-text answer on every question,
// per §21.7's own "a free-text option on every question" rule. A Question
// with no Options and AllowFreeText true is a pure free-text prompt; a
// Question with Options and AllowFreeText both is the common case — a menu
// with an escape hatch for the option that was not on it.
type Question struct {
	ID            string
	Prompt        string
	Options       []Option
	AllowFreeText bool
}

// Answer is what a human gave for one Question: the Value of the Option
// they picked, or free text, never both — Answered reports whether either
// is actually present, so a zero Answer (the question was never reached)
// is distinguishable from a deliberately empty free-text answer only by
// the caller choosing to treat an empty string as meaningful, which no
// caller in this codebase currently does.
type Answer struct {
	Value    string
	FreeText string
}

// Answered reports whether a carries a real answer rather than the zero
// value a Question that was never reached leaves behind.
func (a Answer) Answered() bool { return a.Value != "" || a.FreeText != "" }

// Form is one or more Questions asked together, with one shared Submit
// step (§21.7's "a final Submit tab that summarizes before sending"). A
// Form with a single Question is the tool-approval case; a Form with more
// than one is what the model's future ask_user tool (step 32) and the
// §21.6 mission-constraint survey both need.
type Form struct {
	Title     string
	Questions []Question
}

// Answers maps a Question.ID to the Answer given for it. Only ever
// produced whole, by State.Submit or by a door that skips the tab-bar walk
// entirely (headless's own policy-decides path never renders a Form at
// all, and therefore never needs Answers built incrementally).
type Answers map[string]Answer

// Asker collects a human decision for a Form. Each door (TUI, serve,
// headless) supplies its own implementation; §21.7's table is the
// complete list of what "collects" means on each one — a rendered dialog
// on the TUI, a permission_request/-equivalent round trip over serve's
// WebSocket, and on headless, no Asker at all: policy decides and a denial
// exits non-zero, exactly as a scheduled task's autonomy is declared by
// itself and never inherited (§21.7's own hard rule).
type Asker interface {
	Ask(ctx context.Context, form Form) (Answers, error)
}

// AwaitReply is the round trip every synchronous door-side Asker (and,
// before this package existed, every hand-rolled permissions.Reviewer
// bridge) shares: publish the request through whatever transport the door
// uses, then block on either the answer or ctx.Done(), whichever comes
// first. publish is called exactly once, after reply is ready to receive
// on — never before, so a transport that can answer synchronously (nothing
// in this codebase does, but a future in-process Asker safely could)
// cannot race ahead of the select below.
//
// Before this function existed, internal/app/toolreview.go's toolReviewer
// and internal/app/serve.go's serveReviewer each hand-rolled the identical
// publish-then-select shape around two different transports (a
// *tea.Program message versus a WebSocket event) — the exact duplication
// §21.7 names when it says "permissions.Reviewer becomes a special case of
// ask.Asker, which deletes the duplicated round-trip logic currently in
// internal/app/toolreview.go and internal/app/serve.go". This function is
// that deletion: both bridges now differ only in what publish does, never
// in how the wait itself works.
func AwaitReply[T any](ctx context.Context, reply <-chan T, publish func()) (T, error) {
	publish()
	select {
	case v := <-reply:
		return v, nil
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
}

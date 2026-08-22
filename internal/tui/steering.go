// steering.go closes out W2 item 4's remaining half (F13,
// docs/ROADMAP-ux-2026-08-20.md, DECISION-2 consequence 3): the actual
// steering/follow-up queue engine.AgentSink.Inject (W2 item 1,
// agentloop.go) and the ui.steering_mode/ui.followup_mode config keys
// (W2 item 4's own scaffolding slice, config/schema.go, keys.go) were
// built for but never wired until now.
//
// DECISION-2's own wording draws a hard line between the two lists this
// file holds, and that line drives everything below it:
//
//   - Steering messages (ordinary chat text submitted with Submit while
//     ModeBusy) are "a real conversation event, not a UI trick" —
//     consequence 2's own words. Each one becomes a real convo.Message,
//     shown in the transcript the instant it is typed (queueSteering,
//     below), exactly as if it had been sent while ModeChat. What
//     changes from an ordinary submit is only when the model actually
//     sees it, and when it is actually persisted to JSONL: not on the
//     next request (there is no "next request" mid-turn to attach it
//     to), but on the agent loop's own next Inject poll (agentloop.go's
//     runAgentTurn, right before that iteration's request is rebuilt) —
//     drainSteering below is Inject's entire body, reached through
//     agentstream.go's own agentStreamBuf.inject(). Persistence follows
//     the same path: once Inject hands the message to history.Add,
//     finishAgentTurn's own per-message persistence loop
//     (agentturn.go) is what actually writes it to JSONL, exactly once —
//     see queueSteering's own comment for why this function must not
//     also call recordMessage itself.
//
//   - Follow-up messages (alt+enter) are "session state" — consequence
//     3's own words — not yet a conversation event at all: nothing is
//     persisted or shown in the transcript when one is queued, because
//     there is nothing to show yet, only a plain string sitting in
//     memory until it is actually submitted as an ordinary turn once the
//     current one ends (checkFollowup, root.go's checkEndOfTurn). That
//     submission — through the exact same m.submit(text) any Enter
//     keypress in ModeChat already calls — is what finally turns it into
//     a convo.Message, persists it, and shows it in the transcript; this
//     file's own enqueueFollowup never does either.
//
// Both lists live behind one pointer on Root (steering, not a value),
// for the same reason agentStream/buf already are pointers: Update takes
// and returns Root by value on every message, and a value held across
// that copy would silently fork into two independent queues the instant
// a second Update call ran — the exact bug class agentTurnState's own
// doc comment (agentturn.go) already warns about for hist. Unlike
// agentStream/buf, though, releaseTurn/finishAgentTurn must never nil
// this out: DECISION-2 consequence 3 is explicit that "the queue
// survives a turn boundary" — a follow-up queued mid-turn has to still
// be there once the turn that produced it has already finished, and a
// steering message that arrived one poll too late to matter for this
// iteration must still be there for the next one.
package tui

import (
	"strconv"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/convo"
)

// steeringQueue holds both of F13's lists. mu guards both: enqueueSteering
// (Update's own goroutine, queueSteering below) and drainSteering
// (agentTurnCmd's own goroutine, agentstream.go's agentStreamBuf.inject)
// run on different goroutines for the exact same reason agentStreamBuf's
// own mu does (see its doc comment) — the identical two-goroutine handoff,
// just for messages instead of text deltas. followups, in contrast, is
// only ever touched from Update's own goroutine (enqueueFollowup,
// drainFollowups, peekFollowups, removeFollowupAt are all called from key
// dispatch or checkFollowup, never from inside the agent loop) — kept
// behind the same mutex anyway for one uniform locking discipline rather
// than a second, unlocked code path that would be correct today only by
// accident of which callers happen to exist.
type steeringQueue struct {
	mu        sync.Mutex
	steering  []convo.Message
	followups []string
}

// newSteeringQueue returns an empty queue. Root's own steeringQueue()
// accessor (below) is what every real caller goes through — this is
// exported to the package (lowercase, so package-internal only) mainly
// for tests that want a queue with no Root attached at all.
func newSteeringQueue() *steeringQueue { return &steeringQueue{} }

// enqueueSteering appends a message queueSteering (below) already built
// and shown in the transcript — this call only ever adds it to the list
// drainSteering will eventually hand to Inject, never anything about
// whether it should be a conversation event at all (that decision already
// happened by the time this runs).
func (q *steeringQueue) enqueueSteering(m convo.Message) {
	if q == nil {
		return
	}
	q.mu.Lock()
	q.steering = append(q.steering, m)
	q.mu.Unlock()
}

// enqueueFollowup appends plain text — not yet a convo.Message, per this
// file's own doc comment on why follow-ups are "session state" rather
// than a conversation event until drainFollowups' result is actually
// handed to m.submit.
func (q *steeringQueue) enqueueFollowup(text string) {
	if q == nil || text == "" {
		return
	}
	q.mu.Lock()
	q.followups = append(q.followups, text)
	q.mu.Unlock()
}

// drainSteering is Inject's entire body (engine.AgentSink.Inject,
// agentloop.go; wired through agentstream.go's agentStreamBuf.inject):
// called once per iteration, from the agent loop's own goroutine. mode is
// ui.steering_mode (steeringModeOr, chat.go) resolved once at turn start
// and handed to agentStreamBuf unchanged for the whole turn — see that
// field's own comment for why a value that cannot change mid-turn needs
// no fresher read than that.
//
// "one-at-a-time" (the documented default) removes and returns only the
// single oldest queued message, leaving the rest queued for the next
// poll — DECISION-2's own "Steering mode: one-at-a-time" from the
// original report, taken literally: the model sees one steering nudge
// per iteration, never a pile of them landing in the same context all at
// once. "batch" (or, defensively, anything else — steeringModeOr and
// validateSteering, internal/config/validate.go, already narrow this to
// the two documented values before it reaches here) removes and returns
// every message queued so far in one poll.
func (q *steeringQueue) drainSteering(mode string) []convo.Message {
	if q == nil {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.steering) == 0 {
		return nil
	}
	if mode != "batch" {
		m := q.steering[0]
		q.steering = q.steering[1:]
		return []convo.Message{m}
	}
	out := q.steering
	q.steering = nil
	return out
}

// drainFollowups is checkFollowup's own body (root.go's checkEndOfTurn):
// called once a turn has fully ended and every other end-of-turn check
// (fallback, auto-compact, suggest) has already run and left the mode at
// ModeChat. mode is ui.followup_mode (followupModeOr, chat.go).
//
// "one-at-a-time" removes and returns only the single oldest queued
// follow-up, as its own []string of length 1 — checkFollowup submits it
// as its own turn, and whatever follow-ups remain stay queued for the
// turn after that one ends. "batch" removes and returns every follow-up
// queued so far; checkFollowup joins them into the one message that
// turn's own request will carry, rather than starting one turn per
// follow-up in immediate succession.
func (q *steeringQueue) drainFollowups(mode string) []string {
	if q == nil {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.followups) == 0 {
		return nil
	}
	if mode != "batch" {
		f := q.followups[0]
		q.followups = q.followups[1:]
		return []string{f}
	}
	out := q.followups
	q.followups = nil
	return out
}

// peekFollowups is a read-only snapshot for rendering (queueedit.go's
// renderQueueEdit) — it never drains, so opening the edit overlay to look
// at the queue has no side effect on what the next turn will actually
// submit. The returned slice is a copy: a later removeFollowupAt
// mutating the queue out from under an already-rendered dialog would be
// one more thing to reason about for no benefit.
func (q *steeringQueue) peekFollowups() []string {
	if q == nil {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]string(nil), q.followups...)
}

// steeringLen and followupLen report each list's current length without
// draining — steeringLen exists purely for this package's own tests to
// assert "still queued, untouched" after some other operation; nothing
// in the running interface itself needs to ask a queue how long it is
// without also wanting to act on what is in it.
func (q *steeringQueue) steeringLen() int {
	if q == nil {
		return 0
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.steering)
}

func (q *steeringQueue) followupLen() int {
	if q == nil {
		return 0
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.followups)
}

// removeFollowupAt deletes the follow-up at index i (queueedit.go's own
// "d" key), reporting whether there was one there to remove — the only
// mutation to this list that does not come from either end (enqueue
// appends, drainFollowups removes from the front), which is exactly
// DECISION-2's "so its contents can be edited" half of alt+up, taken as
// literally as one deletable row per keypress rather than full
// reordering/retyping (deferred — see docs/PLAN.md's own Bitácora entry
// for this slice).
func (q *steeringQueue) removeFollowupAt(i int) bool {
	if q == nil {
		return false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if i < 0 || i >= len(q.followups) {
		return false
	}
	q.followups = append(q.followups[:i], q.followups[i+1:]...)
	return true
}

// steeringQueue is Root's own lazily-initialised accessor for its
// steering field: NewRoot already sets that field for every real caller,
// but most of this package's own tests build a Root{} literal directly
// (bypassing NewRoot entirely), and a nil queue there must not be a
// crash the first time queueSteering/queueFollowup/checkFollowup reaches
// for it.
//
// A pointer receiver is required — not merely idiomatic — because the
// lazy-init branch has to mutate m.steering itself, on whichever Root
// value m already is; a value receiver would only ever mutate a copy the
// caller could never see. Every call site in this package calls it on an
// addressable Root (a named local variable, per Go's own addressability
// rules for method calls), so this works uniformly whether m.steering
// was already set by NewRoot or is still nil. Once set, the pointer
// itself travels unchanged through every later copy Update's own
// value-in-value-out signature makes of Root — the same "a pointer field
// survives being copied, the struct it points at does not get copied
// with it" property agentStream/buf already rely on, which is also
// exactly why this queue, unlike those two, is never nilled out by
// releaseTurn: see this file's own package doc comment.
func (m *Root) steeringQueue() *steeringQueue {
	if m.steering == nil {
		m.steering = newSteeringQueue()
	}
	return m.steering
}

// queueSteering is updateBusy's real Submit branch for ordinary text
// (F13's first half): everything busyslash_internal_test.go's own
// TestBusySubmitWithOrdinaryTextReportsNotWiredYet used to name "W2,
// siguiente parte" — this is that "siguiente parte".
//
// DECISION-2 consequence 2's own words, applied literally: the message
// is built and shown in the transcript immediately, exactly as an
// ordinary ModeChat submit already does — the only things this function
// does NOT do are m.conv.Add(msg) and m.recordMessage(msg): m.conv during
// a live tools-enabled turn is not the authoritative copy
// (agentTurnState's own doc comment explains why — hist, a
// *convo.Conversation captured by pointer at startAgentTurn, is), and the
// only correct way to reach hist from here is exactly what this function
// does instead: hand the message to the steering queue and let
// drainSteering/Inject add it on the agent loop's own goroutine, at its
// own next iteration boundary, the same "produce here, consume there,
// never both from the same side" discipline agentStreamBuf's whole
// existence is already built on.
//
// Persistence is deliberately left to finishAgentTurn's own per-message
// loop (agentturn.go), not done here: once Inject copies this exact
// convo.Message into hist.Messages, that loop already walks
// hist.Messages[before:] and calls m.recordMessage on each entry it
// finds there in order — a steering message that landed via Inject is
// simply one more entry in that range. Calling recordMessage a second
// time here, before the message has even reached hist, would not skip
// that loop's own write; it would only make the exact same message land
// in the JSONL file twice. The transcript append below is what actually
// satisfies "shown ... immediately" (DECISION-2's own requirement); the
// JSONL write itself happens at the same moment every other message this
// turn produces does, which is the ordinary, already-correct timing
// finishAgentTurn's loop was built for.
func (m Root) queueSteering(text string) (tea.Model, tea.Cmd) {
	m.input.Reset()
	m.menu = slashMenu{}

	m.transcript = append(m.transcript, transcriptEntry{
		role: "user", name: "tú", text: text, ts: time.Now(),
	})
	m = m.recordHistory(text)

	msg := convo.User(text)
	m.steeringQueue().enqueueSteering(msg)

	return m, nil
}

// queueFollowup is alt+enter's own handler (F13's second half): unlike
// queueSteering above, nothing is persisted or shown yet — see this
// file's own package doc comment for why a follow-up is "session state,"
// not a conversation event, until checkFollowup actually submits it. A
// short transcript notice (slashNotice's own shape, not a real
// transcriptEntry with a "user" role) is still given so the keypress has
// a visible effect: silently swallowing a typed line with nothing to
// show for it would be indistinguishable from a dropped keystroke.
func (m Root) queueFollowup(text string) (tea.Model, tea.Cmd) {
	m.input.Reset()
	m.menu = slashMenu{}
	m.steeringQueue().enqueueFollowup(text)

	g := m.lay.glyphs()
	n := m.steeringQueue().followupLen()
	word := "mensaje"
	if n != 1 {
		word = "mensajes"
	}
	return m.slashNotice(g.dot + " en cola para después: " + strconv.Itoa(n) + " " + word + " en espera (alt+up para editar)")
}

// checkFollowup is checkEndOfTurn's own closing check (root.go),
// DECISION-2 consequence 3's own words: "the next thing submitted once
// the current turn, if any, finishes." Reached only once every other
// end-of-turn check (checkFallback, checkAutoCompact, checkSuggest) has
// already run and left mode at ModeChat — the same "nothing else
// pending" gate checkSuggest's own caller already applies for the
// identical reason: a follow-up must not preempt a compaction or a
// crystallization offer that also wants this exact moment, and must not
// fire at all if either of those instead opened its own dialog.
//
// followupModeOr resolved fresh here, not carried from turn start the
// way agentStreamBuf's own steeringMode is: unlike Inject (polled from
// inside a running loop, where a value that could change mid-turn would
// be one more moving part to reason about), this runs entirely on
// Update's own goroutine, in the one instant right after a turn ends —
// reading m.cfg fresh here costs nothing and needs no snapshot.
func (m Root) checkFollowup() (tea.Model, tea.Cmd) {
	drained := m.steeringQueue().drainFollowups(followupModeOr(m.cfg))
	if len(drained) == 0 {
		return m, nil
	}
	return m.submit(strings.Join(drained, "\n\n"))
}

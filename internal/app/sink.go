package app

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/MichiTrader/ishakat/internal/convo"
)

// sink is the output side of headless mode. It exists so the pipeline never
// needs to know whether it's writing text for `| cat` or one JSON line per
// event for `| jq`: the turn runs exactly the same way in both cases, which
// is what makes headless mode a good test bench for the engine.
type sink interface {
	// meta is emitted once, before the first token.
	meta(ref ModelRef, sessionID string, stream bool)
	delta(s string)
	reasoning(s string)
	tool(name string, args json.RawMessage)
	usage(u *convo.Usage)
	warn(s string)
	fail(err error)
	// done closes the output. msg is the already-completed assistant message.
	done(msg convo.Message, elapsed time.Duration)
}

// ─────────────────────────────────────────────────────────────
// plain text
// ─────────────────────────────────────────────────────────────

// textSink writes the response and nothing else to stdout.
//
// Three decisions make `ishakat -p "…" | cat` usable in a script:
//
//  1. stdout only carries the assistant's text. Warnings, errors and
//     reasoning go to stderr, so redirecting the output to a file gives the
//     file one expects, not a log.
//  2. Writes are unbuffered, event by event: in a pipe you can watch the
//     response progress the same way you would in a terminal.
//  3. The trailing newline is only added if the model didn't put one there.
//     A duplicate `\n` breaks `$(…)` and a missing one glues the shell
//     prompt to the last token.
type textSink struct {
	out io.Writer
	err io.Writer

	// showReasoning comes from ui.reasoning = "full". With "off" or
	// "collapsed" reasoning is not printed: in a pipe there's nothing to
	// collapse.
	showReasoning bool

	// quiet silences warnings, not errors.
	quiet bool

	// color turns off when stderr is not a terminal (§ Step 5: "if stdout
	// isn't a TTY, disable all color"). stdout never carries color in
	// headless mode.
	color bool

	lastByte byte
	wrote    bool

	// warnedSeen is P3's dedupe fix (see warnings.go's WarningPrinter,
	// which app.go's own startup path uses for the exact same reason):
	// headless.go's step 4 can call warn with the identical string more
	// than once in a single run (P2's boot-fallback notice plus a
	// provider-scoped cfg.Warnings entry sometimes overlap in wording), and
	// printing the same sentence to stderr twice is never useful.
	warnedSeen map[string]bool
}

func (t *textSink) meta(ModelRef, string, bool) {}

func (t *textSink) delta(s string) {
	if s == "" {
		return
	}
	if _, err := io.WriteString(t.out, s); err != nil {
		return
	}
	t.wrote = true
	t.lastByte = s[len(s)-1]
}

func (t *textSink) reasoning(s string) {
	if !t.showReasoning || s == "" {
		return
	}
	io.WriteString(t.err, t.paint(dim, s))
}

func (t *textSink) tool(name string, args json.RawMessage) {
	// Tools are post-1.0 (§18). A tool call the model asked for is honest
	// information that can't be swallowed silently, but it shouldn't
	// pollute stdout either.
	fmt.Fprintf(t.err, "%s\n", t.paint(dim, "· tool call requested: "+name+" "+compact(args)))
}

func (t *textSink) usage(*convo.Usage) {}

func (t *textSink) warn(s string) {
	if t.quiet || s == "" {
		return
	}
	if t.warnedSeen == nil {
		t.warnedSeen = map[string]bool{}
	}
	if t.warnedSeen[s] {
		return
	}
	t.warnedSeen[s] = true
	fmt.Fprintf(t.err, "%s %s\n", t.paint(yellow, "⚠"), s)
}

func (t *textSink) fail(err error) {
	if err == nil {
		return
	}
	// If there was already partial text, the newline keeps the error from
	// gluing itself to the model's last token.
	if t.wrote && t.lastByte != '\n' {
		io.WriteString(t.out, "\n")
		t.lastByte = '\n'
	}
	fmt.Fprintf(t.err, "%s %v\n", t.paint(red, "✗"), err)
}

func (t *textSink) done(msg convo.Message, _ time.Duration) {
	if t.wrote && t.lastByte != '\n' {
		io.WriteString(t.out, "\n")
	}
	if msg.Aborted && !t.quiet {
		fmt.Fprintf(t.err, "%s\n", t.paint(dim, "· cancelled; partial response kept"))
	}
}

// Minimal hand-written ANSI codes, on purpose: lipgloss has no business on
// the path of a pipe, and §6.1 says the network layer and the color layer
// don't mix. Three constants are enough for stderr.
const (
	dim    = "2"
	yellow = "33"
	red    = "31"
)

func (t *textSink) paint(code, s string) string {
	if !t.color {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func compact(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if len(s) > 120 {
		return s[:120] + "…"
	}
	return s
}

// ─────────────────────────────────────────────────────────────
// JSON lines
// ─────────────────────────────────────────────────────────────

// jsonEvent is one --json line. One object per line, no wrapper and no
// trailing comma, which is the only thing `jq` can consume in streaming
// mode.
type jsonEvent struct {
	Type string `json:"type"`

	Text string `json:"text,omitempty"`

	// meta
	Model    string `json:"model,omitempty"`
	Provider string `json:"provider,omitempty"`
	WireID   string `json:"wire_id,omitempty"`
	Session  string `json:"session,omitempty"`
	Stream   *bool  `json:"stream,omitempty"`

	// tool_call
	Name string          `json:"name,omitempty"`
	Args json.RawMessage `json:"args,omitempty"`

	// usage
	Usage *convo.Usage `json:"usage,omitempty"`

	// done
	Aborted bool  `json:"aborted,omitempty"`
	MS      int64 `json:"ms,omitempty"`
}

// jsonSink emits one event per line. The contract for whoever pipes this
// into `jq` is the same as provider.Event's channel: "done" is always the
// last event and arrives exactly once, and an "error" precedes it if
// something failed.
type jsonSink struct {
	enc *json.Encoder

	// warnedSeen mirrors textSink's own field: the same run can call warn
	// with an identical string more than once (see textSink.warn's doc
	// comment), and a --json consumer piping into jq has just as little
	// use for the same "warning" line encoded twice as a plain-text one
	// does.
	warnedSeen map[string]bool
}

func newJSONSink(w io.Writer) *jsonSink {
	enc := json.NewEncoder(w)
	// No HTML escaping: a model writing <div> or & has no reason to come out
	// as \u003cdiv\u003e in the JSON line.
	enc.SetEscapeHTML(false)
	return &jsonSink{enc: enc}
}

func (j *jsonSink) emit(ev jsonEvent) {
	_ = j.enc.Encode(ev)
}

func (j *jsonSink) meta(ref ModelRef, session string, stream bool) {
	s := stream
	j.emit(jsonEvent{
		Type: "meta", Model: ref.Ref, Provider: ref.Provider, WireID: ref.WireID,
		Session: session, Stream: &s,
	})
}

func (j *jsonSink) delta(s string) {
	if s != "" {
		j.emit(jsonEvent{Type: "delta", Text: s})
	}
}

func (j *jsonSink) reasoning(s string) {
	// Reasoning is always emitted in --json: the jq consumer can filter it
	// out, and hiding it here would be losing information the provider
	// already sent.
	if s != "" {
		j.emit(jsonEvent{Type: "reasoning", Text: s})
	}
}

func (j *jsonSink) tool(name string, args json.RawMessage) {
	j.emit(jsonEvent{Type: "tool_call", Name: name, Args: args})
}

func (j *jsonSink) usage(u *convo.Usage) {
	if u != nil {
		j.emit(jsonEvent{Type: "usage", Usage: u})
	}
}

func (j *jsonSink) warn(s string) {
	if s == "" {
		return
	}
	if j.warnedSeen == nil {
		j.warnedSeen = map[string]bool{}
	}
	if j.warnedSeen[s] {
		return
	}
	j.warnedSeen[s] = true
	j.emit(jsonEvent{Type: "warning", Text: s})
}

func (j *jsonSink) fail(err error) {
	if err != nil {
		j.emit(jsonEvent{Type: "error", Text: err.Error()})
	}
}

func (j *jsonSink) done(msg convo.Message, elapsed time.Duration) {
	j.emit(jsonEvent{
		Type:    "done",
		Text:    msg.Text(),
		Usage:   msg.Usage,
		Aborted: msg.Aborted,
		MS:      elapsed.Milliseconds(),
	})
}

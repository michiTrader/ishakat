// Package slash is the declarative slash-command registry described by §9.6
// and §13 of docs/PLAN.md. Every command the interactive TUI understands is
// one row of a single table (Commands, below): a name, its aliases, whether
// it takes an argument, a short description, and a Kind that tells the
// caller what running it means.
//
// Nothing here knows how to run a command. internal/tui owns the engine, the
// conversation and the terminal, and switches on Kind itself — this package
// only classifies and looks things up, which is what keeps it importable (and
// testable) without pulling any of that in. Adding a command touches exactly
// one place, the Commands table: /help (Registry.HelpLines) and the
// autocomplete dropdown (Registry.Filter) are both generated from it, never
// hand-duplicated. If a command ever needs a second table to show up in both
// places, the design has drifted from this package's one job.
package slash

import (
	"fmt"
	"strings"
)

// Kind is what a command does, expressed in a form internal/tui can switch on
// without slash knowing anything about engines, conversations or terminals.
type Kind int

const (
	// KindHelp opens the help screen (§9.7).
	KindHelp Kind = iota
	// KindClear wipes the screen, exactly like ctrl+l — the conversation
	// itself (and what the next request sends the model) is untouched.
	KindClear
	// KindNew discards the current conversation and starts a blank one.
	KindNew
	// KindExit quits the program.
	KindExit
	// KindModel opens the model picker (§9.4) or switches directly when the
	// argument resolves unambiguously (§4.5). Step 10.
	KindModel
	// KindCompact summarizes the older turns with compact_model, replacing
	// them with a BlockSummary (§10, Step 12). Manual only — the automatic
	// trigger at [compact].trigger_pct runs the same underlying flow without
	// going through this Kind at all.
	KindCompact
	// KindCopy copies an assistant response to the system clipboard via
	// OSC52 (§13, Step 13). "" copies the last response; an argument is the
	// 1-based count of responses back from the end ("/copy 2" is the answer
	// before the last one). ctrl+y is the same behaviour with no argument.
	KindCopy
	// KindRetry drops the assistant response the last turn produced (if
	// any) and asks the same last user message again (§13, Step 13) —
	// useful after a cancelled or unsatisfying answer without retyping the
	// question.
	KindRetry
	// KindStats reports the session's token and cost accounting (§13, Step
	// 13): the running totals convo.Conversation.Usage already sums, priced
	// against the active model's catalog.Cost.
	KindStats
	// KindResume opens the session picker (§13): a menu of previously saved
	// conversations, headers only, read from disk without loading a single
	// full session until one is actually chosen — the same "list is cheap,
	// load is deferred" split convo.Store.List/Load already draws.
	KindResume
	// KindModels lists the current catalog snapshot inside the session
	// (§13, Step 13), grouped by provider — the read-only counterpart of
	// KindModel's picker, for a quick scan without opening an overlay.
	KindModels
	// KindUnimplemented is a command that already has a row in the table —
	// so /help and the dropdown both list it, matching §13's full command
	// list — but no runner behind it yet. The caller reports that instead of
	// guessing at a behaviour nobody has built.
	KindUnimplemented
)

// Command is one row of the registry. It carries only the declarative shape
// §9 describes; internal/tui's runner switches on Kind to decide what
// actually happens.
type Command struct {
	// Name is the canonical form, without the leading "/".
	Name string
	// Aliases are extra names that resolve to the same Command.
	Aliases []string
	// Describe is the one-line, user-facing description shown in /help and
	// in the autocomplete dropdown.
	Describe string
	// ArgHint is the placeholder shown after the name when the command takes
	// an argument (e.g. "[texto]", "[n]"), or "" when it takes none.
	ArgHint string
	Kind    Kind
}

// Usage is how a command is written out ("/model [texto]"): the one string
// both /help and the dropdown build a row around, so the two can never show
// a different spelling of the same command.
func (c Command) Usage() string {
	if c.ArgHint == "" {
		return "/" + c.Name
	}
	return "/" + c.Name + " " + c.ArgHint
}

// Commands is the single source of truth for every slash command ishakat
// knows about (§13's list, in the same order as §9.7's help screen). Display
// order and Filter's tie-break both follow this order.
var Commands = []Command{
	{Name: "help", Describe: "esta pantalla", Kind: KindHelp},
	{Name: "model", ArgHint: "[texto]", Describe: "cambiar modelo", Kind: KindModel},
	{Name: "models", Describe: "explorar catalogo", Kind: KindModels},
	{Name: "theme", ArgHint: "[nombre]", Describe: "cambiar tema", Kind: KindUnimplemented},
	{Name: "compact", Describe: "resumir contexto", Kind: KindCompact},
	{Name: "new", Describe: "conversacion nueva", Kind: KindNew},
	{Name: "resume", Describe: "reabrir una sesion", Kind: KindResume},
	{Name: "clear", Describe: "limpiar pantalla", Kind: KindClear},
	{Name: "copy", ArgHint: "[n]", Describe: "copiar respuesta", Kind: KindCopy},
	{Name: "retry", Describe: "reintentar ultimo", Kind: KindRetry},
	{Name: "stats", Describe: "tokens y costo", Kind: KindStats},
	{Name: "config", Describe: "config efectiva", Kind: KindUnimplemented},
	{Name: "debug", Describe: "diagnostico", Kind: KindUnimplemented},
	{Name: "exit", Aliases: []string{"quit"}, Describe: "salir", Kind: KindExit},
}

// Registry is a queryable view over a command table, indexed once at
// construction so lookups and dropdown filtering do not walk the whole table
// on every keystroke.
type Registry struct {
	cmds  []Command
	byRef map[string]int // lowercase name or alias -> index into cmds
}

// NewRegistry builds a Registry over cmds. Tests pass a small table of their
// own; Default wraps the real one.
func NewRegistry(cmds []Command) Registry {
	r := Registry{cmds: cmds, byRef: make(map[string]int, len(cmds)*2)}
	for i, c := range cmds {
		r.byRef[strings.ToLower(c.Name)] = i
		for _, a := range c.Aliases {
			r.byRef[strings.ToLower(a)] = i
		}
	}
	return r
}

// Default is the registry over the built-in Commands table — what
// internal/tui.NewRoot uses outside of tests.
func Default() Registry { return NewRegistry(Commands) }

// All returns the table in display order (§9.7). Callers must treat the
// result as read-only: it is the Registry's own backing slice, not a copy.
func (r Registry) All() []Command { return r.cmds }

// Lookup resolves an exact name or alias, case-insensitively.
func (r Registry) Lookup(ref string) (Command, bool) {
	i, ok := r.byRef[strings.ToLower(ref)]
	if !ok {
		return Command{}, false
	}
	return r.cmds[i], true
}

// Filter returns every command whose name or an alias starts with prefix
// (case-insensitive), in table order. This is what the §9.6 dropdown draws
// while the command name is still being typed; an empty prefix — the input
// is just "/" — matches everything.
func (r Registry) Filter(prefix string) []Command {
	prefix = strings.ToLower(prefix)
	var out []Command
	for _, c := range r.cmds {
		if strings.HasPrefix(strings.ToLower(c.Name), prefix) {
			out = append(out, c)
			continue
		}
		for _, a := range c.Aliases {
			if strings.HasPrefix(strings.ToLower(a), prefix) {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

// HelpLines renders the §9.7 command list straight from the table: one line
// per command, Usage() padded to a common column so every description lines
// up regardless of how long the busiest command's usage string is.
func (r Registry) HelpLines() []string {
	width := 0
	for _, c := range r.cmds {
		if w := len(c.Usage()); w > width {
			width = w
		}
	}
	lines := make([]string, len(r.cmds))
	for i, c := range r.cmds {
		lines[i] = fmt.Sprintf("%-*s  %s", width, c.Usage(), c.Describe)
	}
	return lines
}

// ParsedInput is what Parse extracts from one line of slash-command syntax.
type ParsedInput struct {
	// Command is the resolved command; only meaningful when Found is true.
	Command Command
	// Args is everything after the command name, with leading/trailing
	// whitespace trimmed.
	Args string
	// Found is false when the input starts with "/" but names no known
	// command. Raw still carries what the user typed, so the caller can
	// report it without re-deriving it from the original text.
	Found bool
	// Raw is the word typed after "/", before resolution against the table.
	Raw string
}

// IsCommand reports whether text is slash-command syntax at all: a "/" as
// the very first rune of the line. A "/" anywhere else (a URL, a path) is
// ordinary chat text and must never reach Parse.
func IsCommand(text string) bool { return strings.HasPrefix(text, "/") }

// Parse splits one line of slash-command syntax into a name and its
// argument text, then resolves the name against r. Callers are expected to
// have already checked IsCommand — Parse does not validate the leading "/"
// itself, it only strips one if present.
func Parse(text string, r Registry) ParsedInput {
	body := strings.TrimPrefix(text, "/")
	name, args, _ := strings.Cut(body, " ")
	return ParsedInput{
		Command: firstOrZero(r, name),
		Args:    strings.TrimSpace(args),
		Found:   lookupOK(r, name),
		Raw:     name,
	}
}

func firstOrZero(r Registry, name string) Command {
	c, _ := r.Lookup(name)
	return c
}

func lookupOK(r Registry, name string) bool {
	_, ok := r.Lookup(name)
	return ok
}

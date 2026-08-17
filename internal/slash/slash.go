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
	// KindSkills lists the rung-0 prose capabilities Discover found at
	// startup (§19.2/§19.4, Step 19): name and description only, the same
	// progressive-disclosure listing internal/skills.Summary already put in
	// the system prompt — a body never loads here, exactly as the model
	// itself only ever sees a body once it calls read_file on a skill's
	// own File. Read-only, like KindModels: there is no "load skill" Kind,
	// for the same reason skills.go's own package comment gives for not
	// having a second tool that does what read_file already does.
	KindSkills
	// KindLogin opens the §9.6/Step 24 in-session OAuth device-flow wizard
	// (ModeLogin, internal/tui/login.go): the same three-step dance
	// `ishakat login <provider>` already drives from the terminal
	// (RequestDeviceCode → show code/URL → PollForToken → verify → save),
	// now reachable without leaving the running TUI. An argument names the
	// provider preset (e.g. "/login openai"); no argument reports the
	// same usage line the CLI's own `ishakat login` (no args) does.
	KindLogin
	// KindTheme switches the live TUI theme (§8/§11 Fase 3, first
	// increment): no argument lists the themes available (embedded default
	// plus anything found under xdg.ThemesDir()); a name argument that
	// resolves via theme.Load applies it immediately (Root.styles is
	// rebuilt from the new Theme) and persists the choice to [ui].theme so
	// it survives a restart. This is the runner /theme's row in Commands
	// used to lack — §9.7's wireframe and keys.go's ThemePicker ("ctrl+t")
	// both already reserved the slot; this Kind is what finally answers it.
	KindTheme
	// KindConfig implements /config (§13, Step 18's own left-over scope):
	// the effective configuration, secrets redacted — the in-session
	// counterpart to `ishakat config check` (unimplementedNotice's own
	// former stand-in for this row), and the runner that finally gives
	// internal/config.Redacted()/Mask() a real caller (docs/PLAN.md's
	// Phase 4 paragraph flagged both as tested-but-dead code). Read-only,
	// like KindModels/KindSkills: there is no "edit config" Kind, since
	// changing config.toml is a filesystem write this package never makes
	// (§6.1) — `ishakat config init`/a text editor remain how it is
	// actually changed.
	KindConfig
	// KindDebug implements /debug (§13, Step 18's other left-over half,
	// closed alongside /config's own §17 2026-08-13 entry): a local-only
	// diagnostic snapshot — version, platform, cgo/termux, config paths,
	// AGENTS.md layers, the terminal's own already-resolved color/glyph
	// decision — the in-session counterpart to `ishakat doctor`'s
	// non-network half. Deliberately does not repeat doctor's DNS/HTTPS
	// probes: those need either a live netfix.Install() re-run (real I/O,
	// unsafe to block Update on) or net/http itself, so `ishakat doctor`
	// remains the answer for "is the network actually reachable",
	// pointed at explicitly, the same "here is the remedy" honesty
	// KindConfig's own docstring already establishes for its own gaps.
	KindDebug
	// KindTools implements /tools (§13, Step 20's own left-over UI half):
	// a listing of every layer-2 (declarative/script) tool that has been
	// created, with its lifecycle state, danger tier and usage stats — the
	// in-session counterpart to tool_list's own LLM-facing meta-tool,
	// drawn as structured rows instead of the single text blob a model
	// reads. No argument lists every tool; an argument is a tool's own
	// name, showing its manifest in full ("/tools code <name>", §13's own
	// second row for this step). Read-only, like KindModels/KindSkills/
	// KindConfig: there is no "edit"/"create"/"delete" Kind here — §19.6's
	// governance gates (Step 21's remaining `/tools audit`, `/tools
	// create`, `/tools edit`, `/tools delete`, `/tools revive` rows) are a
	// separate, larger increment, deliberately not folded into this one.
	KindTools
	// KindPermissions implements /permissions (§13, §21.14's own Step 32
	// closing criterion: "/permissions lists rules and invariants";
	// §21.16 decision 4: "/permissions is the whole interface for
	// reading and changing autonomy"). No argument lists §21.4's layers
	// 1 (invariants), 3 (autonomy), 4 (mission) and 5 (the currently
	// chosen bash scope) — layer 2 (trust) stays /trust's own concern,
	// not this command's. "autonomy <level>" (level one of auto/agile/
	// readonly) applies and persists a new layer-3 autonomy exactly as
	// /trust's own dialog does. A recently-denied list is a completed
	// increment (Step 32 part 7, PermissionsSnapshot.RecentDenials).
	KindPermissions
	// KindTrust implements /trust (§13, §21.4 layer 2's own row: "review
	// or change the project's trust decision"). Step 30 shipped the
	// dialog itself (trust.go's ModeTrust) and NewRoot's own one-time
	// automatic opening of it on a project's first run with no saved
	// decision — but, before this Kind existed, there was no way to
	// deliberately revisit that choice on a *later* run without deleting
	// trust.json by hand outside the program entirely. No argument
	// reopens the identical ModeTrust overlay a first run would have
	// shown (same trustOptions, same Esc-defaults-to-agile rule, same
	// git line), over Root's own already-detected git facts rather than
	// re-probing the filesystem a second time.
	KindTrust
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
	{Name: "skills", Describe: "capacidades cargadas", Kind: KindSkills},
	{Name: "theme", ArgHint: "[nombre]", Describe: "cambiar tema", Kind: KindTheme},
	{Name: "compact", Describe: "resumir contexto", Kind: KindCompact},
	{Name: "new", Describe: "conversacion nueva", Kind: KindNew},
	{Name: "resume", Describe: "reabrir una sesion", Kind: KindResume},
	{Name: "clear", Describe: "limpiar pantalla", Kind: KindClear},
	{Name: "copy", ArgHint: "[n]", Describe: "copiar respuesta", Kind: KindCopy},
	{Name: "retry", Describe: "reintentar ultimo", Kind: KindRetry},
	{Name: "stats", Describe: "tokens y costo", Kind: KindStats},
	{Name: "config", Describe: "config efectiva", Kind: KindConfig},
	{Name: "debug", Describe: "diagnostico", Kind: KindDebug},
	{Name: "tools", ArgHint: "[nombre]", Describe: "herramientas creadas", Kind: KindTools},
	{Name: "permissions", Describe: "autonomia y reglas", Kind: KindPermissions},
	{Name: "trust", Describe: "revisar confianza", Kind: KindTrust},
	{Name: "login", ArgHint: "[prov]", Describe: "autenticar via OAuth", Kind: KindLogin},
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

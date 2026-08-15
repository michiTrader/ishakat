// toolapprove.go implements Step 16's closing item (docs/PLAN.md): the
// overlay ModeToolApprove draws when a tool call reaches an "ask" tier
// mid-turn and internal/permissions.Guard.Authorize needs a human answer
// before the agent loop can continue. Like confirm.go it is a value type —
// every method takes a toolApproveDialog and returns the next one — and it
// never talks to the Guard itself: toolreview.go's bridge is what receives
// the permissions.Request from inside RunAgentTurn's goroutine and hands it
// here (via ToolApproveRequestMsg) to render and to walk with the keyboard;
// this file only ever produces a permissions.Decision, sent back down the
// reply channel the bridge is blocked on.
//
// As of step 27 (docs/PLAN.md §21.7/§21.14), the dialog's own selection
// state is an internal/ask.State walking a single-question internal/ask.Form
// instead of a hand-rolled index — "toolapprove reimplemented on top [of
// internal/ask] with no behaviour change" is that step's own closing
// criterion. The rows offered, their order, the wrap-around on up/down,
// and the exact permissions.Decision each row sends are all unchanged; only
// where the cursor and the option list live has moved. A single-question
// Form draws no tab bar (ask.State.Render's own rule — nothing to tab
// between), which is what keeps this reimplementation's rendering
// pixel-for-pixel identical to before.
package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/ask"
	"github.com/MichiTrader/ishakat/internal/permissions"
)

// toolApproveOption is one selectable row of the dialog. decision is what
// resolveToolApprove sends down reply when this row is chosen; allow=false,
// allowSession=false is "deny" (permissions.Guard.Authorize's own default
// for decision.Allow == false). It mirrors ask.Option.Label but keeps its
// own permissions.Decision alongside it, since ask.Option.Value is a plain
// string this package alone knows how to interpret (see
// newToolApproveDialog's own value constants below) — internal/ask itself
// stays ignorant of what "once"/"session"/"deny" mean, exactly as §6.1
// requires of a presentation-free primitive.
type toolApproveOption struct {
	decision permissions.Decision
	label    string
}

// The three option values newToolApproveDialog ever puts into its
// single-question ask.Form. Opaque to internal/ask itself — see
// toolApproveOption's own doc comment.
const (
	toolApproveOnce    = "once"
	toolApproveSession = "session"
	toolApproveDeny    = "deny"
)

// toolApproveDialog is ModeToolApprove's own state, live only while mode ==
// ModeToolApprove. state is an internal/ask.State walking a single-question
// Form built from options below; options keeps this dialog's own
// permissions.Decision for each row, in the exact same order as
// state.Form().Questions[0].Options, so selected() can look one up by the
// other's current cursor.
type toolApproveDialog struct {
	req     permissions.Request
	reply   chan<- permissions.Decision
	state   ask.State
	options []toolApproveOption
}

// newToolApproveDialog builds the dialog's rows from req.Tier: a High-tier
// request never offers "allow for this session" — §19.5's own rule, already
// enforced independently inside Guard.Authorize
// ("decision.AllowSession && req.Tier == Sensitive"), repeated here so the
// dialog never even shows a choice the guard would silently ignore anyway.
// The middle "allow for session" row is present for Sensitive only,
// matching exactly the one case Guard.Authorize's own decision.AllowSession
// check can act on.
func newToolApproveDialog(req permissions.Request, reply chan<- permissions.Decision) toolApproveDialog {
	var askOptions []ask.Option
	var options []toolApproveOption

	add := func(value, label string, decision permissions.Decision) {
		askOptions = append(askOptions, ask.Option{Label: label, Value: value})
		options = append(options, toolApproveOption{decision: decision, label: label})
	}
	add(toolApproveOnce, "permitir una vez", permissions.Decision{Allow: true})
	if req.Tier == permissions.Sensitive {
		add(toolApproveSession, "permitir para esta sesión", permissions.Decision{Allow: true, AllowSession: true})
	}
	add(toolApproveDeny, "denegar", permissions.Decision{Allow: false})

	form := ask.Form{Questions: []ask.Question{{ID: "approve", Options: askOptions}}}
	return toolApproveDialog{req: req, reply: reply, state: ask.NewState(form), options: options}
}

// moveSel moves the selection by delta rows, delegating the actual
// wrap-around to ask.State.MoveOption — the same wrap-around
// Picker.moveSel and confirmDialog.moveSel apply by hand, now shared
// through the primitive instead of reimplemented a third time.
func (d toolApproveDialog) moveSel(delta int) toolApproveDialog {
	d.state = d.state.MoveOption(delta)
	return d
}

// sel is the option index currently highlighted, read from the underlying
// ask.State's own cursor. Kept as a method (rather than reintroducing a
// field the Root package's tests and renderToolApprove can read directly)
// since ask.State.Cursor is the one source of truth for it now.
func (d toolApproveDialog) sel() int { return d.state.Cursor() }

// selected is the option under the cursor. newToolApproveDialog always
// returns at least two rows (allow-once, deny), so there is no empty case
// to guard here, the same reasoning confirmDialog.selected already applies.
func (d toolApproveDialog) selected() toolApproveOption { return d.options[d.sel()] }

// updateToolApprove handles every key while mode == ModeToolApprove. Like
// updateConfirm it owns the keyboard outright — there is no textarea
// underneath it to fall through to. Unlike updateConfirm, esc/Cancel does
// not merely close an overlay with nothing else waiting on it: the agent
// loop is blocked on d.reply inside RunAgentTurn's own goroutine, so
// cancelling still has to answer with an explicit deny (never leaving the
// bridge, and the tool call, hanging forever).
func (m Root) updateToolApprove(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	switch keyPressString(key) {
	case m.keys.Cancel:
		return m.resolveToolApproveWith(permissions.Decision{Allow: false})
	case "up":
		m.toolApprove = m.toolApprove.moveSel(-1)
		return m, nil
	case "down":
		m.toolApprove = m.toolApprove.moveSel(1)
		return m, nil
	case m.keys.Submit:
		return m.resolveToolApprove()
	}
	return m, nil
}

// resolveToolApprove sends whichever row was selected down the reply
// channel and returns to ModeBusy: the turn itself is not over, only the
// approval pause is — the agent loop's goroutine (blocked in
// permissions.Guard.Authorize's call to Review) resumes the instant this
// send lands, and the turn's own streamTickMsg-equivalent (see
// toolreview.go's agentTurnCmd) keeps running exactly as it was before this
// overlay opened.
func (m Root) resolveToolApprove() (tea.Model, tea.Cmd) {
	return m.resolveToolApproveWith(m.toolApprove.selected().decision)
}

// resolveToolApproveWith is resolveToolApprove's and updateToolApprove's
// (esc/Cancel) shared tail: reply is sent exactly once per dialog — the
// bridge's Review call only ever waits on one receive — and the channel
// field is cleared alongside the rest of the dialog's state so a stray
// message arriving after this dialog has already closed (there is none in
// practice, since nothing re-sends ToolApproveRequestMsg for the same
// request) cannot double-send on it.
func (m Root) resolveToolApproveWith(decision permissions.Decision) (tea.Model, tea.Cmd) {
	if m.toolApprove.reply != nil {
		m.toolApprove.reply <- decision
	}
	m.toolApprove = toolApproveDialog{}
	m.mode = ModeBusy
	return m, nil
}

// renderToolApprove draws the approval overlay. Like renderConfirm it
// replaces the whole live region and draws no box border — glyphs.go's
// table has none, and this screen is exactly the "single screen" case its
// own comment warns against inventing one for.
func (m Root) renderToolApprove() string {
	g := m.lay.glyphs()
	width := m.lay.ContentWidth()
	d := m.toolApprove

	var b strings.Builder
	b.WriteString(" aprobación de herramienta\n")
	fmt.Fprintf(&b, " %s   %s\n", d.req.Name, tierLabel(d.req.Tier))
	b.WriteString(" " + strings.Repeat(g.rule, width-1) + "\n")
	argLines, structured := renderManifestProvenance(d.req.Name, d.req.Arguments, width-1)
	if !structured {
		argLines = wrapArgsLines(d.req.Arguments, width-1)
	}
	for _, line := range argLines {
		b.WriteString(" " + line + "\n")
	}
	b.WriteString(" " + strings.Repeat(g.rule, width-1) + "\n")

	for i, opt := range d.options {
		pointer := " "
		if i == d.sel() {
			pointer = g.inputPrefix
		}
		line := pointer + " " + opt.label
		if i == d.sel() {
			line = m.styles.Accent.Render(line)
		}
		b.WriteString(" " + line + "\n")
	}

	b.WriteString(" " + strings.Repeat(g.rule, width-1) + "\n")
	fmt.Fprintf(&b, " %s move  enter elegir  esc denegar\n", g.scrollHint)
	return b.String()
}

// tierLabel names a permissions.Tier in the same short Spanish prose the
// rest of this dialog's copy uses. It is written as its own function
// rather than a Stringer on permissions.Tier itself, since that package
// stays presentation-free (§6.1: internal/tools must never import
// internal/tui, and by the same reasoning internal/permissions has no
// reason to know how a tier is displayed).
func tierLabel(t permissions.Tier) string {
	switch t {
	case permissions.Safe:
		return "riesgo bajo"
	case permissions.Controlled:
		return "riesgo controlado"
	case permissions.Sensitive:
		return "riesgo medio"
	case permissions.Critical:
		return "riesgo alto"
	default:
		return "riesgo desconocido"
	}
}

// wrapArgsLines renders req.Arguments (raw JSON) as wrapped text lines. A
// tool call's arguments are already JSON — re-marshalling with indentation
// makes the overlay's most important line (what is this tool about to do)
// readable instead of one dense, unwrapped object literal; invalid JSON
// (should not happen — Guard.hardDeny already rejects it before a Reviewer
// is ever consulted) falls back to the raw bytes rather than hiding them.
func wrapArgsLines(args json.RawMessage, width int) []string {
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, args, "", "  "); err != nil {
		return strings.Split(wrapText(string(args), width), "\n")
	}
	return strings.Split(wrapText(pretty.String(), width), "\n")
}

// toolApproveManifestArgs mirrors the JSON shape of tool_create's
// arguments (internal/tools.toolCreateArgs, unexported) closely enough to
// render §19.6 gate 2's own requirement -- "full manifest + code +
// provenance", "always; not delegable to allow for session" -- as labeled
// fields instead of the one undifferentiated JSON dump wrapArgsLines
// produces for every other tool. This is the gap more than one §17
// Bitácora entry named explicitly: "the interactive approval surface
// still shows whatever generic dialog Step 16 built, not a self-
// extension-aware one".
//
// A second, tui-local copy of these field names -- rather than importing
// tools.toolCreateArgs directly -- is the deliberate cost of §6.1's
// boundary: internal/tools must never import internal/tui
// (TestToolsNoImportaTUI), and root.go's own agentOpts comment already
// commits this package to not reaching into internal/tools either, so
// Request.Arguments's raw JSON is the only channel provenance can travel
// through. Only the fields this dialog actually renders are mirrored --
// params, selftest_*, and the profitability estimates gate 1 already
// consumed before this dialog was ever reached are deliberately left out;
// what a human needs to decide "does this deserve to exist on my disk" is
// what it calls, where it came from, and why, not every knob.
type toolApproveManifestArgs struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Method      string   `json:"method"`
	URL         string   `json:"url"`
	Origin      string   `json:"origin"`
	Reason      string   `json:"reason"`
	Sources     []string `json:"sources"`
	SessionID   string   `json:"session_id"`
	Repetitions int      `json:"repetitions"`
}

// toolApproveEditArgs mirrors tool_edit's argument shape
// (internal/tools.toolEditArgs) for the same reason and under the same
// §6.1 constraint as toolApproveManifestArgs above -- an edit changes an
// already-installed tool's request just as easily as a creation acquires
// a new one (tool_edit.go's own Danger() comment), so the human deciding
// whether to approve it needs to see the exact before/after text, not a
// JSON object naming fields called old_string and new_string.
type toolApproveEditArgs struct {
	Name       string `json:"name"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all"`
}

// renderManifestProvenance renders req.Name/req.Arguments as a structured,
// labeled view for the two meta-tools that write executable capability to
// disk (tool_create, tool_edit) -- every other tool name falls through
// unchanged to wrapArgsLines's generic JSON dump, which is exactly right
// for them: read_file's path or bash's command needs no provenance
// section, only tool_create/tool_edit's own write path does (§19.8
// mitigation 1/2). ok is false whenever this dialog should not attempt the
// structured view -- either the tool is not one of the two, or args fails
// to decode into the expected shape, which should not happen (these are
// the same arguments tool_create.go/tool_edit.go already validated before
// Guard.Authorize was ever reached) but a rendering path degrades to the
// generic dump rather than showing a blank, zero-valued manifest.
func renderManifestProvenance(name string, args json.RawMessage, width int) (lines []string, ok bool) {
	switch name {
	case "tool_create":
		var a toolApproveManifestArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, false
		}
		return renderToolCreateManifest(a, width), true
	case "tool_edit":
		var a toolApproveEditArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, false
		}
		return renderToolEditManifest(a, width), true
	default:
		return nil, false
	}
}

// renderToolCreateManifest lays out a's fields under three headings --
// what it is, what it calls, and why it exists -- so the three questions
// gate 2 exists to answer (what does this do, where does my data go, is
// the stated reason believable) each have their own line rather than
// sharing one undifferentiated block.
func renderToolCreateManifest(a toolApproveManifestArgs, width int) []string {
	var lines []string
	add := func(s string) {
		lines = append(lines, strings.Split(wrapText(s, width), "\n")...)
	}

	add(fmt.Sprintf("crear tool: %s", a.Name))
	if a.Description != "" {
		add("  " + a.Description)
	}
	add("")
	add("solicitud")
	method := a.Method
	if method == "" {
		method = "?"
	}
	add(fmt.Sprintf("  %s %s", method, a.URL))
	add("")
	add("procedencia")
	add(fmt.Sprintf("  origen: %s", originLabel(a.Origin)))
	if a.Reason != "" {
		add(fmt.Sprintf("  motivo: %s", a.Reason))
	}
	if a.Origin == "agent" && a.Repetitions > 0 {
		add(fmt.Sprintf("  repeticiones observadas: %d", a.Repetitions))
	}
	if len(a.Sources) > 0 {
		add(fmt.Sprintf("  fuentes: %s", strings.Join(a.Sources, ", ")))
	} else {
		add("  fuentes: (ninguna declarada)")
	}
	if a.SessionID != "" {
		add(fmt.Sprintf("  sesión: %s", a.SessionID))
	}
	return lines
}

// renderToolEditManifest lays out a's exact-string patch as a before/after
// pair -- the same "what does this actually change" question gate 2 asks
// of a creation, applied to a tool that already exists.
func renderToolEditManifest(a toolApproveEditArgs, width int) []string {
	var lines []string
	add := func(s string) {
		lines = append(lines, strings.Split(wrapText(s, width), "\n")...)
	}
	add(fmt.Sprintf("editar tool: %s", a.Name))
	add("")
	add("reemplaza")
	add("  " + a.OldString)
	add("")
	add("por")
	add("  " + a.NewString)
	if a.ReplaceAll {
		add("")
		add("  (todas las ocurrencias)")
	}
	return lines
}

// originLabel names a tool_create manifest's [origin].created_by value in
// the same short Spanish prose the rest of this dialog's copy uses,
// mirroring §19.6's own "three legitimate origins" table (agent-initiated,
// user-declared, user-forced) rather than showing the raw enum string a
// human has not necessarily read docs/PLAN.md to recognise.
func originLabel(origin string) string {
	switch origin {
	case "agent":
		return "el agente (detectó repetición)"
	case "user_declared":
		return "vos (flujo declarado)"
	case "user_forced":
		return "vos (forzado)"
	case "":
		return "(no especificado)"
	default:
		return origin
	}
}

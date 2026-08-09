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
package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/permissions"
)

// toolApproveOption is one selectable row of the dialog. decision is what
// resolveToolApprove sends down reply when this row is chosen; allow=false,
// allowSession=false is "deny" (permissions.Guard.Authorize's own default
// for decision.Allow == false).
type toolApproveOption struct {
	decision permissions.Decision
	label    string
}

// toolApproveDialog is ModeToolApprove's own state, live only while mode ==
// ModeToolApprove.
type toolApproveDialog struct {
	req     permissions.Request
	reply   chan<- permissions.Decision
	options []toolApproveOption
	sel     int
}

// newToolApproveDialog builds the dialog's rows from req.Tier: a High-tier
// request never offers "allow for this session" — §19.5's own rule, already
// enforced independently inside Guard.Authorize
// ("decision.AllowSession && req.Tier == Medium"), repeated here so the
// dialog never even shows a choice the guard would silently ignore anyway.
// The middle "allow for session" row is present for Medium only, matching
// exactly the one case Guard.Authorize's own decision.AllowSession check
// can act on.
func newToolApproveDialog(req permissions.Request, reply chan<- permissions.Decision) toolApproveDialog {
	options := []toolApproveOption{
		{decision: permissions.Decision{Allow: true}, label: "permitir una vez"},
	}
	if req.Tier == permissions.Medium {
		options = append(options, toolApproveOption{
			decision: permissions.Decision{Allow: true, AllowSession: true},
			label:    "permitir para esta sesión",
		})
	}
	options = append(options, toolApproveOption{
		decision: permissions.Decision{Allow: false},
		label:    "denegar",
	})
	return toolApproveDialog{req: req, reply: reply, options: options}
}

// moveSel moves the selection by delta rows, wrapping like Picker.moveSel
// and confirmDialog.moveSel.
func (d toolApproveDialog) moveSel(delta int) toolApproveDialog {
	if len(d.options) == 0 {
		return d
	}
	n := len(d.options)
	d.sel = ((d.sel+delta)%n + n) % n
	return d
}

// selected is the option under the cursor. newToolApproveDialog always
// returns at least two rows (allow-once, deny), so there is no empty case
// to guard here, the same reasoning confirmDialog.selected already applies.
func (d toolApproveDialog) selected() toolApproveOption { return d.options[d.sel] }

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
	for _, line := range wrapArgsLines(d.req.Arguments, width-1) {
		b.WriteString(" " + line + "\n")
	}
	b.WriteString(" " + strings.Repeat(g.rule, width-1) + "\n")

	for i, opt := range d.options {
		pointer := " "
		if i == d.sel {
			pointer = g.inputPrefix
		}
		line := pointer + " " + opt.label
		if i == d.sel {
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
	case permissions.Low:
		return "riesgo bajo"
	case permissions.Medium:
		return "riesgo medio"
	case permissions.High:
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

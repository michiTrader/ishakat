// askuser.go implements Step 32 part 3: the overlay ModeAskUser draws when
// the model's own ask_user tool call (internal/tools/ask_user.go) reaches
// this session's ask.Asker and needs a human answer before the agent loop
// can continue. Like toolapprove.go it is a value type — every method
// takes an askUserDialog and returns the next one — and it never talks to
// the Asker itself: internal/app's tuiAsker is what receives the ask.Form
// from inside RunAgentTurn's goroutine and hands it here (via
// AskUserRequestMsg) to render and to walk with the keyboard; this file
// only ever produces ask.Answers, sent back down the reply channel the
// bridge is blocked on.
//
// Unlike toolapprove.go's fixed, three-option, no-free-text shape, this
// dialog walks a real internal/ask.State over whatever internal/ask.Form
// the producer built — §21.7's own "one primitive, two producers" diagram
// names ask_user as the model-initiated one, and askUserArgs' own doc
// comment (internal/tools/ask_user.go) confirms it always sends exactly
// one Question today, but nothing here assumes that: MoveTab is exercised
// alongside MoveOption, so a future multi-question Form (the §21.6 mission
// survey this same primitive was generalized for) would already work
// unchanged.
package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/ask"
)

// askUserDialog is ModeAskUser's own state, live only while mode ==
// ModeAskUser. state is the internal/ask.State walking the Form the
// producer sent; text is this dialog's own free-text buffer for whichever
// question tab is currently active — internal/ask.State itself has no
// notion of "text not yet committed", only SetFreeText's already-committed
// Answer, so the in-progress buffer has to live here, the same way
// Picker.query lives in this package rather than in a shared primitive.
type askUserDialog struct {
	reply chan<- ask.Answers
	state ask.State
	text  string
}

// newAskUserDialog starts walking form at its first question, cursor 0,
// empty free-text buffer, nothing answered yet.
func newAskUserDialog(form ask.Form, reply chan<- ask.Answers) askUserDialog {
	return askUserDialog{reply: reply, state: ask.NewState(form)}
}

// moveOption delegates to ask.State.MoveOption — the option cursor's own
// wrap-around within whichever question is current.
func (d askUserDialog) moveOption(delta int) askUserDialog {
	d.state = d.state.MoveOption(delta)
	return d
}

// moveTab delegates to ask.State.MoveTab and clears the free-text buffer:
// a buffer typed for one question showing up unlabeled on the next tab
// (or on the trailing Submit tab, where it means nothing at all) would be
// confusing state to carry across a navigation the human explicitly
// asked for. confirmAskUser (below) also calls MoveTab directly, after
// already having consumed the buffer via SetFreeText — see its own
// comment for why that path does not go through this method.
func (d askUserDialog) moveTab(delta int) askUserDialog {
	d.state = d.state.MoveTab(delta)
	d.text = ""
	return d
}

// backspace removes the last rune of the free-text buffer, rune-safe like
// Picker.backspace, so an accented character disappears as one keystroke's
// worth of undo rather than a mangled trailing byte.
func (d askUserDialog) backspace() askUserDialog {
	r := []rune(d.text)
	if len(r) == 0 {
		return d
	}
	d.text = string(r[:len(r)-1])
	return d
}

// typeText appends s (one key press's worth of text — almost always one
// rune) to the free-text buffer, the identical shape Picker.typeText
// already establishes for raw keystroke capture with no textarea
// underneath.
func (d askUserDialog) typeText(s string) askUserDialog {
	d.text += s
	return d
}

// currentQuestion returns the Question the current tab points at, and
// false on the trailing Submit tab or an out-of-range tab (should not
// happen — ask.State.MoveTab's own wrap-around never produces one, but a
// bounds check here costs nothing and avoids a panic if that ever
// changed).
func (d askUserDialog) currentQuestion() (ask.Question, bool) {
	form := d.state.Form()
	tab := d.state.Tab()
	if d.state.AtSubmit() || tab < 0 || tab >= len(form.Questions) {
		return ask.Question{}, false
	}
	return form.Questions[tab], true
}

// currentAllowsFreeText reports whether typed keystrokes should accumulate
// into the free-text buffer at all — a question with AllowFreeText false
// (a fixed-menu-only question) must not let stray typing build up a
// buffer confirmAskUser would otherwise try to commit.
func (d askUserDialog) currentAllowsFreeText() bool {
	q, ok := d.currentQuestion()
	return ok && q.AllowFreeText
}

// updateAskUser handles every key while mode == ModeAskUser. Like
// updateToolApprove it owns the keyboard outright — there is no textarea
// underneath it — and Esc/Cancel does not merely close an overlay with
// nothing else waiting on it: the agent loop is blocked on d.reply inside
// AskUser.Run's own call to Ask, so cancelling still has to answer with
// something, never leaving the bridge (and the tool call) hanging
// forever. It answers with an empty ask.Answers rather than a synthetic
// "denied" value — unlike a permissions.Decision, ask.Answers has no
// allow/deny axis at all (§19.1's own "ask_user executes nothing, and is
// never denyable"); AskUser.Run's own unanswered-question branch already
// reports "(the human gave no answer)" as the tool's result for exactly
// this shape.
func (m Root) updateAskUser(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	switch keyPressString(key) {
	case m.keys.Cancel:
		return m.resolveAskUserWith(ask.Answers{})
	case "up":
		m.askUser = m.askUser.moveOption(-1)
		return m, nil
	case "down":
		m.askUser = m.askUser.moveOption(1)
		return m, nil
	case "tab":
		m.askUser = m.askUser.moveTab(1)
		return m, nil
	case "shift+tab":
		m.askUser = m.askUser.moveTab(-1)
		return m, nil
	case "backspace":
		m.askUser = m.askUser.backspace()
		return m, nil
	case m.keys.Submit:
		return m.confirmAskUser()
	default:
		if key.Text != "" && m.askUser.currentAllowsFreeText() {
			m.askUser = m.askUser.typeText(key.Text)
		}
		return m, nil
	}
}

// confirmAskUser is m.keys.Submit's own handler, context-sensitive on
// which tab is current — the one branch updateAskUser's own switch is not
// simple enough to inline. On the trailing Submit tab it finalizes the
// whole Form, exactly when ask.State.Submit itself reports Ready. On a
// question tab it commits whichever answer the human actually gave —
// the typed free-text buffer takes priority when non-empty (a human who
// both selected an option with the arrows and then typed something
// clearly means the typed answer, the same "free text is the escape
// hatch" framing askUserArgs' own doc comment gives it), falling back to
// the highlighted option otherwise — then advances one tab automatically,
// the same "answer, then move on" flow ask.State.MoveTab's own doc
// comment describes a multi-question door as expected to drive. A
// question with neither a typed buffer nor a selected option (no options
// declared, AllowFreeText false, nothing typed yet — should not happen
// for a real ask_user call, since askUserArgs' own Run forces
// AllowFreeText true whenever Options is empty) is a no-op: there is
// nothing yet to commit.
func (m Root) confirmAskUser() (tea.Model, tea.Cmd) {
	d := m.askUser
	if d.state.AtSubmit() {
		if answers, ok := d.state.Submit(); ok {
			return m.resolveAskUserWith(answers)
		}
		return m, nil
	}

	switch {
	case d.text != "":
		d.state = d.state.SetFreeText(d.text)
	default:
		if _, ok := d.state.Selected(); !ok {
			return m, nil
		}
		d.state = d.state.Choose()
	}
	d.text = ""
	d.state = d.state.MoveTab(1)
	m.askUser = d
	return m, nil
}

// resolveAskUserWith sends answers down the reply channel and returns to
// ModeBusy: the turn itself is not over, only the pause is — the agent
// loop's goroutine (blocked in internal/app's tuiAsker.Ask, itself blocked
// on AwaitReply's own select) resumes the instant this send lands, the
// identical "turn not over" contract resolveToolApproveWith already
// documents for permissions.Decision.
func (m Root) resolveAskUserWith(answers ask.Answers) (tea.Model, tea.Cmd) {
	if m.askUser.reply != nil {
		m.askUser.reply <- answers
	}
	m.askUser = askUserDialog{}
	m.mode = ModeBusy
	return m, nil
}

// renderAskUser draws the ask_user overlay. Like renderToolApprove it
// replaces the whole live region and draws no box border. The trailing
// Submit tab is rendered through ask.State.Render itself — the
// primitive's own plain-text reference view — rather than reimplemented
// here a second time: unlike a question's own options (which this
// function highlights with m.styles.Accent, something the
// presentation-free primitive cannot do), the Submit tab's "review your
// answers" summary needs to read each question's already-committed
// Answer, and internal/ask.Answers is unexported field access State's own
// Render method already has and this package deliberately does not
// duplicate (§6.1: the primitive owns its own private state).
func (m Root) renderAskUser() string {
	g := m.lay.glyphs()
	width := m.lay.ContentWidth()
	d := m.askUser
	form := d.state.Form()

	var b strings.Builder
	b.WriteString(" pregunta del agente\n")
	b.WriteString(" " + strings.Repeat(g.rule, width-1) + "\n")

	// The Submit tab's own summary is drawn through ask.State.Render
	// itself, which already includes its own tab bar when there is more
	// than one question — see this function's own doc comment for why
	// that one case is not reimplemented here — so askUserTabBar is drawn
	// separately only for a question tab, never doubled on top of
	// Render's own.
	if !d.state.AtSubmit() && len(form.Questions) > 1 {
		for _, line := range strings.Split(wrapText(askUserTabBar(d.state), width-1), "\n") {
			b.WriteString(" " + line + "\n")
		}
		b.WriteString(" " + strings.Repeat(g.rule, width-1) + "\n")
	}

	if d.state.AtSubmit() {
		for _, line := range d.state.Render(width - 1) {
			b.WriteString(" " + line + "\n")
		}
	} else {
		q, _ := d.currentQuestion()
		for _, line := range strings.Split(wrapText(q.Prompt, width-1), "\n") {
			b.WriteString(" " + line + "\n")
		}
		if len(q.Options) > 0 {
			b.WriteString("\n")
			for i, opt := range q.Options {
				pointer := "  "
				if i == d.state.Cursor() {
					pointer = g.inputPrefix + " "
				}
				line := pointer + opt.Label
				if i == d.state.Cursor() {
					line = m.styles.Accent.Render(line)
				}
				b.WriteString(" " + line + "\n")
			}
		}
		if q.AllowFreeText {
			b.WriteString("\n")
			fmt.Fprintf(&b, " %s %s%s\n", g.inputPrefix, d.text, g.streamCursor)
		}
	}

	b.WriteString(" " + strings.Repeat(g.rule, width-1) + "\n")
	fmt.Fprintf(&b, " %s mover  enter confirmar  tab siguiente  esc omitir\n", g.scrollHint)
	return b.String()
}

// askUserTabBar draws one line per question ("[x] " when answered, "[ ]"
// otherwise, "[>]" for whichever tab is current) plus the trailing Submit
// tab, the themed sibling of ask.State's own unexported renderTabBar —
// duplicated here (rather than reused) for the same reason
// renderAskUser's own doc comment gives for the Submit-tab summary: this
// package draws its own accent-colored, glyph-table-aware view, and a tab
// bar (unlike the Submit summary) needs no access to any Answer's actual
// content, only IsAnswered's boolean and Tab()'s current index — both
// already exported.
func askUserTabBar(s ask.State) string {
	form := s.Form()
	parts := make([]string, 0, len(form.Questions)+1)
	for i, q := range form.Questions {
		mark := "[ ]"
		if s.IsAnswered(q.ID) {
			mark = "[x]"
		}
		if i == s.Tab() {
			mark = "[>]"
		}
		label := q.Prompt
		if label == "" {
			label = q.ID
		}
		parts = append(parts, mark+" "+label)
	}
	submit := "enviar"
	if s.AtSubmit() {
		submit = "[>] " + submit
	}
	parts = append(parts, submit)
	return strings.Join(parts, "  ")
}

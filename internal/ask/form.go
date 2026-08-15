// form.go is §21.7's "tab bar on top" shape, generalized from
// internal/tui/toolapprove.go's own single-question dialog: State is the
// value-type walker every door drives with the same handful of verbs
// (MoveTab, MoveOption, Choose, Submit) regardless of how many Questions a
// Form carries. A tool-approval Form has exactly one Question and no tab
// bar is drawn for it (see Render's own comment) — that is what keeps this
// generalization behaviour-identical to the dialog it replaces rather than
// growing a UI element nothing asked for.
package ask

import "strings"

// State is one Form's live progress: which tab (question, or the trailing
// Submit tab) is current, which option is highlighted within each
// question, and which answers have actually been committed so far. It is
// a value type, like toolApproveDialog and confirmDialog already are in
// internal/tui — every method takes a State and returns the next one, so a
// door's own Update loop can keep treating "the current dialog state" as
// one immutable value without this package knowing anything about Bubble
// Tea, WebSockets, or any other transport.
type State struct {
	form    Form
	answers Answers
	tab     int
	cursor  []int
}

// NewState starts walking form at its first question, cursor 0, nothing
// answered yet.
func NewState(form Form) State {
	return State{form: form, answers: Answers{}, cursor: make([]int, len(form.Questions))}
}

// Form returns the Form this State is walking.
func (s State) Form() Form { return s.form }

// Tab returns the current tab index: 0..len(Questions)-1 selects a
// question, and len(Questions) is the trailing Submit tab.
func (s State) Tab() int { return s.tab }

// AtSubmit reports whether the current tab is the trailing Submit tab
// rather than a question.
func (s State) AtSubmit() bool { return s.tab == len(s.form.Questions) }

// MoveTab moves the current tab by delta, wrapping through every question
// plus the trailing Submit tab — §21.7's "Tab/<-> to move between
// questions" — the same wrap-around moveSel already gives a single
// question's own option list. A Form with a single Question (tool
// approval's own shape) has exactly two tabs: that one question, and
// Submit; a door that never calls MoveTab (because it only ever renders
// one question and finalizes on Choose, as toolapprove.go's reimplementation
// does) simply never visits the second one, which is what keeps a
// single-question Form's observable behaviour identical to before this
// package existed.
func (s State) MoveTab(delta int) State {
	n := len(s.form.Questions) + 1
	if n <= 1 {
		return s
	}
	s.tab = ((s.tab+delta)%n + n) % n
	return s
}

// currentQuestion returns the Question the current tab points at, and
// false when the current tab is the trailing Submit tab or the Form has
// no questions at all.
func (s State) currentQuestion() (Question, bool) {
	if s.AtSubmit() || s.tab >= len(s.form.Questions) {
		return Question{}, false
	}
	return s.form.Questions[s.tab], true
}

// MoveOption moves the option cursor within the current question by
// delta, wrapping the same way toolApproveDialog.moveSel and
// confirmDialog.moveSel already do. A no-op on the Submit tab, on a
// question with no Options (a pure free-text question), or on a Form with
// no questions.
func (s State) MoveOption(delta int) State {
	q, ok := s.currentQuestion()
	if !ok || len(q.Options) == 0 {
		return s
	}
	n := len(q.Options)
	cursor := append([]int(nil), s.cursor...)
	cursor[s.tab] = ((cursor[s.tab]+delta)%n + n) % n
	s.cursor = cursor
	return s
}

// Cursor returns the option index currently highlighted within the
// current question, or 0 when the current tab has no cursor of its own
// (the Submit tab, or a question past the cursor slice's length).
func (s State) Cursor() int {
	if s.tab >= len(s.cursor) {
		return 0
	}
	return s.cursor[s.tab]
}

// Selected returns the Option currently highlighted in the current
// question. ok is false on the Submit tab or on a question with no
// Options — exactly the two cases a caller must not treat the zero Option
// as a real answer.
func (s State) Selected() (opt Option, ok bool) {
	q, ok := s.currentQuestion()
	if !ok || len(q.Options) == 0 {
		return Option{}, false
	}
	return q.Options[s.Cursor()], true
}

// Choose commits the currently highlighted option as the current
// question's Answer. A no-op on the Submit tab or on a question with no
// Options — the caller decides separately (via SetFreeText) how a
// free-text-only question gets answered.
func (s State) Choose() State {
	q, ok := s.currentQuestion()
	if !ok {
		return s
	}
	opt, hasOpt := s.Selected()
	if !hasOpt {
		return s
	}
	answers := cloneAnswers(s.answers)
	answers[q.ID] = Answer{Value: opt.Value}
	s.answers = answers
	return s
}

// SetFreeText commits text as the current question's free-text Answer —
// the "free-text option on every question" (§21.7) for a question that
// either has AllowFreeText set alongside Options, or (a pure survey
// question) has no Options at all. A no-op on the Submit tab.
func (s State) SetFreeText(text string) State {
	q, ok := s.currentQuestion()
	if !ok {
		return s
	}
	answers := cloneAnswers(s.answers)
	answers[q.ID] = Answer{FreeText: text}
	s.answers = answers
	return s
}

// IsAnswered reports whether the question named id already has a
// committed Answer — the "check mark on answered tabs" (§21.7) a door's
// own tab bar renders from.
func (s State) IsAnswered(id string) bool {
	_, ok := s.answers[id]
	return ok
}

// Ready reports whether every question in the Form has a committed
// Answer — the precondition Submit checks.
func (s State) Ready() bool {
	for _, q := range s.form.Questions {
		if _, ok := s.answers[q.ID]; !ok {
			return false
		}
	}
	return true
}

// Submit returns the committed Answers when every question has one, and
// false otherwise — the Form is not finished, and the caller (the door's
// own Update loop) should keep walking it rather than treat a partial map
// as the human's real decision.
func (s State) Submit() (Answers, bool) {
	if !s.Ready() {
		return nil, false
	}
	return cloneAnswers(s.answers), true
}

func cloneAnswers(a Answers) Answers {
	out := make(Answers, len(a))
	for k, v := range a {
		out[k] = v
	}
	return out
}

// Render is the primitive's own plain-text reference rendering: no color,
// no glyphs, ASCII only — every door that wants themed output (the TUI's
// accent color on the highlighted row, its own rule/pointer glyphs) draws
// its own view from State's exported methods instead, the same way
// toolapprove.go's renderToolApprove already builds its lines from
// req/options rather than calling a shared Render. What this function
// buys is the one thing §21.7 asks be "tested once": that the tab bar,
// question, options and Submit line all fit inside width columns —
// verified directly against this function at width 40, the closing
// criterion for step 27 (docs/PLAN.md §21.14).
//
// The tab bar is only drawn for a Form with more than one Question: a
// single-question Form (tool approval's own shape) has nothing to
// tab between, and drawing a one-item bar over it would be new visual
// noise a "no behaviour change" reimplementation must not introduce.
func (s State) Render(width int) []string {
	var lines []string
	if len(s.form.Questions) > 1 {
		lines = append(lines, wrapAt(renderTabBar(s), width)...)
	}
	if q, ok := s.currentQuestion(); ok {
		lines = append(lines, wrapAt(q.Prompt, width)...)
		for i, opt := range q.Options {
			pointer := "  "
			if i == s.Cursor() {
				pointer = "> "
			}
			lines = append(lines, wrapAt(pointer+opt.Label, width)...)
		}
		if q.AllowFreeText {
			lines = append(lines, wrapAt("(free text also accepted)", width)...)
		}
	} else {
		lines = append(lines, wrapAt(renderSubmitSummary(s), width)...)
	}
	return lines
}

// renderTabBar draws one line per question ("[x] " when answered, "[ ] "
// otherwise, "[>]" for whichever tab is current) plus the trailing Submit
// tab, joined with two spaces the same way a real tab strip separates its
// labels.
func renderTabBar(s State) string {
	var parts []string
	for i, q := range s.form.Questions {
		mark := "[ ]"
		if s.IsAnswered(q.ID) {
			mark = "[x]"
		}
		if i == s.tab {
			mark = "[>]"
		}
		label := q.Prompt
		if label == "" {
			label = q.ID
		}
		parts = append(parts, mark+" "+label)
	}
	submit := "Submit"
	if s.AtSubmit() {
		submit = "[>] " + submit
	}
	parts = append(parts, submit)
	return strings.Join(parts, "  ")
}

// renderSubmitSummary lists every answered question's Answer, one per
// line, when the Submit tab is current — §21.7's "a final Submit tab that
// summarizes before sending".
func renderSubmitSummary(s State) string {
	var b strings.Builder
	b.WriteString("review your answers:")
	for _, q := range s.form.Questions {
		a, ok := s.answers[q.ID]
		b.WriteString("\n  ")
		b.WriteString(q.ID)
		b.WriteString(": ")
		switch {
		case !ok:
			b.WriteString("(unanswered)")
		case a.FreeText != "":
			b.WriteString(a.FreeText)
		default:
			b.WriteString(a.Value)
		}
	}
	return b.String()
}

// wrapAt folds text to fit within width columns, breaking on spaces where
// possible and inside a word only when a single word itself exceeds
// width. It is a minimal, dependency-free wrap — unlike
// internal/tui/wrap.go's wrapText, this package takes no dependency on
// charmbracelet/x/ansi, since it never has ANSI escapes or wide runes of
// its own to preserve: every door that needs that (the TUI) already wraps
// its own themed lines with wrapText and only reaches this function
// indirectly, through State.Render, for the plain-text reference view.
// width <= 0 means "no wrapping" — the caller has nothing meaningful to
// measure against.
func wrapAt(text string, width int) []string {
	if width <= 0 {
		return strings.Split(text, "\n")
	}
	var out []string
	for _, para := range strings.Split(text, "\n") {
		out = append(out, wrapLine(para, width)...)
	}
	return out
}

func wrapLine(line string, width int) []string {
	if len([]rune(line)) <= width {
		return []string{line}
	}
	words := strings.Fields(line)
	if len(words) == 0 {
		return []string{line}
	}
	var lines []string
	cur := ""
	for _, w := range words {
		candidate := w
		if cur != "" {
			candidate = cur + " " + w
		}
		if len([]rune(candidate)) <= width {
			cur = candidate
			continue
		}
		if cur != "" {
			lines = append(lines, cur)
			cur = ""
		}
		for len([]rune(w)) > width {
			r := []rune(w)
			lines = append(lines, string(r[:width]))
			w = string(r[width:])
		}
		cur = w
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

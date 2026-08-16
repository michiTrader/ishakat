package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MichiTrader/ishakat/internal/ask"
	"github.com/MichiTrader/ishakat/internal/theme"
)

func singleQuestionForm() ask.Form {
	return ask.Form{
		Questions: []ask.Question{{
			ID:     "answer",
			Prompt: "¿qué preferís?",
			Options: []ask.Option{
				{Label: "Café", Value: "coffee"},
				{Label: "Té", Value: "tea"},
			},
			AllowFreeText: true,
		}},
	}
}

func multiQuestionForm() ask.Form {
	return ask.Form{
		Questions: []ask.Question{
			{ID: "q1", Prompt: "¿primero?", Options: []ask.Option{{Label: "A", Value: "a"}, {Label: "B", Value: "b"}}},
			{ID: "q2", Prompt: "¿segundo?", AllowFreeText: true},
		},
	}
}

func TestNewAskUserDialogStartsAtFirstQuestion(t *testing.T) {
	reply := make(chan ask.Answers, 1)
	dialog := newAskUserDialog(singleQuestionForm(), reply)
	if dialog.state.AtSubmit() {
		t.Fatal("new dialog must not start on the Submit tab")
	}
	if dialog.state.Tab() != 0 {
		t.Fatalf("dialog tab = %d, want 0", dialog.state.Tab())
	}
	if dialog.text != "" {
		t.Fatalf("dialog text = %q, want empty", dialog.text)
	}
}

func TestUpdateAskUserCancelSendsEmptyAnswers(t *testing.T) {
	reply := make(chan ask.Answers, 1)
	dialog := newAskUserDialog(singleQuestionForm(), reply)
	root := Root{mode: ModeAskUser, keys: Map{Cancel: "esc", Submit: "enter"}, askUser: dialog}

	model, _ := root.updateAskUser(tea.KeyPressMsg{Code: tea.KeyEsc})
	got := model.(Root)
	if got.mode != ModeBusy {
		t.Fatalf("mode after cancel = %v, want ModeBusy", got.mode)
	}
	if got.askUser.reply != nil {
		t.Fatalf("askUser state was not cleared: %+v", got.askUser)
	}
	select {
	case answers := <-reply:
		if len(answers) != 0 {
			t.Fatalf("cancel answers = %+v, want empty", answers)
		}
	default:
		t.Fatal("cancel did not answer the reply channel")
	}
}

func TestUpdateAskUserSelectsOptionAndAdvancesTab(t *testing.T) {
	reply := make(chan ask.Answers, 1)
	dialog := newAskUserDialog(singleQuestionForm(), reply)
	root := Root{mode: ModeAskUser, keys: Map{Cancel: "esc", Submit: "enter"}, askUser: dialog}

	// Move down to select "Té" and confirm — this commits the option and
	// advances to the trailing Submit tab, since this Form has only one
	// question.
	model, _ := root.updateAskUser(tea.KeyPressMsg{Code: tea.KeyDown})
	root = model.(Root)
	model, _ = root.updateAskUser(tea.KeyPressMsg{Code: tea.KeyEnter})
	root = model.(Root)

	if !root.askUser.state.AtSubmit() {
		t.Fatalf("dialog after choosing an option must advance to Submit, got tab %d", root.askUser.state.Tab())
	}
	if !root.askUser.state.IsAnswered("answer") {
		t.Fatal("question must be marked answered after Choose")
	}

	// Confirm again on the Submit tab finalizes the Form.
	model, _ = root.updateAskUser(tea.KeyPressMsg{Code: tea.KeyEnter})
	got := model.(Root)
	if got.mode != ModeBusy {
		t.Fatalf("mode after submit = %v, want ModeBusy", got.mode)
	}
	select {
	case answers := <-reply:
		if answers["answer"].Value != "tea" {
			t.Fatalf("submitted answers = %+v, want answer=tea", answers)
		}
	default:
		t.Fatal("submit did not answer the reply channel")
	}
}

func TestUpdateAskUserFreeTextTakesPriorityOverSelectedOption(t *testing.T) {
	reply := make(chan ask.Answers, 1)
	dialog := newAskUserDialog(singleQuestionForm(), reply)
	root := Root{mode: ModeAskUser, keys: Map{Cancel: "esc", Submit: "enter"}, askUser: dialog}

	// Type free text without touching the option cursor, then confirm.
	model, _ := root.updateAskUser(tea.KeyPressMsg{Text: "m"})
	root = model.(Root)
	model, _ = root.updateAskUser(tea.KeyPressMsg{Text: "a"})
	root = model.(Root)
	model, _ = root.updateAskUser(tea.KeyPressMsg{Code: tea.KeyEnter})
	root = model.(Root)

	if root.askUser.text != "" {
		t.Fatalf("free-text buffer must be cleared after committing, got %q", root.askUser.text)
	}
	if !root.askUser.state.AtSubmit() {
		t.Fatal("dialog must advance to Submit after committing free text")
	}

	model, _ = root.updateAskUser(tea.KeyPressMsg{Code: tea.KeyEnter})
	_ = model.(Root)
	select {
	case answers := <-reply:
		if answers["answer"].FreeText != "ma" {
			t.Fatalf("submitted answers = %+v, want FreeText=ma", answers)
		}
	default:
		t.Fatal("submit did not answer the reply channel")
	}
}

func TestUpdateAskUserTabDoesNotCarryFreeTextAcrossQuestions(t *testing.T) {
	reply := make(chan ask.Answers, 1)
	dialog := newAskUserDialog(multiQuestionForm(), reply)
	dialog = dialog.typeText("hola")
	if dialog.text != "hola" {
		t.Fatalf("typeText did not set the buffer: %q", dialog.text)
	}
	dialog = dialog.moveTab(1)
	if dialog.text != "" {
		t.Fatalf("moveTab must clear the free-text buffer, got %q", dialog.text)
	}
	if dialog.state.Tab() != 1 {
		t.Fatalf("moveTab did not advance the tab: %d", dialog.state.Tab())
	}
}

func TestUpdateAskUserBackspaceIsRuneSafe(t *testing.T) {
	reply := make(chan ask.Answers, 1)
	dialog := newAskUserDialog(singleQuestionForm(), reply).typeText("café")
	dialog = dialog.backspace()
	if dialog.text != "caf" {
		t.Fatalf("backspace on trailing accented rune = %q, want %q", dialog.text, "caf")
	}
	empty := askUserDialog{}
	if got := empty.backspace(); got.text != "" {
		t.Fatalf("backspace on empty buffer = %q, want empty", got.text)
	}
}

// TestRenderAskUserShowsQuestionAndOptions is the integration check that
// renderAskUser -- not just the dialog's own state transitions -- actually
// draws the current question's prompt and options, the same style
// TestRenderToolApproveUsesStructuredViewForToolCreate checks for the
// tool-approval dialog.
func TestRenderAskUserShowsQuestionAndOptions(t *testing.T) {
	reply := make(chan ask.Answers, 1)
	root := Root{
		mode:    ModeAskUser,
		lay:     Layout{Width: 80, Glyphs: theme.GlyphsUnicode},
		styles:  theme.NewStyles(theme.Load(""), theme.CapNone, theme.GlyphsUnicode),
		keys:    Map{Cancel: "esc", Submit: "enter"},
		askUser: newAskUserDialog(singleQuestionForm(), reply),
	}
	out := root.renderAskUser()
	for _, want := range []string{"¿qué preferís?", "Café", "Té"} {
		if !strings.Contains(out, want) {
			t.Errorf("renderAskUser missing %q; got:\n%s", want, out)
		}
	}
}

// TestRenderAskUserShowsTabBarForMultiQuestionForms confirms the tab bar
// (askUserTabBar) is drawn only when a Form has more than one question --
// the same "no new visual noise for the single-question case" rule
// ask.State.Render's own doc comment states, mirrored here for this
// package's own themed view.
func TestRenderAskUserShowsTabBarForMultiQuestionForms(t *testing.T) {
	reply := make(chan ask.Answers, 1)
	root := Root{
		mode:    ModeAskUser,
		lay:     Layout{Width: 80, Glyphs: theme.GlyphsUnicode},
		styles:  theme.NewStyles(theme.Load(""), theme.CapNone, theme.GlyphsUnicode),
		keys:    Map{Cancel: "esc", Submit: "enter"},
		askUser: newAskUserDialog(multiQuestionForm(), reply),
	}
	out := root.renderAskUser()
	if !strings.Contains(out, "enviar") {
		t.Errorf("renderAskUser must show the tab bar (with the trailing enviar tab) for a multi-question form; got:\n%s", out)
	}
}

func TestCancelAgentTurnClearsAskUserDialog(t *testing.T) {
	reply := make(chan ask.Answers, 1)
	root := Root{mode: ModeAskUser, askUser: newAskUserDialog(singleQuestionForm(), reply)}
	model, _ := root.cancelAgentTurn()
	got := model.(Root)
	if got.askUser.reply != nil {
		t.Fatalf("cancelAgentTurn must clear askUser, got %+v", got.askUser)
	}
}

func TestOpenAskUserSwitchesModeAndStoresDialog(t *testing.T) {
	reply := make(chan ask.Answers, 1)
	root := Root{mode: ModeBusy}
	model, _ := root.openAskUser(AskUserRequestMsg{Form: singleQuestionForm(), Reply: reply})
	got := model.(Root)
	if got.mode != ModeAskUser {
		t.Fatalf("mode after openAskUser = %v, want ModeAskUser", got.mode)
	}
	if got.askUser.reply == nil {
		t.Fatal("openAskUser must store the reply channel")
	}
}

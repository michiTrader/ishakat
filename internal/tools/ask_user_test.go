package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/MichiTrader/ishakat/internal/ask"
)

type fakeAsker struct {
	answers ask.Answers
	err     error
	form    ask.Form
	calls   int
}

func (f *fakeAsker) Ask(_ context.Context, form ask.Form) (ask.Answers, error) {
	f.calls++
	f.form = form
	return f.answers, f.err
}

func TestAskUserNameDangerDescriptionParameters(t *testing.T) {
	a := AskUser{}
	if a.Name() != "ask_user" {
		t.Errorf("Name() = %q, want ask_user", a.Name())
	}
	if a.Danger() != DangerLow {
		t.Errorf("Danger() = %v, want DangerLow", a.Danger())
	}
	if a.Description() == "" {
		t.Error("Description() is empty")
	}
	var schema struct {
		Type       string `json:"type"`
		Properties struct {
			Question struct {
				Type string `json:"type"`
			} `json:"question"`
			Options struct {
				Type  string `json:"type"`
				Items struct {
					Type string `json:"type"`
				} `json:"items"`
			} `json:"options"`
			AllowFreeText struct {
				Type string `json:"type"`
			} `json:"allow_free_text"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(a.Parameters(), &schema); err != nil {
		t.Fatalf("Parameters() did not unmarshal: %v", err)
	}
	if schema.Type != "object" {
		t.Errorf("schema.Type = %q, want object", schema.Type)
	}
	if schema.Properties.Question.Type != "string" {
		t.Errorf("question property type = %q, want string", schema.Properties.Question.Type)
	}
	if schema.Properties.Options.Type != "array" || schema.Properties.Options.Items.Type != "string" {
		t.Errorf("options property = %+v, want array of string", schema.Properties.Options)
	}
	if schema.Properties.AllowFreeText.Type != "boolean" {
		t.Errorf("allow_free_text property type = %q, want boolean", schema.Properties.AllowFreeText.Type)
	}
	if len(schema.Required) != 1 || schema.Required[0] != "question" {
		t.Errorf("Required = %v, want [question]", schema.Required)
	}
}

func TestAskUserRunRejectsMissingQuestion(t *testing.T) {
	a := AskUser{}
	if _, err := a.Run(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("expected an error for a missing question")
	}
}

func TestAskUserRunRejectsBlankQuestion(t *testing.T) {
	a := AskUser{}
	if _, err := a.Run(context.Background(), json.RawMessage(`{"question":"   "}`)); err == nil {
		t.Fatal("expected an error for a blank question")
	}
}

func TestAskUserRunRejectsInvalidJSON(t *testing.T) {
	a := AskUser{}
	if _, err := a.Run(context.Background(), json.RawMessage(`not json`)); err == nil {
		t.Fatal("expected an error for invalid arguments JSON")
	}
}

// TestAskUserRunWithNilAskerIsToolErrorNotGoError mirrors
// TestDispatchRunWithNilRunnerIsToolErrorNotGoError exactly: a tool
// missing its injected capability reports that as Result.IsError data the
// model can react to, never a Go error or a panic.
func TestAskUserRunWithNilAskerIsToolErrorNotGoError(t *testing.T) {
	a := AskUser{}
	res, err := a.Run(context.Background(), json.RawMessage(`{"question":"proceed?"}`))
	if err != nil {
		t.Fatalf("unexpected Go error with a nil Asker: %v", err)
	}
	if !res.IsError {
		t.Error("expected Result.IsError=true when no Asker is configured")
	}
}

func TestAskUserRunBuildsAFormFromArgumentsAndReturnsTheChosenValue(t *testing.T) {
	fa := &fakeAsker{answers: ask.Answers{askUserQuestionID: {Value: "yes"}}}
	a := AskUser{Asker: fa}
	res, err := a.Run(context.Background(), json.RawMessage(`{"question":"proceed?","options":["yes","no"]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Errorf("unexpected IsError=true: %q", res.Text)
	}
	if res.Text != "yes" {
		t.Errorf("Text = %q, want yes", res.Text)
	}
	if fa.calls != 1 {
		t.Fatalf("Asker.Ask called %d times, want 1", fa.calls)
	}
	if len(fa.form.Questions) != 1 {
		t.Fatalf("form has %d questions, want 1", len(fa.form.Questions))
	}
	q := fa.form.Questions[0]
	if q.Prompt != "proceed?" {
		t.Errorf("Prompt = %q, want %q", q.Prompt, "proceed?")
	}
	if len(q.Options) != 2 || q.Options[0].Value != "yes" || q.Options[1].Value != "no" {
		t.Errorf("Options = %+v, want [yes no]", q.Options)
	}
}

// TestAskUserRunForcesFreeTextWhenNoOptionsGiven pins the "cannot be sure,
// do not guess" fallback ask_user.go's own doc comment states: a question
// with no fixed options must still be answerable, so AllowFreeText is
// forced on regardless of what the model passed, matching bashTier's own
// precedent for an input this package cannot classify confidently.
func TestAskUserRunForcesFreeTextWhenNoOptionsGiven(t *testing.T) {
	fa := &fakeAsker{answers: ask.Answers{askUserQuestionID: {FreeText: "in the sandbox"}}}
	a := AskUser{Asker: fa}
	res, err := a.Run(context.Background(), json.RawMessage(`{"question":"where should I write it?"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Text != "in the sandbox" {
		t.Errorf("Text = %q, want %q", res.Text, "in the sandbox")
	}
	if !fa.form.Questions[0].AllowFreeText {
		t.Error("AllowFreeText should be forced true when no options are given")
	}
}

func TestAskUserRunPrefersFreeTextOverValueWhenBothPresent(t *testing.T) {
	fa := &fakeAsker{answers: ask.Answers{askUserQuestionID: {Value: "yes", FreeText: "actually, no"}}}
	a := AskUser{Asker: fa}
	res, err := a.Run(context.Background(), json.RawMessage(`{"question":"proceed?","options":["yes","no"],"allow_free_text":true}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Text != "actually, no" {
		t.Errorf("Text = %q, want free text to win", res.Text)
	}
}

func TestAskUserRunReportsUnansweredQuestionAsData(t *testing.T) {
	fa := &fakeAsker{answers: ask.Answers{}}
	a := AskUser{Asker: fa}
	res, err := a.Run(context.Background(), json.RawMessage(`{"question":"proceed?"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Errorf("unexpected IsError=true: %q", res.Text)
	}
	if res.Text == "" {
		t.Error("expected a non-empty placeholder for an unanswered question")
	}
}

func TestAskUserRunReturnsGoErrorOnContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fa := &fakeAsker{err: context.Canceled}
	a := AskUser{Asker: fa}
	_, err := a.Run(ctx, json.RawMessage(`{"question":"proceed?"}`))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
}

func TestAskUserRunReportsAskerFailureAsToolErrorData(t *testing.T) {
	fa := &fakeAsker{err: errors.New("boom")}
	a := AskUser{Asker: fa}
	res, err := a.Run(context.Background(), json.RawMessage(`{"question":"proceed?"}`))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !res.IsError {
		t.Error("expected Result.IsError=true when the Asker fails without cancellation")
	}
}

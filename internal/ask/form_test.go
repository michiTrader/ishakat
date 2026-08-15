package ask

import (
	"context"
	"strings"
	"testing"
)

// TestRenderFitsFortyColumns is step 27's own closing criterion
// (docs/PLAN.md §21.14): "one 40-column render test". Termux at 40
// columns is this project's floor (internal/tui/toolactivity_internal_test.go's
// own comment), so every line State.Render produces — at any tab, for a
// multi-question Form with long labels and a free-text question — must fit
// inside 40 columns without truncation, the same property
// internal/tui/picker_internal_test.go already asserts of the model
// picker's own wireframe.
func TestRenderFitsFortyColumns(t *testing.T) {
	const width = 40
	form := Form{
		Title: "review before push",
		Questions: []Question{
			{
				ID:     "confirm",
				Prompt: "This step wants to run git push origin main, which is irreversible once accepted",
				Options: []Option{
					{Label: "Yes, push it", Value: "yes"},
					{Label: "No, stop here", Value: "no"},
				},
			},
			{
				ID:            "note",
				Prompt:        "Anything else the human should know before this proceeds?",
				AllowFreeText: true,
			},
		},
	}

	s := NewState(form)
	// Walk every tab, including the trailing Submit one, and check every
	// line at every stop — not just the tab the dialog happens to open
	// on.
	for tab := 0; tab < len(form.Questions)+1; tab++ {
		for _, line := range s.Render(width) {
			if n := len([]rune(line)); n > width {
				t.Errorf("tab %d: line %q is %d columns, want <= %d", tab, line, n, width)
			}
		}
		s = s.MoveTab(1)
	}

	// The single-question shape (tool approval's own case, §21.7) must
	// stay tab-bar-free at 40 columns too — this is the exact shape
	// toolapprove.go's reimplementation feeds this function with.
	single := NewState(Form{Questions: []Question{{
		ID:     "approve",
		Prompt: "bash: node tools/bench.js --frames 600 --warmup 30",
		Options: []Option{
			{Label: "Yes, once", Value: "once"},
			{Label: "Yes, for this session", Value: "session"},
			{Label: "No", Value: "deny"},
		},
	}}})
	for _, line := range single.Render(width) {
		if n := len([]rune(line)); n > width {
			t.Errorf("single-question form: line %q is %d columns, want <= %d", line, n, width)
		}
	}
	if strings.Contains(strings.Join(single.Render(width), "\n"), "[ ]") {
		t.Error("a single-question form must not draw a tab bar (nothing to tab between)")
	}
}

func TestStateChooseAndSubmit(t *testing.T) {
	form := Form{Questions: []Question{
		{ID: "a", Prompt: "pick one", Options: []Option{{Label: "x", Value: "x"}, {Label: "y", Value: "y"}}},
		{ID: "b", Prompt: "free text", AllowFreeText: true},
	}}
	s := NewState(form)

	if _, ok := s.Submit(); ok {
		t.Fatal("Submit succeeded before any question was answered")
	}

	s = s.MoveOption(1).Choose() // picks "y" on question a
	if !s.IsAnswered("a") {
		t.Fatal("question a not marked answered after Choose")
	}
	if _, ok := s.Submit(); ok {
		t.Fatal("Submit succeeded with question b still unanswered")
	}

	s = s.MoveTab(1).SetFreeText("looks fine to me")
	answers, ok := s.Submit()
	if !ok {
		t.Fatal("Submit failed after every question was answered")
	}
	if answers["a"].Value != "y" {
		t.Fatalf("answers[a] = %+v, want Value=y", answers["a"])
	}
	if answers["b"].FreeText != "looks fine to me" {
		t.Fatalf("answers[b] = %+v, want the free text", answers["b"])
	}
}

func TestMoveTabWrapsAcrossQuestionsAndSubmit(t *testing.T) {
	form := Form{Questions: []Question{{ID: "a"}, {ID: "b"}}}
	s := NewState(form)
	if s.Tab() != 0 {
		t.Fatalf("initial tab = %d, want 0", s.Tab())
	}
	s = s.MoveTab(-1)
	if !s.AtSubmit() {
		t.Fatalf("moving back from tab 0 should wrap to Submit (tab %d)", s.Tab())
	}
	s = s.MoveTab(1)
	if s.Tab() != 0 {
		t.Fatalf("moving forward from Submit should wrap to tab 0, got %d", s.Tab())
	}
}

func TestMoveOptionWrapsWithinQuestion(t *testing.T) {
	form := Form{Questions: []Question{{
		ID:      "a",
		Options: []Option{{Value: "1"}, {Value: "2"}, {Value: "3"}},
	}}}
	s := NewState(form)
	s = s.MoveOption(-1)
	if s.Cursor() != 2 {
		t.Fatalf("moving up from the first option should wrap to the last, got cursor %d", s.Cursor())
	}
	s = s.MoveOption(1).MoveOption(1).MoveOption(1)
	if s.Cursor() != 2 {
		t.Fatalf("wrapping fully around should land back where it started, got cursor %d", s.Cursor())
	}
}

func TestAwaitReplyReturnsOnReply(t *testing.T) {
	reply := make(chan int, 1)
	published := false
	got, err := AwaitReply(context.Background(), reply, func() {
		published = true
		reply <- 42
	})
	if err != nil {
		t.Fatalf("AwaitReply err = %v, want nil", err)
	}
	if !published {
		t.Fatal("publish was never called")
	}
	if got != 42 {
		t.Fatalf("AwaitReply = %d, want 42", got)
	}
}

func TestAwaitReplyReturnsOnContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reply := make(chan int)
	_, err := AwaitReply(ctx, reply, func() {})
	if err == nil {
		t.Fatal("AwaitReply err = nil, want the cancellation error")
	}
}

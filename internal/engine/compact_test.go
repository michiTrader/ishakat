package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MichiTrader/ishakat/internal/convo"
)

func TestSummarizeReturnsEmptyForAnEmptyPlan(t *testing.T) {
	stream := func(ctx context.Context, req Request) (<-chan Event, error) {
		t.Fatal("the Streamer must never be called for an empty Plan")
		return nil, nil
	}
	e := New(stream, 0)

	text, err := Summarize(context.Background(), e, "m", nil, convo.Plan{})
	if text != "" || err != nil {
		t.Errorf("Summarize = %q, %v, want \"\", nil", text, err)
	}
}

func TestSummarizeSendsTheRightTranscriptAndModel(t *testing.T) {
	msgs := []convo.Message{
		convo.User("what's the capital of france?"),
		convo.Assistant("Paris.", "a/model"),
		convo.NewMessage(convo.RoleUser, convo.ImageBlock("image/png", []byte{1}, "map.png")),
	}
	plan := convo.Plan{Replace: []int{0, 1, 2}}

	var gotModel string
	var gotSystem string
	var gotMessages []convo.Message
	stream := func(ctx context.Context, req Request) (<-chan Event, error) {
		gotModel = req.Model
		gotSystem = req.System
		gotMessages = req.Messages
		return chanOf(Event{Kind: EventDelta, Text: "A short recap."}, Event{Kind: EventDone}), nil
	}
	e := New(stream, 0)

	text, err := Summarize(context.Background(), e, "b/cheap", msgs, plan)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if text != "A short recap." {
		t.Errorf("text = %q, want %q", text, "A short recap.")
	}
	if gotModel != "b/cheap" {
		t.Errorf("Request.Model = %q, want %q", gotModel, "b/cheap")
	}
	if gotSystem != summarySystemPrompt {
		t.Errorf("Request.System was not the summarization instruction")
	}
	if len(gotMessages) != 1 {
		t.Fatalf("Request.Messages = %v, want exactly one message (the transcript)", gotMessages)
	}
	transcript := gotMessages[0].Text()
	for _, want := range []string{"User: what's the capital of france?", "Assistant: Paris.", "[image attached: map.png]"} {
		if !strings.Contains(transcript, want) {
			t.Errorf("transcript missing %q, got:\n%s", want, transcript)
		}
	}
}

// TestSummarizePlaceholderNamesToolFailure covers a place where losing the
// distinction is durable rather than momentary. The summary replaces the older
// turns and outlives them, so if a tool failed back there and the placeholder
// calls it a "result", the summary can end up asserting that something worked
// when it did not — and the turn that would have corrected the record is the
// very one being discarded.
func TestSummarizePlaceholderNamesToolFailure(t *testing.T) {
	msgs := []convo.Message{
		convo.NewMessage(convo.RoleTool, convo.ToolResultBlock("c1", "read_file", "ok")),
		convo.NewMessage(convo.RoleTool, convo.ToolErrorBlock("c2", "write_file", "read-only fs")),
	}
	plan := convo.Plan{Replace: []int{0, 1}}

	var got []convo.Message
	stream := func(_ context.Context, req Request) (<-chan Event, error) {
		got = req.Messages
		return chanOf(Event{Kind: EventDelta, Text: "recap"}, Event{Kind: EventDone}), nil
	}
	if _, err := Summarize(context.Background(), New(stream, 0), "b/cheap", msgs, plan); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if len(got) != 1 {
		t.Fatalf("Request.Messages = %v, want exactly one transcript message", got)
	}

	transcript := got[0].Text()
	if !strings.Contains(transcript, "[tool failed: write_file]") {
		t.Errorf("the failed tool is not named as a failure, got:\n%s", transcript)
	}
	if !strings.Contains(transcript, "[result from tool: read_file]") {
		t.Errorf("the successful tool lost its placeholder, got:\n%s", transcript)
	}
	// Both tools must appear: a placeholder that swallowed one of them would
	// leave the summary unaware that the call happened at all.
	if strings.Count(transcript, "tool") < 2 {
		t.Errorf("both tool blocks should reach the transcript, got:\n%s", transcript)
	}
}

func TestSummarizeSkipsReasoningBlocksAndEmptyMessages(t *testing.T) {
	msgs := []convo.Message{
		convo.NewMessage(convo.RoleAssistant, convo.ReasoningBlock("scratch thoughts"), convo.TextBlock("the answer")),
		convo.NewMessage(convo.RoleAssistant), // an aborted turn with nothing in it
	}
	plan := convo.Plan{Replace: []int{0, 1}}

	var gotTranscript string
	stream := func(ctx context.Context, req Request) (<-chan Event, error) {
		gotTranscript = req.Messages[0].Text()
		return chanOf(Event{Kind: EventDelta, Text: "recap"}, Event{Kind: EventDone}), nil
	}
	e := New(stream, 0)

	if _, err := Summarize(context.Background(), e, "m", msgs, plan); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if strings.Contains(gotTranscript, "scratch thoughts") {
		t.Errorf("transcript leaked a reasoning block: %q", gotTranscript)
	}
	if strings.Count(gotTranscript, "Assistant:") != 1 {
		t.Errorf("transcript = %q, want exactly one Assistant line (the empty message contributes nothing)", gotTranscript)
	}
}

func TestSummarizeReturnsEmptyWhenEveryReplacedMessageIsEmpty(t *testing.T) {
	msgs := []convo.Message{convo.NewMessage(convo.RoleAssistant)}
	plan := convo.Plan{Replace: []int{0}}

	stream := func(ctx context.Context, req Request) (<-chan Event, error) {
		t.Fatal("the Streamer must never be called when the transcript is empty")
		return nil, nil
	}
	e := New(stream, 0)

	text, err := Summarize(context.Background(), e, "m", msgs, plan)
	if text != "" || err != nil {
		t.Errorf("Summarize = %q, %v, want \"\", nil", text, err)
	}
}

func TestSummarizePropagatesAModelError(t *testing.T) {
	wantErr := errors.New("provider unreachable")
	msgs := []convo.Message{convo.User("hi")}
	plan := convo.Plan{Replace: []int{0}}

	stream := func(ctx context.Context, req Request) (<-chan Event, error) {
		return nil, wantErr
	}
	e := New(stream, 0)

	_, err := Summarize(context.Background(), e, "m", msgs, plan)
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want %v", err, wantErr)
	}
}

func TestSummarizeRejectsAnEmptyAnswer(t *testing.T) {
	msgs := []convo.Message{convo.User("hi")}
	plan := convo.Plan{Replace: []int{0}}

	stream := func(ctx context.Context, req Request) (<-chan Event, error) {
		return chanOf(Event{Kind: EventDelta, Text: "   "}, Event{Kind: EventDone}), nil
	}
	e := New(stream, 0)

	text, err := Summarize(context.Background(), e, "m", msgs, plan)
	if err == nil {
		t.Fatal("err = nil, want an error: a blank summary must not be stored")
	}
	if text != "" {
		t.Errorf("text = %q, want empty alongside the error", text)
	}
}

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestDispatchNameDangerDescriptionParameters(t *testing.T) {
	d := Dispatch{}
	if d.Name() != "dispatch" {
		t.Errorf("Name() = %q, want dispatch", d.Name())
	}
	if d.Danger() != DangerHigh {
		t.Errorf("Danger() = %v, want DangerHigh", d.Danger())
	}
	if d.Description() == "" {
		t.Error("Description() is empty")
	}
	var schema struct {
		Type       string `json:"type"`
		Properties struct {
			Task struct {
				Type string `json:"type"`
			} `json:"task"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(d.Parameters(), &schema); err != nil {
		t.Fatalf("Parameters() did not unmarshal: %v", err)
	}
	if schema.Type != "object" {
		t.Errorf("schema.Type = %q, want object", schema.Type)
	}
	if schema.Properties.Task.Type != "string" {
		t.Errorf("task property type = %q, want string", schema.Properties.Task.Type)
	}
	if len(schema.Required) != 1 || schema.Required[0] != "task" {
		t.Errorf("Required = %v, want [task]", schema.Required)
	}
}

func TestDispatchRunRejectsMissingTask(t *testing.T) {
	d := Dispatch{}
	if _, err := d.Run(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("expected an error for a missing task")
	}
}

func TestDispatchRunRejectsInvalidJSON(t *testing.T) {
	d := Dispatch{}
	if _, err := d.Run(context.Background(), json.RawMessage(`not json`)); err == nil {
		t.Fatal("expected an error for invalid arguments JSON")
	}
}

func TestDispatchRunWithNilRunnerIsToolErrorNotGoError(t *testing.T) {
	d := Dispatch{}
	res, err := d.Run(context.Background(), json.RawMessage(`{"task":"do something"}`))
	if err != nil {
		t.Fatalf("unexpected Go error with a nil Runner: %v", err)
	}
	if !res.IsError {
		t.Error("expected Result.IsError=true when no Runner is configured")
	}
}

func TestDispatchRunCallsRunnerWithTaskAndReturnsItsAnswer(t *testing.T) {
	var gotTask string
	d := Dispatch{Runner: func(ctx context.Context, task string) (string, error) {
		gotTask = task
		return "the sub-agent's answer", nil
	}}
	res, err := d.Run(context.Background(), json.RawMessage(`{"task":"summarize x"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Errorf("unexpected IsError=true: %q", res.Text)
	}
	if res.Text != "the sub-agent's answer" {
		t.Errorf("Text = %q, want %q", res.Text, "the sub-agent's answer")
	}
	if gotTask != "summarize x" {
		t.Errorf("Runner received task %q, want %q", gotTask, "summarize x")
	}
}

func TestDispatchRunEmptyAnswerStillOK(t *testing.T) {
	d := Dispatch{Runner: func(ctx context.Context, task string) (string, error) {
		return "", nil
	}}
	res, err := d.Run(context.Background(), json.RawMessage(`{"task":"x"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Error("an empty answer is not an error")
	}
	if res.Text == "" {
		t.Error("expected a placeholder message for an empty sub-agent answer")
	}
}

func TestDispatchRunRunnerErrorBecomesToolErrorData(t *testing.T) {
	d := Dispatch{Runner: func(ctx context.Context, task string) (string, error) {
		return "", errors.New("boom")
	}}
	res, err := d.Run(context.Background(), json.RawMessage(`{"task":"x"}`))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !res.IsError {
		t.Error("expected Result.IsError=true when the Runner fails")
	}
}

func TestDispatchRunRunnerErrorWithCancelledContextIsGoError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d := Dispatch{Runner: func(ctx context.Context, task string) (string, error) {
		return "", errors.New("sub-agent stream cancelled")
	}}
	_, err := d.Run(ctx, json.RawMessage(`{"task":"x"}`))
	if err == nil {
		t.Fatal("expected a Go error when the caller's own context is already cancelled")
	}
}

package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"codex_go/internal/appserver"
	"codex_go/internal/tui/chatwidget"
)

type safetyRetryClientFixture struct {
	read         *appserver.ThreadReadResponse
	forked       *appserver.ThreadForkResponse
	interruptErr error
	readErr      error
	forkErr      error
	submitErr    error
	readParams   appserver.ThreadReadParams
	forkParams   appserver.ThreadForkParams
	submission   SafetyRetrySubmission
}

func (c *safetyRetryClientFixture) InterruptTurn(_ context.Context, _, _ string) error {
	return c.interruptErr
}
func (c *safetyRetryClientFixture) ReadThread(_ context.Context, params appserver.ThreadReadParams) (*appserver.ThreadReadResponse, error) {
	c.readParams = params
	return c.read, c.readErr
}
func (c *safetyRetryClientFixture) ForkThread(_ context.Context, params appserver.ThreadForkParams) (*appserver.ThreadForkResponse, error) {
	c.forkParams = params
	return c.forked, c.forkErr
}
func (c *safetyRetryClientFixture) SubmitSafetyRetry(_ context.Context, submission SafetyRetrySubmission) error {
	c.submission = submission
	return c.submitErr
}

func TestApplySafetyRetryForksBeforeInterruptedTurnAndSubmitsOnFork(t *testing.T) {
	turns := []appserver.Turn{{ID: "previous", Status: appserver.TurnStatusCompleted}, {ID: "interrupted", Status: appserver.TurnStatusInterrupted}}
	forkedFrom := "source"
	client := &safetyRetryClientFixture{
		read:   &appserver.ThreadReadResponse{Thread: &appserver.Thread{ID: "source", Turns: turns}},
		forked: &appserver.ThreadForkResponse{Thread: &appserver.Thread{ID: "forked", ForkedFromID: &forkedFrom, Turns: turns[:1]}},
	}
	result := ApplySafetyRetry(context.Background(), client, ThreadSessionState{ThreadID: "source", Model: "slow"}, turns, SafetyRetryRequest{ThreadID: "source", TurnID: "interrupted", Model: "fast", Prompt: chatwidget.ThreadComposerState{Text: "retry me"}})
	if !result.Submitted || result.Session.ThreadID != "forked" || result.Session.Model != "fast" || result.Session.ReasoningEffort == nil || *result.Session.ReasoningEffort != "low" {
		t.Fatalf("result = %#v", result)
	}
	if !client.readParams.IncludeTurns || client.forkParams.BeforeTurnID != "interrupted" || client.submission.ThreadID != "forked" || client.submission.ThreadID == "source" {
		t.Fatalf("calls = %#v %#v %#v", client.readParams, client.forkParams, client.submission)
	}
}

func TestSafetyRetryForkPointRustValidation(t *testing.T) {
	tests := []struct {
		name  string
		turns []appserver.Turn
		want  string
	}{
		{"missing", []appserver.Turn{{ID: "other", Status: appserver.TurnStatusCompleted}}, "is missing from the source thread"},
		{"not latest", []appserver.Turn{{ID: "retry", Status: appserver.TurnStatusInterrupted}, {ID: "new", Status: appserver.TurnStatusCompleted}}, "is no longer the latest turn"},
		{"still running", []appserver.Turn{{ID: "retry", Status: appserver.TurnStatusInProgress}}, "is still in progress"},
		{"previous running", []appserver.Turn{{ID: "previous", Status: appserver.TurnStatusInProgress}, {ID: "retry", Status: appserver.TurnStatusInterrupted}}, "previous turn previous is still in progress"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := safetyRetryForkPoint(tt.turns, "retry")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestApplySafetyRetryFailuresRestoreSourceSessionTurnsAndComposer(t *testing.T) {
	turns := []appserver.Turn{{ID: "retry", Status: appserver.TurnStatusInterrupted}}
	base := safetyRetryClientFixture{read: &appserver.ThreadReadResponse{Thread: &appserver.Thread{ID: "source", Turns: turns}}, forked: &appserver.ThreadForkResponse{Thread: &appserver.Thread{ID: "forked"}}}
	for _, tc := range []struct {
		name   string
		mutate func(*safetyRetryClientFixture)
	}{
		{"fork", func(c *safetyRetryClientFixture) { c.forkErr = errors.New("fork unavailable") }},
		{"submit", func(c *safetyRetryClientFixture) { c.submitErr = errors.New("submit unavailable") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := base
			tc.mutate(&client)
			result := ApplySafetyRetry(context.Background(), &client, ThreadSessionState{ThreadID: "source", Model: "slow"}, turns, SafetyRetryRequest{ThreadID: "source", TurnID: "retry", Model: "fast", Prompt: chatwidget.ThreadComposerState{Text: "restore me", LocalImages: []string{"image.png"}}})
			if result.Submitted || result.Session.ThreadID != "source" || result.Composer.Text != "restore me" || len(result.Composer.LocalImages) != 1 || len(result.Turns) != 1 {
				t.Fatalf("result = %#v", result)
			}
		})
	}
}

package app

import (
	"context"
	"fmt"
	"strings"

	"codex_go/internal/appserver"
	"codex_go/internal/tui/chatwidget"
)

type SafetyRetryRequest struct {
	ThreadID string
	TurnID   string
	Model    string
	Prompt   chatwidget.ThreadComposerState
}

type SafetyRetrySubmission struct {
	ThreadID        string
	Model           string
	ReasoningEffort string
	Prompt          chatwidget.ThreadComposerState
}

type SafetyRetryClient interface {
	InterruptTurn(ctx context.Context, threadID, turnID string) error
	ReadThread(ctx context.Context, params appserver.ThreadReadParams) (*appserver.ThreadReadResponse, error)
	ForkThread(ctx context.Context, params appserver.ThreadForkParams) (*appserver.ThreadForkResponse, error)
	SubmitSafetyRetry(ctx context.Context, submission SafetyRetrySubmission) error
}

type SafetyRetryResult struct {
	Session      ThreadSessionState
	Turns        []appserver.Turn
	Composer     chatwidget.ThreadComposerState
	Submitted    bool
	ErrorMessage string
}

func ApplySafetyRetry(ctx context.Context, client SafetyRetryClient, source ThreadSessionState, turns []appserver.Turn, request SafetyRetryRequest) SafetyRetryResult {
	failure := func(err error) SafetyRetryResult {
		return SafetyRetryResult{
			Session: source.Clone(), Turns: cloneAppTurns(turns), Composer: clonePromptEditComposer(request.Prompt),
			ErrorMessage: "Failed to retry with a faster model: " + err.Error(),
		}
	}
	if client == nil {
		return failure(fmt.Errorf("safety retry is unavailable"))
	}
	if strings.TrimSpace(request.ThreadID) == "" || request.ThreadID != source.ThreadID {
		return failure(fmt.Errorf("interrupted turn no longer belongs to the active thread"))
	}
	if strings.TrimSpace(request.TurnID) == "" {
		return failure(fmt.Errorf("interrupted turn is unavailable"))
	}
	if err := client.InterruptTurn(ctx, request.ThreadID, request.TurnID); err != nil {
		return failure(err)
	}
	read, err := client.ReadThread(ctx, appserver.ThreadReadParams{ThreadID: request.ThreadID, IncludeTurns: true})
	if err != nil || read == nil || read.Thread == nil {
		if err == nil {
			err = fmt.Errorf("thread read did not return a session")
		}
		return failure(err)
	}
	if err := safetyRetryForkPoint(read.Thread.Turns, request.TurnID); err != nil {
		return failure(err)
	}
	forked, err := client.ForkThread(ctx, appserver.ThreadForkParams{ThreadID: request.ThreadID, BeforeTurnID: request.TurnID, Model: stringPointerOrNil(request.Model)})
	if err != nil || forked == nil || forked.Thread == nil {
		if err == nil {
			err = fmt.Errorf("thread fork did not return a session")
		}
		return failure(err)
	}
	session := threadSessionStateFromPromptEditResponse(forked.Thread, source)
	session.Model = request.Model
	low := "low"
	session.ReasoningEffort = &low
	submission := SafetyRetrySubmission{ThreadID: forked.Thread.ID, Model: request.Model, ReasoningEffort: low, Prompt: clonePromptEditComposer(request.Prompt)}
	if err := client.SubmitSafetyRetry(ctx, submission); err != nil {
		return failure(err)
	}
	return SafetyRetryResult{Session: session, Turns: cloneAppTurns(forked.Thread.Turns), Composer: chatwidget.ThreadComposerState{}, Submitted: true}
}

func safetyRetryForkPoint(turns []appserver.Turn, turnID string) error {
	index := -1
	for i := range turns {
		if turns[i].ID == turnID {
			index = i
			break
		}
	}
	if index < 0 {
		return fmt.Errorf("interrupted turn %s is missing from the source thread", turnID)
	}
	if index+1 != len(turns) {
		return fmt.Errorf("interrupted turn %s is no longer the latest turn", turnID)
	}
	if turns[index].Status == appserver.TurnStatusInProgress {
		return fmt.Errorf("interrupted turn %s is still in progress", turnID)
	}
	if index > 0 && turns[index-1].Status == appserver.TurnStatusInProgress {
		return fmt.Errorf("previous turn %s is still in progress", turns[index-1].ID)
	}
	return nil
}

func cloneAppTurns(turns []appserver.Turn) []appserver.Turn {
	return append([]appserver.Turn(nil), turns...)
}

func stringPointerOrNil(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return &value
}

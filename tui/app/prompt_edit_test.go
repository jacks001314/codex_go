package app

import (
	"context"
	"errors"
	"testing"

	"codex_go/appserver"
	"codex_go/tui/chatwidget"
)

type promptEditClient struct {
	forked     *appserver.ThreadForkResponse
	started    *appserver.ThreadStartResponse
	err        error
	forkParams appserver.ThreadForkParams
	freshCalls int
}

func (c *promptEditClient) ForkThread(_ context.Context, params appserver.ThreadForkParams) (*appserver.ThreadForkResponse, error) {
	c.forkParams = params
	return c.forked, c.err
}
func (c *promptEditClient) StartFreshThread(_ context.Context, _ ThreadSessionState) (*appserver.ThreadStartResponse, error) {
	c.freshCalls++
	return c.started, c.err
}

func promptEditTurns() []appserver.Turn {
	return []appserver.Turn{
		{ID: "turn-1", Items: []appserver.ThreadItem{{ID: "u1", Type: "userMessage", Text: "first"}, {ID: "a1", Type: "agentMessage", Role: "assistant", Text: "one"}}},
		{ID: "turn-2", Items: []appserver.ThreadItem{{ID: "u2", Type: "userMessage", Text: "second"}}},
	}
}

func TestApplyPromptEditForksBeforeSelectedPromptAndPreservesSourceLikeRust(t *testing.T) {
	source := ThreadSessionState{ThreadID: "source", Model: "gpt-test", CWD: "/repo"}
	forkedFrom := "source"
	client := &promptEditClient{forked: &appserver.ThreadForkResponse{Thread: &appserver.Thread{ID: "forked", ForkedFromID: &forkedFrom, Turns: promptEditTurns()[:1], CWD: "/repo"}}}
	result := ApplyPromptEdit(context.Background(), client, source, promptEditTurns(), PromptEditSelection{ThreadID: "source", UserOrdinal: 1, Prompt: chatwidget.ThreadComposerState{Text: "second", RemoteImageURLs: []string{"https://example.test/image.png"}}})
	if !result.Branched || result.FreshThread || result.Session.ThreadID != "forked" || client.forkParams.BeforeTurnID != "turn-2" {
		t.Fatalf("result = %#v params=%#v", result, client.forkParams)
	}
	if len(result.Turns) != 1 || result.Turns[0].ID != "turn-1" || result.Composer.Text != "second" || len(result.Composer.RemoteImageURLs) != 1 {
		t.Fatalf("fork result = %#v", result)
	}
	if source.ThreadID != "source" || len(promptEditTurns()) != 2 {
		t.Fatalf("source mutated = %#v", source)
	}
}

func TestApplyPromptEditBeforeFirstPromptStartsFreshThreadLikeRust(t *testing.T) {
	client := &promptEditClient{started: &appserver.ThreadStartResponse{Thread: &appserver.Thread{ID: "fresh", CWD: "/repo"}}}
	result := ApplyPromptEdit(context.Background(), client, ThreadSessionState{ThreadID: "source", CWD: "/repo"}, promptEditTurns(), PromptEditSelection{ThreadID: "source", UserOrdinal: 0, Prompt: chatwidget.ThreadComposerState{Text: "first"}})
	if !result.Branched || !result.FreshThread || result.Session.ThreadID != "fresh" || client.freshCalls != 1 || client.forkParams.ThreadID != "" {
		t.Fatalf("result = %#v client=%#v", result, client)
	}
}

func TestApplyPromptEditFailureRestoresSelectionComposerAndSessionLikeRust(t *testing.T) {
	selection := PromptEditSelection{ThreadID: "source", UserOrdinal: 1, Prompt: chatwidget.ThreadComposerState{Text: "edit this prompt"}, PreviousDraft: chatwidget.ThreadComposerState{Text: "old draft"}}
	result := ApplyPromptEdit(context.Background(), &promptEditClient{err: errors.New("branch unavailable")}, ThreadSessionState{ThreadID: "source"}, promptEditTurns(), selection)
	if result.Branched || result.Session.ThreadID != "source" || result.Composer.Text != "edit this prompt" || result.RestoredSelection == nil || result.RestoredSelection.UserOrdinal != 1 {
		t.Fatalf("result = %#v", result)
	}
	if result.ErrorMessage != "Failed to branch before the selected prompt: branch unavailable" {
		t.Fatalf("error = %q", result.ErrorMessage)
	}
}

func TestPromptEditBeforeTurnIDSkipsReviewAndEmptyPromptsLikeRust(t *testing.T) {
	turns := []appserver.Turn{
		{ID: "turn-1", Items: []appserver.ThreadItem{{ID: "u1", Type: "userMessage", Text: "first"}}},
		{ID: "turn-review", Items: []appserver.ThreadItem{
			{ID: "review-start", Type: "enteredReviewMode", Text: "changes against main"},
			{ID: "u2", Type: "userMessage", Text: "review prompt"},
			{ID: "review-end", Type: "exitedReviewMode", Text: "review complete"},
		}},
		{ID: "turn-empty", Items: []appserver.ThreadItem{{ID: "u3", Type: "userMessage", Text: "   "}}},
		{ID: "turn-2", Items: []appserver.ThreadItem{{ID: "u4", Type: "userMessage", Text: "second"}}},
	}

	// The review-mode prompt and the whitespace-only input are hidden, so the
	// second visible prompt is "second" and resolves to turn-2.
	before, err := promptEditBeforeTurnID(turns, 1)
	if err != nil || before != "turn-2" {
		t.Fatalf("beforeTurnID = %q err=%v, want turn-2", before, err)
	}
	if _, err := promptEditBeforeTurnID(turns, 2); err == nil {
		t.Fatal("a third visible prompt should not exist")
	}
}

func TestPromptEditBeforeTurnIDRejectsSteersAndInProgressTurnsLikeRust(t *testing.T) {
	steered := []appserver.Turn{{
		ID: "turn-1",
		Items: []appserver.ThreadItem{
			{ID: "u1", Type: "userMessage", Text: "initial"},
			{ID: "u2", Type: "userMessage", Text: "steer"},
		},
	}}
	if _, err := promptEditBeforeTurnID(steered, 1); err == nil || err.Error() != "the selected prompt is a steer and cannot be branched independently" {
		t.Fatalf("steer err = %v", err)
	}

	running := []appserver.Turn{{
		ID:     "turn-1",
		Status: appserver.TurnStatusInProgress,
		Items:  []appserver.ThreadItem{{ID: "u1", Type: "userMessage", Text: "initial"}},
	}}
	if _, err := promptEditBeforeTurnID(running, 0); err == nil || err.Error() != "the selected prompt belongs to a turn that is still in progress" {
		t.Fatalf("in-progress err = %v", err)
	}

	if before, err := promptEditBeforeTurnID(steered, 0); err != nil || before != "turn-1" {
		t.Fatalf("first prompt beforeTurnID = %q err=%v", before, err)
	}
}

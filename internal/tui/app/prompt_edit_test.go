package app

import (
	"context"
	"errors"
	"testing"

	"codex_go/internal/appserver"
	"codex_go/internal/tui/chatwidget"
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

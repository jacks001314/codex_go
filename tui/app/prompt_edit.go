package app

import (
	"context"
	"fmt"
	"strings"

	"codex_go/appserver"
	"codex_go/tui/chatwidget"
	"codex_go/turn"
)

type PromptEditSelection struct {
	ThreadID      string
	UserOrdinal   int
	Prompt        chatwidget.ThreadComposerState
	PreviousDraft chatwidget.ThreadComposerState
}

type PromptEditForkClient interface {
	ForkThread(ctx context.Context, params appserver.ThreadForkParams) (*appserver.ThreadForkResponse, error)
	StartFreshThread(ctx context.Context, source ThreadSessionState) (*appserver.ThreadStartResponse, error)
}

type PromptEditResult struct {
	Session           ThreadSessionState
	Turns             []appserver.Turn
	Composer          chatwidget.ThreadComposerState
	RestoredSelection *PromptEditSelection
	Branched          bool
	FreshThread       bool
	ErrorMessage      string
}

func ApplyPromptEdit(ctx context.Context, client PromptEditForkClient, source ThreadSessionState, turns []appserver.Turn, selection PromptEditSelection) PromptEditResult {
	failure := func(err error) PromptEditResult {
		return PromptEditResult{
			Session: source.Clone(), Turns: append([]appserver.Turn(nil), turns...), Composer: clonePromptEditComposer(selection.Prompt),
			RestoredSelection: clonePromptEditSelection(&selection), ErrorMessage: "Failed to branch before the selected prompt: " + err.Error(),
		}
	}
	if client == nil {
		return failure(fmt.Errorf("thread fork is unavailable"))
	}
	if strings.TrimSpace(selection.ThreadID) == "" || selection.ThreadID != source.ThreadID {
		return failure(fmt.Errorf("selected prompt no longer belongs to the active thread"))
	}
	beforeTurnID, err := promptEditBeforeTurnID(turns, selection.UserOrdinal)
	if err != nil {
		return failure(err)
	}
	if selection.UserOrdinal == 0 {
		started, err := client.StartFreshThread(ctx, source.Clone())
		if err != nil || started == nil || started.Thread == nil {
			if err == nil {
				err = fmt.Errorf("fresh thread did not return a session")
			}
			return failure(err)
		}
		return PromptEditResult{Session: threadSessionStateFromPromptEditResponse(started.Thread, source), Turns: []appserver.Turn{}, Composer: clonePromptEditComposer(selection.Prompt), Branched: true, FreshThread: true}
	}
	forked, err := client.ForkThread(ctx, appserver.ThreadForkParams{ThreadID: source.ThreadID, BeforeTurnID: beforeTurnID})
	if err != nil || forked == nil || forked.Thread == nil {
		if err == nil {
			err = fmt.Errorf("thread fork did not return a session")
		}
		return failure(err)
	}
	return PromptEditResult{Session: threadSessionStateFromPromptEditResponse(forked.Thread, source), Turns: append([]appserver.Turn(nil), forked.Thread.Turns...), Composer: clonePromptEditComposer(selection.Prompt), Branched: true}
}

func promptEditBeforeTurnID(turns []appserver.Turn, ordinal int) (string, error) {
	if ordinal < 0 {
		return "", fmt.Errorf("selected prompt index is invalid")
	}
	seen := 0
	for _, turn := range turns {
		for _, item := range turn.Items {
			if !promptEditUserItem(item) {
				continue
			}
			if seen == ordinal {
				return turn.ID, nil
			}
			seen++
		}
	}
	return "", fmt.Errorf("selected prompt was not found in thread history")
}

func promptEditUserItem(item appserver.ThreadItem) bool {
	typeName := strings.TrimSpace(item.Type)
	return item.Role != "assistant" && (typeName == "message" || typeName == "user_message" || typeName == "userMessage")
}

func threadSessionStateFromPromptEditResponse(thread *appserver.Thread, source ThreadSessionState) ThreadSessionState {
	result := source.Clone()
	result.ThreadID = thread.ID
	result.ForkedFromID = thread.ForkedFromID
	result.ThreadName = thread.Name
	result.ModelProviderID = thread.ModelProvider
	result.CWD = thread.CWD
	result.RolloutPath = thread.Path
	return result
}

func clonePromptEditSelection(selection *PromptEditSelection) *PromptEditSelection {
	if selection == nil {
		return nil
	}
	out := *selection
	out.Prompt = clonePromptEditComposer(selection.Prompt)
	out.PreviousDraft = clonePromptEditComposer(selection.PreviousDraft)
	return &out
}
func clonePromptEditComposer(value chatwidget.ThreadComposerState) chatwidget.ThreadComposerState {
	value.LocalImages = append([]string(nil), value.LocalImages...)
	value.RemoteImageURLs = append([]string(nil), value.RemoteImageURLs...)
	value.TextElements = append([]turn.TextElement(nil), value.TextElements...)
	value.MentionBindings = append([]string(nil), value.MentionBindings...)
	value.PendingPastes = append([][2]string(nil), value.PendingPastes...)
	return value
}

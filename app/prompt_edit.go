package app

import (
	"context"
	"errors"
	"strings"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/cli"
	"codex_go/session"
	codextui "codex_go/tui"
	tuiapp "codex_go/tui/app"
	codextea "codex_go/tui/tea"
)

// interactiveRemotePromptEditHandler implements the backtrack "edit an earlier
// prompt" flow against the app server (Rust #45845): read the thread's persisted
// turns, resolve the selected transcript ordinal, and revert the current thread
// before that turn so the prompt can be edited without creating a fork.
func interactiveRemotePromptEditHandler(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint, _ *cli.RootOptions, _ *codextui.State, _ ...*taskToolsMCPHost) codextea.PromptEditFunc {
	return func(selection tuiapp.PromptEditSelection) (codextea.SessionResumeResponse, error) {
		threadID := strings.TrimSpace(selection.ThreadID)
		if threadID == "" {
			return codextea.SessionResumeResponse{}, errors.New("remote prompt edit requires a thread id")
		}
		client, err := openRemoteSessionClient(ctx, endpoint)
		if err != nil {
			return codextea.SessionResumeResponse{}, err
		}
		defer client.close()
		sourceThread, err := remoteTUIReadThread(ctx, client, threadID, true)
		if err != nil {
			return codextea.SessionResumeResponse{}, err
		}
		source := tuiapp.ThreadSessionState{ThreadID: threadID, CWD: strings.TrimSpace(sourceThread.CWD)}
		result := tuiapp.ApplyPromptEdit(ctx, remotePromptEditClient{client: client}, source, sourceThread.Turns, selection)
		if message := strings.TrimSpace(result.ErrorMessage); message != "" {
			return codextea.SessionResumeResponse{}, errors.New(strings.TrimPrefix(message, "Failed to revert before the selected prompt: "))
		}
		reverted, err := remoteTUIReadThread(ctx, client, threadID, true)
		if err != nil {
			return codextea.SessionResumeResponse{}, err
		}
		return remoteTUIResumeResponseFromThread(reverted), nil
	}
}

// remotePromptEditClient adapts the app-server websocket client to the prompt
// edit history rewrite.
type remotePromptEditClient struct {
	client *remoteAppServerTUIClient
}

func (c remotePromptEditClient) RevertThread(ctx context.Context, params appserver.ThreadRevertParams) (*appserver.Thread, error) {
	var response appserver.ThreadRevertResponse
	if err := remoteSessionRequest(ctx, c.client, appserver.MethodThreadRevert, params, &response); err != nil {
		return nil, err
	}
	return remoteTUIReadThread(ctx, c.client, strings.TrimSpace(params.ThreadID), true)
}

// interactivePromptEditHandler implements the same flow for the embedded TUI,
// which reaches the store through the in-process app-server router.
func interactivePromptEditHandler(root *cli.RootOptions) codextea.PromptEditFunc {
	return func(selection tuiapp.PromptEditSelection) (codextea.SessionResumeResponse, error) {
		threadID := strings.TrimSpace(selection.ThreadID)
		if threadID == "" {
			return codextea.SessionResumeResponse{}, errors.New("prompt edit requires a thread id")
		}
		store := newSessionStore()
		sourceThread, err := localThreadReadTurns(store, threadID)
		if err != nil {
			return codextea.SessionResumeResponse{}, err
		}
		source := tuiapp.ThreadSessionState{
			ThreadID:        threadID,
			CWD:             strings.TrimSpace(sourceThread.CWD),
			Model:           remoteTUIThreadModel(sourceThread),
			ModelProviderID: remoteTUIThreadProvider(sourceThread),
		}
		result := tuiapp.ApplyPromptEdit(context.Background(), localPromptEditClient{store: store}, source, sourceThread.Turns, selection)
		if message := strings.TrimSpace(result.ErrorMessage); message != "" {
			return codextea.SessionResumeResponse{}, errors.New(strings.TrimPrefix(message, "Failed to revert before the selected prompt: "))
		}
		reverted, err := localThreadReadTurns(store, threadID)
		if err != nil {
			return codextea.SessionResumeResponse{}, err
		}
		return remoteTUIResumeResponseFromThread(reverted), nil
	}
}

// localPromptEditClient adapts the in-process app-server router to the prompt
// edit history rewrite.
type localPromptEditClient struct {
	store *session.Store
}

func (c localPromptEditClient) RevertThread(_ context.Context, params appserver.ThreadRevertParams) (*appserver.Thread, error) {
	result, closeRuntime, err := localSessionRouterRequest(c.store, appserver.MethodThreadRevert, params)
	if closeRuntime != nil {
		defer closeRuntime()
	}
	if err != nil {
		return nil, err
	}
	response, ok := result.(*appserver.ThreadRevertResponse)
	if !ok || response == nil || response.Thread == nil {
		return nil, errors.New("thread/revert returned no thread")
	}
	return localThreadReadTurns(c.store, strings.TrimSpace(params.ThreadID))
}

// localThreadReadTurns reads a local thread with its turns for the prompt-edit
// ordinal lookup.
func localThreadReadTurns(store *session.Store, threadID string) (*appserver.Thread, error) {
	result, closeRuntime, err := localSessionRouterRequest(store, appserver.MethodThreadRead, appserver.ThreadReadParams{
		ThreadID:     strings.TrimSpace(threadID),
		IncludeTurns: true,
	})
	if closeRuntime != nil {
		defer closeRuntime()
	}
	if err != nil {
		return nil, err
	}
	response, ok := result.(*appserver.ThreadReadResponse)
	if !ok || response == nil || response.Thread == nil || strings.TrimSpace(response.Thread.ID) == "" {
		return nil, errors.New("thread/read returned no thread")
	}
	return response.Thread, nil
}

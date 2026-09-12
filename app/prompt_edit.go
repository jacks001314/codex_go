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
// prompt" flow against the app server (Rust ForkSessionForPromptEdit): read the
// thread's persisted turns so the selected transcript ordinal maps onto a turn,
// fork before it (or start a fresh thread for the first prompt), then attach the
// TUI to the branched conversation.
func interactiveRemotePromptEditHandler(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint, root *cli.RootOptions, state *codextui.State) codextea.PromptEditFunc {
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
		result := tuiapp.ApplyPromptEdit(ctx, remotePromptEditClient{client: client, root: root, state: state}, source, sourceThread.Turns, selection)
		if message := strings.TrimSpace(result.ErrorMessage); message != "" {
			return codextea.SessionResumeResponse{}, errors.New(strings.TrimPrefix(message, "Failed to branch before the selected prompt: "))
		}
		branchedThreadID := strings.TrimSpace(result.Session.ThreadID)
		if branchedThreadID == "" {
			branchedThreadID = threadID
		}
		// Attach the branched thread's committed history so the transcript shows
		// the prompts before the edit point.
		branched, err := remoteTUIReadThread(ctx, client, branchedThreadID, true)
		if err != nil {
			return codextea.SessionResumeResponse{}, err
		}
		return remoteTUIResumeResponseFromThread(branched), nil
	}
}

// remotePromptEditClient adapts the app-server websocket client to the prompt
// edit fork operations.
type remotePromptEditClient struct {
	client *remoteAppServerTUIClient
	root   *cli.RootOptions
	state  *codextui.State
}

func (c remotePromptEditClient) ForkThread(ctx context.Context, params appserver.ThreadForkParams) (*appserver.ThreadForkResponse, error) {
	var response appserver.ThreadForkResponse
	if err := remoteSessionRequest(ctx, c.client, appserver.MethodThreadFork, params, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c remotePromptEditClient) StartFreshThread(ctx context.Context, _ tuiapp.ThreadSessionState) (*appserver.ThreadStartResponse, error) {
	params, err := remoteThreadStartParams(c.root, c.state)
	if err != nil {
		return nil, err
	}
	var response appserver.ThreadStartResponse
	if err := remoteSessionRequest(ctx, c.client, appserver.MethodThreadStart, params, &response); err != nil {
		return nil, err
	}
	return &response, nil
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
			return codextea.SessionResumeResponse{}, errors.New(strings.TrimPrefix(message, "Failed to branch before the selected prompt: "))
		}
		branchedThreadID := strings.TrimSpace(result.Session.ThreadID)
		if branchedThreadID == "" {
			branchedThreadID = threadID
		}
		branched, err := localThreadReadTurns(store, branchedThreadID)
		if err != nil {
			return codextea.SessionResumeResponse{}, err
		}
		return remoteTUIResumeResponseFromThread(branched), nil
	}
}

// localPromptEditClient adapts the in-process app-server router to the prompt
// edit fork operations.
type localPromptEditClient struct {
	store *session.Store
}

func (c localPromptEditClient) ForkThread(_ context.Context, params appserver.ThreadForkParams) (*appserver.ThreadForkResponse, error) {
	result, closeRuntime, err := localSessionRouterRequest(c.store, appserver.MethodThreadFork, params)
	if closeRuntime != nil {
		defer closeRuntime()
	}
	if err != nil {
		return nil, err
	}
	response, ok := result.(*appserver.ThreadForkResponse)
	if !ok || response == nil {
		return nil, errors.New("thread/fork returned no response")
	}
	return response, nil
}

func (c localPromptEditClient) StartFreshThread(_ context.Context, source tuiapp.ThreadSessionState) (*appserver.ThreadStartResponse, error) {
	params := appserver.ThreadStartParams{
		CWD:           strings.TrimSpace(source.CWD),
		Model:         strings.TrimSpace(source.Model),
		ModelProvider: strings.TrimSpace(source.ModelProviderID),
	}
	result, closeRuntime, err := localSessionRouterRequest(c.store, appserver.MethodThreadStart, params)
	if closeRuntime != nil {
		defer closeRuntime()
	}
	if err != nil {
		return nil, err
	}
	response, ok := result.(*appserver.ThreadStartResponse)
	if !ok || response == nil {
		return nil, errors.New("thread/start returned no response")
	}
	return response, nil
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

package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/mcp"
	codextea "codex_go/tui/tea"
)

func interactiveLocalMCPLoginStarter(service *mcp.MCPService) func(name, threadID string) (codextea.MCPLoginStart, error) {
	return func(name, threadID string) (codextea.MCPLoginStart, error) {
		if service == nil {
			return codextea.MCPLoginStart{}, errors.New("MCP sign-in is unavailable")
		}
		rawCompletions := make(chan *mcp.MCPOAuthLoginCompletion, 4)
		var listenerMu sync.Mutex
		var unsubscribe func()
		removeListener := func() {
			listenerMu.Lock()
			remove := unsubscribe
			unsubscribe = nil
			listenerMu.Unlock()
			if remove != nil {
				remove()
			}
		}
		remove := service.AddOAuthLoginCompletionListener(func(_ context.Context, completion *mcp.MCPOAuthLoginCompletion) {
			if completion == nil || completion.Name != name {
				return
			}
			select {
			case rawCompletions <- completion:
			default:
			}
		})
		listenerMu.Lock()
		unsubscribe = remove
		listenerMu.Unlock()
		thread := strings.TrimSpace(threadID)
		response, err := service.OauthLogin(&mcp.MCPServerOauthLoginParams{
			Name:     name,
			ThreadID: &thread,
		})
		if err != nil {
			removeListener()
			close(rawCompletions)
			return codextea.MCPLoginStart{}, err
		}
		loginID := ""
		if response != nil {
			if response.LoginID != nil {
				loginID = strings.TrimSpace(*response.LoginID)
			}
			if target := strings.TrimSpace(response.AuthorizationURL); target != "" {
				// Browser launch is best effort; the login attempt remains alive
				// and reports its terminal result through the completion channel.
				_ = openMCPLoginBrowser(target)
			}
		}
		completions := make(chan codextea.MCPLoginCompletedMsg, 1)
		go func() {
			defer close(completions)
			for completion := range rawCompletions {
				if completion == nil {
					continue
				}
				completionLoginID := strings.TrimSpace(completion.LoginID)
				if loginID != "" && completionLoginID != "" && completionLoginID != loginID {
					continue
				}
				removeListener()
				completions <- codextea.MCPLoginCompletedMsg{
					Name:     completion.Name,
					ThreadID: completion.ThreadID,
					LoginID:  completion.LoginID,
					Success:  completion.Success,
					Error:    completion.Error,
				}
				return
			}
		}()
		return codextea.MCPLoginStart{LoginID: loginID, Completions: completions}, nil
	}
}

func interactiveRemoteMCPLoginStarter(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) func(name, threadID string) (codextea.MCPLoginStart, error) {
	return func(name, threadID string) (codextea.MCPLoginStart, error) {
		client, err := openRemoteSessionClient(ctx, endpoint)
		if err != nil {
			return codextea.MCPLoginStart{}, err
		}
		thread := strings.TrimSpace(threadID)
		var response mcp.MCPServerOauthLoginResponse
		if err := remoteSessionRequest(ctx, client, appserver.MethodMCPServerOauthLogin, mcp.MCPServerOauthLoginParams{
			Name:     name,
			ThreadID: &thread,
		}, &response); err != nil {
			client.close()
			return codextea.MCPLoginStart{}, err
		}
		loginID := ""
		if response.LoginID != nil {
			loginID = strings.TrimSpace(*response.LoginID)
		}
		if target := strings.TrimSpace(response.AuthorizationURL); target != "" {
			_ = openMCPLoginBrowser(target)
		}
		completions := make(chan codextea.MCPLoginCompletedMsg, 1)
		go monitorRemoteMCPLoginCompletion(ctx, client, name, loginID, completions)
		return codextea.MCPLoginStart{LoginID: loginID, Completions: completions}, nil
	}
}

func monitorRemoteMCPLoginCompletion(ctx context.Context, client *remoteAppServerTUIClient, name string, loginID string, completions chan<- codextea.MCPLoginCompletedMsg) {
	defer close(completions)
	defer client.close()
	for {
		message, err := client.readRemoteMessage(ctx)
		if err != nil {
			return
		}
		method := strings.TrimSpace(message.Method)
		if method == "" {
			continue
		}
		if len(message.ID) > 0 {
			_ = client.respondServerRequest(ctx, message)
			continue
		}
		if method != string(appserver.NotificationMCPServerOauthLoginCompleted) {
			continue
		}
		var payload appserver.MCPServerOauthLoginCompletedNotification
		if err := json.Unmarshal(message.Params, &payload); err != nil || payload.Name != name {
			continue
		}
		completionLoginID := ""
		if payload.LoginID.Value != nil {
			completionLoginID = strings.TrimSpace(*payload.LoginID.Value)
		}
		if loginID != "" && completionLoginID != "" && completionLoginID != loginID {
			continue
		}
		threadID := ""
		if payload.ThreadID != nil {
			threadID = strings.TrimSpace(*payload.ThreadID)
		}
		errorText := ""
		if payload.Error != nil {
			errorText = strings.TrimSpace(*payload.Error)
		}
		completions <- codextea.MCPLoginCompletedMsg{
			Name:     payload.Name,
			ThreadID: threadID,
			LoginID:  completionLoginID,
			Success:  payload.Success,
			Error:    errorText,
		}
		return
	}
}

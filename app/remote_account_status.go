package app

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/auth"
	tuistatus "codex_go/tui/status"
	codextea "codex_go/tui/tea"
)

const remoteAccountEmailRefreshTimeout = 30 * time.Second

// remoteAccountStatusFromAuthMode mirrors Rust's
// status_account_display_from_auth_mode: account updates carry auth mode and
// plan, but never the signed-in email.
func remoteAccountStatusFromAuthMode(authMode string, planType *auth.PlanType) (*tuistatus.AccountStatus, bool) {
	switch strings.ToLower(strings.TrimSpace(authMode)) {
	case "apikey", "api-key":
		status := tuistatus.APIKeyAccountStatus()
		return &status, false
	case "chatgpt", "chatgptauthtokens", "agentidentity", "personalaccesstoken":
		plan := ""
		if planType != nil {
			plan = tuistatus.PlanTypeDisplayName(*planType)
		}
		status := tuistatus.ChatGPTAccountStatus("", plan)
		return &status, true
	default:
		return nil, false
	}
}

func (c *remoteAppServerTUIClient) accountStatusUpdated(payload auth.AccountUpdatedNotification) {
	if c == nil {
		return
	}
	authMode := strings.TrimSpace(stringPtrValue(payload.AuthMode))
	status, hasChatGPTAccount := remoteAccountStatusFromAuthMode(authMode, payload.PlanType)
	display := ""
	if status != nil {
		display, _ = status.DisplayValue()
	}
	requestID := ""
	if hasChatGPTAccount {
		requestID = uuid.NewString()
		c.setAccountEmailRequestID(requestID)
	} else {
		c.setAccountEmailRequestID("")
	}
	c.send(codextea.AccountStatusUpdatedMsg{
		Display:           display,
		HasChatGPTAccount: hasChatGPTAccount,
		EmailRequestID:    requestID,
	})
	if requestID != "" {
		go c.refreshAccountEmail(requestID)
	}
}

func (c *remoteAppServerTUIClient) setAccountEmailRequestID(requestID string) {
	if c == nil {
		return
	}
	c.accountEmailMu.Lock()
	c.accountEmailRequestID = strings.TrimSpace(requestID)
	c.accountEmailMu.Unlock()
}

func (c *remoteAppServerTUIClient) accountEmailRequestCurrent(requestID string) bool {
	if c == nil {
		return false
	}
	c.accountEmailMu.Lock()
	current := c.accountEmailRequestID
	c.accountEmailMu.Unlock()
	return requestID != "" && requestID == current
}

func (c *remoteAppServerTUIClient) refreshAccountEmail(requestID string) {
	if c == nil || c.endpoint == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), remoteAccountEmailRefreshTimeout)
	defer cancel()
	response, err := remoteReadAccount(ctx, c.endpoint)
	if err != nil {
		slog.Warn("failed to read account identity after account update", "error", err)
		return
	}
	if response == nil || response.Account == nil || response.Account.Type != auth.AccountChatGPT {
		return
	}
	if !c.accountEmailRequestCurrent(requestID) {
		return
	}
	email := ""
	if response.Account.Email != nil {
		email = strings.TrimSpace(*response.Account.Email)
	}
	c.send(codextea.AccountEmailLoadedMsg{
		RequestID: requestID,
		Email:     email,
		Workspace: remoteStatusWorkspaceValue(response),
	})
}

// remoteStatusWorkspaceValue mirrors Rust's workspace_display for the /status
// card (#57366). Provider ownership is checked when the card renders, because
// the workspace belongs to the account while the provider is a thread setting.
func remoteStatusWorkspaceValue(response *auth.GetAccountResponse) string {
	if response == nil || response.Account == nil || response.Account.Type != auth.AccountChatGPT {
		return ""
	}
	return tuistatus.WorkspaceDisplay(response.Account.WorkspaceName, response.WorkspaceRouting)
}

// remoteReadAccount reads account/read on a dedicated app-server connection so
// an account-update notification never blocks the interactive read loop.
func remoteReadAccount(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) (*auth.GetAccountResponse, error) {
	client, err := openRemoteSessionClient(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	defer client.close()
	var response auth.GetAccountResponse
	if err := remoteSessionRequest(ctx, client, appserver.MethodGetAccount, auth.GetAccountParams{}, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

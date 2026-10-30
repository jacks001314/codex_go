package appserver

import (
	"context"
	"errors"
	"strings"
	"time"

	"codex_go/auth"
	"codex_go/chatgptapi"
	"codex_go/config"
)

// Rust parity: codex-rs/app-server/src/request_processors/account_processor/
// workspace_switching.rs and thread_processor/workspace_switching.rs (#57514).
//
// accountSession/list and accountSession/switch only ever act on the active
// managed ChatGPT login. Switching pauses turn admission, unloads every running
// thread, and replaces the credential through a conditional write so a
// concurrent login or logout is never overwritten.

const workspaceMembershipTimeout = 30 * time.Second

func (r *RuntimeRouter) handleAccountSessionList(request *Request) (*auth.AccountSessionsResponse, error) {
	var params auth.AccountSessionsListParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	return r.listAccountSessions(&params)
}

func (r *RuntimeRouter) listAccountSessions(params *auth.AccountSessionsListParams) (*auth.AccountSessionsResponse, error) {
	empty := &auth.AccountSessionsResponse{Sessions: []auth.Session{}}
	if r == nil || r.externalChatGPTAuthActive() {
		return empty, nil
	}
	refresh := params != nil && params.RefreshWorkspaceMetadata
	snapshot := r.cachedAccountAuthSnapshot(refresh)
	sessionID, ok := auth.ManagedAccountSessionID(snapshot)
	if !ok {
		return empty, nil
	}
	accountID := strings.TrimSpace(auth.AccountIDFromAuthForRestrictions(snapshot))
	workspaces := []auth.AccountSessionWorkspace{}
	if accountID != "" {
		workspaces = append(workspaces, auth.AccountSessionWorkspace{AccountID: accountID})
	}
	if refresh {
		accounts, err := r.workspaceMemberships(snapshot)
		if err != nil {
			return nil, err
		}
		workspaces = workspaceSessionEntries(accounts)
		current := r.cachedAccountAuthSnapshot(false)
		currentSession, _ := auth.ManagedAccountSessionID(current)
		if r.externalChatGPTAuthActive() ||
			currentSession != sessionID ||
			strings.TrimSpace(auth.AccountIDFromAuthForRestrictions(current)) != accountID {
			return nil, jsonRPCInvalidRequest("Authentication changed while listing account sessions")
		}
	}
	session := auth.Session{
		SessionID:                  sessionID,
		UserID:                     stringPtrIfNotEmpty(auth.ChatGPTUserIDFromAuth(snapshot)),
		LastUsedAt:                 time.Now().Unix(),
		IsActive:                   true,
		SelectedWorkspaceAccountID: stringPtrIfNotEmpty(accountID),
		Workspaces:                 workspaces,
	}
	if account := auth.AccountFromAuth(snapshot); account != nil {
		session.Email = account.Email
	}
	return &auth.AccountSessionsResponse{
		ActiveSessionID: &sessionID,
		Sessions:        []auth.Session{session},
	}, nil
}

// cachedAccountAuthSnapshot mirrors Rust AuthManager::auth_cached: the account
// manager's credential, falling back to the stored credential on first use.
// With refresh it mirrors AuthManager::auth(): a managed ChatGPT credential
// inside the proactive refresh window is refreshed before the caller captures
// the snapshot, so a workspace exchange never starts from an expired token.
func (r *RuntimeRouter) cachedAccountAuthSnapshot(refresh bool) *auth.AuthDotJSON {
	if r == nil {
		return nil
	}
	snapshot := r.requireAccount().AuthSnapshot()
	if snapshot == nil {
		resolved, err := r.resolveAuthWithLoginRestrictions(r.codexHomeForRollout())
		if err != nil || resolved == nil {
			return nil
		}
		loaded := resolved.Auth
		r.requireAccount().ApplyAuthSnapshot(&loaded)
		snapshot = &loaded
	}
	if !refresh {
		return snapshot
	}
	if refreshed := r.proactiveRefreshManagedChatGPT(
		context.Background(),
		r.codexHomeForRollout(),
		r.configForWorkspaceSwitch(),
		snapshot,
	); refreshed != nil {
		return refreshed
	}
	return snapshot
}

func (r *RuntimeRouter) configForWorkspaceSwitch() *config.Config {
	if r == nil || r.services.Config == nil {
		return &config.Config{Values: map[string]any{}}
	}
	read, err := r.services.Config.Read(&config.ConfigReadParams{})
	if err != nil {
		return &config.Config{Values: map[string]any{}}
	}
	return config.NewConfigFromRead(read)
}

func (r *RuntimeRouter) workspaceMemberships(snapshot *auth.AuthDotJSON) (*chatgptapi.AccountsCheckResponse, error) {
	client, err := r.accountBackendClient(snapshot)
	if err != nil {
		return nil, errors.New("Failed to fetch workspace metadata")
	}
	ctx, cancel := context.WithTimeout(context.Background(), workspaceMembershipTimeout)
	defer cancel()
	accounts, err := client.GetAccountsCheck(ctx)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, errors.New("Workspace metadata request timed out")
		}
		return nil, errors.New("Failed to fetch workspace metadata")
	}
	return accounts, nil
}

func workspaceSessionEntries(accounts *chatgptapi.AccountsCheckResponse) []auth.AccountSessionWorkspace {
	if accounts == nil {
		return []auth.AccountSessionWorkspace{}
	}
	order := make(map[string]int, len(accounts.AccountOrdering))
	for index, accountID := range accounts.AccountOrdering {
		order[accountID] = index
	}
	workspaces := make([]auth.AccountSessionWorkspace, 0, len(accounts.Accounts))
	for _, account := range accounts.Accounts {
		workspace := auth.AccountSessionWorkspace{
			AccountID: account.ID,
			Name:      account.Name,
			ImageURL:  account.ProfilePictureURL,
		}
		switch account.Structure {
		case "personal":
			kind := auth.SessionWorkspacePersonal
			workspace.Kind = &kind
		case "workspace":
			kind := auth.SessionWorkspaceWorkspace
			workspace.Kind = &kind
		}
		workspaces = append(workspaces, workspace)
	}
	sortSessionWorkspaces(workspaces, order)
	return workspaces
}

func sortSessionWorkspaces(workspaces []auth.AccountSessionWorkspace, order map[string]int) {
	for i := 1; i < len(workspaces); i++ {
		for j := i; j > 0; j-- {
			left, leftOK := order[workspaces[j-1].AccountID]
			right, rightOK := order[workspaces[j].AccountID]
			if !leftOK {
				left = len(order) + 1
			}
			if !rightOK {
				right = len(order) + 1
			}
			if left <= right {
				break
			}
			workspaces[j-1], workspaces[j] = workspaces[j], workspaces[j-1]
		}
	}
}

func (r *RuntimeRouter) handleAccountSessionSwitch(request *Request) (*auth.AccountSessionsSwitchResponse, error) {
	var params auth.AccountSessionsSwitchParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	return r.switchAccountSession(&params, r.authStorageOriginatorForRequest(request))
}

func (r *RuntimeRouter) switchAccountSession(params *auth.AccountSessionsSwitchParams, originator string) (*auth.AccountSessionsSwitchResponse, error) {
	if r == nil || params == nil {
		return nil, jsonRPCInvalidRequest("Authentication is unavailable")
	}
	if r.activeLoginInProgress() {
		return nil, jsonRPCInvalidRequest("Finish or cancel the current login before switching workspaces")
	}
	if err := r.validateAccountWorkspaceSwitch(params); err != nil {
		return nil, err
	}
	current := r.cachedAccountAuthSnapshot(false)
	if strings.TrimSpace(auth.AccountIDFromAuthForRestrictions(current)) == strings.TrimSpace(params.AccountID) {
		return r.accountSessionSwitchResponse(params)
	}
	if current == nil {
		return nil, jsonRPCInvalidRequest("Authentication is unavailable")
	}
	memberships, err := r.workspaceMemberships(current)
	if err != nil {
		return nil, err
	}
	target, targetOK := uniqueWorkspaceEntry(memberships, params.AccountID)
	if !targetOK || target.IsDeactivated {
		return nil, jsonRPCInvalidRequest("The selected workspace is unavailable")
	}
	// The exchange strips the FedRAMP hint from the server session's token
	// templates.
	if auth.IsFedrampAccount(current) ||
		target.IsFedrampCompliant ||
		(target.WorkspaceBackendOrigin != nil && *target.WorkspaceBackendOrigin == "https://gov.chatgpt.com") {
		return nil, jsonRPCInvalidRequest("Sign in to switch government workspaces")
	}
	if err := r.validateAccountWorkspaceSwitch(params); err != nil {
		return nil, err
	}
	if target.CanAccessWithSession != nil && !*target.CanAccessWithSession {
		if !params.AllowBrowserLogin {
			return nil, jsonRPCInvalidRequest("The selected workspace requires a new sign-in")
		}
		return r.startWorkspaceBrowserLogin(current, params, target, originator)
	}
	pause, err := r.threads.PauseTurnAdmission(context.Background())
	if err != nil {
		return nil, err
	}
	defer pause.Release()
	if err := r.unloadForWorkspaceSwitch(context.Background()); err != nil {
		return nil, err
	}
	// Rust refresh_workspace_config -> AuthManager::switch_workspace refreshes
	// a managed credential before exchanging it, then validates the refreshed
	// snapshot against the same session and workspace.
	switchSnapshot := r.cachedAccountAuthSnapshot(true)
	switched, err := auth.SwitchWorkspace(
		context.Background(),
		r.authStore(r.codexHomeForRollout()),
		r.workspaceSwitchHTTPClient(),
		r.chatGPTBaseURL(),
		switchSnapshot,
		params.SessionID,
		params.AccountID,
		r.workspaceSwitchAllowedWorkspaces(),
		r.externalChatGPTAuthActive(),
	)
	if err != nil {
		return nil, jsonRPCInvalidRequest(err.Error())
	}
	selected := switched.SelectedAuth()
	r.requireAccount().ApplyAuthSnapshot(selected)
	r.reloadWorkspaceConfigForSwitch()
	// Routing includes fatal authorization and policy checks. Restore the
	// previous login on any failure, unless another auth operation replaced it.
	if _, routingErr := r.workspaceRoutingForAccountRead(context.Background(), selected); routingErr != nil {
		rollbackErr := auth.RollbackWorkspaceSwitch(context.Background(), switched, r.externalChatGPTAuthActive())
		r.requireAccount().ApplyAuthSnapshot(switched.PreviousAuth())
		r.reloadWorkspaceConfigForSwitch()
		if rollbackErr != nil {
			return nil, errors.New("Workspace validation failed: " + routingErr.Error() + "; could not restore the previous workspace: " + rollbackErr.Error())
		}
		return nil, errors.New("Workspace validation failed: " + routingErr.Error())
	}
	r.configureMCPFromConfig()
	r.maybeStartCuratedRepoSync(false)
	r.refreshPluginCachesAndMCPRuntimes()
	r.noteAuthChanged()
	r.retireRemoteControlForAuthChange(context.Background())
	r.notify(NotificationAccountUpdated, r.requireAccount().AccountUpdated())
	return r.accountSessionSwitchResponse(params)
}

func (r *RuntimeRouter) startWorkspaceBrowserLogin(snapshot *auth.AuthDotJSON, params *auth.AccountSessionsSwitchParams, target chatgptapi.AccountsCheckEntry, originator string) (*auth.AccountSessionsSwitchResponse, error) {
	connection := ""
	if target.SSOConnectionName != nil {
		connection = *target.SSOConnectionName
	}
	login, err := auth.NewWorkspaceLogin(snapshot, r.codexHomeForRollout(), r.authStoreOptionsForOriginator(originator), params.SessionID, params.AccountID, connection)
	if err != nil {
		return nil, jsonRPCInvalidRequest(err.Error())
	}
	pause, err := r.threads.PauseTurnAdmission(context.Background())
	if err != nil {
		return nil, err
	}
	if err := r.unloadForWorkspaceSwitch(context.Background()); err != nil {
		pause.Release()
		return nil, err
	}
	loginParams := auth.LoginAccountParams{Type: auth.AccountChatGPT}
	response, err := r.requireAccount().Login(&loginParams)
	if err != nil {
		pause.Release()
		return nil, err
	}
	options := r.accountOAuthOptionsForOriginator(originator)
	options.WorkspaceLogin = login
	options.ForcedWorkspaces = []string{strings.TrimSpace(params.AccountID)}
	if err := r.startAccountLoginRuntimeWithOptions(&loginParams, response, options, pause); err != nil {
		_, _ = r.requireAccount().CancelLogin(&auth.CancelLoginAccountParams{LoginID: response.LoginID})
		pause.Release()
		return nil, err
	}
	return &auth.AccountSessionsSwitchResponse{
		BrowserLogin: &auth.AccountBrowserLogin{LoginID: response.LoginID, AuthURL: response.AuthURL},
	}, nil
}

func uniqueWorkspaceEntry(accounts *chatgptapi.AccountsCheckResponse, accountID string) (chatgptapi.AccountsCheckEntry, bool) {
	if accounts == nil {
		return chatgptapi.AccountsCheckEntry{}, false
	}
	accountID = strings.TrimSpace(accountID)
	var found chatgptapi.AccountsCheckEntry
	count := 0
	for _, account := range accounts.Accounts {
		if account.ID != accountID {
			continue
		}
		found = account
		count++
	}
	return found, count == 1
}

func (r *RuntimeRouter) validateAccountWorkspaceSwitch(params *auth.AccountSessionsSwitchParams) error {
	snapshot := r.cachedAccountAuthSnapshot(false)
	if err := auth.ValidateWorkspaceSwitch(
		snapshot,
		params.SessionID,
		params.AccountID,
		r.workspaceSwitchAllowedWorkspaces(),
		r.externalChatGPTAuthActive(),
	); err != nil {
		return jsonRPCInvalidRequest(err.Error())
	}
	return nil
}

func (r *RuntimeRouter) workspaceSwitchAllowedWorkspaces() []string {
	if r == nil || r.services.Config == nil {
		return nil
	}
	read, err := r.services.Config.Read(&config.ConfigReadParams{})
	if err != nil || read == nil {
		return nil
	}
	cfg := config.Config{Values: read.Config}
	return normalizedWorkspaceQueryList(cfg.ForcedChatGPTWorkspaceIDs())
}

func (r *RuntimeRouter) workspaceSwitchHTTPClient() auth.WorkspaceHTTPClient {
	return r.accountHTTPClient()
}

func (r *RuntimeRouter) reloadWorkspaceConfigForSwitch() {
	if r == nil {
		return
	}
	r.invalidatePendingAuthReads()
	r.modelCatalogInvalidated = true
	r.configureMCPFromConfig()
	r.mcpRuntimes.invalidateAll()
}

// unloadForWorkspaceSwitch cancels the running turns and unloads every thread
// while turn admission is paused, so resuming a thread rebuilds policy and
// transport state for the new workspace.
func (r *RuntimeRouter) unloadForWorkspaceSwitch(ctx context.Context) error {
	if r == nil || r.threads == nil {
		return nil
	}
	for _, active := range r.threads.ActiveTurns() {
		if active != nil && active.Cancel != nil {
			active.Cancel()
		}
	}
	deadline := time.Now().Add(10 * time.Second)
	for len(r.threads.ActiveTurns()) > 0 {
		if ctx != nil && ctx.Err() != nil {
			return errors.New("Turn admission is unavailable")
		}
		if time.Now().After(deadline) {
			return errors.New("Some threads are still running; retry the workspace switch")
		}
		time.Sleep(10 * time.Millisecond)
	}
	closed := r.threads.LiveThreadIDs()
	if err := r.threads.CloseLiveThreads(); err != nil {
		return err
	}
	for _, threadID := range closed {
		r.notify(NotificationThreadClosed, ThreadClosedNotification{ThreadID: string(threadID)})
	}
	// A delegated child may have started while its parent was shutting down.
	// Never replace credentials until every runtime has stopped.
	if len(r.threads.LiveThreadIDs()) > 0 {
		return errors.New("Some threads are still running; retry the workspace switch")
	}
	return nil
}

func (r *RuntimeRouter) activeLoginInProgress() bool {
	if r == nil {
		return false
	}
	r.loginRuntimeMu.Lock()
	defer r.loginRuntimeMu.Unlock()
	return len(r.loginRuntimeCancels) > 0
}

func (r *RuntimeRouter) accountSessionSwitchResponse(params *auth.AccountSessionsSwitchParams) (*auth.AccountSessionsSwitchResponse, error) {
	response, err := r.listAccountSessions(&auth.AccountSessionsListParams{})
	if err != nil {
		return nil, err
	}
	matched := false
	for _, session := range response.Sessions {
		if session.SessionID == params.SessionID &&
			session.SelectedWorkspaceAccountID != nil &&
			*session.SelectedWorkspaceAccountID == strings.TrimSpace(params.AccountID) {
			matched = true
			break
		}
	}
	if response.ActiveSessionID == nil || *response.ActiveSessionID != params.SessionID || !matched {
		return nil, jsonRPCInvalidRequest("Authentication changed during workspace switching")
	}
	return &auth.AccountSessionsSwitchResponse{Sessions: response}, nil
}

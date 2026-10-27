package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Rust parity: codex-rs/login/src/auth/manager/workspace_switching.rs (#57514).
//
// Switching a workspace keeps the managed login's refresh credential and only
// exchanges the access token. All credential writes are conditional so a
// concurrent login or logout can never be overwritten.

const workspaceSwitchTimeout = 30 * time.Second

// WorkspaceHTTPClient is the request executor used for the workspace exchange.
type WorkspaceHTTPClient interface {
	Do(request *http.Request) (*http.Response, error)
}

// WorkspaceSwitch records the credential snapshots a successful switch
// replaced. It never holds a credential in a form that is logged.
type WorkspaceSwitch struct {
	Previous *AuthDotJSON
	Selected *AuthDotJSON

	store      *Store
	httpClient WorkspaceHTTPClient
	baseURL    string
}

// SelectedAuth returns the credential the switch installed.
func (w *WorkspaceSwitch) SelectedAuth() *AuthDotJSON {
	if w == nil {
		return nil
	}
	return cloneAuthDotJSON(w.Selected)
}

// PreviousAuth returns the credential a rollback restores.
func (w *WorkspaceSwitch) PreviousAuth() *AuthDotJSON {
	if w == nil {
		return nil
	}
	return cloneAuthDotJSON(w.Previous)
}

// WorkspaceLogin binds a browser callback to the managed login that requested a
// workspace.
type WorkspaceLogin struct {
	CodexHome    string
	StoreOptions *StoreOptions
	Original     *AuthDotJSON
	WorkspaceID  string
	Email        string
	Connection   string
}

// ManagedAccountSessionID returns the stable identifier of a managed ChatGPT
// login. Older credentials predate login_owner_id and fall back to the user id.
func ManagedAccountSessionID(snapshot *AuthDotJSON) (string, bool) {
	if snapshot == nil || snapshot.Mode() != "chatgpt" || snapshot.Tokens == nil {
		return "", false
	}
	userID := strings.TrimSpace(ChatGPTClaimsFromJWT(stringFromAny(snapshot.Tokens, "id_token")).UserID)
	if userID == "" {
		userID = strings.TrimSpace(stringFromAny(snapshot.Tokens, "chatgpt_user_id"))
	}
	if userID == "" {
		return "", false
	}
	sessionID := strings.TrimSpace(stringFromAny(snapshot.Tokens, "login_owner_id"))
	if sessionID == "" {
		sessionID = userID
	}
	return sessionID, true
}

// ValidateWorkspaceSwitch checks local eligibility without changing credentials
// or an in-progress login.
func ValidateWorkspaceSwitch(snapshot *AuthDotJSON, sessionID string, workspaceID string, allowedWorkspaces []string, externalAuth bool) error {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return errors.New("accountId must not be empty")
	}
	if err := EnsureWorkspaceAccountAllowed(allowedWorkspaces, workspaceID); err != nil {
		return err
	}
	if snapshot == nil || snapshot.Mode() != "chatgpt" {
		return errors.New("Workspace switching requires a managed ChatGPT login")
	}
	if externalAuth {
		return errors.New("Workspace switching is owned by the external authentication provider")
	}
	if current, ok := ManagedAccountSessionID(snapshot); !ok || current != strings.TrimSpace(sessionID) {
		return errors.New("Only the active managed account session can be switched")
	}
	return nil
}

// errAuthenticationChangedDuringSwitch reports an exchange whose result must be
// discarded because the external authentication provider took over while the
// request was in flight (Rust `AuthManager::switch_workspace`).
var errAuthenticationChangedDuringSwitch = errors.New("Authentication changed during workspace switching")

// NewWorkspaceLogin prepares the browser sign-in binding for one workspace.
func NewWorkspaceLogin(snapshot *AuthDotJSON, codexHome string, storeOptions *StoreOptions, sessionID string, workspaceID string, connection string) (*WorkspaceLogin, error) {
	if strings.TrimSpace(workspaceID) == "" {
		return nil, errors.New("accountId must not be empty")
	}
	current, ok := ManagedAccountSessionID(snapshot)
	if !ok || snapshot.Tokens == nil {
		return nil, errors.New("Workspace sign-in requires a managed login")
	}
	if current != strings.TrimSpace(sessionID) {
		return nil, errors.New("The active login changed")
	}
	email := ""
	if account := AccountFromAuth(snapshot); account != nil && account.Email != nil {
		email = strings.TrimSpace(*account.Email)
	}
	return &WorkspaceLogin{
		CodexHome:    codexHome,
		StoreOptions: storeOptions,
		Original:     cloneAuthDotJSON(snapshot),
		WorkspaceID:  strings.TrimSpace(workspaceID),
		Email:        email,
		Connection:   strings.TrimSpace(connection),
	}, nil
}

// Save validates that the completed sign-in still belongs to the requesting
// login and workspace, then replaces the credential only if it is unchanged.
func (l *WorkspaceLogin) Save(tokens *ExchangedTokens) error {
	if l == nil || l.Original == nil || l.Original.Tokens == nil {
		return errors.New("Missing managed login")
	}
	if tokens == nil {
		return errors.New("Missing sign-in credentials")
	}
	previousUser := strings.TrimSpace(ChatGPTClaimsFromJWT(stringFromAny(l.Original.Tokens, "id_token")).UserID)
	if previousUser == "" {
		return errors.New("Missing managed login")
	}
	updated := authDotJSONFromExchangedTokens(tokens)
	idClaims := ChatGPTClaimsFromJWT(stringFromAny(updated.Tokens, "id_token"))
	accessClaims := ChatGPTClaimsFromJWT(stringFromAny(updated.Tokens, "access_token"))
	accountID := strings.TrimSpace(stringFromAny(updated.Tokens, "account_id"))
	if idClaims.UserID != previousUser ||
		accessClaims.UserID != previousUser ||
		accountID != l.WorkspaceID ||
		accessClaims.AccountID != accountID {
		return &WorkspaceLoginError{message: "Sign-in returned a different user or workspace"}
	}
	if owner := strings.TrimSpace(stringFromAny(l.Original.Tokens, "login_owner_id")); owner != "" {
		updated.Tokens["login_owner_id"] = owner
	} else {
		delete(updated.Tokens, "login_owner_id")
	}
	return NewStoreWithOptions(l.CodexHome, l.StoreOptions).ReplaceIfCurrent(l.Original, updated)
}

// WorkspaceLoginError marks a callback that authenticated a different identity
// than the one that requested the workspace.
type WorkspaceLoginError struct {
	message string
}

func (e *WorkspaceLoginError) Error() string {
	if e == nil {
		return ""
	}
	return e.message
}

// SwitchWorkspace exchanges the current session into another workspace without
// exporting credentials.
func SwitchWorkspace(ctx context.Context, store *Store, httpClient WorkspaceHTTPClient, chatGPTBaseURL string, snapshot *AuthDotJSON, sessionID string, workspaceID string, allowedWorkspaces []string, externalAuth bool) (*WorkspaceSwitch, error) {
	if store == nil {
		return nil, errors.New("Authentication is unavailable")
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if err := ValidateWorkspaceSwitch(snapshot, sessionID, workspaceID, allowedWorkspaces, externalAuth); err != nil {
		return nil, err
	}
	gate := accountRefreshGateFor(store.CodexHome)
	if err := gate.acquire(ctx); err != nil {
		return nil, errors.New("Account refresh coordination is unavailable.")
	}
	defer gate.release()
	if err := ValidateWorkspaceSwitch(snapshot, sessionID, workspaceID, allowedWorkspaces, externalAuth); err != nil {
		return nil, err
	}
	if snapshot.Tokens == nil {
		return nil, errors.New("Authentication is unavailable")
	}
	userID := strings.TrimSpace(ChatGPTClaimsFromJWT(stringFromAny(snapshot.Tokens, "id_token")).UserID)
	if userID == "" {
		userID = strings.TrimSpace(stringFromAny(snapshot.Tokens, "chatgpt_user_id"))
	}
	if userID == "" {
		return nil, errors.New("Authentication is missing a user identity")
	}
	if strings.TrimSpace(stringFromAny(snapshot.Tokens, "account_id")) == workspaceID {
		return &WorkspaceSwitch{
			Previous:   cloneAuthDotJSON(snapshot),
			Selected:   cloneAuthDotJSON(snapshot),
			store:      store,
			httpClient: httpClient,
			baseURL:    chatGPTBaseURL,
		}, nil
	}
	accessToken := strings.TrimSpace(stringFromAny(snapshot.Tokens, "access_token"))
	if accessToken == "" {
		return nil, errors.New("Authentication is unavailable")
	}
	exchangeCtx, cancel := context.WithTimeout(ctx, workspaceSwitchTimeout)
	defer cancel()
	newAccessToken, err := ExchangeWorkspaceToken(exchangeCtx, httpClient, chatGPTBaseURL, accessToken, workspaceID, userID)
	if err != nil {
		return nil, err
	}
	// The exchange rechecks the workspace allow-list and rejects an external
	// authentication provider that took over while the request was in flight
	// (Rust ensure_auth_workspace_allowed + has_external_auth).
	if err := EnsureWorkspaceAccountAllowed(allowedWorkspaces, workspaceID); err != nil {
		return nil, err
	}
	if externalAuth {
		return nil, errAuthenticationChangedDuringSwitch
	}
	updated := cloneAuthDotJSON(snapshot)
	updated.Tokens["access_token"] = newAccessToken
	updated.Tokens["account_id"] = workspaceID
	// The exchange does not renew the session or issue refresh/ID tokens.
	updated.AgentIdentity = nil
	if err := store.ReplaceIfCurrent(snapshot, *updated); err != nil {
		return nil, err
	}
	return &WorkspaceSwitch{
		Previous:   cloneAuthDotJSON(snapshot),
		Selected:   cloneAuthDotJSON(updated),
		store:      store,
		httpClient: httpClient,
		baseURL:    chatGPTBaseURL,
	}, nil
}

// RollbackWorkspaceSwitch restores a rejected workspace change only while its
// credentials are still current.
func RollbackWorkspaceSwitch(ctx context.Context, switched *WorkspaceSwitch, externalAuth bool) error {
	if switched == nil || switched.store == nil || switched.Selected == nil || switched.Previous == nil {
		return nil
	}
	gate := accountRefreshGateFor(switched.store.CodexHome)
	if err := gate.acquire(ctx); err != nil {
		return errors.New("Account refresh coordination is unavailable.")
	}
	defer gate.release()
	// Restore only while the switch's credentials are still current, and never
	// overwrite credentials owned by an external authentication provider
	// (Rust `rollback_workspace_switch`).
	if externalAuth {
		return errAccountCredentialsChanged
	}
	current, err := switched.store.LoadAccountGrant()
	if err != nil {
		return err
	}
	if !authDotJSONEqual(current, switched.Selected) {
		return errAccountCredentialsChanged
	}
	selectedTokens := nilSafeTokens(switched.Selected)
	previousTokens := nilSafeTokens(switched.Previous)
	previousWorkspace := strings.TrimSpace(stringFromAny(previousTokens, "account_id"))
	if previousWorkspace == "" {
		return errors.New("Missing previous workspace")
	}
	user := strings.TrimSpace(ChatGPTClaimsFromJWT(stringFromAny(selectedTokens, "id_token")).UserID)
	if user == "" {
		return errors.New("Missing selected user")
	}
	accessToken := strings.TrimSpace(stringFromAny(selectedTokens, "access_token"))
	exchangeCtx, cancel := context.WithTimeout(ctx, workspaceSwitchTimeout)
	defer cancel()
	// The exchange also selects the workspace used by the server on the next
	// refresh.
	if _, err := ExchangeWorkspaceToken(exchangeCtx, switched.httpClient, switched.baseURL, accessToken, previousWorkspace, user); err != nil {
		return err
	}
	return switched.store.ReplaceIfCurrent(switched.Selected, *switched.Previous)
}

// ExchangeWorkspaceToken asks the backend for an access token bound to another
// workspace of the same user.
func ExchangeWorkspaceToken(ctx context.Context, httpClient WorkspaceHTTPClient, chatGPTBaseURL string, accessToken string, workspaceID string, userID string) (string, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(chatGPTBaseURL), "/")
	if baseURL == "" {
		baseURL = "https://chatgpt.com/backend-api"
	}
	payload, err := json.Marshal(map[string]string{"workspace_id": workspaceID})
	if err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/accounts/switch-workspace-token", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	request.Header.Set("Authorization", "Bearer "+accessToken)
	request.Header.Set("Content-Type", "application/json")
	client := httpClient
	if client == nil {
		client = &http.Client{Timeout: workspaceSwitchTimeout}
	}
	response, err := workspaceExchangeClient(client).Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("Workspace switch was rejected (HTTP %s)", response.Status)
	}
	var tokenResponse struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &tokenResponse); err != nil {
		return "", err
	}
	claims := ChatGPTClaimsFromJWT(tokenResponse.AccessToken)
	if claims.AccountID != workspaceID || claims.UserID != userID {
		return "", errors.New("Workspace exchange returned a different user or workspace")
	}
	return tokenResponse.AccessToken, nil
}

// workspaceExchangeClient mirrors Rust's auth-route client: redirects are not
// followed, so a redirect cannot forward the bearer token to another origin.
func workspaceExchangeClient(doer WorkspaceHTTPClient) WorkspaceHTTPClient {
	client, ok := doer.(*http.Client)
	if !ok || client == nil {
		return doer
	}
	cloned := *client
	cloned.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &cloned
}

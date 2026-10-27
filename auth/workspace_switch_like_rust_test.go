package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Rust parity: codex-rs/login/src/auth/manager/workspace_switching.rs (#57514).

func workspaceSwitchSnapshot(accountID string) *AuthDotJSON {
	return &AuthDotJSON{
		AuthMode: "chatgpt",
		Tokens: map[string]any{
			"id_token":       fakeJWTAccount(map[string]any{"chatgpt_account_id": accountID, "chatgpt_user_id": "user-1"}),
			"access_token":   "access-" + accountID,
			"refresh_token":  "refresh-1",
			"account_id":     accountID,
			"login_owner_id": "login-1",
		},
	}
}

func TestManagedAccountSessionIDLikeRust(t *testing.T) {
	snapshot := workspaceSwitchSnapshot("workspace-a")
	if session, ok := ManagedAccountSessionID(snapshot); !ok || session != "login-1" {
		t.Fatalf("ManagedAccountSessionID() = %q, %t", session, ok)
	}
	delete(snapshot.Tokens, "login_owner_id")
	if session, ok := ManagedAccountSessionID(snapshot); !ok || session != "user-1" {
		t.Fatalf("legacy session id = %q, %t", session, ok)
	}
	apiKey := &AuthDotJSON{AuthMode: "api-key", OpenAIAPIKey: "sk-test"}
	if _, ok := ManagedAccountSessionID(apiKey); ok {
		t.Fatal("API key auth must not expose a managed session")
	}
}

func TestSwitchWorkspaceExchangesTokenAndPreservesRefreshLikeRust(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)
	snapshot := workspaceSwitchSnapshot("workspace-a")
	if err := store.Save(*snapshot); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/backend-api/accounts/switch-workspace-token" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer access-workspace-a" {
			t.Fatalf("Authorization = %q", got)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["workspace_id"] != "workspace-b" {
			t.Fatalf("workspace_id = %q", body["workspace_id"])
		}
		_, _ = w.Write([]byte(`{"access_token":"` + fakeJWTAccount(map[string]any{"chatgpt_account_id": "workspace-b", "chatgpt_user_id": "user-1"}) + `"}`))
	}))
	defer server.Close()

	switched, err := SwitchWorkspace(context.Background(), store, server.Client(), server.URL+"/backend-api", snapshot, "login-1", "workspace-b", nil)
	if err != nil {
		t.Fatalf("SwitchWorkspace() error = %v", err)
	}
	if switched.SelectedAuth().Tokens["account_id"] != "workspace-b" {
		t.Fatalf("selected account = %v", switched.SelectedAuth().Tokens["account_id"])
	}
	stored, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if stored.Tokens["account_id"] != "workspace-b" || stored.Tokens["refresh_token"] != "refresh-1" || stored.Tokens["login_owner_id"] != "login-1" {
		t.Fatalf("stored tokens = %#v", stored.Tokens)
	}
	if stored.Tokens["access_token"] == "access-workspace-a" {
		t.Fatal("access token was not exchanged")
	}
}

func TestSwitchWorkspaceRejectsStaleSessionLikeRust(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)
	snapshot := workspaceSwitchSnapshot("workspace-a")
	if err := store.Save(*snapshot); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	_, err := SwitchWorkspace(context.Background(), store, nil, "https://chatgpt.com/backend-api", snapshot, "login-other", "workspace-b", nil)
	if err == nil || err.Error() != "Only the active managed account session can be switched" {
		t.Fatalf("stale session error = %v", err)
	}
}

func TestRollbackWorkspaceSwitchRestoresPreviousLikeRust(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)
	snapshot := workspaceSwitchSnapshot("workspace-a")
	if err := store.Save(*snapshot); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		_, _ = w.Write([]byte(`{"access_token":"` + fakeJWTAccount(map[string]any{"chatgpt_account_id": body["workspace_id"], "chatgpt_user_id": "user-1"}) + `"}`))
	}))
	defer server.Close()

	switched, err := SwitchWorkspace(context.Background(), store, server.Client(), server.URL, snapshot, "login-1", "workspace-b", nil)
	if err != nil {
		t.Fatalf("SwitchWorkspace() error = %v", err)
	}
	if err := RollbackWorkspaceSwitch(context.Background(), switched); err != nil {
		t.Fatalf("RollbackWorkspaceSwitch() error = %v", err)
	}
	stored, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if stored.Tokens["account_id"] != "workspace-a" || stored.Tokens["access_token"] != "access-workspace-a" {
		t.Fatalf("restored tokens = %#v", stored.Tokens)
	}
}

func TestWorkspaceLoginSaveBindsUserAndWorkspaceLikeRust(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)
	snapshot := workspaceSwitchSnapshot("workspace-a")
	if err := store.Save(*snapshot); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	login, err := NewWorkspaceLogin(snapshot, home, nil, "login-1", "workspace-b", "connection-1")
	if err != nil {
		t.Fatalf("NewWorkspaceLogin() error = %v", err)
	}
	if login.Email != "" || login.Connection != "connection-1" {
		t.Fatalf("login = %+v", login)
	}
	tokens := &ExchangedTokens{
		IDToken:      fakeJWTAccount(map[string]any{"chatgpt_account_id": "workspace-b", "chatgpt_user_id": "user-1"}),
		AccessToken:  fakeJWTAccount(map[string]any{"chatgpt_account_id": "workspace-b", "chatgpt_user_id": "user-1"}),
		RefreshToken: "refresh-b",
		Issuer:       "https://auth.example",
		ClientID:     "client-1",
	}
	if err := login.Save(tokens); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	stored, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if stored.Tokens["account_id"] != "workspace-b" || stored.Tokens["login_owner_id"] != "login-1" {
		t.Fatalf("stored tokens = %#v", stored.Tokens)
	}

	wrong := &ExchangedTokens{
		IDToken:     fakeJWTAccount(map[string]any{"chatgpt_account_id": "workspace-a", "chatgpt_user_id": "user-1"}),
		AccessToken: fakeJWTAccount(map[string]any{"chatgpt_account_id": "workspace-a", "chatgpt_user_id": "user-1"}),
	}
	var loginErr *WorkspaceLoginError
	if err := login.Save(wrong); err == nil || !asWorkspaceLoginError(err, &loginErr) {
		t.Fatalf("wrong workspace error = %v", err)
	}
}

func asWorkspaceLoginError(err error, target **WorkspaceLoginError) bool {
	typed, ok := err.(*WorkspaceLoginError)
	if ok {
		*target = typed
	}
	return ok
}

func TestWorkspaceLoginSaveRejectsStaleLoginLikeRust(t *testing.T) {
	snapshot := workspaceSwitchSnapshot("workspace-a")
	if _, err := NewWorkspaceLogin(snapshot, t.TempDir(), nil, "login-other", "workspace-b", ""); err == nil || !strings.Contains(err.Error(), "The active login changed") {
		t.Fatalf("stale login error = %v", err)
	}
}

func TestRefreshPreservesSelectedWorkspaceLikeRust(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)
	expected := workspaceSwitchSnapshot("workspace-b")
	if err := store.Save(*expected); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	refreshedIDToken := fakeJWTAccount(map[string]any{"chatgpt_account_id": "workspace-a", "chatgpt_user_id": "user-1"})
	if err := persistRefreshedChatGPTTokens(home, expected, &ExchangedTokens{
		IDToken:      refreshedIDToken,
		AccessToken:  "new-access",
		RefreshToken: "refresh-2",
	}, nil); err != nil {
		t.Fatalf("persistRefreshedChatGPTTokens() error = %v", err)
	}
	stored, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if stored.Tokens["account_id"] != "workspace-b" {
		t.Fatalf("selected workspace after refresh = %v", stored.Tokens["account_id"])
	}
	if stored.Tokens["refresh_token"] != "refresh-2" || stored.Tokens["access_token"] != "new-access" {
		t.Fatalf("refreshed tokens = %#v", stored.Tokens)
	}
}

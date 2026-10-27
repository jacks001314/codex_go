package appserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"codex_go/auth"
	"codex_go/config"
	"codex_go/session"
)

// Rust parity: codex-rs/app-server/tests/suite/v2/account_workspace_switching.rs
// (#57514).

type workspaceSwitchBackend struct {
	exchanges       atomic.Int32
	memberships     string
	membershipQueue []string
	exchangeAccount func(workspaceID string) string
	onMembership    func()
}

func (b *workspaceSwitchBackend) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/wham/accounts/check"):
			if b.onMembership != nil {
				b.onMembership()
			}
			body := b.memberships
			if len(b.membershipQueue) > 0 {
				body = b.membershipQueue[0]
				b.membershipQueue = b.membershipQueue[1:]
			}
			_, _ = w.Write([]byte(body))
		case strings.HasSuffix(r.URL.Path, "/accounts/switch-workspace-token"):
			b.exchanges.Add(1)
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode switch body: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			accountID := body["workspace_id"]
			if b.exchangeAccount != nil {
				accountID = b.exchangeAccount(accountID)
			}
			_, _ = w.Write([]byte(`{"access_token":"` + fakeJWTAppserver(map[string]any{
				"chatgpt_account_id": accountID,
				"chatgpt_user_id":    "user-1",
			}) + `"}`))
		default:
			t.Errorf("unexpected path = %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

func workspaceSwitchFixture(t *testing.T, backend *workspaceSwitchBackend, accountID string) (*RuntimeRouter, *NotificationBuffer, string) {
	t.Helper()
	clearAuthEnvAppserver(t)
	home := t.TempDir()
	snapshot := auth.AuthDotJSON{
		AuthMode: "chatgpt",
		Tokens: map[string]any{
			"id_token":       fakeJWTAppserver(map[string]any{"chatgpt_account_id": accountID, "chatgpt_user_id": "user-1"}),
			"access_token":   "access-" + accountID,
			"refresh_token":  "refresh-1",
			"account_id":     accountID,
			"login_owner_id": "login-1",
		},
	}
	if err := auth.NewStore(home).Save(snapshot); err != nil {
		t.Fatalf("save auth: %v", err)
	}
	server := httptest.NewServer(backend.handler(t))
	t.Cleanup(server.Close)
	baseURL := server.URL + "/backend-api"
	if err := os.WriteFile(config.ConfigPath(home), []byte("chatgpt_base_url = \""+baseURL+"\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		Account: auth.NewAccountManager(),
		Config:  config.NewConfigService(home),
	})
	sink := NewNotificationBuffer()
	router.SetNotificationSink(sink)
	return router, sink, home
}

func initializeExperimentalConnection(t *testing.T, router *RuntimeRouter, connectionID string) {
	t.Helper()
	initialize := requestWithParams(t, IntID(1), MethodInitialize, InitializeParams{
		ClientInfo:   ClientInfo{Name: "codex-test", Version: "0.1.0"},
		Capabilities: &InitializeCapabilities{ExperimentalAPI: true},
	})
	initialize.ConnectionID = connectionID
	if response := router.Handle(initialize); response.Error != nil {
		t.Fatalf("initialize error: %+v", response.Error)
	}
}

func TestAccountSessionListRefreshLikeRust(t *testing.T) {
	backend := &workspaceSwitchBackend{memberships: `{
		"accounts": {
			"workspace-b": {"account": {"account_id": "workspace-b", "name": "Bravo", "structure": "workspace"}},
			"workspace-a": {"account": {"account_id": "workspace-a", "name": "Alpha", "structure": "personal"}}
		},
		"account_ordering": ["workspace-a", "workspace-b"]
	}`}
	router, _, _ := workspaceSwitchFixture(t, backend, "workspace-a")
	initializeExperimentalConnection(t, router, "conn-ws")

	list := requestWithParams(t, IntID(2), MethodAccountSessionList, auth.AccountSessionsListParams{RefreshWorkspaceMetadata: true})
	list.ConnectionID = "conn-ws"
	response := router.Handle(list)
	if response.Error != nil {
		t.Fatalf("accountSession/list = %+v", response.Error)
	}
	result := response.Result.(*auth.AccountSessionsResponse)
	if result.ActiveSessionID == nil || *result.ActiveSessionID != "login-1" || len(result.Sessions) != 1 {
		t.Fatalf("list result = %+v", result)
	}
	session := result.Sessions[0]
	if session.SelectedWorkspaceAccountID == nil || *session.SelectedWorkspaceAccountID != "workspace-a" {
		t.Fatalf("selected workspace = %+v", session.SelectedWorkspaceAccountID)
	}
	if len(session.Workspaces) != 2 || session.Workspaces[0].AccountID != "workspace-a" || session.Workspaces[1].AccountID != "workspace-b" {
		t.Fatalf("workspaces = %+v", session.Workspaces)
	}
	if session.Workspaces[0].Kind == nil || *session.Workspaces[0].Kind != auth.SessionWorkspacePersonal {
		t.Fatalf("personal kind = %+v", session.Workspaces[0].Kind)
	}
	if session.Workspaces[1].Name == nil || *session.Workspaces[1].Name != "Bravo" {
		t.Fatalf("workspace name = %+v", session.Workspaces[1].Name)
	}
}

func TestAccountSessionListRefreshRejectsConcurrentLoginLikeRust(t *testing.T) {
	backend := &workspaceSwitchBackend{memberships: `{"accounts": [{"id": "workspace-a"}]}`}
	router, _, _ := workspaceSwitchFixture(t, backend, "workspace-a")
	initializeExperimentalConnection(t, router, "conn-ws")
	// The account changed while the workspace list was in flight.
	backend.onMembership = func() {
		router.requireAccount().ApplyAuthSnapshot(&auth.AuthDotJSON{
			AuthMode: "chatgpt",
			Tokens: map[string]any{
				"id_token":       fakeJWTAppserver(map[string]any{"chatgpt_account_id": "workspace-b", "chatgpt_user_id": "user-2"}),
				"access_token":   "access-workspace-b",
				"refresh_token":  "refresh-2",
				"account_id":     "workspace-b",
				"login_owner_id": "login-2",
			},
		})
	}
	list := requestWithParams(t, IntID(2), MethodAccountSessionList, auth.AccountSessionsListParams{RefreshWorkspaceMetadata: true})
	list.ConnectionID = "conn-ws"
	response := router.Handle(list)
	if response.Error == nil || response.Error.Message != "Authentication changed while listing account sessions" {
		t.Fatalf("accountSession/list = %+v", response.Error)
	}
}

func TestAccountSessionSwitchToCurrentWorkspaceKeepsThreadsLikeRust(t *testing.T) {
	backend := &workspaceSwitchBackend{memberships: `{"accounts": [{"id": "workspace-a"}]}`}
	router, sink, home := workspaceSwitchFixture(t, backend, "workspace-a")
	initializeExperimentalConnection(t, router, "conn-ws")

	store := session.NewStore(filepath.Join(home, "sessions"))
	now := time.Now().UTC()
	record := &session.Record{ID: "thread-1", SessionID: "thread-1", CreatedAt: now, UpdatedAt: now, RecencyAt: now}
	if err := store.Create(record); err != nil {
		t.Fatalf("create thread: %v", err)
	}
	router.threads = NewThreadManager(NewThreadStatusManager())
	if err := router.threads.RetainLiveThread(store, record); err != nil {
		t.Fatalf("retain thread: %v", err)
	}

	request := requestWithParams(t, IntID(2), MethodAccountSessionSwitch, auth.AccountSessionsSwitchParams{
		SessionID: "login-1",
		AccountID: "workspace-a",
	})
	request.ConnectionID = "conn-ws"
	response := router.Handle(request)
	if response.Error != nil {
		t.Fatalf("accountSession/switch = %+v", response.Error)
	}
	result := response.Result.(*auth.AccountSessionsSwitchResponse)
	if result.Sessions == nil || len(result.Sessions.Sessions) != 1 {
		t.Fatalf("switch result = %+v", result)
	}
	if backend.exchanges.Load() != 0 {
		t.Fatalf("selecting the current workspace exchanged a token %d times", backend.exchanges.Load())
	}
	if len(router.threads.LiveThreadIDs()) != 1 {
		t.Fatalf("selecting the current workspace unloaded threads: %+v", router.threads.LiveThreadIDs())
	}
	for _, notification := range sink.List() {
		if notification.Method == NotificationThreadClosed {
			t.Fatalf("selecting the current workspace closed a thread: %+v", notification)
		}
	}
}

func TestAccountSessionSwitchReplacesCredentialAndUnloadsThreadsLikeRust(t *testing.T) {
	backend := &workspaceSwitchBackend{membershipQueue: []string{
		`{"accounts": [
			{"id": "workspace-a", "can_access_with_session": true},
			{"id": "workspace-b", "can_access_with_session": true, "workspace_backend_origin": "https://chatgpt.com", "account_routing_override": "NO_CONSTRAINT"}
		]}`,
		`{"accounts": [{"id": "workspace-b", "workspace_backend_origin": "https://chatgpt.com", "account_routing_override": "NO_CONSTRAINT"}]}`,
	}}
	router, sink, home := workspaceSwitchFixture(t, backend, "workspace-a")
	initializeExperimentalConnection(t, router, "conn-ws")

	store := session.NewStore(filepath.Join(home, "sessions"))
	now := time.Now().UTC()
	record := &session.Record{ID: "thread-1", SessionID: "thread-1", CreatedAt: now, UpdatedAt: now, RecencyAt: now}
	if err := store.Create(record); err != nil {
		t.Fatalf("create thread: %v", err)
	}
	router.threads = NewThreadManager(NewThreadStatusManager())
	if err := router.threads.RetainLiveThread(store, record); err != nil {
		t.Fatalf("retain thread: %v", err)
	}

	request := requestWithParams(t, IntID(2), MethodAccountSessionSwitch, auth.AccountSessionsSwitchParams{
		SessionID: "login-1",
		AccountID: "workspace-b",
	})
	request.ConnectionID = "conn-ws"
	response := router.Handle(request)
	if response.Error != nil {
		t.Fatalf("accountSession/switch = %+v", response.Error)
	}
	result := response.Result.(*auth.AccountSessionsSwitchResponse)
	if result.Sessions == nil || len(result.Sessions.Sessions) != 1 {
		t.Fatalf("switch result = %+v", result)
	}
	selected := result.Sessions.Sessions[0].SelectedWorkspaceAccountID
	if selected == nil || *selected != "workspace-b" {
		t.Fatalf("selected workspace = %+v", selected)
	}
	if backend.exchanges.Load() != 1 {
		t.Fatalf("token exchanges = %d", backend.exchanges.Load())
	}
	if len(router.threads.LiveThreadIDs()) != 0 {
		t.Fatalf("threads still live: %+v", router.threads.LiveThreadIDs())
	}
	closed := latestNotificationPayload[ThreadClosedNotification](t, sink, NotificationThreadClosed)
	if closed.ThreadID != "thread-1" {
		t.Fatalf("thread/closed payload = %+v", closed)
	}
	if !sinkHasMethod(sink, NotificationAccountUpdated) {
		t.Fatalf("account/updated missing: %+v", sink.List())
	}
	stored, err := auth.NewStore(home).Load()
	if err != nil || stored == nil {
		t.Fatalf("load stored auth = %+v, %v", stored, err)
	}
	if stored.Tokens["account_id"] != "workspace-b" || stored.Tokens["refresh_token"] != "refresh-1" {
		t.Fatalf("stored tokens = %#v", stored.Tokens)
	}
}

func TestAccountSessionSwitchRejectionsLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		membership string
		auth       string
		params     auth.AccountSessionsSwitchParams
		want       string
	}{
		{
			name:       "deactivated workspace",
			membership: `{"accounts": [{"id": "workspace-b", "is_deactivated": true}]}`,
			auth:       "workspace-a",
			params:     auth.AccountSessionsSwitchParams{SessionID: "login-1", AccountID: "workspace-b"},
			want:       "The selected workspace is unavailable",
		},
		{
			name:       "government workspace",
			membership: `{"accounts": [{"id": "workspace-b", "is_fedramp_compliant_workspace": true}]}`,
			auth:       "workspace-a",
			params:     auth.AccountSessionsSwitchParams{SessionID: "login-1", AccountID: "workspace-b"},
			want:       "Sign in to switch government workspaces",
		},
		{
			name:       "missing sign-in",
			membership: `{"accounts": [{"id": "workspace-b", "can_access_with_session": false}]}`,
			auth:       "workspace-a",
			params:     auth.AccountSessionsSwitchParams{SessionID: "login-1", AccountID: "workspace-b"},
			want:       "The selected workspace requires a new sign-in",
		},
		{
			name:       "stale session",
			membership: `{"accounts": [{"id": "workspace-b"}]}`,
			auth:       "workspace-a",
			params:     auth.AccountSessionsSwitchParams{SessionID: "login-other", AccountID: "workspace-b"},
			want:       "Only the active managed account session can be switched",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			backend := &workspaceSwitchBackend{memberships: testCase.membership}
			router, _, _ := workspaceSwitchFixture(t, backend, testCase.auth)
			initializeExperimentalConnection(t, router, "conn-ws")
			request := requestWithParams(t, IntID(2), MethodAccountSessionSwitch, testCase.params)
			request.ConnectionID = "conn-ws"
			response := router.Handle(request)
			if response.Error == nil || response.Error.Code != JSONRPCInvalidRequestErrorCode || response.Error.Message != testCase.want {
				t.Fatalf("switch error = %+v, want %q", response.Error, testCase.want)
			}
			if backend.exchanges.Load() != 0 {
				t.Fatalf("rejected switch exchanged a token")
			}
		})
	}
}

func TestAccountSessionSwitchBrowserLoginRequiresAllowLikeRust(t *testing.T) {
	backend := &workspaceSwitchBackend{memberships: `{"accounts": [
		{"id": "workspace-b", "can_access_with_session": false, "sso_connection_name": "sso-1"}
	]}`}
	router, _, _ := workspaceSwitchFixture(t, backend, "workspace-a")
	initializeExperimentalConnection(t, router, "conn-ws")
	t.Cleanup(func() { router.cancelAllAccountLoginRuntimes() })

	request := requestWithParams(t, IntID(2), MethodAccountSessionSwitch, auth.AccountSessionsSwitchParams{
		SessionID:         "login-1",
		AccountID:         "workspace-b",
		AllowBrowserLogin: true,
	})
	request.ConnectionID = "conn-ws"
	response := router.Handle(request)
	if response.Error != nil {
		t.Fatalf("browser login switch = %+v", response.Error)
	}
	result := response.Result.(*auth.AccountSessionsSwitchResponse)
	if result.BrowserLogin == nil || result.BrowserLogin.LoginID == "" || result.BrowserLogin.AuthURL == "" {
		t.Fatalf("browser login result = %+v", result)
	}
	if !strings.Contains(result.BrowserLogin.AuthURL, "current_workspace_id=workspace-b") {
		t.Fatalf("auth url = %q", result.BrowserLogin.AuthURL)
	}
	if !strings.Contains(result.BrowserLogin.AuthURL, "connection=sso-1") {
		t.Fatalf("auth url missing SSO connection = %q", result.BrowserLogin.AuthURL)
	}
	if backend.exchanges.Load() != 0 {
		t.Fatal("browser login exchanged a token")
	}
}

func TestAccountSessionSwitchRollsBackFailedRoutingLikeRust(t *testing.T) {
	backend := &workspaceSwitchBackend{membershipQueue: []string{
		`{"accounts": [{"id": "workspace-b"}]}`,
		`{"accounts": [{"id": "workspace-a", "workspace_backend_origin": "https://chatgpt.com", "account_routing_override": "NO_CONSTRAINT"}]}`,
	}}
	router, _, home := workspaceSwitchFixture(t, backend, "workspace-a")
	initializeExperimentalConnection(t, router, "conn-ws")
	// The new workspace is missing from routing discovery, so the post-switch
	// account read fails and the previous credential is restored.

	request := requestWithParams(t, IntID(2), MethodAccountSessionSwitch, auth.AccountSessionsSwitchParams{
		SessionID: "login-1",
		AccountID: "workspace-b",
	})
	request.ConnectionID = "conn-ws"
	response := router.Handle(request)
	if response.Error == nil || !strings.Contains(response.Error.Message, "Workspace validation failed") {
		t.Fatalf("switch error = %+v", response.Error)
	}
	stored, err := auth.NewStore(home).Load()
	if err != nil || stored == nil {
		t.Fatalf("load stored auth = %+v, %v", stored, err)
	}
	if stored.Tokens["account_id"] != "workspace-a" {
		t.Fatalf("rollback did not restore the workspace: %#v", stored.Tokens)
	}
}

func TestAccountSessionSwitchRequiresExperimentalAPI(t *testing.T) {
	backend := &workspaceSwitchBackend{memberships: `{"accounts": []}`}
	router, _, _ := workspaceSwitchFixture(t, backend, "workspace-a")
	initialize := requestWithParams(t, IntID(1), MethodInitialize, InitializeParams{
		ClientInfo:   ClientInfo{Name: "codex-test", Version: "0.1.0"},
		Capabilities: &InitializeCapabilities{ExperimentalAPI: false},
	})
	initialize.ConnectionID = "conn-plain"
	if response := router.Handle(initialize); response.Error != nil {
		t.Fatalf("initialize error: %+v", response.Error)
	}
	request := requestWithParams(t, IntID(1), MethodAccountSessionSwitch, auth.AccountSessionsSwitchParams{
		SessionID: "login-1",
		AccountID: "workspace-b",
	})
	request.ConnectionID = "conn-plain"
	response := router.Handle(request)
	if response.Error == nil || response.Error.Code != JSONRPCInvalidRequestErrorCode {
		t.Fatalf("non-experimental switch = %+v", response.Error)
	}
	if !strings.Contains(response.Error.Message, string(MethodAccountSessionSwitch)) {
		t.Fatalf("experimental error = %q", response.Error.Message)
	}
}

func TestTurnAdmissionPauseBlocksNewTurnsLikeRust(t *testing.T) {
	manager := NewThreadManager(NewThreadStatusManager())
	if err := manager.ReserveTurn("thread-1"); err != nil {
		t.Fatalf("ReserveTurn() error = %v", err)
	}
	paused := make(chan *TurnAdmissionPause, 1)
	failed := make(chan error, 1)
	go func() {
		pause, err := manager.PauseTurnAdmission(context.Background())
		if err != nil {
			failed <- err
			return
		}
		paused <- pause
	}()
	deadline := time.Now().Add(2 * time.Second)
	for !threadsPaused(manager) {
		if time.Now().After(deadline) {
			t.Fatal("PauseTurnAdmission did not install the pause")
		}
		time.Sleep(time.Millisecond)
	}
	if err := manager.ReserveTurn("thread-2"); err == nil || !strings.Contains(err.Error(), "Workspace change in progress; retry when it completes") {
		t.Fatalf("ReserveTurn() during pause = %v", err)
	}
	if _, ok := manager.ConsumeTurn("thread-1", "", false); !ok {
		t.Fatal("ConsumeTurn() failed")
	}
	select {
	case pause := <-paused:
		pause.Release()
	case err := <-failed:
		t.Fatalf("PauseTurnAdmission() error = %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("PauseTurnAdmission() did not observe the drained turn")
	}
	if _, err := manager.PauseTurnAdmission(context.Background()); err != nil {
		t.Fatalf("PauseTurnAdmission() after release error = %v", err)
	}
}

func threadsPaused(m *ThreadManager) bool {
	m.turnsMu.Lock()
	defer m.turnsMu.Unlock()
	return m.paused
}

func TestUnloadForWorkspaceSwitchClosesLiveThreadsLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(filepath.Join(home, "sessions"))
	now := time.Now().UTC()
	record := &session.Record{ID: "thread-1", SessionID: "thread-1", CreatedAt: now, UpdatedAt: now, RecencyAt: now}
	if err := store.Create(record); err != nil {
		t.Fatalf("create thread: %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	if err := router.threads.RetainLiveThread(store, record); err != nil {
		t.Fatalf("retain thread: %v", err)
	}
	sink := NewNotificationBuffer()
	router.SetNotificationSink(sink)
	if err := router.unloadForWorkspaceSwitch(context.Background()); err != nil {
		t.Fatalf("unloadForWorkspaceSwitch() error = %v", err)
	}
	if len(router.threads.LiveThreadIDs()) != 0 {
		t.Fatalf("live threads = %+v", router.threads.LiveThreadIDs())
	}
	closed := latestNotificationPayload[ThreadClosedNotification](t, sink, NotificationThreadClosed)
	if closed.ThreadID != "thread-1" {
		t.Fatalf("thread/closed payload = %+v", closed)
	}
}

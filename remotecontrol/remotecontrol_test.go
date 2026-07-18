package remotecontrol

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestManagerEnableDisableAndStatus(t *testing.T) {
	manager := NewManager("codex", "install-1")

	status := manager.Status()
	if status.Status != StatusDisabled {
		t.Fatalf("initial status = %s, want disabled", status.Status)
	}
	enable, notification := manager.Enable(&EnableParams{})
	if enable.Status != StatusConnected || notification.Status != StatusConnected {
		t.Fatalf("enable = %+v notification = %+v, want connected", enable, notification)
	}
	if enable.EnvironmentID == nil || *enable.EnvironmentID != "default" {
		t.Fatalf("environmentID = %v, want default", enable.EnvironmentID)
	}
	enable, notification = manager.Enable(&EnableParams{})
	if enable.Status != StatusConnected || notification != nil {
		t.Fatalf("second enable = %+v notification = %+v, want connected and no notification", enable, notification)
	}

	disable, notification := manager.Disable(&DisableParams{})
	if disable.Status != StatusDisabled || notification.Status != StatusDisabled {
		t.Fatalf("disable = %+v notification = %+v, want disabled", disable, notification)
	}
	if disable.EnvironmentID != nil || notification.EnvironmentID != nil {
		t.Fatalf("disable environmentID = %v notification = %v, want nil", disable.EnvironmentID, notification.EnvironmentID)
	}
	disable, notification = manager.Disable(&DisableParams{})
	if disable.Status != StatusDisabled || notification != nil {
		t.Fatalf("second disable = %+v notification = %+v, want disabled and no notification", disable, notification)
	}
}

func TestManagerDurableEnableUsesPersistedEnrollmentAndWritesEnabled(t *testing.T) {
	store := newTestEnrollmentStore(t)
	ctx := context.Background()
	target := mustTarget(t, "https://chatgpt.com/backend-api")
	enabled := false
	enrollment := &Enrollment{
		RemoteControlTarget: target,
		AccountID:           "account-a",
		EnvironmentID:       "env_first",
		ServerID:            "srv_e_first",
		ServerName:          "first-server",
	}
	if err := UpdatePersistedRemoteControlEnrollment(ctx, store, target, "account-a", nil, enrollment, &enabled); err != nil {
		t.Fatalf("persist enrollment: %v", err)
	}
	manager := NewManagerWithBackend("codex", "install-1", &ManagerBackendOptions{
		Target:     target,
		Store:      store,
		AuthLoader: staticRemoteControlAuth("account-a"),
	})

	response, notification, err := manager.EnableContext(ctx, &EnableParams{})
	if err != nil {
		t.Fatalf("EnableContext() error = %v", err)
	}
	if response.Status != StatusConnecting || notification == nil || notification.Status != StatusConnecting || response.EnvironmentID == nil || *response.EnvironmentID != "env_first" {
		t.Fatalf("response=%+v notification=%+v", response, notification)
	}
	record, err := store.GetRemoteControlEnrollment(ctx, target.WebSocketURL, "account-a", nil)
	if err != nil {
		t.Fatalf("get record: %v", err)
	}
	if record.RemoteControlEnabled == nil || !*record.RemoteControlEnabled {
		t.Fatalf("record enabled = %+v, want true", record)
	}
}

func TestManagerDurableEnableEnrollsWhenMissing(t *testing.T) {
	store := newTestEnrollmentStore(t)
	ctx := context.Background()
	expiresAt := time.Now().UTC().Add(time.Hour)
	var gotRequest EnrollRemoteServerRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/backend-api/wham/remote/control/server/enroll" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Fatalf("decode enroll request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(EnrollRemoteServerResponse{
			ServerID:           "srv_e_new",
			EnvironmentID:      "env_new",
			RemoteControlToken: "server-token",
			ExpiresAt:          expiresAt.Format(time.RFC3339),
		})
	}))
	defer server.Close()
	target := mustTarget(t, server.URL+"/backend-api")
	manager := NewManagerWithBackend("codex", "install-1", &ManagerBackendOptions{
		Target:     target,
		Store:      store,
		AuthLoader: staticRemoteControlAuth("account-a"),
		ServerAPIOptions: &ServerAPIOptions{
			HTTPClient:       server.Client(),
			AppServerVersion: "test-version",
		},
	})

	response, _, err := manager.EnableContext(ctx, &EnableParams{})
	if err != nil {
		t.Fatalf("EnableContext() error = %v", err)
	}
	if response.Status != StatusConnecting || response.EnvironmentID == nil || *response.EnvironmentID != "env_new" {
		t.Fatalf("response = %+v", response)
	}
	if gotRequest.Name != "codex" || gotRequest.InstallationID != "install-1" || gotRequest.AppServerVersion != "test-version" {
		t.Fatalf("enroll request = %+v", gotRequest)
	}
	record, err := store.GetRemoteControlEnrollment(ctx, target.WebSocketURL, "account-a", nil)
	if err != nil {
		t.Fatalf("get record: %v", err)
	}
	if record == nil || record.ServerID != "srv_e_new" || record.RemoteControlEnabled == nil || !*record.RemoteControlEnabled {
		t.Fatalf("record = %+v", record)
	}
}

func TestManagerDurableEnableRecoversAuthOnPermissionDenied(t *testing.T) {
	store := newTestEnrollmentStore(t)
	ctx := context.Background()
	expiresAt := time.Now().UTC().Add(time.Hour)
	requests := 0
	recoveries := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			http.Error(w, `{"error":"expired"}`, http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(EnrollRemoteServerResponse{
			ServerID:           "srv_e_recovered",
			EnvironmentID:      "env_recovered",
			RemoteControlToken: "server-token",
			ExpiresAt:          expiresAt.Format(time.RFC3339),
		})
	}))
	defer server.Close()
	target := mustTarget(t, server.URL+"/backend-api")
	manager := NewManagerWithBackend("codex", "install-1", &ManagerBackendOptions{
		Target:     target,
		Store:      store,
		AuthLoader: staticRemoteControlAuth("account-a"),
		AuthRecovery: func(context.Context, *RemoteControlConnectionAuth) (*RemoteControlConnectionAuth, bool, error) {
			recoveries++
			return &RemoteControlConnectionAuth{AccountID: "account-a"}, true, nil
		},
		ServerAPIOptions: &ServerAPIOptions{HTTPClient: server.Client()},
	})

	response, _, err := manager.EnableContext(ctx, &EnableParams{})
	if err != nil {
		t.Fatalf("EnableContext() error = %v", err)
	}
	if requests != 2 || recoveries != 1 {
		t.Fatalf("requests/recoveries = %d/%d", requests, recoveries)
	}
	if response.EnvironmentID == nil || *response.EnvironmentID != "env_recovered" {
		t.Fatalf("response = %+v", response)
	}
}

func TestManagerPrepareWebsocketEnrollmentRefreshesPersistedEnrollmentWithoutToken(t *testing.T) {
	store := newTestEnrollmentStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	expiresAt := now.Add(time.Hour).Truncate(time.Second)
	var gotRequest RefreshRemoteServerRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/backend-api/wham/remote/control/server/refresh" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get(RemoteControlAccountIDHeader); got != "account-a" {
			t.Fatalf("account header = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Fatalf("decode refresh request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(EnrollRemoteServerResponse{
			ServerID:           "srv_e_first",
			EnvironmentID:      "env_first",
			RemoteControlToken: "refreshed-token",
			ExpiresAt:          expiresAt.Format(time.RFC3339),
		})
	}))
	defer server.Close()
	target := mustTarget(t, server.URL+"/backend-api")
	enabled := true
	if err := UpdatePersistedRemoteControlEnrollment(ctx, store, target, "account-a", nil, &Enrollment{
		RemoteControlTarget: target,
		AccountID:           "account-a",
		EnvironmentID:       "env_first",
		ServerID:            "srv_e_first",
		ServerName:          "persisted-server",
	}, &enabled); err != nil {
		t.Fatalf("persist enrollment: %v", err)
	}
	manager := NewManagerWithBackend("codex", "install-1", &ManagerBackendOptions{
		Target:     target,
		Store:      store,
		AuthLoader: staticRemoteControlAuth("account-a"),
		ServerAPIOptions: &ServerAPIOptions{
			HTTPClient: server.Client(),
			Now:        func() time.Time { return now },
		},
	})

	auth, enrollment, err := manager.PrepareWebsocketEnrollmentContext(ctx)
	if err != nil {
		t.Fatalf("PrepareWebsocketEnrollmentContext() error = %v", err)
	}
	if auth.AccountID != "account-a" || gotRequest.ServerID != "srv_e_first" || gotRequest.InstallationID != "install-1" {
		t.Fatalf("auth/request = %+v/%+v", auth, gotRequest)
	}
	if enrollment.RemoteControlToken == nil || *enrollment.RemoteControlToken != "refreshed-token" || enrollment.ExpiresAt == nil || !enrollment.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("enrollment = %+v", enrollment)
	}
}

func TestManagerPrepareWebsocketEnrollmentReplacesStaleEnrollmentOnRefreshNotFound(t *testing.T) {
	store := newTestEnrollmentStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	expiresAt := now.Add(time.Hour).Truncate(time.Second)
	requests := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path)
		switch r.URL.Path {
		case "/backend-api/wham/remote/control/server/refresh":
			http.Error(w, `{"detail":"missing"}`, http.StatusNotFound)
		case "/backend-api/wham/remote/control/server/enroll":
			_ = json.NewEncoder(w).Encode(EnrollRemoteServerResponse{
				ServerID:           "srv_e_new",
				EnvironmentID:      "env_new",
				RemoteControlToken: "new-token",
				ExpiresAt:          expiresAt.Format(time.RFC3339),
			})
		default:
			t.Fatalf("unexpected path = %s", r.URL.Path)
		}
	}))
	defer server.Close()
	target := mustTarget(t, server.URL+"/backend-api")
	enabled := true
	if err := UpdatePersistedRemoteControlEnrollment(ctx, store, target, "account-a", nil, &Enrollment{
		RemoteControlTarget: target,
		AccountID:           "account-a",
		EnvironmentID:       "env_old",
		ServerID:            "srv_e_old",
		ServerName:          "old-server",
	}, &enabled); err != nil {
		t.Fatalf("persist enrollment: %v", err)
	}
	manager := NewManagerWithBackend("codex", "install-1", &ManagerBackendOptions{
		Target:     target,
		Store:      store,
		AuthLoader: staticRemoteControlAuth("account-a"),
		ServerAPIOptions: &ServerAPIOptions{
			HTTPClient: server.Client(),
			Now:        func() time.Time { return now },
		},
	})

	_, enrollment, err := manager.PrepareWebsocketEnrollmentContext(ctx)
	if err != nil {
		t.Fatalf("PrepareWebsocketEnrollmentContext() error = %v", err)
	}
	if len(requests) != 2 || requests[0] != "/backend-api/wham/remote/control/server/refresh" || requests[1] != "/backend-api/wham/remote/control/server/enroll" {
		t.Fatalf("requests = %+v", requests)
	}
	if enrollment.ServerID != "srv_e_new" || enrollment.EnvironmentID != "env_new" || enrollment.RemoteControlToken == nil || *enrollment.RemoteControlToken != "new-token" {
		t.Fatalf("enrollment = %+v", enrollment)
	}
	record, err := store.GetRemoteControlEnrollment(ctx, target.WebSocketURL, "account-a", nil)
	if err != nil {
		t.Fatalf("get record: %v", err)
	}
	if record == nil || record.ServerID != "srv_e_new" || record.RemoteControlEnabled == nil || !*record.RemoteControlEnabled {
		t.Fatalf("record = %+v, want new server and enabled preserved", record)
	}
}

func TestManagerDurableDisableWritesPreference(t *testing.T) {
	store := newTestEnrollmentStore(t)
	ctx := context.Background()
	target := mustTarget(t, "https://chatgpt.com/backend-api")
	enabled := true
	enrollment := &Enrollment{RemoteControlTarget: target, AccountID: "account-a", EnvironmentID: "env_first", ServerID: "srv_e_first", ServerName: "server"}
	if err := UpdatePersistedRemoteControlEnrollment(ctx, store, target, "account-a", nil, enrollment, &enabled); err != nil {
		t.Fatalf("persist enrollment: %v", err)
	}
	manager := NewManagerWithBackend("codex", "install-1", &ManagerBackendOptions{
		Target:     target,
		Store:      store,
		AuthLoader: staticRemoteControlAuth("account-a"),
	})

	response, _, err := manager.DisableContext(ctx, &DisableParams{})
	if err != nil {
		t.Fatalf("DisableContext() error = %v", err)
	}
	if response.Status != StatusDisabled || response.EnvironmentID != nil {
		t.Fatalf("response = %+v", response)
	}
	record, err := store.GetRemoteControlEnrollment(ctx, target.WebSocketURL, "account-a", nil)
	if err != nil {
		t.Fatalf("get record: %v", err)
	}
	if record.RemoteControlEnabled == nil || *record.RemoteControlEnabled {
		t.Fatalf("record enabled = %+v, want false", record)
	}
}

func TestManagerDurablePairingRefreshesPersistedEnrollmentToken(t *testing.T) {
	store := newTestEnrollmentStore(t)
	ctx := context.Background()
	expiresAt := time.Now().UTC().Add(time.Hour)
	var refreshCalled bool
	var pairAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/backend-api/wham/remote/control/server/refresh":
			refreshCalled = true
			_ = json.NewEncoder(w).Encode(EnrollRemoteServerResponse{
				ServerID:           "srv_e_first",
				EnvironmentID:      "env_first",
				RemoteControlToken: "refreshed-token",
				ExpiresAt:          expiresAt.Format(time.RFC3339),
			})
		case "/backend-api/wham/remote/control/server/pair":
			pairAuth = r.Header.Get("Authorization")
			_ = json.NewEncoder(w).Encode(StartRemoteControlPairingResponse{
				PairingCode:   "pair-code",
				ServerID:      "srv_e_first",
				EnvironmentID: "env_first",
				ExpiresAt:     expiresAt.Format(time.RFC3339),
			})
		default:
			t.Fatalf("unexpected path = %s", r.URL.Path)
		}
	}))
	defer server.Close()
	target := mustTarget(t, server.URL+"/backend-api")
	enrollment := &Enrollment{
		RemoteControlTarget: target,
		AccountID:           "account-a",
		EnvironmentID:       "env_first",
		ServerID:            "srv_e_first",
		ServerName:          "first-server",
	}
	enabled := false
	if err := UpdatePersistedRemoteControlEnrollment(ctx, store, target, "account-a", nil, enrollment, &enabled); err != nil {
		t.Fatalf("persist enrollment: %v", err)
	}
	manager := NewManagerWithBackend("codex", "install-1", &ManagerBackendOptions{
		Target:     target,
		Store:      store,
		AuthLoader: staticRemoteControlAuth("account-a"),
		ServerAPIOptions: &ServerAPIOptions{
			HTTPClient: server.Client(),
		},
	})
	if _, _, err := manager.EnableContext(ctx, &EnableParams{}); err != nil {
		t.Fatalf("EnableContext() error = %v", err)
	}

	response, err := manager.StartPairingContext(ctx, &PairingStartParams{})
	if err != nil {
		t.Fatalf("StartPairingContext() error = %v", err)
	}
	if !refreshCalled || pairAuth != "Bearer refreshed-token" {
		t.Fatalf("refreshCalled=%v pairAuth=%q", refreshCalled, pairAuth)
	}
	if response.PairingCode != "pair-code" || response.EnvironmentID != "env_first" {
		t.Fatalf("pair response = %+v", response)
	}
}

func TestManagerDurablePairingRefreshRecoversAuthOnPermissionDenied(t *testing.T) {
	store := newTestEnrollmentStore(t)
	ctx := context.Background()
	expiresAt := time.Now().UTC().Add(time.Hour)
	refreshRequests := 0
	recoveries := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/backend-api/wham/remote/control/server/refresh":
			refreshRequests++
			if refreshRequests == 1 {
				http.Error(w, `{"error":"expired"}`, http.StatusForbidden)
				return
			}
			_ = json.NewEncoder(w).Encode(EnrollRemoteServerResponse{
				ServerID:           "srv_e_first",
				EnvironmentID:      "env_first",
				RemoteControlToken: "recovered-token",
				ExpiresAt:          expiresAt.Format(time.RFC3339),
			})
		case "/backend-api/wham/remote/control/server/pair":
			if got := r.Header.Get("Authorization"); got != "Bearer recovered-token" {
				t.Fatalf("pair auth = %q", got)
			}
			_ = json.NewEncoder(w).Encode(StartRemoteControlPairingResponse{
				PairingCode:   "pair-code",
				ServerID:      "srv_e_first",
				EnvironmentID: "env_first",
				ExpiresAt:     expiresAt.Format(time.RFC3339),
			})
		default:
			t.Fatalf("unexpected path = %s", r.URL.Path)
		}
	}))
	defer server.Close()
	target := mustTarget(t, server.URL+"/backend-api")
	enrollment := &Enrollment{RemoteControlTarget: target, AccountID: "account-a", EnvironmentID: "env_first", ServerID: "srv_e_first", ServerName: "first-server"}
	enabled := false
	if err := UpdatePersistedRemoteControlEnrollment(ctx, store, target, "account-a", nil, enrollment, &enabled); err != nil {
		t.Fatalf("persist enrollment: %v", err)
	}
	manager := NewManagerWithBackend("codex", "install-1", &ManagerBackendOptions{
		Target:     target,
		Store:      store,
		AuthLoader: staticRemoteControlAuth("account-a"),
		AuthRecovery: func(context.Context, *RemoteControlConnectionAuth) (*RemoteControlConnectionAuth, bool, error) {
			recoveries++
			return &RemoteControlConnectionAuth{AccountID: "account-a"}, true, nil
		},
		ServerAPIOptions: &ServerAPIOptions{HTTPClient: server.Client()},
	})
	if _, _, err := manager.EnableContext(ctx, &EnableParams{}); err != nil {
		t.Fatalf("EnableContext() error = %v", err)
	}
	if _, err := manager.StartPairingContext(ctx, &PairingStartParams{}); err != nil {
		t.Fatalf("StartPairingContext() error = %v", err)
	}
	if refreshRequests != 2 || recoveries != 1 {
		t.Fatalf("refreshRequests/recoveries = %d/%d", refreshRequests, recoveries)
	}
}

func TestManagerEphemeralEnableWithBackendDoesNotPersist(t *testing.T) {
	store := newTestEnrollmentStore(t)
	ctx := context.Background()
	target := mustTarget(t, "https://chatgpt.com/backend-api")
	enabled := false
	enrollment := &Enrollment{RemoteControlTarget: target, AccountID: "account-a", EnvironmentID: "env_first", ServerID: "srv_e_first", ServerName: "server"}
	if err := UpdatePersistedRemoteControlEnrollment(ctx, store, target, "account-a", nil, enrollment, &enabled); err != nil {
		t.Fatalf("persist enrollment: %v", err)
	}
	manager := NewManagerWithBackend("codex", "install-1", &ManagerBackendOptions{
		Target:     target,
		Store:      store,
		AuthLoader: staticRemoteControlAuth("account-a"),
	})
	response, _, err := manager.EnableContext(ctx, &EnableParams{Ephemeral: true})
	if err != nil {
		t.Fatalf("EnableContext(ephemeral) error = %v", err)
	}
	if response.Status != StatusConnected || response.EnvironmentID == nil || *response.EnvironmentID != "default" {
		t.Fatalf("ephemeral response = %+v", response)
	}
	record, err := store.GetRemoteControlEnrollment(ctx, target.WebSocketURL, "account-a", nil)
	if err != nil {
		t.Fatalf("get record: %v", err)
	}
	if record.RemoteControlEnabled == nil || *record.RemoteControlEnabled {
		t.Fatalf("record enabled = %+v, want false", record)
	}
}

func TestManagerZeroValueIsUsable(t *testing.T) {
	var manager Manager
	if status := manager.Status(); status.Status != StatusDisabled {
		t.Fatalf("zero status = %+v", status)
	}
	enable, notification := manager.Enable(nil)
	if enable == nil || notification == nil || enable.Status != StatusConnected || enable.EnvironmentID == nil || *enable.EnvironmentID != "default" {
		t.Fatalf("enable = %+v notification = %+v", enable, notification)
	}
	start, err := manager.StartPairing(nil)
	if err != nil {
		t.Fatalf("StartPairing(nil) error = %v", err)
	}
	claimed, err := manager.ClaimPairing(&PairingStatusParams{PairingCode: &start.PairingCode})
	if err != nil || !claimed {
		t.Fatalf("ClaimPairing() = %v, %v", claimed, err)
	}
	if err := manager.UpsertClient(start.EnvironmentID, Client{ClientID: "client-1"}); err != nil {
		t.Fatalf("UpsertClient() error = %v", err)
	}
	clients, err := manager.ListClients(&ClientsListParams{EnvironmentID: start.EnvironmentID})
	if err != nil || len(clients.Data) != 1 || clients.Data[0].ClientID != "client-1" {
		t.Fatalf("ListClients() = %+v, %v", clients, err)
	}
	if _, err := manager.RevokeClient(&ClientsRevokeParams{EnvironmentID: start.EnvironmentID, ClientID: "client-1"}); err != nil {
		t.Fatalf("RevokeClient() error = %v", err)
	}
}

func staticRemoteControlAuth(accountID string) RemoteControlAuthLoader {
	return func(context.Context) (*RemoteControlConnectionAuth, error) {
		return &RemoteControlConnectionAuth{AccountID: accountID}, nil
	}
}

func TestPairingLifecycle(t *testing.T) {
	manager := NewManager("codex", "install-1")
	manager.SetClock(func() time.Time { return time.Unix(100, 0) })
	manager.Enable(&EnableParams{})

	start, err := manager.StartPairing(&PairingStartParams{ManualCode: true})
	if err != nil {
		t.Fatalf("StartPairing() error = %v", err)
	}
	if start.PairingCode == "" || start.ManualPairingCode == nil || start.ExpiresAt != time.Unix(100, 0).Add(10*time.Minute).Unix() {
		t.Fatalf("pairing = %+v", start)
	}

	status, err := manager.PairingStatus(&PairingStatusParams{PairingCode: &start.PairingCode})
	if err != nil {
		t.Fatalf("PairingStatus() error = %v", err)
	}
	if status.Claimed {
		t.Fatalf("Claimed = true, want false")
	}
	ok, err := manager.ClaimPairing(&PairingStatusParams{ManualPairingCode: start.ManualPairingCode})
	if err != nil {
		t.Fatalf("ClaimPairing() error = %v", err)
	}
	if !ok {
		t.Fatalf("ClaimPairing() ok = false, want true")
	}
	status, err = manager.PairingStatus(&PairingStatusParams{PairingCode: &start.PairingCode})
	if err != nil {
		t.Fatalf("PairingStatus() after claim error = %v", err)
	}
	if !status.Claimed {
		t.Fatalf("Claimed = false, want true")
	}
}

func TestPairingStatusRequiresCode(t *testing.T) {
	manager := NewManager("codex", "install-1")
	_, err := manager.PairingStatus(&PairingStatusParams{})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("PairingStatus() error = %v, want ErrInvalidRequest", err)
	}
	if _, err := manager.PairingStatus(nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("PairingStatus(nil) error = %v, want ErrInvalidRequest", err)
	}
}

func TestClientListPaginationAndRevoke(t *testing.T) {
	manager := NewManager("codex", "install-1")
	for _, id := range []string{"b", "a", "c"} {
		if err := manager.UpsertClient("env-1", Client{ClientID: id}); err != nil {
			t.Fatalf("UpsertClient(%q) error = %v", id, err)
		}
	}
	limit := uint32(2)
	page, err := manager.ListClients(&ClientsListParams{EnvironmentID: "env-1", Limit: &limit})
	if err != nil {
		t.Fatalf("ListClients() error = %v", err)
	}
	if len(page.Data) != 2 || page.Data[0].ClientID != "a" || page.Data[1].ClientID != "b" {
		t.Fatalf("page = %+v, want a,b", page.Data)
	}
	if page.NextCursor == nil || *page.NextCursor != "2" {
		t.Fatalf("nextCursor = %v, want 2", page.NextCursor)
	}
	page, err = manager.ListClients(&ClientsListParams{EnvironmentID: "env-1", Cursor: page.NextCursor, Limit: &limit})
	if err != nil {
		t.Fatalf("ListClients(next) error = %v", err)
	}
	if len(page.Data) != 1 || page.Data[0].ClientID != "c" {
		t.Fatalf("second page = %+v, want c", page.Data)
	}
	if _, err := manager.RevokeClient(&ClientsRevokeParams{EnvironmentID: "env-1", ClientID: "b"}); err != nil {
		t.Fatalf("RevokeClient() error = %v", err)
	}
	page, err = manager.ListClients(&ClientsListParams{EnvironmentID: "env-1"})
	if err != nil {
		t.Fatalf("ListClients(after revoke) error = %v", err)
	}
	if len(page.Data) != 2 || page.Data[0].ClientID != "a" || page.Data[1].ClientID != "c" {
		t.Fatalf("after revoke = %+v, want a,c", page.Data)
	}
}

func TestManagerBackendClientListAndRevokeUseRemoteControlAPI(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0).UTC()
	lastSeen := now.Add(-time.Minute).Format(time.RFC3339Nano)
	var gotListAccount string
	var gotRevokeAccount string
	var gotListQuery string
	var gotRevokePath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /backend-api/wham/remote/control/environments/env_first/clients":
			gotListAccount = r.Header.Get(RemoteControlAccountIDHeader)
			gotListQuery = r.URL.RawQuery
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{{
					"client_id":    "client-1",
					"display_name": "Desktop",
					"device_type":  "desktop",
					"platform":     "windows",
					"os_version":   "11",
					"device_model": "workstation",
					"app_version":  "1.2.3",
					"last_seen_at": lastSeen,
				}},
				"cursor": "next-page",
			})
		case "DELETE /backend-api/wham/remote/control/environments/env_first/clients/client-1":
			gotRevokeAccount = r.Header.Get(RemoteControlAccountIDHeader)
			gotRevokePath = r.URL.Path
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()
	target := mustTarget(t, server.URL+"/backend-api")
	manager := NewManagerWithBackend("codex", "install-1", &ManagerBackendOptions{
		RemoteControlURL: server.URL + "/backend-api",
		Target:           target,
		AuthLoader:       staticRemoteControlAuth("account-a"),
		ServerAPIOptions: &ServerAPIOptions{
			HTTPClient: server.Client(),
		},
	})

	cursor := "cursor-1"
	limit := uint32(25)
	order := OrderDesc
	clients, err := manager.ListClientsContext(ctx, &ClientsListParams{
		EnvironmentID: "env_first",
		Cursor:        &cursor,
		Limit:         &limit,
		Order:         &order,
	})
	if err != nil {
		t.Fatalf("ListClientsContext() error = %v", err)
	}
	if gotListAccount != "account-a" {
		t.Fatalf("list account header = %q", gotListAccount)
	}
	if gotListQuery != "cursor=cursor-1&limit=25&order=desc" {
		t.Fatalf("list query = %q", gotListQuery)
	}
	if clients.NextCursor == nil || *clients.NextCursor != "next-page" || len(clients.Data) != 1 {
		t.Fatalf("clients = %+v", clients)
	}
	client := clients.Data[0]
	if client.ClientID != "client-1" || client.DisplayName == nil || *client.DisplayName != "Desktop" || client.LastSeenAt == nil || *client.LastSeenAt != now.Add(-time.Minute).Unix() {
		t.Fatalf("client = %+v", client)
	}

	if _, err := manager.RevokeClientContext(ctx, &ClientsRevokeParams{EnvironmentID: "env_first", ClientID: "client-1"}); err != nil {
		t.Fatalf("RevokeClientContext() error = %v", err)
	}
	if gotRevokeAccount != "account-a" || gotRevokePath != "/backend-api/wham/remote/control/environments/env_first/clients/client-1" {
		t.Fatalf("revoke account/path = %q/%q", gotRevokeAccount, gotRevokePath)
	}
}

func TestManagerBackendClientManagementRecoversUnauthorizedOnce(t *testing.T) {
	requests := 0
	recoveries := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			http.Error(w, `{"error":"expired"}`, http.StatusUnauthorized)
			return
		}
		if got := r.Header.Get(RemoteControlAccountIDHeader); got != "account-a" {
			t.Fatalf("account header after recovery = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
	}))
	defer server.Close()
	manager := NewManagerWithBackend("codex", "install-1", &ManagerBackendOptions{
		RemoteControlURL: server.URL + "/backend-api",
		AuthLoader:       staticRemoteControlAuth("account-a"),
		AuthRecovery: func(context.Context, *RemoteControlConnectionAuth) (*RemoteControlConnectionAuth, bool, error) {
			recoveries++
			return &RemoteControlConnectionAuth{AccountID: "account-a"}, true, nil
		},
		ServerAPIOptions: &ServerAPIOptions{HTTPClient: server.Client()},
	})

	clients, err := manager.ListClientsContext(context.Background(), &ClientsListParams{EnvironmentID: "env_first"})
	if err != nil {
		t.Fatalf("ListClientsContext() error = %v", err)
	}
	if len(clients.Data) != 0 || requests != 2 || recoveries != 1 {
		t.Fatalf("clients=%+v requests=%d recoveries=%d", clients, requests, recoveries)
	}
}

func TestClientValidation(t *testing.T) {
	manager := NewManager("codex", "install-1")
	if err := manager.UpsertClient("", Client{ClientID: "c"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("UpsertClient empty env error = %v, want ErrInvalidRequest", err)
	}
	if _, err := manager.ListClients(&ClientsListParams{}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("ListClients empty env error = %v, want ErrInvalidRequest", err)
	}
	if _, err := manager.ListClients(nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("ListClients nil error = %v, want ErrInvalidRequest", err)
	}
	zero := uint32(0)
	if _, err := manager.ListClients(&ClientsListParams{EnvironmentID: "env", Limit: &zero}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("ListClients zero limit error = %v, want ErrInvalidRequest", err)
	}
	if _, err := manager.RevokeClient(&ClientsRevokeParams{EnvironmentID: "env"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("RevokeClient empty client error = %v, want ErrInvalidRequest", err)
	}
}

func TestParseAppServerVersionFromUserAgent(t *testing.T) {
	version, err := ParseAppServerVersionFromUserAgent("codex_app_server_daemon/1.2.3 (Linux 6.8.0; x86_64) codex_cli_rs/1.2.3")
	if err != nil {
		t.Fatalf("ParseAppServerVersionFromUserAgent() error = %v", err)
	}
	if version != "1.2.3" {
		t.Fatalf("version = %q", version)
	}
	if _, err := ParseAppServerVersionFromUserAgent("codex_app_server_daemon"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("missing separator error = %v, want ErrInvalidRequest", err)
	}
	if _, err := ParseAppServerVersionFromUserAgent("codex_app_server_daemon/"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("missing version error = %v, want ErrInvalidRequest", err)
	}
}

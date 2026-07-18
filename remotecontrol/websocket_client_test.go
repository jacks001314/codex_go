package remotecontrol

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestManagerConnectWebsocketContextDialsWithRefreshedEnrollment(t *testing.T) {
	store := newTestEnrollmentStore(t)
	ctx := context.Background()
	expiresAt := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	var refreshSeen bool
	var websocketSeen bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/backend-api/wham/remote/control/server/refresh":
			refreshSeen = true
			var request RefreshRemoteServerRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode refresh request: %v", err)
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			if request.ServerID != "srv_e_first" || request.InstallationID != "install-1" {
				t.Errorf("refresh request = %+v", request)
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(EnrollRemoteServerResponse{
				ServerID:           "srv_e_first",
				EnvironmentID:      "env_first",
				RemoteControlToken: "server-token",
				ExpiresAt:          expiresAt.Format(time.RFC3339),
			})
		case "/backend-api/wham/remote/control/server":
			websocketSeen = true
			if got := r.Header.Get(RemoteControlServerIDHeader); got != "srv_e_first" {
				t.Errorf("server id header = %q", got)
			}
			if got := r.Header.Get(RemoteControlServerNameHeader); got != base64.StdEncoding.EncodeToString([]byte("codex")) {
				t.Errorf("server name header = %q", got)
			}
			if got := r.Header.Get(RemoteControlProtocolVersionHeader); got != RemoteControlProtocolVersion {
				t.Errorf("protocol header = %q", got)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer server-token" {
				t.Errorf("authorization header = %q", got)
			}
			if got := r.Header.Get(RemoteControlInstallationIDHeader); got != "install-1" {
				t.Errorf("installation header = %q", got)
			}
			if got := r.Header.Get(RemoteControlSubscribeCursorHeader); got != "cursor-1" {
				t.Errorf("cursor header = %q", got)
			}
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("websocket accept: %v", err)
				return
			}
			_ = conn.Close(websocket.StatusNormalClosure, "")
		default:
			t.Errorf("unexpected path = %s", r.URL.Path)
			http.NotFound(w, r)
		}
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
		},
	})
	cursor := "cursor-1"

	conn, response, err := manager.ConnectWebsocketContext(ctx, &RemoteControlWebsocketConnectOptions{SubscribeCursor: &cursor})
	if err != nil {
		t.Fatalf("ConnectWebsocketContext() error = %v", err)
	}
	if conn == nil || response == nil || response.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("conn/response = %v/%+v", conn, response)
	}
	_ = conn.Close(websocket.StatusNormalClosure, "")
	if !refreshSeen || !websocketSeen {
		t.Fatalf("refreshSeen=%v websocketSeen=%v", refreshSeen, websocketSeen)
	}
}

func TestManagerConnectWebsocketContextClearsServerTokenOnAuthFailure(t *testing.T) {
	manager, _ := managerWithCurrentWebsocketEnrollment(t, "old-token")
	dial := func(ctx context.Context, url string, options *websocket.DialOptions) (*websocket.Conn, *http.Response, error) {
		if got := options.HTTPHeader.Get("Authorization"); got != "Bearer old-token" {
			t.Fatalf("authorization header = %q", got)
		}
		return nil, &http.Response{
			StatusCode: http.StatusUnauthorized,
			Status:     "401 Unauthorized",
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader(`{"error":"expired"}`)),
		}, errors.New("bad handshake")
	}

	_, _, err := manager.ConnectWebsocketContext(context.Background(), &RemoteControlWebsocketConnectOptions{Dial: dial})
	if err == nil || !strings.Contains(err.Error(), "remote control websocket auth failed with HTTP 401 Unauthorized") {
		t.Fatalf("ConnectWebsocketContext() error = %v", err)
	}
	manager.mu.Lock()
	enrollment := cloneManagerEnrollment(manager.enrollment)
	manager.mu.Unlock()
	if enrollment == nil || enrollment.RemoteControlToken != nil || enrollment.ExpiresAt != nil {
		t.Fatalf("enrollment after auth failure = %+v, want token cleared", enrollment)
	}
}

func TestManagerConnectWebsocketContextReplacesStaleEnrollmentOnMissingRemoteAppServer(t *testing.T) {
	store := newTestEnrollmentStore(t)
	ctx := context.Background()
	expiresAt := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	var enrollSeen bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/backend-api/wham/remote/control/server/enroll" {
			t.Errorf("unexpected path = %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		enrollSeen = true
		_ = json.NewEncoder(w).Encode(EnrollRemoteServerResponse{
			ServerID:           "srv_e_new",
			EnvironmentID:      "env_new",
			RemoteControlToken: "new-token",
			ExpiresAt:          expiresAt.Format(time.RFC3339),
		})
	}))
	defer server.Close()
	target := mustTarget(t, server.URL+"/backend-api")
	token := "old-token"
	expires := time.Now().UTC().Add(time.Hour)
	oldEnrollment := &Enrollment{
		RemoteControlTarget: target,
		AccountID:           "account-a",
		EnvironmentID:       "env_old",
		ServerID:            "srv_e_old",
		ServerName:          "codex",
		RemoteControlToken:  &token,
		ExpiresAt:           &expires,
	}
	enabled := true
	if err := UpdatePersistedRemoteControlEnrollment(ctx, store, target, "account-a", nil, oldEnrollment, &enabled); err != nil {
		t.Fatalf("persist old enrollment: %v", err)
	}
	manager := NewManagerWithBackend("codex", "install-1", &ManagerBackendOptions{
		Target:     target,
		Store:      store,
		AuthLoader: staticRemoteControlAuth("account-a"),
		ServerAPIOptions: &ServerAPIOptions{
			HTTPClient: server.Client(),
		},
	})
	manager.enrollment = cloneManagerEnrollment(oldEnrollment)
	dial := func(context.Context, string, *websocket.DialOptions) (*websocket.Conn, *http.Response, error) {
		return nil, &http.Response{
			StatusCode: http.StatusNotFound,
			Status:     "404 Not Found",
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader(`{"detail":"Remote app server not found"}`)),
		}, errors.New("bad handshake")
	}

	_, _, err := manager.ConnectWebsocketContext(ctx, &RemoteControlWebsocketConnectOptions{Dial: dial})
	if err == nil || !strings.Contains(err.Error(), "failed to connect app-server remote control websocket") {
		t.Fatalf("ConnectWebsocketContext() error = %v", err)
	}
	if !enrollSeen {
		t.Fatalf("enroll was not called")
	}
	record, err := store.GetRemoteControlEnrollment(ctx, target.WebSocketURL, "account-a", nil)
	if err != nil {
		t.Fatalf("get enrollment record: %v", err)
	}
	if record == nil || record.ServerID != "srv_e_new" || record.EnvironmentID != "env_new" || record.RemoteControlEnabled == nil || !*record.RemoteControlEnabled {
		t.Fatalf("record = %+v, want replacement enabled", record)
	}
	manager.mu.Lock()
	current := cloneManagerEnrollment(manager.enrollment)
	manager.mu.Unlock()
	if current == nil || current.ServerID != "srv_e_new" || current.RemoteControlToken == nil || *current.RemoteControlToken != "new-token" {
		t.Fatalf("current enrollment = %+v", current)
	}
}

func managerWithCurrentWebsocketEnrollment(t *testing.T, token string) (*Manager, *RemoteControlTarget) {
	t.Helper()
	store := newTestEnrollmentStore(t)
	target := mustTarget(t, "https://chatgpt.com/backend-api")
	expiresAt := time.Now().UTC().Add(time.Hour)
	manager := NewManagerWithBackend("codex", "install-1", &ManagerBackendOptions{
		Target:     target,
		Store:      store,
		AuthLoader: staticRemoteControlAuth("account-a"),
	})
	manager.enrollment = &Enrollment{
		RemoteControlTarget: target,
		AccountID:           "account-a",
		EnvironmentID:       "env_first",
		ServerID:            "srv_e_first",
		ServerName:          "codex",
		RemoteControlToken:  &token,
		ExpiresAt:           &expiresAt,
	}
	manager.status = StatusConnecting
	envID := "env_first"
	manager.environmentID = &envID
	return manager, target
}

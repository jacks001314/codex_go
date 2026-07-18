package remotecontrol

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"codex_go/model"

	"github.com/coder/websocket"
)

func TestConnectWebsocketProactiveRefreshFailureUsesExistingToken(t *testing.T) {
	now := time.Now().UTC()
	var refreshSeen bool
	var websocketAuth string
	manager, server := newRefreshTestManager(t, now, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/backend-api/wham/remote/control/server/refresh":
			refreshSeen = true
			http.Error(w, "upstream unavailable", http.StatusBadGateway)
		case "/backend-api/wham/remote/control/server":
			websocketAuth = r.Header.Get("Authorization")
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("websocket accept: %v", err)
				return
			}
			_ = conn.Close(websocket.StatusNormalClosure, "")
		default:
			http.NotFound(w, r)
		}
	})
	defer server.Close()
	setRefreshTestEnrollment(t, manager, now, now.Add(4*time.Minute), "old-token")

	conn, _, err := manager.ConnectWebsocketContext(context.Background(), nil)
	if err != nil {
		t.Fatalf("ConnectWebsocketContext() error = %v", err)
	}
	_ = conn.CloseNow()

	if !refreshSeen || websocketAuth != "Bearer old-token" {
		t.Fatalf("refreshSeen=%v websocketAuth=%q", refreshSeen, websocketAuth)
	}
	enrollment := managerEnrollmentSnapshot(t, manager)
	if enrollment.NextRefreshAt == nil || !enrollment.NextRefreshAt.Equal(now.Add(30*time.Second)) {
		t.Fatalf("NextRefreshAt = %v, want %v", enrollment.NextRefreshAt, now.Add(30*time.Second))
	}
}

func TestConnectWebsocketRequiredRefreshFailureDefersReconnectWithoutWebsocket(t *testing.T) {
	now := time.Now().UTC()
	var refreshRequests int
	var websocketRequests int
	manager, server := newRefreshTestManager(t, now, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/backend-api/wham/remote/control/server/refresh":
			refreshRequests++
			w.Header().Set("Retry-After", "120")
			http.Error(w, "upstream unavailable", http.StatusBadGateway)
		case "/backend-api/wham/remote/control/server":
			websocketRequests++
			t.Errorf("required refresh failure must not continue to websocket")
			http.Error(w, "unexpected websocket", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	})
	defer server.Close()
	setRefreshTestEnrollment(t, manager, now, now.Add(-time.Second), "expired-token")

	_, _, firstErr := manager.ConnectWebsocketContext(context.Background(), nil)
	if firstErr == nil || !strings.Contains(firstErr.Error(), "502 Bad Gateway") {
		t.Fatalf("first ConnectWebsocketContext() error = %v, want 502", firstErr)
	}
	_, _, secondErr := manager.ConnectWebsocketContext(context.Background(), nil)
	if !errors.Is(secondErr, ErrRemoteControlRefreshDeferred) {
		t.Fatalf("second ConnectWebsocketContext() error = %v, want refresh deferred", secondErr)
	}
	if refreshRequests != 1 || websocketRequests != 0 {
		t.Fatalf("refreshRequests=%d websocketRequests=%d", refreshRequests, websocketRequests)
	}
}

func TestWebsocketRetryAfterThrottlesPairingRefresh(t *testing.T) {
	now := time.Now().UTC()
	var paths []string
	manager, server := newRefreshTestManager(t, now, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/backend-api/wham/remote/control/server/refresh":
			w.Header().Set("Retry-After", "120")
			http.Error(w, "upstream unavailable", http.StatusBadGateway)
		case "/backend-api/wham/remote/control/server":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("websocket accept: %v", err)
				return
			}
			_ = conn.Close(websocket.StatusNormalClosure, "")
		case "/backend-api/wham/remote/control/server/pair":
			if auth := r.Header.Get("Authorization"); auth != "Bearer old-token" {
				t.Errorf("pair Authorization = %q, want old token", auth)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"pairing_code":        "pairing-code",
				"manual_pairing_code": "ABCD-EFGH",
				"server_id":           "server-id",
				"environment_id":      "environment-id",
				"expires_at":          "3026-05-22T12:34:56Z",
			})
		default:
			http.NotFound(w, r)
		}
	})
	defer server.Close()
	setRefreshTestEnrollment(t, manager, now, now.Add(4*time.Minute), "old-token")

	conn, _, err := manager.ConnectWebsocketContext(context.Background(), nil)
	if err != nil {
		t.Fatalf("ConnectWebsocketContext() error = %v", err)
	}
	_ = conn.CloseNow()
	response, err := manager.StartPairingContext(context.Background(), &PairingStartParams{ManualCode: true})
	if err != nil {
		t.Fatalf("StartPairingContext() error = %v", err)
	}
	if response.PairingCode != "pairing-code" || response.ManualPairingCode == nil || *response.ManualPairingCode != "ABCD-EFGH" {
		t.Fatalf("pairing response = %+v", response)
	}

	want := []string{
		"/backend-api/wham/remote/control/server/refresh",
		"/backend-api/wham/remote/control/server",
		"/backend-api/wham/remote/control/server/pair",
	}
	if strings.Join(paths, "\n") != strings.Join(want, "\n") {
		t.Fatalf("paths = %#v, want %#v", paths, want)
	}
}

func newRefreshTestManager(t *testing.T, now time.Time, handler http.HandlerFunc) (*Manager, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	target := mustTarget(t, server.URL+"/backend-api")
	store := newTestEnrollmentStore(t)
	store.Now = func() time.Time { return now }
	manager := NewManagerWithBackend("test-server", "installation-id", &ManagerBackendOptions{
		Target: target,
		Store:  store,
		AuthLoader: func(context.Context) (*RemoteControlConnectionAuth, error) {
			headers := model.BearerAuthHeaders("access-token", "account-id", false)
			return &RemoteControlConnectionAuth{AccountID: "account-id", AuthHeaders: &headers}, nil
		},
		ServerAPIOptions: &ServerAPIOptions{
			Now:     func() time.Time { return now },
			Backoff: func() time.Duration { return 30 * time.Second },
		},
	})
	manager.PublishConnectionStatus(StatusConnected)
	return manager, server
}

func setRefreshTestEnrollment(t *testing.T, manager *Manager, now time.Time, expiresAt time.Time, token string) {
	t.Helper()
	manager.mu.Lock()
	defer manager.mu.Unlock()
	target := cloneRemoteControlTarget(manager.backend.Target)
	manager.enrollment = &Enrollment{
		RemoteControlTarget: target,
		AccountID:           "account-id",
		EnvironmentID:       "environment-id",
		ServerID:            "server-id",
		ServerName:          "test-server",
		RemoteControlToken:  &token,
		ExpiresAt:           &expiresAt,
	}
	manager.environmentID = stringPtrIfNotEmpty("environment-id")
	manager.now = func() time.Time { return now }
}

func managerEnrollmentSnapshot(t *testing.T, manager *Manager) *Enrollment {
	t.Helper()
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return cloneManagerEnrollment(manager.enrollment)
}

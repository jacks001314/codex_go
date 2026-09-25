package appserver

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"codex_go/config"
)

// newGatewayOAuthRouter builds a router whose effective provider configures
// gateway OAuth against an in-process issuer (Rust message_processor_gateway_
// oauth_tests).
func newGatewayOAuthRouter(t *testing.T) (*RuntimeRouter, string, *httptest.Server) {
	t.Helper()
	issuer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		form, _ := url.ParseQuery(string(body))
		writer.Header().Set("Content-Type", "application/json")
		if form.Get("grant_type") != "authorization_code" {
			writer.WriteHeader(http.StatusBadRequest)
			_, _ = writer.Write([]byte(`{"error":"unsupported_grant_type"}`))
			return
		}
		_, _ = writer.Write([]byte(`{"access_token":"gateway-token","token_type":"bearer","expires_in":3600,"refresh_token":"gateway-refresh"}`))
	}))
	t.Cleanup(issuer.Close)

	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	configBody := strings.Join([]string{
		`model = "gpt-5.4"`,
		`model_provider = "gateway"`,
		``,
		`[model_providers.gateway]`,
		`name = "Gateway provider"`,
		`base_url = "` + issuer.URL + `/v1"`,
		`wire_api = "responses"`,
		``,
		`[model_providers.gateway.gateway_oauth]`,
		`authorization_url = "` + issuer.URL + `/authorize"`,
		`token_url = "` + issuer.URL + `/token"`,
		`client_id = "client-1"`,
		`delivery = { kind = "header", name = "x-gateway-token" }`,
		``,
	}, "\n")
	if err := os.WriteFile(config.ConfigPath(home), []byte(configBody), 0o600); err != nil {
		t.Fatalf("write config error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home)})
	t.Cleanup(func() { _ = router.Close() })
	return router, home, issuer
}

// initializeGatewayOAuthConnection registers the connection the way the
// transport does, so connection-scoped requests are accepted.
func initializeGatewayOAuthConnection(t *testing.T, router *RuntimeRouter, connectionID string) {
	t.Helper()
	request := requestWithParams(t, StringID("init-"+connectionID), MethodInitialize, InitializeParams{
		ClientInfo: ClientInfo{Name: "codex_vscode", Version: "0.1.0"},
	})
	request.ConnectionID = connectionID
	if response := router.Handle(request); response.Error != nil {
		t.Fatalf("initialize %s error: %+v", connectionID, response.Error)
	}
}

func gatewayOAuthNotifications(t *testing.T, router *RuntimeRouter) *[]*GatewayOAuthChangedNotification {
	t.Helper()
	var (
		mu            sync.Mutex
		notifications []*GatewayOAuthChangedNotification
	)
	router.SetNotificationSink(NotificationSinkFunc(func(notification *Notification) {
		if notification.Method != NotificationGatewayOAuthChanged {
			return
		}
		changed, ok := notification.Params.(*GatewayOAuthChangedNotification)
		if !ok {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		notifications = append(notifications, changed)
	}))
	return &notifications
}

// TestGatewayOAuthReadReportsProviderPolicyLikeRust pins the read RPC: it
// reports the effective provider and whether gateway OAuth is required, without
// exposing credentials.
func TestGatewayOAuthReadReportsProviderPolicyLikeRust(t *testing.T) {
	router, _, _ := newGatewayOAuthRouter(t)
	response := router.Handle(requestWithParams(t, IntID(1), MethodGatewayOAuthRead, nil))
	if response.Error != nil {
		t.Fatalf("account/gatewayOAuth/read error = %+v", response.Error)
	}
	read, ok := response.Result.(*GatewayOAuthReadResponse)
	if !ok {
		t.Fatalf("read result = %#v", response.Result)
	}
	if read.ProviderID != "gateway" || read.ProviderName != "Gateway provider" || !read.Required {
		t.Fatalf("read response = %#v", read)
	}
	if read.Status == nil || *read.Status != GatewayOAuthStatusNotReady {
		t.Fatalf("read status = %#v, want notReady before sign-in", read.Status)
	}
	if read.Error != nil {
		t.Fatalf("read error field = %q, want none", *read.Error)
	}
}

// TestGatewayOAuthLoginForwardsAuthURLAndSucceedsLikeRust pins the explicit
// sign-in: the URL handoff reaches the initiating connection, the credential is
// persisted through the same storage as inference, and the terminal status is
// broadcast.
func TestGatewayOAuthLoginForwardsAuthURLAndSucceedsLikeRust(t *testing.T) {
	router, home, _ := newGatewayOAuthRouter(t)
	notifications := gatewayOAuthNotifications(t, router)
	initializeGatewayOAuthConnection(t, router, "connection-1")

	loginDone := make(chan *Response, 1)
	go func() {
		request := requestWithParams(t, IntID(2), MethodGatewayOAuthLogin, nil)
		request.ConnectionID = "connection-1"
		loginDone <- router.Handle(request)
	}()

	authURL := waitForGatewayAuthURL(t, notifications, loginDone)
	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("parse auth url %q: %v", authURL, err)
	}
	redirect, err := http.Get(parsed.Query().Get("redirect_uri") + "?code=code-1&state=" + url.QueryEscape(parsed.Query().Get("state")))
	if err != nil {
		t.Fatalf("answer gateway callback: %v", err)
	}
	_, _ = io.ReadAll(redirect.Body)
	_ = redirect.Body.Close()

	select {
	case response := <-loginDone:
		if response.Error != nil {
			t.Fatalf("account/gatewayOAuth/login error = %+v", response.Error)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("gateway sign-in did not finish")
	}

	// The credential is visible to the read RPC (and therefore to inference).
	response := router.Handle(requestWithParams(t, IntID(3), MethodGatewayOAuthRead, nil))
	read, ok := response.Result.(*GatewayOAuthReadResponse)
	if response.Error != nil || !ok {
		t.Fatalf("read after sign-in = %+v", response)
	}
	if read.Status == nil || *read.Status != GatewayOAuthStatusSucceeded {
		t.Fatalf("read status after sign-in = %#v, want succeeded", read.Status)
	}

	// The credential lands in the shared gateway store, not in the response.
	if _, err := os.Stat(filepath.Join(home, "secrets", "gateway_oauth.age")); err != nil {
		t.Fatalf("gateway credential file: %v", err)
	}
	if strings.Contains(authURL, "gateway-token") {
		t.Fatalf("auth url leaked a credential: %q", authURL)
	}
}

// TestGatewayOAuthCancelIsConnectionBoundLikeRust pins the cancel semantics:
// another connection cannot cancel my sign-in, and cancellation is acknowledged
// only after the active login releases its slot.
func TestGatewayOAuthCancelIsConnectionBoundLikeRust(t *testing.T) {
	router, _, _ := newGatewayOAuthRouter(t)
	notifications := gatewayOAuthNotifications(t, router)
	initializeGatewayOAuthConnection(t, router, "connection-1")
	initializeGatewayOAuthConnection(t, router, "connection-2")

	loginDone := make(chan *Response, 1)
	go func() {
		request := requestWithParams(t, IntID(1), MethodGatewayOAuthLogin, nil)
		request.ConnectionID = "connection-1"
		loginDone <- router.Handle(request)
	}()
	waitForGatewayAuthURL(t, notifications, loginDone)

	// A different connection owns no sign-in.
	other := requestWithParams(t, IntID(2), MethodGatewayOAuthCancel, nil)
	other.ConnectionID = "connection-2"
	response := router.Handle(other)
	if response.Error == nil || response.Error.Code != JSONRPCInvalidRequestErrorCode ||
		response.Error.Message != "Gateway sign-in belongs to another connection" {
		t.Fatalf("cancel from another connection = %+v", response.Error)
	}

	cancel := requestWithParams(t, IntID(3), MethodGatewayOAuthCancel, nil)
	cancel.ConnectionID = "connection-1"
	canceled := router.Handle(cancel)
	if canceled.Error != nil {
		t.Fatalf("cancel error = %+v", canceled.Error)
	}
	select {
	case response := <-loginDone:
		if response.Error == nil || response.Error.Message != "Gateway sign-in was canceled" {
			t.Fatalf("canceled sign-in response = %+v (%s)", response, response.Error.Message)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("canceled sign-in did not return")
	}

	// A second sign-in is accepted once the first released its slot.
	second := requestWithParams(t, IntID(4), MethodGatewayOAuthCancel, nil)
	second.ConnectionID = "connection-1"
	if response := router.Handle(second); response.Error != nil {
		t.Fatalf("cancel with no active sign-in = %+v", response.Error)
	}
}

func waitForGatewayAuthURL(t *testing.T, notifications *[]*GatewayOAuthChangedNotification, loginDone <-chan *Response) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case response := <-loginDone:
			t.Fatalf("gateway sign-in returned before the URL handoff: %+v (%s)", response, response.Error.Message)
		default:
		}
		for _, notification := range *notifications {
			if notification.Status == GatewayOAuthStatusStarted && notification.AuthURL != nil {
				return *notification.AuthURL
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, notification := range *notifications {
		message := ""
		if notification.Error != nil {
			message = *notification.Error
		}
		t.Logf("notification: status=%s provider=%s error=%s", notification.Status, notification.ProviderID, message)
	}
	t.Fatal("no gateway auth url notification arrived")
	return ""
}

var _ = context.Background

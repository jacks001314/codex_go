package auth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func newGatewayLoginIssuer(t *testing.T) *httptest.Server {
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
		_, _ = writer.Write([]byte(`{"access_token":"explicit-token","token_type":"bearer","expires_in":3600,"refresh_token":"explicit-refresh"}`))
	}))
	t.Cleanup(issuer.Close)
	return issuer
}

// answerGatewayCallback fulfills the loopback handoff for an authorization URL.
func answerGatewayCallback(t *testing.T, authorizationURL string) {
	t.Helper()
	parsed, err := url.Parse(authorizationURL)
	if err != nil {
		t.Fatalf("parse authorization url: %v", err)
	}
	go func() {
		response, err := http.Get(parsed.Query().Get("redirect_uri") + "?code=code-1&state=" + url.QueryEscape(parsed.Query().Get("state")))
		if err == nil {
			_, _ = io.ReadAll(response.Body)
			_ = response.Body.Close()
		}
	}()
}

// TestGatewayAuthStatusAndExplicitLoginLikeRust pins Rust #47170/#47207: status
// starts notReady, reports started during a caller-initiated sign-in, reaches
// succeeded once the credential is persisted, and is shared with later managers
// for the same CODEX_HOME.
func TestGatewayAuthStatusAndExplicitLoginLikeRust(t *testing.T) {
	issuer := newGatewayLoginIssuer(t)
	home := t.TempDir()
	store := newGatewayTestStore()
	config := GatewayAuthConfig{
		AuthorizationURL: issuer.URL + "/authorize",
		TokenURL:         issuer.URL + "/token",
		ClientID:         "client-1",
	}
	manager := NewGatewayAuthManager(config, home, issuer.Client(), store)

	status, err := manager.Status()
	if err != nil || status.Kind != GatewayAuthStatusNotReady {
		t.Fatalf("initial status = %#v, %v; want notReady", status, err)
	}

	changes, unsubscribe := manager.SubscribeStatus()
	defer unsubscribe()

	started := make(chan string, 1)
	loginErr := make(chan error, 1)
	go func() {
		loginErr <- manager.LoginWithBrowser(context.Background(), func(url string) { started <- url })
	}()

	var authorizationURL string
	select {
	case authorizationURL = <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("no authorization URL handoff")
	}
	if !strings.Contains(authorizationURL, "code_challenge=") {
		t.Fatalf("authorization url = %q, want a PKCE challenge", authorizationURL)
	}
	if status, err := manager.Status(); err != nil || status.Kind != GatewayAuthStatusStarted {
		t.Fatalf("status during sign-in = %#v, %v; want started", status, err)
	}
	answerGatewayCallback(t, authorizationURL)
	if err := <-loginErr; err != nil {
		t.Fatalf("LoginWithBrowser() error = %v", err)
	}
	if status, err := manager.Status(); err != nil || status.Kind != GatewayAuthStatusSucceeded {
		t.Fatalf("status after sign-in = %#v, %v; want succeeded", status, err)
	}
	if value, ok, err := manager.storage.load(gatewayCredentialID(home, config)); err != nil || !ok || !strings.Contains(value, "explicit-token") {
		t.Fatalf("stored credential = %q, %v, %v", value, ok, err)
	}

	// Published changes reach subscribers, and a manager created later for the
	// same home observes the shared status.
	var sawStarted, sawSucceeded bool
	deadline := time.After(5 * time.Second)
	for !sawStarted || !sawSucceeded {
		select {
		case change := <-changes:
			switch change.Status.Kind {
			case GatewayAuthStatusStarted:
				sawStarted = true
			case GatewayAuthStatusSucceeded:
				sawSucceeded = true
			}
		case <-deadline:
			t.Fatalf("published changes: started=%v succeeded=%v", sawStarted, sawSucceeded)
		}
	}
	second := NewGatewayAuthManager(config, home, issuer.Client(), store)
	if status, err := second.Status(); err != nil || status.Kind != GatewayAuthStatusSucceeded {
		t.Fatalf("status from a later manager = %#v, %v; want the shared succeeded status", status, err)
	}
}

// TestGatewayExplicitLoginRejectsConcurrentAndCancellableLikeRust pins the
// one-sign-in-at-a-time guard and the cancellation path.
func TestGatewayExplicitLoginRejectsConcurrentAndCancellableLikeRust(t *testing.T) {
	issuer := newGatewayLoginIssuer(t)
	manager := NewGatewayAuthManager(GatewayAuthConfig{
		AuthorizationURL: issuer.URL + "/authorize",
		TokenURL:         issuer.URL + "/token",
		ClientID:         "client-1",
	}, t.TempDir(), issuer.Client(), newGatewayTestStore())

	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan string, 1)
	loginErr := make(chan error, 1)
	go func() {
		loginErr <- manager.LoginWithBrowser(ctx, func(url string) { started <- url })
	}()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("no authorization URL handoff")
	}

	if err := manager.LoginWithBrowser(context.Background(), func(string) {}); !errors.Is(err, ErrGatewayLoginInProgress) {
		t.Fatalf("concurrent sign-in error = %v, want the in-progress guard", err)
	}

	cancel()
	if err := <-loginErr; !errors.Is(err, ErrGatewayLoginCanceled) {
		t.Fatalf("canceled sign-in error = %v, want the cancellation error", err)
	}
	status, err := manager.Status()
	if err != nil || status.Kind != GatewayAuthStatusFailed || status.Message != ErrGatewayLoginCanceled.Error() {
		t.Fatalf("status after cancellation = %#v, %v", status, err)
	}
}

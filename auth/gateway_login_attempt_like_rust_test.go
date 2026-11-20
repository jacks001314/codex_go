package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newGatewayAttemptManager(t *testing.T, config GatewayAuthConfig) *GatewayAuthManager {
	t.Helper()
	return NewGatewayAuthManager(config, t.TempDir(), nil, newGatewayTestStore())
}

// Mirrors Rust #62079 start_login_if_required: a saved sign-in is reused without
// reserving, an absent one reserves this credential for the caller, and a second
// attempt is refused until the first reservation is released.
func TestGatewayLoginAttemptReservesCredentialLikeRust(t *testing.T) {
	home := t.TempDir()
	manager := NewGatewayAuthManager(GatewayAuthConfig{
		TokenURL:         "https://issuer.example/token",
		ClientID:         "client-1",
		CredentialsStore: "file",
	}, home, nil, newGatewayTestStore())

	first, err := manager.LoginIfRequired()
	if err != nil {
		t.Fatalf("LoginIfRequired() error = %v", err)
	}
	if first == nil {
		t.Fatal("LoginIfRequired() reused a grant that does not exist")
	}
	if _, err := manager.LoginIfRequired(); !errors.Is(err, ErrGatewayLoginInProgress) {
		t.Fatalf("second LoginIfRequired() = %v, want a sign-in already in progress", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	// A released reservation lets the next attempt retry (Rust retries after a
	// canceled sign-in).
	retried, err := manager.LoginIfRequired()
	if err != nil || retried == nil {
		t.Fatalf("LoginIfRequired() after release = %v, %v", retried, err)
	}
	if err := retried.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// A different resource is a different credential, so its sign-in is independent.
	other := NewGatewayAuthManager(GatewayAuthConfig{
		TokenURL:         "https://issuer.example/token",
		ClientID:         "client-1",
		Resource:         "https://api.example.com",
		CredentialsStore: "file",
	}, home, nil, newGatewayTestStore())
	attempt, err := other.LoginIfRequired()
	if err != nil || attempt == nil {
		t.Fatalf("independent credential LoginIfRequired() = %v, %v", attempt, err)
	}
	if err := attempt.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

// A stored grant keeps the saved sign-in instead of reserving a new one.
func TestGatewayLoginIfRequiredReusesSavedGrantLikeRust(t *testing.T) {
	manager := newGatewayAttemptManager(t, GatewayAuthConfig{
		TokenURL:         "https://issuer.example/token",
		ClientID:         "client-1",
		CredentialsStore: "file",
	})
	if err := manager.saveToken(&gatewayStoredToken{AccessToken: "access-1", AuthorizationID: "grant-1"}); err != nil {
		t.Fatalf("saveToken() error = %v", err)
	}
	attempt, err := manager.LoginIfRequired()
	if err != nil {
		t.Fatalf("LoginIfRequired() error = %v", err)
	}
	if attempt != nil {
		t.Fatalf("LoginIfRequired() reserved a sign-in for a saved grant: %#v", attempt)
	}
}

// Mirrors Rust #62079 device authorization: the configured OAuth resource is
// sent with the device authorization request and every token poll.
func TestGatewayDeviceAuthorizationCarriesResourceLikeRust(t *testing.T) {
	var server *httptest.Server
	deviceResource := ""
	pollResource := ""
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = request.ParseForm()
		switch request.URL.Path {
		case "/device":
			deviceResource = request.Form.Get("resource")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"device_code":      "device-1",
				"user_code":        "ABCD-1234",
				"verification_uri": server.URL + "/activate",
				"expires_in":       30,
				"interval":         1,
			})
		case "/token":
			pollResource = request.Form.Get("resource")
			_, _ = w.Write([]byte(`{"access_token":"device-token","token_type":"bearer","expires_in":3600}`))
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()

	manager := NewGatewayAuthManager(GatewayAuthConfig{
		DeviceAuthorizationURL: server.URL + "/device",
		TokenURL:               server.URL + "/token",
		ClientID:               "client-1",
		Resource:               "https://api.example.com",
		CredentialsStore:       "file",
	}, t.TempDir(), server.Client(), newGatewayTestStore())
	if err := manager.LoginWithDeviceCode(context.Background(), nil); err != nil {
		t.Fatalf("LoginWithDeviceCode() error = %v", err)
	}
	if deviceResource != "https://api.example.com" {
		t.Fatalf("device authorization resource = %q, want the configured resource", deviceResource)
	}
	if pollResource != "https://api.example.com" {
		t.Fatalf("device token resource = %q, want the configured resource", pollResource)
	}
}

// A failed sign-in reports the caller's guidance for that credential kind, and a
// canceled attempt reports the canceled error instead.
func TestGatewaySignInFailureReportsGuidanceLikeRust(t *testing.T) {
	manager := newGatewayAttemptManager(t, GatewayAuthConfig{
		DeviceAuthorizationURL: "https://issuer.example/device",
		TokenURL:               "https://issuer.example/token",
		ClientID:               "client-1",
		CredentialsStore:       "file",
	})
	attempt, err := manager.reserveLogin()
	if err != nil {
		t.Fatalf("reserveLogin() error = %v", err)
	}
	defer func() { _ = attempt.Close() }()
	failure := gatewaySignInFailure(context.Background(), errors.New("boom"), "Config server sign-in failed. Check config_server.oauth and the credential store.")
	if failure == nil || failure.Error() != "Config server sign-in failed. Check config_server.oauth and the credential store." {
		t.Fatalf("failure = %v, want the caller guidance", failure)
	}
	if !errors.Is(gatewaySignInFailure(context.Background(), context.Canceled, "ignored"), ErrGatewayLoginCanceled) {
		t.Fatal("a canceled sign-in must report the canceled error")
	}
	if !errors.Is(gatewaySignInFailure(context.Background(), ErrGatewayDeviceCodeDenied, "ignored"), ErrGatewayDeviceCodeDenied) {
		t.Fatal("a device-flow cause must keep its own guidance")
	}
}

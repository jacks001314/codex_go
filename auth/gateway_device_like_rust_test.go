package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// Rust #59822: device sign-in presents the verification URL and code, polls
// while the issuer reports authorization_pending, and persists the grant.
func TestGatewayAuthManagerLoginWithDeviceCodeLikeRust(t *testing.T) {
	var tokenPolls atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/device":
			_ = request.ParseForm()
			if request.Form.Get("client_id") != "client-1" {
				t.Errorf("device client_id = %q", request.Form.Get("client_id"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"device_code":      "device-1",
				"user_code":        "ABCD-1234",
				"verification_uri": server.URL + "/activate",
				"expires_in":       30,
				"interval":         1,
			})
		case "/token":
			_ = request.ParseForm()
			if request.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:device_code" ||
				request.Form.Get("device_code") != "device-1" {
				t.Errorf("token form = %v", request.Form)
			}
			if tokenPolls.Add(1) == 1 {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"device-token","token_type":"bearer","expires_in":3600,"refresh_token":"device-refresh"}`))
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()

	manager := NewGatewayAuthManager(GatewayAuthConfig{
		DeviceAuthorizationURL: server.URL + "/device",
		TokenURL:               server.URL + "/token",
		ClientID:               "client-1",
		CredentialsStore:       "file",
	}, t.TempDir(), server.Client(), newGatewayTestStore())
	var prompt GatewayDeviceCodePrompt
	if err := manager.LoginWithDeviceCode(context.Background(), func(value GatewayDeviceCodePrompt) {
		prompt = value
	}); err != nil {
		t.Fatalf("LoginWithDeviceCode() error = %v", err)
	}
	if prompt.UserCode != "ABCD-1234" || !strings.HasSuffix(prompt.VerificationURL, "/activate") {
		t.Fatalf("prompt = %#v", prompt)
	}
	if status, err := manager.Status(); err != nil || status.Kind != GatewayAuthStatusSucceeded {
		t.Fatalf("status = %#v, %v", status, err)
	}
	if tokenPolls.Load() != 2 {
		t.Fatalf("token polls = %d, want one pending poll then success", tokenPolls.Load())
	}
	token, err := manager.ResolveAccessTokenWithoutLogin(context.Background())
	if err != nil || token != "device-token" {
		t.Fatalf("stored token = %q, %v", token, err)
	}
}

// Rust #59822: denied and expired codes stop polling with a stable message.
func TestGatewayAuthManagerLoginWithDeviceCodeReportsDenialLikeRust(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/device":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"device_code":      "device-2",
				"user_code":        "DENY-0001",
				"verification_uri": server.URL + "/activate",
				"expires_in":       30,
				"interval":         1,
			})
		case "/token":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"access_denied"}`))
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()

	manager := NewGatewayAuthManager(GatewayAuthConfig{
		DeviceAuthorizationURL: server.URL + "/device",
		TokenURL:               server.URL + "/token",
		ClientID:               "client-1",
		CredentialsStore:       "file",
	}, t.TempDir(), server.Client(), newGatewayTestStore())
	err := manager.LoginWithDeviceCode(context.Background(), nil)
	if !errors.Is(err, ErrGatewayDeviceCodeDenied) {
		t.Fatalf("LoginWithDeviceCode() error = %v, want denied", err)
	}
}

// Rust #59822: a non-loopback plain-HTTP verification URL is rejected.
func TestGatewayDeviceVerificationURLRejectsInsecureTargetsLikeRust(t *testing.T) {
	if _, err := validateGatewayDeviceVerificationURL("http://example.com/activate"); !errors.Is(err, ErrGatewayDeviceVerificationURL) {
		t.Fatalf("insecure URL error = %v", err)
	}
	if got, err := validateGatewayDeviceVerificationURL("https://issuer.example.com/activate"); err != nil || got == "" {
		t.Fatalf("https URL = %q, %v", got, err)
	}
	if got, err := validateGatewayDeviceVerificationURL("http://127.0.0.1:9000/activate"); err != nil || got == "" {
		t.Fatalf("loopback URL = %q, %v", got, err)
	}
}

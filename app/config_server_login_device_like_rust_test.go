package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"codex_go/auth"
	"codex_go/config"
)

// newConfigServerDeviceServer serves device authorization and token polling for a
// config server, and reports how many device requests were made.
func newConfigServerDeviceServer(t *testing.T, deviceRequests *atomic.Int32) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = r.ParseForm()
		switch r.URL.Path {
		case "/device":
			if deviceRequests != nil {
				deviceRequests.Add(1)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"device_code":      "device-1",
				"user_code":        "WXYZ-9876",
				"verification_uri": server.URL + "/activate",
				"expires_in":       30,
				"interval":         1,
			})
		case "/token":
			_, _ = w.Write([]byte(`{"access_token":"config-token","token_type":"bearer","expires_in":3600}`))
		default:
			http.NotFound(w, r)
		}
	}))
	return server
}

func configServerDeviceRequirements(serverURL string, deviceAuthorization bool) *config.ConfigRequirements {
	oauth := &config.ConfigServerOAuth{
		AuthorizationURL: serverURL + "/authorize",
		TokenURL:         serverURL + "/token",
		ClientID:         "company-client",
	}
	if deviceAuthorization {
		deviceURL := serverURL + "/device"
		oauth.DeviceAuthorizationURL = &deviceURL
	}
	return &config.ConfigRequirements{ConfigServer: &config.ConfigServer{
		BaseURL: serverURL + "/settings",
		OAuth:   oauth,
	}}
}

// Mirrors Rust #62079: a required config server signs in with device
// authorization when the CLI selected it, presents the code, and saves the grant
// so the next launch reuses it.
func TestConfigServerDeviceSignInLikeRust(t *testing.T) {
	var deviceRequests atomic.Int32
	server := newConfigServerDeviceServer(t, &deviceRequests)
	defer server.Close()
	home := t.TempDir()
	store := newConfigServerTestStore()
	requirements := configServerDeviceRequirements(server.URL, true)

	var prompt auth.GatewayDeviceCodePrompt
	method := ConfigServerLoginMethod{
		DeviceCode: true,
		OnURL: func(string) {
			t.Error("device sign-in must not open a browser authorization")
		},
		Present: func(value auth.GatewayDeviceCodePrompt) { prompt = value },
	}
	if err := ensureConfigServerLoginWith(context.Background(), home, requirements, server.Client(), store, method); err != nil {
		t.Fatalf("ensureConfigServerLoginWith() error = %v", err)
	}
	if prompt.UserCode != "WXYZ-9876" || !strings.HasSuffix(prompt.VerificationURL, "/activate") {
		t.Fatalf("device prompt = %#v", prompt)
	}
	if deviceRequests.Load() != 1 {
		t.Fatalf("device authorization requests = %d, want 1", deviceRequests.Load())
	}
	// The saved grant is reused, so no second device authorization is requested.
	if err := ensureConfigServerLoginWith(context.Background(), home, requirements, server.Client(), store, method); err != nil {
		t.Fatalf("second ensureConfigServerLoginWith() error = %v", err)
	}
	if deviceRequests.Load() != 1 {
		t.Fatalf("device authorization requests = %d after reuse, want 1", deviceRequests.Load())
	}
}

// A device sign-in needs the config server's device endpoint; the error names the
// configuration key instead of failing later inside the flow.
func TestConfigServerDeviceSignInRequiresEndpointLikeRust(t *testing.T) {
	server := newConfigServerDeviceServer(t, nil)
	defer server.Close()
	err := ensureConfigServerLoginWith(context.Background(), t.TempDir(), configServerDeviceRequirements(server.URL, false), server.Client(), newConfigServerTestStore(), ConfigServerLoginMethod{
		DeviceCode: true,
		Present:    func(auth.GatewayDeviceCodePrompt) {},
	})
	if err == nil || !strings.Contains(err.Error(), "config_server.oauth.device_authorization_url") {
		t.Fatalf("ensureConfigServerLoginWith() = %v, want the missing device endpoint", err)
	}
}

// A config-server sign-in failure reports the config_server.oauth guidance rather
// than a generic gateway message.
func TestConfigServerSignInFailureGuidanceLikeRust(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer server.Close()
	err := ensureConfigServerLoginWith(context.Background(), t.TempDir(), configServerDeviceRequirements(server.URL, true), server.Client(), newConfigServerTestStore(), ConfigServerLoginMethod{
		DeviceCode: true,
		Present:    func(auth.GatewayDeviceCodePrompt) {},
	})
	if err == nil || err.Error() != ConfigServerLoginFailureMessage {
		t.Fatalf("ensureConfigServerLoginWith() = %v, want %q", err, ConfigServerLoginFailureMessage)
	}
}

// One sign-in per credential: a second attempt is refused while the first holds
// the reservation, and a retry succeeds after it is released (Rust #62079).
func TestConfigServerSignInReservesCredentialLikeRust(t *testing.T) {
	var deviceRequests atomic.Int32
	server := newConfigServerDeviceServer(t, &deviceRequests)
	defer server.Close()
	home := t.TempDir()
	store := newConfigServerTestStore()
	requirements := configServerDeviceRequirements(server.URL, true)
	method := ConfigServerLoginMethod{DeviceCode: true, Present: func(auth.GatewayDeviceCodePrompt) {}}

	held, err := newConfigServerAuthManager(home, requirements.ConfigServer, server.Client(), store).LoginIfRequired()
	if err != nil || held == nil {
		t.Fatalf("LoginIfRequired() = %v, %v", held, err)
	}
	if err := ensureConfigServerLoginWith(context.Background(), home, requirements, server.Client(), store, method); !errors.Is(err, auth.ErrGatewayLoginInProgress) {
		t.Fatalf("concurrent ensureConfigServerLoginWith() = %v, want a sign-in in progress", err)
	}
	if err := held.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := ensureConfigServerLoginWith(context.Background(), home, requirements, server.Client(), store, method); err != nil {
		t.Fatalf("ensureConfigServerLoginWith() after release error = %v", err)
	}
}

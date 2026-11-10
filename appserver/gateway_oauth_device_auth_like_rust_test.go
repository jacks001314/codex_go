package appserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"codex_go/config"
)

// newGatewayOAuthDeviceRouter configures a gateway issuer that offers device
// authorization, mirroring Rust's message_processor_gateway_oauth_tests (#60486).
func newGatewayOAuthDeviceRouter(t *testing.T) (*RuntimeRouter, string) {
	t.Helper()
	var polls atomic.Int32
	issuer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/device":
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"device_code":      "device-1",
				"user_code":        "ABCD-1234",
				"verification_uri": "https://gateway.example.com/activate",
				"expires_in":       30,
				"interval":         1,
			})
		case "/token":
			if polls.Add(1) == 1 {
				writer.WriteHeader(http.StatusBadRequest)
				_, _ = writer.Write([]byte("{\"error\":\"authorization_pending\"}"))
				return
			}
			_, _ = writer.Write([]byte("{\"access_token\":\"gateway-token\",\"token_type\":\"bearer\",\"expires_in\":3600,\"refresh_token\":\"gateway-refresh\"}"))
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(issuer.Close)

	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	configBody := strings.Join([]string{
		"model = \"gpt-5.4\"",
		"model_provider = \"gateway\"",
		"",
		"[model_providers.gateway]",
		"name = \"Gateway provider\"",
		"base_url = \"" + issuer.URL + "/v1\"",
		"wire_api = \"responses\"",
		"",
		"[model_providers.gateway.gateway_oauth]",
		"authorization_url = \"" + issuer.URL + "/authorize\"",
		"device_authorization_url = \"" + issuer.URL + "/device\"",
		"token_url = \"" + issuer.URL + "/token\"",
		"client_id = \"client-1\"",
		"delivery = { kind = \"header\", name = \"x-gateway-token\" }",
		gatewayOAuthCredentialsStoreLine(),
		"",
	}, "\n")
	if err := os.WriteFile(config.ConfigPath(home), []byte(configBody), 0o600); err != nil {
		t.Fatalf("write config error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home)})
	t.Cleanup(func() { _ = router.Close() })
	return router, home
}

// Rust #60486: read advertises device authorization, login answers with the
// device prompt before approval, the initiating connection receives the
// correlated completion, and saved credentials satisfy the next login without
// another grant.
func TestGatewayOAuthDeviceAuthorizationLikeRust(t *testing.T) {
	router, _ := newGatewayOAuthDeviceRouter(t)
	notifications := gatewayOAuthNotifications(t, router)
	initializeGatewayOAuthConnection(t, router, "connection-1")

	read := router.Handle(requestWithParams(t, IntID(1), MethodGatewayOAuthRead, nil))
	if read.Error != nil {
		t.Fatalf("account/gatewayOAuth/read error = %+v", read.Error)
	}
	readResponse, ok := read.Result.(*GatewayOAuthReadResponse)
	if !ok || !readResponse.SupportsDeviceAuth {
		t.Fatalf("read response = %#v, want supportsDeviceAuth", read.Result)
	}

	request := requestWithParams(t, IntID(2), MethodGatewayOAuthLogin, map[string]any{"deviceAuth": true})
	request.ConnectionID = "connection-1"
	response := router.Handle(request)
	if response.Error != nil {
		t.Fatalf("account/gatewayOAuth/login error = %+v", response.Error)
	}
	login, ok := response.Result.(*GatewayOAuthLoginResponse)
	if !ok || login.Prompt == nil {
		t.Fatalf("login response = %#v, want a device prompt", response.Result)
	}
	prompt := login.Prompt
	if prompt.UserCode != "ABCD-1234" || !strings.HasSuffix(prompt.VerificationURL, "/activate") || strings.TrimSpace(prompt.LoginID) == "" {
		t.Fatalf("device prompt = %#v", prompt)
	}

	deadline := time.Now().Add(15 * time.Second)
	for {
		completed := false
		for _, notification := range notifications.snapshot() {
			if notification.LoginID == nil || *notification.LoginID != prompt.LoginID {
				continue
			}
			if notification.Status != GatewayOAuthStatusSucceeded || notification.ProviderID != "gateway" {
				t.Fatalf("correlated notification = %#v", notification)
			}
			completed = true
		}
		if completed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no completion notification for login id %q", prompt.LoginID)
		}
		time.Sleep(20 * time.Millisecond)
	}

	second := requestWithParams(t, IntID(3), MethodGatewayOAuthLogin, map[string]any{"deviceAuth": true})
	second.ConnectionID = "connection-1"
	secondResponse := router.Handle(second)
	if secondResponse.Error != nil {
		t.Fatalf("second login error = %+v", secondResponse.Error)
	}
	secondLogin, ok := secondResponse.Result.(*GatewayOAuthLoginResponse)
	if !ok || secondLogin.Prompt != nil {
		t.Fatalf("second login = %#v, want no prompt for the saved grant", secondResponse.Result)
	}
}

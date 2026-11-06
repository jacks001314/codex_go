package appserver

import (
	"os"
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/turn"
)

// newGatewayOAuthProviderSelectionRouter configures three providers: the
// default gateway, a plain provider, and a second gateway that shares the
// default gateway's OAuth credential (Rust #59819).
func newGatewayOAuthProviderSelectionRouter(t *testing.T) *RuntimeRouter {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	configBody := strings.Join([]string{
		`model = "gpt-5.4"`,
		`model_provider = "gateway"`,
		``,
		`[model_providers.gateway]`,
		`name = "Gateway provider"`,
		`base_url = "http://127.0.0.1:9/v1"`,
		`wire_api = "responses"`,
		``,
		`[model_providers.gateway.gateway_oauth]`,
		`authorization_url = "http://127.0.0.1:9/authorize"`,
		`token_url = "http://127.0.0.1:9/token"`,
		`client_id = "client-1"`,
		`delivery = { kind = "header", name = "x-gateway-token" }`,
		gatewayOAuthCredentialsStoreLine(),
		``,
		`[model_providers.gateway-copy]`,
		`name = "Gateway copy"`,
		`base_url = "http://127.0.0.1:9/v1"`,
		`wire_api = "responses"`,
		``,
		`[model_providers.gateway-copy.gateway_oauth]`,
		`authorization_url = "http://127.0.0.1:9/authorize"`,
		`token_url = "http://127.0.0.1:9/token"`,
		`client_id = "client-1"`,
		`delivery = { kind = "header", name = "x-gateway-token" }`,
		gatewayOAuthCredentialsStoreLine(),
		``,
		`[model_providers.plain]`,
		`name = "Plain provider"`,
		`base_url = "http://127.0.0.1:9/v1"`,
		`wire_api = "responses"`,
		``,
	}, "\n")
	if err := os.WriteFile(config.ConfigPath(home), []byte(configBody), 0o600); err != nil {
		t.Fatalf("write config error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home)})
	t.Cleanup(func() { _ = router.Close() })
	return router
}

// Rust #59819: an explicit selector reads another configured provider without
// changing the default provider.
func TestGatewayOAuthReadSelectsConfiguredProviderLikeRust(t *testing.T) {
	router := newGatewayOAuthProviderSelectionRouter(t)
	response := router.Handle(requestWithParams(t, IntID(1), MethodGatewayOAuthRead, map[string]any{"modelProvider": "plain"}))
	if response.Error != nil {
		t.Fatalf("account/gatewayOAuth/read error = %+v", response.Error)
	}
	read, ok := response.Result.(*GatewayOAuthReadResponse)
	if !ok {
		t.Fatalf("read result = %#v", response.Result)
	}
	if read.ProviderID != "plain" || read.ProviderName != "Plain provider" || read.Required || read.Status != nil {
		t.Fatalf("read response = %#v", read)
	}
	// The default provider is untouched.
	response = router.Handle(requestWithParams(t, IntID(2), MethodGatewayOAuthRead, nil))
	current, ok := response.Result.(*GatewayOAuthReadResponse)
	if !ok || current.ProviderID != "gateway" || !current.Required {
		t.Fatalf("default read after selection = %#v", response.Result)
	}
}

// Rust #59819: an unknown explicit provider is rejected with the structured
// modelProviderNotConfigured error.
func TestGatewayOAuthReadRejectsUnknownProviderLikeRust(t *testing.T) {
	router := newGatewayOAuthProviderSelectionRouter(t)
	response := router.Handle(requestWithParams(t, IntID(1), MethodGatewayOAuthRead, map[string]any{"modelProvider": "missing"}))
	if response.Error == nil {
		t.Fatalf("read for an unknown provider succeeded: %#v", response.Result)
	}
	data := response.Error.Data
	if data["type"] != string(ModelProviderNotConfigured) || data["modelProvider"] != "missing" {
		t.Fatalf("error data = %#v", response.Error.Data)
	}
}

// Rust #59819: every configured provider sharing the changed credential is
// reported; providers with a different gateway stay out.
func TestGatewayOAuthCredentialProviderIDsLikeRust(t *testing.T) {
	router := newGatewayOAuthProviderSelectionRouter(t)
	cfg, err := router.effectiveConfigForTurn(&turn.TurnStartParams{})
	if err != nil {
		t.Fatalf("effective config error = %v", err)
	}
	ids := gatewayOAuthCredentialProviderIDs(configValues(cfg), stringConfigValue(cfg, "openai_base_url"), "gateway")
	if len(ids) != 2 || ids[0] != "gateway" || ids[1] != "gateway-copy" {
		t.Fatalf("credential providers = %#v", ids)
	}
}

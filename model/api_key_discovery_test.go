package model

import (
	"testing"

	"codex_go/auth"
)

// Mirrors Rust #44392: API-key model discovery is default-off, gates cached
// catalogs as well as requests, and treats the fetched catalog as authoritative.
func TestAPIKeyModelDiscoveryGateAndAuthoritativeCatalog(t *testing.T) {
	endpoint := &recordingModelsEndpoint{responses: []*ModelsEndpointResponse{{
		Models: []ModelInfo{{
			Slug:           "api-key-remote-only",
			DisplayName:    "API Key Remote Only",
			Visibility:     VisibilityList,
			SupportedInAPI: true,
		}},
	}}}
	manager := NewRemoteModelsManagerWithOptions(&RemoteModelsManagerOptions{
		ModelCatalog: &ModelsResponse{Models: []ModelInfo{{
			Slug:           "bundled",
			DisplayName:    "Bundled",
			Visibility:     VisibilityVisible,
			SupportedInAPI: true,
		}}},
		Endpoint:             endpoint,
		SupportsAPIKeyModels: true,
		APIKeyAuth:           true,
	})
	if !manager.SupportsAPIKeyDiscovery() {
		t.Fatal("SupportsAPIKeyDiscovery() = false, want true")
	}

	disabled := manager.RawModelCatalog(RefreshOnline)
	if endpoint.calls != 0 {
		t.Fatalf("disabled discovery made %d requests, want 0", endpoint.calls)
	}
	if len(disabled.Models) != 1 || disabled.Models[0].Slug != "bundled" {
		t.Fatalf("disabled catalog = %#v, want bundled", disabled.Models)
	}

	manager.SetAPIKeyModelDiscoveryEnabled(true)
	enabled := manager.RawModelCatalog(RefreshOnline)
	if endpoint.calls != 1 {
		t.Fatalf("enabled discovery made %d requests, want 1", endpoint.calls)
	}
	if len(enabled.Models) != 1 || enabled.Models[0].Slug != "api-key-remote-only" {
		t.Fatalf("enabled catalog = %#v, want authoritative remote catalog", enabled.Models)
	}
}

func TestAPIKeyDiscoveryRequiresAPIKeyAuthWithoutCommandAuth(t *testing.T) {
	cases := []struct {
		name string
		opts RemoteModelsManagerOptions
		want bool
	}{
		{"openai api key", RemoteModelsManagerOptions{SupportsAPIKeyModels: true, APIKeyAuth: true}, true},
		{"chatgpt auth", RemoteModelsManagerOptions{SupportsAPIKeyModels: true}, false},
		{"command auth", RemoteModelsManagerOptions{SupportsAPIKeyModels: true, APIKeyAuth: true, CommandAuth: true}, false},
		{"non-openai provider", RemoteModelsManagerOptions{APIKeyAuth: true}, false},
	}
	for _, tc := range cases {
		manager := NewRemoteModelsManagerWithOptions(&tc.opts)
		if got := manager.SupportsAPIKeyDiscovery(); got != tc.want {
			t.Fatalf("%s: SupportsAPIKeyDiscovery() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestConfiguredProviderRoutesAPIKeyModelDiscoveryToCodexBackend(t *testing.T) {
	apiKeyAuth := &auth.AuthDotJSON{AuthMode: "api-key", OpenAIAPIKey: "sk-test"}
	for name, baseURL := range map[string]struct {
		base string
		want string
	}{
		"default openai base url": {"", ChatGPTCodexBaseURL},
		"explicit base url":       {"https://example.com/codex", "https://example.com/codex"},
	} {
		provider := CreateRuntimeProvider(CreateOpenAIProvider(baseURL.base), apiKeyAuth)
		configured, ok := provider.(*ConfiguredProvider)
		if !ok {
			t.Fatalf("%s: provider type = %T", name, provider)
		}
		manager, ok := configured.ModelsManager(nil).(*RemoteModelsManager)
		if !ok {
			t.Fatalf("%s: manager type = %T", name, configured.ModelsManager(nil))
		}
		endpoint, ok := manager.endpoint.(*HTTPModelsEndpoint)
		if !ok {
			t.Fatalf("%s: endpoint type = %T", name, manager.endpoint)
		}
		if endpoint.Provider.BaseURL != baseURL.want {
			t.Fatalf("%s: models base URL = %q, want %q", name, endpoint.Provider.BaseURL, baseURL.want)
		}
	}
}

func TestAuthUsesAPIKey(t *testing.T) {
	if !authUsesAPIKey(&auth.AuthDotJSON{AuthMode: "api-key", OpenAIAPIKey: "sk-test"}) {
		t.Fatal("api-key auth must be detected")
	}
	if authUsesAPIKey(&auth.AuthDotJSON{AuthMode: "chatgpt", Tokens: map[string]any{"access_token": "token"}}) {
		t.Fatal("chatgpt auth must not be treated as an API key")
	}
	if authUsesAPIKey(nil) {
		t.Fatal("nil auth must not be treated as an API key")
	}
}

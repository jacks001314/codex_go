package model

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func modelCatalogTestServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *string) {
	t.Helper()
	requested := new(string)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*requested = r.URL.String()
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	return server, requested
}

// Mirrors Rust `explicit_catalog_reuses_auth_headers_and_query_parameters`
// (#46561): the explicit catalog URL keeps its own query, gains the provider
// query parameters and the client version, and reuses provider authentication.
func TestModelsCatalogURLReusesAuthHeadersAndQueryParametersLikeRust(t *testing.T) {
	var authHeader string
	server, requested := modelCatalogTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[{"slug":"provider-model","display_name":"Provider","visibility":"list","supported_in_api":true}]}`))
	})
	provider := &APIProvider{
		BaseURL:     "https://gateway.example/v1",
		QueryParams: map[string]string{"api-version": "2026 09"},
	}
	endpoint := &HTTPModelsEndpoint{
		Provider:   provider,
		Auth:       &AuthHeaders{Headers: http.Header{"Authorization": {"Bearer token"}}},
		CatalogURL: server.URL + "/codex/models?deployment=one",
	}
	response, err := endpoint.ListModels(context.Background(), "")
	if err != nil {
		t.Fatalf("ListModels error = %v", err)
	}
	if len(response.Models) != 1 || response.Models[0].Slug != "provider-model" {
		t.Fatalf("catalog models = %#v", response.Models)
	}
	if authHeader != "Bearer token" {
		t.Fatalf("catalog auth header = %q, want Bearer token", authHeader)
	}
	parsed, err := url.Parse(*requested)
	if err != nil {
		t.Fatalf("parse catalog request URL %q: %v", *requested, err)
	}
	if parsed.Path != "/codex/models" {
		t.Fatalf("catalog path = %q", parsed.Path)
	}
	query := parsed.Query()
	for key, want := range map[string]string{
		"deployment":     "one",
		"api-version":    "2026 09",
		"client_version": modelsEndpointClientVersion,
	} {
		if got := query.Get(key); got != want {
			t.Fatalf("catalog query %s = %q, want %q (%s)", key, got, want, *requested)
		}
	}
}

func TestModelsCatalogURLRejectsRelativeURLsLikeRust(t *testing.T) {
	for _, catalogURL := range []string{"/codex/models", "codex/models"} {
		if _, err := modelsCatalogURL(&APIProvider{BaseURL: "https://gateway.example/v1"}, catalogURL); err == nil {
			t.Fatalf("modelsCatalogURL(%q) accepted a relative URL", catalogURL)
		}
	}
}

func TestModelsCatalogURLRejectsOversizedResponseLikeRust(t *testing.T) {
	body := make([]byte, modelsCatalogMaxBytes+1)
	copy(body, `{"models":[]}`)
	server, _ := modelCatalogTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	})
	endpoint := &HTTPModelsEndpoint{CatalogURL: server.URL + "/codex/models"}
	if _, err := endpoint.ListModels(context.Background(), ""); err == nil {
		t.Fatal("oversized catalog response was accepted")
	}
}

func TestModelsCatalogURLRejectsRedirectsWithoutForwardingCredentialsLikeRust(t *testing.T) {
	redirected := false
	target, _ := modelCatalogTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		redirected = true
		_, _ = w.Write([]byte(`{"models":[]}`))
	})
	server, _ := modelCatalogTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/codex/models", http.StatusTemporaryRedirect)
	})
	endpoint := &HTTPModelsEndpoint{
		Auth:       &AuthHeaders{Headers: http.Header{"Authorization": {"Bearer secret"}}},
		CatalogURL: server.URL + "/codex/models",
	}
	if _, err := endpoint.ListModels(context.Background(), ""); err == nil {
		t.Fatal("redirecting catalog response was accepted")
	}
	if redirected {
		t.Fatal("the redirect target received the credential-bearing catalog request")
	}
}

// Mirrors Rust's discovery gate (#46561): a custom inference base URL without
// an explicit catalog URL keeps the bundled catalog even when the feature is on.
func TestAPIKeyDiscoveryGateRequiresCatalogForCustomBaseURL(t *testing.T) {
	endpoint := &recordingModelsEndpoint{responses: []*ModelsEndpointResponse{{
		Models: []ModelInfo{{Slug: "remote", Visibility: VisibilityList, SupportedInAPI: true}},
	}}}
	manager := NewRemoteModelsManagerWithOptions(&RemoteModelsManagerOptions{
		Endpoint:             endpoint,
		SupportsAPIKeyModels: false,
		APIKeyAuth:           true,
	})
	manager.SetAPIKeyModelDiscoveryEnabled(true)
	catalog := manager.RawModelCatalog(RefreshOnline)
	if endpoint.calls != 0 {
		t.Fatalf("custom base URL without a catalog made %d requests, want 0", endpoint.calls)
	}
	for _, info := range catalog.Models {
		if info.Slug == "remote" {
			t.Fatalf("bundled catalog unexpectedly contained the remote model: %#v", catalog.Models)
		}
	}
}

func TestProviderSupportsAPIKeyModelsLikeRust(t *testing.T) {
	customBase := CreateOpenAIProvider("https://example.com/codex")
	if customBase.SupportsAPIKeyModels() {
		t.Fatal("custom base URL without a catalog URL must not support API-key models")
	}
	withCatalog := CreateOpenAIProvider("https://example.com/codex")
	withCatalog.ModelCatalogURL = "https://example.com/codex/models"
	if !withCatalog.SupportsAPIKeyModels() {
		t.Fatal("explicit catalog URL must support API-key models")
	}
	defaultBase := CreateOpenAIProvider("")
	if !defaultBase.SupportsAPIKeyModels() {
		t.Fatal("default OpenAI provider must support API-key models")
	}
}

func TestProviderInfoParsesModelCatalogURL(t *testing.T) {
	info, err := ProviderInfoFromConfig(map[string]any{
		"name":              "catalog-test",
		"base_url":          "https://example.com/v1",
		"model_catalog_url": "https://example.com/v1/models",
	})
	if err != nil {
		t.Fatalf("ProviderInfoFromConfig error = %v", err)
	}
	if info.ModelCatalogURL != "https://example.com/v1/models" {
		t.Fatalf("ModelCatalogURL = %q", info.ModelCatalogURL)
	}
	if info.isZero() {
		t.Fatal("provider with a catalog URL must not be considered zero")
	}
}

func TestProviderHasProviderAPIKeyLikeRust(t *testing.T) {
	if !(&ProviderInfo{EnvKey: "PROVIDER_KEY"}).HasProviderAPIKey() {
		t.Fatal("env_key must count as a provider API key")
	}
	if !(&ProviderInfo{ExperimentalBearerToken: "token"}).HasProviderAPIKey() {
		t.Fatal("experimental_bearer_token must count as a provider API key")
	}
	if (&ProviderInfo{Name: "openai"}).HasProviderAPIKey() {
		t.Fatal("a plain provider must not report a provider API key")
	}
}

// Mirrors Rust `cache_identity_tracks_catalog_url`.
func TestModelsCatalogIdentityTracksCatalogURL(t *testing.T) {
	provider := CreateOpenAIProvider("https://gateway.example/v1")
	bundled := ModelsCatalogIdentity(&provider, nil, nil, "")
	provider.ModelCatalogURL = "https://gateway.example/catalog-one"
	first := ModelsCatalogIdentity(&provider, nil, nil, "")
	provider.ModelCatalogURL = "https://gateway.example/catalog-two"
	second := ModelsCatalogIdentity(&provider, nil, nil, "")
	if bundled == first || first == second {
		t.Fatalf("catalog URL did not change the cache identity: %q %q %q", bundled, first, second)
	}
	if strings.TrimSpace(bundled) == "" || strings.TrimSpace(second) == "" {
		t.Fatalf("identity unexpectedly empty: %q %q", bundled, second)
	}
}

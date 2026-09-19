package model

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"codex_go/auth"
)

// TestManagedResidencyReachesProviderAndCatalogRequest mirrors Rust's
// enforce_managed_residency: a managed residency requirement adds the internal
// residency header to the provider (used by every request the provider makes)
// and to the model-catalog fetch.
func TestManagedResidencyReachesProviderAndCatalogRequest(t *testing.T) {
	newCatalogRequest := func(t *testing.T, residency string) string {
		t.Helper()
		var gotResidency string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotResidency = r.Header.Get(ResidencyHeaderName)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"data":[{"slug":"remote","display_name":"Remote","visibility":"visible","supported_in_api":true,"priority":0}]}`))
		}))
		t.Cleanup(server.Close)
		snapshot := auth.FromAPIKey("api-key")
		// A custom inference base URL alone no longer opts API-key sessions into
		// remote catalog discovery (Rust #46561); the residency header reaches
		// the catalog through an explicit catalog URL.
		info := CreateOpenAIProvider(server.URL + "/v1")
		info.ModelCatalogURL = server.URL + "/v1/models"
		provider := &ConfiguredProvider{
			providerID: "openai",
			info:       info,
			auth:       &snapshot,
		}
		if residency != "" {
			provider.SetManagedResidency(residency)
		}
		want := strings.TrimSpace(residency)
		apiProvider, err := provider.APIProvider()
		if err != nil {
			t.Fatalf("APIProvider() error = %v", err)
		}
		if value := apiProvider.Headers.Get(ResidencyHeaderName); value != want {
			t.Fatalf("provider residency header = %q, want %q", value, want)
		}
		manager := provider.ModelsManager(nil)
		// API-key catalogs are opt-in (Rust #44392), so enable discovery to make
		// the manager issue the catalog request.
		SetAPIKeyModelDiscoveryEnabled(manager, true)
		manager.ListModels(RefreshOnline)
		return gotResidency
	}

	if got := newCatalogRequest(t, ""); got != "" {
		t.Fatalf("catalog request residency header = %q, want empty", got)
	}
	// The requirement is trimmed before it becomes a header value.
	if got := newCatalogRequest(t, "  us  "); got != "us" {
		t.Fatalf("catalog request residency header = %q, want us", got)
	}
}

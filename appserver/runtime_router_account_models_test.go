package appserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"codex_go/config"
	"codex_go/model"
)

// TestAccountScopedModelsManagerReturnsManagerLikeRust verifies the app-server
// account-scoped catalog manager (Rust #41467) is wired and safe: it returns a
// non-nil manager, and in a fresh home (no auth) the lazy builder falls back to
// the bundled catalog without panicking or touching the network on an offline
// read.
func TestAccountScopedModelsManagerReturnsManagerLikeRust(t *testing.T) {
	home := t.TempDir()
	svc := config.NewConfigService(home)
	manager := accountScopedModelsManager(home, svc)
	if manager == nil {
		t.Fatal("accountScopedModelsManager returned nil")
	}
	_ = manager.ListModels(model.RefreshOffline)
}

// TestAccountScopedModelsManagerAppliesManagedResidencyLikeRust pins the
// managed `enforce_residency` requirement to the account-scoped catalog fetch:
// Rust sets the process-wide residency from the resolved config and
// enforce_managed_residency adds the header to the catalog request.
func TestAccountScopedModelsManagerAppliesManagedResidencyLikeRust(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test")
	var gotResidency string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotResidency = r.Header.Get(model.ResidencyHeaderName)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[{"slug":"remote","display_name":"Remote","visibility":"visible","supported_in_api":true,"priority":0}]}`))
	}))
	defer server.Close()

	read := &config.ConfigReadResponse{Config: map[string]any{
		"openai_base_url": server.URL + "/v1",
		"features":        map[string]any{"api_key_model_discovery": true},
	}}
	residency := config.ResidencyUS
	requirements := &config.ConfigRequirementsReadResponse{
		Requirements: &config.ConfigRequirements{EnforceResidency: &residency},
	}
	manager, err := buildAccountScopedModelsManager(t.TempDir(), read, requirements)
	if err != nil {
		t.Fatalf("buildAccountScopedModelsManager() error = %v", err)
	}
	manager.ListModels(model.RefreshOnline)
	if gotResidency != "us" {
		t.Fatalf("catalog request residency header = %q, want us", gotResidency)
	}

	// Without the requirement the header stays absent.
	gotResidency = ""
	manager, err = buildAccountScopedModelsManager(t.TempDir(), read, &config.ConfigRequirementsReadResponse{})
	if err != nil {
		t.Fatalf("buildAccountScopedModelsManager() error = %v", err)
	}
	manager.ListModels(model.RefreshOnline)
	if gotResidency != "" {
		t.Fatalf("catalog request residency header = %q, want empty", gotResidency)
	}
}

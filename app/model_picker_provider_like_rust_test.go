package app

import (
	"testing"

	"codex_go/model"
)

// TestModelPickerProviderScopedCatalogResolutionLikeRust mirrors Rust #63553's
// provider-scoped catalog lookup: a resumed thread on an alternate provider
// resolves that provider's catalog endpoint, while an empty provider keeps the
// configured default for new threads.
func TestModelPickerProviderScopedCatalogResolutionLikeRust(t *testing.T) {
	values := map[string]any{
		"model_provider": "acme",
		"model_providers": map[string]any{
			"acme": map[string]any{
				"name":              "Acme",
				"base_url":          "https://acme.example/v1",
				"model_catalog_url": "https://acme.example/catalog",
			},
		},
	}
	info, providerID, err := interactiveModelProviderInfo(values, "acme", t.TempDir())
	if err != nil {
		t.Fatalf("interactiveModelProviderInfo() error = %v", err)
	}
	if providerID != "acme" || info == nil || info.BaseURL != "https://acme.example/v1" ||
		info.ModelCatalogURL != "https://acme.example/catalog" {
		t.Fatalf("acme provider = id %q info %#v", providerID, info)
	}

	info, providerID, err = interactiveModelProviderInfo(values, "", t.TempDir())
	if err != nil {
		t.Fatalf("default provider resolution error = %v", err)
	}
	if providerID != "acme" || info == nil || info.BaseURL != "https://acme.example/v1" {
		t.Fatalf("configured default provider = id %q info %#v", providerID, info)
	}

	delete(values, "model_provider")
	info, providerID, err = interactiveModelProviderInfo(values, "", t.TempDir())
	if err != nil {
		t.Fatalf("built-in default resolution error = %v", err)
	}
	if providerID != model.OpenAIProviderID || info == nil {
		t.Fatalf("built-in default provider = id %q info %#v", providerID, info)
	}
}

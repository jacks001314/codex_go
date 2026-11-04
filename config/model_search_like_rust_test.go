package config

import "testing"

func TestClientSearchProviderAndManagedNativeRequirementLikeRust(t *testing.T) {
	cfg := &Config{Values: map[string]any{"tools": map[string]any{"client_search_provider": "search_mcp"}}}
	if got, ok := cfg.ClientSearchProvider(); !ok || got != "search_mcp" {
		t.Fatalf("ClientSearchProvider() = %q, %v", got, ok)
	}
	if cfg.RequiresNativeSearch() {
		t.Fatal("user-only search selection was treated as a managed requirement")
	}
	cfg.Requirements = &ConfigRequirements{FeatureRequirements: map[string]bool{"standalone_web_search": true}}
	if !cfg.RequiresNativeSearch() {
		t.Fatal("managed native search requirement was not detected")
	}
	if (&Config{}).RequiresNativeSearch() {
		t.Fatal("empty config requires native search")
	}
}

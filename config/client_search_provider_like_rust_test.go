package config

import "testing"

// Rust #62808 removed the tools.client_search_provider hint and the top-level
// client_search_provider member from ConfigReadResponse.
func TestConfigReadOmitsRemovedClientSearchProviderLikeRust(t *testing.T) {
	out := configValuesForJSON(map[string]any{
		"web_search": "live",
		"tools":      map[string]any{"client_search_provider": "search_mcp"},
	})
	if _, present := out["client_search_provider"]; present {
		t.Fatalf("config read response still exposes client_search_provider: %#v", out["client_search_provider"])
	}
}

// The removed table member is ignored like an unknown serde field; a stale
// value must not fail validation or regain meaning.
func TestConfigValidationIgnoresRemovedClientSearchProviderLikeRust(t *testing.T) {
	for _, provider := range []string{"standalone", "search_mcp", "bogus"} {
		values := map[string]any{"tools": map[string]any{"client_search_provider": provider}}
		if err := ValidateConfigValues(values); err != nil {
			t.Fatalf("ValidateConfigValues(%q) error = %v", provider, err)
		}
	}
}

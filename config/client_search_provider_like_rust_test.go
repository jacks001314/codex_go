package config

import "testing"

// Rust #59033: app-server configuration responses expose the effective
// tools.client_search_provider hint, null when the user did not configure it.
func TestConfigReadExposesClientSearchProviderLikeRust(t *testing.T) {
	values := map[string]any{"tools": map[string]any{"client_search_provider": "search_mcp"}}
	out := configValuesForJSON(values)
	if got := out["client_search_provider"]; got != "search_mcp" {
		t.Fatalf("client_search_provider = %#v, want search_mcp", got)
	}
	if _, present := out["client_search_provider"]; !present {
		t.Fatal("client_search_provider missing from config read response")
	}

	out = configValuesForJSON(map[string]any{"tools": map[string]any{}})
	value, present := out["client_search_provider"]
	if !present || value != nil {
		t.Fatalf("client_search_provider = %#v (present=%v), want present null", value, present)
	}
}

func TestConfigValidationAcceptsClientSearchProviderLikeRust(t *testing.T) {
	for _, provider := range []string{"standalone", "search_mcp"} {
		values := map[string]any{"tools": map[string]any{"client_search_provider": provider}}
		if err := ValidateConfigValues(values); err != nil {
			t.Fatalf("ValidateConfigValues(%s) = %v", provider, err)
		}
	}
	values := map[string]any{"tools": map[string]any{"client_search_provider": "bogus"}}
	if err := ValidateConfigValues(values); err == nil {
		t.Fatal("ValidateConfigValues accepted an unknown client_search_provider")
	}
}

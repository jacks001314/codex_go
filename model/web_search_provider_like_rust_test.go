package model

import (
	"encoding/json"
	"testing"
)

// Rust #59033: the resolved model may select the search provider.
func TestModelInfoParsesWebSearchProviderLikeRust(t *testing.T) {
	var info ModelInfo
	if err := json.Unmarshal([]byte(`{"slug":"m","display_name":"M","web_search_provider":"search_mcp"}`), &info); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if info.WebSearchProvider == nil || *info.WebSearchProvider != "search_mcp" {
		t.Fatalf("WebSearchProvider = %v, want search_mcp", info.WebSearchProvider)
	}
	var absent ModelInfo
	if err := json.Unmarshal([]byte(`{"slug":"m2","display_name":"M2"}`), &absent); err != nil {
		t.Fatalf("unmarshal absent: %v", err)
	}
	if absent.WebSearchProvider != nil {
		t.Fatalf("absent WebSearchProvider = %v, want nil", *absent.WebSearchProvider)
	}
	withStandalone := `{"slug":"m3","display_name":"M3","web_search_provider":"standalone"}`
	var standalone ModelInfo
	if err := json.Unmarshal([]byte(withStandalone), &standalone); err != nil {
		t.Fatalf("unmarshal standalone: %v", err)
	}
	if standalone.WebSearchProvider == nil || *standalone.WebSearchProvider != "standalone" {
		t.Fatalf("standalone WebSearchProvider = %v", standalone.WebSearchProvider)
	}
}

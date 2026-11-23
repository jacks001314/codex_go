package model

import (
	"encoding/json"
	"strings"
	"testing"
)

// Rust #62808 removed ModelInfo.web_search_provider together with
// model-selected Search MCP routing. Older catalogs that still carry the
// member must keep parsing, and the field must not resurface on output.
func TestModelInfoIgnoresRemovedWebSearchProviderLikeRust(t *testing.T) {
	raw := `{"slug":"m","display_name":"M","web_search_provider":"search_mcp","truncation_policy":{"mode":"bytes","limit":10000}}`
	var info ModelInfo
	if err := json.Unmarshal([]byte(raw), &info); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if info.Slug != "m" {
		t.Fatalf("Slug = %q", info.Slug)
	}
	encoded, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if strings.Contains(string(encoded), "web_search_provider") {
		t.Fatalf("model info re-serialized the removed field: %s", encoded)
	}
}

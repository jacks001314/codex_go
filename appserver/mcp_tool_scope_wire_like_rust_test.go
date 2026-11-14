package appserver

import (
	"encoding/json"
	"strings"
	"testing"
)

// Rust #61257 includes the MCP registration scope on the v2 mcpToolCall item
// and omits it for a local registration.
func TestMCPToolCallThreadItemSerializesEnvironmentScopeLikeRust(t *testing.T) {
	item := &ThreadItem{
		ID:   "call-1",
		Type: "mcpToolCall",
		Data: map[string]any{"server": "notes", "tool": "lookup", "status": "completed", "environmentId": "env-b"},
	}
	encoded, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"environmentId":"env-b"`) {
		t.Fatalf("wire item = %s", encoded)
	}

	local := &ThreadItem{ID: "call-2", Type: "mcpToolCall", Data: map[string]any{"server": "notes", "tool": "lookup"}}
	encoded, err = json.Marshal(local)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "environmentId") {
		t.Fatalf("local wire item gained an environment scope: %s", encoded)
	}
}

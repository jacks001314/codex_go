package appserver

import (
	"encoding/json"
	"testing"

	"codex_go/mcp"
)

// Mirrors Rust #46952: mcpServer/elicitation/request carries the optional
// _meta object for an openai/userVerification elicitation.
func TestUserVerificationElicitationForwardsMetaLikeRust(t *testing.T) {
	request := &mcp.MCPElicitationRequest{
		ServerName:  "verifier",
		Method:      mcp.UserVerificationMode,
		Title:       "Verify action",
		Description: "Verify the requested operation",
		Challenge:   "AQID",
		Meta:        map[string]any{"example/display": map[string]any{"label": "Operation"}},
	}
	params := appserverMCPElicitationParams(request)
	if params.Mode != mcp.UserVerificationMode {
		t.Fatalf("mode = %q, want %q", params.Mode, mcp.UserVerificationMode)
	}
	meta, ok := params.Meta.(map[string]any)
	if !ok {
		t.Fatalf("forwarded meta = %#v, want an object", params.Meta)
	}
	display, ok := meta["example/display"].(map[string]any)
	if !ok || display["label"] != "Operation" {
		t.Fatalf("forwarded meta = %#v, want example/display.label", meta)
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatal(err)
	}
	if _, ok := payload["_meta"]; !ok {
		t.Fatalf("elicitation request omitted _meta: %s", string(encoded))
	}
}

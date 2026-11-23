package model

import (
	"testing"
)

// Rust #62589 removed the cumulative `mcp_attribution` document from Responses
// request metadata: client metadata now carries only the caller's members.
func TestResponsesClientMetadataOmitsMCPAttributionLikeRust(t *testing.T) {
	request := &AgentRequest{ClientMetadata: map[string]string{
		"x-codex-turn-metadata": `{"request_kind":"turn"}`,
	}}
	metadata := responsesClientMetadataForRequest(request)
	if got := metadata["x-codex-turn-metadata"]; got != `{"request_kind":"turn"}` {
		t.Fatalf("client metadata = %#v", metadata)
	}
	if _, ok := metadata["mcp_attribution"]; ok {
		t.Fatalf("client metadata kept mcp_attribution: %#v", metadata)
	}

	if got := responsesClientMetadataForRequest(nil); got != nil {
		t.Fatalf("nil request metadata = %#v, want nil", got)
	}

	// The copy must not alias the request's map.
	metadata["extra"] = "value"
	if _, ok := request.ClientMetadata["extra"]; ok {
		t.Fatal("client metadata copy aliased the request map")
	}
}

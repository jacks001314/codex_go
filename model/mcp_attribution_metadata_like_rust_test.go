package model

import (
	"encoding/json"
	"strings"
	"testing"

	"codex_go/retainedctx"
)

// Mirrors Rust #47899's Responses-metadata coverage: the cumulative MCP
// attribution travels as a body-only `mcp_attribution` client-metadata member,
// only for providers granted internal metadata, with explicit diagnostics when
// the document cannot be serialized within Rust's 16 KiB bound.
func TestMCPAttributionClientMetadataLikeRust(t *testing.T) {
	reason := retainedctx.McpAttributionErrorHistoryMissingCheckpoint
	attribution := &retainedctx.McpAttribution{
		Status:      retainedctx.McpAttributionStatusAttributionError,
		ErrorReason: &reason,
		Sources: []retainedctx.McpAttributionSource{{
			ServerName:  "example",
			ToolName:    "search",
			FirstTurnID: "turn_1",
		}},
	}
	firstParty := &ResponsesAgentRunner{Provider: &APIProvider{BaseURL: "https://api.openai.com/v1"}}
	metadata := responsesClientMetadataForRequest(&AgentRequest{MCPAttribution: attribution}, firstParty)
	raw := metadata[McpAttributionClientMetadataKey]
	var document map[string]any
	if err := json.Unmarshal([]byte(raw), &document); err != nil {
		t.Fatalf("mcp_attribution = %q: %v", raw, err)
	}
	if document["status"] != "attribution_error" || document["error_reason"] != "history_missing_checkpoint" {
		t.Fatalf("mcp_attribution = %#v", document)
	}
	sources, ok := document["sources"].([]any)
	if !ok || len(sources) != 1 {
		t.Fatalf("mcp_attribution sources = %#v", document["sources"])
	}

	// Rust emits the snapshot even when no source has been recorded yet.
	empty := responsesClientMetadataForRequest(&AgentRequest{MCPAttribution: &retainedctx.McpAttribution{Status: retainedctx.McpAttributionStatusNone}}, firstParty)
	if got := empty[McpAttributionClientMetadataKey]; got != `{"status":"none"}` {
		t.Fatalf("empty snapshot = %q", got)
	}

	// A provider grant opts an untrusted endpoint in; without it the document
	// never leaves the process.
	granted := &ResponsesAgentRunner{Provider: &APIProvider{BaseURL: "https://proxy.example.test/v1", IncludeInternalMetadata: true}}
	if got := responsesClientMetadataForRequest(&AgentRequest{MCPAttribution: attribution}, granted)[McpAttributionClientMetadataKey]; got == "" {
		t.Fatal("provider grant must forward MCP attribution")
	}
	plain := &ResponsesAgentRunner{Provider: &APIProvider{BaseURL: "https://proxy.example.test/v1"}}
	if got := responsesClientMetadataForRequest(&AgentRequest{MCPAttribution: attribution}, plain)[McpAttributionClientMetadataKey]; got != "" {
		t.Fatalf("untrusted provider received MCP attribution: %q", got)
	}

	// Rust's bounded serializer reports `payload_too_large` instead of failing
	// the request or sending a truncated document.
	oversized := &retainedctx.McpAttribution{
		Status:  retainedctx.McpAttributionStatusComplete,
		Sources: []retainedctx.McpAttributionSource{{ServerName: "example", ToolName: strings.Repeat("a", MaxMcpAttributionBytes), FirstTurnID: "turn_1"}},
	}
	if got := responsesClientMetadataForRequest(&AgentRequest{MCPAttribution: oversized}, firstParty)[McpAttributionClientMetadataKey]; got != `{"status":"attribution_error","error_reason":"payload_too_large"}` {
		t.Fatalf("oversized snapshot = %q", got)
	}
}

// The attribution member must ride in the serialized HTTP/WebSocket body, not
// in a compatibility header.
func TestMCPAttributionRidesInResponsesBodyLikeRust(t *testing.T) {
	apiRequest := &responsesAgentRequest{
		Model:          "gpt-5",
		ClientMetadata: responsesClientMetadataForRequest(&AgentRequest{MCPAttribution: &retainedctx.McpAttribution{Status: retainedctx.McpAttributionStatusNone}}, &ResponsesAgentRunner{Provider: &APIProvider{BaseURL: "https://api.openai.com/v1"}}),
	}
	body, err := json.Marshal(apiRequest)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	var decoded struct {
		ClientMetadata map[string]string `json:"client_metadata"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got := decoded.ClientMetadata[McpAttributionClientMetadataKey]; got != `{"status":"none"}` {
		t.Fatalf("body mcp_attribution = %q", got)
	}
}

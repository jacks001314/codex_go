package mcp

import (
	"context"
	"encoding/json"
	"testing"
)

func TestElicitationSchemaNormalizeAndValidate(t *testing.T) {
	schema := (&McpElicitationSchema{
		Properties: map[string]McpElicitationPrimitiveSchema{
			" title ": {Title: "Title"},
		},
		Required: []string{"title"},
	}).Normalize()
	if schema.Type != McpElicitationTypeObject {
		t.Fatalf("Type = %q", schema.Type)
	}
	if schema.Properties["title"].Type != "string" {
		t.Fatalf("property = %#v", schema.Properties["title"])
	}
	if err := schema.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestElicitationSchemaRejectsUndeclaredRequiredProperty(t *testing.T) {
	err := (&McpElicitationSchema{
		Properties: map[string]McpElicitationPrimitiveSchema{"name": {Type: "string"}},
		Required:   []string{"missing"},
	}).Validate()
	if err == nil {
		t.Fatalf("expected validation error")
	}
}

// The OpenAI elicitation arrives on the umbrella `openai/elicitation/create`
// method with its kind in the payload: the form mode elicits, while a mode this
// client cannot serve falls through to the default handler (Rust's
// capability-gated rmcp-client arms).
func TestMCPClientDispatchesOpenAIElicitationByModeLikeRust(t *testing.T) {
	ctx := context.Background()
	var seen *MCPElicitationRequest
	handler := MCPElicitationHandlerFunc(func(_ context.Context, request *MCPElicitationRequest) (*MCPElicitationResponse, error) {
		seen = request
		return &MCPElicitationResponse{Action: MCPElicitationActionAccept, Content: map[string]any{}}, nil
	})

	result, rpcErr := mcpClientRequestResult(ctx, "docs", handler, "openai/elicitation/create", json.RawMessage(`1`),
		json.RawMessage(`{"mode":"form","message":"Approve?","requestedSchema":{"type":"object"}}`))
	if rpcErr != nil {
		t.Fatalf("form mode error = %+v", rpcErr)
	}
	if result == nil || seen == nil || seen.Mode != "form" || seen.Message != "Approve?" {
		t.Fatalf("result = %#v request = %#v", result, seen)
	}

	// A verification mode is not a capability this client advertises, so the
	// method falls through like Rust's unmatched arm.
	if _, rpcErr := mcpClientRequestResult(ctx, "docs", handler, "openai/elicitation/create", json.RawMessage(`2`),
		json.RawMessage(`{"mode":"openai/userVerification","title":"Confirm","description":"Sign","challenge":"Y2g"}`)); rpcErr == nil ||
		rpcErr.Code != -32601 {
		t.Fatalf("verification mode error = %+v", rpcErr)
	}
}

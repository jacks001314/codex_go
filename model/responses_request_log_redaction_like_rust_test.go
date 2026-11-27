package model

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

// Rust #64003: callers can omit an encoded JSON body from diagnostics while
// preserving the exact wire payload and reporting its byte length.
func TestResponseRequestBodyLogRedactionLikeRust(t *testing.T) {
	apiRequest := &responsesAgentRequest{
		Model: "gpt-test",
		Input: []any{map[string]any{"type": "message", "content": "body-marker"}},
	}
	request := &AgentRequest{ThreadID: "thread-1", TurnID: "turn-1", OmitRequestBodyFromLogs: true}
	fields := responsesRequestDiagnosticFields(request, apiRequest)
	if got := fields["request_body"]; got != "<redacted>" {
		t.Fatalf("request_body = %#v, want <redacted>", got)
	}
	byteLen, ok := fields["request_body_bytes"].(int)
	if !ok || byteLen == 0 {
		t.Fatalf("request_body_bytes = %#v, want the encoded payload length", fields["request_body_bytes"])
	}

	plain := responsesRequestDiagnosticFields(&AgentRequest{}, apiRequest)
	encoded, ok := plain["request_body"].(json.RawMessage)
	if !ok || !strings.Contains(string(encoded), "body-marker") {
		t.Fatalf("unredacted request_body = %#v, want encoded payload", plain["request_body"])
	}

	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{
		Provider: &APIProvider{Name: OpenAIProviderName, BaseURL: "https://example.test/v1"},
	})
	httpRequest, err := runner.newResponsesHTTPRequest(context.Background(), request, apiRequest, "application/json")
	if err != nil {
		t.Fatalf("newResponsesHTTPRequest() error = %v", err)
	}
	wireBody, err := io.ReadAll(httpRequest.Body)
	if err != nil {
		t.Fatalf("ReadAll(wire body) error = %v", err)
	}
	if !strings.Contains(string(wireBody), "body-marker") {
		t.Fatalf("wire body was redacted: %s", wireBody)
	}
	if byteLen != len(wireBody) && len(apiRequest.ToolResults) == 0 {
		t.Fatalf("reported body bytes = %d, wire body = %d", byteLen, len(wireBody))
	}
}

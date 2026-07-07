package appserver

import (
	"encoding/json"
	"fmt"
	"testing"

	"codex_go/internal/mcp"
)

func TestJSONRPCErrorDataIncludesMCPRemoteErrorDetails(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", &mcp.MCPRemoteError{
		Method:  "tools/call",
		Code:    -32001,
		Message: "remote denied request",
		Data:    json.RawMessage(`{"reason":"policy","retry":false}`),
	})

	data := jsonRPCErrorData(err)
	if data["type"] != "mcp_remote_error" || data["method"] != "tools/call" || data["message"] != "remote denied request" {
		t.Fatalf("error data = %#v", data)
	}
	if code, ok := data["code"].(int64); !ok || code != -32001 {
		t.Fatalf("error data code = %#v, want -32001", data["code"])
	}
	payload, ok := data["data"].(map[string]any)
	if !ok {
		t.Fatalf("error data payload = %#v, want object", data["data"])
	}
	if payload["reason"] != "policy" || payload["retry"] != false {
		t.Fatalf("error data payload = %#v", payload)
	}
	if code := runtimeErrorCode(err); code != -32001 {
		t.Fatalf("runtimeErrorCode() = %d, want -32001", code)
	}
}
